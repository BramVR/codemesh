package agentruns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/state"
)

type Store interface {
	ListAgentRuns(context.Context) ([]state.AgentRun, error)
	DeleteAgentRuns(context.Context, []string) error
}

type Manager struct {
	Store     Store
	AgentsDir string
	Now       func() time.Time
}

type Run struct {
	ID            string
	ProjectAlias  string
	Base          string
	Profile       string
	CreatedAt     time.Time
	WorkspacePath string
}

type CleanResult struct {
	Deleted int
	Kept    int
}

type runMetadata struct {
	Project struct {
		Alias string `json:"alias"`
	} `json:"project"`
	Base      string `json:"base"`
	Profile   string `json:"profile"`
	CreatedAt string `json:"created_at"`
}

func (m Manager) List(ctx context.Context) ([]Run, error) {
	if m.Store == nil {
		return nil, errors.New("agent runs store is required")
	}
	rows, err := m.Store.ListAgentRuns(ctx)
	if err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(rows))
	for _, row := range rows {
		view, err := runFromState(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, view)
	}
	return runs, nil
}

func (m Manager) Clean(ctx context.Context, olderThan time.Duration) (CleanResult, error) {
	if m.Store == nil {
		return CleanResult{}, errors.New("agent runs store is required")
	}
	if strings.TrimSpace(m.AgentsDir) == "" {
		return CleanResult{}, errors.New("agents directory is required")
	}
	if olderThan < 0 {
		return CleanResult{}, errors.New("older-than duration must be non-negative")
	}
	rows, err := m.Store.ListAgentRuns(ctx)
	if err != nil {
		return CleanResult{}, err
	}
	cutoff := m.now().Add(-olderThan)
	var candidates []state.AgentRun
	result := CleanResult{}
	for _, row := range rows {
		if row.CreatedAt.After(cutoff) {
			result.Kept++
			continue
		}
		candidates = append(candidates, row)
	}

	type deletion struct {
		id     string
		runDir string
	}
	deletions := make([]deletion, 0, len(candidates))
	for _, row := range candidates {
		runDir, err := m.managedRunDir(row)
		if err != nil {
			return CleanResult{}, err
		}
		deletions = append(deletions, deletion{id: row.ID, runDir: runDir})
	}

	var ids []string
	for _, deletion := range deletions {
		if err := removeManagedRunDir(deletion.runDir); err != nil {
			return CleanResult{}, err
		}
		ids = append(ids, deletion.id)
	}
	if err := m.Store.DeleteAgentRuns(ctx, ids); err != nil {
		return CleanResult{}, err
	}
	result.Deleted = len(ids)
	return result, nil
}

func (m Manager) managedRunDir(run state.AgentRun) (string, error) {
	if err := validateRunID(run.ID); err != nil {
		return "", err
	}
	agents, err := filepath.Abs(filepath.Clean(m.AgentsDir))
	if err != nil {
		return "", err
	}
	workspace, err := filepath.Abs(filepath.Clean(run.WorkspacePath))
	if err != nil {
		return "", err
	}
	expected := filepath.Join(agents, run.ID, "workspace")
	if workspace != expected {
		return "", fmt.Errorf("refusing to clean agent run %q: workspace path is outside CodeMesh-managed agents storage: %s", run.ID, run.WorkspacePath)
	}
	return filepath.Dir(workspace), nil
}

func validateRunID(id string) error {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return fmt.Errorf("invalid agent run id %q", id)
	}
	return nil
}

func removeManagedRunDir(runDir string) error {
	info, err := os.Lstat(runDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("check agent run directory %q: %w", runDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to clean symlinked agent run directory: %s", runDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to clean non-directory agent run path: %s", runDir)
	}
	if err := os.RemoveAll(runDir); err != nil {
		return fmt.Errorf("delete agent run directory %q: %w", runDir, err)
	}
	return nil
}

func runFromState(row state.AgentRun) (Run, error) {
	var metadata runMetadata
	if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
		return Run{}, fmt.Errorf("decode agent run %q metadata: %w", row.ID, err)
	}
	created := row.CreatedAt
	if created.IsZero() && metadata.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, metadata.CreatedAt)
		if err != nil {
			return Run{}, fmt.Errorf("parse agent run %q metadata created_at: %w", row.ID, err)
		}
		created = parsed
	}
	return Run{
		ID:            row.ID,
		ProjectAlias:  metadata.Project.Alias,
		Base:          metadata.Base,
		Profile:       metadata.Profile,
		CreatedAt:     created,
		WorkspacePath: row.WorkspacePath,
	}, nil
}

func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
