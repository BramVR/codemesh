package agentprep

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/policy"
	"github.com/BramVR/codemesh/internal/registry"
	"github.com/BramVR/codemesh/internal/state"
)

const MetadataFileName = "codemesh-run.json"

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	RecordAgentRun(context.Context, state.AgentRun) error
}

type Preparer struct {
	Store     Store
	AgentsDir string
	NewID     func() string
	Now       func() time.Time
}

type Request struct {
	Project string
	Base    string
	Profile string
}

type Result struct {
	RunID       string
	ReadyPath   string
	Base        string
	Profile     string
	Diagnostics Diagnostics
	Metadata    Metadata
}

type Metadata struct {
	RunID       string       `json:"run_id"`
	ReadyPath   string       `json:"ready_path"`
	Project     ProjectInfo  `json:"project"`
	Base        string       `json:"base"`
	Profile     string       `json:"profile"`
	HandoffDocs []HandoffDoc `json:"handoff_docs"`
	Diagnostics Diagnostics  `json:"diagnostics"`
	CreatedAt   string       `json:"created_at"`
}

type ProjectInfo struct {
	Alias      string `json:"alias"`
	Remote     string `json:"remote"`
	CloneURL   string `json:"clone_url"`
	SourcePath string `json:"source_path"`
	LocalPath  string `json:"local_path"`
	ProjectID  int64  `json:"project_id,omitempty"`
}

type HandoffDoc struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Pattern string `json:"pattern,omitempty"`
}

