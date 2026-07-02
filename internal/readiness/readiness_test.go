package readiness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/state"
)

func TestEvaluateProjectReportsPresentCheckout(t *testing.T) {
	project := createReadinessFixture(t, "present")

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StatePresent {
		t.Fatalf("state = %s, want %s; blockers=%v warnings=%v", report.State, StatePresent, report.Blockers, report.Warnings)
	}
	if !report.LocalPathPresent {
		t.Fatalf("LocalPathPresent = false, want true")
	}
	if len(report.Warnings) != 0 || len(report.Blockers) != 0 {
		t.Fatalf("diagnostics = warnings %v blockers %v, want none", report.Warnings, report.Blockers)
	}
}

func TestEvaluateProjectUsesRepoPolicyBaseWhenBaseUnset(t *testing.T) {
	project := createReadinessFixture(t, "policy-base")
	runGit(t, project.LocalPath, "checkout", "-b", "release/main")
	runGit(t, project.LocalPath, "push", "origin", "release/main")
	runGit(t, project.NormalizedRemote, "branch", "-D", "main")
	writeFixturePolicy(t, project, `agent:
  base: release/main
`)
	commitFixturePolicy(t, project)
	runGit(t, project.LocalPath, "push", "origin", "release/main")

	report := evaluateFixture(t, project, Options{CheckRemote: true})

	if report.State != StatePresent {
		t.Fatalf("state = %s, want %s; blockers=%v warnings=%v", report.State, StatePresent, report.Blockers, report.Warnings)
	}
	if report.BaseBranch != "release/main" {
		t.Fatalf("BaseBranch = %q, want release/main", report.BaseBranch)
	}
}

func TestEvaluateHandoffUsesRemoteDefaultBaseWhenPolicyBaseUnset(t *testing.T) {
	project := createReadinessFixture(t, "remote-default")
	runGit(t, project.LocalPath, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(project.LocalPath, "develop.txt"), []byte("develop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Develop branch")
	runGit(t, project.LocalPath, "push", "origin", "develop")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/develop")
	runGit(t, project.LocalPath, "checkout", "main")

	decision, err := EvaluateHandoff(context.Background(), project, Options{})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if decision.Report.State != StatePresent {
		t.Fatalf("state = %s, want %s; blockers=%v warnings=%v", decision.Report.State, StatePresent, decision.Report.Blockers, decision.Report.Warnings)
	}
	if decision.Report.BaseBranch != "develop" {
		t.Fatalf("BaseBranch = %q, want develop", decision.Report.BaseBranch)
	}
	if decision.Report.FetchedBase != "develop" || decision.Report.FetchedCommit == "" {
		t.Fatalf("fetched base/commit = %q/%q", decision.Report.FetchedBase, decision.Report.FetchedCommit)
	}
}

func TestEvaluateHandoffUsesRemoteDefaultBaseWithRelativeOrigin(t *testing.T) {
	project := createReadinessFixture(t, "relative-remote-default")
	runGit(t, project.LocalPath, "checkout", "-b", "develop")
	if err := os.WriteFile(filepath.Join(project.LocalPath, "develop.txt"), []byte("develop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Develop branch")
	runGit(t, project.LocalPath, "push", "origin", "develop")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/develop")
	runGit(t, project.LocalPath, "checkout", "main")
	runGit(t, project.LocalPath, "remote", "set-url", "origin", "../remote.git")

	decision, err := EvaluateHandoff(context.Background(), project, Options{})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if decision.Report.BaseBranch != "develop" {
		t.Fatalf("BaseBranch = %q, want develop", decision.Report.BaseBranch)
	}
	if decision.Report.FetchedBase != "develop" || decision.Report.FetchedCommit == "" {
		t.Fatalf("fetched base/commit = %q/%q", decision.Report.FetchedBase, decision.Report.FetchedCommit)
	}
}

func TestEvaluateHandoffBlocksWhenRemoteDefaultLookupFails(t *testing.T) {
	project := createReadinessFixture(t, "remote-default-failure")
	missingRemote := filepath.Join(t.TempDir(), "missing.git")
	project.NormalizedRemote = missingRemote
	runGit(t, project.LocalPath, "remote", "set-url", "origin", missingRemote)

	decision, err := EvaluateHandoff(context.Background(), project, Options{})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if decision.Report.State != StateStale {
		t.Fatalf("state = %s, want %s", decision.Report.State, StateStale)
	}
	if !hasDiagnostic(decision.Report.Blockers, "fetch-failed") {
		t.Fatalf("blockers = %v, want fetch-failed", decision.Report.Blockers)
	}
	if decision.Report.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main fallback only as blocked diagnostic context", decision.Report.BaseBranch)
	}
}

func TestEvaluateHandoffPolicyBaseOverridesRemoteDefault(t *testing.T) {
	project := createReadinessFixture(t, "policy-over-remote-default")
	runGit(t, project.LocalPath, "checkout", "-b", "develop")
	runGit(t, project.LocalPath, "push", "origin", "develop")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/develop")
	runGit(t, project.LocalPath, "checkout", "main")
	runGit(t, project.LocalPath, "checkout", "-b", "release/agent")
	runGit(t, project.LocalPath, "push", "origin", "release/agent")
	runGit(t, project.LocalPath, "checkout", "main")
	writeFixturePolicy(t, project, `agent:
  base: release/agent
`)
	commitFixturePolicy(t, project)

	decision, err := EvaluateHandoff(context.Background(), project, Options{})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if decision.Report.State != StatePresent {
		t.Fatalf("state = %s, want %s; blockers=%v warnings=%v", decision.Report.State, StatePresent, decision.Report.Blockers, decision.Report.Warnings)
	}
	if decision.Report.BaseBranch != "release/agent" {
		t.Fatalf("BaseBranch = %q, want release/agent", decision.Report.BaseBranch)
	}
	if decision.Report.FetchedBase != "release/agent" || decision.Report.FetchedCommit == "" {
		t.Fatalf("fetched base/commit = %q/%q", decision.Report.FetchedBase, decision.Report.FetchedCommit)
	}
}

func TestEvaluateProjectReportsInvalidPolicyAsActionableBlocker(t *testing.T) {
	project := createReadinessFixture(t, "invalid-policy")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: stop
`)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: false})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "invalid-policy") {
		t.Fatalf("blockers = %v, want invalid-policy", report.Blockers)
	}
	if !strings.Contains(report.Blockers[0].Message, "agent.env.mode") {
		t.Fatalf("blocker is not actionable: %v", report.Blockers[0])
	}
}

func TestEvaluateProjectWarnsOnMissingEnvRequirements(t *testing.T) {
	project := createReadinessFixture(t, "env-warn")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: warn
    required_files:
      - .env.local
    required_keys:
      - CODEMESH_TEST_REQUIRED
`)
	commitFixturePolicy(t, project)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: false, Env: keyOnlyEnv{}})

	if report.State != StatePresent {
		t.Fatalf("state = %s, want %s", report.State, StatePresent)
	}
	if !hasDiagnostic(report.Warnings, "missing-env-file") || !hasDiagnostic(report.Warnings, "missing-env-key") {
		t.Fatalf("warnings = %v, want missing env file and key", report.Warnings)
	}
	if len(report.Blockers) != 0 {
		t.Fatalf("blockers = %v, want none", report.Blockers)
	}
}

