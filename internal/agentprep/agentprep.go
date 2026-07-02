package agentprep

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/agentcontract"
	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/envbinding"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/readiness"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/toolchain"
)

const MetadataFileName = agentcontract.FileName

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	RecordAgentRun(context.Context, state.AgentRun) error
	ListEnvBindings(context.Context, int64) ([]state.EnvBinding, error)
}

type Preparer struct {
	Store     Store
	AgentsDir string
	NewID     func() string
	Now       func() time.Time
	Producer  agentcontract.Producer
	Toolchain toolchain.Detector
}

type Request struct {
	Project          string
	Base             string
	Profile          string
	EnvProvider      string
	AllowedEnvScopes []string
	CloneOptions     clonestrategy.Options
}

type Result struct {
	RunID          string
	ReadyPath      string
	Base           string
	Profile        string
	ResolvedCommit string
	BaseProvenance agentcontract.BaseProvenance
	CloneStrategy  clonestrategy.Selection
	Diagnostics    Diagnostics
	Metadata       Metadata
}

type Metadata = agentcontract.Contract
type ProjectInfo = agentcontract.ProjectInfo
type HandoffDoc = agentcontract.HandoffDoc
type Diagnostics = agentcontract.Diagnostics
type Diagnostic = agentcontract.Diagnostic

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

	readinessOpts := readiness.Options{BaseBranch: req.Base, Toolchain: p.Toolchain}
	if strings.TrimSpace(req.EnvProvider) != "" {
		readinessOpts.Env = materializedEnvLookup{}
	}
	decision, err := readiness.EvaluateHandoff(ctx, project, readinessOpts)
	if err != nil {
		return Result{}, err
	}
	base := decision.Report.BaseBranch
	diagnostics := agentDiagnostics(decision.Report.Warnings, decision.Report.Blockers)
	envSummary := envSummaryFromPolicy(decision.Policy.Env.RequiredFiles, decision.Policy.Env.RequiredKeys, req.AllowedEnvScopes)
	if len(diagnostics.Blockers) != 0 {
		return Result{
			Base:        base,
			Profile:     strings.TrimSpace(req.Profile),
			Diagnostics: diagnostics,
			Metadata:    Metadata{Env: envSummary},
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
	cloneResult, err := cloneWorkspace(ctx, cloneURL, base, readyPath, req.CloneOptions)
	if err != nil {
		return Result{
			Base:          base,
			Profile:       strings.TrimSpace(req.Profile),
			CloneStrategy: cloneResult.Strategy,
			Diagnostics:   diagnostics,
			Metadata:      Metadata{Env: envSummary},
		}, err
	}
	resolvedCommit, err := gitResolvedCommit(ctx, readyPath)
	if err != nil {
		return Result{}, err
	}
	baseProvenance := agentcontract.BaseProvenance{
		Base:           base,
		ResolvedCommit: resolvedCommit,
		Remote:         project.NormalizedRemote,
		FetchedBase:    decision.Report.FetchedBase,
		FetchedCommit:  decision.Report.FetchedCommit,
		PreparedHEAD:   resolvedCommit,
		MatchesFetched: decision.Report.FetchedCommit != "" && decision.Report.FetchedCommit == resolvedCommit,
	}
	readyPolicy, err := readiness.PolicyFromCheckout(ctx, readyPath)
	if err != nil {
		diagnostics.Blockers = append(diagnostics.Blockers, Diagnostic{Code: "invalid-policy", Message: err.Error()})
		return Result{
			Base:          base,
			Profile:       strings.TrimSpace(req.Profile),
			CloneStrategy: cloneResult.Strategy,
			Diagnostics:   diagnostics,
		}, BlockedError{Diagnostics: diagnostics}
	}
	handoffDocs, handoffWarnings, err := handoffDocs(readyPath, readyPolicy.IncludeDocs)
	if err != nil {
		return Result{}, err
	}
	diagnostics.Warnings = append(diagnostics.Warnings, handoffWarnings...)
	envSummary = envSummaryFromPolicy(readyPolicy.Env.RequiredFiles, readyPolicy.Env.RequiredKeys, req.AllowedEnvScopes)
	if strings.TrimSpace(req.EnvProvider) != "" {
		envResult, envDiagnostics, err := envbinding.Materializer{Store: p.Store}.Materialize(ctx, envbinding.Request{
			ProjectID:     project.ID,
			RequiredFiles: readyPolicy.Env.RequiredFiles,
			RequiredKeys:  readyPolicy.Env.RequiredKeys,
			Provider:      strings.TrimSpace(req.EnvProvider),
			AllowedScopes: req.AllowedEnvScopes,
			BundlePath:    filepath.Join(runDir, "env", "env.bundle"),
		})
		if err != nil {
			return Result{}, err
		}
		envSummary = envSummaryFromBinding(envResult)
		if len(envDiagnostics) != 0 {
			diagnostics.Blockers = append(diagnostics.Blockers, envDiagnosticList(envDiagnostics)...)
			return Result{
				Base:          base,
				Profile:       strings.TrimSpace(req.Profile),
				CloneStrategy: cloneResult.Strategy,
				Diagnostics:   diagnostics,
				Metadata:      Metadata{Env: envSummary},
			}, BlockedError{Diagnostics: diagnostics}
		}
	}

	now := p.now().UTC()
	metadata := agentcontract.New(agentcontract.Input{
		Producer:  p.producer(),
		RunID:     runID,
		ReadyPath: readyPath,
		Project: agentcontract.ProjectInput{
			Alias:             project.Alias,
			Remote:            project.NormalizedRemote,
			CloneURL:          cloneURL,
			SourcePath:        project.LocalPath,
			LocalPath:         project.LocalPath,
			SourcePathMissing: decision.SourcePathMissing,
			ProjectID:         project.ID,
		},
		Base:              base,
		Profile:           strings.TrimSpace(req.Profile),
		ResolvedCommit:    resolvedCommit,
		BaseProvenance:    baseProvenance,
		CloneStrategy:     cloneResult.Strategy,
		Env:               envSummary,
		Toolchain:         decision.Report.Toolchain,
		ReadinessDecision: "ready",
		HandoffDocs:       handoffDocs,
		Diagnostics:       diagnostics,
		CreatedAt:         now,
	})
	metadataJSON, err := agentcontract.WriteNew(readyPath, metadata)
	if err != nil {
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
		RunID:          runID,
		ReadyPath:      readyPath,
		Base:           base,
		Profile:        strings.TrimSpace(req.Profile),
		ResolvedCommit: resolvedCommit,
		BaseProvenance: baseProvenance,
		CloneStrategy:  cloneResult.Strategy,
		Diagnostics:    diagnostics,
		Metadata:       metadata,
	}, nil
}

type materializedEnvLookup struct{}

func (materializedEnvLookup) HasEnvKey(string) bool {
	return true
}

func agentDiagnostics(warnings, blockers []readiness.Diagnostic) Diagnostics {
	return Diagnostics{
		Warnings: agentDiagnosticList(warnings),
		Blockers: agentDiagnosticList(blockers),
	}
}

func envDiagnosticList(items []envbinding.Diagnostic) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(items))
	for _, item := range items {
		diagnostics = append(diagnostics, Diagnostic{Code: item.Code, Message: item.Message})
	}
	return diagnostics
}

