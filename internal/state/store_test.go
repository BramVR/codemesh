package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitializeCreatesHomeDatabaseAgentsAndWorkspaceSetting(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Initialize(ctx, home, workspace)

	if err != nil {
		t.Fatalf("Initialize error = %v", err)
	}
	if !result.CreatedHome {
		t.Fatalf("CreatedHome = false, want true")
	}
	if _, err := os.Stat(filepath.Join(home, "agents")); err != nil {
		t.Fatalf("agents dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "codemesh.db")); err != nil {
		t.Fatalf("database missing: %v", err)
	}
	assertMode(t, home, 0o700)
	assertMode(t, filepath.Join(home, "agents"), 0o700)
	assertMode(t, filepath.Join(home, "codemesh.db"), 0o600)

	db := openRawDB(t, filepath.Join(home, "codemesh.db"))
	defer db.Close()
	if got := setting(t, db, "default_workspace_root"); got != workspace {
		t.Fatalf("default workspace root = %q, want %q", got, workspace)
	}
}

func TestInitializeIsSafeToRerun(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	first, err := Initialize(ctx, home, workspace)
	if err != nil {
		t.Fatalf("first Initialize error = %v", err)
	}
	second, err := Initialize(ctx, home, workspace)
	if err != nil {
		t.Fatalf("second Initialize error = %v", err)
	}

	if !first.CreatedDatabase {
		t.Fatalf("first CreatedDatabase = false, want true")
	}
	if second.CreatedDatabase {
		t.Fatalf("second CreatedDatabase = true, want false")
	}

	db := openRawDB(t, filepath.Join(home, "codemesh.db"))
	defer db.Close()
	var applied int
	if err := db.QueryRow(`select count(*) from schema_migrations where version = 1`).Scan(&applied); err != nil {
		t.Fatalf("count migration: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration version 1 count = %d, want 1", applied)
	}
}

func TestInitializeTightensExistingStatePermissions(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(home, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "codemesh.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Initialize(ctx, home, workspace); err != nil {
		t.Fatalf("Initialize error = %v", err)
	}

	assertMode(t, home, 0o700)
	assertMode(t, filepath.Join(home, "agents"), 0o700)
	assertMode(t, filepath.Join(home, "codemesh.db"), 0o600)
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codemesh.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate error = %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate error = %v", err)
	}

	var applied int
	if err := store.db.QueryRowContext(ctx, `select count(*) from schema_migrations where version = 1`).Scan(&applied); err != nil {
		t.Fatalf("count migration: %v", err)
	}
	if applied != 1 {
		t.Fatalf("migration version 1 count = %d, want 1", applied)
	}
}

func TestWithTransactionRollsBackProjectMutations(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "codemesh.db"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	err = store.WithTransaction(ctx, func(tx *SQLiteStore) error {
		if _, err := tx.AddProject(ctx, Project{
			Alias:            "alpha",
			NormalizedRemote: "https://github.com/BramVR/alpha",
			CloneURL:         "https://github.com/BramVR/alpha.git",
			LocalPath:        filepath.Join(t.TempDir(), "alpha"),
		}); err != nil {
			return err
		}
		return errors.New("rollback marker")
	})
	if err == nil || !strings.Contains(err.Error(), "rollback marker") {
		t.Fatalf("WithTransaction error = %v, want rollback marker", err)
	}
	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects after rollback = %#v, want none", projects)
	}
}

