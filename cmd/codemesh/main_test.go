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
	repo := filepath.Join(t.TempDir(), "codemesh")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "remote", "add", "origin", remote)
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}
