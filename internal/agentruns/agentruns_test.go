package agentruns

import (
	"context"
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
	runs    []state.AgentRun
	deleted []string
}

func (s *memoryStore) ListAgentRuns(context.Context) ([]state.AgentRun, error) {
	return append([]state.AgentRun(nil), s.runs...), nil
}

func (s *memoryStore) DeleteAgentRuns(_ context.Context, ids []string) error {
	s.deleted = append(s.deleted, ids...)
	return nil
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
