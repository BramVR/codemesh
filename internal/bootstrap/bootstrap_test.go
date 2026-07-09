package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/hydrationexecutor"
	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

func TestApplyClonesMissingManifestProjectsThroughHydrationPlan(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	store := newMemoryStore(machine(workspace))
	alphaRemote := createBareRemote(t, "alpha")
	betaRemote := createBareRemote(t, "beta")
	entries := []workspacemanifest.Entry{
		manifestEntry("https://example.invalid/bram/alpha", "alpha", "tools/alpha", alphaRemote),
		manifestEntry("https://example.invalid/bram/beta", "beta", "beta", betaRemote),
	}

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), entries)
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	if result.Plan.WorkspaceRoot != workspace || result.Plan.Blocked {
		t.Fatalf("plan = %#v, want unblocked plan under workspace", result.Plan)
	}
	if !hasAppliedParent(result, filepath.Join(workspace, "tools")) || !hasAppliedParent(result, workspace) {
		t.Fatalf("applied parents = %#v, want workspace and tools", result.Applied.ParentDirectories)
	}
	for _, path := range []string{workspace, filepath.Join(workspace, "tools")} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("parent %s missing or not directory: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(workspace, "tools", "alpha"), filepath.Join(workspace, "beta")} {
		if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
			t.Fatalf("project checkout %s missing README: %v", path, err)
		}
	}
	if len(result.Applied.ClonedProjects) != 2 {
		t.Fatalf("cloned projects = %#v, want alpha and beta", result.Applied.ClonedProjects)
	}
	if len(store.projects) != 2 {
		t.Fatalf("project rows = %#v, want two missing registry rows", store.projects)
	}
	if store.projects[0].Alias != "alpha" || store.projects[0].LocalPath != filepath.Join(workspace, "tools", "alpha") || store.projects[0].CloneURL != alphaRemote {
		t.Fatalf("first project row = %#v", store.projects[0])
	}
	if store.projects[1].Alias != "beta" || store.projects[1].LocalPath != filepath.Join(workspace, "beta") || store.projects[1].CloneURL != betaRemote {
		t.Fatalf("second project row = %#v", store.projects[1])
	}
}

func TestApplyReplansAfterImportToUsePreservedRegistryCloneURL(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	target := filepath.Join(workspace, "tools", "alpha")
	remote := createBareRemote(t, "preserved-alpha")
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{{
		ID:               1,
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}}
	store.nextID = 2

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntryWithoutCloneURL("https://example.invalid/bram/alpha", "alpha", "tools/alpha"),
	})
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	if len(result.Applied.ClonedProjects) != 1 || result.Applied.ClonedProjects[0].Path != target {
		t.Fatalf("cloned projects = %#v, want alpha at %s", result.Applied.ClonedProjects, target)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("preserved clone URL was not used to hydrate target: %v", err)
	}
}

func TestApplyCreatesWorkspaceRootForRootDesiredPath(t *testing.T) {
	temp := t.TempDir()
	workspace := filepath.Join(temp, "workspace")
	rootRemote := createBareRemote(t, "root")
	parentMarker := filepath.Join(temp, "parent-marker")
	if err := os.WriteFile(parentMarker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://example.invalid/bram/root", "root", ".", rootRemote),
	})
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	if !hasAppliedParent(result, workspace) {
		t.Fatalf("applied parents = %#v, want workspace root", result.Applied.ParentDirectories)
	}
	if hasAppliedParent(result, temp) {
		t.Fatalf("applied parents = %#v, must not include workspace parent", result.Applied.ParentDirectories)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("workspace root missing or not directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "README.md")); err != nil {
		t.Fatalf("bootstrap root checkout missing README: %v", err)
	}
	if got, err := os.ReadFile(parentMarker); err != nil || string(got) != "keep\n" {
		t.Fatalf("parent marker changed or missing: got %q err %v", got, err)
	}
	if len(store.projects) != 1 || store.projects[0].LocalPath != workspace {
		t.Fatalf("project rows = %#v, want root project row at workspace", store.projects)
	}
}

