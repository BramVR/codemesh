package hydrationplanner

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
	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
}

type machineStore interface {
	ListMachines(context.Context) ([]state.Machine, error)
}

type Planner struct {
	store Store
}

type State string

const (
	StatePresent        State = "present"
	StateMissing        State = "missing"
	StatePathConflict   State = "path-conflict"
	StateUnsafePath     State = "unsafe-path"
	StateUnknownProject State = "unknown-project"
)

type ActionKind string

const (
	ActionNone   ActionKind = "none"
	ActionClone  ActionKind = "clone"
	ActionRefuse ActionKind = "refuse"
)

type Plan struct {
	WorkspaceRoot string   `json:"workspace_root,omitempty"`
	Blocked       bool     `json:"blocked"`
	Actions       []Action `json:"actions"`
}

type Action struct {
	Project       string                  `json:"project"`
	ProjectID     int64                   `json:"project_id,omitempty"`
	Identity      string                  `json:"identity,omitempty"`
	Path          string                  `json:"path,omitempty"`
	ObservedPath  string                  `json:"observed_path,omitempty"`
	CanonicalPath string                  `json:"canonical_path,omitempty"`
	CloneURL      string                  `json:"clone_url,omitempty"`
	Source        string                  `json:"source,omitempty"`
	State         State                   `json:"state"`
	Action        ActionKind              `json:"action"`
	Reason        string                  `json:"reason,omitempty"`
	PathPresent   bool                    `json:"path_present"`
	CloneStrategy clonestrategy.Selection `json:"clone_strategy"`
	ProjectRow    state.Project           `json:"-"`
}

func New(store Store) Planner {
	return Planner{store: store}
}

func (p Planner) PlanAll(ctx context.Context, options clonestrategy.Options) (Plan, error) {
	projects, err := p.store.ListProjects(ctx)
	if err != nil {
		return Plan{}, err
	}
	root, err := p.workspaceRoot(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{WorkspaceRoot: root}
	for _, project := range projects {
		plan.addAction(classifyProject(project, root, options))
	}
	sortActions(plan.Actions)
	return plan, nil
}

func (p Planner) PlanProject(ctx context.Context, alias string, options clonestrategy.Options) (Plan, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return Plan{}, errors.New("project name is required")
	}
	projects, err := p.store.ListProjects(ctx)
	if err != nil {
		return Plan{}, err
	}
	root, err := p.workspaceRoot(ctx)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{WorkspaceRoot: root}
	for _, project := range projects {
		if project.Alias == alias {
			plan.addAction(classifyProject(project, root, options))
			return plan, nil
		}
	}
	plan.addAction(Action{
		Project: alias,
		State:   StateUnknownProject,
		Action:  ActionRefuse,
		Reason:  fmt.Sprintf("unknown project: %s", alias),
	})
	return plan, nil
}

func (p Planner) PlanEntries(ctx context.Context, entries []workspacemanifest.Entry, options clonestrategy.Options) (Plan, error) {
	projects, err := p.store.ListProjects(ctx)
	if err != nil {
		return Plan{}, err
	}
	root, err := p.requiredWorkspaceRoot(ctx)
	if err != nil {
		return Plan{}, err
	}
	machine := state.Machine{WorkspaceRoot: root}
	driftPlan, err := reconciliation.BuildDryRunPlan(entries, projects, machine)
	if err != nil {
		return Plan{}, err
	}
	byIdentity := make(map[string]state.Project, len(projects))
	for _, project := range projects {
		byIdentity[project.NormalizedRemote] = project
	}
	plan := Plan{WorkspaceRoot: driftPlan.WorkspaceRoot}
	for _, drift := range driftPlan.Drifts {
		if drift.Kind != reconciliation.DriftConflicting && drift.Kind != reconciliation.DriftMissing && drift.Kind != reconciliation.DriftAdded {
			if project, ok := byIdentity[drift.Identity]; ok {
				project.CanonicalPath = drift.DesiredLocalPath
				project.CloneURL = drift.CloneURL
				project.Source = "canonical"
				plan.addAction(classifyProject(project, driftPlan.WorkspaceRoot, options))
				continue
			}
		}
		plan.addAction(actionFromDrift(drift, options))
	}
	sortActions(plan.Actions)
	return plan, nil
}

