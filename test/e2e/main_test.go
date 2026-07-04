package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestPrintRedactsFakeSecretsOnFailure(t *testing.T) {
	h := testHarness(t)
	var out bytes.Buffer
	h.output = &out

	h.print(result{
		Name:     "secret failure",
		Status:   "FAIL",
		ExitCode: 1,
		Error:    "error " + fakeEnvFixtureKeySecret(),
		Stdout:   "stdout " + fakeEnvFixtureFileSecret(),
		Stderr:   "stderr " + fakeEnvFixtureKeySecret(),
	})

	got := out.String()
	if containsAnySecret(got, fakeEnvFixtureSecrets()) {
		t.Fatalf("printed output leaked fake secret marker")
	}
	if strings.Count(got, "[REDACTED]") != 3 {
		t.Fatalf("printed output redaction count = %d, want 3:\n%s", strings.Count(got, "[REDACTED]"), got)
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

func TestWriteReportIncludesAuditMetadataSummaryAndSecretSafety(t *testing.T) {
	h := testHarness(t)
	fakeSecret := fakeEnvFixtureSecret()
	h.bin = filepath.Join(h.tmp, "bin", "codemesh")
	h.mode = modeSource
	h.startedAt = time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	h.redactions = []string{fakeSecret}
	h.reportPath = filepath.Join(h.tmp, "reports", "e2e.json")
	h.results = []result{
		{Name: "passing case", Status: "PASS", ExitCode: 0},
		{Name: "failing case", Status: "FAIL", ExitCode: 1, Stderr: "missing CODEMESH_E2E_REQUIRED_ENV, value " + fakeSecret},
		{Name: "skipped case", Status: "SKIP", Error: "not applicable"},
	}

	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), fakeSecret) {
		t.Fatalf("report leaked fake env fixture value:\n%s", data)
	}

	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.StartedAt != "2026-06-23T12:00:00Z" {
		t.Fatalf("started_at = %q, want fixed run start", got.StartedAt)
	}
	if got.Mode != modeSource || got.Binary.Path != h.bin || got.Binary.Kind != "built-from-source" || got.Binary.External {
		t.Fatalf("binary metadata = %#v mode = %q", got.Binary, got.Mode)
	}
	if got.Host.OS != runtime.GOOS || got.Host.Arch != runtime.GOARCH || got.Host.GoVersion != runtime.Version() {
		t.Fatalf("host metadata = %#v", got.Host)
	}
	if got.Isolation.CodeMeshHome != h.codemeshHome || got.Isolation.Home != h.home || got.Isolation.Workspace != h.workspace || got.Isolation.RunDir != h.runDir {
		t.Fatalf("isolation metadata = %#v", got.Isolation)
	}
	if got.Summary.Pass != 1 || got.Summary.Fail != 1 || got.Summary.Skip != 1 || got.Summary.Total != 3 {
		t.Fatalf("summary = %#v", got.Summary)
	}
	if len(got.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(got.Results))
	}
	if !got.SecretSafety.Enabled || got.SecretSafety.RedactedValues != 1 {
		t.Fatalf("secret safety = %#v", got.SecretSafety)
	}
}

func TestWriteReportDistinguishesPackagedBinary(t *testing.T) {
	h := testHarness(t)
	h.bin = filepath.Join(h.tmp, "dist", "codemesh")
	h.mode = modePackaged
	h.externalBin = true
	h.reportPath = filepath.Join(h.tmp, "reports", "packaged.json")

	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != modePackaged || got.Binary.Path != h.bin || got.Binary.Kind != "external-packaged" || !got.Binary.External {
		t.Fatalf("packaged metadata = mode %q binary %#v", got.Mode, got.Binary)
	}
}

func TestLiveOptInParsing(t *testing.T) {
	cfg := liveConfigFromEnv(mapLookup(nil))
	if cfg.OptIn || cfg.Strict {
		t.Fatalf("default live config = %#v, want no opt-in and non-strict", cfg)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0] != "github remote smoke" {
		t.Fatalf("default targets = %#v", cfg.Targets)
	}

	cfg = liveConfigFromEnv(mapLookup(map[string]string{
		"CODEMESH_E2E_LIVE":         "1",
		"CODEMESH_E2E_LIVE_STRICT":  "true",
		"CODEMESH_E2E_LIVE_TARGETS": "github, provider smoke",
	}))
	if !cfg.OptIn || !cfg.Strict {
		t.Fatalf("enabled live config = %#v, want opt-in and strict", cfg)
	}
	if strings.Join(cfg.Targets, "|") != "github|provider smoke" {
		t.Fatalf("targets = %#v", cfg.Targets)
	}

	cfg = liveConfigFromEnv(mapLookup(map[string]string{"CODEMESH_E2E_LIVE": "0"}))
	if cfg.OptIn {
		t.Fatalf("CODEMESH_E2E_LIVE=0 enabled live config")
	}
}

