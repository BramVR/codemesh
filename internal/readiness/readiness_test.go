package readiness

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
