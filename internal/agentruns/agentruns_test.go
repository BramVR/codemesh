package agentruns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/state"
)

func TestListPreparedRunsFromMetadata(t *testing.T) {
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: "/tmp/codemesh/agents/run-one/workspace",
		MetadataJSON: `{
  "project": {"alias": "codemesh"},
  "base": "main",
  "profile": "codex",
  "created_at": "2026-06-22T12:00:00Z"
}`,
		CreatedAt: time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC),
	}}}

	runs, err := Manager{Store: store, AgentsDir: "/tmp/codemesh/agents"}.List(context.Background())

	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("run count = %d, want 1", len(runs))
	}
	got := runs[0]
	if got.ProjectAlias != "codemesh" || got.Base != "main" || got.Profile != "codex" || got.WorkspacePath != "/tmp/codemesh/agents/run-one/workspace" {
		t.Fatalf("run view = %#v", got)
	}
	if got.CreatedAt.Format(time.RFC3339) != "2026-06-22T12:00:00Z" {
		t.Fatalf("created = %s", got.CreatedAt.Format(time.RFC3339))
	}
}

func TestCleanOlderThanDeletesOnlyMatchingManagedRunsAndMetadata(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	oldRun := managedRun(t, agents, "run-old")
	newRun := managedRun(t, agents, "run-new")
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{
		{ID: "run-old", WorkspacePath: filepath.Join(oldRun, "workspace"), MetadataJSON: `{"project":{"alias":"old"},"base":"main"}`, CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "run-new", WorkspacePath: filepath.Join(newRun, "workspace"), MetadataJSON: `{"project":{"alias":"new"},"base":"main"}`, CreatedAt: now.Add(-2 * 24 * time.Hour)},
	}}

	result, err := Manager{Store: store, AgentsDir: agents, Now: func() time.Time { return now }}.Clean(context.Background(), 7*24*time.Hour)

	if err != nil {
		t.Fatalf("Clean error = %v", err)
	}
	if result.Deleted != 1 || result.Kept != 1 {
		t.Fatalf("clean result = %#v, want 1 deleted, 1 kept", result)
	}
	if _, err := os.Stat(oldRun); !os.IsNotExist(err) {
		t.Fatalf("old run dir exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newRun, "workspace", "README.md")); err != nil {
		t.Fatalf("new run workspace missing: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "run-old" {
		t.Fatalf("deleted ids = %#v, want run-old", store.deleted)
	}
}

func TestCleanKeepsLockedRun(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	lockedRun := managedRun(t, agents, "run-locked")
	unlock, err := acquireRunLock(lockedRun)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-locked",
		WorkspacePath: filepath.Join(lockedRun, "workspace"),
		MetadataJSON:  `{"project":{"alias":"locked"},"base":"main"}`,
		CreatedAt:     now.Add(-8 * 24 * time.Hour),
	}}}

	result, err := Manager{Store: store, AgentsDir: agents, Now: func() time.Time { return now }}.Clean(context.Background(), 7*24*time.Hour)

	if err != nil {
		t.Fatalf("Clean error = %v", err)
	}
	if result.Deleted != 0 || result.Kept != 1 {
		t.Fatalf("clean result = %#v, want locked run kept", result)
	}
	if _, err := os.Stat(filepath.Join(lockedRun, "workspace", "README.md")); err != nil {
		t.Fatalf("locked workspace changed: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("metadata deleted despite lock: %#v", store.deleted)
	}
}

func TestCleanDeletesMetadataForMissingManagedRunDir(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	missingWorkspace := filepath.Join(agents, "run-missing", "workspace")
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-missing",
		WorkspacePath: missingWorkspace,
		MetadataJSON:  `{"project":{"alias":"missing"},"base":"main"}`,
		CreatedAt:     now.Add(-8 * 24 * time.Hour),
	}}}

	result, err := Manager{Store: store, AgentsDir: agents, Now: func() time.Time { return now }}.Clean(context.Background(), 7*24*time.Hour)

	if err != nil {
		t.Fatalf("Clean error = %v", err)
	}
	if result.Deleted != 1 || result.Kept != 0 {
		t.Fatalf("clean result = %#v, want missing run metadata deleted", result)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "run-missing" {
		t.Fatalf("deleted ids = %#v, want run-missing", store.deleted)
	}
}