func TestApplyRefusesPathConflictWithoutMutation(t *testing.T) {
	workspace := t.TempDir()
	conflictPath := filepath.Join(workspace, "tools", "alpha")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(conflictPath, "local.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "tools/alpha", "https://github.com/BramVR/alpha.git"),
	})
	if err == nil {
		t.Fatal("Apply error = nil, want blocker")
	}
	var blocked BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply error = %T %v, want BlockedError", err, err)
	}
	if !result.Plan.Blocked || len(result.Plan.Blockers) != 1 || result.Plan.Blockers[0].Kind != reconciliation.BlockerPathConflict {
		t.Fatalf("plan = %#v, want path conflict blocker", result.Plan)
	}
	if len(store.projects) != 0 {
		t.Fatalf("project rows = %#v, want no mutation", store.projects)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep\n" {
		t.Fatalf("conflict marker changed or missing: got %q err %v", got, err)
	}
}

func TestEnsurePlaceholderDestinationWithinRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(link, "alpha")

	err := ensurePlaceholderDestinationWithinRoot(workspace, destination)
	if err == nil {
		t.Fatal("ensurePlaceholderDestinationWithinRoot error = nil, want unsafe path")
	}
	var unsafe hydrationexecutor.UnsafePathError
	if !errors.As(err, &unsafe) {
		t.Fatalf("ensurePlaceholderDestinationWithinRoot error = %T %v, want UnsafePathError", err, err)
	}
	if unsafe.Path != destination {
		t.Fatalf("unsafe path = %q, want %q", unsafe.Path, destination)
	}
}

func TestPlaceholdersRegistryRefusesOverlappingPathsBeforeMutation(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	parentPath := filepath.Join(workspace, "tools", "parent")
	childPath := filepath.Join(parentPath, "child")
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{
		{
			ID:               1,
			Alias:            "parent",
			NormalizedRemote: "https://example.invalid/bram/parent",
			CloneURL:         "https://example.invalid/bram/parent.git",
			LocalPath:        parentPath,
			CanonicalPath:    parentPath,
			Source:           "canonical",
		},
		{
			ID:               2,
			Alias:            "child",
			NormalizedRemote: "https://example.invalid/bram/child",
			CloneURL:         "https://example.invalid/bram/child.git",
			LocalPath:        childPath,
			CanonicalPath:    childPath,
			Source:           "canonical",
		},
	}
	store.nextID = 3

	result, err := Bootstrapper{Store: store}.PlaceholdersRegistry(context.Background(), nil, true)
	if err == nil {
		t.Fatal("PlaceholdersRegistry error = nil, want overlapping path blocker")
	}
	var blocked HydrationBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("PlaceholdersRegistry error = %T %v, want HydrationBlockedError", err, err)
	}
	if !result.HydrationPlan.Blocked || len(result.HydrationPlan.Actions) == 0 {
		t.Fatalf("hydration plan = %#v, want blocked path-conflict action", result.HydrationPlan)
	}
	if _, err := os.Stat(parentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parent placeholder was created before overlap refusal: %v", err)
	}
	if _, err := os.Stat(childPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child placeholder was created before overlap refusal: %v", err)
	}
}