func TestEvaluateProjectBlocksOnMissingEnvRequirements(t *testing.T) {
	project := createReadinessFixture(t, "env-block")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: block
    required_files:
      - .env.local
    required_keys:
      - CODEMESH_TEST_REQUIRED
`)
	commitFixturePolicy(t, project)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: false, Env: keyOnlyEnv{}})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "missing-env-file") || !hasDiagnostic(report.Blockers, "missing-env-key") {
		t.Fatalf("blockers = %v, want missing env file and key", report.Blockers)
	}
}

func TestEvaluateProjectBlocksWhenRequiredEnvFileIsDirectory(t *testing.T) {
	project := createReadinessFixture(t, "env-file-directory")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: block
    required_files:
      - .env.local
`)
	commitFixturePolicy(t, project)
	if err := os.Mkdir(filepath.Join(project.LocalPath, ".env.local"), 0o755); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: false, Env: keyOnlyEnv{}})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "invalid-env-file") {
		t.Fatalf("blockers = %v, want invalid-env-file", report.Blockers)
	}
}

func TestEvaluateProjectPassesEnvRequirementsWithoutSecretValues(t *testing.T) {
	project := createReadinessFixture(t, "env-present")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: block
    required_files:
      - .env.local
    required_keys:
      - CODEMESH_TEST_REQUIRED
`)
	if err := os.WriteFile(filepath.Join(project.LocalPath, ".gitignore"), []byte(".env.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitFixturePolicy(t, project)
	secretMarker := "super-secret-value-that-must-not-appear"
	if err := os.WriteFile(filepath.Join(project.LocalPath, ".env.local"), []byte("TOKEN="+secretMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: false, Env: keyOnlyEnv{"CODEMESH_TEST_REQUIRED": true}})

	if report.State != StatePresent {
		t.Fatalf("state = %s, want %s; blockers=%v warnings=%v", report.State, StatePresent, report.Blockers, report.Warnings)
	}
	for _, diagnostic := range append(report.Warnings, report.Blockers...) {
		if strings.Contains(diagnostic.Message, secretMarker) {
			t.Fatalf("diagnostic leaked secret value: %v", diagnostic)
		}
	}
	data, err := os.ReadFile(filepath.Join(project.LocalPath, ".env.local"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "TOKEN="+secretMarker+"\n" {
		t.Fatalf("env file changed; got %q", string(data))
	}
}

func TestEvaluateProjectReportsMissingPathAsBlocker(t *testing.T) {
	project := createReadinessFixture(t, "missing")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateMissing {
		t.Fatalf("state = %s, want %s", report.State, StateMissing)
	}
	if report.LocalPathPresent {
		t.Fatalf("LocalPathPresent = true, want false")
	}
	if !hasDiagnostic(report.Blockers, "missing-path") {
		t.Fatalf("blockers = %v, want missing-path", report.Blockers)
	}
}

func TestEvaluateProjectMissingPathPreservesRequestedBase(t *testing.T) {
	project := createReadinessFixture(t, "missing-requested-base")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "release/main", CheckRemote: true})

	if report.State != StateMissing {
		t.Fatalf("state = %s, want %s", report.State, StateMissing)
	}
	if report.BaseBranch != "release/main" {
		t.Fatalf("BaseBranch = %q, want release/main", report.BaseBranch)
	}
}

func TestEvaluateHandoffBlocksMissingSourceWithoutRegisteredCloneURL(t *testing.T) {
	project := createReadinessFixture(t, "missing-no-clone-url")
	project.CloneURL = ""
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}

	decision, err := EvaluateHandoff(context.Background(), project, Options{BaseBranch: "main"})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if !decision.SourcePathMissing {
		t.Fatalf("SourcePathMissing = false, want true")
	}
	if decision.Report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", decision.Report.State, StateBlocked)
	}
	if !hasDiagnostic(decision.Report.Blockers, "origin-missing") {
		t.Fatalf("blockers = %v, want origin-missing", decision.Report.Blockers)
	}
}

func TestEvaluateProjectReportsDirtyCheckoutAsWarning(t *testing.T) {
	project := createReadinessFixture(t, "dirty")
	if err := os.WriteFile(filepath.Join(project.LocalPath, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateDirty {
		t.Fatalf("state = %s, want %s", report.State, StateDirty)
	}
	if !hasDiagnostic(report.Warnings, "dirty-checkout") {
		t.Fatalf("warnings = %v, want dirty-checkout", report.Warnings)
	}
	if len(report.Blockers) != 0 {
		t.Fatalf("blockers = %v, want none", report.Blockers)
	}
}

func TestEvaluateProjectReportsUntrackedFilesWhenGitConfigHidesThem(t *testing.T) {
	project := createReadinessFixture(t, "hidden-untracked")
	runGit(t, project.LocalPath, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(project.LocalPath, "hidden.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateDirty {
		t.Fatalf("state = %s, want %s", report.State, StateDirty)
	}
	if !hasDiagnostic(report.Warnings, "dirty-checkout") {
		t.Fatalf("warnings = %v, want dirty-checkout", report.Warnings)
	}
}

func TestEvaluateProjectReportsFetchFailureAsStaleBlocker(t *testing.T) {
	project := createReadinessFixture(t, "stale")
	missingRemote := filepath.Join(t.TempDir(), "missing.git")
	project.NormalizedRemote = missingRemote
	runGit(t, project.LocalPath, "remote", "set-url", "origin", missingRemote)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateStale {
		t.Fatalf("state = %s, want %s", report.State, StateStale)
	}
	if !hasDiagnostic(report.Blockers, "fetch-failed") {
		t.Fatalf("blockers = %v, want fetch-failed", report.Blockers)
	}
}

func TestEvaluateProjectRedactsCredentialBearingFetchFailure(t *testing.T) {
	project := createReadinessFixture(t, "redacted-fetch")
	rawRemote := "https://user:redactme@example.invalid/org/repo.git?credential=redactme#fragment"
	normalized, err := gitops.NormalizeRemote(rawRemote)
	if err != nil {
		t.Fatal(err)
	}
	project.NormalizedRemote = normalized
	runGit(t, project.LocalPath, "remote", "set-url", "origin", rawRemote)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if !hasDiagnostic(report.Blockers, "fetch-failed") {
		t.Fatalf("blockers = %v, want fetch-failed", report.Blockers)
	}
	message := report.Blockers[0].Message
	if strings.Contains(message, "redactme") || strings.Contains(message, "credential") || strings.Contains(message, "fragment") {
		t.Fatalf("fetch failure leaked credential-bearing URL parts: %s", message)
	}
}

func TestEvaluateProjectReportsRemoteAheadAsStaleWarning(t *testing.T) {
	project := createReadinessFixture(t, "remote-ahead")
	updater := filepath.Join(t.TempDir(), "updater")
	runGit(t, filepath.Dir(updater), "clone", project.NormalizedRemote, updater)
	if err := os.WriteFile(filepath.Join(updater, "remote.txt"), []byte("remote change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, updater, "add", ".")
	runGit(t, updater, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Remote change")
	runGit(t, updater, "push", "origin", "main")

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateStale {
		t.Fatalf("state = %s, want %s", report.State, StateStale)
	}
	if !hasDiagnostic(report.Warnings, "stale-checkout") {
		t.Fatalf("warnings = %v, want stale-checkout", report.Warnings)
	}
	if len(report.Blockers) != 0 {
		t.Fatalf("blockers = %v, want none", report.Blockers)
	}
}

func TestEvaluateProjectReportsOriginMismatchAsBlocker(t *testing.T) {
	project := createReadinessFixture(t, "origin-mismatch")
	other := createReadinessFixture(t, "other-project")
	runGit(t, project.LocalPath, "remote", "set-url", "origin", other.NormalizedRemote)

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "remote-mismatch") {
		t.Fatalf("blockers = %v, want remote-mismatch", report.Blockers)
	}
}

func TestEvaluateProjectReportsMissingBaseAsBlocker(t *testing.T) {
	project := createReadinessFixture(t, "missing-base")

	report := evaluateFixture(t, project, Options{BaseBranch: "release/missing", CheckRemote: true})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "missing-base") {
		t.Fatalf("blockers = %v, want missing-base", report.Blockers)
	}
}

func TestEvaluateProjectRequiresExactBaseBranchMatch(t *testing.T) {
	project := createReadinessFixture(t, "exact-base")
	runGit(t, project.LocalPath, "checkout", "-b", "release/main")
	runGit(t, project.LocalPath, "push", "origin", "release/main")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/release/main")
	runGit(t, project.NormalizedRemote, "branch", "-D", "main")

	report := evaluateFixture(t, project, Options{BaseBranch: "main", CheckRemote: true})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "missing-base") {
		t.Fatalf("blockers = %v, want missing-base", report.Blockers)
	}
}

func TestEvaluateProjectRejectsInvalidBaseBranch(t *testing.T) {
	project := createReadinessFixture(t, "invalid-base")

	report := evaluateFixture(t, project, Options{BaseBranch: "main*", CheckRemote: true})

	if report.State != StateBlocked {
		t.Fatalf("state = %s, want %s", report.State, StateBlocked)
	}
	if !hasDiagnostic(report.Blockers, "invalid-base") {
		t.Fatalf("blockers = %v, want invalid-base", report.Blockers)
	}
}

func TestEvaluateHandoffUsesRequestedRemoteBasePolicyAndKeepsDirtyWarning(t *testing.T) {
	project := createReadinessFixture(t, "handoff-remote-policy")
	runGit(t, project.LocalPath, "checkout", "-b", "feature")
	runGit(t, project.LocalPath, "checkout", "main")
	writeFixturePolicy(t, project, `agent:
  env:
    mode: block
    required_files:
      - .env.local
`)
	commitFixturePolicy(t, project)
	runGit(t, project.LocalPath, "push", "origin", "main")
	runGit(t, project.LocalPath, "checkout", "feature")
	if err := os.WriteFile(filepath.Join(project.LocalPath, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision, err := EvaluateHandoff(context.Background(), project, Options{BaseBranch: "main", Env: keyOnlyEnv{}})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}

	if decision.Report.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", decision.Report.BaseBranch)
	}
	if !hasDiagnostic(decision.Report.Warnings, "dirty-checkout") {
		t.Fatalf("warnings = %v, want dirty-checkout", decision.Report.Warnings)
	}
	if !hasDiagnostic(decision.Report.Blockers, "missing-env-file") {
		t.Fatalf("blockers = %v, want missing-env-file from requested remote base policy", decision.Report.Blockers)
	}
	if decision.Policy.Env.Mode != "block" {
		t.Fatalf("policy env mode = %q, want block", decision.Policy.Env.Mode)
	}
}

func evaluateFixture(t *testing.T, project state.Project, opts Options) ProjectReport {
	t.Helper()
	report, err := EvaluateProject(context.Background(), project, opts)
	if err != nil {
		t.Fatalf("EvaluateProject error = %v", err)
	}
	return report
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func createReadinessFixture(t *testing.T, name string) state.Project {
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

	return state.Project{
		Alias:            name,
		NormalizedRemote: remote,
		LocalPath:        source,
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeFixturePolicy(t *testing.T, project state.Project, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(project.LocalPath, ".codemesh.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitFixturePolicy(t *testing.T, project state.Project) {
	t.Helper()
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	if _, err := os.Stat(filepath.Join(project.LocalPath, ".gitignore")); err == nil {
		runGit(t, project.LocalPath, "add", ".gitignore")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add CodeMesh policy")
}

type keyOnlyEnv map[string]bool

func (e keyOnlyEnv) HasEnvKey(key string) bool {
	return e[key]
}