func TestExecuteRecordsCommandContractAndOutputFiles(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	createdAt := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON: `{
  "run_id": "run-one",
  "ready_path": "` + filepath.ToSlash(workspace) + `",
  "project": {"alias": "codemesh", "remote": "https://github.com/bramvr/codemesh"},
  "base": "main",
  "profile": "codex",
  "resolved_commit": "abc123",
  "created_at": "2026-06-23T12:00:00Z"
}`,
		CreatedAt: createdAt,
	}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	result, err := Manager{
		Store:     store,
		AgentsDir: agents,
		Now:       func() time.Time { return createdAt.Add(2 * time.Second) },
	}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "print cwd",
		Command: []string{exe, "-test.run=TestAgentRunHelperCommand"},
		Env:     []string{"CODEMESH_TEST_HELPER_COMMAND=print-cwd-secret-value"},
	})

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.Label != "print cwd" || result.ExitCode != 0 || result.CWD != workspace {
		t.Fatalf("execute result = %#v", result)
	}
	stdout, err := os.ReadFile(result.StdoutPath)
	if err != nil {
		t.Fatalf("read stdout path: %v", err)
	}
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stdoutWorkspace, err := filepath.EvalSymlinks(strings.TrimSpace(string(stdout)))
	if err != nil {
		t.Fatal(err)
	}
	if stdoutWorkspace != canonicalWorkspace {
		t.Fatalf("stdout = %q, want canonical workspace path %q", stdout, canonicalWorkspace)
	}
	if _, err := os.Stat(result.StderrPath); err != nil {
		t.Fatalf("stderr path missing: %v", err)
	}
	metadataBytes, err := os.ReadFile(filepath.Join(workspace, "codemesh-run.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if string(metadataBytes) != store.runs[0].MetadataJSON {
		t.Fatal("metadata file and state store diverged")
	}
	if strings.Contains(string(metadataBytes), "print-cwd-secret-value") {
		t.Fatal("metadata embedded an env value")
	}
	var metadata struct {
		Commands []CommandRecord `json:"commands"`
	}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if len(metadata.Commands) != 1 {
		t.Fatalf("command count = %d, want 1", len(metadata.Commands))
	}
	command := metadata.Commands[0]
	if command.Label != "print cwd" || command.CWD != workspace || command.Env.Mode == "" || command.Base.Base != "main" || command.Base.ResolvedCommit != "abc123" || command.ExitCode != 0 || command.StdoutPath != result.StdoutPath || command.StderrPath != result.StderrPath {
		t.Fatalf("command contract = %#v", command)
	}
	if len(command.Env.Keys) != 1 || command.Env.Keys[0] != "CODEMESH_TEST_HELPER_COMMAND" {
		t.Fatalf("env summary = %#v, want key name only", command.Env)
	}
}

func TestExecuteRefusesWhenRunLockExists(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	unlock, err := acquireRunLock(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}

	_, err = Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "blocked",
		Command: []string{"git", "status"},
	})

	if err == nil {
		t.Fatal("Execute error = nil, want lock refusal")
	}
	if !strings.Contains(err.Error(), "command in progress") {
		t.Fatalf("error = %v", err)
	}
	if len(store.runs) != 1 || strings.Contains(store.runs[0].MetadataJSON, "blocked") {
		t.Fatalf("metadata changed despite lock: %#v", store.runs)
	}
}

func TestExecuteReusesUnlockedRunLockFile(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	lockPath, err := runLockPath(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("leftover lock file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	_, err = Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "after unlocked lock",
		Command: []string{exe, "-test.run=TestAgentRunHelperCommand"},
		Env:     []string{"CODEMESH_TEST_HELPER_COMMAND=print-cwd-secret-value"},
	})

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing after run: %v", err)
	}
	if !strings.Contains(store.runs[0].MetadataJSON, "after unlocked lock") {
		t.Fatalf("metadata was not updated after unlocked lock reuse: %s", store.runs[0].MetadataJSON)
	}
}

func TestExecuteRefusesSymlinkedRunLock(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	lockPath, err := runLockPath(runDir)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.lock")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lockPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}

	_, err = Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "lock symlink",
		Command: []string{"git", "status"},
	})

	if err == nil {
		t.Fatal("Execute error = nil, want lock symlink refusal")
	}
	if !strings.Contains(err.Error(), "symlinked agent run lock") {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside lock target changed: %q", data)
	}
}

