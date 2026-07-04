package reconciliation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

func TestDryRunPlanReportsWorkspaceDriftWithoutMutating(t *testing.T) {
	workspace := t.TempDir()
	entries := []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/unchanged", "unchanged", "src/unchanged"),
		manifestEntry("https://github.com/BramVR/missing", "missing", "src/missing"),
		manifestEntry("https://github.com/BramVR/moved", "moved", "src/moved"),
		manifestEntry("https://github.com/BramVR/conflict", "conflict", "src/conflict"),
	}
	projects := []state.Project{
		project(1, "unchanged", "https://github.com/BramVR/unchanged", filepath.Join(workspace, "src", "unchanged")),
		project(2, "moved", "https://github.com/BramVR/moved", filepath.Join(workspace, "legacy", "moved")),
		project(3, "conflict-owner", "https://github.com/BramVR/conflict-owner", filepath.Join(workspace, "src", "conflict")),
		project(4, "local-only", "https://github.com/BramVR/local-only", filepath.Join(workspace, "src", "local-only")),
	}

	plan, err := BuildDryRunPlan(entries, projects, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/unchanged", DriftUnchanged, filepath.Join(workspace, "src", "unchanged"))
	assertDrift(t, plan, "https://github.com/BramVR/missing", DriftMissing, filepath.Join(workspace, "src", "missing"))
	assertDrift(t, plan, "https://github.com/BramVR/moved", DriftMoved, filepath.Join(workspace, "src", "moved"))
	assertDrift(t, plan, "https://github.com/BramVR/conflict", DriftConflicting, filepath.Join(workspace, "src", "conflict"))
	assertDrift(t, plan, "https://github.com/BramVR/local-only", DriftAdded, filepath.Join(workspace, "src", "local-only"))
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want path conflict blocker")
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0].Kind != BlockerPathConflict {
		t.Fatalf("Blockers = %#v, want one path conflict", plan.Blockers)
	}
}

func TestDryRunPlanBlocksFilesystemPathConflictWithoutWriting(t *testing.T) {
	workspace := t.TempDir()
	conflictPath := filepath.Join(workspace, "src", "taken")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(conflictPath, "local.txt")
	if err := os.WriteFile(markerPath, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/taken", "taken", "src/taken"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/taken", DriftConflicting, conflictPath)
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want filesystem path conflict blocker")
	}
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("marker missing after dry run: %v", err)
	}
	if string(data) != "keep me\n" {
		t.Fatalf("marker changed after dry run: %q", data)
	}
}

func TestDryRunPlanBlocksDanglingSymlinkPathConflict(t *testing.T) {
	workspace := t.TempDir()
	conflictPath := filepath.Join(workspace, "src", "dangling")
	if err := os.MkdirAll(filepath.Dir(conflictPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "missing-target"), conflictPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/dangling", "dangling", "src/dangling"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/dangling", DriftConflicting, conflictPath)
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want dangling symlink path conflict")
	}
}

func TestDryRunPlanBlocksNonDirectoryParentPathConflict(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Join(workspace, "src")
	if err := os.WriteFile(parent, []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desiredPath := filepath.Join(parent, "repo")

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/repo", "repo", "src/repo"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/repo", DriftConflicting, desiredPath)
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want non-directory parent conflict")
	}
}

func TestDryRunPlanBlocksManifestAliasConflict(t *testing.T) {
	workspace := t.TempDir()

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/first", "shared", "first"),
		manifestEntry("https://github.com/BramVR/second", "shared", "second"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/first", DriftMissing, filepath.Join(workspace, "first"))
	assertDrift(t, plan, "https://github.com/BramVR/second", DriftConflicting, filepath.Join(workspace, "second"))
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want manifest alias conflict blocker")
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0].Kind != BlockerAliasConflict {
		t.Fatalf("Blockers = %#v, want one alias conflict", plan.Blockers)
	}
}

func TestDryRunPlanReservesAliasAfterDifferentConflict(t *testing.T) {
	workspace := t.TempDir()
	conflictPath := filepath.Join(workspace, "taken")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/first", "shared", "taken"),
		manifestEntry("https://github.com/BramVR/second", "shared", "second"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/first", DriftConflicting, conflictPath)
	assertDrift(t, plan, "https://github.com/BramVR/second", DriftConflicting, filepath.Join(workspace, "second"))
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want path and alias blockers")
	}
	if !hasBlocker(plan, BlockerPathConflict, "https://github.com/BramVR/first") {
		t.Fatalf("Blockers = %#v, want path conflict for first alias claimant", plan.Blockers)
	}
	if !hasBlocker(plan, BlockerAliasConflict, "https://github.com/BramVR/second") {
		t.Fatalf("Blockers = %#v, want alias conflict for later alias reuse", plan.Blockers)
	}
}

