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

	"github.com/BramVR/codemesh/internal/envbinding"
	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/readiness"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/toolchain"
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
	if metadata.ContractVersion != 1 || metadata.Producer.Name != "codemesh" || metadata.Producer.Version == "" {
		t.Fatalf("metadata contract version/producer = version %d producer %#v", metadata.ContractVersion, metadata.Producer)
	}
	if metadata.Base != "main" || metadata.Profile != "codex" || metadata.ReadyPath != result.ReadyPath {
		t.Fatalf("metadata base/profile/path = %#v", metadata)
	}
	if metadata.ResolvedCommit == "" || metadata.ReadinessDecision != "ready" {
		t.Fatalf("metadata commit/decision = %#v", metadata)
	}
	if metadata.BaseProvenance.FetchedBase != "main" || metadata.BaseProvenance.FetchedCommit == "" || metadata.BaseProvenance.PreparedHEAD != metadata.ResolvedCommit || !metadata.BaseProvenance.MatchesFetched {
		t.Fatalf("metadata base provenance = %#v, resolved=%s", metadata.BaseProvenance, metadata.ResolvedCommit)
	}
	if metadata.BaseProvenance.FetchedCommit != metadata.ResolvedCommit {
		t.Fatalf("fetched commit = %q, prepared HEAD = %q", metadata.BaseProvenance.FetchedCommit, metadata.ResolvedCommit)
	}
	if metadata.CloneStrategy.Name != "full-clone" || metadata.CloneStrategy.History != "full" || metadata.CloneStrategy.WorkingTree != "complete" {
		t.Fatalf("metadata clone strategy = %#v, want full clone", metadata.CloneStrategy)
	}
	if result.CloneStrategy != metadata.CloneStrategy {
		t.Fatalf("result clone strategy = %#v, metadata = %#v", result.CloneStrategy, metadata.CloneStrategy)
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
	metadataBytes, err := os.ReadFile(filepath.Join(result.ReadyPath, MetadataFileName))
	if err != nil {
		t.Fatalf("read metadata bytes: %v", err)
	}
	if string(metadataBytes) != store.runs[0].MetadataJSON {
		t.Fatal("contract file bytes and state metadata bytes diverged")
	}
	if !strings.Contains(store.runs[0].MetadataJSON, `"contract_version": 1`) || !strings.Contains(store.runs[0].MetadataJSON, `"producer": {`) {
		t.Fatalf("stored metadata missing contract version/producer:\n%s", store.runs[0].MetadataJSON)
	}
	if !strings.Contains(store.runs[0].MetadataJSON, `"fetched_base": "main"`) || !strings.Contains(store.runs[0].MetadataJSON, `"matches_fetched": true`) {
		t.Fatalf("stored metadata missing base provenance:\n%s", store.runs[0].MetadataJSON)
	}
	if !strings.Contains(store.runs[0].MetadataJSON, `"clone_strategy": {`) || !strings.Contains(store.runs[0].MetadataJSON, `"name": "full-clone"`) {
		t.Fatalf("stored metadata missing clone strategy:\n%s", store.runs[0].MetadataJSON)
	}
}

