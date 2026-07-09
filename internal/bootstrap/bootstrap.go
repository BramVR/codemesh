package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/hydrationexecutor"
	"github.com/BramVR/codemesh/internal/hydrationplanner"
	"github.com/BramVR/codemesh/internal/placeholder"
	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	ListMachines(context.Context) ([]state.Machine, error)
	AddProject(context.Context, state.Project) (state.Project, error)
	UpdateProject(context.Context, int64, state.Project) (state.Project, error)
	DeleteProject(context.Context, int64) error
}

type Bootstrapper struct {
	Store Store
	Git   gitops.Client
}

type Result struct {
	Plan          reconciliation.DriftPlan `json:"plan"`
	HydrationPlan hydrationplanner.Plan    `json:"hydration_plan"`
	Applied       Applied                  `json:"applied"`
}

type Applied struct {
	ParentDirectories   []string                          `json:"parent_directories"`
	AddedProjects       []state.Project                   `json:"added_projects"`
	UpdatedProjects     []state.Project                   `json:"updated_projects"`
	PlaceholderProjects []placeholder.MaterializedProject `json:"placeholder_projects"`
	ClonedProjects      []hydrationexecutor.ClonedProject `json:"cloned_projects"`
}

type BlockedError struct {
	Blockers []reconciliation.Blocker
}

func (e BlockedError) Error() string {
	return fmt.Sprintf("bootstrap blocked by %d plan blocker(s)", len(e.Blockers))
}

type HydrationBlockedError struct {
	Actions []hydrationplanner.Action
}

func (e HydrationBlockedError) Error() string {
	return fmt.Sprintf("bootstrap blocked by %d hydration refusal(s)", len(e.Actions))
}

func (b Bootstrapper) Plan(ctx context.Context, entries []workspacemanifest.Entry) (reconciliation.DriftPlan, error) {
	if b.Store == nil {
		return reconciliation.DriftPlan{}, errors.New("bootstrap store is required")
	}
	return reconciliation.New(b.Store).DryRun(ctx, entries)
}

func (b Bootstrapper) PlanResult(ctx context.Context, entries []workspacemanifest.Entry) (Result, error) {
	plan, err := b.Plan(ctx, entries)
	result := Result{Plan: plan}
	if err != nil {
		return result, err
	}
	hydrationPlan, err := hydrationplanner.New(b.Store).PlanEntries(ctx, entries, clonestrategy.Options{})
	if err != nil {
		return result, err
	}
	result.HydrationPlan = hydrationPlan
	return result, nil
}

func (b Bootstrapper) PlanRegistry(ctx context.Context, aliases []string, all bool) (Result, error) {
	if b.Store == nil {
		return Result{}, errors.New("bootstrap store is required")
	}
	planner := hydrationplanner.New(b.Store)
	if all {
		hydrationPlan, err := planner.PlanAll(ctx, clonestrategy.Options{})
		return Result{HydrationPlan: hydrationPlan}, err
	}
	result := Result{}
	seen := map[string]bool{}
	for _, alias := range aliases {
		if seen[alias] {
			continue
		}
		seen[alias] = true
		plan, err := planner.PlanProject(ctx, alias, clonestrategy.Options{})
		if err != nil {
			return result, err
		}
		if result.HydrationPlan.WorkspaceRoot == "" {
			result.HydrationPlan.WorkspaceRoot = plan.WorkspaceRoot
		}
		if plan.Blocked {
			result.HydrationPlan.Blocked = true
		}
		result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, plan.Actions...)
	}
	return result, nil
}

