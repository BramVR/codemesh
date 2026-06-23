package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpIdentifiesCodeMesh(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "CodeMesh") {
		t.Fatalf("help output did not identify CodeMesh:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unknown"}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("exit code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "unknown command: unknown") {
		t.Fatalf("stderr did not explain the failure:\n%s", stderr.String())
	}
}

func TestInitCreatesLocalState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"init", workspace}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "codemesh.db")); err != nil {
		t.Fatalf("database missing: %v", err)
	}
	if !strings.Contains(stdout.String(), "initialized CodeMesh") {
		t.Fatalf("stdout missing init message:\n%s", stdout.String())
	}
}

func TestInitHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"init", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "codemesh init [workspace-root]") {
		t.Fatalf("init help missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestAddHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"add", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "codemesh add <path>") {
		t.Fatalf("add help missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestAddThenTreeShowsPresentProject(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createGitRepo(t, "git@github.com:BramVR/codemesh.git")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"add", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("add exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "added project: codemesh") {
		t.Fatalf("add stdout missing alias:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "codemesh") || !strings.Contains(stdout.String(), "present") || !strings.Contains(stdout.String(), repo) {
		t.Fatalf("tree output missing project state/path:\n%s", stdout.String())
	}
}

func TestScanThenTreeShowsDiscoveredProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := t.TempDir()
	alpha := createGitRepoAt(t, filepath.Join(workspace, "alpha"), "https://github.com/BramVR/alpha.git")
	nested := createGitRepoAt(t, filepath.Join(alpha, "vendor", "nested"), "https://github.com/BramVR/nested.git")
	createGitRepoAt(t, filepath.Join(workspace, "no-remote"), "")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"scan", workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("scan exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "scan complete") || !strings.Contains(stdout.String(), "added: alpha") {
		t.Fatalf("scan stdout missing added report:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "skipped: "+nested+" (nested Git repo)") || !strings.Contains(stdout.String(), "unsupported") {
		t.Fatalf("scan stdout missing skips:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"scan", workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("rerun scan exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "unchanged: alpha") {
		t.Fatalf("rerun scan stdout missing unchanged report:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	// alpha contains the nested repo fixture, so local tree readiness should surface it as dirty.
	if !strings.Contains(stdout.String(), "alpha") || !strings.Contains(stdout.String(), "dirty") || !strings.Contains(stdout.String(), alpha) {
		t.Fatalf("tree output missing scanned project:\n%s", stdout.String())
	}
}

func TestStatusReportsDirtyCheckoutWarning(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "dirty-source")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"add", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("add exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"status", "dirty-source", "--base", "main"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"project: dirty-source",
		"state: dirty",
		"path_present: true",
		"warning: dirty-checkout",
		"blockers: none",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusWithoutProjectSummarizesKnownProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	clean := createCommittedLocalRemoteClone(t, "clean-repo")
	dirty := createCommittedLocalRemoteClone(t, "dirty-source")
	if err := os.WriteFile(filepath.Join(dirty, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", clean}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add clean exit code = %d, want 0", code)
	}
	if code := run([]string{"add", dirty}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add dirty exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"status"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "- clean-repo state=present") || !strings.Contains(output, "- dirty-source state=dirty") {
		t.Fatalf("status summary missing normalized states:\n%s", output)
	}
}

func TestHydrateMissingProjectClonesDesiredPathAndUpdatesTree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "missing-source")
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "- missing-source missing "+source) {
		t.Fatalf("tree output missing missing project:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"hydrate", "missing-source"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hydrated project: missing-source") {
		t.Fatalf("hydrate stdout missing success:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(source, "README.md")); err != nil {
		t.Fatalf("hydrated checkout missing README: %v", err)
	}
	assertGitStatusClean(t, source)

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree after hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "- missing-source present "+source) {
		t.Fatalf("tree output missing present hydrated project:\n%s", stdout.String())
	}
}

func TestHydratePresentProjectReportsNoCloneNeeded(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "present-source")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hydrate", "present-source"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "project already present: present-source") {
		t.Fatalf("hydrate stdout missing present report:\n%s", stdout.String())
	}
	assertGitStatusClean(t, source)
}

func TestHydrateRefusesExistingNonEmptyPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "conflict-source")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "local.txt"), []byte("do not overwrite\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "conflict-source"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("hydrate exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "path conflict") || !strings.Contains(stderr.String(), source) {
		t.Fatalf("stderr missing clear conflict:\n%s", stderr.String())
	}
	if got, err := os.ReadFile(filepath.Join(source, "local.txt")); err != nil || string(got) != "do not overwrite\n" {
		t.Fatalf("conflict file changed or missing: got %q err %v", got, err)
	}
}

