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
