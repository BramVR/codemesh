package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type result struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

type report struct {
	StartedAt    string   `json:"started_at"`
	Binary       string   `json:"binary"`
	CodeMeshHome string   `json:"codemesh_home"`
	Workspace    string   `json:"workspace"`
	Results      []result `json:"results"`
}

type harness struct {
	root         string
	tmp          string
	bin          string
	codemeshHome string
	home         string
	workspace    string
	reportPath   string
	results      []result
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
	defer os.RemoveAll(tmp)

	h := &harness{
		root:         root,
		tmp:          tmp,
		bin:          filepath.Join(tmp, "bin", "codemesh"),
		codemeshHome: filepath.Join(tmp, "codemesh-home"),
		home:         filepath.Join(tmp, "home"),
		workspace:    filepath.Join(tmp, "workspace"),
		reportPath:   reportPath(root),
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
	if err := h.createGitFixture("future-project"); err != nil {
		return h.fail("harness fixture", err)
	}

	if ok := h.buildBinary(); !ok {
		h.writeReport()
		return 1
	}

	h.caseHelpSmoke()
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
	start := time.Now()
	stdout, stderr, err := h.exec("", "go", "build", "-o", h.bin, "./cmd/codemesh")
	r := result{Name: "build codemesh", Status: "PASS", Duration: time.Since(start).String(), Stdout: stdout, Stderr: stderr}
	if err != nil {
		r.Status = "FAIL"
		r.Error = err.Error()
	}
	h.print(r)
	h.results = append(h.results, r)
	return r.Status == "PASS"
}

func (h *harness) caseHelpSmoke() {
	start := time.Now()
	stdout, stderr, err := h.exec("", h.bin, "--help")
	r := result{Name: "help smoke", Status: "PASS", Duration: time.Since(start).String(), Stdout: stdout, Stderr: stderr}
	if err != nil {
		r.Status = "FAIL"
		r.Error = err.Error()
	} else if !strings.Contains(stdout, "CodeMesh") || !strings.Contains(stdout, "codemesh") {
		r.Status = "FAIL"
		r.Error = "help output did not identify CodeMesh"
	}
	h.print(r)
	h.results = append(h.results, r)
}

func (h *harness) skip(name, reason string) {
	r := result{Name: name, Status: "SKIP", Error: reason}
	h.print(r)
	h.results = append(h.results, r)
}

func (h *harness) fail(name string, err error) int {
	r := result{Name: name, Status: "FAIL", Error: err.Error()}
	h.print(r)
	h.results = append(h.results, r)
	if reportErr := h.writeReport(); reportErr != nil {
		fmt.Printf("FAIL report: %v\n", reportErr)
	}
	return 1
}

func (h *harness) exec(dir string, name string, args ...string) (string, string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	} else {
		cmd.Dir = h.root
	}
	cmd.Env = isolatedEnv(h.codemeshHome, h.workspace, h.home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
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
		Binary:       h.bin,
		CodeMeshHome: h.codemeshHome,
		Workspace:    h.workspace,
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
	fmt.Printf("%s %s\n", r.Status, r.Name)
	if r.Status != "FAIL" {
		return
	}
	if r.Error != "" {
		fmt.Printf("  error: %s\n", r.Error)
	}
	if r.Stdout != "" {
		fmt.Printf("  stdout:\n%s\n", indent(r.Stdout))
	}
	if r.Stderr != "" {
		fmt.Printf("  stderr:\n%s\n", indent(r.Stderr))
	}
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

func reportPath(root string) string {
	if path := os.Getenv("CODEMESH_E2E_REPORT"); path != "" {
		return path
	}
	return filepath.Join(root, "tmp", "e2e-report.json")
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
