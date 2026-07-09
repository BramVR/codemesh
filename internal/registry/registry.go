package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/hydrationexecutor"
	"github.com/BramVR/codemesh/internal/hydrationplanner"
	"github.com/BramVR/codemesh/internal/state"
)

type Store interface {
	AddProject(context.Context, state.Project) (state.Project, error)
	UpsertProject(context.Context, state.Project) (state.Project, state.ProjectUpsertAction, error)
	ListProjects(context.Context) ([]state.Project, error)
}

type projectUpdater interface {
	UpdateProject(context.Context, int64, state.Project) (state.Project, error)
}

type Registry struct {
	store Store
	git   gitops.Client
}

type Entry struct {
	Project state.Project
	State   string
}

type HydrateResult struct {
	Project        state.Project
	AlreadyPresent bool
	CloneStrategy  clonestrategy.Selection
	Plan           hydrationplanner.Plan
	Action         hydrationplanner.Action
}

type PathConflictError struct {
	Path   string
	Reason string
}

type UnknownProjectError struct {
	Alias string
}

func (e UnknownProjectError) Error() string {
	return fmt.Sprintf("unknown project: %s", e.Alias)
}

type UnsafePathError struct {
	Path   string
	Reason string
}

func (e UnsafePathError) Error() string {
	return fmt.Sprintf("unsafe path: %s %s", e.Path, e.Reason)
}

func (e PathConflictError) Error() string {
	return fmt.Sprintf("path conflict: %s %s", e.Path, e.Reason)
}

type ScanResult struct {
	WorkspaceRoot string
	Added         []state.Project
	Updated       []state.Project
	Unchanged     []state.Project
	Skipped       []ScanSkip
}

type ScanSkip struct {
	Path   string
	Reason string
}

func New(store Store) *Registry {
	return &Registry{store: store, git: gitops.Process()}
}

func (r *Registry) AddPath(ctx context.Context, path, alias string) (state.Project, error) {
	inspected, err := r.git.InspectProject(ctx, path)
	if err != nil {
		return state.Project{}, err
	}
	if alias == "" {
		alias = inspected.Alias
	}
	remote, err := NormalizeRemoteFrom(inspected.Remote, inspected.Root)
	if err != nil {
		return state.Project{}, err
	}
	return r.store.AddProject(ctx, state.Project{
		Alias:            alias,
		NormalizedRemote: remote,
		CloneURL:         gitops.CloneURLFor(inspected.Remote, inspected.Root),
		LocalPath:        inspected.Root,
	})
}

