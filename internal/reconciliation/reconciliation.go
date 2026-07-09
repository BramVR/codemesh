package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	ListMachines(context.Context) ([]state.Machine, error)
}

type Reconciler struct {
	store Store
}

type DriftKind string

const (
	DriftAdded       DriftKind = "added"
	DriftMissing     DriftKind = "missing"
	DriftMoved       DriftKind = "moved"
	DriftConflicting DriftKind = "conflicting"
	DriftUnchanged   DriftKind = "unchanged"
)

type BlockerKind string

const (
	BlockerPathConflict     BlockerKind = "path-conflict"
	BlockerAliasConflict    BlockerKind = "alias-conflict"
	BlockerManifestConflict BlockerKind = "manifest-conflict"
)

type DriftPlan struct {
	WorkspaceRoot string    `json:"workspace_root"`
	Blocked       bool      `json:"blocked"`
	Drifts        []Drift   `json:"drifts"`
	Blockers      []Blocker `json:"blockers"`
}

type Drift struct {
	Kind              DriftKind `json:"kind"`
	ProjectID         int64     `json:"project_id,omitempty"`
	Identity          string    `json:"identity"`
	Alias             string    `json:"alias"`
	DesiredPath       string    `json:"desired_path"`
	DesiredLocalPath  string    `json:"desired_local_path"`
	ObservedLocalPath string    `json:"observed_local_path,omitempty"`
	CloneURL          string    `json:"clone_url,omitempty"`
	Reason            string    `json:"reason,omitempty"`
}

type Blocker struct {
	Kind     BlockerKind `json:"kind"`
	Identity string      `json:"identity,omitempty"`
	Alias    string      `json:"alias,omitempty"`
	Path     string      `json:"path,omitempty"`
	Reason   string      `json:"reason"`
}

func New(store Store) Reconciler {
	return Reconciler{store: store}
}

func (r Reconciler) DryRun(ctx context.Context, entries []workspacemanifest.Entry) (DriftPlan, error) {
	if r.store == nil {
		return DriftPlan{}, errors.New("reconciliation store is required")
	}
	machines, err := r.store.ListMachines(ctx)
	if err != nil {
		return DriftPlan{}, err
	}
	machine, err := localMachine(machines)
	if err != nil {
		return DriftPlan{}, err
	}
	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return DriftPlan{}, err
	}
	return BuildDryRunPlan(entries, projects, machine)
}