func (p Planner) workspaceRoot(ctx context.Context) (string, error) {
	if _, ok := p.store.(machineStore); !ok {
		return "", nil
	}
	return p.requiredWorkspaceRoot(ctx)
}

func (p Planner) requiredWorkspaceRoot(ctx context.Context) (string, error) {
	machines, err := p.store.(machineStore).ListMachines(ctx)
	if err != nil {
		return "", err
	}
	if len(machines) == 0 {
		return "", nil
	}
	root := strings.TrimSpace(machines[0].WorkspaceRoot)
	if root == "" {
		return "", nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve machine workspace root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func (p *Plan) addAction(action Action) {
	if action.Action == ActionRefuse {
		p.Blocked = true
	}
	p.Actions = append(p.Actions, action)
}

func classifyProject(project state.Project, workspaceRoot string, options clonestrategy.Options) Action {
	project = withPlacementDefaults(project)
	path := hydrationPath(project)
	action := Action{
		Project:       project.Alias,
		ProjectID:     project.ID,
		Identity:      project.NormalizedRemote,
		Path:          path,
		ObservedPath:  project.LocalPath,
		CanonicalPath: project.CanonicalPath,
		CloneURL:      redactedCloneURL(cloneURLForProject(project)),
		Source:        project.Source,
		CloneStrategy: clonestrategy.SelectionForOptions(options),
		ProjectRow:    project,
	}
	action.ProjectRow.LocalPath = path
	action.ProjectRow.CloneURL = cloneURLForProject(project)
	if workspaceRoot != "" && project.Source == "canonical" && !pathWithinRoot(workspaceRoot, path) {
		action.State = StateUnsafePath
		action.Action = ActionRefuse
		action.Reason = "desired path resolves outside workspace root"
		return action
	}
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		action.PathPresent = true
		if isGitCheckoutPath(path) {
			action.State = StatePresent
			action.Action = ActionNone
			action.CloneStrategy = clonestrategy.FullCloneSelection()
			return action
		}
		if info.IsDir() {
			empty, readErr := dirIsEmpty(path)
			if readErr != nil {
				action.State = StatePathConflict
				action.Action = ActionRefuse
				action.Reason = readErr.Error()
				return action
			}
			if empty {
				action.State = StateMissing
				action.Action = ActionClone
				return action
			}
			action.State = StatePathConflict
			action.Action = ActionRefuse
			action.Reason = "desired path exists and is not empty"
			return action
		}
		action.State = StatePathConflict
		action.Action = ActionRefuse
		action.Reason = "desired path exists and is not a directory"
		return action
	case errors.Is(err, os.ErrNotExist):
		if reason, blocked := parentConflictReason(path); blocked {
			action.State = StatePathConflict
			action.Action = ActionRefuse
			action.Reason = reason
			return action
		}
		action.State = StateMissing
		action.Action = ActionClone
		return action
	default:
		action.PathPresent = true
		action.State = StatePathConflict
		action.Action = ActionRefuse
		action.Reason = "desired path could not be checked safely: " + err.Error()
		return action
	}
}

func actionFromDrift(drift reconciliation.Drift, options clonestrategy.Options) Action {
	action := Action{
		Project:       drift.Alias,
		ProjectID:     drift.ProjectID,
		Identity:      drift.Identity,
		Path:          drift.DesiredLocalPath,
		ObservedPath:  drift.ObservedLocalPath,
		CanonicalPath: drift.DesiredLocalPath,
		CloneURL:      drift.CloneURL,
		Source:        "canonical",
		CloneStrategy: clonestrategy.SelectionForOptions(options),
		ProjectRow: state.Project{
			ID:               drift.ProjectID,
			Alias:            drift.Alias,
			NormalizedRemote: drift.Identity,
			CloneURL:         drift.CloneURL,
			LocalPath:        drift.DesiredLocalPath,
			CanonicalPath:    drift.DesiredLocalPath,
			Source:           "canonical",
		},
	}
	switch drift.Kind {
	case reconciliation.DriftUnchanged:
		action.State = StatePresent
		action.Action = ActionNone
		action.PathPresent = true
		action.CloneStrategy = clonestrategy.FullCloneSelection()
	case reconciliation.DriftMissing, reconciliation.DriftMoved:
		action.State = StateMissing
		action.Action = ActionClone
	case reconciliation.DriftConflicting:
		if strings.Contains(drift.Reason, "outside workspace root") {
			action.State = StateUnsafePath
		} else {
			action.State = StatePathConflict
		}
		action.Action = ActionRefuse
		action.PathPresent = true
		action.Reason = drift.Reason
	case reconciliation.DriftAdded:
		action.State = StatePresent
		action.Action = ActionNone
		action.Path = drift.ObservedLocalPath
		action.CanonicalPath = drift.DesiredLocalPath
		action.PathPresent = true
		action.CloneStrategy = clonestrategy.FullCloneSelection()
	default:
		action.State = StatePathConflict
		action.Action = ActionRefuse
		action.Reason = drift.Reason
	}
	if action.CloneURL == "" {
		action.CloneURL = action.Identity
	}
	action.ProjectRow.CloneURL = cloneURLForDrift(drift)
	action.CloneURL = redactedCloneURL(action.ProjectRow.CloneURL)
	return action
}

func sortActions(actions []Action) {
	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Project == actions[j].Project {
			return actions[i].Identity < actions[j].Identity
		}
		return actions[i].Project < actions[j].Project
	})
}

