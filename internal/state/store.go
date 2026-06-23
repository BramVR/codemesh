package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	CloneURL         string
	LocalPath        string
}

type AgentRun struct {
	ID            string
	ProjectID     int64
	WorkspacePath string
	MetadataJSON  string
	CreatedAt     time.Time
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
	if _, err := applyMigration(ctx, tx, 2, migration2); err != nil {
		return err
	}
	appliedMigration3, err := applyMigration(ctx, tx, 3, migration3)
	if err != nil {
		return err
	}
	if appliedMigration3 {
		if err := backfillCloneURLs(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func backfillCloneURLs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `
select id, normalized_remote, clone_url, local_path
from projects
`)
	if err != nil {
		return fmt.Errorf("list projects for clone URL backfill: %w", err)
	}
	defer rows.Close()

	type backfill struct {
		id       int64
		cloneURL string
	}
	var updates []backfill
	for rows.Next() {
		var id int64
		var normalizedRemote, cloneURL, localPath string
		if err := rows.Scan(&id, &normalizedRemote, &cloneURL, &localPath); err != nil {
			return fmt.Errorf("scan project for clone URL backfill: %w", err)
		}
		if cloneURL != "" && cloneURL != normalizedRemote {
			continue
		}
		origin, ok := gitOriginURL(ctx, localPath)
		if !ok {
			continue
		}
		normalizedOrigin, err := normalizeRemoteForStore(origin, localPath)
		if err != nil || normalizedOrigin != normalizedRemote {
			continue
		}
		backfilled := cloneURLForStore(origin, localPath)
		if backfilled == "" || backfilled == cloneURL {
			continue
		}
		updates = append(updates, backfill{id: id, cloneURL: backfilled})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate projects for clone URL backfill: %w", err)
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx, `
update projects
set clone_url = ?, updated_at = ?
where id = ?
`, update.cloneURL, time.Now().UTC().Format(time.RFC3339), update.id); err != nil {
			return fmt.Errorf("backfill project clone URL: %w", err)
		}
	}
	return nil
}

func gitOriginURL(ctx context.Context, localPath string) (string, bool) {
	info, err := os.Stat(localPath)
	if err != nil || !info.IsDir() {
		return "", false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", localPath, "config", "--get", "remote.origin.url")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}
	origin := strings.TrimSpace(string(output))
	return origin, origin != ""
}

func cloneURLForStore(remote, baseDir string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return remote
	}
	if isSCPLikeCloneURL(remote) {
		return remote
	}
	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if parsed.Scheme == "http" || parsed.Scheme == "https" {
				parsed.User = nil
				return parsed.String()
			}
			if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.User(parsed.User.Username())
				return parsed.String()
			}
		}
		return remote
	}
	if baseDir != "" && !filepath.IsAbs(remote) {
		return filepath.Clean(filepath.Join(baseDir, remote))
	}
	return remote
}

func normalizeRemoteForStore(remote, baseDir string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", errors.New("remote is required")
	}
	if user, host, path, ok := splitSCPLikeCloneURL(remote); ok {
		if host == "github.com" {
			return normalizeGitHubPathForStore(path)
		}
		path = strings.TrimPrefix(path, "/")
		path = strings.TrimSuffix(path, ".git")
		return fmt.Sprintf("ssh://%s@%s/%s", user, host, path), nil
	}

	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" {
		host := strings.ToLower(parsed.Hostname())
		if host == "github.com" {
			return normalizeGitHubPathForStore(parsed.Path)
		}
		if parsed.Scheme == "file" {
			return filepath.Clean(parsed.Path), nil
		}
		host = strings.ToLower(parsed.Host)
		path := strings.TrimSuffix(parsed.EscapedPath(), ".git")
		if parsed.User != nil {
			return fmt.Sprintf("%s://%s@%s%s", parsed.Scheme, parsed.User.Username(), host, path), nil
		}
		return fmt.Sprintf("%s://%s%s", parsed.Scheme, host, path), nil
	}

	if baseDir != "" && !filepath.IsAbs(remote) {
		return filepath.Clean(filepath.Join(baseDir, remote)), nil
	}
	abs, err := filepath.Abs(remote)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func isSCPLikeCloneURL(remote string) bool {
	_, _, _, ok := splitSCPLikeCloneURL(remote)
	return ok
}

