package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	ListMachines(context.Context) ([]state.Machine, error)
	AddProject(context.Context, state.Project) (state.Project, error)
	UpdateProject(context.Context, int64, state.Project) (state.Project, error)
}

type Bootstrapper struct {
	Store Store
}

type Result struct {
	Plan    reconciliation.DriftPlan `json:"plan"`
	Applied Applied                  `json:"applied"`
}

type Applied struct {
	ParentDirectories []string        `json:"parent_directories"`
	AddedProjects     []state.Project `json:"added_projects"`
	UpdatedProjects   []state.Project `json:"updated_projects"`
}

type BlockedError struct {
	Blockers []reconciliation.Blocker
}

func (e BlockedError) Error() string {
	return fmt.Sprintf("bootstrap blocked by %d plan blocker(s)", len(e.Blockers))
}

func (b Bootstrapper) Plan(ctx context.Context, entries []workspacemanifest.Entry) (reconciliation.DriftPlan, error) {
	if b.Store == nil {
		return reconciliation.DriftPlan{}, errors.New("bootstrap store is required")
	}
	return reconciliation.New(b.Store).DryRun(ctx, entries)
}

func (b Bootstrapper) Apply(ctx context.Context, entries []workspacemanifest.Entry) (Result, error) {
	plan, err := b.Plan(ctx, entries)
	if err != nil {
		return Result{}, err
	}
	result := Result{Plan: plan}
	if plan.Blocked {
		return result, BlockedError{Blockers: plan.Blockers}
	}
	projects, err := b.Store.ListProjects(ctx)
	if err != nil {
		return result, err
	}
	importPlan, err := workspacemanifest.PlanImport(entries, projects, plan.WorkspaceRoot)
	if err != nil {
		return result, err
	}
	for _, change := range importPlan.Changes {
		if change.Action == workspacemanifest.ChangeConflict {
			return result, fmt.Errorf("bootstrap import conflict for %q: %s", change.Alias, change.ConflictReason)
		}
	}

	parents := parentDirectories(plan)
	for _, parent := range parents {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return result, fmt.Errorf("create bootstrap parent %q: %w", parent, err)
		}
		result.Applied.ParentDirectories = append(result.Applied.ParentDirectories, parent)
	}

	updated, err := b.applyUpdates(ctx, importPlan.Changes, projects)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, updated...)
	for _, change := range importPlan.Changes {
		if change.Action == workspacemanifest.ChangeAdd {
			project, err := b.Store.AddProject(ctx, projectFromImportChange(change))
			if err != nil {
				return result, fmt.Errorf("add bootstrap project %q: %w", change.Alias, err)
			}
			result.Applied.AddedProjects = append(result.Applied.AddedProjects, project)
		}
	}
	return result, nil
}

func (b Bootstrapper) applyUpdates(ctx context.Context, changes []workspacemanifest.ImportChange, projects []state.Project) ([]state.Project, error) {
	updates := make([]workspacemanifest.ImportChange, 0, len(changes))
	for _, change := range changes {
		if change.Action == workspacemanifest.ChangeUpdate {
			updates = append(updates, change)
		}
	}
	if len(updates) == 0 {
		return nil, nil
	}

	byID := make(map[int64]state.Project, len(projects))
	usedAliases := make(map[string]bool, len(projects)+len(updates))
	for _, project := range projects {
		byID[project.ID] = project
		usedAliases[project.Alias] = true
	}
	for _, change := range updates {
		usedAliases[change.Alias] = true
	}

	tempAliases := make(map[int64]string)
	for _, change := range updates {
		current, ok := byID[change.ProjectID]
		if !ok || current.Alias == change.Alias {
			continue
		}
		tempAliases[change.ProjectID] = bootstrapTempAlias(change.ProjectID, usedAliases)
	}
	for _, change := range updates {
		tempAlias, ok := tempAliases[change.ProjectID]
		if !ok {
			continue
		}
		current := byID[change.ProjectID]
		current.Alias = tempAlias
		if _, err := b.Store.UpdateProject(ctx, change.ProjectID, current); err != nil {
			return nil, fmt.Errorf("free bootstrap project alias %q: %w", change.Alias, err)
		}
	}

	updated := make([]state.Project, 0, len(updates))
	for _, change := range updates {
		project, err := b.Store.UpdateProject(ctx, change.ProjectID, projectFromImportChange(change))
		if err != nil {
			return nil, fmt.Errorf("update bootstrap project %q: %w", change.Alias, err)
		}
		updated = append(updated, project)
	}
	return updated, nil
}

func bootstrapTempAlias(projectID int64, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		alias := fmt.Sprintf("__codemesh_bootstrap_%d", projectID)
		if suffix > 0 {
			alias = fmt.Sprintf("%s_%d", alias, suffix)
		}
		if used[alias] {
			continue
		}
		used[alias] = true
		return alias
	}
}

func projectFromImportChange(change workspacemanifest.ImportChange) state.Project {
	return state.Project{
		ID:               change.ProjectID,
		Alias:            change.Alias,
		NormalizedRemote: change.Identity,
		CloneURL:         change.CloneURL,
		LocalPath:        change.LocalPath,
	}
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
