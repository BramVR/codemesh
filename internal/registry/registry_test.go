package registry

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/gitops"
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

func TestHydrateClonesUsingPreservedCloneURL(t *testing.T) {
	ctx := context.Background()
	remote := createBareRemote(t, "private")
	target := filepath.Join(t.TempDir(), "private")
	store := &projectListStore{projects: []state.Project{{
		Alias:            "private",
		NormalizedRemote: "https://github.com/BramVR/private",
		CloneURL:         remote,
		LocalPath:        target,
	}}}

	result, err := New(store).Hydrate(ctx, "private")
	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if result.AlreadyPresent {
		t.Fatalf("AlreadyPresent = true, want cloned checkout")
	}
	if result.CloneStrategy.Name != "full-clone" || result.CloneStrategy.History != "full" || result.CloneStrategy.WorkingTree != "complete" {
		t.Fatalf("CloneStrategy = %#v, want full clone", result.CloneStrategy)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("hydrated README missing: %v", err)
	}
	origin, err := gitOutput(target, "remote", "get-url", "origin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(origin) != remote {
		t.Fatalf("hydrated origin = %q, want clone URL %q", strings.TrimSpace(origin), remote)
	}
}

func TestHydrateCanOptInToPartialSparseClone(t *testing.T) {
	requireGitPartialSparseSupport(t)
	ctx := context.Background()
	remote := createBareRemoteWithFiles(t, "sparse", map[string]string{
		"README.md": "selected\n",
		"large.txt": "not selected\n",
	})
	target := filepath.Join(t.TempDir(), "sparse")
	store := &projectListStore{projects: []state.Project{{
		Alias:            "sparse",
		NormalizedRemote: remote,
		CloneURL:         remote,
		LocalPath:        target,
	}}}

	result, err := New(store).Hydrate(ctx, "sparse", clonestrategy.Options{
		Partial:     true,
		SparsePaths: []string{"README.md"},
	})

	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if result.CloneStrategy.Name != "partial-sparse-clone" || result.CloneStrategy.Filter != "blob:none" || result.CloneStrategy.WorkingTree != "sparse" {
		t.Fatalf("CloneStrategy = %#v, want partial sparse", result.CloneStrategy)
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("hydrated sparse README missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "large.txt")); !os.IsNotExist(err) {
		t.Fatalf("hydrated sparse checkout included unselected file or stat failed: %v", err)
	}
}

func TestHydrateAlreadyPresentDoesNotReportRequestedCloneOptions(t *testing.T) {
	ctx := context.Background()
	remote := createBareRemote(t, "present-options")
	target := filepath.Join(t.TempDir(), "present-options")
	runGit(t, filepath.Dir(target), "clone", remote, target)
	normalized, err := NormalizeRemote(remote)
	if err != nil {
		t.Fatal(err)
	}
	store := &projectListStore{projects: []state.Project{{
		Alias:            "present-options",
		NormalizedRemote: normalized,
		CloneURL:         remote,
		LocalPath:        target,
	}}}

	result, err := New(store).Hydrate(ctx, "present-options", clonestrategy.Options{Partial: true, SparsePaths: []string{"README.md"}})

	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if !result.AlreadyPresent {
		t.Fatalf("AlreadyPresent = false, want true")
	}
	if result.CloneStrategy.Name != "full-clone" || result.CloneStrategy.History != "full" || result.CloneStrategy.WorkingTree != "complete" || result.CloneStrategy.Filter != "" || len(result.CloneStrategy.SparsePaths) != 0 {
		t.Fatalf("already-present clone strategy = %#v, want existing/default strategy", result.CloneStrategy)
	}
}

func TestHydrateAlreadyPresentCanonicalProjectPersistsDesiredMachinePath(t *testing.T) {
	ctx := context.Background()
	remote := createBareRemote(t, "present-canonical")
	canonicalPath := filepath.Join(t.TempDir(), "present-canonical")
	runGit(t, filepath.Dir(canonicalPath), "clone", remote, canonicalPath)
	normalized, err := NormalizeRemote(remote)
	if err != nil {
		t.Fatal(err)
	}
	observedPath := filepath.Join(t.TempDir(), "stale-observed")
	store := &projectUpdateStore{projects: []state.Project{{
		ID:               1,
		Alias:            "present-canonical",
		NormalizedRemote: normalized,
		CloneURL:         remote,
		LocalPath:        observedPath,
		CanonicalPath:    canonicalPath,
		Source:           "canonical",
	}}}

	result, err := New(store).Hydrate(ctx, "present-canonical")

	if err != nil {
		t.Fatalf("Hydrate error = %v", err)
	}
	if !result.AlreadyPresent || result.Project.LocalPath != canonicalPath {
		t.Fatalf("Hydrate result = %#v, want already-present at canonical path", result)
	}
	if store.projects[0].LocalPath != canonicalPath || store.projects[0].CanonicalPath != canonicalPath || store.projects[0].Source != "canonical" {
		t.Fatalf("stored project = %#v, want canonical placement persisted", store.projects[0])
	}
}

func TestHydrateCleansDestinationAfterPostCloneStrategyFailure(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "partial-failure")
	store := &projectListStore{projects: []state.Project{{
		Alias:            "partial-failure",
		NormalizedRemote: "https://example.invalid/org/repo",
		CloneURL:         "https://example.invalid/org/repo.git",
		LocalPath:        target,
	}}}
	runner := &postCloneFailureRunner{}
	registry := New(store)
	registry.git = gitops.New(runner)

	_, err := registry.Hydrate(ctx, "partial-failure", clonestrategy.Options{Partial: true})

	if err == nil {
		t.Fatal("Hydrate error = nil, want partial filter failure")
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("hydrate target remained after post-clone failure or stat failed: %v", statErr)
	}
}