func (b Bootstrapper) Apply(ctx context.Context, entries []workspacemanifest.Entry) (Result, error) {
	result, err := b.PlanResult(ctx, entries)
	if err != nil {
		return Result{}, err
	}
	if result.Plan.Blocked {
		return result, BlockedError{Blockers: result.Plan.Blockers}
	}
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	projects, err := b.Store.ListProjects(ctx)
	if err != nil {
		return result, err
	}
	importPlan, err := workspacemanifest.PlanImport(entries, projects, result.Plan.WorkspaceRoot)
	if err != nil {
		return result, err
	}
	for _, change := range importPlan.Changes {
		if change.Action == workspacemanifest.ChangeConflict {
			return result, fmt.Errorf("bootstrap import conflict for %q: %s", change.Alias, change.ConflictReason)
		}
	}

	parents := parentDirectories(result.Plan)
	for _, parent := range parents {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return result, fmt.Errorf("create bootstrap parent %q: %w", parent, err)
		}
		result.Applied.ParentDirectories = append(result.Applied.ParentDirectories, parent)
	}

	importResult, err := workspacemanifest.ApplyImportPlan(ctx, b.Store, importPlan, projects)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, importResult.UpdatedProjects...)
	result.Applied.AddedProjects = append(result.Applied.AddedProjects, importResult.AddedProjects...)
	updatedHydrationPlan, err := b.planImportedEntries(ctx, entries)
	if err != nil {
		return result, err
	}
	result.HydrationPlan = updatedHydrationPlan
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	cloneResult, err := hydrationexecutor.New(b.git()).Execute(ctx, result.HydrationPlan, clonestrategy.Options{})
	if err != nil {
		if action, ok := actionFromExecutionError(err); ok {
			result.HydrationPlan.Blocked = true
			result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, action)
			return result, HydrationBlockedError{Actions: []hydrationplanner.Action{action}}
		}
		return result, err
	}
	result.Applied.ClonedProjects = append(result.Applied.ClonedProjects, cloneResult.ClonedProjects...)
	updatedProjects, err := b.persistClonedCanonicalPlacements(ctx, result.HydrationPlan)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, updatedProjects...)
	return result, nil
}

func (b Bootstrapper) Placeholders(ctx context.Context, entries []workspacemanifest.Entry) (Result, error) {
	result, err := b.PlanResult(ctx, entries)
	if err != nil {
		return Result{}, err
	}
	if result.Plan.Blocked {
		return result, BlockedError{Blockers: result.Plan.Blockers}
	}
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	if _, err := preflightPlaceholderMaterialization(result.HydrationPlan); err != nil {
		if action, ok := actionFromExecutionError(err); ok {
			result.HydrationPlan.Blocked = true
			result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, action)
			return result, HydrationBlockedError{Actions: []hydrationplanner.Action{action}}
		}
		return result, err
	}
	projects, err := b.Store.ListProjects(ctx)
	if err != nil {
		return result, err
	}
	importPlan, err := workspacemanifest.PlanImport(entries, projects, result.Plan.WorkspaceRoot)
	if err != nil {
		return result, err
	}
	for _, change := range importPlan.Changes {
		if change.Action == workspacemanifest.ChangeConflict {
			return result, fmt.Errorf("bootstrap import conflict for %q: %s", change.Alias, change.ConflictReason)
		}
	}
	importResult, err := workspacemanifest.ApplyImportPlan(ctx, b.Store, importPlan, projects)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, importResult.UpdatedProjects...)
	result.Applied.AddedProjects = append(result.Applied.AddedProjects, importResult.AddedProjects...)
	result.HydrationPlan, err = b.planImportedEntries(ctx, entries)
	if err != nil {
		return result, err
	}
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	placeholders, err := materializePlaceholders(result.HydrationPlan)
	if err != nil {
		if action, ok := actionFromExecutionError(err); ok {
			result.HydrationPlan.Blocked = true
			result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, action)
			return result, HydrationBlockedError{Actions: []hydrationplanner.Action{action}}
		}
		return result, err
	}
	result.Applied.PlaceholderProjects = append(result.Applied.PlaceholderProjects, placeholders...)
	updatedProjects, err := b.persistClonedCanonicalPlacements(ctx, result.HydrationPlan)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, updatedProjects...)
	return result, nil
}