func TestLiveTargetSelection(t *testing.T) {
	cfg := liveConfigFromEnv(mapLookup(map[string]string{
		"CODEMESH_E2E_LIVE":         "1",
		"CODEMESH_E2E_LIVE_TARGETS": "toolchain",
	}))

	if !liveTargetEnabled(cfg, liveTargetToolchain) {
		t.Fatalf("toolchain target not enabled: %#v", cfg.Targets)
	}
	if liveTargetEnabled(cfg, liveTargetGitHub) || liveTargetEnabled(cfg, liveTargetProvider) {
		t.Fatalf("non-toolchain targets enabled for explicit toolchain selection: %#v", cfg.Targets)
	}

	cfg = liveConfigFromEnv(mapLookup(map[string]string{"CODEMESH_E2E_LIVE": "1"}))
	if !liveTargetEnabled(cfg, liveTargetGitHub) || liveTargetEnabled(cfg, liveTargetToolchain) {
		t.Fatalf("default target selection = %#v, want GitHub only", cfg.Targets)
	}

	cfg = liveConfigFromEnv(mapLookup(map[string]string{
		"CODEMESH_E2E_LIVE":         "1",
		"CODEMESH_E2E_LIVE_TARGETS": "desktop,peekaboo",
	}))
	if !liveTargetEnabled(cfg, liveTargetDesktop) {
		t.Fatalf("desktop target not enabled: %#v", cfg.Targets)
	}
	if liveTargetEnabled(cfg, liveTargetGitHub) || liveTargetEnabled(cfg, liveTargetToolchain) {
		t.Fatalf("non-desktop targets enabled for explicit desktop selection: %#v", cfg.Targets)
	}

	cfg = liveConfigFromEnv(mapLookup(map[string]string{
		"CODEMESH_E2E_LIVE":         "1",
		"CODEMESH_E2E_LIVE_TARGETS": "owned-host, owned hosts",
	}))
	if !liveTargetEnabled(cfg, liveTargetOwnedHost) {
		t.Fatalf("owned-host target not enabled: %#v", cfg.Targets)
	}
	if liveTargetEnabled(cfg, liveTargetGitHub) || liveTargetEnabled(cfg, liveTargetDesktop) {
		t.Fatalf("non-owned-host targets enabled for explicit owned-host selection: %#v", cfg.Targets)
	}
}

func TestOwnedHostDefaultRecordsSkipAndReportMetadata(t *testing.T) {
	t.Setenv("CODEMESH_E2E_OWNED_HOSTS", "")

	h := testHarness(t)
	h.live = &reportLive{OwnedHosts: &reportLiveOwnedHosts{}}

	h.caseLiveOwnedHostSmoke(liveConfig{})

	if len(h.results) != 1 || h.results[0].Name != "owned-host inventory config" || h.results[0].Status != "SKIP" {
		t.Fatalf("results = %#v, want owned-host config skip", h.results)
	}
	if h.live.OwnedHosts == nil || h.live.OwnedHosts.Status != "skipped" || !strings.Contains(h.live.OwnedHosts.SkipReason, "CODEMESH_E2E_OWNED_HOSTS") {
		t.Fatalf("owned-host report = %#v", h.live.OwnedHosts)
	}
}

func TestOwnedHostStrictMissingInventoryFails(t *testing.T) {
	t.Setenv("CODEMESH_E2E_OWNED_HOSTS", "")

	h := testHarness(t)
	h.live = &reportLive{OwnedHosts: &reportLiveOwnedHosts{}}

	h.caseLiveOwnedHostSmoke(liveConfig{Strict: true})

	if len(h.results) != 1 || h.results[0].Status != "FAIL" {
		t.Fatalf("results = %#v, want strict owned-host config failure", h.results)
	}
	if h.live.OwnedHosts == nil || h.live.OwnedHosts.Status != "failed" {
		t.Fatalf("owned-host report = %#v, want failed", h.live.OwnedHosts)
	}
}

