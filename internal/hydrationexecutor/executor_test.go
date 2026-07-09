package hydrationexecutor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/hydrationplanner"
	"github.com/BramVR/codemesh/internal/state"
)

func TestExecuteFailedCloneKeepsPreexistingEmptyDestinationEmpty(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &partialCloneFailureRunner{}
	plan := hydrationplanner.Plan{
		WorkspaceRoot: workspace,
		Actions: []hydrationplanner.Action{{
			Project: "alpha",
			Path:    target,
			State:   hydrationplanner.StateMissing,
			Action:  hydrationplanner.ActionClone,
			ProjectRow: state.Project{
				Alias:            "alpha",
				NormalizedRemote: "https://example.invalid/bram/alpha",
				CloneURL:         "https://example.invalid/bram/alpha.git",
				LocalPath:        target,
			},
		}},
	}

	_, err := New(gitops.New(runner)).Execute(context.Background(), plan, clonestrategyOptionsZero())

	if err == nil {
		t.Fatal("Execute error = nil, want clone failure")
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("target entries = %#v err=%v, want pre-existing empty dir preserved", entries, err)
	}
	parentEntries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range parentEntries {
		if strings.HasPrefix(entry.Name(), ".codemesh-clone-alpha-") {
			t.Fatalf("temporary clone directory was not removed: %s", entry.Name())
		}
	}
}

func TestExecuteRootDestinationUsesTemporaryCloneInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	runner := &partialCloneFailureRunner{}
	plan := hydrationplanner.Plan{
		WorkspaceRoot: workspace,
		Actions: []hydrationplanner.Action{{
			Project: "root",
			Path:    workspace,
			State:   hydrationplanner.StateMissing,
			Action:  hydrationplanner.ActionClone,
			ProjectRow: state.Project{
				Alias:            "root",
				NormalizedRemote: "https://example.invalid/bram/root",
				CloneURL:         "https://example.invalid/bram/root.git",
				LocalPath:        workspace,
			},
		}},
	}

	_, err := New(gitops.New(runner)).Execute(context.Background(), plan, clonestrategyOptionsZero())

	if err == nil {
		t.Fatal("Execute error = nil, want clone failure")
	}
	if runner.destination == "" {
		t.Fatal("runner did not observe clone destination")
	}
	rel, err := filepath.Rel(workspace, runner.destination)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("clone destination = %q, want inside workspace %q", runner.destination, workspace)
	}
	if entries, err := os.ReadDir(workspace); err != nil || len(entries) != 0 {
		t.Fatalf("workspace entries = %#v err=%v, want empty root preserved", entries, err)
	}
}

func TestExecutePreflightsPresentConflictsBeforeCloning(t *testing.T) {
	workspace := t.TempDir()
	alpha := filepath.Join(workspace, "alpha")
	zeta := filepath.Join(workspace, "zeta")
	if err := os.MkdirAll(zeta, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &preflightConflictRunner{presentPath: zeta}
	plan := hydrationplanner.Plan{
		WorkspaceRoot: workspace,
		Actions: []hydrationplanner.Action{
			{
				Project: "alpha",
				Path:    alpha,
				State:   hydrationplanner.StateMissing,
				Action:  hydrationplanner.ActionClone,
				ProjectRow: state.Project{
					Alias:            "alpha",
					NormalizedRemote: "https://example.invalid/bram/alpha",
					CloneURL:         "https://example.invalid/bram/alpha.git",
					LocalPath:        alpha,
				},
			},
			{
				Project: "zeta",
				Path:    zeta,
				State:   hydrationplanner.StatePresent,
				Action:  hydrationplanner.ActionNone,
				ProjectRow: state.Project{
					Alias:            "zeta",
					NormalizedRemote: "https://example.invalid/bram/zeta",
					LocalPath:        zeta,
				},
			},
		},
	}

	_, err := New(gitops.New(runner)).Execute(context.Background(), plan, clonestrategyOptionsZero())

	var conflict PathConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Execute error = %v, want PathConflictError", err)
	}
	if runner.cloneCalled {
		t.Fatal("Execute cloned before preflighting present checkout conflict")
	}
}