func splitSCPLikeCloneURL(remote string) (string, string, string, bool) {
	if strings.Contains(remote, "://") {
		return "", "", "", false
	}
	at := strings.Index(remote, "@")
	if at <= 0 {
		return "", "", "", false
	}
	rest := remote[at+1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 || colon == len(rest)-1 {
		return "", "", "", false
	}
	user := remote[:at]
	host := strings.ToLower(rest[:colon])
	path := rest[colon+1:]
	return user, host, path, true
}

func normalizeGitHubPathForStore(path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" || !strings.Contains(path, "/") {
		return "", fmt.Errorf("invalid GitHub remote path %q", path)
	}
	return "https://github.com/" + path, nil
}

type migrationTx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func applyMigration(ctx context.Context, tx migrationTx, version int, statements []string) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `select count(*) from schema_migrations where version = ?`, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}
	if exists != 0 {
		return false, nil
	}
	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return false, fmt.Errorf("apply migration %d: %w", version, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version, applied_at) values(?, ?)`, version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return false, fmt.Errorf("record migration %d: %w", version, err)
	}
	return true, nil
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
	if project.CloneURL == "" {
		project.CloneURL = project.NormalizedRemote
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
insert into projects(alias, normalized_remote, clone_url, local_path, created_at, updated_at)
values(?, ?, ?, ?, ?, ?)
`, project.Alias, project.NormalizedRemote, project.CloneURL, project.LocalPath, now, now)
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
	if project.CloneURL == "" {
		project.CloneURL = project.NormalizedRemote
	}
	if project.LocalPath == "" {
		return Project{}, "", errors.New("project local path is required")
	}

	var existing Project
	err := s.db.QueryRowContext(ctx, `
select id, alias, normalized_remote, clone_url, local_path
from projects
where normalized_remote = ?
order by id
limit 1
`, project.NormalizedRemote).Scan(&existing.ID, &existing.Alias, &existing.NormalizedRemote, &existing.CloneURL, &existing.LocalPath)
	if err == nil {
		if existing.LocalPath == project.LocalPath && existing.CloneURL == project.CloneURL {
			return existing, ProjectUpsertUnchanged, nil
		}
		if _, err := s.db.ExecContext(ctx, `
update projects
set clone_url = ?, local_path = ?, updated_at = ?
where id = ?
`, project.CloneURL, project.LocalPath, time.Now().UTC().Format(time.RFC3339), existing.ID); err != nil {
			return Project{}, "", fmt.Errorf("update project %q path: %w", existing.Alias, err)
		}
		existing.CloneURL = project.CloneURL
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
select id, alias, normalized_remote, clone_url, local_path
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
		if err := rows.Scan(&project.ID, &project.Alias, &project.NormalizedRemote, &project.CloneURL, &project.LocalPath); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (s *SQLiteStore) RecordAgentRun(ctx context.Context, run AgentRun) error {
	if strings.TrimSpace(run.ID) == "" {
		return errors.New("agent run id is required")
	}
	if strings.TrimSpace(run.WorkspacePath) == "" {
		return errors.New("agent run workspace path is required")
	}
	if strings.TrimSpace(run.MetadataJSON) == "" {
		return errors.New("agent run metadata is required")
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
insert into agent_runs(id, project_id, workspace_path, metadata_json, created_at)
values(?, ?, ?, ?, ?)
`, run.ID, run.ProjectID, run.WorkspacePath, run.MetadataJSON, run.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record agent run %q: %w", run.ID, err)
	}
	return nil
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

var migration3 = []string{
	`alter table projects add column clone_url text not null default ''`,
	`update projects set clone_url = normalized_remote where clone_url = ''`,
}
