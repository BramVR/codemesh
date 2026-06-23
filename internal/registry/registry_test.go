package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/state"
)

func TestNormalizeRemoteTreatsGitHubSSHandHTTPSAsSameIdentity(t *testing.T) {
	forms := []string{
		"git@github.com:BramVR/codemesh.git",
		"ssh://git@github.com/BramVR/codemesh.git",
		"https://github.com/BramVR/codemesh.git",
		"https://github.com/BramVR/codemesh",
	}

	var want string
	for _, form := range forms {
		got, err := NormalizeRemote(form)
		if err != nil {
			t.Fatalf("NormalizeRemote(%q) error = %v", form, err)
		}
		if want == "" {
			want = got
		}
		if got != want {
			t.Fatalf("NormalizeRemote(%q) = %q, want %q", form, got, want)
		}
	}
	if want != "https://github.com/BramVR/codemesh" {
		t.Fatalf("normalized GitHub identity = %q", want)
	}
}

func TestNormalizeRemotePreservesGenericSCPLikeSSHRemote(t *testing.T) {
	got, err := NormalizeRemote("git@gitlab.com:group/repo.git")
	if err != nil {
		t.Fatalf("NormalizeRemote error = %v", err)
	}

	if got != "ssh://git@gitlab.com/group/repo" {
		t.Fatalf("normalized remote = %q", got)
	}
}

func TestNormalizeRemotePreservesURLPort(t *testing.T) {
	got, err := NormalizeRemote("ssh://git@git.example.com:2222/group/repo.git")
	if err != nil {
		t.Fatalf("NormalizeRemote error = %v", err)
	}

	if got != "ssh://git@git.example.com:2222/group/repo" {
		t.Fatalf("normalized remote = %q", got)
	}
}

func TestNormalizeRemoteFromResolvesRelativeLocalRemoteAgainstProjectRoot(t *testing.T) {
	root := filepath.Join("tmp", "workspace", "source")
	got, err := NormalizeRemoteFrom("../remotes/repo.git", root)
	if err != nil {
		t.Fatalf("NormalizeRemoteFrom error = %v", err)
	}

	want := filepath.Clean(filepath.Join("tmp", "workspace", "remotes", "repo.git"))
	if got != want {
		t.Fatalf("normalized remote = %q, want %q", got, want)
	}
}

func TestScanWorkspaceDiscoversProjectsAndReportsSkips(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	root := t.TempDir()
	alpha := createGitRepo(t, filepath.Join(root, "alpha"), "https://github.com/BramVR/alpha.git")
	nested := createGitRepo(t, filepath.Join(alpha, "vendor", "nested"), "https://github.com/BramVR/nested.git")
	noRemote := createGitRepo(t, filepath.Join(root, "no-remote"), "")

	result, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("ScanWorkspace error = %v", err)
	}

	if len(result.Added) != 1 || result.Added[0].Alias != "alpha" || result.Added[0].LocalPath != alpha {
		t.Fatalf("added projects = %#v, want alpha", result.Added)
	}
	if !hasSkip(result.Skipped, nested, "nested") {
		t.Fatalf("skips missing nested repo %s: %#v", nested, result.Skipped)
	}
	if !hasSkip(result.Skipped, noRemote, "unsupported") {
		t.Fatalf("skips missing unsupported repo %s: %#v", noRemote, result.Skipped)
	}

	rerun, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("rerun ScanWorkspace error = %v", err)
	}
	if len(rerun.Unchanged) != 1 || rerun.Unchanged[0].Alias != "alpha" {
		t.Fatalf("rerun unchanged = %#v, want alpha", rerun.Unchanged)
	}

	moved := createGitRepo(t, filepath.Join(root, "moved-alpha"), "https://github.com/BramVR/alpha.git")
	if err := os.RemoveAll(alpha); err != nil {
		t.Fatal(err)
	}
	updated, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("moved ScanWorkspace error = %v", err)
	}
	if len(updated.Updated) != 1 || updated.Updated[0].Alias != "alpha" || updated.Updated[0].LocalPath != moved {
		t.Fatalf("updated projects = %#v, want alpha at moved path", updated.Updated)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
}

