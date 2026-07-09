package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/gitops"
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

type EnvBinding struct {
	ID          int64
	ProjectID   int64
	Requirement string
	Provider    string
	SecretRef   string
	Scopes      []string
}

type MachineFacts struct {
	Name          string
	Hostname      string
	OS            string
	Architecture  string
	CodeMeshHome  string
	WorkspaceRoot string
}

type Machine struct {
	ID            string
	Name          string
	Hostname      string
	OS            string
	Architecture  string
	CodeMeshHome  string
	WorkspaceRoot string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type SQLiteStore struct {
	db   sqlExecutor
	root *sql.DB
	home string
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
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
	home, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolve database directory: %w", err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
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
	return &SQLiteStore{db: db, root: db, home: home}, nil
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

	if s.root == nil {
		return errors.New("migration requires root database handle")
	}
	tx, err := s.root.BeginTx(ctx, nil)
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
	if _, err := applyMigration(ctx, tx, 4, migration4); err != nil {
		return err
	}
	if _, err := applyMigration(ctx, tx, 5, migration5); err != nil {
		return err
	}
	if _, err := applyMigration(ctx, tx, 6, migration6); err != nil {
		return err
	}
	if err := backfillMachineCodeMeshHome(ctx, tx, s.home); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func backfillMachineCodeMeshHome(ctx context.Context, tx *sql.Tx, home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
update machines
set codemesh_home = ?
where machine_id is not null and machine_id != '' and codemesh_home = ''
`, home); err != nil {
		return fmt.Errorf("backfill machine CodeMesh home: %w", err)
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
	output, err := gitops.Process().Output(ctx, localPath, "config", "--get", "remote.origin.url")
	if err != nil {
		return "", false
	}
	origin := strings.TrimSpace(string(output))
	return origin, origin != ""
}

func cloneURLForStore(remote, baseDir string) string {
	return gitops.CloneURLFor(remote, baseDir)
}

func normalizeRemoteForStore(remote, baseDir string) (string, error) {
	return gitops.NormalizeRemoteFrom(remote, baseDir)
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

func (s *SQLiteStore) WithTransaction(ctx context.Context, fn func(*SQLiteStore) error) error {
	if s.root == nil {
		return errors.New("transaction requires root database handle")
	}
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	txStore := &SQLiteStore{db: tx, home: s.home}
	if err := fn(txStore); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w; rollback failed: %v", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
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

func (s *SQLiteStore) UpdateProject(ctx context.Context, id int64, project Project) (Project, error) {
	if id == 0 {
		return Project{}, errors.New("project id is required")
	}
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
	err := s.db.QueryRowContext(ctx, `select id from projects where alias = ? and id != ?`, project.Alias, id).Scan(&existingID)
	if err == nil {
		return Project{}, fmt.Errorf("%w: alias %q already exists; choose a different name", ErrAliasConflict, project.Alias)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("check project alias %q: %w", project.Alias, err)
	}
	err = s.db.QueryRowContext(ctx, `select id from projects where normalized_remote = ? and id != ?`, project.NormalizedRemote, id).Scan(&existingID)
	if err == nil {
		return Project{}, fmt.Errorf("%w: remote %q already exists", ErrRemoteConflict, project.NormalizedRemote)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Project{}, fmt.Errorf("check project remote %q: %w", project.NormalizedRemote, err)
	}

	result, err := s.db.ExecContext(ctx, `
update projects
set alias = ?, normalized_remote = ?, clone_url = ?, local_path = ?, updated_at = ?
where id = ?
`, project.Alias, project.NormalizedRemote, project.CloneURL, project.LocalPath, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return Project{}, fmt.Errorf("update project %q topology: %w", project.Alias, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Project{}, fmt.Errorf("check project %d topology update: %w", id, err)
	}
	if affected == 0 {
		return Project{}, fmt.Errorf("project %d not found", id)
	}
	project.ID = id
	return project, nil
}

func (s *SQLiteStore) DeleteProject(ctx context.Context, id int64) error {
	if id == 0 {
		return errors.New("project id is required")
	}
	result, err := s.db.ExecContext(ctx, `delete from projects where id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check project %d delete: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("project %d not found", id)
	}
	return nil
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

func (s *SQLiteStore) RegisterMachine(ctx context.Context, facts MachineFacts) (Machine, error) {
	if strings.TrimSpace(facts.Hostname) == "" {
		return Machine{}, errors.New("machine hostname is required")
	}
	requestedName := strings.TrimSpace(facts.Name)
	if strings.TrimSpace(facts.OS) == "" {
		return Machine{}, errors.New("machine OS is required")
	}
	if strings.TrimSpace(facts.Architecture) == "" {
		return Machine{}, errors.New("machine architecture is required")
	}
	if strings.TrimSpace(facts.CodeMeshHome) == "" {
		return Machine{}, errors.New("machine CodeMesh home is required")
	}
	if strings.TrimSpace(facts.WorkspaceRoot) == "" {
		return Machine{}, errors.New("machine workspace root is required")
	}

	if s.root == nil {
		return Machine{}, errors.New("machine registration requires root database handle")
	}
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return Machine{}, fmt.Errorf("begin machine registration: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowText := now.Format(time.RFC3339)
	var rowID int64
	var machineID, existingName, createdAtText string
	err = tx.QueryRowContext(ctx, `
select id, coalesce(machine_id, ''), name, coalesce(registered_at, created_at)
from machines
order by case when machine_id is not null and machine_id != '' then 0 else 1 end, id
limit 1
`).Scan(&rowID, &machineID, &existingName, &createdAtText)
	switch {
	case err == nil:
		if machineID == "" {
			machineID, err = newMachineID()
			if err != nil {
				return Machine{}, err
			}
		}
		name := requestedName
		if name == "" {
			name = strings.TrimSpace(existingName)
		}
		if name == "" {
			name = strings.TrimSpace(facts.Hostname)
		}
		if _, err := tx.ExecContext(ctx, `
update machines
set name = ?, machine_id = ?, hostname = ?, os = ?, architecture = ?, codemesh_home = ?, workspace_root = ?, registered_at = ?, updated_at = ?
where id = ?
`, name, machineID, facts.Hostname, facts.OS, facts.Architecture, facts.CodeMeshHome, facts.WorkspaceRoot, createdAtText, nowText, rowID); err != nil {
			return Machine{}, fmt.Errorf("update machine facts: %w", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		machineID, err = newMachineID()
		if err != nil {
			return Machine{}, err
		}
		createdAtText = nowText
		name := requestedName
		if name == "" {
			name = strings.TrimSpace(facts.Hostname)
		}
		if _, err := tx.ExecContext(ctx, `
insert into machines(name, machine_id, hostname, os, architecture, codemesh_home, workspace_root, registered_at, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, name, machineID, facts.Hostname, facts.OS, facts.Architecture, facts.CodeMeshHome, facts.WorkspaceRoot, createdAtText, createdAtText, nowText); err != nil {
			return Machine{}, fmt.Errorf("insert machine facts: %w", err)
		}
	default:
		return Machine{}, fmt.Errorf("read machine registration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Machine{}, fmt.Errorf("commit machine registration: %w", err)
	}

	createdAt, err := time.Parse(time.RFC3339, createdAtText)
	if err != nil {
		return Machine{}, fmt.Errorf("parse machine created_at: %w", err)
	}
	return Machine{
		ID:            machineID,
		Name:          registeredMachineName(requestedName, existingName, facts.Hostname),
		Hostname:      facts.Hostname,
		OS:            facts.OS,
		Architecture:  facts.Architecture,
		CodeMeshHome:  facts.CodeMeshHome,
		WorkspaceRoot: facts.WorkspaceRoot,
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}, nil
}

func registeredMachineName(requestedName, existingName, hostname string) string {
	if requestedName != "" {
		return requestedName
	}
	if strings.TrimSpace(existingName) != "" {
		return strings.TrimSpace(existingName)
	}
	return strings.TrimSpace(hostname)
}

func (s *SQLiteStore) ListMachines(ctx context.Context) ([]Machine, error) {
	rows, err := s.db.QueryContext(ctx, `
select machine_id, name, hostname, os, architecture, codemesh_home, workspace_root, coalesce(registered_at, created_at), updated_at
from machines
where machine_id is not null and machine_id != ''
order by id
`)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	defer rows.Close()

	var machines []Machine
	for rows.Next() {
		var machine Machine
		var createdAt, updatedAt string
		if err := rows.Scan(&machine.ID, &machine.Name, &machine.Hostname, &machine.OS, &machine.Architecture, &machine.CodeMeshHome, &machine.WorkspaceRoot, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan machine: %w", err)
		}
		parsedCreatedAt, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse machine %q created_at: %w", machine.ID, err)
		}
		parsedUpdatedAt, err := time.Parse(time.RFC3339, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse machine %q updated_at: %w", machine.ID, err)
		}
		machine.CreatedAt = parsedCreatedAt
		machine.UpdatedAt = parsedUpdatedAt
		machines = append(machines, machine)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate machines: %w", err)
	}
	return machines, nil
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

func (s *SQLiteStore) ListAgentRuns(ctx context.Context) ([]AgentRun, error) {
	rows, err := s.db.QueryContext(ctx, `
select id, project_id, workspace_path, metadata_json, created_at
from agent_runs
order by created_at desc, id desc
`)
	if err != nil {
		return nil, fmt.Errorf("list agent runs: %w", err)
	}
	defer rows.Close()

	var runs []AgentRun
	for rows.Next() {
		var run AgentRun
		var createdAt string
		if err := rows.Scan(&run.ID, &run.ProjectID, &run.WorkspacePath, &run.MetadataJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan agent run: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse agent run %q created_at: %w", run.ID, err)
		}
		run.CreatedAt = parsed
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent runs: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) UpsertEnvBinding(ctx context.Context, binding EnvBinding) (EnvBinding, error) {
	if binding.ProjectID == 0 {
		return EnvBinding{}, errors.New("env binding project id is required")
	}
	binding.Requirement = strings.TrimSpace(binding.Requirement)
	if binding.Requirement == "" {
		return EnvBinding{}, errors.New("env binding requirement is required")
	}
	binding.Provider = strings.TrimSpace(binding.Provider)
	if binding.Provider == "" {
		return EnvBinding{}, errors.New("env binding provider is required")
	}
	binding.SecretRef = strings.TrimSpace(binding.SecretRef)
	if binding.SecretRef == "" {
		return EnvBinding{}, errors.New("env binding secret reference is required")
	}
	binding.Scopes = normalizedScopes(binding.Scopes)
	if len(binding.Scopes) == 0 {
		return EnvBinding{}, errors.New("env binding must include at least one scope")
	}
	scopesJSON, err := json.Marshal(binding.Scopes)
	if err != nil {
		return EnvBinding{}, fmt.Errorf("encode env binding scopes: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(ctx, `
insert into env_bindings(project_id, requirement, provider, secret_ref, scopes_json, created_at, updated_at)
values(?, ?, ?, ?, ?, ?, ?)
on conflict(project_id, requirement) do update set
  provider = excluded.provider,
  secret_ref = excluded.secret_ref,
  scopes_json = excluded.scopes_json,
  updated_at = excluded.updated_at
`, binding.ProjectID, binding.Requirement, binding.Provider, binding.SecretRef, string(scopesJSON), now, now)
	if err != nil {
		return EnvBinding{}, fmt.Errorf("upsert env binding %q: %w", binding.Requirement, err)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx, `
select id
from env_bindings
where project_id = ? and requirement = ?
`, binding.ProjectID, binding.Requirement).Scan(&id); err != nil {
		return EnvBinding{}, fmt.Errorf("read env binding %q: %w", binding.Requirement, err)
	}
	binding.ID = id
	return binding, nil
}

func (s *SQLiteStore) ListEnvBindings(ctx context.Context, projectID int64) ([]EnvBinding, error) {
	if projectID == 0 {
		return nil, errors.New("env binding project id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
select id, project_id, requirement, provider, secret_ref, scopes_json
from env_bindings
where project_id = ?
order by requirement
`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list env bindings: %w", err)
	}
	defer rows.Close()

	var bindings []EnvBinding
	for rows.Next() {
		var binding EnvBinding
		var scopesJSON string
		if err := rows.Scan(&binding.ID, &binding.ProjectID, &binding.Requirement, &binding.Provider, &binding.SecretRef, &scopesJSON); err != nil {
			return nil, fmt.Errorf("scan env binding: %w", err)
		}
		if err := json.Unmarshal([]byte(scopesJSON), &binding.Scopes); err != nil {
			return nil, fmt.Errorf("decode env binding %q scopes: %w", binding.Requirement, err)
		}
		binding.Scopes = normalizedScopes(binding.Scopes)
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate env bindings: %w", err)
	}
	return bindings, nil
}

func (s *SQLiteStore) DeleteAgentRuns(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if s.root == nil {
		return errors.New("agent run delete requires root database handle")
	}
	tx, err := s.root.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent run delete: %w", err)
	}
	defer tx.Rollback()
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return errors.New("agent run id is required")
		}
		if _, err := tx.ExecContext(ctx, `delete from agent_runs where id = ?`, id); err != nil {
			return fmt.Errorf("delete agent run %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent run delete: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateAgentRunMetadata(ctx context.Context, id, metadataJSON string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("agent run id is required")
	}
	if strings.TrimSpace(metadataJSON) == "" {
		return errors.New("agent run metadata is required")
	}
	result, err := s.db.ExecContext(ctx, `
update agent_runs
set metadata_json = ?
where id = ?
`, metadataJSON, id)
	if err != nil {
		return fmt.Errorf("update agent run %q metadata: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check agent run %q metadata update: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("agent run %q not found", id)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	if s.root == nil {
		return nil
	}
	return s.root.Close()
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

func newMachineID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mint machine id: %w", err)
	}
	return "machine_" + hex.EncodeToString(raw[:]), nil
}

func normalizedScopes(scopes []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
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

var migration4 = []string{
	`alter table machines add column machine_id text`,
	`alter table machines add column hostname text not null default ''`,
	`alter table machines add column os text not null default ''`,
	`alter table machines add column architecture text not null default ''`,
	`alter table machines add column workspace_root text not null default ''`,
	`alter table machines add column registered_at text`,
	`alter table machines add column updated_at text not null default ''`,
	`update machines set updated_at = created_at where updated_at = ''`,
	`create unique index if not exists machines_machine_id_unique on machines(machine_id) where machine_id is not null and machine_id != ''`,
}

var migration5 = []string{
	`create table if not exists env_bindings (
  id integer primary key,
  project_id integer not null,
  requirement text not null,
  provider text not null,
  secret_ref text not null,
  scopes_json text not null,
  created_at text not null,
  updated_at text not null,
  unique(project_id, requirement),
  foreign key(project_id) references projects(id)
)`,
}

var migration6 = []string{
	`alter table machines add column codemesh_home text not null default ''`,
}