func (b Bootstrapper) planImportedEntries(ctx context.Context, entries []workspacemanifest.Entry) (hydrationplanner.Plan, error) {
	planner := hydrationplanner.New(b.Store)
	seen := map[string]bool{}
	combined := hydrationplanner.Plan{}
	for _, entry := range entries {
		alias := entry.Project.Alias
		if seen[alias] {
			continue
		}
		seen[alias] = true
		plan, err := planner.PlanProject(ctx, alias, clonestrategy.Options{})
		if err != nil {
			return combined, err
		}
		if combined.WorkspaceRoot == "" {
			combined.WorkspaceRoot = plan.WorkspaceRoot
		}
		if plan.Blocked {
			combined.Blocked = true
		}
		combined.Actions = append(combined.Actions, plan.Actions...)
	}
	return combined, nil
}

func (b Bootstrapper) ApplyRegistry(ctx context.Context, aliases []string, all bool) (Result, error) {
	result, err := b.PlanRegistry(ctx, aliases, all)
	if err != nil {
		return result, err
	}
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	cloneResult, err := hydrationexecutor.New(b.git()).Execute(ctx, result.HydrationPlan, clonestrategy.Options{})
	if err != nil {
		if action, ok := actionFromExecutionError(err); ok {
			result.HydrationPlan.Blocked = true
			result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, action)
			return result, HydrationBlockedError{Actions: []hydrationplanner.Action{action}}
		}
		return result, err
	}
	result.Applied.ClonedProjects = append(result.Applied.ClonedProjects, cloneResult.ClonedProjects...)
	updatedProjects, err := b.persistClonedCanonicalPlacements(ctx, result.HydrationPlan)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, updatedProjects...)
	return result, nil
}

func (b Bootstrapper) PlaceholdersRegistry(ctx context.Context, aliases []string, all bool) (Result, error) {
	result, err := b.PlanRegistry(ctx, aliases, all)
	if err != nil {
		return result, err
	}
	if result.HydrationPlan.Blocked {
		return result, HydrationBlockedError{Actions: refusalActions(result.HydrationPlan.Actions)}
	}
	placeholders, err := materializePlaceholders(result.HydrationPlan)
	if err != nil {
		if action, ok := actionFromExecutionError(err); ok {
			result.HydrationPlan.Blocked = true
			result.HydrationPlan.Actions = append(result.HydrationPlan.Actions, action)
			return result, HydrationBlockedError{Actions: []hydrationplanner.Action{action}}
		}
		return result, err
	}
	result.Applied.PlaceholderProjects = append(result.Applied.PlaceholderProjects, placeholders...)
	updatedProjects, err := b.persistClonedCanonicalPlacements(ctx, result.HydrationPlan)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, updatedProjects...)
	return result, nil
}

func materializePlaceholders(plan hydrationplanner.Plan) ([]placeholder.MaterializedProject, error) {
	candidates, err := preflightPlaceholderMaterialization(plan)
	if err != nil {
		return nil, err
	}

	materialized := make([]placeholder.MaterializedProject, 0)
	for _, project := range candidates {
		written, err := placeholder.Write(project)
		if err != nil {
			return materialized, hydrationexecutor.PathConflictError{Path: project.LocalPath, Reason: err.Error()}
		}
		materialized = append(materialized, written)
	}
	return materialized, nil
}

