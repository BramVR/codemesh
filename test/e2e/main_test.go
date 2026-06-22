package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRunnerReportsFailureOutputAndSummary(t *testing.T) {
	h := testHarness(t)
	var out bytes.Buffer
	h.output = &out

	r := h.runCommand(commandSpec{
		Label:   "failure smoke",
		Name:    os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "fail-output"},
		Timeout: defaultCommandTimeout,
		Env:     []string{"CODEMESH_E2E_HELPER_PROCESS=1"},
	})

	if r.Status != "FAIL" {
		t.Fatalf("status = %s, want FAIL", r.Status)
	}
	if r.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", r.ExitCode)
	}
	if !strings.Contains(out.String(), "FAIL failure smoke (exit=7 duration=") {
		t.Fatalf("missing concise summary:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "stdout line") || !strings.Contains(out.String(), "stderr line") {
		t.Fatalf("missing captured output:\n%s", out.String())
	}
}

func TestCommandRunnerTimesOut(t *testing.T) {
	h := testHarness(t)
	var out bytes.Buffer
	h.output = &out

	r := h.runCommand(commandSpec{
		Label:   "timeout smoke",
		Name:    os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--", "sleep"},
		Timeout: 50 * time.Millisecond,
		Env:     []string{"CODEMESH_E2E_HELPER_PROCESS=1"},
	})

	if r.Status != "FAIL" {
		t.Fatalf("status = %s, want FAIL", r.Status)
	}
	if !r.TimedOut {
		t.Fatalf("timed out = false, want true")
	}
	if !strings.Contains(out.String(), "timeout after 50ms") {
		t.Fatalf("missing timeout detail:\n%s", out.String())
	}
}

func TestTimeoutTiers(t *testing.T) {
	if defaultCommandTimeout <= 0 {
		t.Fatalf("default timeout must be positive")
	}
	if longCommandTimeout <= defaultCommandTimeout {
		t.Fatalf("long timeout %s must exceed default %s", longCommandTimeout, defaultCommandTimeout)
	}
}

func TestDefaultCommandDirUsesRepoRoot(t *testing.T) {
	h := testHarness(t)

	if got := h.defaultCommandDir(); got != h.root {
		t.Fatalf("default command dir = %s, want repo root %s", got, h.root)
	}
}

func TestPackagedCommandDirUsesOutsideRunDir(t *testing.T) {
	h := testHarness(t)
	h.mode = modePackaged
	h.runDir = filepath.Join(t.TempDir(), "outside")

	if got := h.defaultCommandDir(); got != h.runDir {
		t.Fatalf("default command dir = %s, want packaged run dir %s", got, h.runDir)
	}
	if strings.HasPrefix(h.runDir, h.root) {
		t.Fatalf("test setup bug: packaged run dir is under repo root")
	}
}

func TestPathInside(t *testing.T) {
	tmp := t.TempDir()
	inside, err := pathInside(tmp, filepath.Join(tmp, "child"))
	if err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatalf("child path not detected inside parent")
	}

	outside, err := pathInside(tmp, filepath.Join(filepath.Dir(tmp), "sibling"))
	if err != nil {
		t.Fatal(err)
	}
	if outside {
		t.Fatalf("sibling path detected inside parent")
	}
}

func TestSafeRemoveAllRejectsUnsafePaths(t *testing.T) {
	tmp := t.TempDir()
	safeDir := filepath.Join(tmp, "codemesh-e2e-good")
	if err := os.Mkdir(safeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safeRemoveAll(safeDir); err != nil {
		t.Fatalf("safeRemoveAll safe dir error = %v", err)
	}
	if err := safeRemoveAll(tmp); err == nil {
		t.Fatalf("safeRemoveAll accepted non-harness temp dir")
	}
	if err := safeRemoveAll(filepath.Dir(tmp)); err == nil {
		t.Fatalf("safeRemoveAll accepted parent temp dir")
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("CODEMESH_E2E_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	switch args[1] {
	case "fail-output":
		os.Stdout.WriteString("stdout line\n")
		os.Stderr.WriteString("stderr line\n")
		os.Exit(7)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func testHarness(t *testing.T) *harness {
	t.Helper()
	tmp := t.TempDir()
	return &harness{
		root:         tmp,
		tmp:          tmp,
		codemeshHome: filepath.Join(tmp, "codemesh-home"),
		home:         filepath.Join(tmp, "home"),
		workspace:    filepath.Join(tmp, "workspace"),
		runDir:       tmp,
		output:       &bytes.Buffer{},
	}
}
