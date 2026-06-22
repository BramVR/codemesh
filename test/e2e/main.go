package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultCommandTimeout = 30 * time.Second
	longCommandTimeout    = 2 * time.Minute
	modeDefault           = "default"
	modePackaged          = "packaged"
)

type result struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
	TimedOut bool   `json:"timed_out,omitempty"`
}

type report struct {
	StartedAt    string   `json:"started_at"`
	Mode         string   `json:"mode"`
	Binary       string   `json:"binary"`
	CodeMeshHome string   `json:"codemesh_home"`
	Workspace    string   `json:"workspace"`
	RunDir       string   `json:"run_dir"`
	Results      []result `json:"results"`
}

type harness struct {
	root         string
	tmp          string
	bin          string
	externalBin  bool
	codemeshHome string
	home         string
	workspace    string
	runDir       string
	mode         string
	reportPath   string
	output       io.Writer
	results      []result
}

type commandSpec struct {
	Label      string
	Dir        string
	Name       string
	Args       []string
	Timeout    time.Duration
	Env        []string
	UseHostEnv bool
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL harness setup: %v\n", err)
		os.Exit(1)
	}

	tmp, err := os.MkdirTemp("", "codemesh-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL harness setup: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := safeRemoveAll(tmp); err != nil {
			fmt.Fprintf(os.Stderr, "WARN harness cleanup: %v\n", err)
		}
	}()

	h := &harness{
		root:         root,
		tmp:          tmp,
		codemeshHome: filepath.Join(tmp, "codemesh-home"),
		home:         filepath.Join(tmp, "home"),
		workspace:    filepath.Join(tmp, "workspace"),
		runDir:       filepath.Join(tmp, "run"),
		mode:         e2eMode(),
		reportPath:   reportPath(root),
		output:       os.Stdout,
	}
	if bin, external, err := binaryPath(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL harness setup: %v\n", err)
		os.Exit(1)
	} else {
		h.bin = bin
		h.externalBin = external
	}

	exitCode := h.run()
	os.Exit(exitCode)
}