func TestDryRunPlanReservesAliasAfterManifestPathConflict(t *testing.T) {
	workspace := t.TempDir()

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/first", "first", "shared-path"),
		manifestEntry("https://github.com/BramVR/second", "shared", "shared-path"),
		manifestEntry("https://github.com/BramVR/third", "shared", "third"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/first", DriftMissing, filepath.Join(workspace, "shared-path"))
	assertDrift(t, plan, "https://github.com/BramVR/second", DriftConflicting, filepath.Join(workspace, "shared-path"))
	assertDrift(t, plan, "https://github.com/BramVR/third", DriftConflicting, filepath.Join(workspace, "third"))
	if !hasBlocker(plan, BlockerPathConflict, "https://github.com/BramVR/second") {
		t.Fatalf("Blockers = %#v, want manifest path conflict for second", plan.Blockers)
	}
	if !hasBlocker(plan, BlockerAliasConflict, "https://github.com/BramVR/third") {
		t.Fatalf("Blockers = %#v, want alias conflict for third", plan.Blockers)
	}
}

func TestDryRunPlanReservesPathAfterManifestAliasConflict(t *testing.T) {
	workspace := t.TempDir()

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/first", "shared", "one"),
		manifestEntry("https://github.com/BramVR/second", "shared", "shared-path"),
		manifestEntry("https://github.com/BramVR/third", "third", "shared-path"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/first", DriftMissing, filepath.Join(workspace, "one"))
	assertDrift(t, plan, "https://github.com/BramVR/second", DriftConflicting, filepath.Join(workspace, "shared-path"))
	assertDrift(t, plan, "https://github.com/BramVR/third", DriftConflicting, filepath.Join(workspace, "shared-path"))
	if !hasBlocker(plan, BlockerAliasConflict, "https://github.com/BramVR/second") {
		t.Fatalf("Blockers = %#v, want alias conflict for second", plan.Blockers)
	}
	if !hasBlocker(plan, BlockerPathConflict, "https://github.com/BramVR/third") {
		t.Fatalf("Blockers = %#v, want manifest path conflict for third", plan.Blockers)
	}
}

func TestDryRunPlanBlocksNestedManifestProjectPaths(t *testing.T) {
	workspace := t.TempDir()

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/parent", "parent", "tools/parent"),
		manifestEntry("https://github.com/BramVR/nested", "nested", "tools/parent/nested"),
	}, nil, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/parent", DriftMissing, filepath.Join(workspace, "tools", "parent"))
	assertDrift(t, plan, "https://github.com/BramVR/nested", DriftConflicting, filepath.Join(workspace, "tools", "parent", "nested"))
	if !plan.Blocked || !hasBlocker(plan, BlockerPathConflict, "https://github.com/BramVR/nested") {
		t.Fatalf("plan = %#v, want nested path conflict blocker", plan)
	}
}

func TestDryRunPlanBlocksNestedPathAgainstExistingRegistryProject(t *testing.T) {
	workspace := t.TempDir()
	projects := []state.Project{
		project(1, "parent", "https://github.com/BramVR/parent", filepath.Join(workspace, "tools", "parent")),
	}

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/nested", "nested", "tools/parent/nested"),
	}, projects, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/nested", DriftConflicting, filepath.Join(workspace, "tools", "parent", "nested"))
	if !plan.Blocked || !hasBlocker(plan, BlockerPathConflict, "https://github.com/BramVR/nested") {
		t.Fatalf("plan = %#v, want nested registry path conflict blocker", plan)
	}
}

func TestDryRunPlanAllowsAliasReuseAfterDesiredRename(t *testing.T) {
	workspace := t.TempDir()
	projects := []state.Project{
		project(1, "old", "https://github.com/BramVR/first", filepath.Join(workspace, "first")),
	}

	plan, err := BuildDryRunPlan([]workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/second", "old", "second"),
		manifestEntry("https://github.com/BramVR/first", "new", "first"),
	}, projects, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}

	assertDrift(t, plan, "https://github.com/BramVR/second", DriftMissing, filepath.Join(workspace, "second"))
	assertDrift(t, plan, "https://github.com/BramVR/first", DriftUnchanged, filepath.Join(workspace, "first"))
	if plan.Blocked {
		t.Fatalf("Blocked = true, want alias reuse allowed: %#v", plan.Blockers)
	}
}

