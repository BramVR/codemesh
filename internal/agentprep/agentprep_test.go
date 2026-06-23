package agentprep

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/state"
)

func TestPrepareClonesRequestedBaseAndWritesMetadata(t *testing.T) {
	project := createFixtureProject(t, "prepare-main")
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{
		Project: project.Alias,
		Base:    "main",
		Profile: "codex",
	})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if result.ReadyPath != filepath.Join(home, "agents", "run-test", "workspace") {
		t.Fatalf("ReadyPath = %q", result.ReadyPath)
	}
	if _, err := os.Stat(filepath.Join(result.ReadyPath, "README.md")); err != nil {
		t.Fatalf("ready checkout missing README: %v", err)
	}
	if branch := strings.TrimSpace(gitOutputTest(t, result.ReadyPath, "branch", "--show-current")); branch != "main" {
		t.Fatalf("ready checkout branch = %q, want main", branch)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if metadata.Project.Alias != project.Alias {
		t.Fatalf("metadata alias = %q, want %q", metadata.Project.Alias, project.Alias)
	}
	if metadata.Project.Remote != project.NormalizedRemote {
		t.Fatalf("metadata remote = %q, want %q", metadata.Project.Remote, project.NormalizedRemote)
	}
	if metadata.Base != "main" || metadata.Profile != "codex" || metadata.ReadyPath != result.ReadyPath {
		t.Fatalf("metadata base/profile/path = %#v", metadata)
	}
	if len(metadata.Diagnostics.Warnings) != 0 || len(metadata.Diagnostics.Blockers) != 0 {
		t.Fatalf("metadata diagnostics = %#v, want none", metadata.Diagnostics)
	}
	if len(store.runs) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(store.runs))
	}
	if store.runs[0].WorkspacePath != result.ReadyPath {
		t.Fatalf("stored workspace path = %q, want %q", store.runs[0].WorkspacePath, result.ReadyPath)
	}
}

func TestPrepareUsesPolicyBaseWhenRequestBaseUnset(t *testing.T) {
	project := createFixtureProject(t, "policy-base")
	runGit(t, project.LocalPath, "checkout", "-b", "release/agent")
	writeFile(t, filepath.Join(project.LocalPath, "release.txt"), "release\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Release branch")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  base: release/agent\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Agent policy")
	runGit(t, project.LocalPath, "push", "origin", "release/agent")
	runGit(t, project.NormalizedRemote, "branch", "-D", "main")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if result.Base != "release/agent" {
		t.Fatalf("Base = %q, want release/agent", result.Base)
	}
	if branch := strings.TrimSpace(gitOutputTest(t, result.ReadyPath, "branch", "--show-current")); branch != "release/agent" {
		t.Fatalf("ready checkout branch = %q, want release/agent", branch)
	}
}

