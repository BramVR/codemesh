package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/envbinding"
	_ "modernc.org/sqlite"
)

const (
	defaultCommandTimeout   = 30 * time.Second
	longCommandTimeout      = 2 * time.Minute
	defaultLiveLockStale    = 4 * time.Hour
	modeSource              = "source"
	modePackaged            = "packaged"
	modeLive                = "live"
	liveTargetGitHub        = "github remote smoke"
	liveTargetProvider      = "provider smoke"
	liveTargetToolchain     = "toolchain host smoke"
	liveTargetDesktop       = "desktop peekaboo smoke"
	liveTargetOwnedHost     = "owned-host smoke"
	defaultLiveGitHubRemote = "https://github.com/BramVR/codemesh.git"
)

var errLiveLockHeld = errors.New("live e2e lock already held")

var (
	contractCommitRE       = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	contractRFC3339RE      = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?Z\b`)
	contractDurationRE     = regexp.MustCompile(`\b[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h)\b`)
	contractAgentRunPathRE = regexp.MustCompile(`(<CODEMESH_HOME>/agents/)[^/]+`)
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
	Host         reportHost         `json:"host"`
	Isolation    reportIsolation    `json:"isolation"`
	Live         *reportLive        `json:"live,omitempty"`
	TwoMachine   *reportTwoMachine  `json:"two_machine,omitempty"`
	Summary      reportSummary      `json:"summary"`
	SecretSafety reportSecretSafety `json:"secret_safety"`
	Results      []result           `json:"results"`
}

type reportBinary struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	External bool   `json:"external"`
}

type reportHost struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
}

type reportIsolation struct {
	CodeMeshHome string `json:"codemesh_home"`
	Home         string `json:"home"`
	Workspace    string `json:"workspace"`
	RunDir       string `json:"run_dir"`
	GitConfig    string `json:"git_config"`
}

type reportLive struct {
	OptIn       bool                  `json:"opt_in"`
	Strict      bool                  `json:"strict"`
	Targets     []string              `json:"targets"`
	SkipReasons []string              `json:"skip_reasons,omitempty"`
	LockPath    string                `json:"lock_path,omitempty"`
	LockLabel   string                `json:"lock_label,omitempty"`
	GitHub      *reportLiveGitHub     `json:"github,omitempty"`
	Provider    *reportLiveProvider   `json:"provider,omitempty"`
	Toolchain   *reportLiveToolchain  `json:"toolchain,omitempty"`
	Desktop     *reportLiveDesktop    `json:"desktop,omitempty"`
	OwnedHosts  *reportLiveOwnedHosts `json:"owned_hosts,omitempty"`
}

type reportTwoMachine struct {
	MachineAID          string                    `json:"machine_a_id"`
	MachineBID          string                    `json:"machine_b_id"`
	ManifestLocation    string                    `json:"manifest_location"`
	HydratedProjectID   string                    `json:"hydrated_project_id"`
	HydrationProvenance reportHydrationProvenance `json:"hydration_provenance"`
	DriftSummary        string                    `json:"drift_summary"`
	CleanupStatus       string                    `json:"cleanup_status"`
}

type reportHydrationProvenance struct {
	Remote      string `json:"remote"`
	Base        string `json:"base"`
	DesiredPath string `json:"desired_path"`
	MachineID   string `json:"machine_id"`
}

type reportLiveGitHub struct {
	RemoteURL        string                    `json:"remote_url"`
	DefaultBranch    string                    `json:"default_branch,omitempty"`
	CommandDurations map[string]string         `json:"command_durations,omitempty"`
	CloneStrategies  []reportLiveCloneStrategy `json:"clone_strategies,omitempty"`
	SecretSafety     string                    `json:"secret_safety,omitempty"`
}

type reportLiveCloneStrategy struct {
	Label      string                           `json:"label"`
	Command    string                           `json:"command"`
	Status     string                           `json:"status"`
	Strategy   reportLiveCloneStrategySelection `json:"strategy"`
	SkipReason string                           `json:"skip_reason,omitempty"`
}

type reportLiveCloneStrategySelection struct {
	Name        string   `json:"name"`
	History     string   `json:"history"`
	WorkingTree string   `json:"working_tree"`
	Filter      string   `json:"filter,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
}

type reportLiveProvider struct {
	Status              string `json:"status"`
	Provider            string `json:"provider,omitempty"`
	Requirement         string `json:"requirement,omitempty"`
	Scope               string `json:"scope,omitempty"`
	SecretRefConfigured bool   `json:"secret_ref_configured,omitempty"`
	SkipReason          string `json:"skip_reason,omitempty"`
}

type reportLiveToolchain struct {
	Status       string                       `json:"status"`
	Fixtures     []reportLiveToolchainFixture `json:"fixtures,omitempty"`
	SkipReasons  []string                     `json:"skip_reasons,omitempty"`
	SecretSafety string                       `json:"secret_safety,omitempty"`
}

type reportLiveToolchainFixture struct {
	Name          string                `json:"name"`
	Kind          string                `json:"kind"`
	Status        string                `json:"status"`
	Project       toolchainProjectFacts `json:"project"`
	Host          toolchainHostFacts    `json:"host"`
	DoctorStatus  string                `json:"doctor_status,omitempty"`
	PrepareStatus string                `json:"agent_prepare_status,omitempty"`
	SkipReason    string                `json:"skip_reason,omitempty"`
}

type reportLiveDesktop struct {
	Status         string                     `json:"status"`
	PeekabooPath   string                     `json:"peekaboo_path,omitempty"`
	TerminalApp    string                     `json:"terminal_app,omitempty"`
	ScreenshotPath string                     `json:"screenshot_path,omitempty"`
	TranscriptPath string                     `json:"transcript_path,omitempty"`
	SkipReason     string                     `json:"skip_reason,omitempty"`
	SecretSafety   string                     `json:"secret_safety,omitempty"`
	Permissions    *reportPeekabooPermissions `json:"permissions,omitempty"`
}

type reportLiveOwnedHosts struct {
	Status       string                `json:"status"`
	Inventory    []reportLiveOwnedHost `json:"inventory,omitempty"`
	SkipReason   string                `json:"skip_reason,omitempty"`
	BundlePath   string                `json:"bundle_path,omitempty"`
	SecretSafety string                `json:"secret_safety,omitempty"`
}

type reportLiveOwnedHost struct {
	Name                   string                      `json:"name"`
	Kind                   string                      `json:"kind"`
	TargetOS               string                      `json:"target_os"`
	Address                string                      `json:"address,omitempty"`
	Status                 string                      `json:"status"`
	SkipReason             string                      `json:"skip_reason,omitempty"`
	Facts                  reportOwnedHostFacts        `json:"facts,omitempty"`
	Doctor                 []reportOwnedHostDoctor     `json:"doctor,omitempty"`
	Lock                   *reportOwnedHostLock        `json:"lock,omitempty"`
	CommandDurations       map[string]string           `json:"command_durations,omitempty"`
	Artifacts              []reportOwnedHostArtifact   `json:"artifacts,omitempty"`
	CodeMeshE2EReportPaths []string                    `json:"codemesh_e2e_report_paths,omitempty"`
	SelectedRunIDs         []string                    `json:"selected_run_ids,omitempty"`
	MachineIDs             []string                    `json:"machine_ids,omitempty"`
	ManifestLocation       string                      `json:"manifest_location,omitempty"`
	HydratedProjectID      string                      `json:"hydrated_project_identity,omitempty"`
	CleanupStatus          string                      `json:"cleanup_status,omitempty"`
	Visual                 *reportOwnedHostVisualProof `json:"visual,omitempty"`
	SecretSafety           string                      `json:"secret_safety,omitempty"`
}

type reportOwnedHostFacts struct {
	OS        string `json:"os,omitempty"`
	Arch      string `json:"arch,omitempty"`
	GoVersion string `json:"go_version,omitempty"`
	Shell     string `json:"shell,omitempty"`
}

type reportOwnedHostDoctor struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Duration string `json:"duration,omitempty"`
}

type reportOwnedHostLock struct {
	Path      string `json:"path"`
	Label     string `json:"label"`
	StartedAt string `json:"started_at"`
}

type reportOwnedHostArtifact struct {
	Command    string `json:"command"`
	StdoutPath string `json:"stdout_path,omitempty"`
	StderrPath string `json:"stderr_path,omitempty"`
}

type reportOwnedHostVisualProof struct {
	Status           string `json:"status"`
	SkipReason       string `json:"skip_reason,omitempty"`
	ScreenshotPath   string `json:"screenshot_path,omitempty"`
	VideoPath        string `json:"video_path,omitempty"`
	ContactSheetPath string `json:"contact_sheet_path,omitempty"`
}

type reportPeekabooPermissions struct {
	Source          string `json:"source,omitempty"`
	ScreenRecording bool   `json:"screen_recording"`
	Accessibility   bool   `json:"accessibility"`
}

type toolchainProjectFacts struct {
	Requirement string `json:"requirement"`
}

type toolchainHostFacts struct {
	Command string `json:"command,omitempty"`
	Version string `json:"version,omitempty"`
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
	live         *reportLive
	twoMachine   *reportTwoMachine
}

type liveConfig struct {
	OptIn   bool
	Strict  bool
	Targets []string
}

type liveProviderSmokeConfig struct {
	Provider    string
	Requirement string
	SecretRef   string
	Scope       string
}

type ownedHostTarget struct {
	Name     string
	Kind     string
	TargetOS string
	Address  string
}

type peekabooDesktopArtifacts struct {
	script     string
	screenshot string
	transcript string
}

type liveLockMetadata struct {
	PID       int    `json:"pid"`
	Host      string `json:"host"`
	Label     string `json:"label"`
	StartedAt string `json:"started_at"`
	Token     string `json:"token,omitempty"`
}

type liveLock struct {
	path  string
	token string
}

