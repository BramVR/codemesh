package hydrationplanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

func TestPlannerClassifiesCanonicalProjectActionsWithoutGit(t *testing.T) {
	workspace := t.TempDir()
	present := filepath.Join(workspace, "present")
	missing := filepath.Join(workspace, "missing")
	conflict := filepath.Join(workspace, "conflict")
	unsafe := filepath.Join(filepath.Dir(workspace), "outside")
	for _, path := range []string{present, conflict} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(present, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conflict, "local.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := memoryStore{
		machines: []state.Machine{machine(workspace)},
		projects: []state.Project{
			canonicalProject(1, "present", "https://example.invalid/present", present),
			canonicalProject(2, "missing", "https://example.invalid/missing", missing),
			canonicalProject(3, "conflict", "https://example.invalid/conflict", conflict),
			canonicalProject(4, "unsafe", "https://example.invalid/unsafe", unsafe),
		},
	}

	plan, err := New(store).PlanAll(context.Background(), clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanAll error = %v", err)
	}

	assertAction(t, plan, "present", StatePresent, ActionNone, present)
	assertAction(t, plan, "missing", StateMissing, ActionClone, missing)
	assertAction(t, plan, "conflict", StatePathConflict, ActionRefuse, conflict)
	assertAction(t, plan, "unsafe", StateUnsafePath, ActionRefuse, unsafe)
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want refusal actions to block")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("planner created missing project path or stat failed unexpectedly: %v", err)
	}
}

func TestPlannerReportsUnknownProject(t *testing.T) {
	workspace := t.TempDir()
	plan, err := New(memoryStore{machines: []state.Machine{machine(workspace)}}).PlanProject(context.Background(), "ghost", clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanProject error = %v", err)
	}

	action := assertAction(t, plan, "ghost", StateUnknownProject, ActionRefuse, "")
	if !strings.Contains(action.Reason, "unknown project") {
		t.Fatalf("unknown-project reason = %q", action.Reason)
	}
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want unknown project blocker")
	}
}

func TestPlannerRedactsSerializedCloneURLButKeepsRawExecutionInput(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "private")
	rawCloneURL := "https://user:pass@example.invalid/org/private.git"
	store := memoryStore{
		machines: []state.Machine{machine(workspace)},
		projects: []state.Project{{
			ID:               1,
			Alias:            "private",
			NormalizedRemote: "https://example.invalid/org/private",
			CloneURL:         rawCloneURL,
			LocalPath:        target,
			CanonicalPath:    target,
			Source:           "canonical",
		}},
	}

	plan, err := New(store).PlanProject(context.Background(), "private", clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanProject error = %v", err)
	}

	action := assertAction(t, plan, "private", StateMissing, ActionClone, target)
	if strings.Contains(action.CloneURL, "user") || strings.Contains(action.CloneURL, "pass") {
		t.Fatalf("serialized clone URL was not redacted: %q", action.CloneURL)
	}
	if action.ProjectRow.CloneURL != rawCloneURL {
		t.Fatalf("execution clone URL = %q, want raw input retained off JSON", action.ProjectRow.CloneURL)
	}
}

func TestPlannerBuildsSameCloneActionFromBootstrapManifestAndHydrateRegistry(t *testing.T) {
	workspace := t.TempDir()
	cloneURL := "https://example.invalid/shared.git"
	entry := manifestEntry("https://example.invalid/shared", "shared", "tools/shared", cloneURL)
	store := memoryStore{machines: []state.Machine{machine(workspace)}}

	bootstrapPlan, err := New(store).PlanEntries(context.Background(), []workspacemanifest.Entry{entry}, clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanEntries error = %v", err)
	}
	bootstrapAction := assertAction(t, bootstrapPlan, "shared", StateMissing, ActionClone, filepath.Join(workspace, "tools", "shared"))

	hydrateStore := memoryStore{
		machines: []state.Machine{machine(workspace)},
		projects: []state.Project{{
			ID:               1,
			Alias:            "shared",
			NormalizedRemote: entry.Project.Identity,
			CloneURL:         cloneURL,
			LocalPath:        filepath.Join(workspace, "tools", "shared"),
			CanonicalPath:    filepath.Join(workspace, "tools", "shared"),
			Source:           "canonical",
		}},
	}
	hydratePlan, err := New(hydrateStore).PlanProject(context.Background(), "shared", clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanProject error = %v", err)
	}
	hydrateAction := assertAction(t, hydratePlan, "shared", StateMissing, ActionClone, filepath.Join(workspace, "tools", "shared"))

	if bootstrapAction.Project != hydrateAction.Project || bootstrapAction.Path != hydrateAction.Path || bootstrapAction.CloneURL != hydrateAction.CloneURL || bootstrapAction.Action != hydrateAction.Action {
		t.Fatalf("bootstrap action = %#v\nhydrate action = %#v\nwant same execution input", bootstrapAction, hydrateAction)
	}
}

