package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultCommandTimeout = 30 * time.Second
	longCommandTimeout    = 2 * time.Minute
	modeSource            = "source"
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
	StartedAt    string             `json:"started_at"`
	Mode         string             `json:"mode"`
	Binary       reportBinary       `json:"binary"`
	Isolation    reportIsolation    `json:"isolation"`
	Summary      reportSummary      `json:"summary"`
	SecretSafety reportSecretSafety `json:"secret_safety"`
	Results      []result           `json:"results"`
}

type reportBinary struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	External bool   `json:"external"`
}

type reportIsolation struct {
	CodeMeshHome string `json:"codemesh_home"`
	Home         string `json:"home"`
	Workspace    string `json:"workspace"`
	RunDir       string `json:"run_dir"`
	GitConfig    string `json:"git_config"`
}

type reportSummary struct {
	Pass  int `json:"pass"`
	Fail  int `json:"fail"`
	Skip  int `json:"skip"`
	Total int `json:"total"`
}

type reportSecretSafety struct {
	Enabled        bool `json:"enabled"`
	RedactedValues int  `json:"redacted_values"`
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
	startedAt    time.Time
	redactions   []string
	reportPath   string
	output       io.Writer
	results      []result
}

type offlineGitFixtures struct {
	Root     string
	Remotes  string
	Sources  string
	Projects []gitFixtureProject
}