func TestMigrateDeduplicatesRemoteRowsBeforeUniqueIndex(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codemesh.db")
	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`create table schema_migrations (version integer primary key, applied_at text not null)`); err != nil {
		t.Fatalf("create schema_migrations fixture: %v", err)
	}
	for _, stmt := range migration1 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 1 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`insert into schema_migrations(version, applied_at) values(1, ?)`, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("record migration 1 fixture: %v", err)
	}
	if _, err := db.Exec(`
insert into projects(alias, normalized_remote, local_path, created_at, updated_at)
values
  ('one', 'https://github.com/BramVR/codemesh', '/tmp/old', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
  ('two', 'https://github.com/BramVR/codemesh', '/tmp/new', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')
`); err != nil {
		t.Fatalf("insert duplicate remote fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
	if projects[0].Alias != "one" {
		t.Fatalf("surviving alias = %q, want oldest alias", projects[0].Alias)
	}
	if projects[0].LocalPath != "/tmp/new" {
		t.Fatalf("surviving path = %q, want latest path", projects[0].LocalPath)
	}
	if projects[0].CloneURL != "https://github.com/BramVR/codemesh" {
		t.Fatalf("surviving clone URL = %q, want normalized fallback", projects[0].CloneURL)
	}
}

func TestMigrateBackfillsCloneURLFromPresentCheckoutOrigin(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codemesh.db")
	checkout := filepath.Join(t.TempDir(), "checkout")
	runGit(t, filepath.Dir(checkout), "init", "-b", "main", checkout)
	runGit(t, checkout, "remote", "add", "origin", "git@github.com:BramVR/codemesh.git")

	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`create table schema_migrations (version integer primary key, applied_at text not null)`); err != nil {
		t.Fatalf("create schema_migrations fixture: %v", err)
	}
	for _, stmt := range migration1 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 1 fixture: %v", err)
		}
	}
	for _, stmt := range migration2 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 2 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`insert into schema_migrations(version, applied_at) values(1, ?), (2, ?)`, "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("record migration fixtures: %v", err)
	}
	if _, err := db.Exec(`
insert into projects(alias, normalized_remote, local_path, created_at, updated_at)
values ('codemesh', 'https://github.com/BramVR/codemesh', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
`, checkout); err != nil {
		t.Fatalf("insert old project fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
	if projects[0].CloneURL != "git@github.com:BramVR/codemesh.git" {
		t.Fatalf("clone URL = %q, want SSH origin", projects[0].CloneURL)
	}
}

func TestMigrateSkipsCloneURLBackfillWhenOriginIdentityDiffers(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codemesh.db")
	checkout := filepath.Join(t.TempDir(), "checkout")
	runGit(t, filepath.Dir(checkout), "init", "-b", "main", checkout)
	runGit(t, checkout, "remote", "add", "origin", "git@github.com:BramVR/fork.git")
	createOldProjectDatabase(t, dbPath, checkout, "https://github.com/BramVR/codemesh")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if projects[0].CloneURL != "https://github.com/BramVR/codemesh" {
		t.Fatalf("clone URL = %q, want normalized fallback", projects[0].CloneURL)
	}
}

func TestMigrateDoesNotKeepBackfillingCloneURLAfterMigration3(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	checkout := filepath.Join(t.TempDir(), "checkout")
	runGit(t, filepath.Dir(checkout), "init", "-b", "main", checkout)
	runGit(t, checkout, "remote", "add", "origin", "git@github.com:BramVR/codemesh.git")
	if _, err := store.AddProject(ctx, Project{
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "https://github.com/BramVR/codemesh",
		LocalPath:        checkout,
	}); err != nil {
		t.Fatalf("AddProject error = %v", err)
	}

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if projects[0].CloneURL != "https://github.com/BramVR/codemesh" {
		t.Fatalf("clone URL = %q, want unchanged after migration 3 already applied", projects[0].CloneURL)
	}
}

func TestMigrateAddsMachineFactsWithoutLosingExistingState(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "codemesh.db")
	createOldProjectDatabase(t, dbPath, "/tmp/codemesh", "https://github.com/BramVR/codemesh")
	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`insert into machines(name, created_at) values('legacy-host', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy machine fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 || projects[0].Alias != "codemesh" {
		t.Fatalf("projects after machine migration = %#v, want existing project preserved", projects)
	}
	machine, err := store.RegisterMachine(ctx, MachineFacts{
		Name:          "Office Mac",
		Hostname:      "first-host",
		OS:            "darwin",
		Architecture:  "arm64",
		CodeMeshHome:  "/tmp/codemesh-home",
		WorkspaceRoot: "/tmp/workspace",
	})
	if err != nil {
		t.Fatalf("RegisterMachine error = %v", err)
	}
	if machine.ID == "" || machine.Name != "Office Mac" || machine.Hostname != "first-host" || machine.OS != "darwin" || machine.Architecture != "arm64" || machine.CodeMeshHome != "/tmp/codemesh-home" || machine.WorkspaceRoot != "/tmp/workspace" || machine.CreatedAt.IsZero() || machine.UpdatedAt.IsZero() {
		t.Fatalf("machine facts = %#v", machine)
	}
	var machineRows int
	if err := store.db.QueryRowContext(ctx, `select count(*) from machines`).Scan(&machineRows); err != nil {
		t.Fatalf("count machines: %v", err)
	}
	if machineRows != 1 {
		t.Fatalf("machine row count = %d, want legacy row reused", machineRows)
	}
}

func TestMigrateBackfillsMachineCodeMeshHome(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "codemesh-home")
	dbPath := filepath.Join(home, "codemesh.db")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`create table schema_migrations (version integer primary key, applied_at text not null)`); err != nil {
		t.Fatalf("create schema_migrations fixture: %v", err)
	}
	for _, migration := range [][]string{migration1, migration2, migration3, migration4, migration5} {
		for _, stmt := range migration {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("apply migration fixture: %v", err)
			}
		}
	}
	if _, err := db.Exec(`insert into schema_migrations(version, applied_at) values(1, ?), (2, ?), (3, ?), (4, ?), (5, ?)`, "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", "2026-01-01T00:00:02Z", "2026-01-01T00:00:03Z", "2026-01-01T00:00:04Z"); err != nil {
		t.Fatalf("record migration fixtures: %v", err)
	}
	if _, err := db.Exec(`
insert into machines(name, machine_id, hostname, os, architecture, workspace_root, registered_at, created_at, updated_at)
values('Travel Laptop', 'machine_existing', 'host', 'darwin', 'arm64', '/tmp/workspace', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
`); err != nil {
		t.Fatalf("insert machine fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}

	machines, err := store.ListMachines(ctx)
	if err != nil {
		t.Fatalf("ListMachines error = %v", err)
	}
	if len(machines) != 1 || machines[0].CodeMeshHome != home {
		t.Fatalf("machines after home backfill = %#v, want CodeMesh home %q", machines, home)
	}
}

func TestRegisterMachineReusesIDAndUpdatesMutableFacts(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	first, err := store.RegisterMachine(ctx, MachineFacts{
		Name:          "First Name",
		Hostname:      "first-host",
		OS:            "darwin",
		Architecture:  "arm64",
		CodeMeshHome:  "/tmp/home-one",
		WorkspaceRoot: "/tmp/one",
	})
	if err != nil {
		t.Fatalf("first RegisterMachine error = %v", err)
	}
	second, err := store.RegisterMachine(ctx, MachineFacts{
		Name:          "Second Name",
		Hostname:      "second-host",
		OS:            "linux",
		Architecture:  "amd64",
		CodeMeshHome:  "/tmp/home-two",
		WorkspaceRoot: "/tmp/two",
	})
	if err != nil {
		t.Fatalf("second RegisterMachine error = %v", err)
	}

	if second.ID != first.ID {
		t.Fatalf("machine ID changed: first %q second %q", first.ID, second.ID)
	}
	if second.CreatedAt != first.CreatedAt {
		t.Fatalf("created_at changed: first %s second %s", first.CreatedAt, second.CreatedAt)
	}
	if second.Name != "Second Name" || second.Hostname != "second-host" || second.OS != "linux" || second.Architecture != "amd64" || second.CodeMeshHome != "/tmp/home-two" || second.WorkspaceRoot != "/tmp/two" {
		t.Fatalf("mutable facts not updated: %#v", second)
	}
	machines, err := store.ListMachines(ctx)
	if err != nil {
		t.Fatalf("ListMachines error = %v", err)
	}
	if len(machines) != 1 {
		t.Fatalf("machine rows = %d, want 1", len(machines))
	}
	if machines[0].Name != "Second Name" || machines[0].CodeMeshHome != "/tmp/home-two" {
		t.Fatalf("listed machine facts not updated: %#v", machines[0])
	}
}

func TestRegisterMachinePreservesDisplayNameWhenNameOmitted(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	first, err := store.RegisterMachine(ctx, MachineFacts{
		Name:          "Travel Laptop",
		Hostname:      "first-host",
		OS:            "darwin",
		Architecture:  "arm64",
		CodeMeshHome:  "/tmp/home-one",
		WorkspaceRoot: "/tmp/one",
	})
	if err != nil {
		t.Fatalf("first RegisterMachine error = %v", err)
	}
	second, err := store.RegisterMachine(ctx, MachineFacts{
		Hostname:      "renamed-host",
		OS:            "darwin",
		Architecture:  "arm64",
		CodeMeshHome:  "/tmp/home-two",
		WorkspaceRoot: "/tmp/two",
	})
	if err != nil {
		t.Fatalf("second RegisterMachine error = %v", err)
	}

	if second.ID != first.ID || second.Name != "Travel Laptop" || second.Hostname != "renamed-host" || second.CodeMeshHome != "/tmp/home-two" || second.WorkspaceRoot != "/tmp/two" {
		t.Fatalf("machine after omitted name update = %#v", second)
	}
}

func TestListAndDeleteAgentRuns(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

	if err := store.RecordAgentRun(ctx, AgentRun{
		ID:            "run-old",
		ProjectID:     42,
		WorkspacePath: "/tmp/codemesh/agents/run-old/workspace",
		MetadataJSON:  `{"run_id":"run-old"}`,
		CreatedAt:     now.Add(-8 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("RecordAgentRun old error = %v", err)
	}
	if err := store.RecordAgentRun(ctx, AgentRun{
		ID:            "run-new",
		ProjectID:     42,
		WorkspacePath: "/tmp/codemesh/agents/run-new/workspace",
		MetadataJSON:  `{"run_id":"run-new"}`,
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("RecordAgentRun new error = %v", err)
	}

	runs, err := store.ListAgentRuns(ctx)
	if err != nil {
		t.Fatalf("ListAgentRuns error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("run count = %d, want 2", len(runs))
	}
	if runs[0].ID != "run-new" || runs[1].ID != "run-old" {
		t.Fatalf("runs order = %#v, want newest first", runs)
	}

	if err := store.DeleteAgentRuns(ctx, []string{"run-old"}); err != nil {
		t.Fatalf("DeleteAgentRuns error = %v", err)
	}
	runs, err = store.ListAgentRuns(ctx)
	if err != nil {
		t.Fatalf("ListAgentRuns after delete error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-new" {
		t.Fatalf("remaining runs = %#v, want run-new only", runs)
	}
}

func TestCloneURLForStoreStripsURLPasswords(t *testing.T) {
	got := cloneURLForStore("ssh://git:secret@example.invalid/org/repo.git", "")

	if got != "ssh://git@example.invalid/org/repo.git" {
		t.Fatalf("clone URL = %q, want password stripped", got)
	}
}

func TestAddProjectPersistsProject(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	project, err := store.AddProject(ctx, Project{
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "git@github.com:BramVR/codemesh.git",
		LocalPath:        "/tmp/codemesh",
	})
	if err != nil {
		t.Fatalf("AddProject error = %v", err)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
	if projects[0].ID != project.ID {
		t.Fatalf("project id = %d, want %d", projects[0].ID, project.ID)
	}
	if projects[0].Alias != "codemesh" || projects[0].NormalizedRemote != "https://github.com/BramVR/codemesh" || projects[0].CloneURL != "git@github.com:BramVR/codemesh.git" || projects[0].LocalPath != "/tmp/codemesh" {
		t.Fatalf("project = %#v", projects[0])
	}
}

func TestRecordAgentRunPersistsMetadata(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	project, err := store.AddProject(ctx, Project{
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		LocalPath:        "/tmp/codemesh",
	})
	if err != nil {
		t.Fatalf("AddProject error = %v", err)
	}
	createdAt := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	if err := store.RecordAgentRun(ctx, AgentRun{
		ID:            "run-test",
		ProjectID:     project.ID,
		WorkspacePath: "/tmp/codemesh-agent",
		MetadataJSON:  `{"ready_path":"/tmp/codemesh-agent"}`,
		CreatedAt:     createdAt,
	}); err != nil {
		t.Fatalf("RecordAgentRun error = %v", err)
	}

	var workspacePath, metadataJSON, storedCreatedAt string
	var projectID int64
	if err := store.db.QueryRowContext(ctx, `
select project_id, workspace_path, metadata_json, created_at
from agent_runs
where id = ?
`, "run-test").Scan(&projectID, &workspacePath, &metadataJSON, &storedCreatedAt); err != nil {
		t.Fatalf("read agent run: %v", err)
	}
	if projectID != project.ID || workspacePath != "/tmp/codemesh-agent" || metadataJSON != `{"ready_path":"/tmp/codemesh-agent"}` || storedCreatedAt != createdAt.Format(time.RFC3339) {
		t.Fatalf("agent run row = projectID %d workspace %q metadata %q created %q", projectID, workspacePath, metadataJSON, storedCreatedAt)
	}
}

func TestEnvBindingsPersistPrivateProviderReferencesByProject(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	project, err := store.AddProject(ctx, Project{
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		LocalPath:        "/tmp/codemesh",
	})
	if err != nil {
		t.Fatalf("AddProject error = %v", err)
	}
	binding, err := store.UpsertEnvBinding(ctx, EnvBinding{
		ProjectID:   project.ID,
		Requirement: "CODEMESH_TEST_BOUND_TOKEN",
		Provider:    "fake",
		SecretRef:   "fake://agent-token",
		Scopes:      []string{"codex", "codex", "readonly"},
	})
	if err != nil {
		t.Fatalf("UpsertEnvBinding error = %v", err)
	}
	if binding.ID == 0 {
		t.Fatal("env binding id = 0")
	}

	bindings, err := store.ListEnvBindings(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListEnvBindings error = %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("env bindings = %d, want 1", len(bindings))
	}
	got := bindings[0]
	if got.ProjectID != project.ID || got.Requirement != "CODEMESH_TEST_BOUND_TOKEN" || got.Provider != "fake" || got.SecretRef != "fake://agent-token" || strings.Join(got.Scopes, ",") != "codex,readonly" {
		t.Fatalf("env binding = %#v", got)
	}
}

func TestAddProjectAliasConflictIsActionable(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	if _, err := store.AddProject(ctx, Project{Alias: "codemesh", NormalizedRemote: "https://github.com/BramVR/codemesh", LocalPath: "/tmp/one"}); err != nil {
		t.Fatalf("first AddProject error = %v", err)
	}
	_, err := store.AddProject(ctx, Project{Alias: "codemesh", NormalizedRemote: "https://github.com/BramVR/other", LocalPath: "/tmp/two"})

	if err == nil {
		t.Fatalf("second AddProject error = nil, want alias conflict")
	}
	if !errors.Is(err, ErrAliasConflict) {
		t.Fatalf("error = %v, want ErrAliasConflict", err)
	}
	if !strings.Contains(err.Error(), "codemesh") || !strings.Contains(err.Error(), "--alias") {
		t.Fatalf("alias conflict is not actionable: %v", err)
	}
}

func TestAddProjectRemoteConflictIsActionable(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	if _, err := store.AddProject(ctx, Project{Alias: "one", NormalizedRemote: "https://github.com/BramVR/codemesh", LocalPath: "/tmp/one"}); err != nil {
		t.Fatalf("first AddProject error = %v", err)
	}
	_, err := store.AddProject(ctx, Project{Alias: "two", NormalizedRemote: "https://github.com/BramVR/codemesh", LocalPath: "/tmp/two"})

	if err == nil {
		t.Fatalf("second AddProject error = nil, want remote conflict")
	}
	if !errors.Is(err, ErrRemoteConflict) {
		t.Fatalf("error = %v, want ErrRemoteConflict", err)
	}
	if !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "codemesh") {
		t.Fatalf("remote conflict is not actionable: %v", err)
	}
}

func TestUpsertProjectByRemoteUpdatesPathWithoutDuplicate(t *testing.T) {
	ctx := context.Background()
	store := migratedStore(t)
	defer store.Close()

	first, action, err := store.UpsertProject(ctx, Project{
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "git@github.com:BramVR/codemesh.git",
		LocalPath:        "/tmp/old",
	})
	if err != nil {
		t.Fatalf("first UpsertProject error = %v", err)
	}
	if action != ProjectUpsertAdded {
		t.Fatalf("first upsert action = %s, want %s", action, ProjectUpsertAdded)
	}

	second, action, err := store.UpsertProject(ctx, Project{
		Alias:            "ignored",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "ssh://git@github.com/BramVR/codemesh.git",
		LocalPath:        "/tmp/new",
	})
	if err != nil {
		t.Fatalf("second UpsertProject error = %v", err)
	}
	if action != ProjectUpsertUpdated {
		t.Fatalf("second upsert action = %s, want %s", action, ProjectUpsertUpdated)
	}
	if second.ID != first.ID {
		t.Fatalf("updated id = %d, want %d", second.ID, first.ID)
	}
	if second.Alias != "codemesh" {
		t.Fatalf("updated alias = %q, want existing alias", second.Alias)
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects error = %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project count = %d, want 1", len(projects))
	}
	if projects[0].LocalPath != "/tmp/new" {
		t.Fatalf("local path = %q, want updated path", projects[0].LocalPath)
	}
	if projects[0].CloneURL != "ssh://git@github.com/BramVR/codemesh.git" {
		t.Fatalf("clone URL = %q, want updated SSH URL", projects[0].CloneURL)
	}
}

func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	return db
}

func migratedStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "codemesh.db"))
	if err != nil {
		t.Fatalf("Open error = %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate error = %v", err)
	}
	return store
}

func setting(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(`select value from settings where key = ?`, key).Scan(&value); err != nil {
		t.Fatalf("read setting %q: %v", key, err)
	}
	return value
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
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

func createOldProjectDatabase(t *testing.T, dbPath, checkout, normalizedRemote string) {
	t.Helper()
	db := openRawDB(t, dbPath)
	if _, err := db.Exec(`create table schema_migrations (version integer primary key, applied_at text not null)`); err != nil {
		t.Fatalf("create schema_migrations fixture: %v", err)
	}
	for _, stmt := range migration1 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 1 fixture: %v", err)
		}
	}
	for _, stmt := range migration2 {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 2 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`insert into schema_migrations(version, applied_at) values(1, ?), (2, ?)`, "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z"); err != nil {
		t.Fatalf("record migration fixtures: %v", err)
	}
	if _, err := db.Exec(`
insert into projects(alias, normalized_remote, local_path, created_at, updated_at)
values ('codemesh', ?, ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')
`, normalizedRemote, checkout); err != nil {
		t.Fatalf("insert old project fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