func TestPlannerReadsLocalOnlyPolicyFromObservedCanonicalCheckout(t *testing.T) {
	workspace := t.TempDir()
	observed := filepath.Join(workspace, "old", "alpha")
	target := filepath.Join(workspace, "tools", "alpha")
	if err := os.MkdirAll(filepath.Join(observed, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(observed, ".codemesh.yml"), []byte("local_only:\n  paths:\n    - path: node_modules\n      category: dependency\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := memoryStore{
		machines: []state.Machine{machine(workspace)},
		projects: []state.Project{{
			ID:               1,
			Alias:            "alpha",
			NormalizedRemote: "https://example.invalid/alpha",
			CloneURL:         "https://example.invalid/alpha.git",
			LocalPath:        observed,
			CanonicalPath:    target,
			Source:           "canonical",
		}},
	}

	plan, err := New(store).PlanProject(context.Background(), "alpha", clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanProject error = %v", err)
	}

	action := assertAction(t, plan, "alpha", StateMissing, ActionClone, target)
	if len(action.LocalOnlyPaths) != 1 || action.LocalOnlyPaths[0].Path != "node_modules" || action.LocalOnlyPaths[0].Category != "dependency" {
		t.Fatalf("local_only_paths = %#v", action.LocalOnlyPaths)
	}
}

func TestPlannerRejectsInvalidLocalOnlyPolicyFromObservedCanonicalCheckout(t *testing.T) {
	workspace := t.TempDir()
	observed := filepath.Join(workspace, "old", "alpha")
	target := filepath.Join(workspace, "tools", "alpha")
	if err := os.MkdirAll(filepath.Join(observed, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(observed, ".codemesh.yml"), []byte("local_only:\n  paths:\n    - path: src\n      category: source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := memoryStore{
		machines: []state.Machine{machine(workspace)},
		projects: []state.Project{{
			ID:               1,
			Alias:            "alpha",
			NormalizedRemote: "https://example.invalid/alpha",
			CloneURL:         "https://example.invalid/alpha.git",
			LocalPath:        observed,
			CanonicalPath:    target,
			Source:           "canonical",
		}},
	}

	plan, err := New(store).PlanProject(context.Background(), "alpha", clonestrategy.Options{})
	if err != nil {
		t.Fatalf("PlanProject error = %v", err)
	}

	action := assertAction(t, plan, "alpha", StatePathConflict, ActionRefuse, target)
	if !strings.Contains(action.Reason, "source content must stay in Git-managed source") {
		t.Fatalf("reason = %q", action.Reason)
	}
	if !plan.Blocked {
		t.Fatalf("Blocked = false, want invalid observed policy to block clone")
	}
}

func assertAction(t *testing.T, plan Plan, alias string, state State, action ActionKind, path string) Action {
	t.Helper()
	for _, item := range plan.Actions {
		if item.Project == alias {
			if item.State != state || item.Action != action || item.Path != path {
				t.Fatalf("action %s = %#v, want state=%s action=%s path=%s", alias, item, state, action, path)
			}
			return item
		}
	}
	t.Fatalf("action %s missing from plan %#v", alias, plan)
	return Action{}
}

func canonicalProject(id int64, alias, remote, path string) state.Project {
	return state.Project{
		ID:               id,
		Alias:            alias,
		NormalizedRemote: remote,
		CloneURL:         remote + ".git",
		LocalPath:        path,
		CanonicalPath:    path,
		Source:           "canonical",
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
}

func (s memoryStore) ListMachines(context.Context) ([]state.Machine, error) {
	return append([]state.Machine(nil), s.machines...), nil
}

func (s memoryStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}
