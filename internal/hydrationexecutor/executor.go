package hydrationexecutor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/hydrationplanner"
	"github.com/BramVR/codemesh/internal/state"
)

type Executor struct {
	Git gitops.Client
}

type Result struct {
	ClonedProjects []ClonedProject
}

type ClonedProject struct {
	Project       string                  `json:"project"`
	ProjectID     int64                   `json:"project_id,omitempty"`
	Remote        string                  `json:"remote"`
	CloneURL      string                  `json:"clone_url"`
	Path          string                  `json:"path"`
	CloneStrategy clonestrategy.Selection `json:"clone_strategy"`
}

type RefusalError struct {
	Action hydrationplanner.Action
}

type UnsafePathError struct {
	Path   string
	Reason string
}

type PathConflictError struct {
	Path   string
	Reason string
}

func (e UnsafePathError) Error() string {
	return "unsafe path: " + e.Reason
}

func (e PathConflictError) Error() string {
	return fmt.Sprintf("path conflict: %s %s", e.Path, e.Reason)
}

func (e RefusalError) Error() string {
	return fmt.Sprintf("hydrate action refused: %s %s", e.Action.State, e.Action.Reason)
}

func New(git gitops.Client) Executor {
	return Executor{Git: git}
}

func (e Executor) Execute(ctx context.Context, plan hydrationplanner.Plan, options clonestrategy.Options) (Result, error) {
	if plan.Blocked {
		for _, action := range plan.Actions {
			if action.Action == hydrationplanner.ActionRefuse {
				return Result{}, RefusalError{Action: action}
			}
		}
		return Result{}, errors.New("hydration plan is blocked")
	}
	result := Result{}
	plannedPaths := map[string]string{}
	for _, action := range plan.Actions {
		switch action.Action {
		case hydrationplanner.ActionClone:
			if err := addPlannedPath(plannedPaths, action.Path, action.Project); err != nil {
				return result, err
			}
		case hydrationplanner.ActionRefuse:
			return result, RefusalError{Action: action}
		case hydrationplanner.ActionNone:
			if action.State == hydrationplanner.StatePresent && !e.gitCheckoutMatches(ctx, action) {
				return result, PathConflictError{Path: action.Path, Reason: "exists but does not match the registered project"}
			}
			if action.State == hydrationplanner.StatePresent {
				if err := addPlannedPath(plannedPaths, action.Path, action.Project); err != nil {
					return result, err
				}
			}
		}
	}
	for _, action := range plan.Actions {
		if action.Action != hydrationplanner.ActionClone {
			continue
		}
		cloned, err := e.clone(ctx, plan.WorkspaceRoot, action, options)
		if err != nil {
			return result, err
		}
		result.ClonedProjects = append(result.ClonedProjects, cloned)
	}
	return result, nil
}

func addPlannedPath(planned map[string]string, path, project string) error {
	cleanPath := filepath.Clean(path)
	for existingPath, existingProject := range planned {
		if pathsOverlap(cleanPath, existingPath) {
			return PathConflictError{
				Path:   path,
				Reason: fmt.Sprintf("desired path conflicts with project %q", existingProject),
			}
		}
	}
	planned[cleanPath] = project
	return nil
}

func pathsOverlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == b {
		return true
	}
	return pathWithin(a, b) || pathWithin(b, a)
}