type gitFixtureProject struct {
	Name        string
	Remote      string
	Source      string
	BaseBranch  string
	RequiredEnv []string
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

type scenario struct {
	h            *harness
	name         string
	codemeshHome string
	fixtures     offlineGitFixtures
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
		startedAt:    time.Now().UTC(),
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
	if _, err := h.createOfflineGitFixtures(); err != nil {
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
	h.caseMachineRegisterWorkflow()
	h.caseOfflineGitFixtureSmoke()
	h.caseNegativeCLIWorkflow()
	h.caseProjectRegistryScanWorkflow()
	h.caseProjectRegistryFixtureWorkflow()
	h.caseProjectRegistryAliasPathStateWorkflow()
	h.caseReadinessStatusFixtureWorkflow()
	h.caseHydrationFixtureWorkflow()
	h.caseAgentPrepFixtureWorkflow()
	h.record(result{Name: "offline e2e boundary: live network not required", Status: "PASS", ExitCode: 0})

	if !h.caseSecretSafetyReportAndStateStore() {
		_ = h.writeReport()
		return 1
	}

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

func (h *harness) caseProjectRegistryScanWorkflow() {
	s, err := h.newScenario("project registry scan")
	if err != nil {
		h.record(result{Name: "project registry scan workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	fixtures := s.fixtures
	scan := s.command("project registry scan fixtures", "scan", fixtures.Sources)
	if scan.Status != "PASS" || !s.expectOutput(scan, "added: clean-repo", "added: dirty-source", "scan complete") {
		return
	}

	rerun := s.command("project registry scan fixtures rerun", "scan", fixtures.Sources)
	if rerun.Status != "PASS" || !s.expectOutput(rerun, "unchanged: clean-repo") {
		return
	}
	if !s.expectProjectRowCount("project registry scan idempotent state rows", 7) {
		return
	}

	tree := s.command("project registry tree scanned fixtures", "tree")
	clean := fixtures.Project("clean-repo")
	if clean == nil {
		s.failScenarioAssertion("project registry tree scanned fixtures", "clean-repo fixture missing")
		return
	}
	if tree.Status == "PASS" {
		s.expectOutput(tree, "clean-repo", "present", clean.Source)
	}
}

func (h *harness) caseMachineRegisterWorkflow() {
	s, err := h.newScenario("machine register")
	if err != nil {
		h.record(result{Name: "machine register workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	firstRoot := filepath.Join(s.fixtures.Root, "workspace-one")
	secondRoot := filepath.Join(s.fixtures.Root, "workspace-two")
	if err := os.MkdirAll(firstRoot, 0o755); err != nil {
		h.record(result{Name: "machine register first root setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.MkdirAll(secondRoot, 0o755); err != nil {
		h.record(result{Name: "machine register second root setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	register := s.command("machine register human output", "machine", "register", firstRoot)
	if register.Status != "PASS" || !s.expectOutput(register, "machine registered", "id: ", "hostname: ", "os: ", "architecture: ", "workspace_root: "+firstRoot, "created_at: ", "updated_at: ") {
		return
	}
	firstID := valueAfterPrefix(register.Stdout, "id: ")
	if firstID == "" {
		s.failCommandAssertion(register, "stdout did not include machine id")
		return
	}

	rerun := s.command("machine register updates facts", "machine", "register", secondRoot)
	if rerun.Status != "PASS" || !s.expectOutput(rerun, "id: "+firstID, "workspace_root: "+secondRoot) {
		return
	}

	jsonRun := s.command("machine register json output", "machine", "register", secondRoot, "--json")
	if jsonRun.Status != "PASS" {
		return
	}
	var payload struct {
		ID            string `json:"id"`
		WorkspaceRoot string `json:"workspace_root"`
	}
	if err := json.Unmarshal([]byte(jsonRun.Stdout), &payload); err != nil {
		s.failCommandAssertion(jsonRun, "stdout was not JSON: "+err.Error())
		return
	}
	if payload.ID != firstID || payload.WorkspaceRoot != secondRoot {
		s.failCommandAssertion(jsonRun, fmt.Sprintf("JSON payload = %#v, want id %q workspace %q", payload, firstID, secondRoot))
		return
	}

	machines, err := readMachineRowsFromStore(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		h.record(result{Name: "machine register state row", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if len(machines) != 1 || machines[0].ID != firstID || machines[0].WorkspaceRoot != secondRoot {
		h.record(result{Name: "machine register state row", Status: "FAIL", Error: fmt.Sprintf("machine rows = %#v", machines), ExitCode: -1})
		return
	}
	h.record(result{Name: "machine register state row", Status: "PASS", ExitCode: 0})
}

func (h *harness) caseProjectRegistryFixtureWorkflow() {
	s, err := h.newScenario("project registry add")
	if err != nil {
		h.record(result{Name: "project registry fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	project := s.fixture("clean-repo")
	if project == nil {
		h.record(result{Name: "project registry fixture workflow", Status: "FAIL", Error: "clean-repo fixture missing", ExitCode: -1})
		return
	}
	projectSource, err := filepath.EvalSymlinks(project.Source)
	if err != nil {
		h.record(result{Name: "project registry fixture source canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	add := s.command("project registry add clean repo", "add", project.Source)
	if add.Status != "PASS" {
		return
	}
	if !s.expectProjectRows("project registry add state row", projectRow{
		Alias:            "clean-repo",
		NormalizedRemote: filepath.Clean(project.Remote),
		CloneURL:         project.Remote,
		LocalPath:        projectSource,
	}) {
		return
	}
	duplicateAdd := s.expectedFailure("project registry add clean repo rerun", "add", project.Source)
	if duplicateAdd.Status != "FAIL" {
		duplicateAdd.Status = "FAIL"
		duplicateAdd.Error = "duplicate add unexpectedly passed"
	} else if !strings.Contains(duplicateAdd.Stderr, "already exists") || !strings.Contains(duplicateAdd.Stderr, "clean-repo") {
		duplicateAdd.Error = "duplicate add did not report existing project identity"
	} else {
		duplicateAdd.Status = "PASS"
		duplicateAdd.Error = ""
	}
	s.record(duplicateAdd)
	if duplicateAdd.Status != "PASS" || !s.expectProjectRowCount("project registry add idempotent state rows", 1) {
		return
	}
	tree := s.command("project registry tree", "tree")
	s.expectOutput(tree, "clean-repo", "present", projectSource)
}

func (h *harness) caseProjectRegistryAliasPathStateWorkflow() {
	s, err := h.newScenario("project registry alias path state")
	if err != nil {
		h.record(result{Name: "project registry alias path state workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	alpha, err := h.createRemoteOnlyFixture(s.fixtures, "alias-alpha", nil)
	if err != nil {
		h.record(result{Name: "project registry alias alpha setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	beta, err := h.createRemoteOnlyFixture(s.fixtures, "alias-beta", nil)
	if err != nil {
		h.record(result{Name: "project registry alias beta setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	aliasRoot := filepath.Join(s.fixtures.Sources, "aliases")
	alphaPath := filepath.Join(aliasRoot, "a", "shared")
	betaPath := filepath.Join(aliasRoot, "b", "shared")
	if err := h.cloneFixtureRemote(alpha, alphaPath); err != nil {
		h.record(result{Name: "project registry alias alpha clone", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	alphaPath, err = filepath.EvalSymlinks(alphaPath)
	if err != nil {
		h.record(result{Name: "project registry alias alpha canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := h.cloneFixtureRemote(beta, betaPath); err != nil {
		h.record(result{Name: "project registry alias beta clone", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	betaPath, err = filepath.EvalSymlinks(betaPath)
	if err != nil {
		h.record(result{Name: "project registry alias beta canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	scan := s.command("project registry duplicate alias scan", "scan", aliasRoot)
	if scan.Status != "PASS" || !s.expectOutput(scan, "added: shared "+alphaPath, "added: shared-2 "+betaPath) {
		return
	}
	if !s.expectProjectRows("project registry duplicate alias state rows",
		projectRow{Alias: "shared", NormalizedRemote: filepath.Clean(alpha.Remote), CloneURL: alpha.Remote, LocalPath: alphaPath},
		projectRow{Alias: "shared-2", NormalizedRemote: filepath.Clean(beta.Remote), CloneURL: beta.Remote, LocalPath: betaPath},
	) {
		return
	}

	rerun := s.command("project registry duplicate alias scan rerun", "scan", aliasRoot)
	if rerun.Status != "PASS" || !s.expectOutput(rerun, "unchanged: shared "+alphaPath, "unchanged: shared-2 "+betaPath) {
		return
	}
	if !s.expectProjectRows("project registry duplicate alias deterministic state rows",
		projectRow{Alias: "shared", NormalizedRemote: filepath.Clean(alpha.Remote), CloneURL: alpha.Remote, LocalPath: alphaPath},
		projectRow{Alias: "shared-2", NormalizedRemote: filepath.Clean(beta.Remote), CloneURL: beta.Remote, LocalPath: betaPath},
	) {
		return
	}

	movedAlphaPath := filepath.Join(aliasRoot, "c", "shared-moved")
	if err := os.RemoveAll(alphaPath); err != nil {
		h.record(result{Name: "project registry alias remove old path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := h.cloneFixtureRemote(alpha, movedAlphaPath); err != nil {
		h.record(result{Name: "project registry alias moved clone", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	movedAlphaPath, err = filepath.EvalSymlinks(movedAlphaPath)
	if err != nil {
		h.record(result{Name: "project registry alias moved canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	update := s.command("project registry known remote moved path scan", "scan", aliasRoot)
	if update.Status != "PASS" || !s.expectOutput(update, "updated: shared "+movedAlphaPath, "unchanged: shared-2 "+betaPath) {
		return
	}
	if !s.expectProjectRows("project registry moved path state rows",
		projectRow{Alias: "shared", NormalizedRemote: filepath.Clean(alpha.Remote), CloneURL: alpha.Remote, LocalPath: movedAlphaPath},
		projectRow{Alias: "shared-2", NormalizedRemote: filepath.Clean(beta.Remote), CloneURL: beta.Remote, LocalPath: betaPath},
	) {
		return
	}

	if err := os.RemoveAll(betaPath); err != nil {
		h.record(result{Name: "project registry alias remove present path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	tree := s.command("project registry derived missing tree", "tree")
	if tree.Status != "PASS" || !s.expectOutput(tree, "shared present "+movedAlphaPath, "shared-2 missing "+betaPath) {
		return
	}
	if !s.expectProjectRows("project registry missing path metadata unchanged",
		projectRow{Alias: "shared", NormalizedRemote: filepath.Clean(alpha.Remote), CloneURL: alpha.Remote, LocalPath: movedAlphaPath},
		projectRow{Alias: "shared-2", NormalizedRemote: filepath.Clean(beta.Remote), CloneURL: beta.Remote, LocalPath: betaPath},
	) {
		return
	}
	if !s.expectProjectSchemaNoPresenceColumns("project registry presence derived state schema") {
		return
	}
}

func (h *harness) caseReadinessStatusFixtureWorkflow() {
	s, err := h.newScenario("readiness status")
	if err != nil {
		h.record(result{Name: "readiness status fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	scan := s.command("readiness status scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" {
		return
	}

	tree := s.command("readiness tree scanned fixtures", "tree")
	if tree.Status != "PASS" {
		return
	}

	clean := s.command("readiness status clean repo", "status", "clean-repo", "--base", "main")
	if clean.Status != "PASS" || !s.expectOutput(clean, "state: present", "path_present: true", "warnings: none", "blockers: none") {
		return
	}
	if !s.expectTreeStatusAgreement(tree, clean, "clean-repo") {
		return
	}

	dirty := s.command("readiness status dirty source", "status", "dirty-source", "--base", "main")
	if dirty.Status != "PASS" || !s.expectOutput(dirty, "state: dirty", "warning: dirty-checkout", "blockers: none") {
		return
	}
	if !s.expectTreeStatusAgreement(tree, dirty, "dirty-source") {
		return
	}

	missingProject, err := h.createClonedFixture(s.fixtures, "readiness-missing-path", nil)
	if err != nil {
		h.record(result{Name: "readiness status missing path setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("readiness add missing path project", "add", missingProject.Source); add.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(missingProject.Source); err != nil {
		h.record(result{Name: "readiness remove missing path project", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	missingTree := s.command("readiness tree missing path", "tree")
	missing := s.command("readiness status missing path", "status", missingProject.Name, "--base", "main")
	if missing.Status != "PASS" || !s.expectOutput(missing, "state: missing", "path_present: false", "blocker: missing-path") {
		return
	}
	if !s.expectTreeStatusAgreement(missingTree, missing, missingProject.Name) {
		return
	}

	missingBase := s.fixture("missing-base-branch")
	if missingBase == nil {
		h.record(result{Name: "readiness status missing base", Status: "FAIL", Error: "missing-base-branch fixture missing", ExitCode: -1})
		return
	}
	base := s.command("readiness status missing base", "status", "missing-base-branch", "--base", missingBase.BaseBranch)
	s.expectOutput(base, "state: blocked", "blocker: missing-base")

	fetchFailure, err := h.createClonedFixture(s.fixtures, "readiness-fetch-failure", func(source string) error {
		return runGitNoOutput(source, "remote", "set-url", "origin", filepath.Join(s.fixtures.Remotes, "missing-fetch-remote.git"))
	})
	if err != nil {
		h.record(result{Name: "readiness fetch failure setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("readiness add fetch failure project", "add", fetchFailure.Source); add.Status != "PASS" {
		return
	}
	fetch := s.command("readiness status fetch failure", "status", fetchFailure.Name, "--base", "main")
	if fetch.Status != "PASS" || !s.expectOutput(fetch, "state: stale", "blocker: fetch-failed") {
		return
	}

	invalidPolicy, err := h.createClonedFixtureWithSeed(s.fixtures, "readiness-invalid-policy", func(seed string) error {
		return os.WriteFile(filepath.Join(seed, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: stop\n"), 0o644)
	}, nil)
	if err != nil {
		h.record(result{Name: "readiness invalid policy setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("readiness add invalid policy project", "add", invalidPolicy.Source); add.Status != "PASS" {
		return
	}
	invalidTree := s.command("readiness tree invalid policy", "tree")
	invalid := s.command("readiness status invalid policy", "status", invalidPolicy.Name, "--base", "main")
	if invalid.Status != "PASS" || !s.expectOutput(invalid, "state: blocked", "blocker: invalid-policy", "agent.env.mode") {
		return
	}
	if !s.expectTreeStatusAgreement(invalidTree, invalid, invalidPolicy.Name) {
		return
	}

	envWarn := s.command("readiness status env warn", "status", "required-env-warn", "--base", "main")
	if envWarn.Status != "PASS" || !s.expectOutput(envWarn, "state: present", "warning: missing-env-file", "warning: missing-env-key", "blockers: none") {
		return
	}

	envMissing := s.fixture("required-env-missing")
	if envMissing == nil {
		h.record(result{Name: "readiness status missing env", Status: "FAIL", Error: "required-env-missing fixture missing", ExitCode: -1})
		return
	}
	envResult := s.command("readiness status missing env", "status", "required-env-missing")
	if s.expectOutput(envResult, "state: blocked", "blocker: missing-env-file", "blocker: missing-env-key") {
		s.expectNoOutput(envResult, "=", fakeEnvFixtureFileSecret(), fakeEnvFixtureKeySecret())
	}

	envPresent := s.commandEnv("readiness status env present no secret leak", []string{"CODEMESH_E2E_PRESENT_ENV=" + fakeEnvFixtureKeySecret()}, "status", "required-env-present", "--base", "main")
	if envPresent.Status != "PASS" || !s.expectOutput(envPresent, "state: present", "warnings: none", "blockers: none") {
		return
	}
	s.expectNoOutput(envPresent, fakeEnvFixtureFileSecret(), fakeEnvFixtureKeySecret())
}

func (h *harness) caseSecretSafetyReportAndStateStore() bool {
	secrets := fakeEnvFixtureSecrets()
	for _, r := range h.results {
		if containsAnySecret(r.Stdout, secrets) || containsAnySecret(r.Stderr, secrets) || containsAnySecret(r.Error, secrets) {
			h.record(result{Name: "secret safety command output", Status: "FAIL", Error: "fake env secret marker appeared in recorded command output", ExitCode: -1})
			return false
		}
	}
	if err := h.writeReport(); err != nil {
		h.record(result{Name: "secret safety report write", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	reportData, err := os.ReadFile(h.reportPath)
	if err != nil {
		h.record(result{Name: "secret safety report read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if containsAnySecret(string(reportData), secrets) {
		h.record(result{Name: "secret safety JSON report", Status: "FAIL", Error: "fake env secret marker appeared in e2e JSON report", ExitCode: -1})
		return false
	}
	for _, path := range h.stateStorePaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			h.record(result{Name: "secret safety state store read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
			return false
		}
		if containsAnySecret(string(data), secrets) {
			h.record(result{Name: "secret safety state store", Status: "FAIL", Error: "fake env secret marker appeared in state store data: " + path, ExitCode: -1})
			return false
		}
	}
	h.record(result{Name: "secret safety public artifacts", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) stateStorePaths() []string {
	var paths []string
	_ = filepath.WalkDir(h.tmp, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if d.Name() == "codemesh.db" {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

func containsAnySecret(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

func (h *harness) caseNegativeCLIWorkflow() {
	s, err := h.newScenario("negative cli")
	if err != nil {
		h.record(result{Name: "negative cli workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	unknown := s.expectedFailure("negative cli unknown command", "bogus")
	if !s.expectFailure(unknown, 2, "unknown command: bogus", "Usage:") {
		return
	}
	if !s.expectNoStateStore("negative cli unknown command no state store") {
		return
	}
	if !s.expectPathMissing("negative cli unknown command no workspace mutation", filepath.Join(s.h.workspace, "bogus")) {
		return
	}

	invalidArgCount := s.expectedFailure("negative cli invalid hydrate arg count", "hydrate")
	if !s.expectFailure(invalidArgCount, 2, "hydrate requires exactly one project", "codemesh hydrate <project>") {
		return
	}
	if !s.expectNoStateStore("negative cli invalid arg count no state store") {
		return
	}

	scan := s.command("negative cli scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" || !s.expectProjectRowCount("negative cli baseline state rows", 7) {
		return
	}

	unknownProject := s.expectedFailure("negative cli unknown project", "status", "ghost-project")
	if !s.expectFailure(unknownProject, 1, "unknown project: ghost-project") {
		return
	}
	if !s.expectProjectRowCount("negative cli unknown project state unchanged", 7) {
		return
	}
	if !s.expectPathMissing("negative cli unknown project no filesystem mutation", filepath.Join(s.fixtures.Sources, "ghost-project")) {
		return
	}

	conflict, err := h.createClonedFixture(s.fixtures, "negative-hydrate-conflict", nil)
	if err != nil {
		h.record(result{Name: "negative cli hydrate conflict setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if conflict.Source, err = filepath.EvalSymlinks(conflict.Source); err != nil {
		h.record(result{Name: "negative cli hydrate conflict canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("negative cli hydrate conflict add", "add", conflict.Source); add.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(conflict.Source); err != nil {
		h.record(result{Name: "negative cli hydrate conflict remove source", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.MkdirAll(conflict.Source, 0o755); err != nil {
		h.record(result{Name: "negative cli hydrate conflict dir setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	conflictMarker := filepath.Join(conflict.Source, "local.txt")
	if err := os.WriteFile(conflictMarker, []byte("keep me\n"), 0o644); err != nil {
		h.record(result{Name: "negative cli hydrate conflict marker setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	hydrateConflict := s.expectedFailure("negative cli hydrate path conflict", "hydrate", conflict.Name)
	if !s.expectFailure(hydrateConflict, 1, "hydrate project: path conflict", conflict.Source) {
		return
	}
	if !s.expectProjectRowCount("negative cli hydrate conflict state unchanged", 8) {
		return
	}
	if got, err := os.ReadFile(conflictMarker); err != nil || string(got) != "keep me\n" {
		h.record(result{Name: "negative cli hydrate conflict marker unchanged", Status: "FAIL", Error: fmt.Sprintf("marker changed or missing: got %q err %v", got, err), ExitCode: -1})
		return
	}
	h.record(result{Name: "negative cli hydrate conflict marker unchanged", Status: "PASS", ExitCode: 0})
}

func (h *harness) caseHydrationFixtureWorkflow() {
	s, err := h.newScenario("hydration")
	if err != nil {
		h.record(result{Name: "hydration fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	target, err := h.createClonedFixture(s.fixtures, "hydrate-target", nil)
	if err != nil {
		h.record(result{Name: "hydration fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if target.Source, err = filepath.EvalSymlinks(target.Source); err != nil {
		h.record(result{Name: "hydration target canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	other, err := h.createClonedFixture(s.fixtures, "hydrate-other", nil)
	if err != nil {
		h.record(result{Name: "hydration fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if other.Source, err = filepath.EvalSymlinks(other.Source); err != nil {
		h.record(result{Name: "hydration other canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	conflict, err := h.createClonedFixture(s.fixtures, "hydrate-conflict", nil)
	if err != nil {
		h.record(result{Name: "hydration fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if conflict.Source, err = filepath.EvalSymlinks(conflict.Source); err != nil {
		h.record(result{Name: "hydration conflict canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	for _, project := range []gitFixtureProject{target, other, conflict} {
		add := s.command("hydration add "+project.Name, "add", project.Source)
		if add.Status != "PASS" {
			return
		}
	}
	if err := os.RemoveAll(target.Source); err != nil {
		h.record(result{Name: "hydration remove target source", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.RemoveAll(other.Source); err != nil {
		h.record(result{Name: "hydration remove other source", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.RemoveAll(conflict.Source); err != nil {
		h.record(result{Name: "hydration remove conflict source", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.MkdirAll(conflict.Source, 0o755); err != nil {
		h.record(result{Name: "hydration conflict dir setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	conflictMarker := filepath.Join(conflict.Source, "local.txt")
	if err := os.WriteFile(conflictMarker, []byte("do not overwrite\n"), 0o644); err != nil {
		h.record(result{Name: "hydration conflict file setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	before := s.command("hydration tree before", "tree")
	if before.Status != "PASS" || !s.expectOutput(before, "hydrate-target missing", "hydrate-other missing", "hydrate-conflict blocked") {
		return
	}

	hydrate := s.command("hydrate missing fixture", "hydrate", "hydrate-target")
	if hydrate.Status != "PASS" || !s.expectOutput(hydrate, "hydrated project: hydrate-target") {
		return
	}
	if !s.expectPathExists("hydrate checkout exists", filepath.Join(target.Source, "README.md")) {
		return
	}
	if err := h.expectGitStatus(target.Source, ""); err != nil {
		h.record(result{Name: "hydrate checkout usable", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if !s.expectGitCheckoutAtBase("hydrate checkout branch", target.Source, target.BaseBranch) {
		return
	}
	if !s.expectGitOrigin("hydrate checkout origin", target.Source, target.Remote) {
		return
	}
	if !s.expectPathMissing("hydrate no sibling placeholder", other.Source) {
		return
	}

	noop := s.command("hydrate already present fixture", "hydrate", "hydrate-target")
	if noop.Status != "PASS" || !s.expectOutput(noop, "project already present: hydrate-target", "path: "+target.Source) {
		return
	}

	conflictResult := s.expectedFailure("hydrate path conflict refusal", "hydrate", "hydrate-conflict")
	if conflictResult.Status != "FAIL" {
		conflictResult.Status = "FAIL"
		conflictResult.Error = "path conflict hydrate unexpectedly passed"
	} else if !strings.Contains(conflictResult.Stderr, "path conflict") || !strings.Contains(conflictResult.Stderr, conflict.Source) {
		conflictResult.Error = "path conflict hydrate did not report the unsafe path"
	} else if got, err := os.ReadFile(conflictMarker); err != nil || string(got) != "do not overwrite\n" {
		conflictResult.Error = fmt.Sprintf("path conflict marker changed or missing: got %q err %v", got, err)
	} else {
		conflictResult.Status = "PASS"
		conflictResult.Error = ""
	}
	s.record(conflictResult)
	if conflictResult.Status != "PASS" {
		return
	}

	after := s.command("hydration tree after", "tree")
	if after.Status != "PASS" || !s.expectOutput(after, "hydrate-target present", "hydrate-other missing", "hydrate-conflict blocked") {
		return
	}

	status := s.command("hydration status after", "status", "hydrate-target", "--base", "main")
	s.expectOutput(status, "state: present", "path_present: true")
}

func (h *harness) caseAgentPrepFixtureWorkflow() {
	s, err := h.newScenario("agent prep")
	if err != nil {
		h.record(result{Name: "agent prep fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	scan := s.command("agent prep scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" {
		return
	}

	prepare := s.command("agent prep clean fixture", "agent", "prepare", "clean-repo", "--base", "main", "--profile", "codex")
	readyPath := valueAfterPrefix(prepare.Stdout, "ready_path: ")
	if prepare.Status == "PASS" {
		if readyPath == "" {
			prepare.Status = "FAIL"
			prepare.Error = "agent prepare output did not include ready_path"
			s.updateResult(prepare)
		} else if !strings.HasPrefix(readyPath, filepath.Join(s.codemeshHome, "agents")+string(filepath.Separator)) {
			prepare.Status = "FAIL"
			prepare.Error = "ready path was not under CodeMesh-managed agents storage"
			s.updateResult(prepare)
		} else if _, err := os.Stat(filepath.Join(readyPath, "README.md")); err != nil {
			prepare.Status = "FAIL"
			prepare.Error = fmt.Sprintf("ready checkout missing README: %v", err)
			s.updateResult(prepare)
		} else if _, err := os.Stat(filepath.Join(readyPath, ".git")); err != nil {
			prepare.Status = "FAIL"
			prepare.Error = fmt.Sprintf("ready checkout missing .git: %v", err)
			s.updateResult(prepare)
		} else if !s.expectGitCheckoutAtBase("agent prep ready checkout base", readyPath, "main") {
			return
		} else if _, err := os.Stat(filepath.Join(readyPath, "codemesh-run.json")); err != nil {
			prepare.Status = "FAIL"
			prepare.Error = fmt.Sprintf("ready metadata missing: %v", err)
			s.updateResult(prepare)
		} else if !s.expectAgentRunMetadata("agent prep state metadata", readyPath, "clean-repo", "main", "codex") {
			return
		} else if !s.expectOutput(prepare, "handoff_docs: 4") {
			return
		} else if !s.expectAgentRunHandoffDocs("agent prep default handoff docs", readyPath, []agentHandoffDoc{
			{Path: "AGENTS.md", Source: "default"},
			{Path: "CONTEXT.md", Source: "default"},
			{Path: "README.md", Source: "default"},
			{Path: "docs/adr/0001-fixture.md", Source: "default"},
		}) {
			return
		}
	}
	if prepare.Status != "PASS" {
		return
	}

	runs := s.command("agent runs list prepared fixture", "runs")
	if runs.Status != "PASS" || !s.expectOutput(runs, "project=clean-repo", "base=main", "profile=codex", "workspace="+readyPath) {
		return
	}

	clean := s.command("agent runs clean old fixture", "clean", "--older-than", "0d")
	if clean.Status != "PASS" || !s.expectOutput(clean, "deleted: 1", "kept: 0") {
		return
	}
	if !s.expectPathMissing("agent runs cleaned workspace", readyPath) {
		return
	}

	afterClean := s.command("agent runs list after clean", "runs")
	if afterClean.Status != "PASS" || !s.expectOutput(afterClean, "(empty)") {
		return
	}

	policyDocs := s.command("agent prep policy handoff docs", "agent", "prepare", "policy-docs", "--base", "main")
	if policyDocs.Status != "PASS" || !s.expectOutput(policyDocs, "warning: handoff-doc-missing", "blockers: none", "handoff_docs: 7") {
		return
	}
	if policyPath := s.expectReadyPath("agent prep policy docs ready path", policyDocs); policyPath != "" {
		if !s.expectAgentRunHandoffDocs("agent prep policy handoff docs metadata", policyPath, []agentHandoffDoc{
			{Path: "AGENTS.md", Source: "default"},
			{Path: "README.md", Source: "default"},
			{Path: "docs/adr/0001-default.md", Source: "default"},
			{Path: "docs/runbook.md", Source: "policy", Pattern: "docs/runbook.md"},
			{Path: "docs/notes/a.md", Source: "policy", Pattern: "docs/notes/**"},
			{Path: "docs/notes/deep/n.md", Source: "policy", Pattern: "docs/notes/**"},
			{Path: "docs/notes/z.md", Source: "policy", Pattern: "docs/notes/**"},
		}) {
			return
		}
		if !s.expectAgentRunDiagnostics("agent prep policy handoff missing diagnostics", policyPath, []string{"handoff-doc-missing"}, nil) {
			return
		}
	}

	dirty := s.command("agent prep dirty source warning", "agent", "prepare", "dirty-source", "--base", "main")
	if dirty.Status == "PASS" && !s.expectOutput(dirty, "warning: dirty-checkout") {
		return
	}
	if dirty.Status == "PASS" && valueAfterPrefix(dirty.Stdout, "ready_path: ") == "" {
		dirty.Status = "FAIL"
		dirty.Error = "dirty source prep did not print ready path"
		s.updateResult(dirty)
	} else if dirty.Status == "PASS" {
		dirtyPath := valueAfterPrefix(dirty.Stdout, "ready_path: ")
		if !s.expectGitCheckoutAtBase("agent prep dirty ready checkout base", dirtyPath, "main") {
			return
		}
	}
	if dirty.Status != "PASS" {
		return
	}

	dirtyFixture := s.fixture("dirty-source")
	if dirtyFixture == nil {
		s.h.record(result{Name: "agent prep source-only doc setup", Status: "FAIL", Error: "dirty-source fixture missing", ExitCode: -1})
		return
	}
	sourceOnlyRel := filepath.ToSlash(filepath.Join("docs", "adr", "9999-source-only.md"))
	sourceOnlyMarker := fakeSourceOnlyHandoffDocMarker()
	dirtySourceDoc := filepath.Join(dirtyFixture.Source, filepath.FromSlash(sourceOnlyRel))
	if err := os.MkdirAll(filepath.Dir(dirtySourceDoc), 0o755); err != nil {
		s.h.record(result{Name: "agent prep source-only doc setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.WriteFile(dirtySourceDoc, []byte("# Source-only ADR\n\n"+sourceOnlyMarker+"\n"), 0o644); err != nil {
		s.h.record(result{Name: "agent prep source-only doc setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if dirtyStatus, err := s.h.gitStatus(dirtyFixture.Source); err != nil {
		s.h.record(result{Name: "agent prep source-only doc dirty status", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	} else if !strings.Contains(dirtyStatus, sourceOnlyRel) && !strings.Contains(dirtyStatus, filepath.FromSlash("docs/")) {
		s.h.record(result{Name: "agent prep source-only doc dirty status", Status: "FAIL", Error: "source-only doc did not make source checkout dirty", ExitCode: -1})
		return
	}
	dirtySourceOnly := s.command("agent prep ignores source-only handoff doc", "agent", "prepare", "dirty-source", "--base", "main")
	if dirtySourceOnly.Status != "PASS" || !s.expectOutput(dirtySourceOnly, "warning: dirty-checkout", "blockers: none", "handoff_docs: 1") {
		return
	}
	dirtySourceOnlyPath := s.expectReadyPath("agent prep source-only ready path", dirtySourceOnly)
	if dirtySourceOnlyPath == "" {
		return
	}
	if !s.expectAgentRunHandoffDocs("agent prep source-only handoff docs from prepared clone", dirtySourceOnlyPath, []agentHandoffDoc{
		{Path: "README.md", Source: "default"},
	}) {
		return
	}
	if !s.expectAgentRunMetadataExcludes("agent prep source-only doc absent from metadata", dirtySourceOnlyPath, sourceOnlyRel, sourceOnlyMarker) {
		return
	}
	if !s.expectStateStoreExcludes("agent prep source-only doc absent from sqlite", sourceOnlyRel, sourceOnlyMarker) {
		return
	}

	envBlocked := s.expectedFailure("agent prep env blocker", "agent", "prepare", "required-env-missing")
	if envBlocked.Status != "FAIL" {
		envBlocked.Status = "FAIL"
		envBlocked.Error = "env-blocked prep unexpectedly passed"
	} else if !strings.Contains(envBlocked.Stderr, "blocker: missing-env-file") || !strings.Contains(envBlocked.Stderr, "blocker: missing-env-key") || strings.Contains(envBlocked.Stderr, "=") || containsAnySecret(envBlocked.Stderr, fakeEnvFixtureSecrets()) {
		envBlocked.Error = "env-blocked prep did not report missing env blockers without values"
	} else {
		envBlocked.Status = "PASS"
		envBlocked.Error = ""
	}
	s.record(envBlocked)

	envWarn := s.command("agent prep env warning", "agent", "prepare", "required-env-warn", "--base", "main")
	if envWarn.Status != "PASS" || !s.expectOutput(envWarn, "warning: missing-env-file", "warning: missing-env-key", "blockers: none", "ready_path: ") {
		return
	}
	warnPath := s.expectReadyPath("agent prep env warning ready path", envWarn)
	if warnPath == "" || !s.expectAgentRunMetadata("agent prep env warn state metadata", warnPath, "required-env-warn", "main", "") {
		return
	}

	envPresent := s.commandEnv("agent prep env present no secret leak", []string{"CODEMESH_E2E_PRESENT_ENV=" + fakeEnvFixtureKeySecret()}, "agent", "prepare", "required-env-present", "--base", "main")
	if envPresent.Status != "PASS" || !s.expectOutput(envPresent, "warnings: none", "blockers: none", "ready_path: ") {
		return
	}
	s.expectNoOutput(envPresent, fakeEnvFixtureFileSecret(), fakeEnvFixtureKeySecret())
	if readyPath := s.expectReadyPath("agent prep env present ready path", envPresent); readyPath != "" {
		metadataPath := filepath.Join(readyPath, "codemesh-run.json")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			s.h.record(result{Name: "agent prep env present metadata read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
			return
		}
		if containsAnySecret(string(data), fakeEnvFixtureSecrets()) {
			s.h.record(result{Name: "agent prep env present metadata secret safety", Status: "FAIL", Error: "fake env secret marker appeared in run metadata", ExitCode: -1})
			return
		}
	}
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

func (h *harness) caseOfflineGitFixtureSmoke() {
	fixtures, err := h.createOfflineGitFixtures()
	if err != nil {
		h.record(result{Name: "offline git fixture smoke", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	checks := []struct {
		name string
		fn   func() error
	}{
		{"clean repo", func() error {
			project := fixtures.Project("clean-repo")
			if project == nil {
				return errors.New("clean-repo fixture missing")
			}
			return h.expectGitStatus(project.Source, "")
		}},
		{"dirty source checkout", func() error {
			project := fixtures.Project("dirty-source")
			if project == nil {
				return errors.New("dirty-source fixture missing")
			}
			status, err := h.gitStatus(project.Source)
			if err != nil {
				return err
			}
			if status == "" {
				return errors.New("dirty-source fixture is clean")
			}
			return nil
		}},
		{"missing project path", func() error {
			project := fixtures.Project("missing-project-path")
			if project == nil {
				return errors.New("missing-project-path fixture missing")
			}
			if _, err := os.Stat(project.Source); err == nil {
				return fmt.Errorf("missing project path exists: %s", project.Source)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("stat missing project path: %w", err)
			}
			return nil
		}},
		{"missing base branch", func() error {
			project := fixtures.Project("missing-base-branch")
			if project == nil {
				return errors.New("missing-base-branch fixture missing")
			}
			stdout, _, err := h.exec(h.tmp, "git", "ls-remote", "--heads", project.Remote, project.BaseBranch)
			if err != nil {
				return err
			}
			if strings.TrimSpace(stdout) != "" {
				return fmt.Errorf("base branch %q exists unexpectedly", project.BaseBranch)
			}
			return nil
		}},
		{"required env missing", func() error {
			project := fixtures.Project("required-env-missing")
			if project == nil {
				return errors.New("required-env-missing fixture missing")
			}
			if len(project.RequiredEnv) != 1 || project.RequiredEnv[0] != "CODEMESH_E2E_REQUIRED_ENV" {
				return fmt.Errorf("unexpected required env keys: %v", project.RequiredEnv)
			}
			if envHasKey(isolatedEnv(h.codemeshHome, h.workspace, h.home), project.RequiredEnv[0]) {
				return fmt.Errorf("fake env key %s is present in isolated command env", project.RequiredEnv[0])
			}
			return nil
		}},
	}
	for _, check := range checks {
		if err := check.fn(); err != nil {
			h.record(result{Name: "offline git fixture " + check.name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
			return
		}
	}
	h.record(result{Name: "offline git fixture smoke", Status: "PASS", ExitCode: 0})
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

func (h *harness) writeReport() error {
	if err := os.MkdirAll(filepath.Dir(h.reportPath), 0o755); err != nil {
		return err
	}
	startedAt := h.startedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	results, redactedValues := sanitizeResults(h.results, h.redactionMarkers()...)
	r := report{
		StartedAt: startedAt.UTC().Format(time.RFC3339),
		Mode:      h.mode,
		Binary: reportBinary{
			Path:     h.bin,
			Kind:     h.binaryKind(),
			External: h.externalBin,
		},
		Isolation: reportIsolation{
			CodeMeshHome: h.codemeshHome,
			Home:         h.home,
			Workspace:    h.workspace,
			RunDir:       h.runDir,
			GitConfig:    filepath.Join(h.home, ".gitconfig"),
		},
		Summary:      summarizeResults(results),
		SecretSafety: reportSecretSafety{Enabled: true, RedactedValues: redactedValues},
		Results:      results,
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(h.reportPath, data, 0o644)
}

func (h *harness) binaryKind() string {
	if h.externalBin {
		return "external-packaged"
	}
	return "built-from-source"
}

func summarizeResults(results []result) reportSummary {
	var summary reportSummary
	for _, r := range results {
		switch r.Status {
		case "PASS":
			summary.Pass++
		case "FAIL":
			summary.Fail++
		case "SKIP":
			summary.Skip++
		}
		summary.Total++
	}
	return summary
}

func sanitizeResults(results []result, redactions ...string) ([]result, int) {
	sanitized := make([]result, len(results))
	redacted := 0
	for i, r := range results {
		r.Stdout, redacted = redactString(r.Stdout, redacted, redactions...)
		r.Stderr, redacted = redactString(r.Stderr, redacted, redactions...)
		r.Error, redacted = redactString(r.Error, redacted, redactions...)
		sanitized[i] = r
	}
	return sanitized, redacted
}

func redactString(value string, count int, redactions ...string) (string, int) {
	for _, marker := range redactions {
		if marker == "" {
			continue
		}
		if strings.Contains(value, marker) {
			count += strings.Count(value, marker)
			value = strings.ReplaceAll(value, marker, "[REDACTED]")
		}
	}
	return value, count
}

func (h *harness) redactionMarkers() []string {
	if len(h.redactions) != 0 {
		return h.redactions
	}
	markers := fakeEnvFixtureSecrets()
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		markers = append(markers, hostname)
	}
	return markers
}

func fakeEnvFixtureSecret() string {
	return fakeEnvFixtureKeySecret()
}

func fakeEnvFixtureSecrets() []string {
	return []string{fakeEnvFixtureFileSecret(), fakeEnvFixtureKeySecret()}
}

func fakeEnvFixtureFileSecret() string {
	return strings.Join([]string{"e2e", "fixture", "env", "file", "secret"}, "-")
}

func fakeEnvFixtureKeySecret() string {
	return strings.Join([]string{"e2e", "fixture", "env", "key", "secret"}, "-")
}

func fakeHandoffDocContentMarker() string {
	return strings.Join([]string{"e2e", "handoff", "doc", "content"}, "-")
}

func fakeSourceOnlyHandoffDocMarker() string {
	return strings.Join([]string{"e2e", "source", "only", "handoff", "doc"}, "-")
}

func (f offlineGitFixtures) Project(name string) *gitFixtureProject {
	for i := range f.Projects {
		if f.Projects[i].Name == name {
			return &f.Projects[i]
		}
	}
	return nil
}

func valueAfterPrefix(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func (h *harness) createOfflineGitFixtures() (offlineGitFixtures, error) {
	fixtures := offlineGitFixtures{
		Root:    filepath.Join(h.tmp, "git-fixtures"),
		Remotes: filepath.Join(h.tmp, "git-fixtures", "remotes"),
		Sources: filepath.Join(h.tmp, "git-fixtures", "sources"),
	}
	if err := os.MkdirAll(fixtures.Remotes, 0o755); err != nil {
		return fixtures, err
	}
	if err := os.MkdirAll(fixtures.Sources, 0o755); err != nil {
		return fixtures, err
	}

	writeDefaultHandoffDocs := func(path string) error {
		if err := os.WriteFile(filepath.Join(path, "AGENTS.md"), []byte("agent docs "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(path, "CONTEXT.md"), []byte("context docs "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		adrDir := filepath.Join(path, "docs", "adr")
		if err := os.MkdirAll(adrDir, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(adrDir, "0001-fixture.md"), []byte("adr docs "+fakeHandoffDocContentMarker()+"\n"), 0o644)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "clean-repo", writeDefaultHandoffDocs, nil); err != nil {
		return fixtures, err
	} else {
		fixtures.Projects = append(fixtures.Projects, project)
	}
	writePolicyHandoffDocs := func(path string) error {
		if err := os.WriteFile(filepath.Join(path, "AGENTS.md"), []byte("agent docs "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		adrDir := filepath.Join(path, "docs", "adr")
		if err := os.MkdirAll(adrDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(adrDir, "0001-default.md"), []byte("adr docs "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		notesDir := filepath.Join(path, "docs", "notes")
		if err := os.MkdirAll(notesDir, 0o755); err != nil {
			return err
		}
		deepNotesDir := filepath.Join(notesDir, "deep")
		if err := os.MkdirAll(deepNotesDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(path, "docs", "runbook.md"), []byte("runbook "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(notesDir, "z.md"), []byte("note z "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(notesDir, "a.md"), []byte("note a "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(deepNotesDir, "n.md"), []byte("note nested "+fakeHandoffDocContentMarker()+"\n"), 0o644); err != nil {
			return err
		}
		policy := []byte("agent:\n  include_docs:\n    - README.md\n    - .git/config\n    - docs/runbook.md\n    - docs/notes/**\n    - docs/missing/**\n    - docs/./runbook.md\n")
		return os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "policy-docs", writePolicyHandoffDocs, nil); err != nil {
		return fixtures, err
	} else {
		fixtures.Projects = append(fixtures.Projects, project)
	}
	if project, err := h.createClonedFixture(fixtures, "dirty-source", func(source string) error {
		return os.WriteFile(filepath.Join(source, "dirty.txt"), []byte("uncommitted fixture change\n"), 0o644)
	}); err != nil {
		return fixtures, err
	} else {
		fixtures.Projects = append(fixtures.Projects, project)
	}
	if project, err := h.createRemoteOnlyFixture(fixtures, "missing-project-path", nil); err != nil {
		return fixtures, err
	} else {
		fixtures.Projects = append(fixtures.Projects, project)
	}
	if project, err := h.createClonedFixture(fixtures, "missing-base-branch", nil); err != nil {
		return fixtures, err
	} else {
		project.BaseBranch = "release/missing"
		fixtures.Projects = append(fixtures.Projects, project)
	}
	writeEnvPolicy := func(path string) error {
		policy := []byte("agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n    required_keys:\n      - CODEMESH_E2E_REQUIRED_ENV\n")
		return os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "required-env-missing", writeEnvPolicy, nil); err != nil {
		return fixtures, err
	} else {
		project.RequiredEnv = []string{"CODEMESH_E2E_REQUIRED_ENV"}
		fixtures.Projects = append(fixtures.Projects, project)
	}
	writeWarnEnvPolicy := func(path string) error {
		policy := []byte("agent:\n  env:\n    mode: warn\n    required_files:\n      - .env.local\n    required_keys:\n      - CODEMESH_E2E_WARN_ENV\n")
		return os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "required-env-warn", writeWarnEnvPolicy, nil); err != nil {
		return fixtures, err
	} else {
		project.RequiredEnv = []string{"CODEMESH_E2E_WARN_ENV"}
		fixtures.Projects = append(fixtures.Projects, project)
	}
	writePresentEnvPolicy := func(path string) error {
		policy := []byte("agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n    required_keys:\n      - CODEMESH_E2E_PRESENT_ENV\n")
		if err := os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, ".gitignore"), []byte(".env.local\n"), 0o644)
	}
	writePresentEnvFile := func(path string) error {
		return os.WriteFile(filepath.Join(path, ".env.local"), []byte("TOKEN="+fakeEnvFixtureFileSecret()+"\n"), 0o600)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "required-env-present", writePresentEnvPolicy, writePresentEnvFile); err != nil {
		return fixtures, err
	} else {
		project.RequiredEnv = []string{"CODEMESH_E2E_PRESENT_ENV"}
		fixtures.Projects = append(fixtures.Projects, project)
	}
	return fixtures, nil
}

func (h *harness) createClonedFixture(fixtures offlineGitFixtures, name string, mutate func(string) error) (gitFixtureProject, error) {
	return h.createClonedFixtureWithSeed(fixtures, name, nil, mutate)
}

func (h *harness) createClonedFixtureWithSeed(fixtures offlineGitFixtures, name string, mutateSeed, mutateClone func(string) error) (gitFixtureProject, error) {
	project, err := h.createRemoteOnlyFixture(fixtures, name, mutateSeed)
	if err != nil {
		return project, err
	}
	if _, err := os.Stat(project.Source); errors.Is(err, os.ErrNotExist) {
		if _, _, err := h.exec(fixtures.Sources, "git", "clone", project.Remote, project.Source); err != nil {
			return project, err
		}
	} else if err != nil {
		return project, err
	}
	if mutateClone != nil {
		if err := mutateClone(project.Source); err != nil {
			return project, err
		}
	}
	return project, nil
}

func (h *harness) createRemoteOnlyFixture(fixtures offlineGitFixtures, name string, mutateSeed func(string) error) (gitFixtureProject, error) {
	remote := filepath.Join(fixtures.Remotes, name+".git")
	source := filepath.Join(fixtures.Sources, name)
	seed := filepath.Join(fixtures.Root, "seeds", name)
	project := gitFixtureProject{Name: name, Remote: remote, Source: source, BaseBranch: "main"}
	if _, err := os.Stat(remote); err == nil {
		return project, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return project, err
	}
	if err := os.MkdirAll(seed, 0o755); err != nil {
		return project, err
	}
	if _, _, err := h.exec(seed, "git", "init", "-b", "main"); err != nil {
		return project, err
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		return project, err
	}
	if mutateSeed != nil {
		if err := mutateSeed(seed); err != nil {
			return project, err
		}
	}
	if _, _, err := h.exec(seed, "git", "add", "."); err != nil {
		return project, err
	}
	if _, _, err := h.exec(seed, "git", "-c", "user.name=CodeMesh E2E", "-c", "user.email=e2e@example.invalid", "commit", "-m", "Initial fixture"); err != nil {
		return project, err
	}
	if _, _, err := h.exec(fixtures.Remotes, "git", "init", "--bare", "-b", "main", remote); err != nil {
		return project, err
	}
	if _, _, err := h.exec(seed, "git", "remote", "add", "origin", remote); err != nil {
		return project, err
	}
	if _, _, err := h.exec(seed, "git", "push", "-u", "origin", "main"); err != nil {
		return project, err
	}
	return project, nil
}

func (h *harness) cloneFixtureRemote(project gitFixtureProject, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, _, err := h.exec(filepath.Dir(target), "git", "clone", project.Remote, target); err != nil {
		return err
	}
	return nil
}

func (h *harness) expectGitStatus(dir, want string) error {
	got, err := h.gitStatus(dir)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("git status for %s = %q, want %q", dir, got, want)
	}
	return nil
}

func (h *harness) gitStatus(dir string) (string, error) {
	stdout, _, err := h.exec(dir, "git", "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

func (h *harness) print(r result) {
	var ignored int
	errorText, ignored := redactString(r.Error, ignored, h.redactionMarkers()...)
	stdout, ignored := redactString(r.Stdout, ignored, h.redactionMarkers()...)
	stderr, _ := redactString(r.Stderr, ignored, h.redactionMarkers()...)
	if r.Duration != "" {
		fmt.Fprintf(h.output, "%s %s (exit=%d duration=%s)\n", r.Status, r.Name, r.ExitCode, r.Duration)
	} else {
		fmt.Fprintf(h.output, "%s %s\n", r.Status, r.Name)
	}
	if r.Status != "FAIL" {
		return
	}
	if errorText != "" {
		fmt.Fprintf(h.output, "  error: %s\n", errorText)
	}
	if stdout != "" {
		fmt.Fprintf(h.output, "  stdout:\n%s\n", indent(stdout))
	}
	if stderr != "" {
		fmt.Fprintf(h.output, "  stderr:\n%s\n", indent(stderr))
	}
}

func (h *harness) record(r result) {
	h.print(r)
	h.results = append(h.results, r)
}

func (h *harness) newScenario(name string) (*scenario, error) {
	codemeshHome := filepath.Join(h.tmp, "codemesh-"+slug(name)+"-home")
	if err := os.MkdirAll(codemeshHome, 0o755); err != nil {
		return nil, err
	}
	fixtures, err := h.createOfflineGitFixtures()
	if err != nil {
		return nil, err
	}
	return &scenario{
		h:            h,
		name:         name,
		codemeshHome: codemeshHome,
		fixtures:     fixtures,
	}, nil
}

func (s *scenario) fixture(name string) *gitFixtureProject {
	return s.fixtures.Project(name)
}

func (s *scenario) command(label string, args ...string) result {
	r := s.execute(label, nil, args...)
	s.h.record(r)
	return r
}

func (s *scenario) commandEnv(label string, env []string, args ...string) result {
	r := s.execute(label, env, args...)
	s.h.record(r)
	return r
}

func (s *scenario) expectedFailure(label string, args ...string) result {
	return s.execute(label, nil, args...)
}

func (s *scenario) record(r result) {
	s.h.record(r)
}

func (s *scenario) execute(label string, env []string, args ...string) result {
	return s.h.executeCommand(commandSpec{
		Label:   label,
		Name:    s.h.bin,
		Args:    args,
		Timeout: defaultCommandTimeout,
		Env:     append([]string{"CODEMESH_HOME=" + s.codemeshHome}, env...),
	})
}

func (s *scenario) expectOutput(r result, fragments ...string) bool {
	if r.Status != "PASS" {
		return false
	}
	for _, fragment := range fragments {
		if !strings.Contains(r.Stdout, fragment) {
			s.failCommandAssertion(r, fmt.Sprintf("stdout did not include %q", fragment))
			return false
		}
	}
	return true
}

func (s *scenario) expectNoOutput(r result, fragments ...string) bool {
	if r.Status != "PASS" {
		return false
	}
	for _, fragment := range fragments {
		if strings.Contains(r.Stdout, fragment) || strings.Contains(r.Stderr, fragment) {
			s.failCommandAssertion(r, fmt.Sprintf("command output included %q", fragment))
			return false
		}
	}
	return true
}

func (s *scenario) expectFailure(r result, exitCode int, stderrFragments ...string) bool {
	if r.Status != "FAIL" {
		r.Status = "FAIL"
		r.Error = fmt.Sprintf("command exited %d, want failure exit %d", r.ExitCode, exitCode)
		s.record(r)
		return false
	}
	if r.ExitCode != exitCode {
		r.Error = fmt.Sprintf("exit code = %d, want %d", r.ExitCode, exitCode)
		s.record(r)
		return false
	}
	for _, fragment := range stderrFragments {
		if !strings.Contains(r.Stderr, fragment) {
			r.Error = fmt.Sprintf("stderr did not include %q", fragment)
			s.record(r)
			return false
		}
	}
	r.Status = "PASS"
	r.Error = ""
	s.record(r)
	return true
}

func (s *scenario) expectNoStateStore(name string) bool {
	dbPath := filepath.Join(s.codemeshHome, "codemesh.db")
	if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("state store exists or stat failed: %v", err), ExitCode: -1})
		return false
	}
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func (s *scenario) expectTreeStatusAgreement(tree, status result, alias string) bool {
	if tree.Status != "PASS" || status.Status != "PASS" {
		return false
	}
	treeState, ok := treeStateForAlias(tree.Stdout, alias)
	if !ok {
		s.failCommandAssertion(tree, fmt.Sprintf("tree output did not include project %q", alias))
		return false
	}
	statusState, ok := projectStatusState(status.Stdout)
	if !ok {
		s.failCommandAssertion(status, "status output did not include state")
		return false
	}
	if treeState != statusState {
		s.failCommandAssertion(status, fmt.Sprintf("tree/status state mismatch for %s: tree=%s status=%s", alias, treeState, statusState))
		return false
	}
	return true
}

func (s *scenario) expectPathExists(name, path string) bool {
	if _, err := os.Stat(path); err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectPathMissing(name, path string) bool {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("path exists or stat failed: %v", err), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectReadyPath(name string, r result) string {
	readyPath := valueAfterPrefix(r.Stdout, "ready_path: ")
	if readyPath == "" {
		s.h.record(result{Name: name, Status: "FAIL", Error: "agent prepare output did not include ready_path", ExitCode: -1})
	}
	return readyPath
}

func (s *scenario) expectGitCheckoutAtBase(name, path, base string) bool {
	inside, _, err := s.h.exec(path, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("ready path is not a Git checkout: %v", err), ExitCode: -1})
		return false
	}
	branch, _, err := s.h.exec(path, "git", "branch", "--show-current")
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if strings.TrimSpace(branch) != base {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("ready checkout branch = %q, want %q", strings.TrimSpace(branch), base), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectGitOrigin(name, path, remote string) bool {
	origin, _, err := s.h.exec(path, "git", "remote", "get-url", "origin")
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if strings.TrimSpace(origin) != remote {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("origin = %q, want %q", strings.TrimSpace(origin), remote), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunMetadata(name, readyPath, projectAlias, base, profile string) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if fileMetadata.ReadyPath != readyPath || fileMetadata.Project.Alias != projectAlias || fileMetadata.Base != base || fileMetadata.Profile != profile {
		s.h.record(result{Name: name, Status: "FAIL", Error: "codemesh-run.json metadata does not match prepared workspace", ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(s.codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if dbMetadata.ReadyPath != readyPath || dbMetadata.Project.Alias != projectAlias || dbMetadata.Base != base || dbMetadata.Profile != profile {
		s.h.record(result{Name: name, Status: "FAIL", Error: "state-store agent run metadata does not reference prepared workspace", ExitCode: -1})
		return false
	}
	if containsAnySecret(fileMetadata.Raw, fakeEnvFixtureSecrets()) || containsAnySecret(dbMetadata.Raw, fakeEnvFixtureSecrets()) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "fake env secret marker appeared in agent run metadata", ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunHandoffDocs(name, readyPath string, want []agentHandoffDoc) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(s.codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if !agentHandoffDocsEqual(fileMetadata.HandoffDocs, want) || !agentHandoffDocsEqual(dbMetadata.HandoffDocs, want) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("handoff docs file=%#v db=%#v want=%#v", fileMetadata.HandoffDocs, dbMetadata.HandoffDocs, want), ExitCode: -1})
		return false
	}
	if strings.Contains(fileMetadata.Raw, fakeHandoffDocContentMarker()) || strings.Contains(dbMetadata.Raw, fakeHandoffDocContentMarker()) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "handoff doc contents appeared in agent run metadata", ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunDiagnostics(name, readyPath string, warningCodes, blockerCodes []string) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(s.codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if !agentDiagnosticsContain(fileMetadata.Diagnostics.Warnings, warningCodes) || !agentDiagnosticsContain(dbMetadata.Diagnostics.Warnings, warningCodes) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("diagnostic warnings file=%#v db=%#v want=%#v", fileMetadata.Diagnostics.Warnings, dbMetadata.Diagnostics.Warnings, warningCodes), ExitCode: -1})
		return false
	}
	if !agentDiagnosticsContain(fileMetadata.Diagnostics.Blockers, blockerCodes) || !agentDiagnosticsContain(dbMetadata.Diagnostics.Blockers, blockerCodes) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("diagnostic blockers file=%#v db=%#v want=%#v", fileMetadata.Diagnostics.Blockers, dbMetadata.Diagnostics.Blockers, blockerCodes), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunMetadataExcludes(name, readyPath string, fragments ...string) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(s.codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	for _, fragment := range fragments {
		if strings.Contains(fileMetadata.Raw, fragment) || strings.Contains(dbMetadata.Raw, fragment) {
			s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("agent run metadata included %q", fragment), ExitCode: -1})
			return false
		}
	}
	return true
}

func (s *scenario) expectStateStoreExcludes(name string, fragments ...string) bool {
	data, err := os.ReadFile(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	for _, fragment := range fragments {
		if strings.Contains(string(data), fragment) {
			s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("state store included %q", fragment), ExitCode: -1})
			return false
		}
	}
	return true
}

func (s *scenario) expectProjectRowCount(name string, want int) bool {
	projects, err := readProjectRowsFromStore(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(projects) != want {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project row count = %d, want %d", len(projects), want), ExitCode: -1})
		return false
	}
	return s.expectProjectRowsAreIsolated(name+" isolation", projects)
}

func (s *scenario) expectProjectRows(name string, want ...projectRow) bool {
	got, err := readProjectRowsFromStore(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(got) != len(want) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project rows = %#v, want %#v", got, want), ExitCode: -1})
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project row %d = %#v, want %#v", i, got[i], want[i]), ExitCode: -1})
			return false
		}
	}
	return s.expectProjectRowsAreIsolated(name+" isolation", got)
}

func (s *scenario) expectProjectRowsAreIsolated(name string, rows []projectRow) bool {
	for _, row := range rows {
		if strings.Contains(row.NormalizedRemote, "github.com") || strings.Contains(row.CloneURL, "github.com") {
			s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project row reached GitHub: %#v", row), ExitCode: -1})
			return false
		}
		for label, path := range map[string]string{
			"normalized_remote": row.NormalizedRemote,
			"clone_url":         row.CloneURL,
			"local_path":        row.LocalPath,
		} {
			inside, err := pathInside(s.h.tmp, path)
			if err != nil {
				s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("check %s %q: %v", label, path, err), ExitCode: -1})
				return false
			}
			if !inside {
				s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("%s outside harness temp dir: %s", label, path), ExitCode: -1})
				return false
			}
		}
	}
	return true
}

func (s *scenario) expectProjectSchemaNoPresenceColumns(name string) bool {
	columns, err := projectColumns(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	want := []string{"id", "alias", "normalized_remote", "local_path", "created_at", "updated_at", "clone_url"}
	if !stringSlicesEqual(columns, want) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("projects columns = %v, want metadata-only schema %v", columns, want), ExitCode: -1})
		return false
	}
	return true
}

type agentMetadata struct {
	Raw       string
	RunID     string `json:"run_id"`
	ReadyPath string `json:"ready_path"`
	Project   struct {
		Alias string `json:"alias"`
	} `json:"project"`
	Base        string            `json:"base"`
	Profile     string            `json:"profile"`
	HandoffDocs []agentHandoffDoc `json:"handoff_docs"`
	Diagnostics agentDiagnostics  `json:"diagnostics"`
}

type agentHandoffDoc struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Pattern string `json:"pattern,omitempty"`
}

type agentDiagnostics struct {
	Warnings []agentDiagnostic `json:"warnings"`
	Blockers []agentDiagnostic `json:"blockers"`
}

type agentDiagnostic struct {
	Code string `json:"code"`
}

type projectRow struct {
	Alias            string
	NormalizedRemote string
	CloneURL         string
	LocalPath        string
}

type machineRow struct {
	ID            string
	Hostname      string
	OS            string
	Architecture  string
	WorkspaceRoot string
}

func readAgentMetadata(path string) (agentMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agentMetadata{}, err
	}
	var metadata agentMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return agentMetadata{}, err
	}
	metadata.Raw = string(data)
	return metadata, nil
}

func readAgentRunMetadataFromStore(dbPath, runID string) (agentMetadata, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return agentMetadata{}, err
	}
	defer db.Close()
	var metadataJSON string
	if err := db.QueryRow(`select metadata_json from agent_runs where id = ?`, runID).Scan(&metadataJSON); err != nil {
		return agentMetadata{}, err
	}
	var metadata agentMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return agentMetadata{}, err
	}
	metadata.Raw = metadataJSON
	return metadata, nil
}

func agentHandoffDocsEqual(got, want []agentHandoffDoc) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func agentDiagnosticsContain(diagnostics []agentDiagnostic, codes []string) bool {
	if len(codes) == 0 {
		return len(diagnostics) == 0
	}
	for _, code := range codes {
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Code == code {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func readProjectRowsFromStore(dbPath string) ([]projectRow, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`select alias, normalized_remote, clone_url, local_path from projects order by alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []projectRow
	for rows.Next() {
		var project projectRow
		if err := rows.Scan(&project.Alias, &project.NormalizedRemote, &project.CloneURL, &project.LocalPath); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func readMachineRowsFromStore(dbPath string) ([]machineRow, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`select machine_id, hostname, os, architecture, workspace_root from machines where machine_id is not null and machine_id != '' order by id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var machines []machineRow
	for rows.Next() {
		var machine machineRow
		if err := rows.Scan(&machine.ID, &machine.Hostname, &machine.OS, &machine.Architecture, &machine.WorkspaceRoot); err != nil {
			return nil, err
		}
		machines = append(machines, machine)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return machines, nil
}

func projectColumns(dbPath string) ([]string, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`pragma table_info(projects)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func stringSlicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func (s *scenario) failCommandAssertion(r result, message string) {
	r.Name += " assertion"
	r.Status = "FAIL"
	r.Error = message
	r.ExitCode = -1
	s.h.record(r)
}

func (s *scenario) failScenarioAssertion(name, message string) {
	s.h.record(result{Name: name + " assertion", Status: "FAIL", Error: message, ExitCode: -1})
}

func (s *scenario) updateResult(r result) {
	for i := len(s.h.results) - 1; i >= 0; i-- {
		if s.h.results[i].Name == r.Name {
			previous := s.h.results[i]
			s.h.results[i] = r
			if previous.Status != r.Status || previous.Error != r.Error {
				s.h.print(r)
			}
			break
		}
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

func treeStateForAlias(output, alias string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "-" && fields[1] == alias {
			return fields[2], true
		}
	}
	return "", false
}

func projectStatusState(output string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "state: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "state: ")), true
		}
	}
	return "", false
}

func runGitNoOutput(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return nil
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
	return modeSource
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
	parentPath, err := canonicalPathForInside(parent)
	if err != nil {
		return false, err
	}
	childPath, err := canonicalPathForInside(child)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(parentPath, childPath)
	if err != nil {
		return false, err
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."), nil
}

func canonicalPathForInside(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return realPath, nil
	}
	var suffix []string
	for current := abs; ; current = filepath.Dir(current) {
		parent := filepath.Dir(current)
		if parent == current {
			return abs, nil
		}
		suffix = append([]string{filepath.Base(current)}, suffix...)
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(append([]string{realParent}, suffix...)...), nil
		}
	}
}

func reportPath(root string) string {
	if path := os.Getenv("CODEMESH_E2E_REPORT"); path != "" {
		return path
	}
	return filepath.Join(root, "tmp", "e2e-report.json")
}

func slug(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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

func envHasKey(env []string, key string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, key+"=") {
			return true
		}
	}
	return false
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}
