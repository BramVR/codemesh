package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	if len(cfg.Targets) != 1 || cfg.Targets[0] != "live guardrails" {
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

	h := testHarness(t)
	h.mode = modeLive
	h.bin = filepath.Join(h.tmp, "bin", "codemesh")
	h.reportPath = filepath.Join(h.tmp, "reports", "live.json")
	if err := h.setupIsolation(); err != nil {
		t.Fatal(err)
	}
	if code := h.runLive(); code == 0 {
		t.Fatalf("runLive exit = 0, want strict prerequisite failure")
	}
	if len(h.results) == 0 || h.results[0].Status != "FAIL" {
		t.Fatalf("results = %#v, want first live result failure", h.results)
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
	if len(fixtures.Projects) != 8 {
		t.Fatalf("fixture count = %d, want 8", len(fixtures.Projects))
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