func TestHydrateDoesNotCreatePlaceholderDirectoriesForOtherMissingProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	alpha := createCommittedLocalRemoteClone(t, "alpha-missing")
	beta := createCommittedLocalRemoteClone(t, "beta-missing")
	alpha, err := filepath.EvalSymlinks(alpha)
	if err != nil {
		t.Fatal(err)
	}
	beta, err = filepath.EvalSymlinks(beta)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", alpha}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add alpha exit code = %d, want 0", code)
	}
	if code := run([]string{"add", beta}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add beta exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(alpha); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(beta); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"hydrate", "alpha-missing"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(alpha); err != nil {
		t.Fatalf("hydrated path missing: %v", err)
	}
	if _, err := os.Stat(beta); !os.IsNotExist(err) {
		t.Fatalf("other missing project path was created or stat failed unexpectedly: %v", err)
	}
}

func TestAgentPreparePrintsReadyPathAndWritesRunMetadata(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-ready")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-ready", "--base", "main", "--profile", "codex"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("agent prepare exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "ready_path: ") {
		t.Fatalf("stdout missing ready path:\n%s", output)
	}
	readyPath := valueAfterPrefix(t, output, "ready_path: ")
	if _, err := os.Stat(filepath.Join(readyPath, "README.md")); err != nil {
		t.Fatalf("ready checkout missing README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(readyPath, "codemesh-run.json")); err != nil {
		t.Fatalf("metadata missing: %v", err)
	}
}

func TestAgentPrepareBlocksOnReadinessBlockers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-blocked")
	if err := os.WriteFile(filepath.Join(source, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".codemesh.yml")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env")
	runGit(t, source, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-blocked"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("agent prepare exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "blocker: missing-env-file") {
		t.Fatalf("stderr missing actionable blocker:\n%s", stderr.String())
	}
	if entries, err := os.ReadDir(filepath.Join(home, "agents")); err == nil && len(entries) != 0 {
		t.Fatalf("agents dir has entries after blocked prep: %v", entries)
	}
}

func TestAddAliasConflictFailsWithActionableError(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	first := createGitRepo(t, "https://github.com/BramVR/first.git")
	second := createGitRepo(t, "https://github.com/BramVR/second.git")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", "--alias", "shared", first}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("first add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"add", "--alias", "shared", second}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("second add exit code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "alias") || !strings.Contains(stderr.String(), "shared") || !strings.Contains(stderr.String(), "--alias") {
		t.Fatalf("stderr missing actionable conflict:\n%s", stderr.String())
	}
}

func valueAfterPrefix(t *testing.T, output, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("prefix %q missing in output:\n%s", prefix, output)
	return ""
}

func assertGitStatusClean(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Fatalf("git status not clean:\n%s", output)
	}
}

func createGitRepo(t *testing.T, remote string) string {
	t.Helper()
	return createGitRepoAt(t, filepath.Join(t.TempDir(), "codemesh"), remote)
}

func createGitRepoAt(t *testing.T, repo, remote string) string {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	if remote != "" {
		runGit(t, repo, "remote", "add", "origin", remote)
	}
	root, err := gitRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func createCommittedLocalRemoteClone(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	remote := filepath.Join(root, "remote.git")
	source := filepath.Join(root, name)
	runGit(t, root, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	runGit(t, root, "clone", "--bare", seed, remote)
	runGit(t, root, "clone", remote, source)
	return source
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