func BuildDryRunPlan(entries []workspacemanifest.Entry, projects []state.Project, machine state.Machine) (DriftPlan, error) {
	root, err := cleanWorkspaceRoot(machine.WorkspaceRoot)
	if err != nil {
		return DriftPlan{}, err
	}
	desiredEntries, err := normalizeEntries(entries)
	if err != nil {
		return DriftPlan{}, err
	}
	desiredAliasByIdentity := make(map[string]string, len(desiredEntries))
	desiredIdentity := make(map[string]bool, len(desiredEntries))
	for _, entry := range desiredEntries {
		if _, exists := desiredAliasByIdentity[entry.Project.Identity]; !exists {
			desiredAliasByIdentity[entry.Project.Identity] = entry.Project.Alias
		}
		desiredIdentity[entry.Project.Identity] = true
	}

	byIdentity := make(map[string]state.Project, len(projects))
	byPath := make(map[string]state.Project, len(projects))
	aliasOwner := make(map[string]string, len(projects))
	for _, project := range projects {
		byIdentity[project.NormalizedRemote] = project
		projectInManifest := desiredIdentity[project.NormalizedRemote]
		canonicalPath := projectCanonicalPath(project)
		if !projectInManifest && strings.TrimSpace(canonicalPath) != "" {
			byPath[cleanPath(canonicalPath)] = project
		}
		if strings.TrimSpace(project.LocalPath) != "" && (!projectInManifest || pathExistsOrUnknown(project.LocalPath)) {
			byPath[cleanPath(project.LocalPath)] = project
		}
		if desiredAlias, ok := desiredAliasByIdentity[project.NormalizedRemote]; ok && desiredAlias != project.Alias {
			continue
		}
		aliasOwner[project.Alias] = project.NormalizedRemote
	}

	plan := DriftPlan{WorkspaceRoot: root}
	compareRoot := workspacemanifest.CanonicalPathForComparison(root)
	seenDesiredIdentities := map[string]bool{}
	seenDesiredPaths := map[string]string{}
	for _, project := range projects {
		projectInManifest := desiredIdentity[project.NormalizedRemote]
		if strings.TrimSpace(project.LocalPath) != "" && (!projectInManifest || pathExistsOrUnknown(project.LocalPath)) {
			seenDesiredPaths[cleanPath(project.LocalPath)] = project.NormalizedRemote
		}
		canonicalPath := projectCanonicalPath(project)
		if projectInManifest || strings.TrimSpace(canonicalPath) == "" {
			continue
		}
		seenDesiredPaths[cleanPath(canonicalPath)] = project.NormalizedRemote
	}
	for _, entry := range desiredEntries {
		project := entry.Project
		desiredLocalPath := filepath.Join(root, filepath.FromSlash(project.DesiredPath))
		desiredLocalPath = filepath.Clean(desiredLocalPath)
		desiredComparePath := workspacemanifest.CanonicalPathForComparison(desiredLocalPath)
		drift := Drift{
			Identity:         project.Identity,
			Alias:            project.Alias,
			DesiredPath:      project.DesiredPath,
			DesiredLocalPath: desiredLocalPath,
			CloneURL:         cloneURLFor(project),
		}

		if seenDesiredIdentities[project.Identity] {
			plan.addConflict(drift, BlockerManifestConflict, desiredLocalPath, fmt.Sprintf("identity %q appears more than once in the manifest", project.Identity))
			continue
		}
		seenDesiredIdentities[project.Identity] = true
		if !workspacemanifest.PathWithinRoot(compareRoot, desiredComparePath) {
			plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, "desired path resolves outside workspace root")
			continue
		}
		aliasOwnerIdentity, aliasTaken := aliasOwner[project.Alias]
		aliasConflict := aliasTaken && aliasOwnerIdentity != project.Identity
		pathOwnerIdentity, pathTaken := seenDesiredPaths[desiredComparePath]
		pathConflict := pathTaken && pathOwnerIdentity != project.Identity
		nestedPathOwnerIdentity, nestedPath, nestedPathConflict := workspacemanifest.NestedDesiredPathConflict(seenDesiredPaths, desiredComparePath, project.Identity)
		if !aliasConflict {
			aliasOwner[project.Alias] = project.Identity
		}
		if !pathConflict && !nestedPathConflict {
			seenDesiredPaths[desiredComparePath] = project.Identity
		}
		if aliasConflict {
			plan.addConflict(drift, BlockerAliasConflict, "", fmt.Sprintf("alias %q already belongs to %q", project.Alias, aliasOwnerIdentity))
			continue
		}
		if pathConflict {
			plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, fmt.Sprintf("desired path already requested by %q", pathOwnerIdentity))
			continue
		}
		if nestedPathConflict {
			plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, fmt.Sprintf("desired path nests with project path %q owned by %q", nestedPath, nestedPathOwnerIdentity))
			continue
		}

		observed, exists := byIdentity[project.Identity]
		if !exists {
			if owner, ok := byPath[desiredComparePath]; ok && owner.NormalizedRemote != project.Identity {
				plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, fmt.Sprintf("desired path is already registered to %q", owner.NormalizedRemote))
				continue
			}
			if reason, blocked := workspacemanifest.PathConflictReason(desiredLocalPath); blocked {
				plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, reason)
				continue
			}
			drift.Kind = DriftMissing
			plan.Drifts = append(plan.Drifts, drift)
			continue
		}

		drift.ProjectID = observed.ID
		drift.ObservedLocalPath = observed.LocalPath
		observedPath := cleanPath(projectCanonicalPath(observed))
		localPath := cleanPath(observed.LocalPath)
		if observedPath == desiredComparePath {
			drift.Kind = DriftUnchanged
			plan.Drifts = append(plan.Drifts, drift)
			continue
		}
		if owner, ok := byPath[desiredComparePath]; ok && owner.NormalizedRemote != project.Identity {
			plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, fmt.Sprintf("desired path is already registered to %q", owner.NormalizedRemote))
			continue
		}
		if localPath != desiredComparePath {
			if reason, blocked := workspacemanifest.PathConflictReason(desiredLocalPath); blocked {
				plan.addConflict(drift, BlockerPathConflict, desiredLocalPath, reason)
				continue
			}
		}
		drift.Kind = DriftMoved
		plan.Drifts = append(plan.Drifts, drift)
	}

	for _, project := range sortedProjects(projects) {
		if desiredIdentity[project.NormalizedRemote] {
			continue
		}
		plan.Drifts = append(plan.Drifts, Drift{
			Kind:              DriftAdded,
			ProjectID:         project.ID,
			Identity:          project.NormalizedRemote,
			Alias:             project.Alias,
			DesiredLocalPath:  filepath.Clean(projectCanonicalPath(project)),
			ObservedLocalPath: project.LocalPath,
			Reason:            "project is present in the local Project Registry but absent from the Workspace Manifest",
		})
	}

	return plan, nil
}

func (p *DriftPlan) addConflict(drift Drift, kind BlockerKind, path, reason string) {
	drift.Kind = DriftConflicting
	drift.Reason = reason
	p.Drifts = append(p.Drifts, drift)
	p.Blockers = append(p.Blockers, Blocker{
		Kind:     kind,
		Identity: drift.Identity,
		Alias:    drift.Alias,
		Path:     path,
		Reason:   reason,
	})
	p.Blocked = true
}

func localMachine(machines []state.Machine) (state.Machine, error) {
	if len(machines) == 0 {
		return state.Machine{}, errors.New("registered machine is required for reconciliation")
	}
	return machines[0], nil
}

func normalizeEntries(entries []workspacemanifest.Entry) ([]workspacemanifest.Entry, error) {
	normalized := make([]workspacemanifest.Entry, 0, len(entries))
	for _, entry := range entries {
		normalizedEntry := workspacemanifest.NewEntry(entry.Project)
		if entry.ManifestVersion != 0 {
			normalizedEntry.ManifestVersion = entry.ManifestVersion
		}
		if err := workspacemanifest.ValidateEntry(normalizedEntry); err != nil {
			return nil, err
		}
		normalized = append(normalized, normalizedEntry)
	}
	return normalized, nil
}

func cleanWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("machine workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve machine workspace root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	return workspacemanifest.CanonicalPathForComparison(path)
}

func projectCanonicalPath(project state.Project) string {
	if strings.TrimSpace(project.CanonicalPath) != "" {
		return project.CanonicalPath
	}
	return project.LocalPath
}

func pathExistsOrUnknown(path string) bool {
	_, err := os.Lstat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func cloneURLFor(project workspacemanifest.ProjectEntry) string {
	if project.CloneHints.URL != "" {
		return project.CloneHints.URL
	}
	return project.Identity
}

func sortedProjects(projects []state.Project) []state.Project {
	sorted := append([]state.Project(nil), projects...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].LocalPath == sorted[j].LocalPath {
			return sorted[i].NormalizedRemote < sorted[j].NormalizedRemote
		}
		return sorted[i].LocalPath < sorted[j].LocalPath
	})
	return sorted
}