func (r *Registry) ScanWorkspace(ctx context.Context, root string) (ScanResult, error) {
	if strings.TrimSpace(root) == "" {
		return ScanResult{}, errors.New("workspace root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ScanResult{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = realRoot
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return ScanResult{}, fmt.Errorf("stat workspace root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return ScanResult{}, fmt.Errorf("workspace root is not a directory: %s", absRoot)
	}

	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	aliases := make(map[string]bool, len(projects))
	for _, project := range projects {
		aliases[project.Alias] = true
	}

	result := ScanResult{WorkspaceRoot: absRoot}
	var discoveredRoots []string
	seenRemotes := make(map[string]string)
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: path, Reason: walkErr.Error()})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}

		projectPath := filepath.Dir(path)
		if isNestedProject(projectPath, discoveredRoots) {
			result.Skipped = append(result.Skipped, ScanSkip{Path: projectPath, Reason: "nested Git repo"})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		discoveredRoots = append(discoveredRoots, projectPath)

		inspected, err := r.git.InspectProject(ctx, projectPath)
		if err != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: projectPath, Reason: "unsupported Git repo: " + err.Error()})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		remote, err := NormalizeRemoteFrom(inspected.Remote, inspected.Root)
		if err != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: inspected.Root, Reason: "unsupported Git remote: " + err.Error()})
			return filepath.SkipDir
		}
		if firstPath, ok := seenRemotes[remote]; ok {
			result.Skipped = append(result.Skipped, ScanSkip{Path: inspected.Root, Reason: "duplicate remote already scanned at " + firstPath})
			return filepath.SkipDir
		}
		alias := uniqueAlias(inspected.Alias, aliases)
		project, action, err := r.store.UpsertProject(ctx, state.Project{
			Alias:            alias,
			NormalizedRemote: remote,
			CloneURL:         gitops.CloneURLFor(inspected.Remote, inspected.Root),
			LocalPath:        inspected.Root,
		})
		if err != nil {
			return err
		}
		aliases[project.Alias] = true
		seenRemotes[remote] = inspected.Root
		discoveredRoots = append(discoveredRoots, inspected.Root)
		switch action {
		case state.ProjectUpsertAdded:
			result.Added = append(result.Added, project)
		case state.ProjectUpsertUpdated:
			result.Updated = append(result.Updated, project)
		case state.ProjectUpsertUnchanged:
			result.Unchanged = append(result.Unchanged, project)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

func (r *Registry) Hydrate(ctx context.Context, alias string, opts ...clonestrategy.Options) (HydrateResult, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return HydrateResult{}, errors.New("project name is required")
	}
	options := clonestrategy.Options{}
	if len(opts) != 0 {
		options = opts[0]
	}
	plan, err := hydrationplanner.New(r.store).PlanProject(ctx, alias, options)
	if err != nil {
		return HydrateResult{}, err
	}
	action := hydrationplanner.Action{}
	if len(plan.Actions) != 0 {
		action = plan.Actions[0]
	}
	hydrationProject := action.ProjectRow
	if hydrationProject.Alias == "" {
		hydrationProject.Alias = alias
	}
	result := HydrateResult{Project: hydrationProject, Plan: plan, Action: action, CloneStrategy: action.CloneStrategy}
	switch action.State {
	case hydrationplanner.StateUnknownProject:
		return result, UnknownProjectError{Alias: alias}
	case hydrationplanner.StateUnsafePath:
		return result, UnsafePathError{Path: action.Path, Reason: action.Reason}
	case hydrationplanner.StatePathConflict:
		return result, PathConflictError{Path: action.Path, Reason: action.Reason}
	case hydrationplanner.StatePresent:
		if !gitCheckoutMatches(ctx, r.git, hydrationProject) {
			return result, PathConflictError{Path: action.Path, Reason: "exists but does not match the registered project"}
		}
		if hydrationProject.Source == "canonical" && action.ObservedPath != "" && action.ObservedPath != hydrationProject.LocalPath {
			if updater, ok := r.store.(projectUpdater); ok {
				updated, err := updater.UpdateProject(ctx, action.ProjectID, hydrationProject)
				if err != nil {
					return result, err
				}
				hydrationProject = updated
			}
		}
		result.Project = hydrationProject
		result.AlreadyPresent = true
		result.CloneStrategy = clonestrategy.FullCloneSelection()
		return result, nil
	case hydrationplanner.StateMissing, hydrationplanner.StatePlaceholder:
		cloneResult, err := hydrationexecutor.New(r.git).Execute(ctx, hydrationplanner.Plan{WorkspaceRoot: plan.WorkspaceRoot, Actions: []hydrationplanner.Action{action}}, options)
		if len(cloneResult.ClonedProjects) != 0 {
			result.CloneStrategy = cloneResult.ClonedProjects[0].CloneStrategy
		}
		if err != nil {
			var unsafe hydrationexecutor.UnsafePathError
			if errors.As(err, &unsafe) {
				return result, UnsafePathError{Path: unsafe.Path, Reason: unsafe.Reason}
			}
			var conflict hydrationexecutor.PathConflictError
			if errors.As(err, &conflict) {
				return result, PathConflictError{Path: conflict.Path, Reason: conflict.Reason}
			}
			return result, err
		}
		result.AlreadyPresent = false
		if hydrationProject.Source == "canonical" && action.ObservedPath != "" && action.ObservedPath != hydrationProject.LocalPath {
			if updater, ok := r.store.(projectUpdater); ok {
				updated, err := updater.UpdateProject(ctx, action.ProjectID, hydrationProject)
				if err != nil {
					return result, err
				}
				hydrationProject = updated
			}
		}
		result.Project = hydrationProject
		return result, nil
	}
	return result, fmt.Errorf("hydrate plan for %q did not produce an executable action", alias)
}

func hydrationPath(project state.Project) string {
	if project.Source == "canonical" && strings.TrimSpace(project.CanonicalPath) != "" {
		return project.CanonicalPath
	}
	return project.LocalPath
}

