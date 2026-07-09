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
	DeleteProject(context.Context, int64) error
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

	importResult, err := workspacemanifest.ApplyImportPlan(ctx, b.Store, importPlan, projects)
	if err != nil {
		return result, err
	}
	result.Applied.UpdatedProjects = append(result.Applied.UpdatedProjects, importResult.UpdatedProjects...)
	result.Applied.AddedProjects = append(result.Applied.AddedProjects, importResult.AddedProjects...)
	return result, nil
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
