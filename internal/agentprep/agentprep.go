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
	"path/filepath"
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
	RunID       string      `json:"run_id"`
	ReadyPath   string      `json:"ready_path"`
	Project     ProjectInfo `json:"project"`
	Base        string      `json:"base"`
	Profile     string      `json:"profile"`
	Diagnostics Diagnostics `json:"diagnostics"`
	CreatedAt   string      `json:"created_at"`
}

type ProjectInfo struct {
	Alias      string `json:"alias"`
	Remote     string `json:"remote"`
	CloneURL   string `json:"clone_url"`
	SourcePath string `json:"source_path"`
	LocalPath  string `json:"local_path"`
	ProjectID  int64  `json:"project_id,omitempty"`
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

	cloneURL := project.CloneURL
	if cloneURL == "" {
		cloneURL = project.NormalizedRemote
	}
	if err := gitClone(ctx, cloneURL, base, readyPath); err != nil {
		return Result{}, err
	}

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
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "fetch-failed", Message: err.Error()})
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