func TestPlaceholdersRegistryRefusesPlaceholderInsidePresentCheckout(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	parentPath := filepath.Join(workspace, "tools", "parent")
	childPath := filepath.Join(parentPath, "child")
	if err := os.MkdirAll(filepath.Join(parentPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{
		{
			ID:               1,
			Alias:            "parent",
			NormalizedRemote: "https://example.invalid/bram/parent",
			CloneURL:         "https://example.invalid/bram/parent.git",
			LocalPath:        parentPath,
			CanonicalPath:    parentPath,
			Source:           "canonical",
		},
		{
			ID:               2,
			Alias:            "child",
			NormalizedRemote: "https://example.invalid/bram/child",
			CloneURL:         "https://example.invalid/bram/child.git",
			LocalPath:        childPath,
			CanonicalPath:    childPath,
			Source:           "canonical",
		},
	}
	store.nextID = 3

	result, err := Bootstrapper{Store: store}.PlaceholdersRegistry(context.Background(), nil, true)
	if err == nil {
		t.Fatal("PlaceholdersRegistry error = nil, want present checkout overlap blocker")
	}
	var blocked HydrationBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("PlaceholdersRegistry error = %T %v, want HydrationBlockedError", err, err)
	}
	if !result.HydrationPlan.Blocked {
		t.Fatalf("hydration plan = %#v, want blocked path-conflict action", result.HydrationPlan)
	}
	if _, err := os.Stat(childPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child placeholder was created inside present checkout: %v", err)
	}
}

func TestPlaceholdersRegistryPersistsCanonicalPlacement(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	observedPath := filepath.Join(workspace, "old-alpha")
	canonicalPath := filepath.Join(workspace, "tools", "alpha")
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{{
		ID:               1,
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         "https://example.invalid/bram/alpha.git",
		LocalPath:        observedPath,
		CanonicalPath:    canonicalPath,
		Source:           "canonical",
	}}
	store.nextID = 2

	result, err := Bootstrapper{Store: store}.PlaceholdersRegistry(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("PlaceholdersRegistry error = %v", err)
	}
	if len(result.Applied.PlaceholderProjects) != 1 || result.Applied.PlaceholderProjects[0].Path != canonicalPath {
		t.Fatalf("placeholder projects = %#v, want alpha at canonical path", result.Applied.PlaceholderProjects)
	}
	if len(result.Applied.UpdatedProjects) != 1 || result.Applied.UpdatedProjects[0].LocalPath != canonicalPath {
		t.Fatalf("updated projects = %#v, want canonical placement persisted", result.Applied.UpdatedProjects)
	}
	if store.projects[0].LocalPath != canonicalPath || store.projects[0].CanonicalPath != canonicalPath {
		t.Fatalf("project row = %#v, want canonical path persisted", store.projects[0])
	}
	if _, err := os.Stat(filepath.Join(canonicalPath, ".codemesh-placeholder.json")); err != nil {
		t.Fatalf("placeholder metadata missing at canonical path: %v", err)
	}
	if _, err := os.Stat(observedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("observed path mutated unexpectedly: %v", err)
	}
}

func TestPlaceholdersRefusesSymlinkEscapeBeforeImportMutation(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Placeholders(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://example.invalid/bram/alpha", "alpha", "link/alpha", "https://example.invalid/bram/alpha.git"),
	})
	if err == nil {
		t.Fatal("Placeholders error = nil, want unsafe path blocker")
	}
	var hydrationBlocked HydrationBlockedError
	var planBlocked BlockedError
	if !errors.As(err, &hydrationBlocked) && !errors.As(err, &planBlocked) {
		t.Fatalf("Placeholders error = %T %v, want blocked placeholder refusal", err, err)
	}
	if !result.Plan.Blocked && !result.HydrationPlan.Blocked {
		t.Fatalf("result = %#v, want blocked unsafe path", result)
	}
	if len(store.projects) != 0 {
		t.Fatalf("project rows = %#v, want no import mutation", store.projects)
	}
	if _, err := os.Stat(filepath.Join(outside, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("placeholder escaped outside workspace or stat failed unexpectedly: %v", err)
	}
}

func TestPlaceholdersRegistryRefusesUnsafePathBeforeAnyPlaceholderWrite(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	safePath := filepath.Join(workspace, "alpha")
	unsafePath := filepath.Join(workspace, "link", "beta")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{
		{
			ID:               1,
			Alias:            "alpha",
			NormalizedRemote: "https://example.invalid/bram/alpha",
			CloneURL:         "https://example.invalid/bram/alpha.git",
			LocalPath:        safePath,
			CanonicalPath:    safePath,
			Source:           "canonical",
		},
		{
			ID:               2,
			Alias:            "beta",
			NormalizedRemote: "https://example.invalid/bram/beta",
			CloneURL:         "https://example.invalid/bram/beta.git",
			LocalPath:        unsafePath,
			CanonicalPath:    unsafePath,
			Source:           "canonical",
		},
	}
	store.nextID = 3

	result, err := Bootstrapper{Store: store}.PlaceholdersRegistry(context.Background(), nil, true)
	if err == nil {
		t.Fatal("PlaceholdersRegistry error = nil, want unsafe path blocker")
	}
	var blocked HydrationBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("PlaceholdersRegistry error = %T %v, want HydrationBlockedError", err, err)
	}
	if !result.HydrationPlan.Blocked {
		t.Fatalf("hydration plan = %#v, want blocked unsafe path", result.HydrationPlan)
	}
	if _, err := os.Stat(safePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("safe placeholder was written before unsafe refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "beta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe placeholder escaped outside workspace or stat failed unexpectedly: %v", err)
	}
}

func TestApplyRefusesNestedProjectPathsWithoutCreatingPlaceholderParent(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/parent", "parent", "tools/parent", "https://github.com/BramVR/parent.git"),
		manifestEntry("https://github.com/BramVR/nested", "nested", "tools/parent/nested", "https://github.com/BramVR/nested.git"),
	})
	if err == nil {
		t.Fatal("Apply error = nil, want nested path blocker")
	}
	var blocked BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply error = %T %v, want BlockedError", err, err)
	}
	if !result.Plan.Blocked {
		t.Fatalf("plan = %#v, want blocked", result.Plan)
	}
	if _, err := os.Stat(filepath.Join(workspace, "tools", "parent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nested bootstrap created parent project path or stat failed unexpectedly: %v", err)
	}
	if len(store.projects) != 0 {
		t.Fatalf("project rows = %#v, want no mutation", store.projects)
	}
}