func TestPrepareStopsOnReadinessBlockerBeforeWorkspacePrep(t *testing.T) {
	project := createFixtureProject(t, "blocked-env")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err == nil {
		t.Fatal("Prepare error = nil, want blocker")
	}
	if len(result.Diagnostics.Blockers) != 1 || result.Diagnostics.Blockers[0].Code != "missing-env-file" {
		t.Fatalf("blockers = %#v, want missing-env-file", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareChecksFetchedPolicyButLocalEnvFilePresence(t *testing.T) {
	project := createFixtureProject(t, "local-env-file")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require local env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	writeFile(t, filepath.Join(project.LocalPath, ".env.local"), "TOKEN=not-read\n")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if len(result.Diagnostics.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want none", result.Diagnostics.Blockers)
	}
}

func TestPrepareRecordsDirtyAndEnvWarnings(t *testing.T) {
	project := createFixtureProject(t, "warn-diagnostics")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: warn\n    required_keys:\n      - CODEMESH_TEST_REQUIRED\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Warn env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	writeFile(t, filepath.Join(project.LocalPath, "dirty.txt"), "local change\n")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if !hasDiagnostic(result.Diagnostics.Warnings, "dirty-checkout") || !hasDiagnostic(result.Diagnostics.Warnings, "missing-env-key") {
		t.Fatalf("warnings = %#v, want dirty-checkout and missing-env-key", result.Diagnostics.Warnings)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if !hasDiagnostic(metadata.Diagnostics.Warnings, "dirty-checkout") || !hasDiagnostic(metadata.Diagnostics.Warnings, "missing-env-key") {
		t.Fatalf("metadata warnings = %#v", metadata.Diagnostics.Warnings)
	}
}

func TestPrepareDoesNotFollowRepoSymlinkForMetadata(t *testing.T) {
	project := createFixtureProject(t, "metadata-symlink")
	target := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, target, "keep me\n")
	if err := os.Symlink(target, filepath.Join(project.LocalPath, MetadataFileName)); err != nil {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "add", MetadataFileName)
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add metadata symlink")
	runGit(t, project.LocalPath, "push", "origin", "main")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	_, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err == nil {
		t.Fatal("Prepare error = nil, want metadata write failure")
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "keep me\n" {
		t.Fatalf("symlink target changed or unreadable: got %q err %v", got, readErr)
	}
}

func TestMetadataCloneURLRedactsCredentialBearingURLParts(t *testing.T) {
	raw := "https://user:secret@example.invalid/org/repo.git?token=secret#frag"

	got := redactedCloneURLForMetadata(raw)

	if strings.Contains(got, "secret") || strings.Contains(got, "token") || strings.Contains(got, "frag") {
		t.Fatalf("redacted clone URL leaked credential-bearing parts: %s", got)
	}
	if got != "https://redacted@example.invalid/org/repo.git" {
		t.Fatalf("redacted clone URL = %q", got)
	}
}

func TestPrepareChecksEnvPolicyFromRequestedRemoteBase(t *testing.T) {
	project := createFixtureProject(t, "remote-base-policy")
	runGit(t, project.LocalPath, "checkout", "-b", "feature")
	runGit(t, project.LocalPath, "checkout", "main")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Main requires env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	runGit(t, project.LocalPath, "checkout", "feature")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want remote-base env blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "missing-env-file") {
		t.Fatalf("blockers = %#v, want missing-env-file from main policy", result.Diagnostics.Blockers)
	}
}

func TestCloneErrorRedactsCredentialBearingURL(t *testing.T) {
	cloneURL := "https://user:secret@example.invalid/org/repo.git?token=secret#frag"
	err := gitClone(context.Background(), cloneURL, "main", filepath.Join(t.TempDir(), "workspace"))

	if err == nil {
		t.Fatal("gitClone error = nil, want failure")
	}
	message := err.Error()
	if strings.Contains(message, "secret") || strings.Contains(message, "token") || strings.Contains(message, "frag") {
		t.Fatalf("clone error leaked credential-bearing URL parts: %s", message)
	}
}

type memoryStore struct {
	projects []state.Project
	runs     []state.AgentRun
}

func newMemoryStore(projects ...state.Project) *memoryStore {
	return &memoryStore{projects: projects}
}

func (s *memoryStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s *memoryStore) RecordAgentRun(_ context.Context, run state.AgentRun) error {
	s.runs = append(s.runs, run)
	return nil
}

func testPreparer(home string, store *memoryStore) Preparer {
	return Preparer{
		Store:     store,
		AgentsDir: filepath.Join(home, "agents"),
		NewID:     func() string { return "run-test" },
		Now:       func() time.Time { return time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC) },
	}
}

func createFixtureProject(t *testing.T, name string) state.Project {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	remote := filepath.Join(root, name+".git")
	source := filepath.Join(root, name)
	runGit(t, root, "init", "-b", "main", seed)
	writeFile(t, filepath.Join(seed, "README.md"), "# "+name+"\n")
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	runGit(t, root, "clone", "--bare", seed, remote)
	runGit(t, root, "clone", remote, source)
	return state.Project{
		ID:               42,
		Alias:            name,
		NormalizedRemote: remote,
		CloneURL:         remote,
		LocalPath:        source,
	}
}

func readMetadata(t *testing.T, readyPath string) Metadata {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(readyPath, MetadataFileName))
	if err != nil {
		t.Fatal(err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func gitOutputTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}
