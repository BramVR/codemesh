package clonestrategy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/gitops"
)

func TestFullCloneUsesBranchSingleBranchAndReportsSelection(t *testing.T) {
	fake := &gitops.FakeRunner{}
	strategy := FullClone{Git: gitops.New(fake)}

	result, err := strategy.Clone(context.Background(), Request{
		CloneURL:    "https://example.invalid/org/repo.git",
		Destination: "/tmp/workspace",
		Branch:      "main",
	})

	if err != nil {
		t.Fatalf("Clone error = %v", err)
	}
	if result.Strategy != FullCloneSelection() {
		t.Fatalf("strategy = %#v, want full clone", result.Strategy)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("git calls = %#v", fake.Calls)
	}
	got := strings.Join(fake.Calls[0].Args, " ")
	want := "clone --branch main --single-branch https://example.invalid/org/repo.git /tmp/workspace"
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
	if len(args) != 3 || args[0] != "clone" || args[1] != " /tmp/source repo.git " || args[2] != " /tmp/target workspace " {
		t.Fatalf("git args = %#v, want exact clone URL and destination preserved", args)
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