func TestOwnedHostInventoryFromEnvIncludesStaticTargets(t *testing.T) {
	t.Setenv("CODEMESH_E2E_OWNED_HOSTS", "local-macos,hermes-win,hermes-vm")
	t.Setenv("CODEMESH_E2E_EXTRA_LINUX_HOST", "builder.example.invalid")

	got, err := ownedHostInventoryFromEnv(os.LookupEnv)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("inventory count = %d, want 4: %#v", len(got), got)
	}
	want := []ownedHostTarget{
		{Name: "local-macos", Kind: "local", TargetOS: "darwin"},
		{Name: "hermes-win", Kind: "ssh", TargetOS: "windows", Address: "hermes-win"},
		{Name: "hermes-vm", Kind: "ssh", TargetOS: "linux", Address: "hermes-vm"},
		{Name: "extra-linux", Kind: "ssh", TargetOS: "linux", Address: "builder.example.invalid"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inventory[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestWriteReportIncludesOwnedHostProofMetadata(t *testing.T) {
	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "dist", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live-owned-host.json")
	h.live = &reportLive{
		OptIn:   true,
		Targets: []string{liveTargetOwnedHost},
		OwnedHosts: &reportLiveOwnedHosts{
			Status:       "pass",
			BundlePath:   "tmp/e2e-owned-host",
			SecretSafety: "pass",
			Inventory: []reportLiveOwnedHost{{
				Name:                   "local-macos",
				Kind:                   "local",
				TargetOS:               "darwin",
				Status:                 "pass",
				Facts:                  reportOwnedHostFacts{OS: "darwin", Arch: "arm64", GoVersion: "go1.26.3", Shell: "sh"},
				Doctor:                 []reportOwnedHostDoctor{{Name: "git", Status: "pass", Duration: "1ms"}},
				Lock:                   &reportOwnedHostLock{Path: "/tmp/codemesh-e2e-live-locks/host-local-macos.lock", Label: "codemesh owned-host local-macos", StartedAt: "2026-07-04T12:00:00Z"},
				CommandDurations:       map[string]string{"agent_run": "10ms"},
				Artifacts:              []reportOwnedHostArtifact{{Command: "agent_run_contract", StdoutPath: "tmp/e2e-owned-host/local-macos/codemesh-run.json"}},
				CodeMeshE2EReportPaths: []string{"tmp/e2e-report.json"},
				SelectedRunIDs:         []string{"run-owned"},
				MachineIDs:             []string{"machine-a", "machine-b"},
				ManifestLocation:       "tmp/e2e-owned-host/local-macos/manifest",
				HydratedProjectID:      "git://127.0.0.1:12345/owned-target",
				CleanupStatus:          "managed agent run cleaned",
				Visual:                 &reportOwnedHostVisualProof{Status: "skipped", SkipReason: "visual proof unavailable"},
				SecretSafety:           "pass",
			}},
		},
	}

	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Live == nil || got.Live.OwnedHosts == nil || got.Live.OwnedHosts.Status != "pass" {
		t.Fatalf("owned-host report missing: %#v", got.Live)
	}
	host := got.Live.OwnedHosts.Inventory[0]
	if host.SelectedRunIDs[0] != "run-owned" || host.MachineIDs[1] != "machine-b" || host.ManifestLocation != "tmp/e2e-owned-host/local-macos/manifest" || host.Artifacts[0].StdoutPath == "" {
		t.Fatalf("owned-host host report = %#v", host)
	}
	if host.Visual == nil || host.Visual.Status != "skipped" || host.SecretSafety != "pass" {
		t.Fatalf("owned-host visual/secret report = %#v", host)
	}
}

func TestLiveGitHubRemoteDefaultsAndOverride(t *testing.T) {
	if got := liveGitHubRemoteFromEnv(mapLookup(nil)); got != defaultLiveGitHubRemote {
		t.Fatalf("default GitHub remote = %q, want %q", got, defaultLiveGitHubRemote)
	}
	if got := liveGitHubRemoteFromEnv(mapLookup(map[string]string{"CODEMESH_LIVE_GITHUB_REPO": " https://github.com/BramVR/other.git "})); got != "https://github.com/BramVR/other.git" {
		t.Fatalf("override GitHub remote = %q", got)
	}
}

func TestParseRemoteDefaultBranchFromSymref(t *testing.T) {
	output := "ref: refs/heads/trunk\tHEAD\n0123456789abcdef\tHEAD\n"
	got, err := parseRemoteDefaultBranch(output)
	if err != nil {
		t.Fatal(err)
	}
	if got != "trunk" {
		t.Fatalf("default branch = %q, want trunk", got)
	}
	if _, err := parseRemoteDefaultBranch("0123456789abcdef\tHEAD\n"); err == nil {
		t.Fatalf("parseRemoteDefaultBranch accepted missing symref")
	}
}

func TestParsePeekabooPermissionsRequiresScreenRecordingAndAccessibility(t *testing.T) {
	raw := `{
	  "success": true,
	  "data": {
	    "source": "local",
	    "permissions": [
	      {"name": "Screen Recording", "isRequired": true, "isGranted": true},
	      {"name": "Accessibility", "isRequired": true, "isGranted": true}
	    ]
	  }
	}`
	got, err := parsePeekabooPermissions([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "local" || !got.ScreenRecording || !got.Accessibility {
		t.Fatalf("permissions = %#v, want granted local screen/accessibility", got)
	}

	raw = `{"success":true,"data":{"permissions":[{"name":"Screen Recording","isRequired":true,"isGranted":true}]}}`
	if _, err := parsePeekabooPermissions([]byte(raw)); err == nil || !strings.Contains(err.Error(), "Accessibility") {
		t.Fatalf("missing Accessibility error = %v", err)
	}
}

func TestWriteReportIncludesLiveDesktopArtifacts(t *testing.T) {
	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "dist", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	h.live = &reportLive{
		OptIn:   true,
		Strict:  false,
		Targets: []string{liveTargetDesktop},
		Desktop: &reportLiveDesktop{
			Status:         "pass",
			PeekabooPath:   "/opt/homebrew/bin/peekaboo",
			TerminalApp:    "Terminal",
			ScreenshotPath: h.repoArtifactPath(filepath.Join(h.root, "tmp", "e2e-peekaboo-desktop.png")),
			TranscriptPath: h.repoArtifactPath(filepath.Join(h.root, "tmp", "e2e-peekaboo-transcript.txt")),
			SecretSafety:   "pass",
			Permissions: &reportPeekabooPermissions{
				Source:          "local",
				ScreenRecording: true,
				Accessibility:   true,
			},
		},
	}

	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Live == nil || got.Live.Desktop == nil {
		t.Fatalf("live desktop report missing: %#v", got.Live)
	}
	if got.Live.Desktop.ScreenshotPath != "tmp/e2e-peekaboo-desktop.png" {
		t.Fatalf("screenshot path = %q, want stable repo-relative tmp path", got.Live.Desktop.ScreenshotPath)
	}
	if got.Live.Desktop.TranscriptPath != "tmp/e2e-peekaboo-transcript.txt" {
		t.Fatalf("transcript path = %q, want stable repo-relative tmp path", got.Live.Desktop.TranscriptPath)
	}
	if got.Live.Desktop.SecretSafety != "pass" {
		t.Fatalf("desktop report = %#v", got.Live.Desktop)
	}
	if got.Live.Desktop.Permissions == nil || !got.Live.Desktop.Permissions.ScreenRecording || !got.Live.Desktop.Permissions.Accessibility {
		t.Fatalf("desktop permissions = %#v", got.Live.Desktop.Permissions)
	}
}

func TestWriteReportOmitsDesktopPermissionsWhenNotChecked(t *testing.T) {
	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "dist", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	h.live = &reportLive{
		OptIn:   true,
		Targets: []string{liveTargetDesktop},
		Desktop: &reportLiveDesktop{
			Status:       "skipped",
			TerminalApp:  "Terminal",
			SkipReason:   "Peekaboo desktop smoke requires macOS",
			SecretSafety: "not_run",
		},
	}

	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"permissions"`) {
		t.Fatalf("skipped desktop report should omit unchecked permissions:\n%s", data)
	}
}

func TestLiveGitHubRemoteRedaction(t *testing.T) {
	raw := "https://redactme@github.com/BramVR/codemesh.git?x=y#frag"
	got := redactedLiveGitHubRemote(raw)
	if got != "https://redacted@github.com/BramVR/codemesh.git" {
		t.Fatalf("redacted remote = %q", got)
	}
	reason := liveGitHubCommandFailureReason(result{Error: "failed " + raw}, raw)
	if strings.Contains(reason, "redactme") || strings.Contains(reason, "x=y") || strings.Contains(reason, "#frag") {
		t.Fatalf("failure reason leaked credential-bearing remote: %s", reason)
	}
	if got := liveGitHubReportRemote("credential-bearing invalid remote"); got != "invalid CODEMESH_LIVE_GITHUB_REPO" {
		t.Fatalf("invalid report remote = %q", got)
	}
	if got := liveGitHubReportRemote(defaultLiveGitHubRemote); got != defaultLiveGitHubRemote {
		t.Fatalf("valid report remote = %q", got)
	}
}

func TestSkippableLiveGitHubSmokeErrors(t *testing.T) {
	for _, message := range []string{
		"exec: \"git\": executable file not found in $PATH",
		"fatal: unable to access 'https://github.com/BramVR/codemesh.git/': Could not resolve host: github.com",
		"fatal: unable to access 'https://github.com/BramVR/codemesh.git/': The requested URL returned error: 429",
		"remote: rate limit exceeded",
		"blocker: fetch-failed git fetch failed: connection timed out",
	} {
		if !isSkippableLiveGitHubSmokeError(errors.New(message)) {
			t.Fatalf("error was not skippable: %s", message)
		}
	}
}

func TestLiveGitHubCommandFailureSkipClassification(t *testing.T) {
	h := testHarness(t)
	h.live = &reportLive{GitHub: &reportLiveGitHub{SecretSafety: "pending"}}
	nonTransient := result{Name: "live github default branch", Status: "FAIL", Error: "remote: Repository not found.", ExitCode: 128}
	if h.recordLiveGitHubCommandSkipOrFail(liveConfig{}, nonTransient, defaultLiveGitHubRemote) {
		t.Fatalf("non-transient repository failure was converted to skip")
	}
	if len(h.results) != 0 {
		t.Fatalf("unexpected result recorded for non-transient failure: %#v", h.results)
	}

	transient := result{Name: "live github default branch", Status: "FAIL", Stderr: "fatal: unable to access: Could not resolve host: github.com", ExitCode: 128, Duration: "1ms"}
	if !h.recordLiveGitHubCommandSkipOrFail(liveConfig{}, transient, defaultLiveGitHubRemote) {
		t.Fatalf("transient network failure was not converted to skip")
	}
	if len(h.results) != 1 || h.results[0].Status != "SKIP" || h.live.GitHub.SecretSafety != "skipped" {
		t.Fatalf("transient result = %#v secret safety=%q", h.results, h.live.GitHub.SecretSafety)
	}

	h.results = nil
	h.live.GitHub.SecretSafety = "pending"
	timeout := result{Name: "live github seed clone", Status: "FAIL", Error: "timeout after 2m0s", TimedOut: true, ExitCode: -1, Duration: "2m0s"}
	if !h.recordLiveGitHubCommandSkipOrFail(liveConfig{}, timeout, defaultLiveGitHubRemote) {
		t.Fatalf("timed-out network command was not converted to skip")
	}
	if len(h.results) != 1 || h.results[0].Status != "SKIP" {
		t.Fatalf("timeout result = %#v", h.results)
	}
}

func TestLiveProviderSmokeConfigRequiresExactEnvVars(t *testing.T) {
	cfg, ok, reason := liveProviderSmokeConfigFromEnv(mapLookup(nil))
	if ok || cfg.Provider != "" {
		t.Fatalf("default live provider config = %#v ok=%t, want disabled", cfg, ok)
	}
	for _, name := range []string{
		"CODEMESH_E2E_LIVE_PROVIDER",
		"CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT",
		"CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF",
		"CODEMESH_E2E_LIVE_PROVIDER_SCOPE",
	} {
		if !strings.Contains(reason, name) {
			t.Fatalf("missing env reason %q did not mention %s", reason, name)
		}
	}

	cfg, ok, reason = liveProviderSmokeConfigFromEnv(mapLookup(map[string]string{
		"CODEMESH_E2E_LIVE_PROVIDER":             " future-provider ",
		"CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT": " CODEMESH_LIVE_TARGET ",
		"CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF":  " provider://single-target ",
		"CODEMESH_E2E_LIVE_PROVIDER_SCOPE":       " codex ",
	}))
	if !ok || reason != "" {
		t.Fatalf("enabled live provider config = %#v ok=%t reason=%q", cfg, ok, reason)
	}
	if cfg.Provider != "future-provider" || cfg.Requirement != "CODEMESH_LIVE_TARGET" || cfg.SecretRef != "provider://single-target" || cfg.Scope != "codex" {
		t.Fatalf("provider config = %#v", cfg)
	}
}

func TestLiveProviderSmokeRecordsSkipUnlessConfigured(t *testing.T) {
	clearLiveProviderSmokeEnv(t)
	h := testHarness(t)
	h.live = &reportLive{}

	h.caseLiveProviderSmoke(liveConfig{})

	if len(h.results) != 1 || h.results[0].Name != "live provider smoke config" || h.results[0].Status != "SKIP" {
		t.Fatalf("results = %#v, want provider config skip", h.results)
	}
	if h.live.Provider == nil || h.live.Provider.Status != "skipped" || !strings.Contains(h.live.Provider.SkipReason, "CODEMESH_E2E_LIVE_PROVIDER") {
		t.Fatalf("provider report = %#v", h.live.Provider)
	}
}

func TestLiveProviderSmokeConfiguredContractSkipsUntilImplementationExists(t *testing.T) {
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER", "future-provider")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT", "CODEMESH_LIVE_TARGET")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF", "provider://single-target")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_SCOPE", "codex")
	h := testHarness(t)
	h.live = &reportLive{}

	h.caseLiveProviderSmoke(liveConfig{})

	if len(h.results) != 1 || h.results[0].Name != "live provider smoke" || h.results[0].Status != "SKIP" {
		t.Fatalf("results = %#v, want implementation skip", h.results)
	}
	if h.live.Provider == nil || h.live.Provider.Status != "skipped" || h.live.Provider.Provider != "future-provider" || h.live.Provider.Requirement != "CODEMESH_LIVE_TARGET" || h.live.Provider.Scope != "codex" {
		t.Fatalf("provider report = %#v", h.live.Provider)
	}
	if h.live.Provider.SecretRefConfigured != true || strings.Contains(h.live.Provider.SkipReason, "provider://single-target") {
		t.Fatalf("provider report leaked or missed secret-ref metadata: %#v", h.live.Provider)
	}
	h.mode = modeLive
	h.reportPath = filepath.Join(h.tmp, "reports", "live-provider.json")
	if err := h.writeReport(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "provider://single-target") {
		t.Fatalf("live provider report leaked configured secret ref:\n%s", data)
	}
}

func TestLiveToolchainOptionalToolSkipOrFail(t *testing.T) {
	h := testHarness(t)
	h.live = &reportLive{Toolchain: &reportLiveToolchain{}}
	fixture := reportLiveToolchainFixture{
		Name:    "toolchain-package",
		Kind:    "package manager fixture",
		Status:  "skipped",
		Project: toolchainProjectFacts{Requirement: "npm"},
	}

	h.recordLiveToolchainSkipOrFail(liveConfig{}, "live toolchain npm prerequisite", "optional host tool \"npm\" not found", fixture)

	if len(h.results) != 1 || h.results[0].Status != "SKIP" {
		t.Fatalf("non-strict result = %#v, want SKIP", h.results)
	}
	if h.live.Toolchain.Fixtures[0].Project.Requirement != "npm" || h.live.Toolchain.Fixtures[0].Status != "skipped" {
		t.Fatalf("toolchain report fixture = %#v", h.live.Toolchain.Fixtures)
	}

	h.results = nil
	h.live.SkipReasons = nil
	h.live.Toolchain.SkipReasons = nil
	h.live.Toolchain.Fixtures = nil
	h.recordLiveToolchainSkipOrFail(liveConfig{Strict: true}, "live toolchain npm prerequisite", "optional host tool \"npm\" not found", fixture)

	if len(h.results) != 1 || h.results[0].Status != "FAIL" {
		t.Fatalf("strict result = %#v, want FAIL", h.results)
	}
	if h.live.Toolchain.Fixtures[0].Status != "failed" {
		t.Fatalf("strict toolchain report fixture = %#v, want failed", h.live.Toolchain.Fixtures)
	}
}

func clearLiveProviderSmokeEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER", "")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT", "")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF", "")
	t.Setenv("CODEMESH_E2E_LIVE_PROVIDER_SCOPE", "")
}

func TestLiveReportPathIsolationIgnoresIntentionalBinaryAndLockMetadata(t *testing.T) {
	forbidden := []string{filepath.Join("home", "Projects")}
	r := report{
		Binary: reportBinary{Path: filepath.Join("home", "Projects", "codemesh", "dist", "codemesh")},
		Live:   &reportLive{LockPath: filepath.Join("home", "Projects", "locks", "host.lock")},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if liveReportContainsForbiddenPaths(data, forbidden) {
		t.Fatalf("binary or lock metadata was treated as live state leakage")
	}

	r.Results = []result{{Name: "leak", Stdout: filepath.Join("home", "Projects", "codemesh")}}
	data, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if !liveReportContainsForbiddenPaths(data, forbidden) {
		t.Fatalf("result output path leakage was not detected")
	}
}

func TestLiveHydratedStatusAllowsStaleDefaultBranch(t *testing.T) {
	stale := "project: live-github\nstate: stale\npath_present: true\nbase: trunk\nwarning: stale-checkout local base branch advanced\nblockers: none\n"
	if !liveHydratedStatusLooksUsable(stale, "trunk") {
		t.Fatalf("stale hydrated status should be usable")
	}
	fetchFailed := "project: live-github\nstate: stale\npath_present: true\nbase: trunk\nblocker: fetch-failed git fetch failed: connection timed out\n"
	if liveHydratedStatusLooksUsable(fetchFailed, "trunk") {
		t.Fatalf("fetch-failed hydrated status should not be usable")
	}
	missing := "project: live-github\nstate: missing\npath_present: false\nbase: trunk\n"
	if liveHydratedStatusLooksUsable(missing, "trunk") {
		t.Fatalf("missing hydrated status should not be usable")
	}
}

func TestLiveCloneStrategyReportRecordsPassSkipAndStrictFailure(t *testing.T) {
	h := testHarness(t)
	h.live = &reportLive{GitHub: &reportLiveGitHub{}}
	partial := reportLiveCloneStrategySelection{Name: "partial-clone", History: "partial", WorkingTree: "complete", Filter: "blob:none"}
	sparse := reportLiveCloneStrategySelection{Name: "sparse-checkout", History: "full", WorkingTree: "sparse", SparsePaths: []string{"README.md"}}

	h.recordLiveCloneStrategyPass("partial", "agent prepare", partial)
	h.recordLiveCloneStrategySkipOrFail(liveConfig{}, "sparse", "hydrate", sparse, "git sparse checkout unsupported", "2ms", 1)
	h.recordLiveCloneStrategySkipOrFail(liveConfig{Strict: true}, "strict sparse", "hydrate", sparse, "git sparse checkout unsupported", "3ms", 1)

	got := h.live.GitHub.CloneStrategies
	if len(got) != 3 {
		t.Fatalf("clone strategies = %#v, want 3 records", got)
	}
	if got[0].Label != "partial" || got[0].Command != "agent prepare" || got[0].Status != "pass" || got[0].Strategy.Name != "partial-clone" || got[0].Strategy.Filter != "blob:none" {
		t.Fatalf("pass strategy record = %#v", got[0])
	}
	if got[1].Status != "skipped" || got[1].SkipReason == "" || h.results[0].Status != "SKIP" {
		t.Fatalf("non-strict skip record = %#v results=%#v", got[1], h.results)
	}
	if got[2].Status != "failed" || got[2].SkipReason == "" || h.results[1].Status != "FAIL" {
		t.Fatalf("strict failure record = %#v results=%#v", got[2], h.results)
	}
}

func TestSelectLiveSparsePathsUsesTrackedFilePair(t *testing.T) {
	include, exclude, ok := selectLiveSparsePaths([]string{
		".github/workflows/ci.yml",
		"README.md",
		"docs/e2e.md",
		"go.mod",
	})
	if !ok || include != "README.md" || exclude != "go.mod" {
		t.Fatalf("selected sparse paths = %q %q %t, want README.md go.mod true", include, exclude, ok)
	}

	include, exclude, ok = selectLiveSparsePaths([]string{"only.txt"})
	if ok || include != "" || exclude != "" {
		t.Fatalf("single tracked file selection = %q %q %t, want unavailable", include, exclude, ok)
	}
}

func TestLiveAgentPrepareReadyExitClassAllowsWarnings(t *testing.T) {
	for _, exitClass := range []string{"success", "readiness-warning"} {
		if !liveAgentPrepareReadyExitClass(exitClass) {
			t.Fatalf("exit class %q should be accepted for ready live Agent Prep", exitClass)
		}
	}
	for _, exitClass := range []string{"readiness-blocked", "internal-error", ""} {
		if liveAgentPrepareReadyExitClass(exitClass) {
			t.Fatalf("exit class %q should not be accepted for ready live Agent Prep", exitClass)
		}
	}
}

func TestLiveDefaultRecordsSkipAndReportMetadata(t *testing.T) {
	t.Setenv("CODEMESH_E2E_LIVE", "")
	t.Setenv("CODEMESH_E2E_LIVE_STRICT", "")

	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "bin", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	h.startedAt = time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

	if err := h.setupIsolation(); err != nil {
		t.Fatal(err)
	}
	if code := h.runLive(); code != 0 {
		t.Fatalf("runLive exit = %d, want 0", code)
	}

	data, err := os.ReadFile(h.reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var got report
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != modeLive || got.Live == nil {
		t.Fatalf("mode/live metadata = %q %#v", got.Mode, got.Live)
	}
	if got.Live.OptIn || got.Live.Strict {
		t.Fatalf("live opt-in metadata = %#v, want skipped non-strict", got.Live)
	}
	if len(got.Live.SkipReasons) != 1 || !strings.Contains(got.Live.SkipReasons[0], "CODEMESH_E2E_LIVE") {
		t.Fatalf("skip reasons = %#v", got.Live.SkipReasons)
	}
	if got.Summary.Skip != 1 || got.Summary.Pass != 1 || got.Summary.Fail != 0 || got.Summary.Total != 2 {
		t.Fatalf("summary = %#v", got.Summary)
	}
	if got.Isolation.CodeMeshHome != h.codemeshHome || got.Isolation.Home != h.home || got.Isolation.Workspace != h.workspace || got.Isolation.RunDir != h.runDir {
		t.Fatalf("isolation metadata = %#v", got.Isolation)
	}
}

func TestLiveStrictMissingPrerequisitesFails(t *testing.T) {
	t.Setenv("CODEMESH_E2E_LIVE", "1")
	t.Setenv("CODEMESH_E2E_LIVE_STRICT", "1")
	t.Setenv("CODEMESH_E2E_LIVE_LOCK_DIR", filepath.Join(t.TempDir(), "locks"))
	t.Setenv("PATH", "")

	h := testHarness(t)
	h.mode = modeLive
	h.bin = os.Args[0]
	h.externalBin = true
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	if err := h.setupIsolation(); err != nil {
		t.Fatal(err)
	}
	if code := h.runLive(); code == 0 {
		t.Fatalf("runLive exit = 0, want strict prerequisite failure")
	}
	if len(h.results) == 0 || h.results[0].Name != "live github git prerequisite" || h.results[0].Status != "FAIL" {
		t.Fatalf("results = %#v, want strict missing-git failure", h.results)
	}
}

func TestLiveLockContentionSkipsWhenNonStrict(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "locks")
	t.Setenv("CODEMESH_E2E_LIVE", "1")
	t.Setenv("CODEMESH_E2E_LIVE_STRICT", "")
	t.Setenv("CODEMESH_E2E_LIVE_LOCK_DIR", lockDir)
	held, err := acquireLiveLock(lockDir, "held live", time.Now().UTC(), os.Getpid(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "bin", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	if err := h.setupIsolation(); err != nil {
		t.Fatal(err)
	}
	if code := h.runLive(); code != 0 {
		t.Fatalf("runLive exit = %d, want skip success", code)
	}
	if len(h.results) == 0 || h.results[0].Status != "SKIP" || h.results[0].Name != "live e2e lock" {
		t.Fatalf("results = %#v, want lock skip", h.results)
	}
}

func TestLiveLockAcquireReleaseWritesHostMetadata(t *testing.T) {
	dir := t.TempDir()
	startedAt := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)

	lock, err := acquireLiveLock(dir, "unit live", startedAt, os.Getpid(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(lock.path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata liveLockMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.PID != os.Getpid() || metadata.Label != "unit live" || metadata.StartedAt != "2026-06-30T12:00:00Z" || metadata.Host == "" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains after release: %v", err)
	}
}

func TestLiveLockCleansStaleLock(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	stale, err := acquireLiveLock(dir, "old live", old, 12345, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	current := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	lock, err := acquireLiveLock(dir, "new live", current, os.Getpid(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if lock.path != stale.path {
		t.Fatalf("lock path = %s, want stale path %s", lock.path, stale.path)
	}
	data, err := os.ReadFile(lock.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new live") || strings.Contains(string(data), "old live") {
		t.Fatalf("stale lock not replaced:\n%s", data)
	}
}

func TestLiveLockCleanupGuardSerializesStaleRecovery(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	stale, err := acquireLiveLock(dir, "old live", old, 12345, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	guard, err := acquireLiveCleanupGuard(stale.path, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLiveLock(dir, "second live", now, os.Getpid(), time.Hour); !errors.Is(err, errLiveLockHeld) {
		t.Fatalf("acquire with cleanup guard error = %v, want lock held", err)
	}
	if _, err := os.Stat(stale.path); err != nil {
		t.Fatalf("stale lock was removed while cleanup guard held: %v", err)
	}
	if err := guard.release(); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLiveLock(dir, "new live", now, os.Getpid(), time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestLiveLockReleaseDoesNotRemoveNewerLock(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	stale, err := acquireLiveLock(dir, "old live", old, 12345, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	current := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	currentLock, err := acquireLiveLock(dir, "new live", current, os.Getpid(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.release(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(currentLock.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new live") {
		t.Fatalf("new lock was removed or replaced:\n%s", data)
	}
}

func TestLiveLockCleansMalformedStaleLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "host-"+slug("malformed-host")+".lock")
	if err := os.WriteFile(lockPath, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	if err := removeStaleLiveLock(lockPath, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("malformed stale lock remains: %v", err)
	}
}

func TestLiveLockRefusesFreshLock(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	if _, err := acquireLiveLock(dir, "first live", now, os.Getpid(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLiveLock(dir, "second live", now.Add(time.Minute), os.Getpid(), time.Hour); err == nil {
		t.Fatalf("fresh lock was not refused")
	}
}

func TestDefaultCommandDirUsesRepoRoot(t *testing.T) {
	h := testHarness(t)

	if got := h.defaultCommandDir(); got != h.root {
		t.Fatalf("default command dir = %s, want repo root %s", got, h.root)
	}
}

func TestLiveRepoRootFallsBackToWorkingDirectoryWhenGitIsMissing(t *testing.T) {
	t.Setenv("PATH", "")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := repoRootForMode(modeLive)
	if err != nil {
		t.Fatal(err)
	}
	if root != wd {
		t.Fatalf("live root = %s, want working directory %s", root, wd)
	}
	if _, err := repoRootForMode(modeSource); err == nil {
		t.Fatalf("source mode accepted missing git")
	}
}

func TestPackagedCommandDirUsesOutsideRunDir(t *testing.T) {
	h := testHarness(t)
	h.mode = modePackaged
	h.runDir = filepath.Join(t.TempDir(), "outside")

	if got := h.defaultCommandDir(); got != h.runDir {
		t.Fatalf("default command dir = %s, want packaged run dir %s", got, h.runDir)
	}
	inside, err := pathInside(h.root, h.runDir)
	if err != nil {
		t.Fatal(err)
	}
	if inside {
		t.Fatalf("test setup bug: packaged run dir is under repo root")
	}
}

func TestLiveCommandDirUsesOutsideRunDir(t *testing.T) {
	h := testHarness(t)
	h.mode = modeLive
	h.runDir = filepath.Join(t.TempDir(), "live-run")

	if got := h.defaultCommandDir(); got != h.runDir {
		t.Fatalf("default command dir = %s, want live run dir %s", got, h.runDir)
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

func TestOfflineGitFixturesCreateLocalRemotesAndClones(t *testing.T) {
	h := testHarness(t)
	if err := os.MkdirAll(h.workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.home, ".gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	fixtures, err := h.createOfflineGitFixtures()
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures.Projects) != 10 {
		t.Fatalf("fixture count = %d, want 10", len(fixtures.Projects))
	}

	clean := fixtures.Project("clean-repo")
	if clean == nil {
		t.Fatalf("clean fixture missing")
	}
	assertUnder(t, h.tmp, clean.Remote)
	assertUnder(t, h.tmp, clean.Source)
	assertGitStatus(t, h, clean.Source, "")
	assertBareRemote(t, h, clean.Remote)

	dirty := fixtures.Project("dirty-source")
	if dirty == nil {
		t.Fatalf("dirty fixture missing")
	}
	if dirtyStatus := gitStatus(t, h, dirty.Source); dirtyStatus == "" {
		t.Fatalf("dirty fixture status empty")
	}

	remoteDefault := fixtures.Project("remote-default-dev")
	if remoteDefault == nil {
		t.Fatalf("remote default fixture missing")
	}
	if remoteDefault.BaseBranch != "develop" {
		t.Fatalf("remote default base branch = %q, want develop", remoteDefault.BaseBranch)
	}
	if branch := currentBranch(t, h, remoteDefault.Source); branch != "main" {
		t.Fatalf("remote default source branch = %q, want main", branch)
	}
	if stdout, _, err := h.exec(remoteDefault.Remote, "git", "symbolic-ref", "--short", "HEAD"); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(stdout) != "develop" {
		t.Fatalf("remote default HEAD = %q, want develop", strings.TrimSpace(stdout))
	}

	missingPath := fixtures.Project("missing-project-path")
	if missingPath == nil {
		t.Fatalf("missing path fixture missing")
	}
	if _, err := os.Stat(missingPath.Source); !os.IsNotExist(err) {
		t.Fatalf("missing path fixture source exists or stat failed unexpectedly: %v", err)
	}

	missingBase := fixtures.Project("missing-base-branch")
	if missingBase == nil {
		t.Fatalf("missing base fixture missing")
	}
	if missingBase.BaseBranch != "release/missing" {
		t.Fatalf("missing base branch = %q", missingBase.BaseBranch)
	}
	if stdout, _, err := h.exec(h.tmp, "git", "ls-remote", "--heads", missingBase.Remote, missingBase.BaseBranch); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(stdout) != "" {
		t.Fatalf("missing base branch exists unexpectedly: %s", stdout)
	}

	envMissing := fixtures.Project("required-env-missing")
	if envMissing == nil {
		t.Fatalf("env missing fixture missing")
	}
	if len(envMissing.RequiredEnv) != 1 || envMissing.RequiredEnv[0] != "CODEMESH_E2E_REQUIRED_ENV" {
		t.Fatalf("required env = %#v, want fake fixture key", envMissing.RequiredEnv)
	}
	policy, err := os.ReadFile(filepath.Join(envMissing.Source, ".codemesh.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policy), "CODEMESH_E2E_REQUIRED_ENV") {
		t.Fatalf("policy missing fake env key:\n%s", string(policy))
	}
	remotePolicy, _, err := h.exec(h.tmp, "git", "--git-dir", envMissing.Remote, "show", "main:.codemesh.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remotePolicy, "CODEMESH_E2E_REQUIRED_ENV") {
		t.Fatalf("remote policy missing fake env key:\n%s", remotePolicy)
	}

	envWarn := fixtures.Project("required-env-warn")
	if envWarn == nil {
		t.Fatalf("env warn fixture missing")
	}
	if len(envWarn.RequiredEnv) != 1 || envWarn.RequiredEnv[0] != "CODEMESH_E2E_WARN_ENV" {
		t.Fatalf("warn env = %#v, want fake fixture key", envWarn.RequiredEnv)
	}

	envPresent := fixtures.Project("required-env-present")
	if envPresent == nil {
		t.Fatalf("env present fixture missing")
	}
	if len(envPresent.RequiredEnv) != 1 || envPresent.RequiredEnv[0] != "CODEMESH_E2E_PRESENT_ENV" {
		t.Fatalf("present env = %#v, want fake fixture key", envPresent.RequiredEnv)
	}
	envFile, err := os.ReadFile(filepath.Join(envPresent.Source, ".env.local"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envFile), fakeEnvFixtureFileSecret()) {
		t.Fatalf("env present fixture file missing fake secret marker")
	}
	if containsAnySecret(remotePolicy, fakeEnvFixtureSecrets()) {
		t.Fatalf("remote missing-env policy unexpectedly contains fake secret marker")
	}
}

func TestScenarioCreatesFixturesAndIsolatedCommands(t *testing.T) {
	h := testHarness(t)
	h.bin = os.Args[0]

	s, err := h.newScenario("Readiness Status")
	if err != nil {
		t.Fatal(err)
	}
	assertUnder(t, h.tmp, s.codemeshHome)
	if s.codemeshHome == h.codemeshHome {
		t.Fatalf("scenario home reused harness default home")
	}
	clean := s.fixture("clean-repo")
	if clean == nil {
		t.Fatalf("clean fixture missing")
	}
	assertUnder(t, h.tmp, clean.Remote)
	assertUnder(t, h.tmp, clean.Source)

	r := s.commandEnv("scenario helper env", []string{"CODEMESH_E2E_HELPER_PROCESS=1"}, "-test.run=TestHelperProcess", "--", "print-env", "CODEMESH_HOME", "HOME", "GIT_CONFIG_GLOBAL", "CODEMESH_E2E_REQUIRED_ENV")
	if r.Status != "PASS" {
		t.Fatalf("status = %s, error = %s", r.Status, r.Error)
	}
	if !s.expectOutput(r, "CODEMESH_HOME="+s.codemeshHome, "HOME="+h.home, "GIT_CONFIG_GLOBAL="+filepath.Join(h.home, ".gitconfig")) {
		t.Fatalf("env output assertion failed: %#v", h.results)
	}
	if !s.expectNoOutput(r, "CODEMESH_E2E_REQUIRED_ENV=") {
		t.Fatalf("fake env key leaked into command env: %#v", h.results)
	}
	if len(h.results) != 1 || h.results[0].Name != "scenario helper env" {
		t.Fatalf("scenario command was not recorded: %#v", h.results)
	}
	if h.results[0].Status != "PASS" {
		t.Fatalf("scenario command status = %s, error = %s", h.results[0].Status, h.results[0].Error)
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
	case "print-env":
		for _, key := range args[2:] {
			if value, ok := os.LookupEnv(key); ok {
				os.Stdout.WriteString(key + "=" + value + "\n")
			}
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func assertUnder(t *testing.T, parent, child string) {
	t.Helper()
	inside, err := pathInside(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatalf("%s is not under %s", child, parent)
	}
}

func assertBareRemote(t *testing.T, h *harness, remote string) {
	t.Helper()
	stdout, _, err := h.exec(remote, "git", "rev-parse", "--is-bare-repository")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != "true" {
		t.Fatalf("remote %s is not bare: %s", remote, stdout)
	}
}

func assertGitStatus(t *testing.T, h *harness, dir, want string) {
	t.Helper()
	if got := gitStatus(t, h, dir); got != want {
		t.Fatalf("git status = %q, want %q", got, want)
	}
}

func gitStatus(t *testing.T, h *harness, dir string) string {
	t.Helper()
	stdout, _, err := h.exec(dir, "git", "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(stdout)
}

func currentBranch(t *testing.T, h *harness, dir string) string {
	t.Helper()
	stdout, _, err := h.exec(dir, "git", "branch", "--show-current")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(stdout)
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
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