func TestExecutePersistsMetadataAfterCallerContextCanceled(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := Manager{Store: store, AgentsDir: agents}.Execute(ctx, ExecuteRequest{
		RunID:   "run-one",
		Label:   "canceled caller",
		Command: []string{exe, "-test.run=TestAgentRunHelperCommand"},
		Env:     []string{"CODEMESH_TEST_HELPER_COMMAND=print-cwd-secret-value"},
	})

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.ExitCode != 130 {
		t.Fatalf("exit code = %d, want 130 for caller cancellation", result.ExitCode)
	}
	if store.canceledUpdate {
		t.Fatal("metadata update used canceled caller context")
	}
	if !strings.Contains(store.runs[0].MetadataJSON, "canceled caller") || !strings.Contains(store.runs[0].MetadataJSON, `"exit_code": 130`) {
		t.Fatalf("metadata was not persisted after cancellation: %s", store.runs[0].MetadataJSON)
	}
}

func TestExecuteRefusesSymlinkedOutputDirectory(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	outside := filepath.Join(root, "outside-output")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(runDir, "outputs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}

	_, err := Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "outside output",
		Command: []string{"git", "status"},
	})

	if err == nil {
		t.Fatal("Execute error = nil, want output symlink refusal")
	}
	if !strings.Contains(err.Error(), "symlinked directory") {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outside output directory changed: %v", entries)
	}
}

func TestExecuteRefusesDanglingSymlinkedOutputDirectoryBeforeCreate(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	outside := filepath.Join(root, "outside-output", "created-by-mkdir")
	if err := os.Symlink(outside, filepath.Join(runDir, "outputs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}

	_, err := Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "dangling output",
		Command: []string{"git", "status"},
	})

	if err == nil {
		t.Fatal("Execute error = nil, want output symlink refusal")
	}
	if !strings.Contains(err.Error(), "symlinked directory") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(outside)); !os.IsNotExist(err) {
		t.Fatalf("outside output parent exists or stat failed: %v", err)
	}
}

func TestExecuteMapsTimeoutToExitCode124(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	runDir := managedRun(t, agents, "run-one")
	workspace := filepath.Join(runDir, "workspace")
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	result, err := Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "timeout",
		Command: []string{exe, "-test.run=TestAgentRunHelperCommand"},
		Env:     []string{"CODEMESH_TEST_HELPER_COMMAND=sleep"},
		Timeout: time.Millisecond,
	})

	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.ExitCode != 124 {
		t.Fatalf("exit code = %d, want 124", result.ExitCode)
	}
	if !strings.Contains(store.runs[0].MetadataJSON, `"exit_code": 124`) {
		t.Fatalf("metadata missing timeout exit code: %s", store.runs[0].MetadataJSON)
	}
}

func TestWriteMetadataFileRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "codemesh-run.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := writeMetadataFile(workspace, []byte("{}\n"))

	if err == nil {
		t.Fatal("writeMetadataFile error = nil, want symlink refusal")
	}
	if !strings.Contains(err.Error(), "symlinked metadata") {
		t.Fatalf("error = %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside target changed: %q", data)
	}
}

func TestExecuteRefusesSymlinkedRunDirectory(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	outsideRun := filepath.Join(root, "outside-run")
	if err := os.MkdirAll(filepath.Join(outsideRun, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(agents, "run-one")
	if err := os.Symlink(outsideRun, runDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	workspace := filepath.Join(runDir, "workspace")
	store := &memoryStore{runs: []state.AgentRun{{
		ID:            "run-one",
		WorkspacePath: workspace,
		MetadataJSON:  `{"run_id":"run-one","ready_path":"` + filepath.ToSlash(workspace) + `","project":{"alias":"codemesh"},"base":"main","created_at":"2026-06-23T12:00:00Z"}`,
		CreatedAt:     time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	}}}

	_, err := Manager{Store: store, AgentsDir: agents}.Execute(context.Background(), ExecuteRequest{
		RunID:   "run-one",
		Label:   "outside",
		Command: []string{"git", "status"},
	})

	if err == nil {
		t.Fatal("Execute error = nil, want symlink refusal")
	}
	if !strings.Contains(err.Error(), "symlinked agent run directory") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outsideRun, "outputs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside output dir exists or stat failed: %v", err)
	}
}

func TestCleanRefusesUnsafePathBeforeDeletingAnything(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	safeRun := managedRun(t, agents, "run-safe")
	outside := filepath.Join(root, "outside", "workspace")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{
		{ID: "run-safe", WorkspacePath: filepath.Join(safeRun, "workspace"), MetadataJSON: `{"project":{"alias":"safe"},"base":"main"}`, CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "run-unsafe", WorkspacePath: outside, MetadataJSON: `{"project":{"alias":"unsafe"},"base":"main"}`, CreatedAt: now.Add(-8 * 24 * time.Hour)},
	}}

	_, err := Manager{Store: store, AgentsDir: agents, Now: func() time.Time { return now }}.Clean(context.Background(), 7*24*time.Hour)

	if err == nil {
		t.Fatal("Clean error = nil, want unsafe path refusal")
	}
	if !strings.Contains(err.Error(), "outside CodeMesh-managed agents storage") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(safeRun, "workspace", "README.md")); statErr != nil {
		t.Fatalf("safe run was deleted before refusal: %v", statErr)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("outside path changed: %v", statErr)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("metadata deleted despite unsafe path: %#v", store.deleted)
	}
}