func TestApplyUpdatesAliasBeforeAddingManifestRowThatReusesOldAlias(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	oldPath := filepath.Join(workspace, "tools", "alpha")
	store := newMemoryStore(machine(workspace))
	oldAlphaRemote := createBareRemote(t, "old-alpha")
	alphaRemote := createBareRemote(t, "alpha")
	store.projects = []state.Project{{
		ID:               10,
		Alias:            "old-alpha",
		NormalizedRemote: "https://github.com/BramVR/alpha",
		CloneURL:         alphaRemote,
		LocalPath:        oldPath,
	}}
	store.nextID = 11
	entries := []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/old-alpha", "old-alpha", "tools/old-alpha", oldAlphaRemote),
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "tools/alpha", alphaRemote),
	}

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), entries)
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	if len(result.Applied.UpdatedProjects) != 1 || result.Applied.UpdatedProjects[0].Alias != "alpha" {
		t.Fatalf("updated projects = %#v, want alpha alias update", result.Applied.UpdatedProjects)
	}
	if len(result.Applied.AddedProjects) != 1 || result.Applied.AddedProjects[0].Alias != "old-alpha" {
		t.Fatalf("added projects = %#v, want old alias reused by new identity", result.Applied.AddedProjects)
	}
	if store.projects[0].Alias != "alpha" || store.projects[1].Alias != "old-alpha" {
		t.Fatalf("project rows = %#v", store.projects)
	}
}

func TestApplySwapsExistingAliasesWithoutUniqueConstraintFailure(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	alphaRemote := createBareRemote(t, "swap-alpha")
	betaRemote := createBareRemote(t, "swap-beta")
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{
		{
			ID:               1,
			Alias:            "alpha",
			NormalizedRemote: "https://github.com/BramVR/alpha",
			CloneURL:         alphaRemote,
			LocalPath:        filepath.Join(workspace, "alpha"),
		},
		{
			ID:               2,
			Alias:            "beta",
			NormalizedRemote: "https://github.com/BramVR/beta",
			CloneURL:         betaRemote,
			LocalPath:        filepath.Join(workspace, "beta"),
		},
	}
	store.nextID = 3

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/alpha", "beta", "alpha", alphaRemote),
		manifestEntry("https://github.com/BramVR/beta", "alpha", "beta", betaRemote),
	})
	if err != nil {
		t.Fatalf("Apply error = %v", err)
	}

	if len(result.Applied.UpdatedProjects) != 2 {
		t.Fatalf("updated projects = %#v, want two alias updates", result.Applied.UpdatedProjects)
	}
	if store.projects[0].Alias != "beta" || store.projects[1].Alias != "alpha" {
		t.Fatalf("project rows = %#v, want swapped aliases", store.projects)
	}
}

