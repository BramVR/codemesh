package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

func TestApplyCreatesParentsAndRegistryRowsWithoutProjectDirectories(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	store := newMemoryStore(machine(workspace))
	entries := []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "tools/alpha"),
		manifestEntry("https://github.com/BramVR/beta", "beta", "beta"),
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
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("project path %s was created or stat failed unexpectedly: %v", path, err)
		}
	}
	if len(store.projects) != 2 {
		t.Fatalf("project rows = %#v, want two missing registry rows", store.projects)
	}
	if store.projects[0].Alias != "alpha" || store.projects[0].LocalPath != filepath.Join(workspace, "tools", "alpha") {
		t.Fatalf("first project row = %#v", store.projects[0])
	}
	if store.projects[1].Alias != "beta" || store.projects[1].LocalPath != filepath.Join(workspace, "beta") {
		t.Fatalf("second project row = %#v", store.projects[1])
	}
}

func TestApplyCreatesWorkspaceRootForRootDesiredPath(t *testing.T) {
	temp := t.TempDir()
	workspace := filepath.Join(temp, "workspace")
	parentMarker := filepath.Join(temp, "parent-marker")
	if err := os.WriteFile(parentMarker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/root", "root", "."),
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
	if _, err := os.Stat(filepath.Join(workspace, "root")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bootstrap created project placeholder under root desired path: %v", err)
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
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "tools/alpha"),
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

func TestApplyRefusesNestedProjectPathsWithoutCreatingPlaceholderParent(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	store := newMemoryStore(machine(workspace))

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/parent", "parent", "tools/parent"),
		manifestEntry("https://github.com/BramVR/nested", "nested", "tools/parent/nested"),
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
	store.projects = []state.Project{{
		ID:               10,
		Alias:            "old-alpha",
		NormalizedRemote: "https://github.com/BramVR/alpha",
		CloneURL:         "https://github.com/BramVR/alpha.git",
		LocalPath:        oldPath,
	}}
	store.nextID = 11
	entries := []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/old-alpha", "old-alpha", "tools/old-alpha"),
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "tools/alpha"),
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
	store := newMemoryStore(machine(workspace))
	store.projects = []state.Project{
		{
			ID:               1,
			Alias:            "alpha",
			NormalizedRemote: "https://github.com/BramVR/alpha",
			CloneURL:         "https://github.com/BramVR/alpha.git",
			LocalPath:        filepath.Join(workspace, "alpha"),
		},
		{
			ID:               2,
			Alias:            "beta",
			NormalizedRemote: "https://github.com/BramVR/beta",
			CloneURL:         "https://github.com/BramVR/beta.git",
			LocalPath:        filepath.Join(workspace, "beta"),
		},
	}
	store.nextID = 3

	result, err := Bootstrapper{Store: store}.Apply(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/alpha", "beta", "alpha"),
		manifestEntry("https://github.com/BramVR/beta", "alpha", "beta"),
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
		manifestEntry("https://github.com/BramVR/alpha", "alpha", "alpha"),
	})
	if err == nil || !strings.Contains(err.Error(), "registered machine") {
		t.Fatalf("Plan error = %v, want registered machine requirement", err)
	}
}

func manifestEntry(identity, alias, desiredPath string) workspacemanifest.Entry {
	return workspacemanifest.NewEntry(workspacemanifest.ProjectEntry{
		Identity:    identity,
		Alias:       alias,
		DesiredPath: desiredPath,
		CloneHints:  workspacemanifest.CloneHints{URL: identity + ".git"},
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