type liveCleanupGuard struct {
	path string
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

type twoMachineNode struct {
	Name          string
	CodeMeshHome  string
	Home          string
	WorkspaceRoot string
}

func main() {
	mode := e2eMode()
	root, err := repoRootForMode(mode)
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
		mode:         mode,
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
	if err := h.setupIsolation(); err != nil {
		return h.fail("harness setup", err)
	}
	if h.mode == modeLive {
		return h.runLive()
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
	h.caseDoctorPreflightWorkflow()
	h.caseBootstrapTopologyWorkflow()
	h.caseHydrationFixtureWorkflow()
	h.caseTwoMachineManifestBootstrapReconcileSmoke()
	h.caseAgentPrepFixtureWorkflow()
	h.caseCLIContractSnapshotWorkflow()
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

func (h *harness) setupIsolation() error {
	if err := os.MkdirAll(filepath.Dir(h.bin), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(h.codemeshHome, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(h.home, ".gitconfig"), nil, 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(h.workspace, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(h.runDir, 0o755); err != nil {
		return err
	}
	if h.mode == modePackaged {
		inside, err := pathInside(h.root, h.runDir)
		if err != nil {
			return err
		}
		if inside {
			return fmt.Errorf("packaged run dir must be outside repo: %s", h.runDir)
		}
	}
	return nil
}

func (h *harness) runLive() int {
	cfg := liveConfigFromEnv(os.LookupEnv)
	h.live = &reportLive{
		OptIn:   cfg.OptIn,
		Strict:  cfg.Strict,
		Targets: append([]string(nil), cfg.Targets...),
	}
	if !cfg.OptIn {
		h.live.SkipReasons = append(h.live.SkipReasons, "CODEMESH_E2E_LIVE not set")
		h.skip("live e2e opt-in", "CODEMESH_E2E_LIVE=1 required for live checks")
		return h.finishLive()
	}

	lock, err := acquireLiveLock(defaultLiveLockDir(), "codemesh e2e live", time.Now().UTC(), os.Getpid(), defaultLiveLockStale)
	if err != nil {
		if errors.Is(err, errLiveLockHeld) && !cfg.Strict {
			h.live.SkipReasons = append(h.live.SkipReasons, err.Error())
			h.skip("live e2e lock", err.Error())
			return h.finishLive()
		}
		h.record(result{Name: "live e2e lock", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return h.finishLive()
	}
	h.live.LockPath = lock.path
	h.live.LockLabel = "codemesh e2e live"

	if !h.prepareBinary() {
		if err := lock.release(); err != nil {
			h.record(result{Name: "live e2e lock release", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		}
		return h.finishLive()
	}
	if liveTargetEnabled(cfg, liveTargetGitHub) {
		h.caseLiveGitHubRemoteSmoke(cfg)
	}
	if liveTargetEnabled(cfg, liveTargetProvider) {
		h.caseLiveProviderSmoke(cfg)
	}
	if liveTargetEnabled(cfg, liveTargetToolchain) {
		h.caseLiveToolchainHostSmoke(cfg)
	}
	if liveTargetEnabled(cfg, liveTargetDesktop) {
		h.caseLivePeekabooDesktopSmoke(cfg)
	}
	if liveTargetEnabled(cfg, liveTargetOwnedHost) {
		h.caseLiveOwnedHostSmoke(cfg)
	}
	if h.live.GitHub != nil && h.live.GitHub.SecretSafety == "" {
		h.live.GitHub.SecretSafety = "pending"
	}
	if err := lock.release(); err != nil {
		h.record(result{Name: "live e2e lock release", Status: "FAIL", Error: err.Error(), ExitCode: -1})
	}
	return h.finishLive()
}

func (h *harness) caseLiveOwnedHostSmoke(cfg liveConfig) {
	if h.live == nil {
		h.live = &reportLive{}
	}
	h.live.OwnedHosts = &reportLiveOwnedHosts{Status: "running", SecretSafety: "pending"}
	inventory, err := ownedHostInventoryFromEnv(os.LookupEnv)
	if err != nil {
		h.recordLiveOwnedHostSkipOrFail(cfg, "owned-host inventory config", err.Error())
		return
	}
	if len(inventory) == 0 {
		h.recordLiveOwnedHostSkipOrFail(cfg, "owned-host inventory config", "CODEMESH_E2E_OWNED_HOSTS must name owned hosts or point to non-secret inventory config")
		return
	}
	bundlePath, err := h.prepareOwnedHostEvidenceBundle()
	if err != nil {
		h.record(result{Name: "owned-host evidence bundle setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.OwnedHosts.Status = "failed"
		h.live.OwnedHosts.SecretSafety = "not_run"
		return
	}
	h.live.OwnedHosts.BundlePath = h.repoArtifactPath(bundlePath)
	passCount := 0
	failCount := 0
	for _, host := range inventory {
		h.live.OwnedHosts.Inventory = append(h.live.OwnedHosts.Inventory, reportLiveOwnedHost{
			Name:             host.Name,
			Kind:             host.Kind,
			TargetOS:         host.TargetOS,
			Address:          host.Address,
			Status:           "running",
			CommandDurations: map[string]string{},
			Visual: &reportOwnedHostVisualProof{
				Status:     "skipped",
				SkipReason: "visual proof requires a configured free local desktop capture path",
			},
			SecretSafety: "pending",
		})
		index := len(h.live.OwnedHosts.Inventory) - 1
		hostReport := &h.live.OwnedHosts.Inventory[index]
		ok := h.runOwnedHostTarget(cfg, host, bundlePath, hostReport)
		switch hostReport.Status {
		case "pass":
			passCount++
		case "failed":
			failCount++
		}
		if !ok && cfg.Strict {
			failCount++
		}
	}
	if err := h.scanOwnedHostEvidenceBundle(bundlePath); err != nil {
		h.record(result{Name: "owned-host secret safety evidence bundle", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.OwnedHosts.Status = "failed"
		h.live.OwnedHosts.SecretSafety = "failed"
		return
	}
	h.record(result{Name: "owned-host secret safety evidence bundle", Status: "PASS", ExitCode: 0})
	h.live.OwnedHosts.SecretSafety = "pass"
	switch {
	case failCount > 0:
		h.live.OwnedHosts.Status = "failed"
	case passCount > 0:
		h.live.OwnedHosts.Status = "pass"
	default:
		h.live.OwnedHosts.Status = "skipped"
	}
}

func (h *harness) recordLiveOwnedHostSkipOrFail(cfg liveConfig, name, reason string) {
	status := "SKIP"
	reportStatus := "skipped"
	if cfg.Strict {
		status = "FAIL"
		reportStatus = "failed"
	}
	if h.live == nil {
		h.live = &reportLive{}
	}
	if h.live.OwnedHosts == nil {
		h.live.OwnedHosts = &reportLiveOwnedHosts{}
	}
	h.live.OwnedHosts.Status = reportStatus
	h.live.OwnedHosts.SkipReason = reason
	if h.live.OwnedHosts.SecretSafety == "" || h.live.OwnedHosts.SecretSafety == "pending" {
		h.live.OwnedHosts.SecretSafety = "not_run"
	}
	h.live.SkipReasons = append(h.live.SkipReasons, reason)
	h.record(result{Name: name, Status: status, Error: reason, Duration: formatDuration(0), ExitCode: -1})
}

func (h *harness) prepareOwnedHostEvidenceBundle() (string, error) {
	dir := filepath.Join(h.root, "tmp", "e2e-owned-host")
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (h *harness) runOwnedHostTarget(cfg liveConfig, target ownedHostTarget, bundlePath string, hostReport *reportLiveOwnedHost) bool {
	lock, err := acquireLiveLockForHost(defaultLiveLockDir(), target.Name, "codemesh owned-host "+target.Name, time.Now().UTC(), os.Getpid(), defaultLiveLockStale)
	if err != nil {
		h.recordOwnedHostSkipOrFail(cfg, hostReport, "owned-host "+target.Name+" lock", err.Error())
		return !cfg.Strict
	}
	hostReport.Lock = &reportOwnedHostLock{
		Path:      lock.path,
		Label:     "codemesh owned-host " + target.Name,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	defer func() {
		if err := lock.release(); err != nil {
			h.record(result{Name: "owned-host " + target.Name + " lock release", Status: "FAIL", Error: err.Error(), ExitCode: -1})
			hostReport.Status = "failed"
		}
	}()
	switch target.Kind {
	case "local":
		return h.runOwnedHostLocalTarget(cfg, target, bundlePath, hostReport)
	case "ssh":
		return h.runOwnedHostSSHDoctor(cfg, target, bundlePath, hostReport)
	default:
		h.recordOwnedHostSkipOrFail(cfg, hostReport, "owned-host "+target.Name+" kind", "unsupported owned-host kind "+target.Kind)
		return !cfg.Strict
	}
}

func (h *harness) runOwnedHostLocalTarget(cfg liveConfig, target ownedHostTarget, bundlePath string, hostReport *reportLiveOwnedHost) bool {
	hostReport.Facts = reportOwnedHostFacts{OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), Shell: "sh"}
	if runtime.GOOS != target.TargetOS {
		h.recordOwnedHostSkipOrFail(cfg, hostReport, "owned-host "+target.Name+" OS prerequisite", "local host is "+runtime.GOOS+", target requires "+target.TargetOS)
		return !cfg.Strict
	}
	if !h.ownedHostDoctorLocal(cfg, target, hostReport) {
		return !cfg.Strict
	}
	if !h.runOwnedHostLocalWorkspaceFlow(target, bundlePath, hostReport) {
		hostReport.Status = "failed"
		hostReport.SecretSafety = "failed"
		return false
	}
	hostReport.Status = "pass"
	hostReport.SecretSafety = "pass"
	return true
}

func (h *harness) ownedHostDoctorLocal(cfg liveConfig, target ownedHostTarget, hostReport *reportLiveOwnedHost) bool {
	checks := []struct {
		name string
		fn   func() error
	}{
		{"os-arch", func() error { return nil }},
		{"shell", func() error {
			_, err := exec.LookPath("sh")
			return err
		}},
		{"git", func() error {
			_, err := exec.LookPath("git")
			return err
		}},
		{"go", func() error {
			_, err := exec.LookPath("go")
			return err
		}},
		{"writable-temp-root", func() error {
			return os.MkdirAll(filepath.Join(h.tmp, "owned-host", target.Name), 0o755)
		}},
		{"packaged-binary", func() error {
			return ensureExecutable(h.bin)
		}},
	}
	for _, check := range checks {
		start := time.Now()
		err := check.fn()
		outcome := reportOwnedHostDoctor{Name: check.name, Status: "pass", Duration: formatDuration(time.Since(start))}
		if err != nil {
			outcome.Status = "failed"
			outcome.Detail = err.Error()
			hostReport.Doctor = append(hostReport.Doctor, outcome)
			h.recordOwnedHostSkipOrFail(cfg, hostReport, "owned-host "+target.Name+" doctor "+check.name, err.Error())
			return false
		}
		hostReport.Doctor = append(hostReport.Doctor, outcome)
	}
	h.record(result{Name: "owned-host " + target.Name + " doctor", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) runOwnedHostLocalWorkspaceFlow(target ownedHostTarget, bundlePath string, hostReport *reportLiveOwnedHost) bool {
	caseRoot := filepath.Join(h.tmp, "owned-host", target.Name, "work")
	fixtures := offlineGitFixtures{
		Root:    filepath.Join(caseRoot, "git-fixtures"),
		Remotes: filepath.Join(caseRoot, "git-fixtures", "remotes"),
		Sources: filepath.Join(caseRoot, "git-fixtures", "sources"),
	}
	if err := os.MkdirAll(fixtures.Remotes, 0o755); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	targetProject, err := h.createRemoteOnlyFixture(fixtures, "owned-target", nil)
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " target remote setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	unrelatedProject, err := h.createRemoteOnlyFixture(fixtures, "owned-unrelated", nil)
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " unrelated remote setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	remoteBase, stopGitDaemon, err := h.startLocalGitDaemon(fixtures.Remotes, filepath.Base(targetProject.Remote))
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " local git daemon", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	defer stopGitDaemon()
	targetRemote := remoteBase + "/" + filepath.Base(targetProject.Remote)
	unrelatedRemote := remoteBase + "/" + filepath.Base(unrelatedProject.Remote)
	machineA, err := h.newTwoMachineNode(caseRoot, "machine-a")
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " machine A setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	machineB, err := h.newTwoMachineNode(caseRoot, "machine-b")
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " machine B setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	targetSourceA := filepath.Join(machineA.WorkspaceRoot, "projects", "owned-target")
	unrelatedSourceA := filepath.Join(machineA.WorkspaceRoot, "projects", "owned-unrelated")
	targetPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "owned-target")
	unrelatedPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "owned-unrelated")
	if err := os.MkdirAll(filepath.Dir(targetSourceA), 0o755); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " source parent setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}

	if smoke := h.ownedHostCommand(target, hostReport, bundlePath, "packaged help smoke", "packaged_help", twoMachineNode{WorkspaceRoot: caseRoot, CodeMeshHome: filepath.Join(caseRoot, "smoke-home"), Home: filepath.Join(caseRoot, "smoke-home")}, h.bin, "--help"); smoke.Status != "PASS" || !strings.Contains(smoke.Stdout, "CodeMesh") {
		return false
	}
	if clone := h.ownedHostCommand(target, hostReport, bundlePath, "machine A clone selected source", "clone_selected", machineA, "git", "clone", targetRemote, targetSourceA); clone.Status != "PASS" {
		return false
	}
	if clone := h.ownedHostCommand(target, hostReport, bundlePath, "machine A clone unrelated source", "clone_unrelated", machineA, "git", "clone", unrelatedRemote, unrelatedSourceA); clone.Status != "PASS" {
		return false
	}
	if init := h.ownedHostCommand(target, hostReport, bundlePath, "machine A init", "machine_a_init", machineA, h.bin, "init", machineA.WorkspaceRoot); init.Status != "PASS" {
		return false
	}
	registerA := h.ownedHostCommand(target, hostReport, bundlePath, "machine A register", "machine_a_register", machineA, h.bin, "machine", "register", machineA.WorkspaceRoot, "--json")
	machineAID, ok := h.expectMachineRegisterJSON("owned-host "+target.Name+" machine A id", registerA, machineA.WorkspaceRoot)
	if !ok {
		return false
	}
	if add := h.ownedHostCommand(target, hostReport, bundlePath, "machine A add selected project", "machine_a_add_selected", machineA, h.bin, "add", targetSourceA, "--alias", "owned-target"); add.Status != "PASS" {
		return false
	}
	if add := h.ownedHostCommand(target, hostReport, bundlePath, "machine A add unrelated project", "machine_a_add_unrelated", machineA, h.bin, "add", unrelatedSourceA, "--alias", "owned-unrelated"); add.Status != "PASS" {
		return false
	}
	rowsA, err := readProjectRowsFromStore(filepath.Join(machineA.CodeMeshHome, "codemesh.db"))
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " machine A rows", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	targetRowA, ok := projectRowByAlias(rowsA, "owned-target")
	if !ok {
		h.record(result{Name: "owned-host " + target.Name + " machine A rows", Status: "FAIL", Error: "owned-target row missing", ExitCode: -1})
		return false
	}
	unrelatedRowA, ok := projectRowByAlias(rowsA, "owned-unrelated")
	if !ok {
		h.record(result{Name: "owned-host " + target.Name + " machine A rows", Status: "FAIL", Error: "owned-unrelated row missing", ExitCode: -1})
		return false
	}
	if targetRowA.CloneURL != targetRemote || unrelatedRowA.CloneURL != unrelatedRemote {
		h.record(result{Name: "owned-host " + target.Name + " machine A rows", Status: "FAIL", Error: fmt.Sprintf("clone URLs = %q %q", targetRowA.CloneURL, unrelatedRowA.CloneURL), ExitCode: -1})
		return false
	}
	manifest := filepath.Join(caseRoot, "manifest")
	targetDesiredPath := "projects/owned-target"
	unrelatedDesiredPath := "projects/owned-unrelated"
	targetIdentity := targetRemote
	unrelatedIdentity := unrelatedRemote
	normalizedTargetIdentity := strings.TrimSuffix(targetIdentity, ".git")
	if err := writeE2EManifestEntryWithCloneURL(manifest, "owned-target.json", targetIdentity, "owned-target", targetDesiredPath, targetRemote); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " selected manifest", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if err := writeE2EManifestEntryWithCloneURL(manifest, "owned-unrelated.json", unrelatedIdentity, "owned-unrelated", unrelatedDesiredPath, unrelatedRemote); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " unrelated manifest", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	manifestBundle := filepath.Join(bundlePath, slug(target.Name), "manifest")
	if err := copyOwnedHostDir(manifest, manifestBundle); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " manifest bundle", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	hostReport.ManifestLocation = h.repoArtifactPath(manifestBundle)
	hostReport.HydratedProjectID = normalizedTargetIdentity

	if init := h.ownedHostCommand(target, hostReport, bundlePath, "machine B init", "machine_b_init", machineB, h.bin, "init", machineB.WorkspaceRoot); init.Status != "PASS" {
		return false
	}
	registerB := h.ownedHostCommand(target, hostReport, bundlePath, "machine B register", "machine_b_register", machineB, h.bin, "machine", "register", machineB.WorkspaceRoot, "--json")
	machineBID, ok := h.expectMachineRegisterJSON("owned-host "+target.Name+" machine B id", registerB, machineB.WorkspaceRoot)
	if !ok {
		return false
	}
	hostReport.MachineIDs = []string{machineAID, machineBID}
	if machineAID == machineBID {
		h.record(result{Name: "owned-host " + target.Name + " distinct machine identities", Status: "FAIL", Error: "machine IDs matched across isolated homes", ExitCode: -1})
		return false
	}
	if plan := h.ownedHostCommand(target, hostReport, bundlePath, "bootstrap dry-run plan", "bootstrap_dry_run", machineB, h.bin, "bootstrap", manifest); plan.Status != "PASS" || !resultContainsAll(plan, "bootstrap plan", "apply: false", "missing: owned-target "+targetPathB, "missing: owned-unrelated "+unrelatedPathB) {
		return false
	}
	if apply := h.ownedHostCommand(target, hostReport, bundlePath, "bootstrap apply topology", "bootstrap_apply", machineB, h.bin, "bootstrap", manifest, "--apply"); apply.Status != "PASS" || !resultContainsAll(apply, "apply: true", "added: owned-target "+targetPathB, "added: owned-unrelated "+unrelatedPathB) {
		return false
	}
	if !h.expectPathMissingResult("owned-host "+target.Name+" bootstrap no default selected checkout", targetPathB) || !h.expectPathMissingResult("owned-host "+target.Name+" bootstrap no default unrelated checkout", unrelatedPathB) {
		return false
	}
	hydrate := h.ownedHostCommand(target, hostReport, bundlePath, "hydrate selected project", "hydrate", machineB, h.bin, "hydrate", "owned-target", "--json")
	if hydrate.Status != "PASS" || !h.expectTwoMachineHydrateJSON("owned-host "+target.Name+" hydrate metadata", hydrate, "owned-target", normalizedTargetIdentity, targetPathB) {
		return false
	}
	if !h.expectGitCheckoutAtBase("owned-host "+target.Name+" hydrated checkout branch", targetPathB, "main") || !h.expectPathMissingResult("owned-host "+target.Name+" selected hydrate no unrelated checkout", unrelatedPathB) {
		return false
	}
	prepare := h.ownedHostCommand(target, hostReport, bundlePath, "agent prepare selected project", "agent_prepare", machineB, h.bin, "agent", "prepare", "owned-target", "--base", "main", "--profile", "codex", "--json")
	if prepare.Status != "PASS" {
		return false
	}
	readyPath, err := agentPrepareReadyPath(prepare.Stdout)
	if err != nil {
		h.record(result{Name: "owned-host " + target.Name + " ready path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	contractBundle := filepath.Join(bundlePath, slug(target.Name), "codemesh-run.json")
	if err := copyOwnedHostFile(filepath.Join(readyPath, "codemesh-run.json"), contractBundle); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " run contract bundle", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	hostReport.Artifacts = append(hostReport.Artifacts, reportOwnedHostArtifact{
		Command:    "agent_run_contract",
		StdoutPath: h.repoArtifactPath(contractBundle),
	})
	runs := h.ownedHostCommand(target, hostReport, bundlePath, "runs reads prepared agent run", "runs_prepared", machineB, h.bin, "runs")
	if runs.Status != "PASS" || !resultContainsAll(runs, "project=owned-target", "base=main", "state=prepared", "workspace="+readyPath) {
		return false
	}
	runID := firstRunID(runs.Stdout)
	if runID == "" {
		h.record(result{Name: "owned-host " + target.Name + " run id", Status: "FAIL", Error: "runs output did not include a run id", ExitCode: -1})
		return false
	}
	hostReport.SelectedRunIDs = []string{runID}
	agentRun := h.ownedHostCommand(target, hostReport, bundlePath, "agent run harmless command", "agent_run", machineB, h.bin, "agent", "run", runID, "--label", "workspace root", "--", "git", "rev-parse", "--show-toplevel")
	if agentRun.Status != "PASS" || !resultContainsAll(agentRun, "agent command complete", "run: "+runID, "exit_code: 0", "stdout_path: ", "stderr_path: ") {
		return false
	}
	if !h.expectCommandStdoutEqualsCanonicalPath("owned-host "+target.Name+" agent command stdout", valueAfterPrefix(agentRun.Stdout, "stdout_path: "), readyPath) {
		return false
	}
	if !h.expectAgentRunCommandContract("owned-host "+target.Name+" agent command metadata", machineB.CodeMeshHome, readyPath, "workspace root") {
		return false
	}
	runsExecuted := h.ownedHostCommand(target, hostReport, bundlePath, "runs reads executed agent run", "runs_executed", machineB, h.bin, "runs")
	if runsExecuted.Status != "PASS" || !resultContainsAll(runsExecuted, "project=owned-target", "state=executed", "workspace="+readyPath) {
		return false
	}
	clean := h.ownedHostCommand(target, hostReport, bundlePath, "clean prepared agent run", "clean", machineB, h.bin, "clean", "--older-than", "0d")
	if clean.Status != "PASS" || !resultContainsAll(clean, "deleted: 1", "kept: 0") {
		return false
	}
	hostReport.CleanupStatus = "managed agent run cleaned; harness temp cleanup scheduled"
	driftManifest := filepath.Join(caseRoot, "manifest-drift")
	movedPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "owned-target-moved")
	if err := writeE2EManifestEntryWithCloneURL(driftManifest, "owned-target.json", targetIdentity, "owned-target", "projects/owned-target-moved", targetRemote); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " drift manifest selected", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if err := writeE2EManifestEntryWithCloneURL(driftManifest, "owned-unrelated.json", unrelatedIdentity, "owned-unrelated", unrelatedDesiredPath, unrelatedRemote); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " drift manifest unrelated", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	drift := h.ownedHostCommand(target, hostReport, bundlePath, "reconcile dry-run moved drift", "reconcile_dry_run", machineB, h.bin, "bootstrap", driftManifest, "--json")
	if drift.Status != "PASS" || !resultContainsAll(drift, `"command":"bootstrap"`, `"apply":false`, `"kind":"moved"`, `"alias":"owned-target"`, movedPathB) {
		return false
	}
	if err := scanPathForFakeSecrets(caseRoot, fakeEnvFixtureSecrets()); err != nil {
		h.record(result{Name: "owned-host " + target.Name + " local state secret safety", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	hostReport.CodeMeshE2EReportPaths = append(hostReport.CodeMeshE2EReportPaths, h.repoArtifactPath(h.reportPath))
	h.record(result{Name: "owned-host " + target.Name + " workspace flow", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) ownedHostCommand(target ownedHostTarget, hostReport *reportLiveOwnedHost, bundlePath, label, key string, machine twoMachineNode, name string, args ...string) result {
	if err := os.MkdirAll(machine.CodeMeshHome, 0o755); err != nil {
		return result{Name: label, Status: "FAIL", Error: err.Error(), ExitCode: -1}
	}
	if err := os.MkdirAll(machine.Home, 0o755); err != nil {
		return result{Name: label, Status: "FAIL", Error: err.Error(), ExitCode: -1}
	}
	gitConfig := filepath.Join(machine.Home, ".gitconfig")
	if _, err := os.Stat(gitConfig); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(gitConfig, nil, 0o644); err != nil {
			return result{Name: label, Status: "FAIL", Error: err.Error(), ExitCode: -1}
		}
	}
	r := h.executeCommand(commandSpec{
		Label:   "owned-host " + target.Name + " " + label,
		Dir:     machine.WorkspaceRoot,
		Name:    name,
		Args:    args,
		Timeout: longCommandTimeout,
		Env:     machine.env(),
	})
	if hostReport.CommandDurations == nil {
		hostReport.CommandDurations = map[string]string{}
	}
	hostReport.CommandDurations[key] = r.Duration
	stdoutPath, stderrPath := h.writeOwnedHostCommandArtifacts(bundlePath, target.Name, key, r.Stdout, r.Stderr)
	hostReport.Artifacts = append(hostReport.Artifacts, reportOwnedHostArtifact{
		Command:    key,
		StdoutPath: h.repoArtifactPath(stdoutPath),
		StderrPath: h.repoArtifactPath(stderrPath),
	})
	recorded := r
	recorded.Stdout = ""
	recorded.Stderr = ""
	h.record(recorded)
	return r
}

func (h *harness) writeOwnedHostCommandArtifacts(bundlePath, hostName, key, stdout, stderr string) (string, string) {
	dir := filepath.Join(bundlePath, slug(hostName))
	_ = os.MkdirAll(dir, 0o755)
	stdoutPath := filepath.Join(dir, slug(key)+".stdout.txt")
	stderrPath := filepath.Join(dir, slug(key)+".stderr.txt")
	redactions := h.redactionMarkers()
	stdout, _ = redactString(stdout, 0, redactions...)
	stderr, _ = redactString(stderr, 0, redactions...)
	_ = os.WriteFile(stdoutPath, []byte(stdout), 0o644)
	_ = os.WriteFile(stderrPath, []byte(stderr), 0o644)
	return stdoutPath, stderrPath
}

func (h *harness) runOwnedHostSSHDoctor(cfg liveConfig, target ownedHostTarget, _ string, hostReport *reportLiveOwnedHost) bool {
	if _, err := exec.LookPath("ssh"); err != nil {
		h.recordOwnedHostSkipOrFail(cfg, hostReport, "owned-host "+target.Name+" ssh prerequisite", "ssh executable not found")
		return !cfg.Strict
	}
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target.Address}
	if target.TargetOS == "windows" {
		args = append(args, "powershell", "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
	} else {
		args = append(args, "sh", "-lc", "uname -s && uname -m && command -v git && command -v go")
	}
	r := h.executeCommand(commandSpec{
		Label:      "owned-host " + target.Name + " ssh doctor",
		Name:       "ssh",
		Args:       args,
		Timeout:    15 * time.Second,
		UseHostEnv: true,
	})
	hostReport.CommandDurations = map[string]string{"ssh_doctor": r.Duration}
	if r.Status != "PASS" {
		h.recordOwnedHostSkipOrFail(cfg, hostReport, r.Name, strings.TrimSpace(resultError(r).Error()))
		return !cfg.Strict
	}
	hostReport.Doctor = append(hostReport.Doctor, reportOwnedHostDoctor{Name: "ssh-reachability", Status: "pass", Detail: strings.TrimSpace(r.Stdout), Duration: r.Duration})
	hostReport.Status = "skipped"
	hostReport.SkipReason = "remote SSH owned-host full workspace flow is reserved for a follow-up slice after reachable host script validation"
	hostReport.SecretSafety = "not_run"
	h.skip("owned-host "+target.Name+" remote workspace flow", hostReport.SkipReason)
	return true
}

func (h *harness) recordOwnedHostSkipOrFail(cfg liveConfig, hostReport *reportLiveOwnedHost, name, reason string) {
	status := "SKIP"
	reportStatus := "skipped"
	if cfg.Strict {
		status = "FAIL"
		reportStatus = "failed"
	}
	hostReport.Status = reportStatus
	hostReport.SkipReason = reason
	if hostReport.SecretSafety == "" || hostReport.SecretSafety == "pending" {
		hostReport.SecretSafety = "not_run"
	}
	if h.live != nil {
		h.live.SkipReasons = append(h.live.SkipReasons, reason)
	}
	h.record(result{Name: name, Status: status, Error: reason, Duration: formatDuration(0), ExitCode: -1})
}

func (h *harness) scanOwnedHostEvidenceBundle(bundlePath string) error {
	return scanPathForFakeSecrets(bundlePath, fakeEnvFixtureSecrets())
}

func scanPathForFakeSecrets(root string, secrets []string) error {
	var leaks []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if containsAnySecret(string(data), secrets) {
			leaks = append(leaks, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(leaks) != 0 {
		return errors.New("fake secret marker appeared under " + root + ": " + strings.Join(leaks, ", "))
	}
	return nil
}

func copyOwnedHostDir(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyOwnedHostFile(path, target)
	})
}

func copyOwnedHostFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func ownedHostInventoryFromEnv(lookup func(string) (string, bool)) ([]ownedHostTarget, error) {
	value, _ := lookup("CODEMESH_E2E_OWNED_HOSTS")
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var hosts []ownedHostTarget
	for _, raw := range strings.Split(value, ",") {
		label := strings.ToLower(strings.TrimSpace(raw))
		if label == "" {
			continue
		}
		switch label {
		case "local", "local-macos", "macos":
			hosts = append(hosts, ownedHostTarget{Name: "local-macos", Kind: "local", TargetOS: "darwin"})
		case "hermes-win", "windows":
			hosts = append(hosts, ownedHostTarget{Name: "hermes-win", Kind: "ssh", TargetOS: "windows", Address: "hermes-win"})
		case "hermes-vm", "linux":
			hosts = append(hosts, ownedHostTarget{Name: "hermes-vm", Kind: "ssh", TargetOS: "linux", Address: "hermes-vm"})
		default:
			return nil, fmt.Errorf("unknown owned-host target %q", raw)
		}
	}
	if extra, ok := lookup("CODEMESH_E2E_EXTRA_LINUX_HOST"); ok {
		extra = strings.TrimSpace(extra)
		if extra != "" {
			hosts = append(hosts, ownedHostTarget{Name: "extra-linux", Kind: "ssh", TargetOS: "linux", Address: extra})
		}
	}
	return hosts, nil
}

func (h *harness) prepareBinary() bool {
	if !h.externalBin {
		return h.buildBinary()
	}
	if err := ensureExecutable(h.bin); err != nil {
		h.record(result{Name: "packaged binary setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	return true
}

func (h *harness) caseLiveGitHubRemoteSmoke(cfg liveConfig) {
	remote := liveGitHubRemoteFromEnv(os.LookupEnv)
	h.live.GitHub = &reportLiveGitHub{
		RemoteURL:        liveGitHubReportRemote(remote),
		CommandDurations: make(map[string]string),
		SecretSafety:     "pending",
	}
	if err := validateLiveGitHubRemote(remote); err != nil {
		h.live.GitHub.SecretSafety = "not_run"
		h.record(result{Name: "live github remote config", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if _, err := exec.LookPath("git"); err != nil {
		h.recordLiveGitHubSkipOrFail(cfg, "live github git prerequisite", "git executable not found", "", -1)
		return
	}

	lsRemote := h.executeCommand(commandSpec{
		Label:   "live github default branch",
		Dir:     h.runDir,
		Name:    "git",
		Args:    []string{"ls-remote", "--symref", remote, "HEAD"},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("ls_remote", lsRemote)
	if lsRemote.Status != "PASS" {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, lsRemote, remote) {
			return
		}
		h.record(lsRemote)
		return
	}
	defaultBranch, err := parseRemoteDefaultBranch(lsRemote.Stdout)
	if err != nil {
		h.recordLiveGitHubSkipOrFail(cfg, lsRemote.Name, err.Error(), lsRemote.Duration, lsRemote.ExitCode)
		return
	}
	h.live.GitHub.DefaultBranch = defaultBranch
	h.record(lsRemote)

	seedPath := filepath.Join(h.workspace, "live-github-seed")
	cloneSeed := h.executeCommand(commandSpec{
		Label:   "live github seed clone",
		Dir:     h.runDir,
		Name:    "git",
		Args:    []string{"clone", "--depth", "1", "--branch", defaultBranch, "--single-branch", remote, seedPath},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("seed_clone", cloneSeed)
	if cloneSeed.Status != "PASS" {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, cloneSeed, remote) {
			return
		}
		h.record(cloneSeed)
		return
	}
	h.record(cloneSeed)
	realSeedPath, err := filepath.EvalSymlinks(seedPath)
	if err != nil {
		h.record(result{Name: "live github seed canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	seedPath = realSeedPath

	initResult := h.runCommand(commandSpec{
		Label:   "live github init isolated workspace",
		Name:    h.bin,
		Args:    []string{"init", h.workspace},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_init", initResult)
	if initResult.Status != "PASS" {
		return
	}

	addResult := h.runCommand(commandSpec{
		Label:   "live github add seed checkout",
		Name:    h.bin,
		Args:    []string{"add", seedPath, "--alias", "live-github"},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_add", addResult)
	if addResult.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(seedPath); err != nil {
		h.record(result{Name: "live github remove seed checkout", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	statusMissing := h.runCommand(commandSpec{
		Label:   "live github status missing project",
		Name:    h.bin,
		Args:    []string{"status", "live-github", "--base", defaultBranch},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_status_missing", statusMissing)
	if statusMissing.Status != "PASS" || !h.expectLiveCommandOutput(statusMissing, "state: missing", "base: "+defaultBranch, "blocker: missing-path") {
		return
	}

	prepare := h.executeCommand(commandSpec{
		Label:   "live github agent prepare missing project",
		Name:    h.bin,
		Args:    []string{"agent", "prepare", "live-github", "--base", defaultBranch, "--profile", "codex", "--json"},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_agent_prepare", prepare)
	if prepare.Status != "PASS" {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, prepare, remote) {
			return
		}
		h.record(prepare)
		return
	}
	h.record(prepare)
	readyPath, ok := h.expectLiveAgentPrepareStrategyJSON("live github full agent prepare clone strategy", prepare, remote, seedPath, defaultBranch, reportLiveCloneStrategySelection{
		Name:        "full-clone",
		History:     "full",
		WorkingTree: "complete",
	})
	if !ok {
		return
	}
	if !h.expectLiveAgentWorkspace(remote, seedPath, readyPath, defaultBranch) {
		return
	}

	runs := h.executeCommand(commandSpec{
		Label:   "live github runs reads prepared agent run",
		Name:    h.bin,
		Args:    []string{"runs"},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_runs", runs)
	if runs.Status != "PASS" {
		h.record(runs)
		return
	}
	h.record(runs)
	if !h.expectLiveCommandOutput(runs, "project=live-github", "base="+defaultBranch, "profile=codex", "state=prepared", "workspace="+readyPath) {
		return
	}
	runID := firstRunID(runs.Stdout)
	if runID == "" {
		h.record(result{Name: "live github run id", Status: "FAIL", Error: "runs output did not include a run id", ExitCode: -1})
		return
	}

	agentRun := h.executeCommand(commandSpec{
		Label:   "live github agent run harmless command",
		Name:    h.bin,
		Args:    []string{"agent", "run", runID, "--label", "workspace root", "--", "git", "rev-parse", "--show-toplevel"},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_agent_run", agentRun)
	if agentRun.Status != "PASS" {
		h.record(agentRun)
		return
	}
	h.record(agentRun)
	if !h.expectLiveCommandOutput(agentRun, "agent command complete", "run: "+runID, "label: workspace root", "exit_code: 0", "stdout_path: ", "stderr_path: ") {
		return
	}
	if !h.expectCommandStdoutEqualsCanonicalPath("live github agent command stdout", valueAfterPrefix(agentRun.Stdout, "stdout_path: "), readyPath) {
		return
	}
	if !h.expectAgentRunCommandContract("live github agent command metadata", h.codemeshHome, readyPath, "workspace root") {
		return
	}

	runsExecuted := h.executeCommand(commandSpec{
		Label:   "live github runs reads executed agent run",
		Name:    h.bin,
		Args:    []string{"runs"},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_runs_executed", runsExecuted)
	if runsExecuted.Status != "PASS" {
		h.record(runsExecuted)
		return
	}
	h.record(runsExecuted)
	if !h.expectLiveCommandOutput(runsExecuted, "project=live-github", "state=executed", "workspace="+readyPath) {
		return
	}

	clean := h.executeCommand(commandSpec{
		Label:   "live github clean prepared agent run",
		Name:    h.bin,
		Args:    []string{"clean", "--older-than", "0d"},
		Timeout: defaultCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_clean", clean)
	if clean.Status != "PASS" {
		h.record(clean)
		return
	}
	h.record(clean)
	if !h.expectLiveCommandOutput(clean, "deleted: 1", "kept: 0") {
		return
	}
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		h.record(result{Name: "live github clean removed managed agent workspace", Status: "FAIL", Error: fmt.Sprintf("ready path exists or stat failed after clean: %v", err), ExitCode: -1})
		return
	}

	hydrate := h.executeCommand(commandSpec{
		Label:   "live github hydrate missing project",
		Name:    h.bin,
		Args:    []string{"hydrate", "live-github", "--json"},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_hydrate", hydrate)
	if hydrate.Status != "PASS" {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, hydrate, remote) {
			return
		}
		h.record(hydrate)
		return
	}
	h.record(hydrate)
	if !h.expectLiveHydrateStrategyJSON("live github full hydrate clone strategy", hydrate, "live-github", seedPath, true, reportLiveCloneStrategySelection{
		Name:        "full-clone",
		History:     "full",
		WorkingTree: "complete",
	}) {
		return
	}
	if !h.expectGitCheckoutAtBase("live github hydrated checkout branch", seedPath, defaultBranch) {
		return
	}
	if !h.expectGitOrigin("live github hydrated checkout origin", seedPath, remote) {
		return
	}

	statusHydrated := h.executeCommand(commandSpec{
		Label:   "live github status hydrated project",
		Name:    h.bin,
		Args:    []string{"status", "live-github", "--base", defaultBranch},
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_status_hydrated", statusHydrated)
	if statusHydrated.Status != "PASS" {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, statusHydrated, remote) {
			return
		}
		h.record(statusHydrated)
		return
	}
	if !liveHydratedStatusLooksUsable(statusHydrated.Stdout, defaultBranch) {
		if h.recordLiveGitHubCommandSkipOrFail(cfg, statusHydrated, remote) {
			return
		}
		h.record(statusHydrated)
		h.record(result{Name: statusHydrated.Name + " assertion", Status: "FAIL", Error: "hydrated status did not report usable checkout path and base", ExitCode: -1})
		return
	}
	h.record(statusHydrated)
	if !h.expectLiveCommandOutput(statusHydrated, "path_present: true", "base: "+defaultBranch) {
		return
	}
	sparseInclude, sparseExclude, sparseAvailable := h.liveSparseCheckoutPaths(seedPath)
	if err := os.RemoveAll(seedPath); err != nil {
		h.record(result{Name: "live github remove hydrated checkout for clone strategy smokes", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	h.runLiveAgentPrepareStrategySmoke(cfg, "partial", remote, seedPath, defaultBranch, []string{"--partial-clone"}, reportLiveCloneStrategySelection{
		Name:        "partial-clone",
		History:     "partial",
		WorkingTree: "complete",
		Filter:      "blob:none",
	}, nil, nil)
	if sparseAvailable {
		h.runLiveAgentPrepareStrategySmoke(cfg, "sparse", remote, seedPath, defaultBranch, []string{"--sparse", sparseInclude}, reportLiveCloneStrategySelection{
			Name:        "sparse-checkout",
			History:     "full",
			WorkingTree: "sparse",
			SparsePaths: []string{sparseInclude},
		}, []string{sparseInclude}, []string{sparseExclude})
	} else {
		h.recordLiveCloneStrategySkipOrFail(cfg, "sparse", "agent prepare", reportLiveCloneStrategySelection{
			Name:        "sparse-checkout",
			History:     "full",
			WorkingTree: "sparse",
		}, "live sparse checkout smoke requires at least two tracked files", "", -1)
	}
	if !h.expectLiveGitHubState(remote, seedPath) {
		return
	}
	if !h.expectLiveHostPathIsolation() {
		return
	}
	h.live.GitHub.SecretSafety = "pass"
}

func (h *harness) caseLiveProviderSmoke(_ liveConfig) {
	providerCfg, configured, reason := liveProviderSmokeConfigFromEnv(os.LookupEnv)
	if h.live == nil {
		h.live = &reportLive{}
	}
	if !configured {
		h.live.Provider = &reportLiveProvider{
			Status:     "skipped",
			SkipReason: reason,
		}
		h.skip("live provider smoke config", reason)
		return
	}
	h.live.Provider = &reportLiveProvider{
		Status:              "skipped",
		Provider:            providerCfg.Provider,
		Requirement:         providerCfg.Requirement,
		Scope:               providerCfg.Scope,
		SecretRefConfigured: providerCfg.SecretRef != "",
		SkipReason:          "live provider smoke is reserved until a real provider implementation exists",
	}
	h.skip("live provider smoke", h.live.Provider.SkipReason)
}

func (h *harness) caseLivePeekabooDesktopSmoke(cfg liveConfig) {
	if h.live == nil {
		h.live = &reportLive{}
	}
	terminalApp := strings.TrimSpace(os.Getenv("CODEMESH_E2E_DESKTOP_TERMINAL_APP"))
	if terminalApp == "" {
		terminalApp = "Terminal"
	}
	h.live.Desktop = &reportLiveDesktop{Status: "running", TerminalApp: terminalApp, SecretSafety: "pending"}
	if runtime.GOOS != "darwin" {
		h.recordLiveDesktopSkipOrFail(cfg, "live peekaboo macOS prerequisite", "Peekaboo desktop smoke requires macOS", "", -1)
		return
	}
	peekaboo, err := findPeekabooBinary()
	if err != nil {
		h.recordLiveDesktopSkipOrFail(cfg, "live peekaboo binary prerequisite", err.Error(), "", -1)
		return
	}
	h.live.Desktop.PeekabooPath = peekaboo

	permissions := h.peekabooCommand("live peekaboo permissions", peekaboo, "permissions", "--json", "--no-remote")
	if permissions.Status != "PASS" {
		permissions = h.peekabooCommand("live peekaboo permissions status", peekaboo, "permissions", "status", "--json", "--no-remote")
	}
	if permissions.Status != "PASS" {
		h.recordLiveDesktopSkipOrFail(cfg, permissions.Name, resultError(permissions).Error(), permissions.Duration, permissions.ExitCode)
		return
	}
	parsedPermissions, err := parsePeekabooPermissions([]byte(permissions.Stdout))
	if err != nil {
		h.recordLiveDesktopSkipOrFail(cfg, "live peekaboo permissions assertion", err.Error(), permissions.Duration, permissions.ExitCode)
		return
	}
	h.live.Desktop.Permissions = &parsedPermissions
	h.record(permissions)

	artifacts, err := h.preparePeekabooDesktopArtifacts()
	if err != nil {
		h.record(result{Name: "live peekaboo artifact setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Desktop.Status = "failed"
		return
	}
	h.live.Desktop.ScreenshotPath = h.repoArtifactPath(artifacts.screenshot)
	h.live.Desktop.TranscriptPath = h.repoArtifactPath(artifacts.transcript)
	if err := h.writePeekabooDesktopScript(artifacts); err != nil {
		h.record(result{Name: "live peekaboo script setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Desktop.Status = "failed"
		return
	}

	launchArgs := []string{"app", "launch", terminalApp, "--open", artifacts.script, "--wait-until-ready", "--json", "--no-remote"}
	if terminalApp == "Terminal" {
		launchArgs = []string{"app", "launch", "--bundle-id", "com.apple.Terminal", "--open", artifacts.script, "--wait-until-ready", "--json", "--no-remote"}
	}
	for _, step := range []result{
		h.peekabooCommand("live peekaboo launch terminal command", peekaboo, launchArgs...),
		h.peekabooCommand("live peekaboo focus terminal", peekaboo, "app", "switch", "--to", terminalApp, "--json", "--no-remote"),
	} {
		if step.Status != "PASS" {
			h.recordLiveDesktopSkipOrFail(cfg, step.Name, resultError(step).Error(), step.Duration, step.ExitCode)
			return
		}
		h.record(step)
	}
	if err := waitForFileFragment(artifacts.transcript, "CODEMESH_PEEKABOO_SMOKE_DONE", 45*time.Second); err != nil {
		if transcriptStarted(artifacts.transcript) {
			h.record(result{Name: "live peekaboo terminal transcript", Status: "FAIL", Error: err.Error(), ExitCode: -1})
			h.live.Desktop.Status = "failed"
			return
		}
		h.recordLiveDesktopSkipOrFail(cfg, "live peekaboo terminal transcript", err.Error(), "", -1)
		return
	}
	transcript, err := os.ReadFile(artifacts.transcript)
	if err != nil {
		h.record(result{Name: "live peekaboo transcript read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Desktop.Status = "failed"
		return
	}
	if !h.expectPeekabooTranscript(string(transcript)) {
		h.live.Desktop.Status = "failed"
		return
	}
	h.live.Desktop.SecretSafety = "pass"
	screenshot := h.peekabooCommand("live peekaboo terminal screenshot", peekaboo, "image", "--app", terminalApp, "--mode", "window", "--path", artifacts.screenshot, "--json", "--no-remote")
	if screenshot.Status != "PASS" {
		h.recordLiveDesktopSkipOrFail(cfg, screenshot.Name, resultError(screenshot).Error(), screenshot.Duration, screenshot.ExitCode)
		return
	}
	screenshot.Stdout = ""
	h.record(screenshot)
	if info, err := os.Stat(artifacts.screenshot); err != nil || info.Size() == 0 {
		if err == nil {
			err = errors.New("screenshot file is empty")
		}
		h.record(result{Name: "live peekaboo screenshot artifact", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Desktop.Status = "failed"
		return
	}
	if err := screenshotHasVisiblePixels(artifacts.screenshot); err != nil {
		h.record(result{Name: "live peekaboo screenshot visibility", Status: "FAIL", Error: err.Error(), Duration: screenshot.Duration, ExitCode: -1})
		h.live.Desktop.Status = "failed"
		return
	}
	h.record(result{Name: "live peekaboo packaged CLI visible output", Status: "PASS", ExitCode: 0})
	h.live.Desktop.Status = "pass"
	h.live.Desktop.SecretSafety = "pass"
}

func (h *harness) caseLiveToolchainHostSmoke(cfg liveConfig) {
	if h.live == nil {
		h.live = &reportLive{}
	}
	h.live.Toolchain = &reportLiveToolchain{Status: "running", SecretSafety: "pending"}
	s, err := h.newScenarioWithFixtureRoot("live toolchain host", filepath.Join(h.tmp, "live-toolchain-git-fixtures"))
	if err != nil {
		h.record(result{Name: "live toolchain host setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	goProject, err := h.createClonedFixtureWithSeed(s.fixtures, "live-toolchain-go", writeLiveGoToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "live toolchain go fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	packageProject, err := h.createClonedFixtureWithSeed(s.fixtures, "live-toolchain-package", writeLivePackageToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "live toolchain package fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	missingWarn, err := h.createClonedFixtureWithSeed(s.fixtures, "live-toolchain-missing-warn", writeMissingWarnToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "live toolchain missing warn fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	missingBlock, err := h.createClonedFixtureWithSeed(s.fixtures, "live-toolchain-missing-block", writeMissingBlockToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "live toolchain missing block fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	scan := s.command("live toolchain scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" {
		h.live.Toolchain.Status = "failed"
		return
	}
	if !h.liveToolchainPresentFixture(cfg, s, goProject, "go project", "go") {
		h.live.Toolchain.Status = "failed"
		return
	}
	if !h.liveToolchainPresentFixture(cfg, s, packageProject, "package manager fixture", "npm") {
		h.live.Toolchain.Status = "failed"
		return
	}
	if !h.liveToolchainOptionalMissingFixture(cfg, "codemesh-definitely-missing-optional-tool") {
		h.live.Toolchain.Status = "failed"
		return
	}
	controlledPath, err := h.controlledPath("git", "bash", "sh", "dirname")
	if err != nil {
		h.record(result{Name: "live toolchain controlled path setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		h.live.Toolchain.Status = "failed"
		return
	}
	if !h.liveToolchainMissingStrictDoctor(s, missingWarn, controlledPath) {
		h.live.Toolchain.Status = "failed"
		return
	}
	if !h.liveToolchainMissingBlockPrepare(s, missingBlock, controlledPath) {
		h.live.Toolchain.Status = "failed"
		return
	}
	h.live.Toolchain.Status = "pass"
	h.live.Toolchain.SecretSafety = "pass"
}

func (h *harness) liveToolchainPresentFixture(cfg liveConfig, s *scenario, project gitFixtureProject, kind, requirement string) bool {
	if _, err := exec.LookPath(requirement); err != nil {
		reason := fmt.Sprintf("optional host tool %q not found", requirement)
		h.recordLiveToolchainSkipOrFail(cfg, "live toolchain "+kind+" prerequisite", reason, reportLiveToolchainFixture{
			Name:       project.Name,
			Kind:       kind,
			Status:     "skipped",
			Project:    toolchainProjectFacts{Requirement: requirement},
			SkipReason: reason,
		})
		return !cfg.Strict
	}
	doctor := s.command("live toolchain "+kind+" doctor json", "doctor", project.Name, "--base", "main", "--json")
	if doctor.Status != "PASS" {
		return false
	}
	doctorToolchain, err := singleDoctorToolchain(doctor.Stdout)
	if err != nil {
		h.record(result{Name: "live toolchain " + kind + " doctor facts", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if doctorToolchain.Name != requirement || doctorToolchain.Status != "present" || doctorToolchain.Project.Requirement != requirement || doctorToolchain.Host.Command != requirement || doctorToolchain.Host.Version == "" {
		h.record(result{Name: "live toolchain " + kind + " doctor facts", Status: "FAIL", Error: fmt.Sprintf("toolchain = %#v, want present %s with host version", doctorToolchain, requirement), ExitCode: -1})
		return false
	}
	prepare := s.command("live toolchain "+kind+" agent prepare json", "agent", "prepare", project.Name, "--base", "main", "--json")
	if prepare.Status != "PASS" {
		return false
	}
	readyPath, err := agentPrepareReadyPath(prepare.Stdout)
	if err != nil {
		h.record(result{Name: "live toolchain " + kind + " ready path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	metadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		h.record(result{Name: "live toolchain " + kind + " metadata", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(metadata.Toolchain) != 1 || !agentToolchainEqual(metadata.Toolchain[0], doctorToolchain) {
		h.record(result{Name: "live toolchain " + kind + " doctor/prepare agreement", Status: "FAIL", Error: fmt.Sprintf("doctor=%#v contract=%#v", doctorToolchain, metadata.Toolchain), ExitCode: -1})
		return false
	}
	if !expectNoToolchainArtifacts(h, "live toolchain "+kind+" no environment build", readyPath) {
		return false
	}
	h.live.Toolchain.Fixtures = append(h.live.Toolchain.Fixtures, reportLiveToolchainFixture{
		Name:          project.Name,
		Kind:          kind,
		Status:        "present",
		Project:       doctorToolchain.Project,
		Host:          doctorToolchain.Host,
		DoctorStatus:  doctorToolchain.Status,
		PrepareStatus: metadata.Toolchain[0].Status,
	})
	h.record(result{Name: "live toolchain " + kind + " agreement", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) peekabooCommand(label, peekaboo string, args ...string) result {
	return h.executeCommand(commandSpec{
		Label:      label,
		Name:       peekaboo,
		Args:       args,
		Timeout:    defaultCommandTimeout,
		UseHostEnv: true,
	})
}

func (h *harness) recordLiveDesktopSkipOrFail(cfg liveConfig, name, reason, duration string, exitCode int) bool {
	status := "SKIP"
	reportStatus := "skipped"
	if cfg.Strict {
		status = "FAIL"
		reportStatus = "failed"
	}
	if h.live == nil {
		h.live = &reportLive{}
	}
	if h.live.Desktop == nil {
		h.live.Desktop = &reportLiveDesktop{}
	}
	h.live.Desktop.Status = reportStatus
	h.live.Desktop.SkipReason = reason
	if h.live.Desktop.SecretSafety == "" || h.live.Desktop.SecretSafety == "pending" {
		h.live.Desktop.SecretSafety = "not_run"
	}
	h.live.SkipReasons = append(h.live.SkipReasons, reason)
	h.record(result{Name: name, Status: status, Error: reason, Duration: duration, ExitCode: exitCode})
	return !cfg.Strict
}

func (h *harness) preparePeekabooDesktopArtifacts() (peekabooDesktopArtifacts, error) {
	dir := filepath.Join(h.root, "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return peekabooDesktopArtifacts{}, err
	}
	artifacts := peekabooDesktopArtifacts{
		script:     filepath.Join(dir, "e2e-peekaboo-run.command"),
		screenshot: filepath.Join(dir, "e2e-peekaboo-desktop.png"),
		transcript: filepath.Join(dir, "e2e-peekaboo-transcript.txt"),
	}
	for _, path := range []string{artifacts.screenshot, artifacts.transcript} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return peekabooDesktopArtifacts{}, err
		}
	}
	return artifacts, nil
}

func (h *harness) repoArtifactPath(path string) string {
	rel, err := filepath.Rel(h.root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return path
	}
	return filepath.ToSlash(rel)
}

func (h *harness) writePeekabooDesktopScript(artifacts peekabooDesktopArtifacts) error {
	gitConfig := filepath.Join(h.home, ".gitconfig")
	pathValue := "/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin"
	script := fmt.Sprintf(`#!/bin/zsh
set -euo pipefail
exec > >(tee %s) 2>&1
export CODEMESH_HOME=%s
export HOME=%s
export CODEMESH_WORKSPACE=%s
export GIT_CONFIG_GLOBAL=%s
export GIT_CONFIG_NOSYSTEM=1
export GIT_TERMINAL_PROMPT=0
export PATH=%s
export TERM="${TERM:-xterm-256color}"
cd %s
echo "CODEMESH_PEEKABOO_SMOKE_BEGIN"
echo "run_dir: $PWD"
echo "codemesh_home: $CODEMESH_HOME"
echo "home: $HOME"
echo
echo "$ codemesh --help"
env -i PATH="$PATH" TERM="$TERM" HOME="$HOME" CODEMESH_HOME="$CODEMESH_HOME" CODEMESH_WORKSPACE="$CODEMESH_WORKSPACE" GIT_CONFIG_GLOBAL="$GIT_CONFIG_GLOBAL" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 %s --help
echo
echo "$ codemesh init $CODEMESH_WORKSPACE"
env -i PATH="$PATH" TERM="$TERM" HOME="$HOME" CODEMESH_HOME="$CODEMESH_HOME" CODEMESH_WORKSPACE="$CODEMESH_WORKSPACE" GIT_CONFIG_GLOBAL="$GIT_CONFIG_GLOBAL" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 %s init "$CODEMESH_WORKSPACE"
echo
echo "$ codemesh status"
env -i PATH="$PATH" TERM="$TERM" HOME="$HOME" CODEMESH_HOME="$CODEMESH_HOME" CODEMESH_WORKSPACE="$CODEMESH_WORKSPACE" GIT_CONFIG_GLOBAL="$GIT_CONFIG_GLOBAL" GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 %s status
echo "CODEMESH_PEEKABOO_SMOKE_DONE"
`, shellQuote(artifacts.transcript), shellQuote(h.codemeshHome), shellQuote(h.home), shellQuote(h.workspace), shellQuote(gitConfig), shellQuote(pathValue), shellQuote(h.runDir), shellQuote(h.bin), shellQuote(h.bin), shellQuote(h.bin))
	if err := os.WriteFile(artifacts.script, []byte(script), 0o755); err != nil {
		return err
	}
	return nil
}

func (h *harness) expectPeekabooTranscript(transcript string) bool {
	fragments := []string{
		"CODEMESH_PEEKABOO_SMOKE_BEGIN",
		"$ codemesh --help",
		"Usage:",
		"codemesh init [workspace-root]",
		"$ codemesh init ",
		"initialized CodeMesh",
		"$ codemesh status",
		"readiness:",
		"(empty)",
		"CODEMESH_PEEKABOO_SMOKE_DONE",
	}
	for _, fragment := range fragments {
		if !strings.Contains(transcript, fragment) {
			h.record(result{Name: "live peekaboo transcript assertion", Status: "FAIL", Error: fmt.Sprintf("transcript did not include %q", fragment), ExitCode: -1})
			return false
		}
	}
	for _, marker := range h.forbiddenHostPathMarkers() {
		if marker != "" && strings.Contains(transcript, marker) {
			h.record(result{Name: "live peekaboo host path isolation", Status: "FAIL", Error: "transcript included forbidden host path marker", ExitCode: -1})
			return false
		}
	}
	return true
}

func (h *harness) liveToolchainOptionalMissingFixture(cfg liveConfig, requirement string) bool {
	if _, err := exec.LookPath(requirement); err == nil {
		h.record(result{Name: "live toolchain optional missing prerequisite", Status: "FAIL", Error: fmt.Sprintf("unexpected optional test tool %q exists on host", requirement), ExitCode: -1})
		return false
	}
	reason := fmt.Sprintf("optional host tool %q not found", requirement)
	h.recordLiveToolchainSkipOrFail(cfg, "live toolchain optional missing prerequisite", reason, reportLiveToolchainFixture{
		Name:       "live-toolchain-optional-missing",
		Kind:       "optional host tool",
		Status:     "skipped",
		Project:    toolchainProjectFacts{Requirement: requirement},
		SkipReason: reason,
	})
	return !cfg.Strict
}

func (h *harness) liveToolchainMissingStrictDoctor(s *scenario, project gitFixtureProject, controlledPath string) bool {
	strict := s.expectedFailureEnv("live toolchain missing strict doctor", []string{"PATH=" + controlledPath}, "doctor", project.Name, "--base", "main", "--strict", "--json")
	if strict.Status != "FAIL" {
		strict.Status = "FAIL"
		strict.Error = "strict missing-tool doctor unexpectedly passed"
	} else if strict.ExitCode != 1 {
		strict.Error = fmt.Sprintf("strict missing-tool exit code = %d, want 1", strict.ExitCode)
	} else if strict.Stderr != "" {
		strict.Error = "strict missing-tool doctor wrote stderr: " + strict.Stderr
	} else if err := doctorJSONMatches(strict.Stdout, project.Name, "readiness-warning", "warning", "present", "main", true, []string{"missing-toolchain"}, nil); err != nil {
		strict.Error = err.Error()
	} else {
		strict.Status = "PASS"
		strict.Error = ""
	}
	s.record(strict)
	return strict.Status == "PASS"
}

func (h *harness) liveToolchainMissingBlockPrepare(s *scenario, project gitFixtureProject, controlledPath string) bool {
	blocked := s.expectedFailureEnv("live toolchain missing block agent prepare", []string{"PATH=" + controlledPath}, "agent", "prepare", project.Name, "--base", "main", "--json")
	if blocked.Status != "FAIL" {
		blocked.Status = "FAIL"
		blocked.Error = "missing-tool block agent prepare unexpectedly passed"
	} else if blocked.ExitCode != 1 {
		blocked.Error = fmt.Sprintf("missing-tool block exit code = %d, want 1", blocked.ExitCode)
	} else if blocked.Stderr != "" {
		blocked.Error = "missing-tool block agent prepare wrote stderr: " + blocked.Stderr
	} else if !s.expectAgentPrepareJSON(blocked, "readiness-blocked", project.Name, false, "main", "", 0, nil, []string{"missing-toolchain"}) {
		return false
	} else {
		blocked.Status = "PASS"
		blocked.Error = ""
	}
	s.record(blocked)
	return blocked.Status == "PASS"
}

func (h *harness) recordLiveToolchainSkipOrFail(cfg liveConfig, name, reason string, fixture reportLiveToolchainFixture) {
	status := "SKIP"
	if cfg.Strict {
		status = "FAIL"
		if fixture.Status == "" || fixture.Status == "skipped" {
			fixture.Status = "failed"
		}
	}
	if h.live != nil {
		h.live.SkipReasons = append(h.live.SkipReasons, reason)
		if h.live.Toolchain != nil {
			h.live.Toolchain.SkipReasons = append(h.live.Toolchain.SkipReasons, reason)
			h.live.Toolchain.Fixtures = append(h.live.Toolchain.Fixtures, fixture)
		}
	}
	h.record(result{Name: name, Status: status, Error: reason, Duration: formatDuration(0), ExitCode: -1})
}

func (h *harness) recordLiveGitHubDuration(name string, r result) {
	if h.live == nil || h.live.GitHub == nil || r.Duration == "" {
		return
	}
	h.live.GitHub.CommandDurations[name] = r.Duration
}

func (h *harness) recordLiveCloneStrategyPass(label, command string, strategy reportLiveCloneStrategySelection) {
	if h.live == nil || h.live.GitHub == nil {
		return
	}
	h.live.GitHub.CloneStrategies = append(h.live.GitHub.CloneStrategies, reportLiveCloneStrategy{
		Label:    label,
		Command:  command,
		Status:   "pass",
		Strategy: strategy,
	})
}

func (h *harness) recordLiveCloneStrategySkipOrFail(cfg liveConfig, label, command string, strategy reportLiveCloneStrategySelection, reason, duration string, exitCode int) {
	status := "SKIP"
	reportStatus := "skipped"
	if cfg.Strict {
		status = "FAIL"
		reportStatus = "failed"
	}
	if h.live != nil {
		h.live.SkipReasons = append(h.live.SkipReasons, reason)
		if h.live.GitHub != nil {
			h.live.GitHub.CloneStrategies = append(h.live.GitHub.CloneStrategies, reportLiveCloneStrategy{
				Label:      label,
				Command:    command,
				Status:     reportStatus,
				Strategy:   strategy,
				SkipReason: reason,
			})
		}
	}
	if duration == "" {
		duration = formatDuration(0)
	}
	h.record(result{Name: "live github " + label + " clone strategy", Status: status, Error: reason, Duration: duration, ExitCode: exitCode})
}

func (h *harness) recordLiveGitHubSkipOrFail(cfg liveConfig, name, reason, duration string, exitCode int) {
	if h.live != nil {
		h.live.SkipReasons = append(h.live.SkipReasons, reason)
	}
	status := "SKIP"
	if cfg.Strict {
		status = "FAIL"
	}
	if h.live != nil && h.live.GitHub != nil && h.live.GitHub.SecretSafety == "pending" {
		if status == "SKIP" {
			h.live.GitHub.SecretSafety = "skipped"
		} else {
			h.live.GitHub.SecretSafety = "not_run"
		}
	}
	if duration == "" {
		duration = formatDuration(0)
	}
	h.record(result{Name: name, Status: status, Error: reason, Duration: duration, ExitCode: exitCode})
}

func (h *harness) recordLiveGitHubCommandSkipOrFail(cfg liveConfig, r result, remote string) bool {
	reason := liveGitHubCommandFailureReason(r, remote)
	if !r.TimedOut && !isSkippableLiveGitHubSmokeError(errors.New(reason)) {
		return false
	}
	h.recordLiveGitHubSkipOrFail(cfg, r.Name, reason, r.Duration, r.ExitCode)
	return true
}

func liveGitHubCommandFailureReason(r result, remote string) string {
	detail := strings.TrimSpace(strings.Join([]string{r.Error, r.Stdout, r.Stderr}, "\n"))
	if detail == "" {
		detail = fmt.Sprintf("command exited %d", r.ExitCode)
	}
	return "live GitHub remote unavailable: " + redactLiveGitHubDetail(detail, remote)
}

func redactLiveGitHubDetail(detail, remote string) string {
	redacted := redactedLiveGitHubRemote(remote)
	detail = strings.ReplaceAll(detail, remote, redacted)
	if strings.HasSuffix(remote, "/") {
		return detail
	}
	return strings.ReplaceAll(detail, remote+"/", redacted+"/")
}

func redactedLiveGitHubRemote(remote string) string {
	parsed, err := url.Parse(remote)
	if err != nil || parsed.Scheme == "" {
		return remote
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func liveGitHubReportRemote(remote string) string {
	if validateLiveGitHubRemote(remote) != nil {
		return "invalid CODEMESH_LIVE_GITHUB_REPO"
	}
	return redactedLiveGitHubRemote(remote)
}

func (h *harness) expectLiveCommandOutput(r result, fragments ...string) bool {
	if r.Status != "PASS" {
		return false
	}
	if resultContainsAll(r, fragments...) {
		return true
	}
	for _, fragment := range fragments {
		if !strings.Contains(r.Stdout, fragment) {
			h.record(result{Name: r.Name + " assertion", Status: "FAIL", Error: fmt.Sprintf("stdout did not include %q", fragment), ExitCode: -1})
			return false
		}
	}
	return true
}

func liveHydratedStatusLooksUsable(output, base string) bool {
	if !strings.Contains(output, "path_present: true") || !strings.Contains(output, "base: "+base) {
		return false
	}
	if !strings.Contains(output, "blockers: none") {
		return false
	}
	return strings.Contains(output, "state: present") || strings.Contains(output, "state: stale")
}

type liveAgentPrepareJSON struct {
	Command   string `json:"command"`
	ExitClass string `json:"exit_class"`
	Payload   struct {
		Project         string                           `json:"project"`
		Ready           bool                             `json:"ready"`
		RunID           string                           `json:"run_id"`
		Base            string                           `json:"base"`
		Profile         string                           `json:"profile"`
		RunContractPath string                           `json:"run_contract_path"`
		ReadyPath       string                           `json:"ready_path"`
		CloneStrategy   reportLiveCloneStrategySelection `json:"clone_strategy"`
	} `json:"payload"`
}

type liveHydrateJSON struct {
	Command   string `json:"command"`
	ExitClass string `json:"exit_class"`
	Payload   struct {
		Project       string                           `json:"project"`
		Outcome       string                           `json:"outcome"`
		Path          string                           `json:"path"`
		PathPresent   bool                             `json:"path_present"`
		CloneStrategy reportLiveCloneStrategySelection `json:"clone_strategy"`
	} `json:"payload"`
}

func (h *harness) expectLiveAgentPrepareStrategyJSON(name string, r result, remote, registeredSourcePath, base string, want reportLiveCloneStrategySelection) (string, bool) {
	var payload liveAgentPrepareJSON
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "stdout was not JSON: " + err.Error(), ExitCode: -1})
		return "", false
	}
	got := payload.Payload
	if payload.Command != "agent prepare" || !liveAgentPrepareReadyExitClass(payload.ExitClass) || got.Project != "live-github" || !got.Ready || got.Base != base || got.Profile != "codex" || got.ReadyPath == "" || got.RunContractPath != filepath.Join(got.ReadyPath, "codemesh-run.json") {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("agent prepare JSON payload = %#v", payload), ExitCode: -1})
		return "", false
	}
	if !liveCloneStrategyEqual(got.CloneStrategy, want) {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("command clone strategy = %#v, want %#v", got.CloneStrategy, want), ExitCode: -1})
		return "", false
	}
	metadata, err := readAgentMetadata(got.RunContractPath)
	if err != nil {
		h.record(result{Name: name + " contract", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return "", false
	}
	if metadata.Project.CloneURL != remote || metadata.Project.SourcePath != registeredSourcePath || !metadata.Project.SourcePathMissing {
		h.record(result{Name: name + " contract project", Status: "FAIL", Error: fmt.Sprintf("contract project = %#v", metadata.Project), ExitCode: -1})
		return "", false
	}
	if !liveCloneStrategyEqual(metadata.CloneStrategy, want) {
		h.record(result{Name: name + " contract", Status: "FAIL", Error: fmt.Sprintf("contract clone strategy = %#v, want %#v", metadata.CloneStrategy, want), ExitCode: -1})
		return "", false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(h.codemeshHome, "codemesh.db"), got.RunID)
	if err != nil {
		h.record(result{Name: name + " state", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return "", false
	}
	if !liveCloneStrategyEqual(dbMetadata.CloneStrategy, want) {
		h.record(result{Name: name + " state", Status: "FAIL", Error: fmt.Sprintf("state clone strategy = %#v, want %#v", dbMetadata.CloneStrategy, want), ExitCode: -1})
		return "", false
	}
	reportLabel := strings.TrimSuffix(strings.TrimPrefix(name, "live github "), " clone strategy")
	reportLabel = strings.TrimSuffix(reportLabel, " metadata")
	h.recordLiveCloneStrategyPass(reportLabel, "agent prepare", want)
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return got.ReadyPath, true
}

func liveAgentPrepareReadyExitClass(exitClass string) bool {
	return exitClass == "success" || exitClass == "readiness-warning"
}

func (h *harness) expectLiveHydrateStrategyJSON(name string, r result, alias, path string, pathPresent bool, want reportLiveCloneStrategySelection) bool {
	var payload liveHydrateJSON
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "stdout was not JSON: " + err.Error(), ExitCode: -1})
		return false
	}
	got := payload.Payload
	if payload.Command != "hydrate" || payload.ExitClass != "success" || got.Project != alias || got.Outcome != "hydrated" || got.Path != path || got.PathPresent != pathPresent {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("hydrate JSON payload = %#v", payload), ExitCode: -1})
		return false
	}
	if !liveCloneStrategyEqual(got.CloneStrategy, want) {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("hydrate clone strategy = %#v, want %#v", got.CloneStrategy, want), ExitCode: -1})
		return false
	}
	h.recordLiveCloneStrategyPass("full hydrate", "hydrate", want)
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) runLiveAgentPrepareStrategySmoke(cfg liveConfig, label, remote, registeredSourcePath, base string, strategyArgs []string, want reportLiveCloneStrategySelection, presentPaths, absentPaths []string) bool {
	args := append([]string{"agent", "prepare", "live-github", "--base", base, "--profile", "codex"}, strategyArgs...)
	args = append(args, "--json")
	r := h.executeCommand(commandSpec{
		Label:   "live github " + label + " agent prepare clone strategy",
		Name:    h.bin,
		Args:    args,
		Timeout: longCommandTimeout,
	})
	h.recordLiveGitHubDuration("codemesh_agent_prepare_"+strings.ReplaceAll(label, " ", "_"), r)
	if r.Status != "PASS" {
		reason := liveGitHubCommandFailureReason(r, remote)
		if liveCloneStrategyFailureIsSkippable(r, remote) {
			h.recordLiveCloneStrategySkipOrFail(cfg, label, "agent prepare", want, reason, r.Duration, r.ExitCode)
			return !cfg.Strict
		}
		h.record(r)
		return false
	}
	h.record(r)
	readyPath, ok := h.expectLiveAgentPrepareStrategyJSON("live github "+label+" agent prepare metadata", r, remote, registeredSourcePath, base, want)
	if !ok {
		return false
	}
	if !h.expectGitCheckoutAtBase("live github "+label+" agent checkout branch", readyPath, base) {
		return false
	}
	for _, path := range presentPaths {
		if _, err := os.Stat(filepath.Join(readyPath, path)); err != nil {
			h.record(result{Name: "live github " + label + " sparse present path", Status: "FAIL", Error: fmt.Sprintf("%s missing: %v", path, err), ExitCode: -1})
			return false
		}
	}
	for _, path := range absentPaths {
		if _, err := os.Stat(filepath.Join(readyPath, path)); !errors.Is(err, os.ErrNotExist) {
			h.record(result{Name: "live github " + label + " sparse absent path", Status: "FAIL", Error: fmt.Sprintf("%s materialized or stat failed: %v", path, err), ExitCode: -1})
			return false
		}
	}
	return true
}

func liveCloneStrategyEqual(got, want reportLiveCloneStrategySelection) bool {
	return got.Name == want.Name &&
		got.History == want.History &&
		got.WorkingTree == want.WorkingTree &&
		got.Filter == want.Filter &&
		stringSlicesEqual(got.SparsePaths, want.SparsePaths)
}

func liveCloneStrategyFailureIsSkippable(r result, remote string) bool {
	reason := liveGitHubCommandFailureReason(r, remote)
	if r.TimedOut || isSkippableLiveGitHubSmokeError(errors.New(reason)) {
		return true
	}
	detail := strings.ToLower(reason)
	return strings.Contains(detail, "partial clone") ||
		strings.Contains(detail, "partialclonefilter") ||
		strings.Contains(detail, "filter was not") ||
		strings.Contains(detail, "filter was not honored") ||
		strings.Contains(detail, "sparse-checkout") ||
		strings.Contains(detail, "unknown option")
}

func (h *harness) liveSparseCheckoutPaths(checkoutPath string) (string, string, bool) {
	output, _, err := h.exec(checkoutPath, "git", "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return "", "", false
	}
	return selectLiveSparsePaths(strings.Split(output, "\n"))
}

func selectLiveSparsePaths(paths []string) (string, string, bool) {
	preferred := []string{"README.md", "go.mod"}
	candidates := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	available := make(map[string]bool, len(paths))
	for _, raw := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		path = strings.Trim(path, "/")
		if path == "" || strings.HasPrefix(path, ".git/") || strings.HasPrefix(path, ".github/") || strings.Contains(path, "/.git/") {
			continue
		}
		available[path] = true
	}
	for _, path := range preferred {
		if !available[path] || seen[path] {
			continue
		}
		seen[path] = true
		candidates = append(candidates, path)
	}
	for _, raw := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		path = strings.Trim(path, "/")
		if path == "" || strings.HasPrefix(path, ".git/") || strings.HasPrefix(path, ".github/") || strings.Contains(path, "/.git/") {
			continue
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		candidates = append(candidates, path)
	}
	if len(candidates) < 2 {
		return "", "", false
	}
	return candidates[0], candidates[1], true
}

func resultContainsAll(r result, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(r.Stdout, fragment) {
			return false
		}
	}
	return true
}

func (h *harness) expectGitCheckoutAtBase(name, path, base string) bool {
	inside, _, err := h.exec(path, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("path is not a Git checkout: %v", err), ExitCode: -1})
		return false
	}
	branch, _, err := h.exec(path, "git", "branch", "--show-current")
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if strings.TrimSpace(branch) != base {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("checkout branch = %q, want %q", strings.TrimSpace(branch), base), ExitCode: -1})
		return false
	}
	return true
}

func (h *harness) expectGitOrigin(name, path, remote string) bool {
	origin, _, err := h.exec(path, "git", "remote", "get-url", "origin")
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if strings.TrimSpace(origin) != remote {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("origin = %q, want %q", strings.TrimSpace(origin), remote), ExitCode: -1})
		return false
	}
	return true
}

func (h *harness) expectLiveAgentWorkspace(remote, registeredSourcePath, readyPath, base string) bool {
	if readyPath == "" {
		h.record(result{Name: "live github agent ready path", Status: "FAIL", Error: "agent prepare output did not include ready_path", ExitCode: -1})
		return false
	}
	if !strings.HasPrefix(readyPath, filepath.Join(h.codemeshHome, "agents")+string(filepath.Separator)) {
		h.record(result{Name: "live github agent managed ready path", Status: "FAIL", Error: "ready_path was not under isolated CodeMesh agents storage", ExitCode: -1})
		return false
	}
	if _, err := os.Stat(registeredSourcePath); !errors.Is(err, os.ErrNotExist) {
		h.record(result{Name: "live github agent missing source preserved", Status: "FAIL", Error: fmt.Sprintf("registered source path exists or stat failed: %v", err), ExitCode: -1})
		return false
	}
	if !h.expectGitCheckoutAtBase("live github agent checkout branch", readyPath, base) {
		return false
	}
	if !h.expectGitOrigin("live github agent checkout origin", readyPath, remote) {
		return false
	}
	resolvedCommit, _, err := h.exec(readyPath, "git", "rev-parse", "HEAD")
	if err != nil {
		h.record(result{Name: "live github agent resolved commit", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	metadataPath := filepath.Join(readyPath, "codemesh-run.json")
	metadata, err := readAgentMetadata(metadataPath)
	if err != nil {
		h.record(result{Name: "live github agent metadata read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if metadata.ReadyPath != readyPath || metadata.Project.Alias != "live-github" || metadata.Project.Remote != normalizeLiveGitHubRemoteForMetadata(remote) || metadata.Project.CloneURL != remote || metadata.Project.SourcePath != registeredSourcePath {
		h.record(result{Name: "live github agent metadata project identity", Status: "FAIL", Error: "codemesh-run.json project identity did not match live registered remote", ExitCode: -1})
		return false
	}
	if !metadata.Project.SourcePathMissing {
		h.record(result{Name: "live github agent metadata source absence", Status: "FAIL", Error: "codemesh-run.json did not record missing source checkout", ExitCode: -1})
		return false
	}
	if metadata.ContractVersion != 1 || metadata.Producer.Name != "codemesh" || metadata.Producer.Version == "" {
		h.record(result{Name: "live github agent metadata contract version", Status: "FAIL", Error: "codemesh-run.json missing contract version or producer", ExitCode: -1})
		return false
	}
	if metadata.Base != base || metadata.Profile != "codex" || metadata.ResolvedCommit != strings.TrimSpace(resolvedCommit) || metadata.ReadinessDecision != "ready" {
		h.record(result{Name: "live github agent metadata checkout contract", Status: "FAIL", Error: "codemesh-run.json checkout contract did not match prepared workspace", ExitCode: -1})
		return false
	}
	if len(metadata.Diagnostics.Blockers) != 0 {
		h.record(result{Name: "live github agent metadata readiness", Status: "FAIL", Error: fmt.Sprintf("metadata blockers=%#v", metadata.Diagnostics.Blockers), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(h.codemeshHome, "codemesh.db"), metadata.RunID)
	if err != nil {
		h.record(result{Name: "live github agent state metadata read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if dbMetadata.ReadyPath != metadata.ReadyPath || dbMetadata.ResolvedCommit != metadata.ResolvedCommit || dbMetadata.ReadinessDecision != metadata.ReadinessDecision {
		h.record(result{Name: "live github agent state metadata parity", Status: "FAIL", Error: "state-store metadata did not match codemesh-run.json", ExitCode: -1})
		return false
	}
	if dbMetadata.Project.SourcePathMissing != metadata.Project.SourcePathMissing {
		h.record(result{Name: "live github agent state source absence parity", Status: "FAIL", Error: "state-store source_path_missing did not match codemesh-run.json", ExitCode: -1})
		return false
	}
	if dbMetadata.ContractVersion != metadata.ContractVersion || dbMetadata.Producer != metadata.Producer {
		h.record(result{Name: "live github agent state contract parity", Status: "FAIL", Error: "state-store contract metadata did not match codemesh-run.json", ExitCode: -1})
		return false
	}
	h.record(result{Name: "live github agent workspace metadata", Status: "PASS", ExitCode: 0})
	return true
}

func normalizeLiveGitHubRemoteForMetadata(remote string) string {
	parsed, err := url.Parse(remote)
	if err != nil {
		return remote
	}
	host := strings.ToLower(parsed.Hostname())
	path := strings.TrimPrefix(parsed.Path, "/")
	path = strings.TrimSuffix(path, ".git")
	return parsed.Scheme + "://" + host + "/" + path
}

func (h *harness) expectLiveGitHubState(remote, localPath string) bool {
	rows, err := readProjectRowsFromStore(filepath.Join(h.codemeshHome, "codemesh.db"))
	if err != nil {
		h.record(result{Name: "live github state read", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(rows) != 1 {
		h.record(result{Name: "live github state row count", Status: "FAIL", Error: fmt.Sprintf("project row count = %d, want 1", len(rows)), ExitCode: -1})
		return false
	}
	row := rows[0]
	if row.Alias != "live-github" || row.CloneURL != remote || row.LocalPath != localPath {
		h.record(result{Name: "live github state row", Status: "FAIL", Error: fmt.Sprintf("project row = %#v, want alias live-github clone %s path %s", row, remote, localPath), ExitCode: -1})
		return false
	}
	if inside, err := pathInside(h.workspace, row.LocalPath); err != nil || !inside {
		h.record(result{Name: "live github state path isolation", Status: "FAIL", Error: fmt.Sprintf("local path outside live workspace: %s (%v)", row.LocalPath, err), ExitCode: -1})
		return false
	}
	h.record(result{Name: "live github state row", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) expectLiveHostPathIsolation() bool {
	forbidden := h.forbiddenHostPathMarkers()
	for _, r := range h.results {
		if containsAnyFragment(r.Stdout, forbidden) || containsAnyFragment(r.Stderr, forbidden) || containsAnyFragment(r.Error, forbidden) {
			h.record(result{Name: "live github command output path isolation", Status: "FAIL", Error: "host workspace or normal CodeMesh home path appeared in command output", ExitCode: -1})
			return false
		}
	}
	for _, path := range h.stateStorePaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			h.record(result{Name: "live github state path isolation", Status: "FAIL", Error: err.Error(), ExitCode: -1})
			return false
		}
		if containsAnyFragment(string(data), forbidden) {
			h.record(result{Name: "live github state path isolation", Status: "FAIL", Error: "host workspace or normal CodeMesh home path appeared in state store", ExitCode: -1})
			return false
		}
	}
	h.record(result{Name: "live github command/state path isolation", Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) finishLive() int {
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
	if !s.expectProjectRowCount("project registry scan idempotent state rows", 9) {
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
	if register.Status != "PASS" || !s.expectOutput(register, "machine registered", "id: ", "hostname: ", "os: ", "architecture: ", "workspace_root: "+firstRoot, "registered_at: ", "updated_at: ") {
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
	treeJSON := s.command("readiness tree json scanned fixtures", "tree", "--json")
	if treeJSON.Status != "PASS" || !s.expectTreeJSON(treeJSON, "readiness-blocked", map[string]string{
		"clean-repo":   "present",
		"dirty-source": "dirty",
	}) {
		return
	}

	clean := s.command("readiness status clean repo", "status", "clean-repo", "--base", "main")
	if clean.Status != "PASS" || !s.expectOutput(clean, "state: present", "path_present: true", "warnings: none", "blockers: none") {
		return
	}
	if !s.expectTreeStatusAgreement(tree, clean, "clean-repo") {
		return
	}
	cleanJSON := s.command("readiness status clean repo json", "status", "clean-repo", "--base", "main", "--json")
	if cleanJSON.Status != "PASS" || !s.expectStatusJSON(cleanJSON, "clean-repo", "success", "present", "main") {
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

func (h *harness) caseDoctorPreflightWorkflow() {
	s, err := h.newScenario("doctor preflight")
	if err != nil {
		h.record(result{Name: "doctor preflight workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	toolchainProject, err := h.createClonedFixtureWithSeed(s.fixtures, "doctor-toolchain", writeToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "doctor preflight toolchain setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	s.fixtures.Projects = append(s.fixtures.Projects, toolchainProject)
	scan := s.command("doctor preflight scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" {
		return
	}

	green := s.command("doctor preflight clean handoff green", "doctor", "clean-repo", "--base", "main")
	if green.Status != "PASS" || !s.expectOutput(green, "handoff: green", "project: clean-repo", "state: present", "path_present: true", "source_path_missing: false", "warnings: none", "blockers: none") {
		return
	}
	if !s.expectNoOutput(green, "ready_path: ") {
		return
	}
	if !s.expectAgentRunRows("doctor preflight clean records no agent run", 0) {
		return
	}
	if !s.expectPathMissing("doctor preflight clean creates no agents dir", filepath.Join(s.codemeshHome, "agents")) {
		return
	}

	strict := s.expectedFailure("doctor preflight strict dirty json", "doctor", "dirty-source", "--base", "main", "--strict", "--json")
	if strict.Status != "FAIL" || strict.ExitCode != 1 {
		strict.Status = "FAIL"
		strict.Error = fmt.Sprintf("strict doctor exit status = %s code=%d, want failure code 1", strict.Status, strict.ExitCode)
	} else if strict.Stderr != "" {
		strict.Error = "strict doctor wrote stderr: " + strict.Stderr
	} else if err := doctorJSONMatches(strict.Stdout, "dirty-source", "readiness-warning", "warning", "dirty", "main", true, []string{"dirty-checkout"}, nil); err != nil {
		strict.Error = err.Error()
	} else {
		strict.Status = "PASS"
		strict.Error = ""
	}
	s.record(strict)
	if strict.Status != "PASS" || !s.expectAgentRunRows("doctor preflight strict records no agent run", 0) {
		return
	}

	toolchainHuman := s.command("doctor preflight toolchain human", "doctor", "doctor-toolchain", "--base", "main")
	if toolchainHuman.Status != "PASS" || !s.expectOutput(toolchainHuman, "handoff: green", "toolchain: go present", "warnings: none", "blockers: none") {
		return
	}
	toolchainJSON := s.command("doctor preflight toolchain json", "doctor", "doctor-toolchain", "--base", "main", "--json")
	if toolchainJSON.Status != "PASS" {
		return
	}
	if err := doctorToolchainJSONMatches(toolchainJSON.Stdout, "doctor-toolchain", "go", "present"); err != nil {
		toolchainJSON.Status = "FAIL"
		toolchainJSON.Error = err.Error()
		s.updateResult(toolchainJSON)
		return
	}
	if !s.expectAgentRunRows("doctor preflight toolchain records no agent run", 0) {
		return
	}

	blocked := s.expectedFailure("doctor preflight missing base blocker", "doctor", "missing-base-branch", "--base", "release/missing")
	if blocked.Status != "FAIL" || blocked.ExitCode != 1 {
		blocked.Status = "FAIL"
		blocked.Error = fmt.Sprintf("blocked doctor exit status = %s code=%d, want failure code 1", blocked.Status, blocked.ExitCode)
	} else if blocked.Stderr != "" {
		blocked.Error = "blocked doctor wrote stderr: " + blocked.Stderr
	} else if !strings.Contains(blocked.Stdout, "handoff: blocked") || !strings.Contains(blocked.Stdout, "blocker: missing-base") {
		blocked.Error = "blocked doctor did not report an actionable missing-base blocker"
	} else {
		blocked.Status = "PASS"
		blocked.Error = ""
	}
	s.record(blocked)
	if blocked.Status != "PASS" {
		return
	}
	s.expectAgentRunRows("doctor preflight blocker records no agent run", 0)
}

func (h *harness) caseBootstrapTopologyWorkflow() {
	s, err := h.newScenario("bootstrap topology")
	if err != nil {
		h.record(result{Name: "bootstrap topology workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	workspace := filepath.Join(s.fixtures.Root, "fresh-workspace")
	manifest := filepath.Join(s.fixtures.Root, "manifest")
	alphaPath := filepath.Join(workspace, "tools", "alpha")
	betaPath := filepath.Join(workspace, "beta")
	if err := writeE2EManifestEntry(manifest, "alpha.json", "https://example.invalid/bram/alpha", "alpha", "tools/alpha"); err != nil {
		h.record(result{Name: "bootstrap topology manifest alpha", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := writeE2EManifestEntry(manifest, "beta.json", "https://example.invalid/bram/beta", "beta", "beta"); err != nil {
		h.record(result{Name: "bootstrap topology manifest beta", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if init := s.command("bootstrap topology init", "init", workspace); init.Status != "PASS" {
		return
	}
	if register := s.command("bootstrap topology machine register", "machine", "register", workspace); register.Status != "PASS" {
		return
	}

	plan := s.command("bootstrap topology plan", "bootstrap", manifest)
	if plan.Status != "PASS" || !s.expectOutput(plan, "bootstrap plan", "workspace_root: "+workspace, "blocked: false", "missing: alpha "+alphaPath, "missing: beta "+betaPath) {
		return
	}
	if !s.expectPathMissing("bootstrap topology dry-run workspace missing", workspace) {
		return
	}
	if !s.expectProjectRowCountRaw("bootstrap topology dry-run rows", 0) {
		return
	}

	apply := s.command("bootstrap topology apply", "bootstrap", manifest, "--apply")
	if apply.Status != "PASS" || !s.expectOutput(apply, "bootstrap plan", "applied", "parent: "+workspace, "parent: "+filepath.Join(workspace, "tools"), "added: alpha "+alphaPath, "added: beta "+betaPath) {
		return
	}
	if !s.expectPathExists("bootstrap topology parent exists", filepath.Join(workspace, "tools")) {
		return
	}
	if !s.expectPathMissing("bootstrap topology alpha remains missing", alphaPath) || !s.expectPathMissing("bootstrap topology beta remains missing", betaPath) {
		return
	}
	if !s.expectProjectRowsRaw("bootstrap topology rows",
		projectRow{Alias: "alpha", NormalizedRemote: "https://example.invalid/bram/alpha", CloneURL: "https://example.invalid/bram/alpha.git", LocalPath: alphaPath},
		projectRow{Alias: "beta", NormalizedRemote: "https://example.invalid/bram/beta", CloneURL: "https://example.invalid/bram/beta.git", LocalPath: betaPath},
	) {
		return
	}
	tree := s.command("bootstrap topology tree missing projects", "tree")
	if tree.Status != "PASS" || !s.expectOutput(tree, "alpha missing "+alphaPath, "beta missing "+betaPath) {
		return
	}
	status := s.command("bootstrap topology status missing project", "status", "alpha", "--json")
	if status.Status != "PASS" || !s.expectOutput(status, `"exit_class":"readiness-blocked"`, `"state":"missing"`, `"path_present":false`) {
		return
	}
	bindTargetEnv := s.command("workspace target bind fake env", "env", "bind", "alpha", "CODEMESH_E2E_TARGET_TOKEN", "--provider", "fake", "--ref", "fake://e2e-target-token", "--scope", "codex")
	targetFakeValue := envbinding.FakeProviderValue("fake://e2e-target-token")
	if bindTargetEnv.Status != "PASS" || !s.expectOutput(bindTargetEnv, "bound env requirement: CODEMESH_E2E_TARGET_TOKEN", "provider: fake", "scopes: codex") || !s.expectNoOutput(bindTargetEnv, targetFakeValue) {
		return
	}
	targetExport := s.command("workspace target export local fake", "target", "export", "local-fake-target", "--kind", "agent", "--scope", "codex", "--json")
	if targetExport.Status != "PASS" {
		return
	}
	if err := targetExportJSONMatches(targetExport.Stdout, "local-fake-target", "agent", workspace, "alpha", "tools/alpha", "CODEMESH_E2E_TARGET_TOKEN", "fake://e2e-target-token"); err != nil {
		targetExport.Status = "FAIL"
		targetExport.Error = err.Error()
		s.updateResult(targetExport)
		return
	}
	if !s.expectNoOutput(targetExport, targetFakeValue, "agent_run", "dirty-checkout", "stale") {
		return
	}
	if !s.expectAgentRunRows("workspace target export records no agent run", 0) {
		return
	}

	conflictScenario, err := h.newScenario("bootstrap conflict")
	if err != nil {
		h.record(result{Name: "bootstrap conflict workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	conflictWorkspace := filepath.Join(conflictScenario.fixtures.Root, "workspace")
	conflictManifest := filepath.Join(conflictScenario.fixtures.Root, "manifest")
	conflictPath := filepath.Join(conflictWorkspace, "tools", "alpha")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		h.record(result{Name: "bootstrap conflict path setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	marker := filepath.Join(conflictPath, "local.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		h.record(result{Name: "bootstrap conflict marker setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := writeE2EManifestEntry(conflictManifest, "alpha.json", "https://example.invalid/bram/conflict-alpha", "alpha", "tools/alpha"); err != nil {
		h.record(result{Name: "bootstrap conflict manifest", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if init := conflictScenario.command("bootstrap conflict init", "init", conflictWorkspace); init.Status != "PASS" {
		return
	}
	if register := conflictScenario.command("bootstrap conflict machine register", "machine", "register", conflictWorkspace); register.Status != "PASS" {
		return
	}
	conflict := conflictScenario.expectedFailure("bootstrap path conflict refusal", "bootstrap", conflictManifest, "--apply", "--json")
	if conflict.Status != "FAIL" {
		conflict.Status = "FAIL"
		conflict.Error = "bootstrap conflict unexpectedly passed"
	} else if conflict.ExitCode != 1 {
		conflict.Error = fmt.Sprintf("bootstrap conflict exit code = %d, want 1", conflict.ExitCode)
	} else if conflict.Stderr != "" {
		conflict.Error = "bootstrap conflict wrote stderr: " + conflict.Stderr
	} else if !strings.Contains(conflict.Stdout, `"exit_class":"readiness-blocked"`) || !strings.Contains(conflict.Stdout, `"kind":"path-conflict"`) || !strings.Contains(conflict.Stdout, conflictPath) {
		conflict.Error = "bootstrap conflict JSON did not report path-conflict blocker"
	} else if got, err := os.ReadFile(marker); err != nil || string(got) != "keep\n" {
		conflict.Error = fmt.Sprintf("bootstrap conflict marker changed or missing: got %q err %v", got, err)
	} else {
		conflict.Status = "PASS"
		conflict.Error = ""
	}
	conflictScenario.record(conflict)
	if conflict.Status != "PASS" {
		return
	}
	conflictScenario.expectProjectRowCountRaw("bootstrap conflict rows unchanged", 0)
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
	if h.mode == modeLive && liveReportContainsForbiddenPaths(reportData, h.forbiddenHostPathMarkers()) {
		h.record(result{Name: "live github report path isolation", Status: "FAIL", Error: "host workspace or normal CodeMesh home path appeared in live report outputs or isolation metadata", ExitCode: -1})
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

func containsAnyFragment(value string, fragments []string) bool {
	for _, fragment := range fragments {
		if fragment != "" && strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func liveReportContainsForbiddenPaths(data []byte, forbidden []string) bool {
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return containsAnyFragment(string(data), forbidden)
	}
	values := []string{
		r.Isolation.CodeMeshHome,
		r.Isolation.Home,
		r.Isolation.Workspace,
		r.Isolation.RunDir,
		r.Isolation.GitConfig,
	}
	for _, result := range r.Results {
		values = append(values, result.Stdout, result.Stderr, result.Error)
	}
	if r.Live != nil {
		values = append(values, r.Live.SkipReasons...)
		if r.Live.GitHub != nil {
			values = append(values, r.Live.GitHub.RemoteURL, r.Live.GitHub.DefaultBranch, r.Live.GitHub.SecretSafety)
		}
	}
	for _, value := range values {
		if containsAnyFragment(value, forbidden) {
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
	if scan.Status != "PASS" || !s.expectProjectRowCount("negative cli baseline state rows", 9) {
		return
	}

	unknownProject := s.expectedFailure("negative cli unknown project", "status", "ghost-project")
	if !s.expectFailure(unknownProject, 1, "unknown project: ghost-project") {
		return
	}
	if !s.expectProjectRowCount("negative cli unknown project state unchanged", 9) {
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
	if !s.expectProjectRowCount("negative cli hydrate conflict state unchanged", 10) {
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
	hydrateJSON := s.command("hydrate missing fixture json", "hydrate", "hydrate-other", "--json")
	if hydrateJSON.Status != "PASS" || !s.expectHydrateJSON(hydrateJSON, "success", "hydrate-other", "hydrated", other.Source, true, nil) {
		return
	}
	noopJSON := s.command("hydrate already present fixture json", "hydrate", "hydrate-other", "--json")
	if noopJSON.Status != "PASS" || !s.expectHydrateJSON(noopJSON, "success", "hydrate-other", "already-present", other.Source, true, nil) {
		return
	}

	conflictResult := s.expectedFailure("hydrate path conflict refusal", "hydrate", "hydrate-conflict", "--json")
	if conflictResult.Status != "FAIL" {
		conflictResult.Status = "FAIL"
		conflictResult.Error = "path conflict hydrate unexpectedly passed"
	} else if conflictResult.ExitCode != 1 {
		conflictResult.Error = fmt.Sprintf("path conflict exit code = %d, want 1", conflictResult.ExitCode)
	} else if conflictResult.Stderr != "" {
		conflictResult.Error = "path conflict hydrate wrote stderr: " + conflictResult.Stderr
	} else if !s.expectHydrateJSON(conflictResult, "readiness-blocked", "hydrate-conflict", "path-conflict", conflict.Source, true, []string{"path-conflict"}) {
		return
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
	if after.Status != "PASS" || !s.expectOutput(after, "hydrate-target present", "hydrate-other present", "hydrate-conflict blocked") {
		return
	}

	status := s.command("hydration status after", "status", "hydrate-target", "--base", "main")
	s.expectOutput(status, "state: present", "path_present: true")
}

func (h *harness) caseTwoMachineManifestBootstrapReconcileSmoke() {
	caseRoot := filepath.Join(h.tmp, "two-machine")
	fixtures := offlineGitFixtures{
		Root:    filepath.Join(caseRoot, "git-fixtures"),
		Remotes: filepath.Join(caseRoot, "git-fixtures", "remotes"),
		Sources: filepath.Join(caseRoot, "git-fixtures", "sources"),
	}
	if err := os.MkdirAll(fixtures.Remotes, 0o755); err != nil {
		h.record(result{Name: "two-machine fixture setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	target, err := h.createRemoteOnlyFixture(fixtures, "mesh-target", nil)
	if err != nil {
		h.record(result{Name: "two-machine target remote setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	unrelated, err := h.createRemoteOnlyFixture(fixtures, "mesh-unrelated", nil)
	if err != nil {
		h.record(result{Name: "two-machine unrelated remote setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}

	remoteBase, stopGitDaemon, err := h.startLocalGitDaemon(fixtures.Remotes, filepath.Base(target.Remote))
	if err != nil {
		h.record(result{Name: "two-machine local git daemon", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	defer stopGitDaemon()
	targetRemote := remoteBase + "/" + filepath.Base(target.Remote)
	unrelatedRemote := remoteBase + "/" + filepath.Base(unrelated.Remote)

	machineA, err := h.newTwoMachineNode(caseRoot, "machine-a")
	if err != nil {
		h.record(result{Name: "two-machine machine A setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	machineB, err := h.newTwoMachineNode(caseRoot, "machine-b")
	if err != nil {
		h.record(result{Name: "two-machine machine B setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := os.MkdirAll(filepath.Join(machineA.WorkspaceRoot, "projects"), 0o755); err != nil {
		h.record(result{Name: "two-machine machine A workspace setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	targetSourceA := filepath.Join(machineA.WorkspaceRoot, "projects", "mesh-target")
	unrelatedSourceA := filepath.Join(machineA.WorkspaceRoot, "projects", "mesh-unrelated")
	targetPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "mesh-target")
	unrelatedPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "mesh-unrelated")

	if clone := h.twoMachineCommand("two-machine machine A clone selected source", machineA, "git", "clone", targetRemote, targetSourceA); clone.Status != "PASS" {
		return
	}
	if clone := h.twoMachineCommand("two-machine machine A clone unrelated source", machineA, "git", "clone", unrelatedRemote, unrelatedSourceA); clone.Status != "PASS" {
		return
	}
	if init := h.twoMachineCommand("two-machine machine A init", machineA, h.bin, "init", machineA.WorkspaceRoot); init.Status != "PASS" {
		return
	}
	registerA := h.twoMachineCommand("two-machine machine A register", machineA, h.bin, "machine", "register", machineA.WorkspaceRoot, "--json")
	machineAID, ok := h.expectMachineRegisterJSON("two-machine machine A id", registerA, machineA.WorkspaceRoot)
	if !ok {
		return
	}
	if add := h.twoMachineCommand("two-machine machine A add selected project", machineA, h.bin, "add", targetSourceA, "--alias", "mesh-target"); add.Status != "PASS" {
		return
	}
	if add := h.twoMachineCommand("two-machine machine A add unrelated project", machineA, h.bin, "add", unrelatedSourceA, "--alias", "mesh-unrelated"); add.Status != "PASS" {
		return
	}
	rowsA, err := readProjectRowsFromStore(filepath.Join(machineA.CodeMeshHome, "codemesh.db"))
	if err != nil {
		h.record(result{Name: "two-machine machine A manifest source rows", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	targetRowA, ok := projectRowByAlias(rowsA, "mesh-target")
	if !ok {
		h.record(result{Name: "two-machine machine A manifest source rows", Status: "FAIL", Error: "mesh-target row missing", ExitCode: -1})
		return
	}
	unrelatedRowA, ok := projectRowByAlias(rowsA, "mesh-unrelated")
	if !ok {
		h.record(result{Name: "two-machine machine A manifest source rows", Status: "FAIL", Error: "mesh-unrelated row missing", ExitCode: -1})
		return
	}
	if targetRowA.CloneURL != targetRemote || unrelatedRowA.CloneURL != unrelatedRemote {
		h.record(result{Name: "two-machine machine A manifest source rows", Status: "FAIL", Error: fmt.Sprintf("clone URLs = %q %q", targetRowA.CloneURL, unrelatedRowA.CloneURL), ExitCode: -1})
		return
	}

	manifest := filepath.Join(caseRoot, "manifest")
	targetDesiredPath := "projects/mesh-target"
	unrelatedDesiredPath := "projects/mesh-unrelated"
	targetManifest := filepath.Join(manifest, "mesh-target.json")
	if err := writeE2EManifestEntryWithCloneURL(manifest, "mesh-target.json", targetRowA.NormalizedRemote, "mesh-target", targetDesiredPath, targetRowA.CloneURL); err != nil {
		h.record(result{Name: "two-machine selected manifest entry", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := writeE2EManifestEntryWithCloneURL(manifest, "mesh-unrelated.json", unrelatedRowA.NormalizedRemote, "mesh-unrelated", unrelatedDesiredPath, unrelatedRowA.CloneURL); err != nil {
		h.record(result{Name: "two-machine unrelated manifest entry", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if !h.expectManifestEntry("two-machine machine A selected manifest state", targetManifest, targetRowA.NormalizedRemote, "mesh-target", targetDesiredPath, targetRowA.CloneURL) {
		return
	}

	if init := h.twoMachineCommand("two-machine machine B init", machineB, h.bin, "init", machineB.WorkspaceRoot); init.Status != "PASS" {
		return
	}
	registerB := h.twoMachineCommand("two-machine machine B register", machineB, h.bin, "machine", "register", machineB.WorkspaceRoot, "--json")
	machineBID, ok := h.expectMachineRegisterJSON("two-machine machine B id", registerB, machineB.WorkspaceRoot)
	if !ok {
		return
	}
	if machineAID == machineBID {
		h.record(result{Name: "two-machine distinct machine identities", Status: "FAIL", Error: "machine IDs matched across isolated homes", ExitCode: -1})
		return
	}
	h.record(result{Name: "two-machine distinct machine identities", Status: "PASS", ExitCode: 0})

	plan := h.twoMachineCommand("two-machine bootstrap dry-run plan", machineB, h.bin, "bootstrap", manifest)
	if plan.Status != "PASS" || !resultContainsAll(plan, "bootstrap plan", "apply: false", "blocked: false", "missing: mesh-target "+targetPathB, "missing: mesh-unrelated "+unrelatedPathB) {
		if plan.Status == "PASS" {
			plan.Status = "FAIL"
			plan.Error = "bootstrap dry-run did not report both missing projects"
			h.updateResultByName(plan)
		}
		return
	}
	if !h.expectProjectRowsAt("two-machine bootstrap dry-run leaves registry empty", machineB.CodeMeshHome) {
		return
	}
	if !h.expectPathMissingResult("two-machine bootstrap dry-run no selected checkout", targetPathB) || !h.expectPathMissingResult("two-machine bootstrap dry-run no unrelated checkout", unrelatedPathB) {
		return
	}

	apply := h.twoMachineCommand("two-machine bootstrap apply topology", machineB, h.bin, "bootstrap", manifest, "--apply")
	if apply.Status != "PASS" || !resultContainsAll(apply, "apply: true", "applied", "added: mesh-target "+targetPathB, "added: mesh-unrelated "+unrelatedPathB) {
		if apply.Status == "PASS" {
			apply.Status = "FAIL"
			apply.Error = "bootstrap apply did not report both registry additions"
			h.updateResultByName(apply)
		}
		return
	}
	if !h.expectProjectRowsAt("two-machine machine B bootstrapped registry", machineB.CodeMeshHome,
		projectRow{Alias: "mesh-target", NormalizedRemote: targetRowA.NormalizedRemote, CloneURL: targetRemote, LocalPath: targetPathB},
		projectRow{Alias: "mesh-unrelated", NormalizedRemote: unrelatedRowA.NormalizedRemote, CloneURL: unrelatedRemote, LocalPath: unrelatedPathB},
	) {
		return
	}
	if !h.expectPathMissingResult("two-machine bootstrap apply no selected checkout", targetPathB) || !h.expectPathMissingResult("two-machine bootstrap apply no unrelated checkout", unrelatedPathB) {
		return
	}

	hydrate := h.twoMachineCommand("two-machine hydrate selected project", machineB, h.bin, "hydrate", "mesh-target", "--json")
	if hydrate.Status != "PASS" || !h.expectTwoMachineHydrateJSON("two-machine hydrate selected project metadata", hydrate, "mesh-target", targetRowA.NormalizedRemote, targetPathB) {
		return
	}
	if !h.expectGitCheckoutAtBase("two-machine hydrated checkout branch", targetPathB, "main") {
		return
	}
	if !h.expectGitOrigin("two-machine hydrated checkout origin", targetPathB, targetRemote) {
		return
	}
	if !h.expectPathMissingResult("two-machine selected hydrate no unrelated checkout", unrelatedPathB) {
		return
	}
	if inside, err := pathInside(machineA.WorkspaceRoot, targetPathB); err != nil || inside || targetPathB == targetSourceA {
		h.record(result{Name: "two-machine no source checkout reuse", Status: "FAIL", Error: fmt.Sprintf("machine B target path reused machine A source: inside=%t err=%v", inside, err), ExitCode: -1})
		return
	}
	h.record(result{Name: "two-machine no source checkout reuse", Status: "PASS", ExitCode: 0})

	driftManifest := filepath.Join(caseRoot, "manifest-drift")
	movedDesiredPath := "projects/mesh-target-moved"
	movedPathB := filepath.Join(machineB.WorkspaceRoot, "projects", "mesh-target-moved")
	if err := writeE2EManifestEntryWithCloneURL(driftManifest, "mesh-target.json", targetRowA.NormalizedRemote, "mesh-target", movedDesiredPath, targetRowA.CloneURL); err != nil {
		h.record(result{Name: "two-machine drift manifest selected", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if err := writeE2EManifestEntryWithCloneURL(driftManifest, "mesh-unrelated.json", unrelatedRowA.NormalizedRemote, "mesh-unrelated", unrelatedDesiredPath, unrelatedRowA.CloneURL); err != nil {
		h.record(result{Name: "two-machine drift manifest unrelated", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	drift := h.twoMachineCommand("two-machine reconcile dry-run moved drift", machineB, h.bin, "bootstrap", driftManifest, "--json")
	if drift.Status != "PASS" || !resultContainsAll(drift, `"command":"bootstrap"`, `"apply":false`, `"kind":"moved"`, `"alias":"mesh-target"`, movedPathB) {
		if drift.Status == "PASS" {
			drift.Status = "FAIL"
			drift.Error = "reconcile dry-run did not report moved drift before mutation"
			h.updateResultByName(drift)
		}
		return
	}
	if !h.expectProjectRowsAt("two-machine reconcile dry-run registry unchanged", machineB.CodeMeshHome,
		projectRow{Alias: "mesh-target", NormalizedRemote: targetRowA.NormalizedRemote, CloneURL: targetRemote, LocalPath: targetPathB},
		projectRow{Alias: "mesh-unrelated", NormalizedRemote: unrelatedRowA.NormalizedRemote, CloneURL: unrelatedRemote, LocalPath: unrelatedPathB},
	) {
		return
	}
	if !h.expectPathMissingResult("two-machine reconcile dry-run no moved checkout", movedPathB) {
		return
	}

	h.twoMachine = &reportTwoMachine{
		MachineAID:        machineAID,
		MachineBID:        machineBID,
		ManifestLocation:  manifest,
		HydratedProjectID: targetRowA.NormalizedRemote,
		HydrationProvenance: reportHydrationProvenance{
			Remote:      targetRowA.NormalizedRemote,
			Base:        "main",
			DesiredPath: targetDesiredPath,
			MachineID:   machineBID,
		},
		DriftSummary:  "moved: mesh-target " + targetPathB + " -> " + movedPathB,
		CleanupStatus: "scheduled: harness temp cleanup",
	}
	h.record(result{Name: "two-machine smoke report", Status: "PASS", ExitCode: 0})
}

func (h *harness) caseAgentPrepFixtureWorkflow() {
	s, err := h.newScenario("agent prep")
	if err != nil {
		h.record(result{Name: "agent prep fixture workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	toolchainProject, err := h.createClonedFixtureWithSeed(s.fixtures, "agent-toolchain", writeToolchainPolicy, nil)
	if err != nil {
		h.record(result{Name: "agent prep toolchain setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	s.fixtures.Projects = append(s.fixtures.Projects, toolchainProject)
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
	if runs.Status != "PASS" || !s.expectOutput(runs, "project=clean-repo", "base=main", "profile=codex", "state=prepared", "workspace="+readyPath) {
		return
	}
	runID := firstRunID(runs.Stdout)
	if runID == "" {
		s.h.record(result{Name: "agent runs prepared id", Status: "FAIL", Error: "runs output did not include a run id", ExitCode: -1})
		return
	}

	agentRun := s.command("agent run harmless command", "agent", "run", runID, "--label", "workspace root", "--", "git", "rev-parse", "--show-toplevel")
	if agentRun.Status != "PASS" || !s.expectOutput(agentRun, "agent command complete", "run: "+runID, "label: workspace root", "exit_code: 0", "stdout_path: ", "stderr_path: ") {
		return
	}
	if !s.expectCommandStdoutEqualsCanonicalPath("agent run stdout path", valueAfterPrefix(agentRun.Stdout, "stdout_path: "), readyPath) {
		return
	}
	if !s.expectAgentRunCommandContract("agent run command metadata", readyPath, "workspace root") {
		return
	}

	runsExecuted := s.command("agent runs list executed fixture", "runs")
	if runsExecuted.Status != "PASS" || !s.expectOutput(runsExecuted, "project=clean-repo", "state=executed", "workspace="+readyPath) {
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

	jsonPrepare := s.command("agent prep clean fixture json", "agent", "prepare", "clean-repo", "--base", "main", "--profile", "codex", "--json")
	if jsonPrepare.Status != "PASS" || !s.expectAgentPrepareJSON(jsonPrepare, "success", "clean-repo", true, "main", "codex", 4, nil, nil) {
		return
	}

	toolchainPrep := s.command("agent prep toolchain contract", "agent", "prepare", "agent-toolchain", "--base", "main")
	if toolchainPrep.Status != "PASS" || !s.expectOutput(toolchainPrep, "warnings: none", "blockers: none", "ready_path: ") {
		return
	}
	toolchainPath := s.expectReadyPath("agent prep toolchain ready path", toolchainPrep)
	if toolchainPath == "" || !s.expectAgentRunToolchain("agent prep toolchain metadata", toolchainPath, "go", "present") {
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

	remoteDefault := s.command("agent prep remote default base", "agent", "prepare", "remote-default-dev", "--profile", "codex")
	if remoteDefault.Status != "PASS" || !s.expectOutput(remoteDefault, "base: develop", "blockers: none", "ready_path: ") {
		return
	}
	remoteDefaultPath := s.expectReadyPath("agent prep remote default ready path", remoteDefault)
	if remoteDefaultPath == "" {
		return
	}
	if !s.expectGitCheckoutAtBase("agent prep remote default checkout base", remoteDefaultPath, "develop") {
		return
	}
	if !s.expectPathExists("agent prep remote default branch file", filepath.Join(remoteDefaultPath, "develop.txt")) {
		return
	}
	if !s.expectAgentRunMetadata("agent prep remote default metadata", remoteDefaultPath, "remote-default-dev", "develop", "codex") {
		return
	}

	missingSource, err := h.createClonedFixtureWithSeed(s.fixtures, "agent-prep-missing-source", func(seed string) error {
		policy := []byte("agent:\n  env:\n    mode: warn\n    required_files:\n      - .env.agent\n")
		return os.WriteFile(filepath.Join(seed, ".codemesh.yml"), policy, 0o644)
	}, nil)
	if err != nil {
		h.record(result{Name: "agent prep missing source setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if missingSource.Source, err = filepath.EvalSymlinks(missingSource.Source); err != nil {
		h.record(result{Name: "agent prep missing source canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("agent prep add missing source project", "add", missingSource.Source); add.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(missingSource.Source); err != nil {
		h.record(result{Name: "agent prep remove source checkout", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	missingSourcePrep := s.command("agent prep missing source checkout", "agent", "prepare", missingSource.Name, "--base", "main", "--profile", "codex")
	if missingSourcePrep.Status != "PASS" || !s.expectOutput(missingSourcePrep, "warning: missing-env-file", "blockers: none", "ready_path: ") {
		return
	}
	missingSourcePath := s.expectReadyPath("agent prep missing source ready path", missingSourcePrep)
	if missingSourcePath == "" {
		return
	}
	if !s.expectGitCheckoutAtBase("agent prep missing source checkout base", missingSourcePath, "main") {
		return
	}
	if !s.expectPathMissing("agent prep missing source did not create placeholder", missingSource.Source) {
		return
	}
	if !s.expectAgentRunSourcePathMissing("agent prep missing source contract", missingSourcePath, missingSource.Source, true) {
		return
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
	envBlockedJSON := s.expectedFailure("agent prep env blocker json", "agent", "prepare", "required-env-missing", "--json")
	if envBlockedJSON.Status != "FAIL" {
		envBlockedJSON.Status = "FAIL"
		envBlockedJSON.Error = "env-blocked json prep unexpectedly passed"
	} else if envBlockedJSON.ExitCode != 1 {
		envBlockedJSON.Error = fmt.Sprintf("env-blocked json exit code = %d, want 1", envBlockedJSON.ExitCode)
	} else if envBlockedJSON.Stderr != "" {
		envBlockedJSON.Error = "env-blocked json wrote stderr: " + envBlockedJSON.Stderr
	} else if !s.expectAgentPrepareJSON(envBlockedJSON, "readiness-blocked", "required-env-missing", false, "main", "", 0, nil, []string{"missing-env-file", "missing-env-key"}) {
		return
	} else if strings.Contains(envBlockedJSON.Stdout, "=") || containsAnySecret(envBlockedJSON.Stdout, fakeEnvFixtureSecrets()) {
		envBlockedJSON.Error = "env-blocked json included env values"
	} else {
		envBlockedJSON.Status = "PASS"
		envBlockedJSON.Error = ""
	}
	s.record(envBlockedJSON)
	if envBlockedJSON.Status != "PASS" {
		return
	}

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

	bindEnv := s.command("agent prep bind fake env", "env", "bind", "required-env-bound", "CODEMESH_E2E_BOUND_ENV", "--provider", "fake", "--ref", "fake://e2e-bound-env", "--scope", "codex")
	fakeBoundValue := fakeEnvBindingSecret()
	if bindEnv.Status != "PASS" || !s.expectOutput(bindEnv, "bound env requirement: CODEMESH_E2E_BOUND_ENV", "provider: fake", "scopes: codex") || !s.expectNoOutput(bindEnv, fakeBoundValue) {
		return
	}
	scopeDenied := s.expectedFailure("agent prep env binding scope denied", "agent", "prepare", "required-env-bound", "--base", "main", "--env-provider", "fake", "--allow-env-scope", "readonly")
	if scopeDenied.Status != "FAIL" {
		scopeDenied.Status = "FAIL"
		scopeDenied.Error = "scope-denied prep unexpectedly passed"
	} else if !strings.Contains(scopeDenied.Stderr, "blocker: env-scope-denied") || !strings.Contains(scopeDenied.Stderr, "CODEMESH_E2E_BOUND_ENV") || !strings.Contains(scopeDenied.Stderr, "readonly") || containsAnySecret(scopeDenied.Stderr, fakeEnvFixtureSecrets()) {
		scopeDenied.Error = "scope-denied prep did not report actionable diagnostics without values"
	} else {
		scopeDenied.Status = "PASS"
		scopeDenied.Error = ""
	}
	s.record(scopeDenied)
	if scopeDenied.Status != "PASS" {
		return
	}
	boundEnv := s.command("agent prep env binding fake provider", "agent", "prepare", "required-env-bound", "--base", "main", "--env-provider", "fake", "--allow-env-scope", "codex")
	if boundEnv.Status != "PASS" || !s.expectOutput(boundEnv, "env_materialization: materialized", "env_bundle: present", "env_bundle_path: ", "ready_path: ") || !s.expectNoOutput(boundEnv, fakeBoundValue) {
		return
	}
	boundPath := s.expectReadyPath("agent prep env binding ready path", boundEnv)
	if boundPath == "" {
		return
	}
	if !s.expectAgentRunEnvMaterialization("agent prep env binding metadata", boundPath, "CODEMESH_E2E_BOUND_ENV", fakeBoundValue) {
		return
	}
	boundBundle := valueAfterPrefix(boundEnv.Stdout, "env_bundle_path: ")
	if boundBundle == "" {
		s.h.record(result{Name: "agent prep env binding bundle path", Status: "FAIL", Error: "env bundle path missing from output", ExitCode: -1})
		return
	}
	if !s.expectPathExists("agent prep env binding bundle exists", boundBundle) {
		return
	}
	if !s.expectPathMissing("agent prep env binding bundle outside checkout", filepath.Join(boundPath, "env", "env.bundle")) {
		return
	}
	cleanBound := s.command("agent prep env binding cleanup", "clean", "--older-than", "0d")
	if cleanBound.Status != "PASS" || !s.expectOutput(cleanBound, "deleted: ") {
		return
	}
	if !s.expectPathMissing("agent prep env binding cleanup removes bundle", boundBundle) {
		return
	}
	if !s.expectPathMissing("agent prep env binding cleanup removes workspace", boundPath) {
		return
	}
}

func (h *harness) caseCLIContractSnapshotWorkflow() {
	s, err := h.newScenarioWithFixtureRoot("cli contract snapshots", filepath.Join(h.tmp, "contract-git-fixtures"))
	if err != nil {
		h.record(result{Name: "cli contract snapshot workflow", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	scan := s.command("cli contract scan fixtures", "scan", s.fixtures.Sources)
	if scan.Status != "PASS" {
		return
	}

	if !s.contractJSON("tree-scanned-workspace", "tree", "--json") {
		return
	}
	if !s.contractJSON("status-ready", "status", "clean-repo", "--base", "main", "--json") {
		return
	}
	if !s.contractJSON("status-warning", "status", "dirty-source", "--base", "main", "--json") {
		return
	}
	missingProject, err := h.createClonedFixture(s.fixtures, "contract-missing-project", nil)
	if err != nil {
		h.record(result{Name: "cli contract missing project setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if missingProject.Source, err = filepath.EvalSymlinks(missingProject.Source); err != nil {
		h.record(result{Name: "cli contract missing project canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("cli contract add missing project", "add", missingProject.Source); add.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(missingProject.Source); err != nil {
		h.record(result{Name: "cli contract remove missing project", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if !s.contractJSON("status-missing-project", "status", missingProject.Name, "--base", "main", "--json") {
		return
	}

	fetchFailure, err := h.createClonedFixture(s.fixtures, "contract-fetch-failure", func(source string) error {
		return runGitNoOutput(source, "remote", "set-url", "origin", filepath.Join(s.fixtures.Remotes, "missing-contract-fetch-remote.git"))
	})
	if err != nil {
		h.record(result{Name: "cli contract fetch failure setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("cli contract add stale project", "add", fetchFailure.Source); add.Status != "PASS" {
		return
	}
	if !s.contractJSON("status-stale", "status", fetchFailure.Name, "--base", "main", "--json") {
		return
	}

	invalidPolicy, err := h.createClonedFixtureWithSeed(s.fixtures, "contract-invalid-policy", func(seed string) error {
		return os.WriteFile(filepath.Join(seed, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: stop\n"), 0o644)
	}, nil)
	if err != nil {
		h.record(result{Name: "cli contract invalid policy setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("cli contract add invalid policy project", "add", invalidPolicy.Source); add.Status != "PASS" {
		return
	}
	if !s.contractJSON("status-invalid-policy", "status", invalidPolicy.Name, "--base", "main", "--json") {
		return
	}

	hydrateTarget, err := h.createClonedFixture(s.fixtures, "contract-hydrate-target", nil)
	if err != nil {
		h.record(result{Name: "cli contract hydrate target setup", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if hydrateTarget.Source, err = filepath.EvalSymlinks(hydrateTarget.Source); err != nil {
		h.record(result{Name: "cli contract hydrate target canonical path", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if add := s.command("cli contract add hydrate target", "add", hydrateTarget.Source); add.Status != "PASS" {
		return
	}
	if err := os.RemoveAll(hydrateTarget.Source); err != nil {
		h.record(result{Name: "cli contract remove hydrate target", Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return
	}
	if !s.contractJSON("hydrate-hydrated", "hydrate", hydrateTarget.Name, "--json") {
		return
	}
	if !s.contractJSON("hydrate-missing-project", "hydrate", "ghost-project", "--json") {
		return
	}

	if !s.contractJSON("agent-prepare-ready", "agent", "prepare", "clean-repo", "--base", "main", "--profile", "codex", "--json") {
		return
	}
	if !s.contractJSON("agent-prepare-blocked", "agent", "prepare", "required-env-missing", "--json") {
		return
	}
	if !s.contractNonJSON("command-misuse", "hydrate", "--json") {
		return
	}
}

type cliContractSnapshot struct {
	Case          string          `json:"case"`
	Args          []string        `json:"args"`
	Process       contractProcess `json:"process"`
	StdoutJSON    any             `json:"stdout_json,omitempty"`
	StdoutPresent bool            `json:"stdout_present,omitempty"`
	StderrPresent bool            `json:"stderr_present"`
}

type contractProcess struct {
	ExitCode  int    `json:"exit_code"`
	ExitClass string `json:"exit_class"`
}

type contractReplacement struct {
	From string
	To   string
}

func (s *scenario) contractJSON(caseName string, args ...string) bool {
	r := s.execute("cli contract "+caseName, nil, args...)
	return s.expectContractSnapshot(caseName, r, args, true)
}

func (s *scenario) contractNonJSON(caseName string, args ...string) bool {
	r := s.execute("cli contract "+caseName, nil, args...)
	return s.expectContractSnapshot(caseName, r, args, false)
}

func (s *scenario) expectContractSnapshot(caseName string, r result, args []string, stdoutJSON bool) bool {
	stdoutPath, stderrPath, err := s.writeContractDebug(caseName, r)
	if err != nil {
		r.Status = "FAIL"
		r.Error = err.Error()
		s.record(r)
		return false
	}

	actual, err := s.normalizedContractSnapshot(caseName, r, args, stdoutJSON)
	if err != nil {
		r.Status = "FAIL"
		r.Error = fmt.Sprintf("%v\nstdout_path: %s\nstderr_path: %s", err, stdoutPath, stderrPath)
		s.record(r)
		return false
	}
	expectedPath := filepath.Join(s.h.root, "test", "e2e", "snapshots", caseName+".json")
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		if contractSnapshotsUpdateEnabled() {
			if writeErr := writeContractSnapshot(expectedPath, actual); writeErr != nil {
				r.Status = "FAIL"
				r.Error = fmt.Sprintf("write contract snapshot %s: %v\nstdout_path: %s\nstderr_path: %s", expectedPath, writeErr, stdoutPath, stderrPath)
				s.record(r)
				return false
			}
			r.Status = "PASS"
			r.Error = ""
			r.Stdout = ""
			r.Stderr = ""
			s.record(r)
			return true
		}
		r.Status = "FAIL"
		r.Error = fmt.Sprintf("read contract snapshot %s: %v\nstdout_path: %s\nstderr_path: %s", expectedPath, err, stdoutPath, stderrPath)
		s.record(r)
		return false
	}
	expectedText := strings.TrimSpace(string(expected))
	actualText := strings.TrimSpace(string(actual))
	if expectedText != actualText {
		if contractSnapshotsUpdateEnabled() {
			if writeErr := writeContractSnapshot(expectedPath, actual); writeErr != nil {
				r.Status = "FAIL"
				r.Error = fmt.Sprintf("write contract snapshot %s: %v\nstdout_path: %s\nstderr_path: %s", expectedPath, writeErr, stdoutPath, stderrPath)
				s.record(r)
				return false
			}
			r.Status = "PASS"
			r.Error = ""
			r.Stdout = ""
			r.Stderr = ""
			s.record(r)
			return true
		}
		r.Status = "FAIL"
		r.Error = fmt.Sprintf("normalized JSON contract mismatch for %s:\n%s\nstdout_path: %s\nstderr_path: %s", caseName, normalizedJSONDiff(expectedText, actualText), stdoutPath, stderrPath)
		s.record(r)
		return false
	}
	r.Status = "PASS"
	r.Error = ""
	r.Stdout = ""
	r.Stderr = ""
	s.record(r)
	return true
}

func contractSnapshotsUpdateEnabled() bool {
	return os.Getenv("CODEMESH_E2E_UPDATE_CONTRACTS") == "1"
}

func writeContractSnapshot(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *scenario) writeContractDebug(caseName string, r result) (string, string, error) {
	dir := filepath.Join(s.h.tmp, "contract-debug", slug(caseName))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	stdoutPath := filepath.Join(dir, "stdout.txt")
	stderrPath := filepath.Join(dir, "stderr.txt")
	if err := os.WriteFile(stdoutPath, []byte(r.Stdout), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(stderrPath, []byte(r.Stderr), 0o644); err != nil {
		return "", "", err
	}
	return stdoutPath, stderrPath, nil
}

func (s *scenario) normalizedContractSnapshot(caseName string, r result, args []string, stdoutJSON bool) ([]byte, error) {
	snapshot := cliContractSnapshot{
		Case: caseName,
		Args: append([]string(nil), args...),
		Process: contractProcess{
			ExitCode:  r.ExitCode,
			ExitClass: processExitClass(r.ExitCode),
		},
		StdoutPresent: strings.TrimSpace(r.Stdout) != "",
		StderrPresent: strings.TrimSpace(r.Stderr) != "",
	}
	if stdoutJSON {
		var raw any
		if err := json.Unmarshal([]byte(r.Stdout), &raw); err != nil {
			return nil, fmt.Errorf("stdout was not JSON: %w", err)
		}
		normalized := normalizeContractValue(raw, s.contractReplacements(), "")
		snapshot.StdoutJSON = normalized
		if exitClass, ok := contractExitClass(normalized); ok {
			snapshot.Process.ExitClass = exitClass
		}
	}
	return marshalContractSnapshot(snapshot)
}

func marshalContractSnapshot(snapshot cliContractSnapshot) ([]byte, error) {
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(snapshot); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func processExitClass(exitCode int) string {
	switch exitCode {
	case 0:
		return "success"
	case 2:
		return "usage-error"
	default:
		return "internal-error"
	}
}

func contractExitClass(value any) (string, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	exitClass, ok := object["exit_class"].(string)
	return exitClass, ok
}

func (s *scenario) contractReplacements() []contractReplacement {
	var replacements []contractReplacement
	replacements = appendContractReplacement(replacements, s.codemeshHome, "<CODEMESH_HOME>")
	replacements = appendContractReplacement(replacements, s.fixtures.Root, "<FIXTURE_ROOT>")
	replacements = appendContractReplacement(replacements, s.fixtures.Sources, "<FIXTURE_SOURCES>")
	replacements = appendContractReplacement(replacements, s.fixtures.Remotes, "<FIXTURE_REMOTES>")
	replacements = appendContractReplacement(replacements, s.h.workspace, "<WORKSPACE>")
	replacements = appendContractReplacement(replacements, s.h.home, "<HOME>")
	replacements = appendContractReplacement(replacements, s.h.tmp, "<E2E_TMP>")
	sort.SliceStable(replacements, func(i, j int) bool {
		return len(replacements[i].From) > len(replacements[j].From)
	})
	return replacements
}

func appendContractReplacement(replacements []contractReplacement, from, to string) []contractReplacement {
	if from == "" {
		return replacements
	}
	replacements = append(replacements, contractReplacement{From: from, To: to})
	if evaluated, err := filepath.EvalSymlinks(from); err == nil && evaluated != from {
		replacements = append(replacements, contractReplacement{From: evaluated, To: to})
	}
	return replacements
}

func normalizeContractValue(value any, replacements []contractReplacement, key string) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for k, v := range typed {
			normalized[k] = normalizeContractValue(v, replacements, k)
		}
		return normalized
	case []any:
		normalized := make([]any, len(typed))
		for i, v := range typed {
			normalized[i] = normalizeContractValue(v, replacements, key)
		}
		return normalized
	case string:
		return normalizeContractString(key, typed, replacements)
	default:
		return typed
	}
}

func normalizeContractString(key, value string, replacements []contractReplacement) string {
	if key == "run_id" {
		return "<RUN_ID>"
	}
	normalized := filepath.ToSlash(value)
	for _, replacement := range replacements {
		from := filepath.ToSlash(replacement.From)
		if from != "" {
			normalized = strings.ReplaceAll(normalized, from, replacement.To)
		}
	}
	normalized = contractAgentRunPathRE.ReplaceAllString(normalized, `${1}<RUN_ID>`)
	normalized = contractCommitRE.ReplaceAllString(normalized, "<COMMIT_SHA>")
	normalized = contractRFC3339RE.ReplaceAllString(normalized, "<TIMESTAMP>")
	normalized = contractDurationRE.ReplaceAllString(normalized, "<DURATION>")
	return normalized
}

func normalizedJSONDiff(expected, actual string) string {
	expectedLines := strings.Split(expected, "\n")
	actualLines := strings.Split(actual, "\n")
	max := len(expectedLines)
	if len(actualLines) > max {
		max = len(actualLines)
	}
	for i := 0; i < max; i++ {
		expectedLine := "<missing>"
		actualLine := "<missing>"
		if i < len(expectedLines) {
			expectedLine = expectedLines[i]
		}
		if i < len(actualLines) {
			actualLine = actualLines[i]
		}
		if expectedLine != actualLine {
			start := i - 3
			if start < 0 {
				start = 0
			}
			end := i + 4
			if end > max {
				end = max
			}
			var b strings.Builder
			fmt.Fprintf(&b, "--- expected\n+++ actual\n@@ line %d @@\n", i+1)
			for line := start; line < end; line++ {
				want := "<missing>"
				got := "<missing>"
				if line < len(expectedLines) {
					want = expectedLines[line]
				}
				if line < len(actualLines) {
					got = actualLines[line]
				}
				if want == got {
					fmt.Fprintf(&b, " %s\n", want)
					continue
				}
				fmt.Fprintf(&b, "-%s\n+%s\n", want, got)
			}
			return strings.TrimRight(b.String(), "\n")
		}
	}
	return "no diff"
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
	r := result{Name: name, Status: "SKIP", Error: reason, Duration: formatDuration(0)}
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

func (h *harness) gitRefExists(dir, ref string) (bool, error) {
	r := h.executeCommand(commandSpec{
		Label:   "git ref exists",
		Dir:     dir,
		Name:    "git",
		Args:    []string{"show-ref", "--verify", "--quiet", ref},
		Timeout: defaultCommandTimeout,
	})
	if r.Status == "PASS" {
		return true, nil
	}
	if r.ExitCode == 1 {
		return false, nil
	}
	return false, resultError(r)
}

func (h *harness) controlledPath(commands ...string) (string, error) {
	dir := filepath.Join(h.tmp, "controlled-path-"+slug(strings.Join(commands, "-")))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, command := range commands {
		source, err := exec.LookPath(command)
		if err != nil {
			return "", fmt.Errorf("find %s for controlled PATH: %w", command, err)
		}
		target := filepath.Join(dir, command)
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		wrapper := "#!/bin/sh\nexec " + shellQuote(source) + " \"$@\"\n"
		if err := os.WriteFile(target, []byte(wrapper), 0o755); err != nil {
			return "", fmt.Errorf("write %s wrapper into controlled PATH: %w", command, err)
		}
	}
	return dir, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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
		Host: reportHost{
			OS:        runtime.GOOS,
			Arch:      runtime.GOARCH,
			GoVersion: runtime.Version(),
		},
		Isolation: reportIsolation{
			CodeMeshHome: h.codemeshHome,
			Home:         h.home,
			Workspace:    h.workspace,
			RunDir:       h.runDir,
			GitConfig:    filepath.Join(h.home, ".gitconfig"),
		},
		Live:         h.live,
		TwoMachine:   h.twoMachine,
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
	return []string{fakeEnvFixtureFileSecret(), fakeEnvFixtureKeySecret(), fakeEnvBindingSecret()}
}

func fakeEnvFixtureFileSecret() string {
	return strings.Join([]string{"e2e", "fixture", "env", "file", "secret"}, "-")
}

func fakeEnvFixtureKeySecret() string {
	return strings.Join([]string{"e2e", "fixture", "env", "key", "secret"}, "-")
}

func fakeEnvBindingSecret() string {
	return envbinding.FakeProviderValue("fake://e2e-bound-env")
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

func firstRunID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			fields := strings.Fields(strings.TrimPrefix(line, "- "))
			if len(fields) != 0 {
				return fields[0]
			}
		}
	}
	return ""
}

func (h *harness) createOfflineGitFixtures() (offlineGitFixtures, error) {
	return h.createOfflineGitFixturesAt(filepath.Join(h.tmp, "git-fixtures"))
}

func (h *harness) createOfflineGitFixturesAt(root string) (offlineGitFixtures, error) {
	fixtures := offlineGitFixtures{
		Root:    root,
		Remotes: filepath.Join(root, "remotes"),
		Sources: filepath.Join(root, "sources"),
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
	if project, err := h.createClonedFixture(fixtures, "remote-default-dev", nil); err != nil {
		return fixtures, err
	} else {
		developExists, err := h.gitRefExists(project.Source, "refs/heads/develop")
		if err != nil {
			return fixtures, err
		}
		if !developExists {
			if _, _, err := h.exec(project.Source, "git", "checkout", "-b", "develop"); err != nil {
				return fixtures, err
			}
			if err := os.WriteFile(filepath.Join(project.Source, "develop.txt"), []byte("develop branch\n"), 0o644); err != nil {
				return fixtures, err
			}
			if _, _, err := h.exec(project.Source, "git", "add", "."); err != nil {
				return fixtures, err
			}
			if _, _, err := h.exec(project.Source, "git", "-c", "user.name=CodeMesh E2E", "-c", "user.email=e2e@example.invalid", "commit", "-m", "Develop branch"); err != nil {
				return fixtures, err
			}
			if _, _, err := h.exec(project.Source, "git", "push", "-u", "origin", "develop"); err != nil {
				return fixtures, err
			}
		}
		if _, _, err := h.exec(project.Remote, "git", "symbolic-ref", "HEAD", "refs/heads/develop"); err != nil {
			return fixtures, err
		}
		if _, _, err := h.exec(project.Source, "git", "checkout", "main"); err != nil {
			return fixtures, err
		}
		project.BaseBranch = "develop"
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
	writeBoundEnvPolicy := func(path string) error {
		policy := []byte("agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_E2E_BOUND_ENV\n")
		return os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644)
	}
	if project, err := h.createClonedFixtureWithSeed(fixtures, "required-env-bound", writeBoundEnvPolicy, nil); err != nil {
		return fixtures, err
	} else {
		project.RequiredEnv = []string{"CODEMESH_E2E_BOUND_ENV"}
		fixtures.Projects = append(fixtures.Projects, project)
	}
	return fixtures, nil
}

func writeToolchainPolicy(path string) error {
	policy := []byte("agent:\n  toolchain:\n    mode: warn\n    requirements:\n      - go\n")
	return os.WriteFile(filepath.Join(path, ".codemesh.yml"), policy, 0o644)
}

func writeLiveGoToolchainPolicy(path string) error {
	if err := os.WriteFile(filepath.Join(path, "go.mod"), []byte("module example.invalid/live-toolchain-go\n\ngo 1.26\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, ".codemesh.yml"), []byte("agent:\n  toolchain:\n    mode: warn\n    requirements:\n      - go\n"), 0o644)
}

func writeLivePackageToolchainPolicy(path string) error {
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte("{\n  \"scripts\": {\n    \"check\": \"echo ok\"\n  }\n}\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, ".codemesh.yml"), []byte("agent:\n  toolchain:\n    mode: warn\n    requirements:\n      - npm\n"), 0o644)
}

func writeMissingWarnToolchainPolicy(path string) error {
	return os.WriteFile(filepath.Join(path, ".codemesh.yml"), []byte("agent:\n  toolchain:\n    mode: warn\n    requirements:\n      - codemesh-missing-tool\n"), 0o644)
}

func writeMissingBlockToolchainPolicy(path string) error {
	return os.WriteFile(filepath.Join(path, ".codemesh.yml"), []byte("agent:\n  toolchain:\n    mode: block\n    requirements:\n      - codemesh-missing-tool\n"), 0o644)
}

func writeE2EManifestEntry(dir, name, identity, alias, desiredPath string) error {
	return writeE2EManifestEntryWithCloneURL(dir, name, identity, alias, desiredPath, identity+".git")
}

func writeE2EManifestEntryWithCloneURL(dir, name, identity, alias, desiredPath, cloneURL string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data := fmt.Sprintf(`{
  "manifest_version": 1,
  "project": {
    "identity": %q,
    "alias": %q,
    "desired_path": %q,
    "clone_hints": {
      "url": %q
    },
    "groups": []
  }
}
`, identity, alias, desiredPath, cloneURL)
	return os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644)
}

func (h *harness) newTwoMachineNode(root, name string) (twoMachineNode, error) {
	node := twoMachineNode{
		Name:          name,
		CodeMeshHome:  filepath.Join(root, name, "codemesh-home"),
		Home:          filepath.Join(root, name, "home"),
		WorkspaceRoot: filepath.Join(root, name, "workspace"),
	}
	if err := os.MkdirAll(node.CodeMeshHome, 0o755); err != nil {
		return node, err
	}
	if err := os.MkdirAll(node.Home, 0o755); err != nil {
		return node, err
	}
	if err := os.WriteFile(filepath.Join(node.Home, ".gitconfig"), nil, 0o644); err != nil {
		return node, err
	}
	if err := os.MkdirAll(node.WorkspaceRoot, 0o755); err != nil {
		return node, err
	}
	return node, nil
}

func (n twoMachineNode) env() []string {
	return []string{
		"CODEMESH_HOME=" + n.CodeMeshHome,
		"HOME=" + n.Home,
		"GIT_CONFIG_GLOBAL=" + filepath.Join(n.Home, ".gitconfig"),
	}
}

func (h *harness) twoMachineCommand(label string, machine twoMachineNode, name string, args ...string) result {
	r := h.executeCommand(commandSpec{
		Label:   label,
		Dir:     machine.WorkspaceRoot,
		Name:    name,
		Args:    args,
		Timeout: longCommandTimeout,
		Env:     machine.env(),
	})
	h.record(r)
	return r
}

func (h *harness) startLocalGitDaemon(basePath, probeRepo string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return "", nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "git", "daemon", "--verbose", "--log-destination=stderr", "--export-all", "--reuseaddr", "--base-path="+basePath, "--listen=127.0.0.1", "--port="+strconv.Itoa(port), basePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	stop := func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}

	remoteBase := "git://127.0.0.1:" + strconv.Itoa(port)
	probeURL := remoteBase + "/" + probeRepo
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-done:
			cancel()
			return "", nil, fmt.Errorf("git daemon exited early: %v %s", waitErr, strings.TrimSpace(stderr.String()))
		default:
		}
		probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
		probe := exec.CommandContext(probeCtx, "git", "ls-remote", probeURL, "HEAD")
		if err := probe.Run(); err == nil {
			probeCancel()
			return remoteBase, stop, nil
		}
		probeCancel()
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	return "", nil, fmt.Errorf("git daemon did not become ready for %s: %s", probeURL, strings.TrimSpace(stderr.String()))
}

func (h *harness) expectMachineRegisterJSON(name string, r result, workspaceRoot string) (string, bool) {
	if r.Status != "PASS" {
		return "", false
	}
	var payload struct {
		ID            string `json:"id"`
		WorkspaceRoot string `json:"workspace_root"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "stdout was not JSON: " + err.Error(), ExitCode: -1})
		return "", false
	}
	if payload.ID == "" || payload.WorkspaceRoot != workspaceRoot {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("machine payload = %#v, want workspace %s", payload, workspaceRoot), ExitCode: -1})
		return "", false
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return payload.ID, true
}

func (h *harness) expectManifestEntry(name, path, identity, alias, desiredPath, cloneURL string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	var entry struct {
		ManifestVersion int `json:"manifest_version"`
		Project         struct {
			Identity    string `json:"identity"`
			Alias       string `json:"alias"`
			DesiredPath string `json:"desired_path"`
			CloneHints  struct {
				URL string `json:"url"`
			} `json:"clone_hints"`
		} `json:"project"`
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if entry.ManifestVersion != 1 || entry.Project.Identity != identity || entry.Project.Alias != alias || entry.Project.DesiredPath != desiredPath || entry.Project.CloneHints.URL != cloneURL {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("manifest entry = %#v", entry), ExitCode: -1})
		return false
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func projectRowByAlias(rows []projectRow, alias string) (projectRow, bool) {
	for _, row := range rows {
		if row.Alias == alias {
			return row, true
		}
	}
	return projectRow{}, false
}

func (h *harness) expectProjectRowsAt(name, codemeshHome string, want ...projectRow) bool {
	got, err := readProjectRowsFromStore(filepath.Join(codemeshHome, "codemesh.db"))
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(got) != len(want) {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project rows = %#v, want %#v", got, want), ExitCode: -1})
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project row %d = %#v, want %#v", i, got[i], want[i]), ExitCode: -1})
			return false
		}
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) expectPathMissingResult(name, path string) bool {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("path exists or stat failed: %v", err), ExitCode: -1})
		return false
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func (h *harness) expectTwoMachineHydrateJSON(name string, r result, alias, remote, path string) bool {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Outcome     string `json:"outcome"`
			Path        string `json:"path"`
			PathPresent bool   `json:"path_present"`
			Remote      string `json:"remote"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "stdout was not JSON: " + err.Error(), ExitCode: -1})
		return false
	}
	got := payload.Payload
	if payload.Command != "hydrate" || payload.ExitClass != "success" || got.Project != alias || got.Outcome != "hydrated" || got.Path != path || !got.PathPresent || got.Remote != remote {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("hydrate payload = %#v", payload), ExitCode: -1})
		return false
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
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

func (h *harness) updateResultByName(r result) {
	for i := len(h.results) - 1; i >= 0; i-- {
		if h.results[i].Name == r.Name {
			previous := h.results[i]
			h.results[i] = r
			if previous.Status != r.Status || previous.Error != r.Error {
				h.print(r)
			}
			return
		}
	}
	h.record(r)
}

func (h *harness) newScenario(name string) (*scenario, error) {
	return h.newScenarioWithFixtureRoot(name, filepath.Join(h.tmp, "git-fixtures"))
}

func (h *harness) newScenarioWithFixtureRoot(name, fixtureRoot string) (*scenario, error) {
	codemeshHome := filepath.Join(h.tmp, "codemesh-"+slug(name)+"-home")
	if err := os.MkdirAll(codemeshHome, 0o755); err != nil {
		return nil, err
	}
	fixtures, err := h.createOfflineGitFixturesAt(fixtureRoot)
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

func (s *scenario) expectedFailureEnv(label string, env []string, args ...string) result {
	return s.execute(label, env, args...)
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

func (s *scenario) expectStatusJSON(r result, alias, exitClass, state, base string) bool {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				Alias       string `json:"alias"`
				State       string `json:"state"`
				PathPresent bool   `json:"path_present"`
				Remote      string `json:"remote"`
				Base        string `json:"base"`
				Diagnostics struct {
					Warnings []struct {
						Code string `json:"code"`
					} `json:"warnings"`
					Blockers []struct {
						Code string `json:"code"`
					} `json:"blockers"`
				} `json:"diagnostics"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		s.failCommandAssertion(r, "stdout was not JSON: "+err.Error())
		return false
	}
	if payload.Command != "status" || payload.ExitClass != exitClass {
		s.failCommandAssertion(r, fmt.Sprintf("command metadata = %#v, want command status exit_class %s", payload, exitClass))
		return false
	}
	if len(payload.Payload.Projects) != 1 {
		s.failCommandAssertion(r, fmt.Sprintf("project count = %d, want 1", len(payload.Payload.Projects)))
		return false
	}
	project := payload.Payload.Projects[0]
	if project.Alias != alias || project.State != state || !project.PathPresent || project.Remote == "" || project.Base != base {
		s.failCommandAssertion(r, fmt.Sprintf("project payload = %#v", project))
		return false
	}
	if len(project.Diagnostics.Warnings) != 0 || len(project.Diagnostics.Blockers) != 0 {
		s.failCommandAssertion(r, fmt.Sprintf("diagnostics = %#v, want empty", project.Diagnostics))
		return false
	}
	return true
}

func (s *scenario) expectTreeJSON(r result, exitClass string, states map[string]string) bool {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				Alias       string `json:"alias"`
				State       string `json:"state"`
				Path        string `json:"path"`
				PathPresent bool   `json:"path_present"`
				Remote      string `json:"remote"`
				Base        string `json:"base"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		s.failCommandAssertion(r, "stdout was not JSON: "+err.Error())
		return false
	}
	if payload.Command != "tree" || payload.ExitClass != exitClass {
		s.failCommandAssertion(r, fmt.Sprintf("command metadata = %#v, want command tree exit_class %s", payload, exitClass))
		return false
	}
	seen := make(map[string]string, len(payload.Payload.Projects))
	for _, project := range payload.Payload.Projects {
		if project.Path == "" || project.Remote == "" || project.Base == "" {
			s.failCommandAssertion(r, fmt.Sprintf("tree project missing canonical fields: %#v", project))
			return false
		}
		seen[project.Alias] = project.State
	}
	for alias, want := range states {
		if got, ok := seen[alias]; !ok || got != want {
			s.failCommandAssertion(r, fmt.Sprintf("tree JSON state for %s = %q present=%t, want %q", alias, got, ok, want))
			return false
		}
	}
	return true
}

func (s *scenario) expectHydrateJSON(r result, exitClass, alias, outcome, path string, pathPresent bool, blockerCodes []string) bool {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Outcome     string `json:"outcome"`
			Path        string `json:"path"`
			PathPresent bool   `json:"path_present"`
			Remote      string `json:"remote"`
		} `json:"payload"`
		Diagnostics struct {
			Blockers []struct {
				Code string `json:"code"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		s.failCommandAssertion(r, "stdout was not JSON: "+err.Error())
		return false
	}
	got := payload.Payload
	if payload.Command != "hydrate" || payload.ExitClass != exitClass || got.Project != alias || got.Outcome != outcome || got.Path != path || got.PathPresent != pathPresent || got.Remote == "" {
		s.failCommandAssertion(r, fmt.Sprintf("hydrate JSON payload = %#v, want class=%s alias=%s outcome=%s path=%s present=%t", payload, exitClass, alias, outcome, path, pathPresent))
		return false
	}
	if err := diagnosticCodesMatch("hydrate blockers", hydrateBlockerCodes(payload.Diagnostics.Blockers), blockerCodes); err != nil {
		s.failCommandAssertion(r, err.Error())
		return false
	}
	return true
}

func hydrateBlockerCodes(items []struct {
	Code string `json:"code"`
}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func (s *scenario) expectAgentPrepareJSON(r result, exitClass, alias string, ready bool, base, profile string, handoffDocsCount int, warningCodes, blockerCodes []string) bool {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project          string `json:"project"`
			Ready            bool   `json:"ready"`
			RunID            string `json:"run_id"`
			Base             string `json:"base"`
			Profile          string `json:"profile"`
			HandoffDocsCount int    `json:"handoff_docs_count"`
			RunContractPath  string `json:"run_contract_path"`
			ReadyPath        string `json:"ready_path"`
			ResolvedCommit   string `json:"resolved_commit"`
			Diagnostics      struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		s.failCommandAssertion(r, "stdout was not JSON: "+err.Error())
		return false
	}
	got := payload.Payload
	if payload.Command != "agent prepare" || payload.ExitClass != exitClass || got.Project != alias || got.Ready != ready || got.Base != base || got.Profile != profile || got.HandoffDocsCount != handoffDocsCount {
		s.failCommandAssertion(r, fmt.Sprintf("agent prepare JSON payload = %#v", payload))
		return false
	}
	if ready {
		if got.RunID == "" || got.ReadyPath == "" || got.RunContractPath != filepath.Join(got.ReadyPath, "codemesh-run.json") || got.ResolvedCommit == "" {
			s.failCommandAssertion(r, fmt.Sprintf("agent prepare JSON missing ready fields: %#v", got))
			return false
		}
		if _, err := os.Stat(got.RunContractPath); err != nil {
			s.failCommandAssertion(r, "run contract path missing: "+err.Error())
			return false
		}
	} else if got.RunID != "" || got.ReadyPath != "" || got.RunContractPath != "" || got.ResolvedCommit != "" {
		s.failCommandAssertion(r, fmt.Sprintf("blocked agent prepare JSON included ready fields: %#v", got))
		return false
	}
	if err := diagnosticCodesMatch("agent prepare warnings", agentPrepareWarningCodes(got.Diagnostics.Warnings), warningCodes); err != nil {
		s.failCommandAssertion(r, err.Error())
		return false
	}
	if err := diagnosticCodesMatch("agent prepare blockers", agentPrepareBlockerCodes(got.Diagnostics.Blockers), blockerCodes); err != nil {
		s.failCommandAssertion(r, err.Error())
		return false
	}
	return true
}

func agentPrepareWarningCodes(items []struct {
	Code string `json:"code"`
}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func agentPrepareBlockerCodes(items []struct {
	Code string `json:"code"`
}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func doctorJSONMatches(raw, alias, exitClass, handoff, state, base string, strict bool, warningCodes, blockerCodes []string) error {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Handoff     string `json:"handoff"`
			Strict      bool   `json:"strict"`
			State       string `json:"state"`
			PathPresent bool   `json:"path_present"`
			Remote      string `json:"remote"`
			Base        string `json:"base"`
			Diagnostics struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("stdout was not JSON: %w", err)
	}
	if payload.Command != "doctor" || payload.ExitClass != exitClass {
		return fmt.Errorf("command metadata = %#v, want command doctor exit_class %s", payload, exitClass)
	}
	got := payload.Payload
	if got.Project != alias || got.Handoff != handoff || got.Strict != strict || got.State != state || !got.PathPresent || got.Remote == "" || got.Base != base {
		return fmt.Errorf("payload = %#v", got)
	}
	if err := diagnosticCodesMatch("warnings", doctorWarningCodes(got.Diagnostics.Warnings), warningCodes); err != nil {
		return err
	}
	if err := diagnosticCodesMatch("blockers", doctorBlockerCodes(got.Diagnostics.Blockers), blockerCodes); err != nil {
		return err
	}
	return nil
}

func doctorToolchainJSONMatches(raw, alias, name, status string) error {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project   string `json:"project"`
			Handoff   string `json:"handoff"`
			Toolchain []struct {
				Name    string                `json:"name"`
				Status  string                `json:"status"`
				Project toolchainProjectFacts `json:"project"`
				Host    toolchainHostFacts    `json:"host"`
			} `json:"toolchain"`
			Diagnostics struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("stdout was not JSON: %w", err)
	}
	wantExitClass := "readiness-warning"
	wantHandoff := "warning"
	wantWarnings := []string{"unknown-toolchain"}
	if status == "present" {
		wantExitClass = "success"
		wantHandoff = "green"
		wantWarnings = nil
	}
	if payload.Command != "doctor" || payload.ExitClass != wantExitClass || payload.Payload.Project != alias || payload.Payload.Handoff != wantHandoff {
		return fmt.Errorf("toolchain doctor metadata = %#v", payload)
	}
	if len(payload.Payload.Toolchain) != 1 || payload.Payload.Toolchain[0].Name != name || payload.Payload.Toolchain[0].Status != status {
		return fmt.Errorf("toolchain payload = %#v, want %s=%s", payload.Payload.Toolchain, name, status)
	}
	if status == "present" && (payload.Payload.Toolchain[0].Project.Requirement != name || payload.Payload.Toolchain[0].Host.Command != name || payload.Payload.Toolchain[0].Host.Version == "") {
		return fmt.Errorf("toolchain facts = %#v, want separated project requirement and host command/version", payload.Payload.Toolchain[0])
	}
	if err := diagnosticCodesMatch("toolchain warnings", doctorWarningCodes(payload.Payload.Diagnostics.Warnings), wantWarnings); err != nil {
		return err
	}
	if len(payload.Payload.Diagnostics.Blockers) != 0 {
		return fmt.Errorf("toolchain diagnostics = %#v", payload.Payload.Diagnostics)
	}
	return nil
}

func targetExportJSONMatches(raw, targetName, targetKind, workspaceRoot, alias, desiredPath, requirement, secretRef string) error {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			TargetSpecVersion int `json:"target_spec_version"`
			Target            struct {
				Name          string   `json:"name"`
				Kind          string   `json:"kind"`
				WorkspaceRoot string   `json:"workspace_root"`
				Scopes        []string `json:"scopes"`
			} `json:"target"`
			Machine struct {
				ID            string `json:"id"`
				Hostname      string `json:"hostname"`
				OS            string `json:"os"`
				Architecture  string `json:"architecture"`
				WorkspaceRoot string `json:"workspace_root"`
			} `json:"machine"`
			Topology []struct {
				Project struct {
					Alias       string `json:"alias"`
					DesiredPath string `json:"desired_path"`
				} `json:"project"`
			} `json:"topology"`
			EnvPolicy []struct {
				Project struct {
					Alias       string `json:"alias"`
					DesiredPath string `json:"desired_path"`
				} `json:"project"`
				Env struct {
					Bindings []struct {
						Requirement string   `json:"requirement"`
						Provider    string   `json:"provider"`
						SecretRef   string   `json:"secret_ref"`
						Scopes      []string `json:"scopes"`
						Values      string   `json:"values"`
					} `json:"bindings"`
				} `json:"env"`
			} `json:"env_policy"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return fmt.Errorf("stdout was not JSON: %w", err)
	}
	got := payload.Payload
	if payload.Command != "target export" || payload.ExitClass != "success" || got.TargetSpecVersion != 1 {
		return fmt.Errorf("target export metadata = %#v", payload)
	}
	if got.Target.Name != targetName || got.Target.Kind != targetKind || got.Target.WorkspaceRoot != workspaceRoot || !reflect.DeepEqual(got.Target.Scopes, []string{"codex"}) {
		return fmt.Errorf("target facts = %#v", got.Target)
	}
	if got.Machine.ID == "" || got.Machine.Hostname == "" || got.Machine.OS == "" || got.Machine.Architecture == "" || got.Machine.WorkspaceRoot != workspaceRoot {
		return fmt.Errorf("machine facts = %#v", got.Machine)
	}
	foundTopology := false
	for _, entry := range got.Topology {
		if entry.Project.Alias == alias && entry.Project.DesiredPath == desiredPath {
			foundTopology = true
			break
		}
	}
	if !foundTopology {
		return fmt.Errorf("topology missing %s %s: %#v", alias, desiredPath, got.Topology)
	}
	for _, project := range got.EnvPolicy {
		if project.Project.Alias != alias || project.Project.DesiredPath != desiredPath {
			continue
		}
		for _, binding := range project.Env.Bindings {
			if binding.Requirement == requirement && binding.Provider == "fake" && binding.SecretRef == secretRef && binding.Values == "not-recorded" && reflect.DeepEqual(binding.Scopes, []string{"codex"}) {
				return nil
			}
		}
		return fmt.Errorf("env policy for %s missing scoped binding: %#v", alias, project.Env.Bindings)
	}
	return fmt.Errorf("env policy missing project %s: %#v", alias, got.EnvPolicy)
}

func singleDoctorToolchain(raw string) (agentToolchainStatus, error) {
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			Toolchain []agentToolchainStatus `json:"toolchain"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return agentToolchainStatus{}, fmt.Errorf("stdout was not JSON: %w", err)
	}
	if payload.Command != "doctor" {
		return agentToolchainStatus{}, fmt.Errorf("command = %q, want doctor", payload.Command)
	}
	if len(payload.Payload.Toolchain) != 1 {
		return agentToolchainStatus{}, fmt.Errorf("toolchain count = %d, want 1", len(payload.Payload.Toolchain))
	}
	return payload.Payload.Toolchain[0], nil
}

func agentPrepareReadyPath(raw string) (string, error) {
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			ReadyPath string `json:"ready_path"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", fmt.Errorf("stdout was not JSON: %w", err)
	}
	if payload.Command != "agent prepare" {
		return "", fmt.Errorf("command = %q, want agent prepare", payload.Command)
	}
	if strings.TrimSpace(payload.Payload.ReadyPath) == "" {
		return "", errors.New("agent prepare JSON did not include ready_path")
	}
	return payload.Payload.ReadyPath, nil
}

func doctorWarningCodes(items []struct {
	Code string `json:"code"`
}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func doctorBlockerCodes(items []struct {
	Code string `json:"code"`
}) []string {
	codes := make([]string, 0, len(items))
	for _, item := range items {
		codes = append(codes, item.Code)
	}
	return codes
}

func diagnosticCodesMatch(label string, got, want []string) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s codes = %v, want %v", label, got, want)
	}
	for _, expected := range want {
		found := false
		for _, actual := range got {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s codes = %v, want %v", label, got, want)
		}
	}
	return nil
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
	if fileMetadata.BaseProvenance.FetchedBase != base || fileMetadata.BaseProvenance.FetchedCommit == "" || fileMetadata.BaseProvenance.PreparedHEAD != fileMetadata.ResolvedCommit || !fileMetadata.BaseProvenance.MatchesFetched {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("codemesh-run.json base provenance = %#v", fileMetadata.BaseProvenance), ExitCode: -1})
		return false
	}
	if fileMetadata.ContractVersion != 1 || fileMetadata.Producer.Name != "codemesh" || fileMetadata.Producer.Version == "" {
		s.h.record(result{Name: name, Status: "FAIL", Error: "codemesh-run.json missing agent run contract version or producer", ExitCode: -1})
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
	if !reflect.DeepEqual(dbMetadata.BaseProvenance, fileMetadata.BaseProvenance) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "state-store base provenance diverged from file metadata", ExitCode: -1})
		return false
	}
	if dbMetadata.ContractVersion != fileMetadata.ContractVersion || dbMetadata.Producer != fileMetadata.Producer {
		s.h.record(result{Name: name, Status: "FAIL", Error: "state-store agent run contract metadata diverged from file metadata", ExitCode: -1})
		return false
	}
	if containsAnySecret(fileMetadata.Raw, fakeEnvFixtureSecrets()) || containsAnySecret(dbMetadata.Raw, fakeEnvFixtureSecrets()) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "fake env secret marker appeared in agent run metadata", ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunSourcePathMissing(name, readyPath, sourcePath string, want bool) bool {
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
	if fileMetadata.Project.SourcePath != sourcePath || dbMetadata.Project.SourcePath != sourcePath {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("source path file=%q db=%q want %q", fileMetadata.Project.SourcePath, dbMetadata.Project.SourcePath, sourcePath), ExitCode: -1})
		return false
	}
	if fileMetadata.Project.SourcePathMissing != want || dbMetadata.Project.SourcePathMissing != want {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("source_path_missing file=%t db=%t want %t", fileMetadata.Project.SourcePathMissing, dbMetadata.Project.SourcePathMissing, want), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectCommandStdoutEqualsCanonicalPath(name, stdoutPath, wantPath string) bool {
	return s.h.expectCommandStdoutEqualsCanonicalPath(name, stdoutPath, wantPath)
}

func (h *harness) expectCommandStdoutEqualsCanonicalPath(name, stdoutPath, wantPath string) bool {
	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	canonical, err := filepath.EvalSymlinks(wantPath)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if strings.TrimSpace(string(data)) != canonical {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("stdout = %q, want %q", strings.TrimSpace(string(data)), canonical), ExitCode: -1})
		return false
	}
	return true
}

func (s *scenario) expectAgentRunCommandContract(name, readyPath, label string) bool {
	return s.h.expectAgentRunCommandContract(name, s.codemeshHome, readyPath, label)
}

func (h *harness) expectAgentRunCommandContract(name, codemeshHome, readyPath, label string) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if containsAnySecret(fileMetadata.Raw, fakeEnvFixtureSecrets()) || containsAnySecret(dbMetadata.Raw, fakeEnvFixtureSecrets()) {
		h.record(result{Name: name, Status: "FAIL", Error: "fake env secret marker appeared in command metadata", ExitCode: -1})
		return false
	}
	fileCommand, err := expectedAgentCommand(fileMetadata, readyPath, label)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "file metadata: " + err.Error(), ExitCode: -1})
		return false
	}
	dbCommand, err := expectedAgentCommand(dbMetadata, readyPath, label)
	if err != nil {
		h.record(result{Name: name, Status: "FAIL", Error: "state metadata: " + err.Error(), ExitCode: -1})
		return false
	}
	if !reflect.DeepEqual(fileCommand, dbCommand) {
		h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("file/state command metadata differ: file=%#v db=%#v", fileCommand, dbCommand), ExitCode: -1})
		return false
	}
	runDir := filepath.Dir(readyPath)
	for _, outputPath := range []string{fileCommand.StdoutPath, fileCommand.StderrPath} {
		inside, err := pathInside(runDir, outputPath)
		if err != nil || !inside {
			h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("command output path outside managed run dir: %s (%v)", outputPath, err), ExitCode: -1})
			return false
		}
		if _, err := os.Stat(outputPath); err != nil {
			h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
			return false
		}
	}
	h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func expectedAgentCommand(metadata agentMetadata, readyPath, label string) (agentCommand, error) {
	if len(metadata.Commands) != 1 {
		return agentCommand{}, fmt.Errorf("command count = %d, want 1", len(metadata.Commands))
	}
	command := metadata.Commands[0]
	if command.Label != label {
		return agentCommand{}, fmt.Errorf("label = %q, want %q", command.Label, label)
	}
	if command.CWD != readyPath {
		return agentCommand{}, fmt.Errorf("cwd = %q, want %q", command.CWD, readyPath)
	}
	if command.Env.Mode == "" || command.Env.Values != "not-recorded" || len(command.Env.Keys) != 0 {
		return agentCommand{}, fmt.Errorf("env summary = %#v", command.Env)
	}
	if command.Base.Base != metadata.Base || command.Base.ResolvedCommit != metadata.ResolvedCommit || command.Base.Remote != metadata.Project.Remote {
		return agentCommand{}, fmt.Errorf("base provenance = %#v, want base=%s commit=%s remote=%s", command.Base, metadata.Base, metadata.ResolvedCommit, metadata.Project.Remote)
	}
	if command.ExitCode != 0 || command.Duration == "" || command.StdoutPath == "" || command.StderrPath == "" || command.ExecutedAt == "" {
		return agentCommand{}, fmt.Errorf("incomplete command record = %#v", command)
	}
	return command, nil
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

func (s *scenario) expectAgentRunEnvMaterialization(name, readyPath, requirement, fakeValue string) bool {
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
	if containsAnySecret(fileMetadata.Raw, []string{fakeValue}) || containsAnySecret(dbMetadata.Raw, []string{fakeValue}) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "fake provider value appeared in agent run metadata", ExitCode: -1})
		return false
	}
	if fileMetadata.Env.MaterializationStatus != "materialized" || dbMetadata.Env.MaterializationStatus != "materialized" {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("env materialization status file=%q db=%q", fileMetadata.Env.MaterializationStatus, dbMetadata.Env.MaterializationStatus), ExitCode: -1})
		return false
	}
	if len(fileMetadata.Env.Requirements) != 1 || fileMetadata.Env.Requirements[0].Name != requirement || fileMetadata.Env.Requirements[0].Kind != "env_key" {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("env requirements = %#v", fileMetadata.Env.Requirements), ExitCode: -1})
		return false
	}
	if strings.Join(fileMetadata.Env.AllowedScopes, ",") != "codex" || strings.Join(dbMetadata.Env.AllowedScopes, ",") != "codex" {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("allowed scopes file=%v db=%v", fileMetadata.Env.AllowedScopes, dbMetadata.Env.AllowedScopes), ExitCode: -1})
		return false
	}
	bundlePath := fileMetadata.Env.Bundle.Path
	if !fileMetadata.Env.Bundle.Present || bundlePath == "" || fileMetadata.Env.Bundle.Values != "not-recorded" {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("env bundle metadata = %#v", fileMetadata.Env.Bundle), ExitCode: -1})
		return false
	}
	if bundlePath != dbMetadata.Env.Bundle.Path {
		s.h.record(result{Name: name, Status: "FAIL", Error: "file/state env bundle path diverged", ExitCode: -1})
		return false
	}
	insideWorkspace, err := pathInside(readyPath, bundlePath)
	if err != nil || insideWorkspace {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("env bundle path is inside workspace: %s (%v)", bundlePath, err), ExitCode: -1})
		return false
	}
	insideRun, err := pathInside(filepath.Dir(readyPath), bundlePath)
	if err != nil || !insideRun {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("env bundle path outside managed run dir: %s (%v)", bundlePath, err), ExitCode: -1})
		return false
	}
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if !strings.Contains(string(bundle), requirement+"="+fakeValue) {
		s.h.record(result{Name: name, Status: "FAIL", Error: "env bundle missing deterministic fake provider value", ExitCode: -1})
		return false
	}
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func (s *scenario) expectAgentRunToolchain(name, readyPath, requirement, status string) bool {
	fileMetadata, err := readAgentMetadata(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: "file metadata: " + err.Error(), ExitCode: -1})
		return false
	}
	dbMetadata, err := readAgentRunMetadataFromStore(filepath.Join(s.codemeshHome, "codemesh.db"), fileMetadata.RunID)
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: "state metadata: " + err.Error(), ExitCode: -1})
		return false
	}
	if !agentToolchainMatches(fileMetadata.Toolchain, requirement, status) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("file toolchain = %#v, want %s=%s", fileMetadata.Toolchain, requirement, status), ExitCode: -1})
		return false
	}
	if !agentToolchainMatches(dbMetadata.Toolchain, requirement, status) {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("state toolchain = %#v, want %s=%s", dbMetadata.Toolchain, requirement, status), ExitCode: -1})
		return false
	}
	if status == "present" {
		if len(fileMetadata.Diagnostics.Warnings) != 0 || len(fileMetadata.Diagnostics.Blockers) != 0 {
			s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("metadata diagnostics = %#v", fileMetadata.Diagnostics), ExitCode: -1})
			return false
		}
	} else if !hasAgentDiagnostic(fileMetadata.Diagnostics.Warnings, "unknown-toolchain") || len(fileMetadata.Diagnostics.Blockers) != 0 {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("metadata diagnostics = %#v", fileMetadata.Diagnostics), ExitCode: -1})
		return false
	}
	if !expectNoToolchainArtifacts(s.h, name, readyPath) {
		return false
	}
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
}

func agentToolchainMatches(items []agentToolchainStatus, name, status string) bool {
	return len(items) == 1 && items[0].Name == name && items[0].Status == status
}

func agentToolchainEqual(left, right agentToolchainStatus) bool {
	return left.Name == right.Name &&
		left.Status == right.Status &&
		left.Project == right.Project &&
		left.Host == right.Host
}

func expectNoToolchainArtifacts(h *harness, name, root string) bool {
	for _, path := range []string{"node_modules", ".tool-versions", ".codemesh-toolchain"} {
		if _, err := os.Stat(filepath.Join(root, path)); err == nil {
			h.record(result{Name: name, Status: "FAIL", Error: "toolchain readiness created " + path, ExitCode: -1})
			return false
		} else if !errors.Is(err, os.ErrNotExist) {
			h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("check %s: %v", path, err), ExitCode: -1})
			return false
		}
	}
	return true
}

func hasAgentDiagnostic(items []agentDiagnostic, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
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

func (s *scenario) expectAgentRunRows(name string, want int) bool {
	db, err := sql.Open("sqlite", filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`select count(*) from agent_runs`).Scan(&count); err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if count != want {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("agent run rows = %d, want %d", count, want), ExitCode: -1})
		return false
	}
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
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

func (s *scenario) expectProjectRowCountRaw(name string, want int) bool {
	projects, err := readProjectRowsFromStore(filepath.Join(s.codemeshHome, "codemesh.db"))
	if err != nil {
		s.h.record(result{Name: name, Status: "FAIL", Error: err.Error(), ExitCode: -1})
		return false
	}
	if len(projects) != want {
		s.h.record(result{Name: name, Status: "FAIL", Error: fmt.Sprintf("project row count = %d, want %d", len(projects), want), ExitCode: -1})
		return false
	}
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
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

func (s *scenario) expectProjectRowsRaw(name string, want ...projectRow) bool {
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
	s.h.record(result{Name: name, Status: "PASS", ExitCode: 0})
	return true
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
	Raw             string
	ContractVersion int `json:"contract_version"`
	Producer        struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"producer"`
	RunID     string `json:"run_id"`
	ReadyPath string `json:"ready_path"`
	Project   struct {
		Alias             string `json:"alias"`
		Remote            string `json:"remote"`
		CloneURL          string `json:"clone_url"`
		SourcePath        string `json:"source_path"`
		SourcePathMissing bool   `json:"source_path_missing"`
	} `json:"project"`
	Base              string                           `json:"base"`
	Profile           string                           `json:"profile"`
	ResolvedCommit    string                           `json:"resolved_commit"`
	BaseProvenance    agentCommandBase                 `json:"base_provenance"`
	CloneStrategy     reportLiveCloneStrategySelection `json:"clone_strategy"`
	Env               agentEnvMetadata                 `json:"env"`
	Toolchain         []agentToolchainStatus           `json:"toolchain"`
	ReadinessDecision string                           `json:"readiness_decision"`
	HandoffDocs       []agentHandoffDoc                `json:"handoff_docs"`
	Diagnostics       agentDiagnostics                 `json:"diagnostics"`
	Commands          []agentCommand                   `json:"commands"`
}

type agentToolchainStatus struct {
	Name    string                `json:"name"`
	Status  string                `json:"status"`
	Project toolchainProjectFacts `json:"project"`
	Host    toolchainHostFacts    `json:"host"`
}

type agentEnvMetadata struct {
	Requirements          []agentEnvRequirement `json:"requirements"`
	AllowedScopes         []string              `json:"allowed_scopes"`
	MaterializationStatus string                `json:"materialization_status"`
	Bundle                agentEnvBundle        `json:"bundle"`
}

type agentEnvRequirement struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type agentEnvBundle struct {
	Present bool   `json:"present"`
	Path    string `json:"path"`
	Format  string `json:"format"`
	Values  string `json:"values"`
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

type agentCommand struct {
	Label      string           `json:"label"`
	CWD        string           `json:"cwd"`
	Env        agentCommandEnv  `json:"env"`
	Base       agentCommandBase `json:"base_provenance"`
	ExitCode   int              `json:"exit_code"`
	Duration   string           `json:"duration"`
	StdoutPath string           `json:"stdout_path"`
	StderrPath string           `json:"stderr_path"`
	ExecutedAt string           `json:"executed_at"`
}

type agentCommandEnv struct {
	Mode   string   `json:"mode"`
	Keys   []string `json:"keys"`
	Values string   `json:"values"`
}

type agentCommandBase struct {
	Base           string `json:"base"`
	ResolvedCommit string `json:"resolved_commit"`
	Remote         string `json:"remote"`
	FetchedBase    string `json:"fetched_base"`
	FetchedCommit  string `json:"fetched_commit"`
	PreparedHEAD   string `json:"prepared_head"`
	MatchesFetched bool   `json:"matches_fetched"`
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

func repoRootForMode(mode string) (string, error) {
	root, err := repoRoot()
	if err == nil {
		return root, nil
	}
	if mode != modeLive {
		return "", err
	}
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		return "", err
	}
	return wd, nil
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
	if h.mode == modePackaged || h.mode == modeLive {
		return h.runDir
	}
	return h.root
}

func e2eMode() string {
	switch os.Getenv("CODEMESH_E2E_MODE") {
	case modePackaged:
		return modePackaged
	case modeLive:
		return modeLive
	default:
		return modeSource
	}
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

func liveConfigFromEnv(lookup func(string) (string, bool)) liveConfig {
	optInValue, optInSet := lookup("CODEMESH_E2E_LIVE")
	strictValue, strictSet := lookup("CODEMESH_E2E_LIVE_STRICT")
	targetValue, _ := lookup("CODEMESH_E2E_LIVE_TARGETS")
	targets := splitLiveTargets(targetValue)
	if len(targets) == 0 {
		targets = []string{"github remote smoke"}
	}
	return liveConfig{
		OptIn:   optInSet && truthyEnv(optInValue),
		Strict:  strictSet && truthyEnv(strictValue),
		Targets: targets,
	}
}

func liveTargetEnabled(cfg liveConfig, target string) bool {
	for _, selected := range cfg.Targets {
		if liveTargetMatches(selected, target) {
			return true
		}
	}
	return false
}

func liveTargetMatches(selected, target string) bool {
	selected = strings.ToLower(strings.TrimSpace(selected))
	target = strings.ToLower(strings.TrimSpace(target))
	if selected == target || selected == "all" {
		return true
	}
	switch target {
	case liveTargetGitHub:
		return selected == "github" || selected == "github remote"
	case liveTargetProvider:
		return selected == "provider" || selected == "live provider"
	case liveTargetToolchain:
		return selected == "toolchain" || selected == "host toolchain" || selected == "toolchain host"
	case liveTargetDesktop:
		return selected == "desktop" || selected == "peekaboo" || selected == "desktop peekaboo" || selected == "peekaboo desktop"
	case liveTargetOwnedHost:
		return selected == "owned-host" || selected == "owned host" || selected == "owned-hosts" || selected == "owned hosts"
	default:
		return false
	}
}

func findPeekabooBinary() (string, error) {
	if path := strings.TrimSpace(os.Getenv("PEEKABOO_BIN")); path != "" {
		if err := ensureExecutable(path); err != nil {
			return "", err
		}
		return path, nil
	}
	if err := ensureExecutable("/opt/homebrew/bin/peekaboo"); err == nil {
		return "/opt/homebrew/bin/peekaboo", nil
	}
	if path, err := exec.LookPath("peekaboo"); err == nil {
		return path, nil
	}
	return "", errors.New("peekaboo executable not found at /opt/homebrew/bin/peekaboo or PATH")
}

func parsePeekabooPermissions(raw []byte) (reportPeekabooPermissions, error) {
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Source      string `json:"source"`
			Permissions []struct {
				Name       string `json:"name"`
				IsRequired bool   `json:"isRequired"`
				IsGranted  bool   `json:"isGranted"`
			} `json:"permissions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return reportPeekabooPermissions{}, err
	}
	if !payload.Success {
		return reportPeekabooPermissions{}, errors.New("peekaboo permissions command did not report success")
	}
	var got reportPeekabooPermissions
	got.Source = payload.Data.Source
	required := map[string]*bool{
		"screen recording": &got.ScreenRecording,
		"accessibility":    &got.Accessibility,
	}
	for _, permission := range payload.Data.Permissions {
		slot, ok := required[strings.ToLower(strings.TrimSpace(permission.Name))]
		if !ok {
			continue
		}
		*slot = permission.IsRequired && permission.IsGranted
	}
	var missing []string
	if !got.ScreenRecording {
		missing = append(missing, "Screen Recording")
	}
	if !got.Accessibility {
		missing = append(missing, "Accessibility")
	}
	if len(missing) != 0 {
		return got, errors.New("peekaboo missing required permissions: " + strings.Join(missing, ", "))
	}
	return got, nil
}

func liveGitHubRemoteFromEnv(lookup func(string) (string, bool)) string {
	if value, ok := lookup("CODEMESH_LIVE_GITHUB_REPO"); ok {
		if remote := strings.TrimSpace(value); remote != "" {
			return remote
		}
	}
	return defaultLiveGitHubRemote
}

func liveProviderSmokeConfigFromEnv(lookup func(string) (string, bool)) (liveProviderSmokeConfig, bool, string) {
	required := []string{
		"CODEMESH_E2E_LIVE_PROVIDER",
		"CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT",
		"CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF",
		"CODEMESH_E2E_LIVE_PROVIDER_SCOPE",
	}
	values := make(map[string]string, len(required))
	var missing []string
	for _, name := range required {
		value, ok := lookup(name)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			missing = append(missing, name)
			continue
		}
		values[name] = value
	}
	if len(missing) != 0 {
		return liveProviderSmokeConfig{}, false, "live provider smoke requires exact env vars: " + strings.Join(missing, ", ")
	}
	return liveProviderSmokeConfig{
		Provider:    values["CODEMESH_E2E_LIVE_PROVIDER"],
		Requirement: values["CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT"],
		SecretRef:   values["CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF"],
		Scope:       values["CODEMESH_E2E_LIVE_PROVIDER_SCOPE"],
	}, true, ""
}

func validateLiveGitHubRemote(remote string) error {
	parsed, err := url.Parse(strings.TrimSpace(remote))
	if err != nil || parsed.Scheme != "https" || strings.ToLower(parsed.Hostname()) != "github.com" || strings.Trim(parsed.Path, "/") == "" {
		return errors.New("CODEMESH_LIVE_GITHUB_REPO must be an HTTPS GitHub remote such as https://github.com/OWNER/REPO.git")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("CODEMESH_LIVE_GITHUB_REPO must not include credentials, query strings, or fragments")
	}
	return nil
}

func parseRemoteDefaultBranch(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ref: refs/heads/") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "ref:" || fields[2] != "HEAD" {
			continue
		}
		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		if branch != "" {
			return branch, nil
		}
	}
	return "", errors.New("remote default branch was not discoverable from HEAD symref")
}

func isSkippableLiveGitHubSmokeError(err error) bool {
	if err == nil {
		return false
	}
	detail := strings.ToLower(err.Error())
	skippableFragments := []string{
		"executable file not found",
		"could not resolve host",
		"failed to connect",
		"connection refused",
		"connection reset",
		"connection timed out",
		"operation timed out",
		"timeout after",
		"network is unreachable",
		"temporary failure",
		"tls handshake timeout",
		"early eof",
		"remote end hung up",
		"proxyconnect tcp",
		"fetch-failed",
		"rate limit",
		"too many requests",
		"returned error: 429",
		"returned error: 403",
	}
	return containsAnyFragment(detail, skippableFragments)
}

func truthyEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func splitLiveTargets(value string) []string {
	var targets []string
	for _, raw := range strings.Split(value, ",") {
		target := strings.TrimSpace(raw)
		if target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

func defaultLiveLockDir() string {
	if path := strings.TrimSpace(os.Getenv("CODEMESH_E2E_LIVE_LOCK_DIR")); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), "codemesh-e2e-live-locks")
}

func acquireLiveLock(dir, label string, now time.Time, pid int, staleAfter time.Duration) (*liveLock, error) {
	if strings.TrimSpace(label) == "" {
		return nil, errors.New("live lock label is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	path := filepath.Join(dir, "host-"+slug(host)+".lock")
	metadata := liveLockMetadata{
		PID:       pid,
		Host:      host,
		Label:     label,
		StartedAt: now.UTC().Format(time.RFC3339),
		Token:     liveLockToken(pid, now),
	}
	if err := writeLiveLock(path, metadata); err == nil {
		return &liveLock{path: path, token: metadata.Token}, nil
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	guard, err := acquireLiveCleanupGuard(path, now, staleAfter)
	if err != nil {
		return nil, err
	}
	defer guard.release()
	if err := removeStaleLiveLock(path, now, staleAfter); err != nil {
		return nil, err
	}
	if err := writeLiveLock(path, metadata); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", errLiveLockHeld, path)
		}
		return nil, err
	}
	return &liveLock{path: path, token: metadata.Token}, nil
}

func acquireLiveLockForHost(dir, hostName, label string, now time.Time, pid int, staleAfter time.Duration) (*liveLock, error) {
	if strings.TrimSpace(hostName) == "" {
		return nil, errors.New("owned-host lock host name is required")
	}
	if strings.TrimSpace(label) == "" {
		return nil, errors.New("live lock label is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "host-"+slug(hostName)+".lock")
	metadata := liveLockMetadata{
		PID:       pid,
		Host:      hostName,
		Label:     label,
		StartedAt: now.UTC().Format(time.RFC3339),
		Token:     liveLockToken(pid, now),
	}
	if err := removeStaleLiveLock(path, now, staleAfter); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", errLiveLockHeld, path)
		}
		return nil, err
	}
	defer file.Close()
	if err := json.NewEncoder(file).Encode(metadata); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return &liveLock{path: path, token: metadata.Token}, nil
}

func writeLiveLock(path string, metadata liveLockMetadata) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	data, marshalErr := json.MarshalIndent(metadata, "", "  ")
	if marshalErr == nil {
		data = append(data, '\n')
		_, marshalErr = file.Write(data)
	}
	closeErr := file.Close()
	if marshalErr != nil {
		_ = os.Remove(path)
		return marshalErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return closeErr
	}
	return nil
}

func liveLockToken(pid int, now time.Time) string {
	return fmt.Sprintf("%d-%d", pid, now.UTC().UnixNano())
}

func acquireLiveCleanupGuard(lockPath string, now time.Time, staleAfter time.Duration) (*liveCleanupGuard, error) {
	path := lockPath + ".cleanup"
	if err := removeStaleCleanupGuard(path, now, staleAfter); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", errLiveLockHeld, lockPath)
		}
		return nil, err
	}
	_, writeErr := fmt.Fprintf(file, "%d %s\n", os.Getpid(), now.UTC().Format(time.RFC3339))
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return nil, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return nil, closeErr
	}
	return &liveCleanupGuard{path: path}, nil
}

func removeStaleCleanupGuard(path string, now time.Time, staleAfter time.Duration) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if staleAfter > 0 && now.Sub(info.ModTime()) >= staleAfter {
		return os.Remove(path)
	}
	return nil
}

func removeStaleLiveLock(path string, now time.Time, staleAfter time.Duration) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var metadata liveLockMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return removeMalformedStaleLiveLock(path, now, staleAfter, fmt.Errorf("live e2e lock is unreadable: %w", err))
	}
	startedAt, err := time.Parse(time.RFC3339, metadata.StartedAt)
	if err != nil {
		return removeMalformedStaleLiveLock(path, now, staleAfter, fmt.Errorf("live e2e lock has invalid start time: %w", err))
	}
	if staleAfter <= 0 || now.Sub(startedAt) < staleAfter {
		return nil
	}
	return os.Remove(path)
}

func removeMalformedStaleLiveLock(path string, now time.Time, staleAfter time.Duration, parseErr error) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if staleAfter > 0 && now.Sub(info.ModTime()) >= staleAfter {
		return os.Remove(path)
	}
	return parseErr
}

func (l *liveLock) release() error {
	if l == nil || l.path == "" {
		return nil
	}
	data, err := os.ReadFile(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var metadata liveLockMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil
	}
	if metadata.Token != l.token {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (g *liveCleanupGuard) release() error {
	if g == nil || g.path == "" {
		return nil
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

func (h *harness) forbiddenHostPathMarkers() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(home, ".codemesh"),
		filepath.Join(home, "Projects"),
		filepath.Join(home, "Code"),
	}
	var markers []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if inside, err := pathInside(h.tmp, candidate); err == nil && inside {
			continue
		}
		markers = append(markers, candidate)
	}
	return markers
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

func waitForFileFragment(path, fragment string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if strings.Contains(string(data), fragment) {
				return nil
			}
			lastErr = fmt.Errorf("%s did not include completion marker yet", path)
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout waiting for transcript")
	}
	return fmt.Errorf("timeout waiting for %q in %s: %w", fragment, path, lastErr)
}

func transcriptStarted(path string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(data), "CODEMESH_PEEKABOO_SMOKE_BEGIN")
}

func screenshotHasVisiblePixels(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	img, _, err := image.Decode(file)
	if err != nil {
		return err
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += max(1, bounds.Dy()/100) {
		for x := bounds.Min.X; x < bounds.Max.X; x += max(1, bounds.Dx()/100) {
			r, g, b, _ := img.At(x, y).RGBA()
			if r > 0x0800 || g > 0x0800 || b > 0x0800 {
				return nil
			}
		}
	}
	return errors.New("screenshot contains no visible nonblack pixels")
}