func preflightPlaceholderMaterialization(plan hydrationplanner.Plan) ([]state.Project, error) {
	candidates := make([]state.Project, 0)
	plannedPaths := map[string]string{}
	for _, action := range plan.Actions {
		project, ok := placeholderProjectForAction(action)
		if ok {
			if err := addPlannedPlaceholderPath(plannedPaths, project.LocalPath, project.Alias); err != nil {
				return nil, err
			}
			candidates = append(candidates, project)
			continue
		}
		if action.Action == hydrationplanner.ActionNone && action.State == hydrationplanner.StatePresent {
			if err := addPlannedPlaceholderPath(plannedPaths, action.Path, action.Project); err != nil {
				return nil, err
			}
		}
	}
	for _, project := range candidates {
		if err := ensurePlaceholderDestinationWithinRoot(plan.WorkspaceRoot, project.LocalPath); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func placeholderProjectForAction(action hydrationplanner.Action) (state.Project, bool) {
	if action.Action != hydrationplanner.ActionClone {
		return state.Project{}, false
	}
	if action.State != hydrationplanner.StateMissing && action.State != hydrationplanner.StatePlaceholder {
		return state.Project{}, false
	}
	project := action.ProjectRow
	if project.LocalPath == "" {
		project.LocalPath = action.Path
	}
	if project.CanonicalPath == "" {
		project.CanonicalPath = action.CanonicalPath
	}
	return project, true
}

func addPlannedPlaceholderPath(planned map[string]string, path, project string) error {
	cleanPath := filepath.Clean(path)
	for existingPath, existingProject := range planned {
		if pathsOverlap(cleanPath, existingPath) {
			return hydrationexecutor.PathConflictError{
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

func ensurePlaceholderDestinationWithinRoot(workspaceRoot, destination string) error {
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
		return hydrationexecutor.UnsafePathError{Path: destination, Reason: "desired path resolves outside workspace root"}
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

func (b Bootstrapper) persistClonedCanonicalPlacements(ctx context.Context, plan hydrationplanner.Plan) ([]state.Project, error) {
	updated := make([]state.Project, 0)
	for _, action := range plan.Actions {
		if action.Action != hydrationplanner.ActionClone || action.ProjectID == 0 || action.ProjectRow.Source != "canonical" || action.ObservedPath == "" || action.ObservedPath == action.Path {
			continue
		}
		project := action.ProjectRow
		project.LocalPath = action.Path
		project.CanonicalPath = action.Path
		stored, err := b.Store.UpdateProject(ctx, action.ProjectID, project)
		if err != nil {
			return updated, err
		}
		updated = append(updated, stored)
	}
	return updated, nil
}

func actionFromExecutionError(err error) (hydrationplanner.Action, bool) {
	var conflict hydrationexecutor.PathConflictError
	if errors.As(err, &conflict) {
		return hydrationplanner.Action{
			Path:   conflict.Path,
			State:  hydrationplanner.StatePathConflict,
			Action: hydrationplanner.ActionRefuse,
			Reason: conflict.Reason,
		}, true
	}
	var unsafe hydrationexecutor.UnsafePathError
	if errors.As(err, &unsafe) {
		return hydrationplanner.Action{
			Path:   unsafe.Path,
			State:  hydrationplanner.StateUnsafePath,
			Action: hydrationplanner.ActionRefuse,
			Reason: unsafe.Reason,
		}, true
	}
	return hydrationplanner.Action{}, false
}

func (b Bootstrapper) git() gitops.Client {
	if b.Git == (gitops.Client{}) {
		return gitops.Process()
	}
	return b.Git
}

func refusalActions(actions []hydrationplanner.Action) []hydrationplanner.Action {
	refusals := make([]hydrationplanner.Action, 0)
	for _, action := range actions {
		if action.Action == hydrationplanner.ActionRefuse {
			refusals = append(refusals, action)
		}
	}
	return refusals
}

func parentDirectories(plan reconciliation.DriftPlan) []string {
	seen := map[string]bool{}
	workspaceRoot := filepath.Clean(plan.WorkspaceRoot)
	for _, drift := range plan.Drifts {
		if drift.Kind != reconciliation.DriftMissing && drift.Kind != reconciliation.DriftMoved && drift.Kind != reconciliation.DriftUnchanged {
			continue
		}
		desiredPath := filepath.Clean(drift.DesiredLocalPath)
		parent := filepath.Dir(desiredPath)
		if desiredPath == workspaceRoot {
			parent = workspaceRoot
		}
		if parent == "." || parent == "" {
			continue
		}
		seen[filepath.Clean(parent)] = true
	}
	parents := make([]string, 0, len(seen))
	for parent := range seen {
		parents = append(parents, parent)
	}
	sort.Strings(parents)
	return parents
}
