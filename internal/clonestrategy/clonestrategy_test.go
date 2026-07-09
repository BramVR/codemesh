package clonestrategy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/gitops"
)

func TestFullCloneUsesBranchSingleBranchAndReportsSelection(t *testing.T) {
	fake := &gitops.FakeRunner{Responses: []gitops.FakeResponse{
		{},
		{Output: "true\n"},
		{Output: "blob:none\n"},
	}}
	strategy := FullClone{Git: gitops.New(fake)}

	result, err := strategy.Clone(context.Background(), Request{
		CloneURL:    "https://example.invalid/org/repo.git",
		Destination: "/tmp/workspace",
		Branch:      "main",
	})

	if err != nil {
		t.Fatalf("Clone error = %v", err)
	}
	if result.Strategy.Name != FullCloneName || result.Strategy.History != "full" || result.Strategy.WorkingTree != "complete" {
		t.Fatalf("strategy = %#v, want full clone", result.Strategy)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("git calls = %#v", fake.Calls)
	}
	got := strings.Join(fake.Calls[0].Args, " ")
	want := "clone --branch main --single-branch -- https://example.invalid/org/repo.git /tmp/workspace"
	if got != want {
		t.Fatalf("git args = %q, want %q", got, want)
	}
}

func TestFullClonePreservesExactCloneURLAndDestination(t *testing.T) {
	fake := &gitops.FakeRunner{}
	strategy := FullClone{Git: gitops.New(fake)}

	_, err := strategy.Clone(context.Background(), Request{
		CloneURL:    " /tmp/source repo.git ",
		Destination: " /tmp/target workspace ",
	})

	if err != nil {
		t.Fatalf("Clone error = %v", err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("git calls = %#v", fake.Calls)
	}
	args := fake.Calls[0].Args
	if len(args) != 4 || args[0] != "clone" || args[1] != "--" || args[2] != " /tmp/source repo.git " || args[3] != " /tmp/target workspace " {
		t.Fatalf("git args = %#v, want exact clone URL and destination preserved", args)
	}
}

func TestCloneWithPartialAndSparseOptionsRecordsStrategyAndUsesGitNativeLaziness(t *testing.T) {
	fake := &gitops.FakeRunner{Responses: []gitops.FakeResponse{
		{},
		{Output: "true\n"},
		{Output: "blob:none\n"},
	}}
	strategy := FullClone{Git: gitops.New(fake)}

	result, err := strategy.Clone(context.Background(), Request{
		CloneURL:    "https://example.invalid/org/repo.git",
		Destination: "/tmp/workspace",
		Branch:      "main",
		Options: Options{
			Partial:     true,
			SparsePaths: []string{"README.md", "docs/adr"},
		},
	})

	if err != nil {
		t.Fatalf("Clone error = %v", err)
	}
	if result.Strategy.Name != PartialSparseCloneName || result.Strategy.History != "partial" || result.Strategy.WorkingTree != "sparse" || result.Strategy.Filter != "blob:none" {
		t.Fatalf("strategy = %#v, want partial sparse clone", result.Strategy)
	}
	if strings.Join(result.Strategy.SparsePaths, ",") != "README.md,docs/adr" {
		t.Fatalf("strategy sparse paths = %#v", result.Strategy.SparsePaths)
	}
	if len(fake.Calls) != 5 {
		t.Fatalf("git calls = %#v, want clone, partial verification, sparse-checkout set, checkout", fake.Calls)
	}
	if got, want := strings.Join(fake.Calls[0].Args, " "), "clone --filter=blob:none --no-checkout --branch main --single-branch -- https://example.invalid/org/repo.git /tmp/workspace"; got != want {
		t.Fatalf("clone args = %q, want %q", got, want)
	}
	if fake.Calls[3].Dir != "/tmp/workspace" || strings.Join(fake.Calls[3].Args, " ") != "sparse-checkout set --no-cone -- /README.md /docs/adr" {
		t.Fatalf("sparse checkout call = %#v", fake.Calls[3])
	}
	if fake.Calls[4].Dir != "/tmp/workspace" || strings.Join(fake.Calls[4].Args, " ") != "checkout main" {
		t.Fatalf("checkout call = %#v", fake.Calls[4])
	}
}

func TestPartialCloneFailsWhenGitReportsFilterIgnored(t *testing.T) {
	fake := &gitops.FakeRunner{Responses: []gitops.FakeResponse{{
		Stderr: "warning: filtering not recognized by server, ignoring\n",
	}}}
	strategy := FullClone{Git: gitops.New(fake)}

	_, err := strategy.Clone(context.Background(), Request{
		CloneURL:    "https://example.invalid/org/repo.git",
		Destination: "/tmp/workspace",
		Options:     Options{Partial: true},
	})

	if err == nil {
		t.Fatal("Clone error = nil, want ignored filter failure")
	}
	if !strings.Contains(err.Error(), "partial clone filter was not honored") {
		t.Fatalf("Clone error = %q, want clear ignored filter diagnostic", err.Error())
	}
}

func TestFullCloneFailureDiagnosticsRedactCredentialURLParts(t *testing.T) {
	secret := "strategy-secret"
	cloneURL := "https://user:" + secret + "@example.invalid/org/repo.git?token=" + secret + "#frag"
	fake := &gitops.FakeRunner{Responses: []gitops.FakeResponse{{
		Err: gitops.NewCommandError(
			[]string{"clone", cloneURL, "/tmp/workspace"},
			[]byte("fatal: could not read "+cloneURL),
			errors.New("exit status 128"),
		),
	}}}
	strategy := FullClone{Git: gitops.New(fake)}

	_, err := strategy.Clone(context.Background(), Request{
		CloneURL:    cloneURL,
		Destination: "/tmp/workspace",
	})

	if err == nil {
		t.Fatal("Clone error = nil, want failure")
	}
	var cloneErr CloneError
	if !errors.As(err, &cloneErr) {
		t.Fatalf("Clone error = %T, want CloneError", err)
	}
	if cloneErr.Strategy.Name != FullCloneName {
		t.Fatalf("CloneError strategy = %#v", cloneErr.Strategy)
	}
	if errors.Unwrap(err) != nil {
		t.Fatalf("CloneError unwrap exposed raw git error: %#v", errors.Unwrap(err))
	}
	for _, leaked := range []string{secret, "token=", "#frag"} {
		if strings.Contains(err.Error(), leaked) || strings.Contains(cloneErr.CloneURL, leaked) {
			t.Fatalf("clone diagnostic leaked %q: error=%q url=%q", leaked, err.Error(), cloneErr.CloneURL)
		}
	}
	if !strings.Contains(err.Error(), "https://redacted@example.invalid/org/repo.git") {
		t.Fatalf("clone diagnostic missing redacted URL: %q", err.Error())
	}
}