func TestScanWorkspaceSkipsDuplicateRemoteWithinSingleRun(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	root := t.TempDir()
	first := createGitRepo(t, filepath.Join(root, "alpha"), "https://github.com/BramVR/alpha.git")
	duplicate := createGitRepo(t, filepath.Join(root, "zeta-alpha"), "https://github.com/BramVR/alpha.git")

	result, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("ScanWorkspace error = %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].LocalPath != first {
		t.Fatalf("added projects = %#v, want first checkout", result.Added)
	}
	if !hasSkip(result.Skipped, duplicate, "duplicate remote") {
		t.Fatalf("skips missing duplicate remote %s: %#v", duplicate, result.Skipped)
	}

	rerun, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("rerun ScanWorkspace error = %v", err)
	}
	if len(rerun.Unchanged) != 1 || rerun.Unchanged[0].LocalPath != first {
		t.Fatalf("rerun unchanged = %#v, want first checkout", rerun.Unchanged)
	}
	if len(rerun.Updated) != 0 {
		t.Fatalf("rerun updated = %#v, want none", rerun.Updated)
	}
}

func TestScanWorkspaceSkipsNestedRepoInsideUnsupportedParent(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	root := t.TempDir()
	parent := createGitRepo(t, filepath.Join(root, "no-remote"), "")
	nested := createGitRepo(t, filepath.Join(parent, "nested"), "https://github.com/BramVR/nested.git")

	result, err := New(store).ScanWorkspace(ctx, root)
	if err != nil {
		t.Fatalf("ScanWorkspace error = %v", err)
	}
	if len(result.Added) != 0 {
		t.Fatalf("added projects = %#v, want none", result.Added)
	}
	if !hasSkip(result.Skipped, parent, "unsupported") {
		t.Fatalf("skips missing unsupported parent %s: %#v", parent, result.Skipped)
	}
	if !hasSkip(result.Skipped, nested, "nested") {
		t.Fatalf("skips missing nested repo %s: %#v", nested, result.Skipped)
	}
}

func TestScanWorkspaceDiscoversGitWorktreeCheckouts(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	workspace := t.TempDir()
	source := createCommittedGitRepo(t, filepath.Join(t.TempDir(), "source"), "https://github.com/BramVR/worktree-source.git")
	worktree := filepath.Join(workspace, "source-worktree")
	runGit(t, source, "worktree", "add", "-b", "source-worktree", worktree)
	worktreeRoot, err := gitOutput(worktree, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	worktree = strings.TrimSpace(worktreeRoot)

	result, err := New(store).ScanWorkspace(ctx, workspace)
	if err != nil {
		t.Fatalf("ScanWorkspace error = %v", err)
	}
	if len(result.Added) != 1 || result.Added[0].Alias != "source-worktree" || result.Added[0].LocalPath != worktree {
		t.Fatalf("added projects = %#v, want worktree checkout", result.Added)
	}
}

func hasSkip(skips []ScanSkip, path, reasonPart string) bool {
	return slices.ContainsFunc(skips, func(skip ScanSkip) bool {
		return skip.Path == path && strings.Contains(skip.Reason, reasonPart)
	})
}

func createGitRepo(t *testing.T, path, remote string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, path, "init", "-b", "main")
	if remote != "" {
		runGit(t, path, "remote", "add", "origin", remote)
	}
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(root)
}

func createCommittedGitRepo(t *testing.T, path, remote string) string {
	t.Helper()
	root := createGitRepo(t, path, remote)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	return root
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func migratedStore(t *testing.T) *state.SQLiteStore {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "codemesh.db"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}
	return store
}