func envSummaryFromPolicy(requiredFiles, requiredKeys, allowedScopes []string) agentcontract.EnvInfo {
	return envSummaryFromBinding(envbinding.SummaryForRequirements(requiredFiles, requiredKeys, allowedScopes))
}

func envSummaryFromBinding(summary envbinding.Summary) agentcontract.EnvInfo {
	requirements := make([]agentcontract.EnvRequirement, 0, len(summary.Requirements))
	for _, requirement := range summary.Requirements {
		requirements = append(requirements, agentcontract.EnvRequirement{
			Name: requirement.Name,
			Kind: requirement.Kind,
		})
	}
	return agentcontract.EnvInfo{
		Requirements:          requirements,
		AllowedScopes:         append([]string(nil), summary.AllowedScopes...),
		MaterializationStatus: summary.MaterializationStatus,
		Bundle: agentcontract.EnvBundle{
			Present: summary.Bundle.Present,
			Path:    summary.Bundle.Path,
			Format:  summary.Bundle.Format,
			Values:  summary.Bundle.Values,
		},
	}
}

func agentDiagnosticList(items []readiness.Diagnostic) []Diagnostic {
	diagnostics := make([]Diagnostic, 0, len(items))
	for _, item := range items {
		diagnostics = append(diagnostics, Diagnostic{Code: item.Code, Message: item.Message})
	}
	return diagnostics
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

func (p Preparer) producer() agentcontract.Producer {
	if p.Producer.Name != "" || p.Producer.Version != "" {
		return p.Producer
	}
	return agentcontract.DefaultProducer("0.0.0-dev")
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
	if _, err := cloneWorkspace(ctx, cloneURL, base, readyPath, clonestrategy.Options{}); err != nil {
		return err
	}
	return nil
}

func cloneWorkspace(ctx context.Context, cloneURL, base, readyPath string, options clonestrategy.Options) (clonestrategy.Result, error) {
	result, err := (clonestrategy.FullClone{}).Clone(ctx, clonestrategy.Request{
		CloneURL:    cloneURL,
		Branch:      base,
		Destination: readyPath,
		Options:     options,
	})
	if err != nil {
		return result, fmt.Errorf("clone agent workspace: %s", err.Error())
	}
	return result, nil
}

func gitResolvedCommit(ctx context.Context, readyPath string) (string, error) {
	commit, err := gitOutput(ctx, readyPath, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve agent workspace commit: %w", err)
	}
	return strings.TrimSpace(commit), nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return gitops.Process().Output(ctx, dir, args...)
}