func hydrateProject(ctx context.Context, git gitops.Client, project state.Project, options clonestrategy.Options) (bool, clonestrategy.Selection, error) {
	strategy := clonestrategy.FullCloneSelection()
	cleanupOnCloneFailure := false
	info, err := os.Stat(project.LocalPath)
	switch {
	case err == nil:
		if gitCheckoutMatches(ctx, git, project) {
			return true, strategy, nil
		}
		if !info.IsDir() {
			return false, strategy, PathConflictError{Path: project.LocalPath, Reason: "exists and is not a directory"}
		}
		empty, err := dirIsEmpty(project.LocalPath)
		if err != nil {
			return false, strategy, err
		}
		if !empty {
			return false, strategy, PathConflictError{Path: project.LocalPath, Reason: "exists and is not empty"}
		}
		cleanupOnCloneFailure = true
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(project.LocalPath), 0o755); err != nil {
			return false, strategy, fmt.Errorf("create project parent directory: %w", err)
		}
		cleanupOnCloneFailure = true
	default:
		return false, strategy, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}

	cloneURL := project.CloneURL
	if cloneURL == "" {
		cloneURL = project.NormalizedRemote
	}
	strategy = clonestrategy.SelectionForOptions(options)
	result, err := (clonestrategy.FullClone{Git: git}).Clone(ctx, clonestrategy.Request{
		CloneURL:    cloneURL,
		Destination: project.LocalPath,
		Options:     options,
	})
	strategy = result.Strategy
	if err != nil {
		if cleanupOnCloneFailure {
			_ = os.RemoveAll(project.LocalPath)
		}
		return false, strategy, fmt.Errorf("clone %q into %q: %s", redactedCloneURL(cloneURL), project.LocalPath, err.Error())
	}
	return false, strategy, nil
}

func gitCheckoutMatches(ctx context.Context, git gitops.Client, project state.Project) bool {
	inside, err := git.Output(ctx, project.LocalPath, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return false
	}
	root, err := git.Output(ctx, project.LocalPath, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(strings.TrimSpace(root), project.LocalPath) {
		return false
	}
	remote, err := git.Output(ctx, project.LocalPath, "remote", "get-url", "origin")
	if err != nil {
		return false
	}
	normalized, err := NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath)
	if err != nil {
		return false
	}
	return gitops.RemoteMatchesSource(normalized, project.NormalizedRemote, project.CloneURL, project.LocalPath)
}

func cloneURLFor(remote, baseDir string) string {
	return gitops.CloneURLFor(remote, baseDir)
}

func redactedCloneURL(raw string) string {
	return gitops.RedactURLForMetadata(raw)
}

func redactedCloneOutput(output, cloneURL string) string {
	return gitops.RedactCloneOutput(output, cloneURL)
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if realA, err := filepath.EvalSymlinks(a); err == nil {
		a = realA
	}
	if realB, err := filepath.EvalSymlinks(b); err == nil {
		b = realB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func dirIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read project path %q: %w", path, err)
	}
	return len(entries) == 0, nil
}

func (r *Registry) Entries(ctx context.Context) ([]Entry, error) {
	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(projects))
	for _, project := range projects {
		entryState := "present"
		if _, err := os.Stat(project.LocalPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
			}
			entryState = "missing"
		}
		entries = append(entries, Entry{Project: project, State: entryState})
	}
	return entries, nil
}

func isNestedProject(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func uniqueAlias(alias string, used map[string]bool) string {
	if alias == "" {
		alias = "project"
	}
	if !used[alias] {
		return alias
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", alias, i)
		if !used[candidate] {
			return candidate
		}
	}
}

type InspectedGitProject struct {
	Root   string
	Remote string
	Alias  string
}

func InspectGitProject(path string) (InspectedGitProject, error) {
	inspected, err := gitops.Process().InspectProject(context.Background(), path)
	if err != nil {
		return InspectedGitProject{}, err
	}
	return InspectedGitProject{
		Root:   inspected.Root,
		Remote: inspected.Remote,
		Alias:  inspected.Alias,
	}, nil
}

func NormalizeRemote(remote string) (string, error) {
	return gitops.NormalizeRemote(remote)
}

func NormalizeRemoteFrom(remote, baseDir string) (string, error) {
	return gitops.NormalizeRemoteFrom(remote, baseDir)
}

func gitOutput(dir string, args ...string) (string, error) {
	return gitops.Process().Output(context.Background(), dir, args...)
}