func TestReconcilerReadsStateWithoutObservedStateLeakageOrWrites(t *testing.T) {
	workspace := t.TempDir()
	store := &recordingStore{
		projects: []state.Project{
			project(1, "observed-secret-alias", "https://github.com/BramVR/desired", filepath.Join(workspace, "observed-secret-path")),
		},
		machines: []state.Machine{machine(workspace)},
	}

	plan, err := New(store).DryRun(context.Background(), []workspacemanifest.Entry{
		manifestEntry("https://github.com/BramVR/desired", "desired", "desired-path"),
	})
	if err != nil {
		t.Fatalf("DryRun error = %v", err)
	}

	if store.writeCount != 0 {
		t.Fatalf("writeCount = %d, want dry-run read only", store.writeCount)
	}
	change := assertDrift(t, plan, "https://github.com/BramVR/desired", DriftMoved, filepath.Join(workspace, "desired-path"))
	if change.Alias != "desired" || change.DesiredPath != "desired-path" {
		t.Fatalf("desired fields leaked observed state: %#v", change)
	}
}

func TestDryRunPlanOmitsObservedOnlyCloneURL(t *testing.T) {
	workspace := t.TempDir()
	localOnly := project(1, "local-only", "https://github.com/BramVR/local-only", filepath.Join(workspace, "local-only"))
	localOnly.CloneURL = "https://user:leak-marker@example.invalid/org/repo.git?token=leak-marker#piece"

	plan, err := BuildDryRunPlan(nil, []state.Project{localOnly}, machine(workspace))
	if err != nil {
		t.Fatalf("BuildDryRunPlan error = %v", err)
	}
	drift := assertDrift(t, plan, "https://github.com/BramVR/local-only", DriftAdded, filepath.Join(workspace, "local-only"))
	if drift.CloneURL != "" {
		t.Fatalf("added drift clone URL = %q, want omitted observed-only clone URL", drift.CloneURL)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal plan error = %v", err)
	}
	if strings.Contains(string(data), "leak-marker") || strings.Contains(string(data), "user:") {
		t.Fatalf("plan leaked observed clone URL: %s", data)
	}
}

func TestDryRunPlanRejectsUnsupportedManifestVersion(t *testing.T) {
	workspace := t.TempDir()

	_, err := BuildDryRunPlan([]workspacemanifest.Entry{{
		ManifestVersion: workspacemanifest.ManifestVersion + 1,
		Project: workspacemanifest.ProjectEntry{
			Identity:    "https://github.com/BramVR/future",
			Alias:       "future",
			DesiredPath: "future",
		},
	}}, nil, machine(workspace))
	if err == nil {
		t.Fatal("BuildDryRunPlan error = nil, want unsupported manifest version rejection")
	}
}

func manifestEntry(identity, alias, desiredPath string) workspacemanifest.Entry {
	return workspacemanifest.NewEntry(workspacemanifest.ProjectEntry{
		Identity:    identity,
		Alias:       alias,
		DesiredPath: desiredPath,
	})
}

func project(id int64, alias, identity, localPath string) state.Project {
	return state.Project{
		ID:               id,
		Alias:            alias,
		NormalizedRemote: identity,
		CloneURL:         identity + ".git",
		LocalPath:        localPath,
	}
}

func machine(workspaceRoot string) state.Machine {
	return state.Machine{
		ID:            "machine-1",
		Hostname:      "observed-hostname",
		OS:            "darwin",
		Architecture:  "arm64",
		WorkspaceRoot: workspaceRoot,
	}
}

func assertDrift(t *testing.T, plan DriftPlan, identity string, kind DriftKind, desiredPath string) Drift {
	t.Helper()
	for _, drift := range plan.Drifts {
		if drift.Identity != identity {
			continue
		}
		if drift.Kind != kind {
			t.Fatalf("drift %s kind = %s, want %s: %#v", identity, drift.Kind, kind, drift)
		}
		if drift.DesiredLocalPath != desiredPath {
			t.Fatalf("drift %s desired path = %q, want %q", identity, drift.DesiredLocalPath, desiredPath)
		}
		return drift
	}
	t.Fatalf("missing drift for %s in %#v", identity, plan.Drifts)
	return Drift{}
}

func hasBlocker(plan DriftPlan, kind BlockerKind, identity string) bool {
	for _, blocker := range plan.Blockers {
		if blocker.Kind == kind && blocker.Identity == identity {
			return true
		}
	}
	return false
}

type recordingStore struct {
	projects   []state.Project
	machines   []state.Machine
	writeCount int
}

func (s *recordingStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s *recordingStore) ListMachines(context.Context) ([]state.Machine, error) {
	return append([]state.Machine(nil), s.machines...), nil
}