func (h *harness) run() int {
	if err := os.MkdirAll(filepath.Dir(h.bin), 0o755); err != nil {
		return h.fail("harness setup", err)
	}
	if err := os.MkdirAll(h.codemeshHome, 0o755); err != nil {
		return h.fail("harness setup", err)
	}
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		return h.fail("harness setup", err)
	}
	if err := os.WriteFile(filepath.Join(h.home, ".gitconfig"), nil, 0o644); err != nil {
		return h.fail("harness setup", err)
	}
	if err := os.MkdirAll(h.workspace, 0o755); err != nil {
		return h.fail("harness setup", err)
	}
	if err := os.MkdirAll(h.runDir, 0o755); err != nil {
		return h.fail("harness setup", err)
	}
	if h.mode == modePackaged {
		inside, err := pathInside(h.root, h.runDir)
		if err != nil {
			return h.fail("harness setup", err)
		}
		if inside {
			return h.fail("harness setup", fmt.Errorf("packaged run dir must be outside repo: %s", h.runDir))
		}
	}
	if err := h.createGitFixture("future-project"); err != nil {
		return h.fail("harness fixture", err)
	}

	if !h.externalBin {
		if ok := h.buildBinary(); !ok {
			h.writeReport()
			return 1
		}
	} else if err := ensureExecutable(h.bin); err != nil {
		h.record(result{Name: "packaged binary setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.writeReport()
		return 1
	}

	h.caseHelpSmoke()
	h.caseInitHelpSmoke()
	h.caseInitSmoke()
	h.skip("project registry fixture smoke", "pending project registry commands")
	h.skip("readiness fixture smoke", "pending readiness commands")
	h.skip("hydration fixture smoke", "pending hydration commands")
	h.skip("agent prep fixture smoke", "pending agent prep commands")

	if err := h.writeReport(); err != nil {
		fmt.Printf("FAIL report: %v\n", err)
		return 1
	}

	for _, r := range h.results {
		if r.Status == "FAIL" {
			return 1
		}
	}
	return 0
}

func (h *harness) buildBinary() bool {
	r := h.runCommand(commandSpec{
		Label:      "build codemesh",
		Dir:        h.root,
		Name:       "go",
		Args:       []string{"build", "-o", h.bin, "./cmd/codemesh"},
		Timeout:    longCommandTimeout,
		UseHostEnv: true,
	})
	return r.Status == "PASS"
}

func (h *harness) caseHelpSmoke() {
	r := h.executeCommand(commandSpec{
		Label:   "help smoke",
		Name:    h.bin,
		Args:    []string{"--help"},
		Timeout: defaultCommandTimeout,
	})
	if r.Status == "PASS" && (!strings.Contains(r.Stdout, "CodeMesh") || !strings.Contains(r.Stdout, "codemesh")) {
		r.Status = "FAIL"
		r.Error = "help output did not identify CodeMesh"
	}
	h.record(r)
}

func (h *harness) caseInitHelpSmoke() {
	r := h.executeCommand(commandSpec{
		Label:   "init help smoke",
		Name:    h.bin,
		Args:    []string{"init", "--help"},
		Timeout: defaultCommandTimeout,
	})
	if r.Status == "PASS" && !strings.Contains(r.Stdout, "codemesh init [workspace-root]") {
		r.Status = "FAIL"
		r.Error = "init help output did not include usage"
	}
	h.record(r)
}

func (h *harness) caseInitSmoke() {
	r := h.executeCommand(commandSpec{
		Label:   "init smoke",
		Name:    h.bin,
		Args:    []string{"init", h.workspace},
		Timeout: defaultCommandTimeout,
	})
	if r.Status == "PASS" {
		if !strings.Contains(r.Stdout, "initialized CodeMesh") {
			r.Status = "FAIL"
			r.Error = "init output did not confirm initialization"
		} else if _, err := os.Stat(filepath.Join(h.codemeshHome, "codemesh.db")); err != nil {
			r.Status = "FAIL"
			r.Error = fmt.Sprintf("state database missing: %v", err)
		} else if _, err := os.Stat(filepath.Join(h.codemeshHome, "agents")); err != nil {
			r.Status = "FAIL"
			r.Error = fmt.Sprintf("agents dir missing: %v", err)
		}
	}
	h.record(r)
	if r.Status != "PASS" {
		return
	}

	rerun := h.executeCommand(commandSpec{
		Label:   "init rerun smoke",
		Name:    h.bin,
		Args:    []string{"init", h.workspace},
		Timeout: defaultCommandTimeout,
	})
	h.record(rerun)
}

func (h *harness) skip(name, reason string) {
	r := result{Name: name, Status: "SKIP", Error: reason}
	h.record(r)
}

func (h *harness) fail(name string, err error) int {
	r := result{Name: name, Status: "FAIL", Error: err.Error()}
	h.record(r)
	if reportErr := h.writeReport(); reportErr != nil {
		fmt.Fprintf(h.output, "FAIL report: %v\n", reportErr)
	}
	return 1
}

func (h *harness) exec(dir string, name string, args ...string) (string, string, error) {
	spec := commandSpec{
		Label:   name,
		Dir:     dir,
		Name:    name,
		Args:    args,
		Timeout: defaultCommandTimeout,
	}
	r := h.executeCommand(spec)
	if r.Status == "FAIL" {
		h.record(r)
	}
	return r.Stdout, r.Stderr, resultError(r)
}

func (h *harness) runCommand(spec commandSpec) result {
	r := h.executeCommand(spec)
	h.record(r)
	return r
}

func (h *harness) executeCommand(spec commandSpec) result {
	if spec.Timeout <= 0 {
		spec.Timeout = defaultCommandTimeout
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.WaitDelay = time.Second
	dir := spec.Dir
	if dir != "" {
		cmd.Dir = dir
	} else {
		cmd.Dir = h.defaultCommandDir()
	}
	if spec.UseHostEnv {
		cmd.Env = append(os.Environ(), spec.Env...)
	} else {
		cmd.Env = append(isolatedEnv(h.codemeshHome, h.workspace, h.home), spec.Env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	r := result{
		Name:     spec.Label,
		Status:   "PASS",
		Duration: formatDuration(time.Since(start)),
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}
	if err != nil {
		r.Status = "FAIL"
		r.Error = err.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.Status = "FAIL"
		r.TimedOut = true
		r.Error = fmt.Sprintf("timeout after %s", spec.Timeout)
	}
	return r
}

func resultError(r result) error {
	if r.Status == "PASS" {
		return nil
	}
	if r.Error != "" {
		return errors.New(r.Error)
	}
	return fmt.Errorf("%s failed with exit code %d", r.Name, r.ExitCode)
}

func (h *harness) createGitFixture(name string) error {
	path := filepath.Join(h.workspace, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if _, _, err := h.exec(path, "git", "init", "-b", "main"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("# Future Project\n"), 0o644); err != nil {
		return err
	}
	if _, _, err := h.exec(path, "git", "add", "README.md"); err != nil {
		return err
	}
	if _, _, err := h.exec(path, "git", "-c", "user.name=CodeMesh E2E", "-c", "user.email=e2e@example.invalid", "commit", "-m", "Initial fixture"); err != nil {
		return err
	}
	return nil
}

func (h *harness) writeReport() error {
	if err := os.MkdirAll(filepath.Dir(h.reportPath), 0o755); err != nil {
		return err
	}
	r := report{
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		Mode:         h.mode,
		Binary:       h.bin,
		CodeMeshHome: h.codemeshHome,
		Workspace:    h.workspace,
		RunDir:       h.runDir,
		Results:      h.results,
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.reportPath, data, 0o644)
}

func (h *harness) print(r result) {
	if r.Duration != "" {
		fmt.Fprintf(h.output, "%s %s (exit=%d duration=%s)\n", r.Status, r.Name, r.ExitCode, r.Duration)
	} else {
		fmt.Fprintf(h.output, "%s %s\n", r.Status, r.Name)
	}
	if r.Status != "FAIL" {
		return
	}
	if r.Error != "" {
		fmt.Fprintf(h.output, "  error: %s\n", r.Error)
	}
	if r.Stdout != "" {
		fmt.Fprintf(h.output, "  stdout:\n%s\n", indent(r.Stdout))
	}
	if r.Stderr != "" {
		fmt.Fprintf(h.output, "  stderr:\n%s\n", indent(r.Stderr))
	}
}

func (h *harness) record(r result) {
	h.print(r)
	h.results = append(h.results, r)
}

func repoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	root := strings.TrimSpace(stdout.String())
	if root == "" {
		return "", errors.New("empty git root")
	}
	return root, nil
}

func (h *harness) defaultCommandDir() string {
	if h.mode == modePackaged {
		return h.runDir
	}
	return h.root
}

func e2eMode() string {
	if os.Getenv("CODEMESH_E2E_MODE") == modePackaged {
		return modePackaged
	}
	return modeDefault
}

func binaryPath(tmp string) (string, bool, error) {
	if path := strings.TrimSpace(os.Getenv("CODEMESH_E2E_BINARY")); path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", false, err
		}
		return abs, true, nil
	}
	return filepath.Join(tmp, "bin", "codemesh"), false, nil
}

func ensureExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat packaged binary: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("packaged binary is a directory: %s", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("packaged binary is not executable: %s", path)
	}
	return nil
}

func pathInside(parent, child string) (bool, error) {
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false, err
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false, err
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."), nil
}

func reportPath(root string) string {
	if path := os.Getenv("CODEMESH_E2E_REPORT"); path != "" {
		return path
	}
	return filepath.Join(root, "tmp", "e2e-report.json")
}

func safeRemoveAll(path string) error {
	if path == "" {
		return errors.New("refusing to remove empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(tmp, abs)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return fmt.Errorf("refusing to remove path outside temp dir: %s", abs)
	}
	if !strings.HasPrefix(filepath.Base(abs), "codemesh-e2e-") {
		return fmt.Errorf("refusing to remove non-harness temp path: %s", abs)
	}
	return os.RemoveAll(abs)
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.String()
	}
	return d.Round(time.Millisecond).String()
}

func isolatedEnv(codemeshHome, workspace, home string) []string {
	allow := map[string]bool{
		"PATH":        true,
		"TMPDIR":      true,
		"USER":        true,
		"SHELL":       true,
		"TERM":        true,
		"GOCACHE":     true,
		"GOMODCACHE":  true,
		"GOROOT":      true,
		"GOPATH":      true,
		"CGO_ENABLED": true,
	}
	var env []string
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allow[key] {
			env = append(env, item)
		}
	}
	env = append(env,
		"HOME="+home,
		"CODEMESH_HOME="+codemeshHome,
		"CODEMESH_WORKSPACE="+workspace,
		"GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GOENV=off",
		"GOPROXY=off",
		"GOSUMDB=off",
	)
	return env
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}
