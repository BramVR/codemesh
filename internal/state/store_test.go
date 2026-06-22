package state

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
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

func openRawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	return db
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