func TestHydrateDoesNotTreatSubdirectoryInsideCheckoutAsPresent(t *testing.T) {
	ctx := context.Background()
	remote := createBareRemote(t, "same-origin")
	checkout := filepath.Join(t.TempDir(), "checkout")
	runGit(t, filepath.Dir(checkout), "clone", remote, checkout)
	subdir := filepath.Join(checkout, "ordinary-dir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "local.txt"), []byte("not a checkout root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	normalized, err := NormalizeRemote(remote)
	if err != nil {
		t.Fatal(err)
	}
	store := &projectListStore{projects: []state.Project{{
		Alias:            "same-origin",
		NormalizedRemote: normalized,
		CloneURL:         remote,
		LocalPath:        subdir,
	}}}

	_, err = New(store).Hydrate(ctx, "same-origin")

	if err == nil {
		t.Fatalf("Hydrate error = nil, want path conflict")
	}
	if !strings.Contains(err.Error(), "path conflict") {
		t.Fatalf("Hydrate error = %v, want path conflict", err)
	}
}

func TestHydrateRefusesPresentCheckoutForDifferentRemote(t *testing.T) {
	ctx := context.Background()
	registeredRemote := createBareRemote(t, "registered")
	wrongRemote := createBareRemote(t, "wrong")
	target := filepath.Join(t.TempDir(), "checkout")
	runGit(t, filepath.Dir(target), "clone", wrongRemote, target)
	normalizedRegistered, err := NormalizeRemote(registeredRemote)
	if err != nil {
		t.Fatal(err)
	}
	store := &projectListStore{projects: []state.Project{{
		Alias:            "registered",
		NormalizedRemote: normalizedRegistered,
		CloneURL:         registeredRemote,
		LocalPath:        target,
	}}}

	_, err = New(store).Hydrate(ctx, "registered")

	if err == nil {
		t.Fatal("Hydrate error = nil, want path conflict for wrong checkout")
	}
	var conflict PathConflictError
	if !errors.As(err, &conflict) || conflict.Path != target {
		t.Fatalf("Hydrate error = %T %v, want PathConflictError at %s", err, err, target)
	}
}

func TestCloneURLForStripsHTTPSCredentials(t *testing.T) {
	got := cloneURLFor("https://token:secret@example.invalid/org/repo.git", "")

	if got != "https://example.invalid/org/repo.git" {
		t.Fatalf("clone URL = %q, want credentials stripped", got)
	}
}

func TestCloneURLForStripsURLPasswords(t *testing.T) {
	got := cloneURLFor("ssh://git:secret@example.invalid/org/repo.git", "")

	if got != "ssh://git@example.invalid/org/repo.git" {
		t.Fatalf("clone URL = %q, want password stripped", got)
	}
}

func TestRedactedCloneOutputHidesCredentialURL(t *testing.T) {
	cloneURL := "https://token:secret@example.invalid/org/repo.git"
	output := "fatal: could not read Username for 'https://token:secret@example.invalid/org/repo.git'"

	got := redactedCloneOutput(output, cloneURL)

	if strings.Contains(got, "token") || strings.Contains(got, "secret") {
		t.Fatalf("redacted output leaked credentials: %q", got)
	}
	if !strings.Contains(got, "https://redacted@example.invalid/org/repo.git") {
		t.Fatalf("redacted output = %q, want redacted URL", got)
	}
}

func hasSkip(skips []ScanSkip, path, reasonPart string) bool {
	return slices.ContainsFunc(skips, func(skip ScanSkip) bool {
		return skip.Path == path && strings.Contains(skip.Reason, reasonPart)
	})
}

func requireGitPartialSparseSupport(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	seed := createCommittedGitRepo(t, filepath.Join(tmp, "seed"), "")
	if err := os.WriteFile(filepath.Join(seed, "large.txt"), []byte("not selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add sparse probe")
	largeBlob, err := gitOutput(seed, "rev-parse", "HEAD:large.txt")
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(tmp, "remote.git")
	clone := filepath.Join(tmp, "clone")
	runGit(t, tmp, "clone", "--bare", seed, remote)
	output, err := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout", "--branch", "main", "--single-branch", "file://"+remote, clone).CombinedOutput()
	if err != nil {
		t.Skipf("git partial clone probe failed: %v: %s", err, output)
	}
	lower := strings.ToLower(string(output))
	if strings.Contains(lower, "filtering not recognized") || strings.Contains(lower, "filter") && strings.Contains(lower, "ignoring") {
		t.Skipf("git partial clone filter unsupported by local file transport: %s", output)
	}
	runGit(t, clone, "sparse-checkout", "set", "--no-cone", "--", "/README.md")
	runGit(t, clone, "checkout", "main")
	cmd := exec.Command("git", "-C", clone, "cat-file", "-e", strings.TrimSpace(largeBlob))
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := cmd.Run(); err == nil {
		t.Skipf("git partial clone filter fetched unselected blob %s", strings.TrimSpace(largeBlob))
	}
}

func createBareRemote(t *testing.T, name string) string {
	t.Helper()
	return createBareRemoteWithFiles(t, name, nil)
}

func createBareRemoteWithFiles(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	seed := createCommittedGitRepo(t, filepath.Join(root, "seed"), "")
	if len(files) != 0 {
		for rel, content := range files {
			path := filepath.Join(seed, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		runGit(t, seed, "add", ".")
		runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Update fixture")
	}
	remote := filepath.Join(root, name+".git")
	runGit(t, root, "clone", "--bare", seed, remote)
	return remote
}

type projectListStore struct {
	projects []state.Project
}

type projectUpdateStore struct {
	projects []state.Project
}

type postCloneFailureRunner struct{}

func (r *postCloneFailureRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := r.RunDetail(ctx, dir, args...)
	return output.Stdout, err
}

func (r *postCloneFailureRunner) RunDetail(_ context.Context, _ string, args ...string) (gitops.CommandOutput, error) {
	if len(args) >= 3 && args[0] == "clone" {
		destination := args[len(args)-1]
		if err := os.MkdirAll(filepath.Join(destination, ".git"), 0o755); err != nil {
			return gitops.CommandOutput{}, err
		}
		if err := os.WriteFile(filepath.Join(destination, "README.md"), []byte("partial failure\n"), 0o644); err != nil {
			return gitops.CommandOutput{}, err
		}
		return gitops.CommandOutput{Stderr: "warning: filtering not recognized by server, ignoring\n"}, nil
	}
	return gitops.CommandOutput{}, nil
}

func (s *projectListStore) AddProject(context.Context, state.Project) (state.Project, error) {
	panic("not implemented")
}

func (s *projectListStore) UpsertProject(context.Context, state.Project) (state.Project, state.ProjectUpsertAction, error) {
	panic("not implemented")
}

func (s *projectListStore) ListProjects(context.Context) ([]state.Project, error) {
	return s.projects, nil
}

func (s *projectUpdateStore) AddProject(context.Context, state.Project) (state.Project, error) {
	panic("not implemented")
}

func (s *projectUpdateStore) UpsertProject(context.Context, state.Project) (state.Project, state.ProjectUpsertAction, error) {
	panic("not implemented")
}

func (s *projectUpdateStore) ListProjects(context.Context) ([]state.Project, error) {
	return s.projects, nil
}

func (s *projectUpdateStore) UpdateProject(_ context.Context, id int64, project state.Project) (state.Project, error) {
	for i := range s.projects {
		if s.projects[i].ID == id {
			project.ID = id
			s.projects[i] = project
			return project, nil
		}
	}
	return state.Project{}, os.ErrNotExist
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