func TestPrepareRecordsToolchainReadinessInRunContract(t *testing.T) {
	project := createFixtureProject(t, "prepare-toolchain")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), `agent:
  toolchain:
    mode: warn
    requirements:
      - go
`)
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Declare toolchain")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})
	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}

	if !hasDiagnostic(result.Diagnostics.Warnings, "unknown-toolchain") {
		t.Fatalf("warnings = %#v, want unknown-toolchain", result.Diagnostics.Warnings)
	}
	if len(result.Metadata.Toolchain) != 1 || result.Metadata.Toolchain[0].Name != "go" || result.Metadata.Toolchain[0].Status != toolchain.StatusUnknown {
		t.Fatalf("result metadata toolchain = %#v", result.Metadata.Toolchain)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if len(metadata.Toolchain) != 1 || metadata.Toolchain[0].Name != "go" || metadata.Toolchain[0].Status != toolchain.StatusUnknown {
		t.Fatalf("file metadata toolchain = %#v", metadata.Toolchain)
	}
	if !strings.Contains(store.runs[0].MetadataJSON, `"toolchain": [`) || !strings.Contains(store.runs[0].MetadataJSON, `"status": "unknown"`) {
		t.Fatalf("stored metadata missing toolchain readiness:\n%s", store.runs[0].MetadataJSON)
	}
	for _, path := range []string{"node_modules", ".tool-versions", ".codemesh-toolchain"} {
		if _, err := os.Stat(filepath.Join(result.ReadyPath, path)); err == nil {
			t.Fatalf("agent prep created %s; CodeMesh must report/delegate only", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("check %s: %v", path, err)
		}
	}
}

func TestPrepareMissingSourceCheckoutClonesFromRegisteredRemote(t *testing.T) {
	project := createFixtureProject(t, "missing-source-prep")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main", Profile: "codex"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.ReadyPath, ".git")); err != nil {
		t.Fatalf("ready checkout missing .git: %v", err)
	}
	if branch := strings.TrimSpace(gitOutputTest(t, result.ReadyPath, "branch", "--show-current")); branch != "main" {
		t.Fatalf("ready checkout branch = %q, want main", branch)
	}
	if hasDiagnostic(result.Diagnostics.Blockers, "missing-path") {
		t.Fatalf("blockers = %#v, want missing source path ignored for remote prep", result.Diagnostics.Blockers)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if metadata.Project.SourcePath != project.LocalPath || metadata.Project.Remote != project.NormalizedRemote {
		t.Fatalf("metadata project = %#v", metadata.Project)
	}
	if !metadata.Project.SourcePathMissing {
		t.Fatalf("metadata source path missing = false, want true")
	}
	if metadata.ResolvedCommit == "" || metadata.ReadinessDecision != "ready" {
		t.Fatalf("metadata commit/decision = %#v", metadata)
	}
	if len(store.runs) != 1 || !strings.Contains(store.runs[0].MetadataJSON, `"readiness_decision": "ready"`) {
		t.Fatalf("stored runs = %#v", store.runs)
	}
}

func TestPrepareMissingSourceCheckoutUsesRemotePolicyBaseWhenRequestBaseUnset(t *testing.T) {
	project := createFixtureProject(t, "missing-source-policy-base")
	runGit(t, project.LocalPath, "checkout", "-b", "release/agent")
	writeFile(t, filepath.Join(project.LocalPath, "release.txt"), "release\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Release branch")
	runGit(t, project.LocalPath, "push", "origin", "release/agent")
	runGit(t, project.LocalPath, "checkout", "main")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  base: release/agent\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Agent policy")
	runGit(t, project.LocalPath, "push", "origin", "main")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

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
	if len(store.runs) != 1 {
		t.Fatalf("recorded runs = %d, want 1", len(store.runs))
	}
}

func TestPrepareUsesRemoteDefaultBaseWhenRequestAndPolicyUnset(t *testing.T) {
	project := createFixtureProject(t, "remote-default-base")
	runGit(t, project.LocalPath, "checkout", "-b", "develop")
	writeFile(t, filepath.Join(project.LocalPath, "develop.txt"), "develop\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Develop branch")
	runGit(t, project.LocalPath, "push", "origin", "develop")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/develop")
	runGit(t, project.LocalPath, "checkout", "main")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Profile: "codex"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if result.Base != "develop" {
		t.Fatalf("Base = %q, want develop", result.Base)
	}
	if branch := strings.TrimSpace(gitOutputTest(t, result.ReadyPath, "branch", "--show-current")); branch != "develop" {
		t.Fatalf("ready checkout branch = %q, want develop", branch)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if metadata.BaseProvenance.FetchedBase != "develop" || metadata.BaseProvenance.FetchedCommit != metadata.BaseProvenance.PreparedHEAD || !metadata.BaseProvenance.MatchesFetched {
		t.Fatalf("base provenance = %#v", metadata.BaseProvenance)
	}
}

func TestPreparePolicyBaseOverridesRemoteDefaultBase(t *testing.T) {
	project := createFixtureProject(t, "policy-over-remote-default")
	runGit(t, project.LocalPath, "checkout", "-b", "develop")
	writeFile(t, filepath.Join(project.LocalPath, "develop.txt"), "develop\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Develop branch")
	runGit(t, project.LocalPath, "push", "origin", "develop")
	runGit(t, project.NormalizedRemote, "symbolic-ref", "HEAD", "refs/heads/develop")
	runGit(t, project.LocalPath, "checkout", "main")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  base: release/agent\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Agent policy")
	runGit(t, project.LocalPath, "checkout", "-b", "release/agent")
	writeFile(t, filepath.Join(project.LocalPath, "release.txt"), "release\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Release branch")
	runGit(t, project.LocalPath, "push", "origin", "main", "release/agent")
	runGit(t, project.LocalPath, "checkout", "main")
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

func TestPrepareMissingSourceCheckoutStillAppliesRemoteEnvPolicy(t *testing.T) {
	project := createFixtureProject(t, "missing-source-env-policy")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n    required_keys:\n      - CODEMESH_TEST_REQUIRED\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env key")
	runGit(t, project.LocalPath, "push", "origin", "main")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want remote env policy blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "missing-env-file") || !hasDiagnostic(result.Diagnostics.Blockers, "missing-env-key") {
		t.Fatalf("blockers = %#v, want missing-env-file and missing-env-key", result.Diagnostics.Blockers)
	}
	if result.Metadata.Env.MaterializationStatus != "not_requested" {
		t.Fatalf("env materialization status = %q, want not_requested", result.Metadata.Env.MaterializationStatus)
	}
	if got := len(result.Metadata.Env.Requirements); got != 2 {
		t.Fatalf("env requirements count = %d, want 2: %#v", got, result.Metadata.Env.Requirements)
	}
	if result.Metadata.Env.Bundle.Values != "not-recorded" {
		t.Fatalf("env bundle values = %q, want not-recorded", result.Metadata.Env.Bundle.Values)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMaterializesFakeProviderEnvBundle(t *testing.T) {
	project := createFixtureProject(t, "fake-env-bundle")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_TEST_BOUND_TOKEN\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require bound env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	store.bindings = []state.EnvBinding{{
		ProjectID:   project.ID,
		Requirement: "CODEMESH_TEST_BOUND_TOKEN",
		Provider:    "fake",
		SecretRef:   "fake://agent-token",
		Scopes:      []string{"codex"},
	}}
	preparer := testPreparer(t.TempDir(), store)

	result, err := preparer.Prepare(context.Background(), Request{
		Project:          project.Alias,
		Base:             "main",
		EnvProvider:      "fake",
		AllowedEnvScopes: []string{"codex"},
	})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if metadata.Env.MaterializationStatus != "materialized" {
		t.Fatalf("env materialization status = %q, want materialized", metadata.Env.MaterializationStatus)
	}
	if len(metadata.Env.Requirements) != 1 || metadata.Env.Requirements[0].Name != "CODEMESH_TEST_BOUND_TOKEN" || metadata.Env.Requirements[0].Kind != "env_key" {
		t.Fatalf("env requirements = %#v", metadata.Env.Requirements)
	}
	if strings.Join(metadata.Env.AllowedScopes, ",") != "codex" {
		t.Fatalf("allowed scopes = %v", metadata.Env.AllowedScopes)
	}
	if !metadata.Env.Bundle.Present || metadata.Env.Bundle.Path == "" || !strings.HasPrefix(metadata.Env.Bundle.Path, filepath.Dir(result.ReadyPath)+string(filepath.Separator)) {
		t.Fatalf("bundle metadata = %#v, ready path %s", metadata.Env.Bundle, result.ReadyPath)
	}
	if strings.HasPrefix(metadata.Env.Bundle.Path, result.ReadyPath+string(filepath.Separator)) {
		t.Fatalf("bundle path is inside prepared checkout: %s", metadata.Env.Bundle.Path)
	}
	bundle, err := os.ReadFile(metadata.Env.Bundle.Path)
	if err != nil {
		t.Fatalf("read env bundle: %v", err)
	}
	fakeValue := envbinding.FakeProviderValue("fake://agent-token")
	if !strings.Contains(string(bundle), "CODEMESH_TEST_BOUND_TOKEN="+fakeValue) {
		t.Fatalf("bundle = %q, want materialized fake value", string(bundle))
	}
	contractBytes, err := os.ReadFile(filepath.Join(result.ReadyPath, MetadataFileName))
	if err != nil {
		t.Fatalf("read contract: %v", err)
	}
	if strings.Contains(store.runs[0].MetadataJSON, fakeValue) || strings.Contains(string(contractBytes), fakeValue) {
		t.Fatalf("metadata leaked fake provider value:\n%s", store.runs[0].MetadataJSON)
	}
}

func TestPrepareDeniesEnvBindingOutsideAllowedScopes(t *testing.T) {
	project := createFixtureProject(t, "fake-env-scope-denied")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_TEST_BOUND_TOKEN\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require bound env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	store.bindings = []state.EnvBinding{{
		ProjectID:   project.ID,
		Requirement: "CODEMESH_TEST_BOUND_TOKEN",
		Provider:    "fake",
		SecretRef:   "fake://agent-token",
		Scopes:      []string{"codex"},
	}}
	home := t.TempDir()
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{
		Project:          project.Alias,
		Base:             "main",
		EnvProvider:      "fake",
		AllowedEnvScopes: []string{"readonly"},
	})

	if err == nil {
		t.Fatal("Prepare error = nil, want scope denial")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "env-scope-denied") {
		t.Fatalf("blockers = %#v, want env-scope-denied", result.Diagnostics.Blockers)
	}
	if result.CloneStrategy.Name != "full-clone" || result.CloneStrategy.History != "full" || result.CloneStrategy.WorkingTree != "complete" {
		t.Fatalf("scope denial clone strategy = %#v, want full clone", result.CloneStrategy)
	}
	if !strings.Contains(result.Diagnostics.Blockers[0].Message, "CODEMESH_TEST_BOUND_TOKEN") || !strings.Contains(result.Diagnostics.Blockers[0].Message, "readonly") {
		t.Fatalf("scope denial is not actionable: %q", result.Diagnostics.Blockers[0].Message)
	}
	if result.Metadata.Env.MaterializationStatus != "denied" {
		t.Fatalf("env materialization status = %q, want denied", result.Metadata.Env.MaterializationStatus)
	}
	if len(result.Metadata.Env.Requirements) != 1 || result.Metadata.Env.Requirements[0].Name != "CODEMESH_TEST_BOUND_TOKEN" {
		t.Fatalf("env requirements = %#v", result.Metadata.Env.Requirements)
	}
	if strings.Join(result.Metadata.Env.AllowedScopes, ",") != "readonly" {
		t.Fatalf("allowed scopes = %v, want readonly", result.Metadata.Env.AllowedScopes)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMissingSourceCheckoutBlocksWithoutCloneURL(t *testing.T) {
	project := createFixtureProject(t, "missing-source-no-clone-url")
	project.CloneURL = ""
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want missing clone URL blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "origin-missing") {
		t.Fatalf("blockers = %#v, want origin-missing", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMissingSourceCheckoutReportsMissingBase(t *testing.T) {
	project := createFixtureProject(t, "missing-source-missing-base")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "missing-base"})

	if err == nil {
		t.Fatal("Prepare error = nil, want missing-base blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "missing-base") {
		t.Fatalf("blockers = %#v, want missing-base", result.Diagnostics.Blockers)
	}
	if hasDiagnostic(result.Diagnostics.Blockers, "fetch-failed") {
		t.Fatalf("blockers = %#v, want no fetch-failed for missing branch", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMissingSourceCheckoutRequiresExactBaseRef(t *testing.T) {
	project := createFixtureProject(t, "missing-source-exact-base")
	runGit(t, project.LocalPath, "checkout", "-b", "release/main")
	writeFile(t, filepath.Join(project.LocalPath, "release.txt"), "release\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Release branch")
	runGit(t, project.LocalPath, "push", "origin", "release/main")
	runGit(t, project.NormalizedRemote, "branch", "-D", "main")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want missing-base blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "missing-base") {
		t.Fatalf("blockers = %#v, want missing-base for exact refs/heads/main", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMissingSourceCheckoutReportsInvalidRemotePolicy(t *testing.T) {
	project := createFixtureProject(t, "missing-source-invalid-policy")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  include_docs:\n    - /tmp/outside.md\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add invalid policy")
	runGit(t, project.LocalPath, "push", "origin", "main")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want invalid-policy blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "invalid-policy") {
		t.Fatalf("blockers = %#v, want invalid-policy", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareMissingSourceCheckoutDoesNotFollowPolicySymlink(t *testing.T) {
	project := createFixtureProject(t, "missing-source-policy-symlink")
	outsidePolicy := filepath.Join(t.TempDir(), "outside-policy.yml")
	writeFile(t, outsidePolicy, "agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_TEST_REQUIRED\n")
	if err := os.Symlink(outsidePolicy, filepath.Join(project.LocalPath, ".codemesh.yml")); err != nil {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add policy symlink")
	runGit(t, project.LocalPath, "push", "origin", "main")
	if err := os.RemoveAll(project.LocalPath); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	store := newMemoryStore(project)
	preparer := testPreparer(home, store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want invalid-policy blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "invalid-policy") {
		t.Fatalf("blockers = %#v, want invalid-policy from symlink blob", result.Diagnostics.Blockers)
	}
	if hasDiagnostic(result.Diagnostics.Blockers, "missing-env-key") {
		t.Fatalf("blockers = %#v, followed symlinked local policy", result.Diagnostics.Blockers)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists or stat failed: %v", statErr)
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareRecordsDefaultHandoffDocsFromPreparedClone(t *testing.T) {
	project := createFixtureProject(t, "default-handoff-docs")
	writeFile(t, filepath.Join(project.LocalPath, "AGENTS.md"), "agent instructions from selected base\n")
	if err := os.MkdirAll(filepath.Join(project.LocalPath, "docs", "adr"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project.LocalPath, "docs", "adr", "0002-second.md"), "second adr\n")
	writeFile(t, filepath.Join(project.LocalPath, "docs", "adr", "0001-first.md"), "first adr\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add handoff docs")
	runGit(t, project.LocalPath, "push", "origin", "main")
	localOnlyContext := "local source checkout only context content"
	writeFile(t, filepath.Join(project.LocalPath, "CONTEXT.md"), localOnlyContext+"\n")
	store := newMemoryStore(project)
	preparer := testPreparer(filepath.Join(t.TempDir(), "home[glob]"), store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	want := []HandoffDoc{
		{Path: "AGENTS.md", Source: "default"},
		{Path: "README.md", Source: "default"},
		{Path: "docs/adr/0001-first.md", Source: "default"},
		{Path: "docs/adr/0002-second.md", Source: "default"},
	}
	if !handoffDocsEqual(result.Metadata.HandoffDocs, want) {
		t.Fatalf("handoff docs = %#v, want %#v", result.Metadata.HandoffDocs, want)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if !handoffDocsEqual(metadata.HandoffDocs, want) {
		t.Fatalf("file handoff docs = %#v, want %#v", metadata.HandoffDocs, want)
	}
	if strings.Contains(store.runs[0].MetadataJSON, localOnlyContext) {
		t.Fatal("stored metadata leaked local source checkout doc contents")
	}
}

func TestPrepareAppendsPolicyHandoffDocsWithPatternsAndStableDedupe(t *testing.T) {
	project := createFixtureProject(t, "policy-handoff-docs")
	writeFile(t, filepath.Join(project.LocalPath, "AGENTS.md"), "default agent doc\n")
	if err := os.MkdirAll(filepath.Join(project.LocalPath, "docs", "adr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project.LocalPath, "docs", "guides"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project.LocalPath, "docs", "guides", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(project.LocalPath, "docs", "adr", "0001-default.md"), "default adr\n")
	writeFile(t, filepath.Join(project.LocalPath, "docs", "runbook.md"), "policy runbook no metadata leak\n")
	writeFile(t, filepath.Join(project.LocalPath, "docs", "guides", "b.md"), "policy guide b\n")
	writeFile(t, filepath.Join(project.LocalPath, "docs", "guides", "a.md"), "policy guide a\n")
	writeFile(t, filepath.Join(project.LocalPath, "docs", "guides", "deep", "c.md"), "policy guide c\n")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), `agent:
  include_docs:
    - AGENTS.md
    - .git/config
    - docs/runbook.md
    - docs/guides/**
    - docs/./runbook.md
`)
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add policy handoff docs")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if len(result.Diagnostics.Warnings) != 0 || len(result.Diagnostics.Blockers) != 0 {
		t.Fatalf("diagnostics = %#v, want none", result.Diagnostics)
	}
	want := []HandoffDoc{
		{Path: "AGENTS.md", Source: "default"},
		{Path: "README.md", Source: "default"},
		{Path: "docs/adr/0001-default.md", Source: "default"},
		{Path: "docs/runbook.md", Source: "policy", Pattern: "docs/runbook.md"},
		{Path: "docs/guides/a.md", Source: "policy", Pattern: "docs/guides/**"},
		{Path: "docs/guides/b.md", Source: "policy", Pattern: "docs/guides/**"},
		{Path: "docs/guides/deep/c.md", Source: "policy", Pattern: "docs/guides/**"},
	}
	if !handoffDocsEqual(result.Metadata.HandoffDocs, want) {
		t.Fatalf("handoff docs = %#v, want %#v", result.Metadata.HandoffDocs, want)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if !handoffDocsEqual(metadata.HandoffDocs, want) {
		t.Fatalf("file handoff docs = %#v, want %#v", metadata.HandoffDocs, want)
	}
	if strings.Contains(store.runs[0].MetadataJSON, "policy runbook no metadata leak") {
		t.Fatal("stored metadata leaked policy-selected doc contents")
	}
}

func TestPrepareWarnsWhenPolicyHandoffDocsAreMissing(t *testing.T) {
	project := createFixtureProject(t, "missing-policy-handoff-docs")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), `agent:
  include_docs:
    - docs/missing.md
    - docs/missing/**
`)
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add stale handoff docs policy")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	if !hasDiagnostic(result.Diagnostics.Warnings, "handoff-doc-missing") {
		t.Fatalf("warnings = %#v, want handoff-doc-missing", result.Diagnostics.Warnings)
	}
	if len(result.Diagnostics.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want none", result.Diagnostics.Blockers)
	}
	metadata := readMetadata(t, result.ReadyPath)
	if !hasDiagnostic(metadata.Diagnostics.Warnings, "handoff-doc-missing") {
		t.Fatalf("metadata warnings = %#v, want handoff-doc-missing", metadata.Diagnostics.Warnings)
	}
}

func TestPrepareRecordsHandoffDocsFromRequestedBase(t *testing.T) {
	project := createFixtureProject(t, "handoff-doc-base")
	runGit(t, project.LocalPath, "checkout", "-b", "docs-base")
	writeFile(t, filepath.Join(project.LocalPath, "CONTEXT.md"), "context from requested base\n")
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add base context")
	runGit(t, project.LocalPath, "push", "origin", "docs-base")
	runGit(t, project.LocalPath, "checkout", "main")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "docs-base"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	want := []HandoffDoc{
		{Path: "CONTEXT.md", Source: "default"},
		{Path: "README.md", Source: "default"},
	}
	if !handoffDocsEqual(result.Metadata.HandoffDocs, want) {
		t.Fatalf("handoff docs = %#v, want %#v", result.Metadata.HandoffDocs, want)
	}
}

func TestPrepareDoesNotFollowHandoffDocSymlinks(t *testing.T) {
	project := createFixtureProject(t, "handoff-doc-symlinks")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(outside, "AGENTS.md"), "outside agent docs\n")
	adrOutside := filepath.Join(outside, "adr")
	if err := os.MkdirAll(adrOutside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(adrOutside, "0001-outside.md"), "outside adr\n")
	if err := os.Symlink(filepath.Join(outside, "AGENTS.md"), filepath.Join(project.LocalPath, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project.LocalPath, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(adrOutside, filepath.Join(project.LocalPath, "docs", "adr")); err != nil {
		t.Fatal(err)
	}
	runGit(t, project.LocalPath, "add", ".")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add symlinked docs")
	runGit(t, project.LocalPath, "push", "origin", "main")
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err != nil {
		t.Fatalf("Prepare error = %v", err)
	}
	want := []HandoffDoc{{Path: "README.md", Source: "default"}}
	if !handoffDocsEqual(result.Metadata.HandoffDocs, want) {
		t.Fatalf("handoff docs = %#v, want %#v", result.Metadata.HandoffDocs, want)
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

func TestPrepareReportsPathFileAsReadinessBlockerBeforePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, path, "file\n")
	project := state.Project{
		Alias:            "path-file",
		NormalizedRemote: "https://example.invalid/path-file",
		CloneURL:         "https://example.invalid/path-file",
		LocalPath:        path,
	}
	preparer := testPreparer(t.TempDir(), newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err == nil {
		t.Fatal("Prepare error = nil, want blocker")
	}
	if !hasDiagnostic(result.Diagnostics.Blockers, "path-not-directory") {
		t.Fatalf("blockers = %#v, want path-not-directory", result.Diagnostics.Blockers)
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

func TestPrepareReturnsSharedReadinessDecisionDiagnostics(t *testing.T) {
	project := createFixtureProject(t, "shared-readiness")
	writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_TEST_REQUIRED\n")
	runGit(t, project.LocalPath, "add", ".codemesh.yml")
	runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env")
	runGit(t, project.LocalPath, "push", "origin", "main")
	store := newMemoryStore(project)
	preparer := testPreparer(t.TempDir(), store)

	decision, err := readiness.EvaluateHandoff(context.Background(), project, readiness.Options{})
	if err != nil {
		t.Fatalf("EvaluateHandoff error = %v", err)
	}
	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias})

	if err == nil {
		t.Fatal("Prepare error = nil, want shared readiness blocker")
	}
	if result.Base != decision.Report.BaseBranch {
		t.Fatalf("Base = %q, want readiness base %q", result.Base, decision.Report.BaseBranch)
	}
	if diagnosticCodes(result.Diagnostics.Blockers) != readinessCodes(decision.Report.Blockers) {
		t.Fatalf("blocker codes = %s, want readiness codes %s", diagnosticCodes(result.Diagnostics.Blockers), readinessCodes(decision.Report.Blockers))
	}
	if len(store.runs) != 0 {
		t.Fatalf("recorded runs = %d, want 0", len(store.runs))
	}
}

func TestPrepareStopsOnInvalidPolicyIncludeDocsBeforeWorkspacePrep(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{name: "absolute", entry: "/tmp/outside.md"},
		{name: "parent escape", entry: "../outside.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			project := createFixtureProject(t, "blocked-include-docs-"+strings.ReplaceAll(tt.name, " ", "-"))
			writeFile(t, filepath.Join(project.LocalPath, ".codemesh.yml"), "agent:\n  include_docs:\n    - "+tt.entry+"\n")
			runGit(t, project.LocalPath, "add", ".codemesh.yml")
			runGit(t, project.LocalPath, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add invalid handoff docs policy")
			runGit(t, project.LocalPath, "push", "origin", "main")
			home := t.TempDir()
			store := newMemoryStore(project)
			preparer := testPreparer(home, store)

			result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

			if err == nil {
				t.Fatal("Prepare error = nil, want invalid policy blocker")
			}
			if len(result.Diagnostics.Blockers) != 1 || result.Diagnostics.Blockers[0].Code != "invalid-policy" {
				t.Fatalf("blockers = %#v, want invalid-policy", result.Diagnostics.Blockers)
			}
			message := result.Diagnostics.Blockers[0].Message
			if !strings.Contains(message, "agent.include_docs") || !strings.Contains(message, tt.entry) {
				t.Fatalf("blocker message = %q, want field and invalid entry", message)
			}
			if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
				t.Fatalf("run dir exists or stat failed: %v", statErr)
			}
			if len(store.runs) != 0 {
				t.Fatalf("recorded runs = %d, want 0", len(store.runs))
			}
		})
	}
}

func TestPrepareRemovesRunDirectoryWhenCloneFails(t *testing.T) {
	project := createFixtureProject(t, "clone-fails")
	project.CloneURL = "https://example.invalid/missing.git"
	home := t.TempDir()
	preparer := testPreparer(home, newMemoryStore(project))

	result, err := preparer.Prepare(context.Background(), Request{Project: project.Alias, Base: "main"})

	if err == nil {
		t.Fatal("Prepare error = nil, want clone failure")
	}
	if result.CloneStrategy.Name != "full-clone" || result.CloneStrategy.History != "full" || result.CloneStrategy.WorkingTree != "complete" {
		t.Fatalf("clone failure strategy = %#v, want full clone", result.CloneStrategy)
	}
	if _, statErr := os.Stat(filepath.Join(home, "agents", "run-test")); !os.IsNotExist(statErr) {
		t.Fatalf("run dir exists after clone failure or stat failed: %v", statErr)
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

	got := gitops.RedactURLForMetadata(raw)

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
	bindings []state.EnvBinding
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

func (s *memoryStore) ListEnvBindings(_ context.Context, projectID int64) ([]state.EnvBinding, error) {
	var bindings []state.EnvBinding
	for _, binding := range s.bindings {
		if binding.ProjectID == projectID {
			bindings = append(bindings, binding)
		}
	}
	return bindings, nil
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

func handoffDocsEqual(got, want []HandoffDoc) bool {
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

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func diagnosticCodes(diagnostics []Diagnostic) string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return strings.Join(codes, ",")
}

func readinessCodes(diagnostics []readiness.Diagnostic) string {
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
	}
	return strings.Join(codes, ",")
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