func TestPlanRequiresRegisteredMachineWorkspaceRoot(t *testing.T) {
	_, err := Bootstrapper{Store: newMemoryStore()}.Plan(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "alpha", "https://github.com/BramVR/alpha.git"),
	})
	if err == nil || !strings.Contains(err.Error(), "registered machine") {
		t.Fatalf("Plan error = %v, want registered machine requirement", err)
	}
}

func manifestEntry(identity, alias, desiredPath, cloneURL string) workspacemanifest.Entry {
	return workspacemanifest.NewEntry(workspacemanifest.ProjectEntry{
		Identity:    identity,
		Alias:       alias,
		DesiredPath: desiredPath,
		CloneHints:  workspacemanifest.CloneHints{URL: cloneURL},
	})
}

func manifestEntryWithoutCloneURL(identity, alias, desiredPath string) workspacemanifest.Entry {
	return workspacemanifest.NewEntry(workspacemanifest.ProjectEntry{
		Identity:    identity,
		Alias:       alias,
		DesiredPath: desiredPath,
	})
}

func machine(workspace string) state.Machine {
	now := time.Now().UTC()
	return state.Machine{
		ID:            "machine-test",
		Hostname:      "host",
		OS:            "testos",
		Architecture:  "testarch",
		WorkspaceRoot: workspace,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

type memoryStore struct {
	machines []state.Machine
	projects []state.Project
	nextID   int64
}

func newMemoryStore(machines ...state.Machine) *memoryStore {
	return &memoryStore{machines: machines, nextID: 1}
}

func (s *memoryStore) ListMachines(context.Context) ([]state.Machine, error) {
	return append([]state.Machine(nil), s.machines...), nil
}

func (s *memoryStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s *memoryStore) AddProject(_ context.Context, project state.Project) (state.Project, error) {
	for _, existing := range s.projects {
		if existing.Alias == project.Alias {
			return state.Project{}, state.ErrAliasConflict
		}
		if existing.NormalizedRemote == project.NormalizedRemote {
			return state.Project{}, state.ErrRemoteConflict
		}
	}
	project.ID = s.nextID
	s.nextID++
	s.projects = append(s.projects, project)
	return project, nil
}

func (s *memoryStore) UpdateProject(_ context.Context, id int64, project state.Project) (state.Project, error) {
	for i := range s.projects {
		if s.projects[i].ID == id {
			for _, existing := range s.projects {
				if existing.ID != id && existing.Alias == project.Alias {
					return state.Project{}, state.ErrAliasConflict
				}
				if existing.ID != id && existing.NormalizedRemote == project.NormalizedRemote {
					return state.Project{}, state.ErrRemoteConflict
				}
			}
			project.ID = id
			s.projects[i] = project
			return project, nil
		}
	}
	return state.Project{}, errors.New("not found")
}

func (s *memoryStore) DeleteProject(_ context.Context, id int64) error {
	for i := range s.projects {
		if s.projects[i].ID == id {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return nil
		}
	}
	return errors.New("not found")
}

func hasAppliedParent(result Result, path string) bool {
	for _, parent := range result.Applied.ParentDirectories {
		if parent == path {
			return true
		}
	}
	return false
}

func createBareRemote(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, name+"-seed")
	remote := filepath.Join(root, name+".git")
	runGit(t, root, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial commit")
	runGit(t, root, "clone", "--bare", seed, remote)
	return remote
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
}