func pathWithin(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (e Executor) clone(ctx context.Context, workspaceRoot string, action hydrationplanner.Action, options clonestrategy.Options) (ClonedProject, error) {
	project := action.ProjectRow
	if project.LocalPath == "" {
		project.LocalPath = action.Path
	}
	if project.CloneURL == "" {
		project.CloneURL = project.NormalizedRemote
	}
	selection := clonestrategy.SelectionForOptions(options)
	cleanupOnCloneFailure := false
	moveIntoExistingDestination := false
	clonePath := project.LocalPath
	info, err := os.Stat(project.LocalPath)
	switch {
	case err == nil:
		if !info.IsDir() {
			return ClonedProject{}, PathConflictError{Path: project.LocalPath, Reason: "exists and is not a directory"}
		}
		empty, err := dirIsEmpty(project.LocalPath)
		if err != nil {
			return ClonedProject{}, err
		}
		if !empty {
			return ClonedProject{}, PathConflictError{Path: project.LocalPath, Reason: "exists and is not empty"}
		}
		if err := ensureRealDestinationWithinRoot(workspaceRoot, project.LocalPath); err != nil {
			return ClonedProject{}, err
		}
		tempParent := filepath.Dir(project.LocalPath)
		if workspaceRoot != "" && samePath(project.LocalPath, workspaceRoot) {
			tempParent = project.LocalPath
		}
		clonePath, err = os.MkdirTemp(tempParent, ".codemesh-clone-"+filepath.Base(project.LocalPath)+"-")
		if err != nil {
			return ClonedProject{}, fmt.Errorf("create temporary clone directory: %w", err)
		}
		if err := ensureRealDestinationWithinRoot(workspaceRoot, clonePath); err != nil {
			_ = os.RemoveAll(clonePath)
			return ClonedProject{}, err
		}
		cleanupOnCloneFailure = true
		moveIntoExistingDestination = true
	case errors.Is(err, os.ErrNotExist):
		if err := ensureRealDestinationWithinRoot(workspaceRoot, project.LocalPath); err != nil {
			return ClonedProject{}, err
		}
		if err := os.MkdirAll(filepath.Dir(project.LocalPath), 0o755); err != nil {
			return ClonedProject{}, fmt.Errorf("create project parent directory: %w", err)
		}
		cleanupOnCloneFailure = true
	default:
		return ClonedProject{}, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}
	if err := ensureRealDestinationWithinRoot(workspaceRoot, project.LocalPath); err != nil {
		if moveIntoExistingDestination {
			_ = os.RemoveAll(clonePath)
		}
		return ClonedProject{}, err
	}

	git := e.Git
	if git == (gitops.Client{}) {
		git = gitops.Process()
	}
	cloneResult, err := (clonestrategy.FullClone{Git: git}).Clone(ctx, clonestrategy.Request{
		CloneURL:    project.CloneURL,
		Destination: clonePath,
		Options:     options,
	})
	selection = cloneResult.Strategy
	if err != nil {
		if cleanupOnCloneFailure {
			_ = os.RemoveAll(clonePath)
		}
		return ClonedProject{}, fmt.Errorf("clone %q into %q: %s", gitops.RedactURLForMetadata(project.CloneURL), project.LocalPath, err.Error())
	}
	if moveIntoExistingDestination {
		if empty, err := dirIsEmptyExcept(project.LocalPath, clonePath); err != nil {
			_ = os.RemoveAll(clonePath)
			return ClonedProject{}, err
		} else if !empty {
			_ = os.RemoveAll(clonePath)
			return ClonedProject{}, PathConflictError{Path: project.LocalPath, Reason: "changed while clone was running"}
		}
		if err := moveDirectoryContents(clonePath, project.LocalPath); err != nil {
			_ = os.RemoveAll(clonePath)
			return ClonedProject{}, fmt.Errorf("move cloned project into %q: %w", project.LocalPath, err)
		}
		if err := os.Remove(clonePath); err != nil {
			return ClonedProject{}, fmt.Errorf("remove temporary clone directory: %w", err)
		}
	}
	return ClonedProject{
		Project:       project.Alias,
		ProjectID:     action.ProjectID,
		Remote:        project.NormalizedRemote,
		CloneURL:      gitops.RedactURLForMetadata(project.CloneURL),
		Path:          project.LocalPath,
		CloneStrategy: selection,
	}, nil
}

func (e Executor) gitCheckoutMatches(ctx context.Context, action hydrationplanner.Action) bool {
	project := action.ProjectRow
	if project.LocalPath == "" {
		project.LocalPath = action.Path
	}
	git := e.Git
	if git == (gitops.Client{}) {
		git = gitops.Process()
	}
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
	normalized, err := gitops.NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath)
	if err != nil {
		return false
	}
	return remoteMatchesProjectSource(normalized, project)
}

func remoteMatchesProjectSource(normalized string, project state.Project) bool {
	if normalized == project.NormalizedRemote {
		return true
	}
	cloneURL := strings.TrimSpace(project.CloneURL)
	if cloneURL == "" {
		return false
	}
	cloneIdentity, err := gitops.NormalizeRemoteFrom(cloneURL, project.LocalPath)
	return err == nil && normalized == cloneIdentity
}

func ensureRealDestinationWithinRoot(workspaceRoot, destination string) error {
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil
	}
	realRoot, err := realPathForPossiblyMissing(workspaceRoot)
	if err != nil {
		return fmt.Errorf("check workspace root %q: %w", workspaceRoot, err)
	}
	realDestination, err := realPathForPossiblyMissing(destination)
	if err != nil {
		return fmt.Errorf("check project path %q: %w", destination, err)
	}
	rel, err := filepath.Rel(realRoot, realDestination)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return UnsafePathError{Path: destination, Reason: "desired path resolves outside workspace root"}
	}
	return nil
}

func realPathForPossiblyMissing(path string) (string, error) {
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	ancestor := filepath.Dir(path)
	suffix := []string{filepath.Base(path)}
	for {
		realAncestor, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			parts := append([]string{realAncestor}, suffix...)
			return filepath.Join(parts...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return "", os.ErrNotExist
		}
		suffix = append([]string{filepath.Base(ancestor)}, suffix...)
		ancestor = next
	}
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
	return dirIsEmptyExcept(path, "")
}

func dirIsEmptyExcept(path, except string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read project path %q: %w", path, err)
	}
	if except == "" {
		return len(entries) == 0, nil
	}
	except = filepath.Clean(except)
	for _, entry := range entries {
		if filepath.Clean(filepath.Join(path, entry.Name())) == except {
			continue
		}
		return false, nil
	}
	return true, nil
}

func moveDirectoryContents(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