type Diagnostics struct {
	Warnings []Diagnostic `json:"warnings"`
	Blockers []Diagnostic `json:"blockers"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BlockedError struct {
	Diagnostics Diagnostics
}

func (e BlockedError) Error() string {
	return "agent workspace prep blocked by readiness diagnostics"
}

func (p Preparer) Prepare(ctx context.Context, req Request) (Result, error) {
	if p.Store == nil {
		return Result{}, errors.New("agent prep store is required")
	}
	if strings.TrimSpace(p.AgentsDir) == "" {
		return Result{}, errors.New("agents directory is required")
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return Result{}, errors.New("project name is required")
	}
	project, err := p.lookupProject(ctx, projectName)
	if err != nil {
		return Result{}, err
	}

	pathDiagnostics, err := evaluateSourcePath(project)
	if err != nil {
		return Result{}, err
	}
	if len(pathDiagnostics.Blockers) != 0 {
		return Result{
			Profile:     strings.TrimSpace(req.Profile),
			Diagnostics: pathDiagnostics,
		}, BlockedError{Diagnostics: pathDiagnostics}
	}
	base, err := p.resolveBase(project, req.Base)
	if err != nil {
		return Result{}, err
	}
	diagnostics, err := evaluateForPrep(ctx, project, base)
	if err != nil {
		return Result{}, err
	}
	if len(diagnostics.Blockers) != 0 {
		return Result{
			Base:        base,
			Profile:     strings.TrimSpace(req.Profile),
			Diagnostics: diagnostics,
		}, BlockedError{Diagnostics: diagnostics}
	}

	runID := p.newID()
	runDir := filepath.Join(p.AgentsDir, runID)
	readyPath := filepath.Join(runDir, "workspace")
	if err := os.MkdirAll(p.AgentsDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create agents directory: %w", err)
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create agent run directory: %w", err)
	}
	runRecorded := false
	defer func() {
		if !runRecorded {
			_ = os.RemoveAll(runDir)
		}
	}()

	cloneURL := project.CloneURL
	if cloneURL == "" {
		cloneURL = project.NormalizedRemote
	}
	if err := gitClone(ctx, cloneURL, base, readyPath); err != nil {
		return Result{}, err
	}
	readyPolicy, err := policy.Resolve(readyPath)
	if err != nil {
		return Result{}, err
	}
	handoffDocs, handoffWarnings, err := handoffDocs(readyPath, readyPolicy.IncludeDocs)
	if err != nil {
		return Result{}, err
	}
	diagnostics.Warnings = append(diagnostics.Warnings, handoffWarnings...)

	now := p.now().UTC()
	metadata := Metadata{
		RunID:     runID,
		ReadyPath: readyPath,
		Project: ProjectInfo{
			Alias:      project.Alias,
			Remote:     project.NormalizedRemote,
			CloneURL:   redactedCloneURLForMetadata(cloneURL),
			SourcePath: project.LocalPath,
			LocalPath:  project.LocalPath,
			ProjectID:  project.ID,
		},
		Base:        base,
		Profile:     strings.TrimSpace(req.Profile),
		HandoffDocs: handoffDocs,
		Diagnostics: diagnostics,
		CreatedAt:   now.Format(time.RFC3339),
	}
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encode agent run metadata: %w", err)
	}
	metadataJSON = append(metadataJSON, '\n')
	if err := writeNewFile(filepath.Join(readyPath, MetadataFileName), metadataJSON, 0o600); err != nil {
		return Result{}, fmt.Errorf("write agent run metadata: %w", err)
	}
	if err := p.Store.RecordAgentRun(ctx, state.AgentRun{
		ID:            runID,
		ProjectID:     project.ID,
		WorkspacePath: readyPath,
		MetadataJSON:  string(metadataJSON),
		CreatedAt:     now,
	}); err != nil {
		return Result{}, err
	}
	runRecorded = true

	return Result{
		RunID:       runID,
		ReadyPath:   readyPath,
		Base:        base,
		Profile:     strings.TrimSpace(req.Profile),
		Diagnostics: diagnostics,
		Metadata:    metadata,
	}, nil
}

func (p Preparer) lookupProject(ctx context.Context, alias string) (state.Project, error) {
	projects, err := p.Store.ListProjects(ctx)
	if err != nil {
		return state.Project{}, err
	}
	for _, project := range projects {
		if project.Alias == alias {
			return project, nil
		}
	}
	return state.Project{}, fmt.Errorf("unknown project: %s", alias)
}

func (p Preparer) resolveBase(project state.Project, requestedBase string) (string, error) {
	if base := strings.TrimSpace(requestedBase); base != "" {
		return base, nil
	}
	projectPolicy, err := policy.Resolve(project.LocalPath)
	if err != nil {
		return "", err
	}
	return projectPolicy.BaseBranch, nil
}

func evaluateSourcePath(project state.Project) (Diagnostics, error) {
	var diagnostics Diagnostics
	info, err := os.Stat(project.LocalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{
				Code:    "missing-path",
				Message: fmt.Sprintf("local path does not exist: %s", project.LocalPath),
			})
			return diagnostics, nil
		}
		return Diagnostics{}, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}
	if !info.IsDir() {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{
			Code:    "path-not-directory",
			Message: fmt.Sprintf("local path is not a directory: %s", project.LocalPath),
		})
	}
	return diagnostics, nil
}

func (p Preparer) newID() string {
	if p.NewID != nil {
		return p.NewID()
	}
	var buf [12]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "run-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("run-%d", p.now().UnixNano())
}

func (p Preparer) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func evaluateForPrep(ctx context.Context, project state.Project, base string) (Diagnostics, error) {
	var diagnostics Diagnostics
	pathDiagnostics, err := evaluateSourcePath(project)
	if err != nil {
		return Diagnostics{}, err
	}
	if len(pathDiagnostics.Blockers) != 0 {
		diagnostics.Blockers = append(diagnostics.Blockers, pathDiagnostics.Blockers...)
		return diagnostics, nil
	}

	if dirty, err := gitOutput(ctx, project.LocalPath, "status", "--porcelain", "--untracked-files=all"); err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "git-status-failed", Message: err.Error()})
		return diagnostics, nil
	} else if strings.TrimSpace(dirty) != "" {
		diagnostics.Warnings = append(diagnostics.Warnings, Diagnostic{
			Code:    "dirty-checkout",
			Message: "source checkout has uncommitted or untracked changes",
		})
	}

	if err := validateBaseBranch(ctx, base); err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "invalid-base", Message: err.Error()})
		return diagnostics, nil
	}
	remote, err := gitOutput(ctx, project.LocalPath, "remote", "get-url", "origin")
	if err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "origin-missing", Message: err.Error()})
		return diagnostics, nil
	}
	normalized, err := registry.NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath)
	if err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "origin-unsupported", Message: err.Error()})
		return diagnostics, nil
	}
	if normalized != project.NormalizedRemote {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{
			Code:    "remote-mismatch",
			Message: fmt.Sprintf("origin remote %q does not match registered remote %q", normalized, project.NormalizedRemote),
		})
		return diagnostics, nil
	}
	if _, err := gitOutput(ctx, project.LocalPath, "fetch", "--quiet", "origin", "refs/heads/"+base); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{
				Code:    "missing-base",
				Message: fmt.Sprintf("base branch %q was not found on origin", base),
			})
			return diagnostics, nil
		}
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "fetch-failed", Message: redactedCloneOutput(err.Error(), strings.TrimSpace(remote))})
		return diagnostics, nil
	}
	basePolicy, err := policyFromFetchedBase(ctx, project.LocalPath, base)
	if err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "invalid-policy", Message: err.Error()})
		return diagnostics, nil
	}
	addEnvDiagnostics(project.LocalPath, basePolicy, &diagnostics)
	return diagnostics, nil
}

func policyFromFetchedBase(ctx context.Context, repo, base string) (policy.Policy, error) {
	data, err := gitOutput(ctx, repo, "show", "FETCH_HEAD:"+policy.FileName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return policy.Defaults(), nil
		}
		return policy.Policy{}, err
	}
	return policy.ParseBytes("origin/"+base+":"+policy.FileName, []byte(data))
}

func addEnvDiagnostics(sourcePath string, projectPolicy policy.Policy, diagnostics *Diagnostics) {
	var missing []Diagnostic
	for _, requiredFile := range projectPolicy.Env.RequiredFiles {
		info, err := os.Stat(filepath.Join(sourcePath, requiredFile))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, Diagnostic{
					Code:    "missing-env-file",
					Message: fmt.Sprintf("required env file is missing: %s", requiredFile),
				})
				continue
			}
			missing = append(missing, Diagnostic{
				Code:    "missing-env-file",
				Message: fmt.Sprintf("required env file cannot be checked: %s: %v", requiredFile, err),
			})
			continue
		}
		if !info.Mode().IsRegular() {
			missing = append(missing, Diagnostic{
				Code:    "invalid-env-file",
				Message: fmt.Sprintf("required env file is not a regular file: %s", requiredFile),
			})
		}
	}
	for _, key := range projectPolicy.Env.RequiredKeys {
		if _, ok := os.LookupEnv(key); !ok {
			missing = append(missing, Diagnostic{
				Code:    "missing-env-key",
				Message: fmt.Sprintf("required env key is missing: %s", key),
			})
		}
	}
	if len(missing) == 0 {
		return
	}
	if projectPolicy.Env.Mode == policy.EnvModeBlock {
		diagnostics.Blockers = append(diagnostics.Blockers, missing...)
		return
	}
	diagnostics.Warnings = append(diagnostics.Warnings, missing...)
}

func handoffDocs(root string, policyPatterns []string) ([]HandoffDoc, []Diagnostic, error) {
	docs, err := defaultHandoffDocs(root)
	if err != nil {
		return nil, nil, err
	}
	return appendPolicyHandoffDocs(root, docs, policyPatterns)
}

func defaultHandoffDocs(root string) ([]HandoffDoc, error) {
	docs := make([]HandoffDoc, 0)
	for _, path := range []string{"AGENTS.md", "CONTEXT.md", "README.md"} {
		ok, err := handoffDocExists(root, path)
		if err != nil {
			return nil, err
		}
		if ok {
			docs = append(docs, HandoffDoc{Path: path, Source: "default"})
		}
	}
	adrDir := filepath.Join(root, "docs", "adr")
	info, err := os.Lstat(adrDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return docs, nil
		}
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return docs, nil
	}
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		rel := filepath.Join("docs", "adr", entry.Name())
		ok, err := handoffDocExists(root, rel)
		if err != nil {
			return nil, err
		}
		if ok {
			path := filepath.ToSlash(rel)
			docs = append(docs, HandoffDoc{Path: path, Source: "default"})
		}
	}
	return docs, nil
}

func appendPolicyHandoffDocs(root string, docs []HandoffDoc, patterns []string) ([]HandoffDoc, []Diagnostic, error) {
	seen := make(map[string]bool, len(docs))
	for _, doc := range docs {
		seen[doc.Path] = true
	}
	var warnings []Diagnostic
	for _, pattern := range patterns {
		matches, err := policyHandoffDocMatches(root, pattern)
		if err != nil {
			return nil, nil, err
		}
		found := false
		for _, rel := range matches {
			if seen[rel] {
				found = true
				continue
			}
			ok, err := handoffDocExists(root, rel)
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			found = true
			docs = append(docs, HandoffDoc{Path: rel, Source: "policy", Pattern: pattern})
			seen[rel] = true
		}
		if !found && (len(matches) != 0 || validPolicyHandoffPattern(pattern)) {
			warnings = append(warnings, Diagnostic{
				Code:    "handoff-doc-missing",
				Message: fmt.Sprintf("policy handoff doc matched no files: %s", pattern),
			})
		}
	}
	return docs, warnings, nil
}

func validPolicyHandoffPattern(pattern string) bool {
	clean, ok := cleanHandoffRel(pattern)
	if !ok {
		return false
	}
	return !strings.ContainsAny(clean, "*?[") || validHandoffGlob(clean)
}

func policyHandoffDocMatches(root, pattern string) ([]string, error) {
	clean, ok := cleanHandoffRel(pattern)
	if !ok {
		return nil, nil
	}
	if !strings.ContainsAny(clean, "*?[") {
		return []string{clean}, nil
	}
	if !validHandoffGlob(clean) {
		return nil, nil
	}
	var matches []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		cleanRel, ok := cleanHandoffRel(rel)
		if !ok {
			return nil
		}
		if matchHandoffGlob(clean, cleanRel) {
			matches = append(matches, cleanRel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func validHandoffGlob(pattern string) bool {
	for _, segment := range strings.Split(pattern, "/") {
		if segment == "**" {
			continue
		}
		if _, err := path.Match(segment, ""); err != nil {
			return false
		}
	}
	return true
}

func matchHandoffGlob(pattern, rel string) bool {
	patternParts := strings.Split(pattern, "/")
	relParts := strings.Split(rel, "/")
	var match func(int, int) bool
	match = func(patternIndex, relIndex int) bool {
		if patternIndex == len(patternParts) {
			return relIndex == len(relParts)
		}
		if patternParts[patternIndex] == "**" {
			for nextRelIndex := relIndex; nextRelIndex <= len(relParts); nextRelIndex++ {
				if match(patternIndex+1, nextRelIndex) {
					return true
				}
			}
			return false
		}
		if relIndex == len(relParts) {
			return false
		}
		ok, err := path.Match(patternParts[patternIndex], relParts[relIndex])
		if err != nil || !ok {
			return false
		}
		return match(patternIndex+1, relIndex+1)
	}
	return match(0, 0)
}

func cleanHandoffRel(rel string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	slashPath := filepath.ToSlash(clean)
	for _, part := range strings.Split(slashPath, "/") {
		if part == ".git" {
			return "", false
		}
	}
	return slashPath, true
}

func handoffDocExists(root, rel string) (bool, error) {
	clean, ok := cleanHandoffRel(rel)
	if !ok {
		return false, nil
	}
	parts := strings.Split(clean, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("check handoff doc %q: %w", clean, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return false, nil
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return false, nil
			}
			continue
		}
		return info.Mode().IsRegular(), nil
	}
	return false, nil
}

func gitClone(ctx context.Context, cloneURL, base, readyPath string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--branch", base, "--single-branch", cloneURL, readyPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("clone agent workspace: %s", redactedCloneOutput(detail, cloneURL))
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}
	return string(output), nil
}

func validateBaseBranch(ctx context.Context, base string) error {
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", base)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("invalid base branch %q: %s", base, detail)
	}
	return nil
}

func redactedCloneURLForMetadata(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return raw
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func redactedCloneOutput(output, cloneURL string) string {
	redacted := redactedCloneURLForMetadata(cloneURL)
	if redacted != cloneURL {
		output = strings.ReplaceAll(output, cloneURL, redacted)
		output = strings.ReplaceAll(output, cloneURL+"/", redacted+"/")
	}
	parsed, err := url.Parse(cloneURL)
	if err != nil || parsed.Scheme == "" {
		return output
	}
	candidates := []string{}
	withoutUser := *parsed
	withoutUser.User = nil
	candidates = append(candidates, withoutUser.String())
	withoutFragment := *parsed
	withoutFragment.Fragment = ""
	candidates = append(candidates, withoutFragment.String())
	withoutUserFragment := withoutUser
	withoutUserFragment.Fragment = ""
	candidates = append(candidates, withoutUserFragment.String())
	withoutQuery := withoutFragment
	withoutQuery.RawQuery = ""
	candidates = append(candidates, withoutQuery.String())
	for _, candidate := range candidates {
		if candidate != "" && candidate != redacted {
			output = strings.ReplaceAll(output, candidate, redacted)
			output = strings.ReplaceAll(output, candidate+"/", redacted+"/")
		}
	}
	return output
}

func writeNewFile(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