func TestExecutePreflightsDuplicateCloneDestinationsBeforeCloning(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "shared")
	runner := &preflightConflictRunner{}
	plan := hydrationplanner.Plan{
		WorkspaceRoot: workspace,
		Actions: []hydrationplanner.Action{
			{
				Project: "alpha",
				Path:    target,
				State:   hydrationplanner.StateMissing,
				Action:  hydrationplanner.ActionClone,
				ProjectRow: state.Project{
					Alias:            "alpha",
					NormalizedRemote: "https://example.invalid/bram/alpha",
					CloneURL:         "https://example.invalid/bram/alpha.git",
					LocalPath:        target,
				},
			},
			{
				Project: "beta",
				Path:    target,
				State:   hydrationplanner.StateMissing,
				Action:  hydrationplanner.ActionClone,
				ProjectRow: state.Project{
					Alias:            "beta",
					NormalizedRemote: "https://example.invalid/bram/beta",
					CloneURL:         "https://example.invalid/bram/beta.git",
					LocalPath:        target,
				},
			},
		},
	}

	_, err := New(gitops.New(runner)).Execute(context.Background(), plan, clonestrategyOptionsZero())

	var conflict PathConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Execute error = %v, want PathConflictError", err)
	}
	if runner.cloneCalled {
		t.Fatal("Execute cloned before preflighting duplicate clone destinations")
	}
}

func TestExecutePreflightsCloneNestedUnderPresentProjectBeforeCloning(t *testing.T) {
	workspace := t.TempDir()
	parent := filepath.Join(workspace, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &preflightConflictRunner{
		presentPath: parent,
		remote:      "https://example.invalid/bram/parent.git",
	}
	plan := hydrationplanner.Plan{
		WorkspaceRoot: workspace,
		Actions: []hydrationplanner.Action{
			{
				Project: "child",
				Path:    child,
				State:   hydrationplanner.StateMissing,
				Action:  hydrationplanner.ActionClone,
				ProjectRow: state.Project{
					Alias:            "child",
					NormalizedRemote: "https://example.invalid/bram/child",
					CloneURL:         "https://example.invalid/bram/child.git",
					LocalPath:        child,
				},
			},
			{
				Project: "parent",
				Path:    parent,
				State:   hydrationplanner.StatePresent,
				Action:  hydrationplanner.ActionNone,
				ProjectRow: state.Project{
					Alias:            "parent",
					NormalizedRemote: "https://example.invalid/bram/parent",
					LocalPath:        parent,
				},
			},
		},
	}

	_, err := New(gitops.New(runner)).Execute(context.Background(), plan, clonestrategyOptionsZero())

	var conflict PathConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Execute error = %v, want PathConflictError", err)
	}
	if runner.cloneCalled {
		t.Fatal("Execute cloned before preflighting nested present project conflict")
	}
}

type partialCloneFailureRunner struct {
	destination string
}

func (r *partialCloneFailureRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := r.RunDetail(ctx, dir, args...)
	return output.Stdout, err
}

func (r *partialCloneFailureRunner) RunDetail(_ context.Context, _ string, args ...string) (gitops.CommandOutput, error) {
	if len(args) >= 3 && args[0] == "clone" {
		destination := args[len(args)-1]
		r.destination = destination
		if err := os.WriteFile(filepath.Join(destination, "partial.txt"), []byte("partial\n"), 0o644); err != nil {
			return gitops.CommandOutput{}, err
		}
		return gitops.CommandOutput{}, errors.New("clone failed after writing partial contents")
	}
	return gitops.CommandOutput{}, nil
}

type preflightConflictRunner struct {
	presentPath string
	remote      string
	cloneCalled bool
}

func (r *preflightConflictRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := r.RunDetail(ctx, dir, args...)
	return output.Stdout, err
}

func (r *preflightConflictRunner) RunDetail(_ context.Context, dir string, args ...string) (gitops.CommandOutput, error) {
	if len(args) == 0 {
		return gitops.CommandOutput{}, nil
	}
	if args[0] == "clone" {
		r.cloneCalled = true
		return gitops.CommandOutput{}, nil
	}
	if dir == r.presentPath && strings.Join(args, " ") == "rev-parse --is-inside-work-tree" {
		return gitops.CommandOutput{Stdout: "true\n"}, nil
	}
	if dir == r.presentPath && strings.Join(args, " ") == "rev-parse --show-toplevel" {
		return gitops.CommandOutput{Stdout: r.presentPath + "\n"}, nil
	}
	if dir == r.presentPath && strings.Join(args, " ") == "remote get-url origin" {
		remote := r.remote
		if remote == "" {
			remote = "https://example.invalid/bram/wrong.git"
		}
		return gitops.CommandOutput{Stdout: remote + "\n"}, nil
	}
	return gitops.CommandOutput{}, nil
}

func clonestrategyOptionsZero() clonestrategy.Options {
	return clonestrategy.Options{}
}