func withPlacementDefaults(project state.Project) state.Project {
	if strings.TrimSpace(project.CanonicalPath) == "" {
		project.CanonicalPath = project.LocalPath
	}
	if strings.TrimSpace(project.Source) == "canonical" {
		project.Source = "canonical"
	} else {
		project.Source = "local-only"
	}
	if project.CloneURL == "" {
		project.CloneURL = project.NormalizedRemote
	}
	return project
}

func hydrationPath(project state.Project) string {
	if project.Source == "canonical" && strings.TrimSpace(project.CanonicalPath) != "" {
		return project.CanonicalPath
	}
	return project.LocalPath
}

func cloneURLForProject(project state.Project) string {
	if strings.TrimSpace(project.CloneURL) != "" {
		return project.CloneURL
	}
	return project.NormalizedRemote
}

func cloneURLForDrift(drift reconciliation.Drift) string {
	if strings.TrimSpace(drift.CloneURL) != "" {
		return drift.CloneURL
	}
	return drift.Identity
}

func redactedCloneURL(raw string) string {
	return gitops.RedactURLForMetadata(raw)
}

func pathWithinRoot(root, path string) bool {
	return workspacemanifest.PathWithinRoot(root, path)
}

func isGitCheckoutPath(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

func dirIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read project path %q: %w", path, err)
	}
	return len(entries) == 0, nil
}

func parentConflictReason(path string) (string, bool) {
	for parent := filepath.Dir(path); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
		info, err := os.Stat(parent)
		switch {
		case err == nil:
			if !info.IsDir() {
				return fmt.Sprintf("desired path parent %q exists and is not a directory", parent), true
			}
			return "", false
		case errors.Is(err, os.ErrNotExist):
			next := filepath.Dir(parent)
			if next == parent {
				return "", false
			}
		default:
			return "desired path parent could not be checked safely: " + err.Error(), true
		}
	}
	return "", false
}
