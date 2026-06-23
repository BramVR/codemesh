package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const databaseName = "codemesh.db"

var (
	ErrAliasConflict  = errors.New("project alias already exists")
	ErrRemoteConflict = errors.New("project remote already exists")
)

type ProjectUpsertAction string

const (
	ProjectUpsertAdded     ProjectUpsertAction = "added"
	ProjectUpsertUpdated   ProjectUpsertAction = "updated"
	ProjectUpsertUnchanged ProjectUpsertAction = "unchanged"
)

type InitResult struct {
	Home            string
	Database        string
	AgentsDir       string
	WorkspaceRoot   string
	CreatedHome     bool
	CreatedDatabase bool
}

type Store interface {
	Migrate(context.Context) error
	SetSetting(context.Context, string, string) error
	Close() error
}

type Project struct {
	ID               int64
	Alias            string
	NormalizedRemote string
	LocalPath        string
}

type SQLiteStore struct {
	db *sql.DB
}

func Initialize(ctx context.Context, home, workspaceRoot string) (InitResult, error) {
	if home == "" {
		return InitResult{}, errors.New("codemesh home is required")
	}
	if workspaceRoot == "" {
		return InitResult{}, errors.New("workspace root is required")
	}

	homeExisted, err := pathExists(home)
	if err != nil {
		return InitResult{}, err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return InitResult{}, fmt.Errorf("create codemesh home: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return InitResult{}, fmt.Errorf("secure codemesh home: %w", err)
	}
	agentsDir := filepath.Join(home, "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		return InitResult{}, fmt.Errorf("create agents dir: %w", err)
	}
	if err := os.Chmod(agentsDir, 0o700); err != nil {
		return InitResult{}, fmt.Errorf("secure agents dir: %w", err)
	}

	dbPath := filepath.Join(home, databaseName)
	dbExisted, err := pathExists(dbPath)
	if err != nil {
		return InitResult{}, err
	}
	store, err := Open(dbPath)
	if err != nil {
		return InitResult{}, err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return InitResult{}, err
	}
	if err := store.SetSetting(ctx, "default_workspace_root", workspaceRoot); err != nil {
		return InitResult{}, err
	}

	return InitResult{
		Home:            home,
		Database:        dbPath,
		AgentsDir:       agentsDir,
		WorkspaceRoot:   workspaceRoot,
		CreatedHome:     !homeExisted,
		CreatedDatabase: !dbExisted,
	}, nil
}

func Open(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	if err := ensurePrivateFile(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
create table if not exists schema_migrations (
  version integer primary key,
  applied_at text not null
);
`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from schema_migrations where version = 1`).Scan(&exists); err != nil {
		return fmt.Errorf("check migration 1: %w", err)
	}
	if exists == 0 {
		for _, stmt := range migration1 {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply migration 1: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version, applied_at) values(1, ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("record migration 1: %w", err)
		}
	}
	if err := applyMigration(ctx, tx, 2, migration2); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

type migrationTx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func applyMigration(ctx context.Context, tx migrationTx, version int, statements []string) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from schema_migrations where version = ?`, version).Scan(&exists); err != nil {
		return fmt.Errorf("check migration %d: %w", version, err)
	}
	if exists != 0 {
		return nil
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version, applied_at) values(?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	return nil
}

func (s *SQLiteStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
insert into settings(key, value, updated_at)
values(?, ?, ?)
on conflict(key) do update set value = excluded.value, updated_at = excluded.updated_at
`, key, value, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) AddProject(ctx context.Context, project Project) (Project, error) {
	if project.Alias == "" {
		return Project{}, errors.New("project alias is required")
	}
	if project.NormalizedRemote == "" {
		return Project{}, errors.New("project normalized remote is required")
	}
	if project.LocalPath == "" {
		return Project{}, errors.New("project local path is required")
	}

	var existingID int64
	err := s.db.QueryRowContext(ctx, `select id from projects where alias = ?`, project.Alias).Scan(&existingID)
	if err == nil {
		return Project{}, fmt.Errorf("%w: alias %q already exists; choose a different name with --alias", ErrAliasConflict, project.Alias)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("check project alias %q: %w", project.Alias, err)
	}
	var existingAlias string
	err = s.db.QueryRowContext(ctx, `select alias from projects where normalized_remote = ?`, project.NormalizedRemote).Scan(&existingAlias)
	if err == nil {
		return Project{}, fmt.Errorf("%w: remote %q already exists as alias %q", ErrRemoteConflict, project.NormalizedRemote, existingAlias)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("check project remote %q: %w", project.NormalizedRemote, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.ExecContext(ctx, `
insert into projects(alias, normalized_remote, local_path, created_at, updated_at)
values(?, ?, ?, ?, ?)
`, project.Alias, project.NormalizedRemote, project.LocalPath, now, now)
	if err != nil {
		return Project{}, fmt.Errorf("add project %q: %w", project.Alias, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Project{}, fmt.Errorf("read project id: %w", err)
	}
	project.ID = id
	return project, nil
}

func (s *SQLiteStore) UpsertProject(ctx context.Context, project Project) (Project, ProjectUpsertAction, error) {
	if project.Alias == "" {
		return Project{}, "", errors.New("project alias is required")
	}
	if project.NormalizedRemote == "" {
		return Project{}, "", errors.New("project normalized remote is required")
	}
	if project.LocalPath == "" {
		return Project{}, "", errors.New("project local path is required")
	}

	var existing Project
	err := s.db.QueryRowContext(ctx, `
select id, alias, normalized_remote, local_path
from projects
where normalized_remote = ?
order by id
limit 1
`, project.NormalizedRemote).Scan(&existing.ID, &existing.Alias, &existing.NormalizedRemote, &existing.LocalPath)
	if err == nil {
		if existing.LocalPath == project.LocalPath {
			return existing, ProjectUpsertUnchanged, nil
		}
		if _, err := s.db.ExecContext(ctx, `
update projects
set local_path = ?, updated_at = ?
where id = ?
`, project.LocalPath, time.Now().UTC().Format(time.RFC3339), existing.ID); err != nil {
			return Project{}, "", fmt.Errorf("update project %q path: %w", existing.Alias, err)
		}
		existing.LocalPath = project.LocalPath
		return existing, ProjectUpsertUpdated, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, "", fmt.Errorf("check project remote %q: %w", project.NormalizedRemote, err)
	}

	added, err := s.AddProject(ctx, project)
	if err != nil {
		return Project{}, "", err
	}
	return added, ProjectUpsertAdded, nil
}

func (s *SQLiteStore) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, alias, normalized_remote, local_path
from projects
order by alias
`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var project Project
		if err := rows.Scan(&project.ID, &project.Alias, &project.NormalizedRemote, &project.LocalPath); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func ensurePrivateFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create state database: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close state database: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure state database: %w", err)
	}
	return nil
}

var migration1 = []string{
	`create table if not exists settings (
  key text primary key,
  value text not null,
  updated_at text not null
)`,
	`create table if not exists projects (
  id integer primary key,
  alias text not null unique,
  normalized_remote text not null,
  local_path text not null,
  created_at text not null,
  updated_at text not null
)`,
	`create table if not exists machines (
  id integer primary key,
  name text not null,
  created_at text not null
)`,
	`create table if not exists scans (
  id integer primary key,
  workspace_root text not null,
  scanned_at text not null
)`,
	`create table if not exists agent_runs (
  id text primary key,
  project_id integer,
  workspace_path text not null,
  metadata_json text not null,
  created_at text not null,
  foreign key(project_id) references projects(id)
)`,
}

var migration2 = []string{
	`update projects
set
  local_path = (
    select latest.local_path
    from projects latest
    where latest.normalized_remote = projects.normalized_remote
    order by latest.updated_at desc, latest.id desc
    limit 1
  ),
  updated_at = (
    select latest.updated_at
    from projects latest
    where latest.normalized_remote = projects.normalized_remote
    order by latest.updated_at desc, latest.id desc
    limit 1
  )
where id in (
  select min(id)
  from projects
  group by normalized_remote
  having count(*) > 1
)`,
	`delete from projects
where id not in (
  select keep_id
  from (
    select min(id) as keep_id
    from projects
    group by normalized_remote
  )
)`,
	`create unique index if not exists projects_normalized_remote_unique on projects(normalized_remote)`,
}