func TestCleanRefusesMalformedRunIDBeforeDeletingAnything(t *testing.T) {
	root := t.TempDir()
	agents := filepath.Join(root, "agents")
	safeRun := managedRun(t, agents, "run-safe")
	outsideWorkspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(outsideWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	store := &memoryStore{runs: []state.AgentRun{
		{ID: "run-safe", WorkspacePath: filepath.Join(safeRun, "workspace"), MetadataJSON: `{"project":{"alias":"safe"},"base":"main"}`, CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "..", WorkspacePath: outsideWorkspace, MetadataJSON: `{"project":{"alias":"unsafe"},"base":"main"}`, CreatedAt: now.Add(-8 * 24 * time.Hour)},
	}}

	_, err := Manager{Store: store, AgentsDir: agents, Now: func() time.Time { return now }}.Clean(context.Background(), 7*24*time.Hour)

	if err == nil {
		t.Fatal("Clean error = nil, want malformed id refusal")
	}
	if !strings.Contains(err.Error(), "invalid agent run id") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(safeRun, "workspace", "README.md")); statErr != nil {
		t.Fatalf("safe run was deleted before refusal: %v", statErr)
	}
	if _, statErr := os.Stat(outsideWorkspace); statErr != nil {
		t.Fatalf("outside path changed: %v", statErr)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("metadata deleted despite malformed id: %#v", store.deleted)
	}
}

type memoryStore struct {
	runs           []state.AgentRun
	deleted        []string
	canceledUpdate bool
}

func (s *memoryStore) ListAgentRuns(context.Context) ([]state.AgentRun, error) {
	return append([]state.AgentRun(nil), s.runs...), nil
}

func (s *memoryStore) DeleteAgentRuns(_ context.Context, ids []string) error {
	s.deleted = append(s.deleted, ids...)
	kept := s.runs[:0]
	for _, run := range s.runs {
		deleteRun := false
		for _, id := range ids {
			if run.ID == id {
				deleteRun = true
				break
			}
		}
		if !deleteRun {
			kept = append(kept, run)
		}
	}
	s.runs = kept
	return nil
}

func (s *memoryStore) UpdateAgentRunMetadata(ctx context.Context, id, metadataJSON string) error {
	if ctx.Err() != nil {
		s.canceledUpdate = true
	}
	for i := range s.runs {
		if s.runs[i].ID == id {
			s.runs[i].MetadataJSON = metadataJSON
			return nil
		}
	}
	return fmt.Errorf("unknown run: %s", id)
}

func managedRun(t *testing.T, agents, id string) string {
	t.Helper()
	workspace := filepath.Join(agents, id, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(agents, id)
}

func TestAgentRunHelperCommand(t *testing.T) {
	if os.Getenv("CODEMESH_TEST_HELPER_COMMAND") == "" {
		return
	}
	if os.Getenv("CODEMESH_TEST_HELPER_COMMAND") == "sleep" {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
	if os.Getenv("CODEMESH_TEST_HELPER_COMMAND") != "print-cwd-secret-value" {
		os.Exit(2)
	}
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(3)
	}
	if pwd := os.Getenv("PWD"); pwd != "" {
		realPWD, pwdErr := filepath.EvalSymlinks(pwd)
		realCWD, cwdErr := filepath.EvalSymlinks(cwd)
		if pwdErr != nil || cwdErr != nil || realPWD != realCWD {
			os.Exit(4)
		}
	}
	fmt.Fprintln(os.Stdout, cwd)
	os.Exit(0)
}
