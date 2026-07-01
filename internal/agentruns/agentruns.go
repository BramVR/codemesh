package agentruns

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/state"
)

const defaultExecuteTimeout = 10 * time.Minute
const metadataPersistTimeout = 5 * time.Second

var errRunLocked = errors.New("agent run command in progress")

type Store interface {
	ListAgentRuns(context.Context) ([]state.AgentRun, error)
	DeleteAgentRuns(context.Context, []string) error
	UpdateAgentRunMetadata(context.Context, string, string) error
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
	State         string
	CreatedAt     time.Time
	WorkspacePath string
}

type ExecuteRequest struct {
	RunID   string
	Label   string
	Command []string
	Env     []string
	Timeout time.Duration
}

type CommandRecord struct {
	Label      string         `json:"label"`
	CWD        string         `json:"cwd"`
	Env        EnvSummary     `json:"env"`
	Base       BaseProvenance `json:"base_provenance"`
	ExitCode   int            `json:"exit_code"`
	Duration   string         `json:"duration"`
	StdoutPath string         `json:"stdout_path"`
	StderrPath string         `json:"stderr_path"`
	ExecutedAt string         `json:"executed_at"`
}

type EnvSummary struct {
	Mode   string   `json:"mode"`
	Keys   []string `json:"keys,omitempty"`
	Values string   `json:"values"`
}

type BaseProvenance struct {
	Base           string `json:"base"`
	ResolvedCommit string `json:"resolved_commit"`
	Remote         string `json:"remote"`
}

type CleanResult struct {
	Deleted int
	Kept    int
}

type runMetadata struct {
	RunID     string `json:"run_id,omitempty"`
	ReadyPath string `json:"ready_path,omitempty"`
	Project   struct {
		Alias      string `json:"alias"`
		Remote     string `json:"remote,omitempty"`
		CloneURL   string `json:"clone_url,omitempty"`
		SourcePath string `json:"source_path,omitempty"`
		LocalPath  string `json:"local_path,omitempty"`
		ProjectID  int64  `json:"project_id,omitempty"`
	} `json:"project"`
	Base              string          `json:"base"`
	Profile           string          `json:"profile"`
	ResolvedCommit    string          `json:"resolved_commit,omitempty"`
	ReadinessDecision string          `json:"readiness_decision,omitempty"`
	HandoffDocs       json.RawMessage `json:"handoff_docs,omitempty"`
	Diagnostics       json.RawMessage `json:"diagnostics,omitempty"`
	CreatedAt         string          `json:"created_at"`
	Commands          []CommandRecord `json:"commands,omitempty"`
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
		unlock func()
	}
	deletions := make([]deletion, 0, len(candidates))
	defer func() {
		for _, deletion := range deletions {
			deletion.unlock()
		}
	}()
	for _, row := range candidates {
		runDir, err := m.managedRunDir(row)
		if err != nil {
			return CleanResult{}, err
		}
		if _, err := os.Lstat(runDir); errors.Is(err, os.ErrNotExist) {
			deletions = append(deletions, deletion{id: row.ID, runDir: runDir, unlock: func() {}})
			continue
		} else if err != nil {
			return CleanResult{}, fmt.Errorf("check agent run directory %q: %w", runDir, err)
		}
		unlock, err := acquireRunLock(runDir)
		if err != nil {
			if errors.Is(err, errRunLocked) {
				result.Kept++
				continue
			}
			return CleanResult{}, err
		}
		deletions = append(deletions, deletion{id: row.ID, runDir: runDir, unlock: unlock})
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

func (m Manager) Execute(ctx context.Context, req ExecuteRequest) (CommandRecord, error) {
	if m.Store == nil {
		return CommandRecord{}, errors.New("agent runs store is required")
	}
	if strings.TrimSpace(m.AgentsDir) == "" {
		return CommandRecord{}, errors.New("agents directory is required")
	}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return CommandRecord{}, errors.New("agent run id is required")
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		return CommandRecord{}, errors.New("command label is required")
	}
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return CommandRecord{}, errors.New("command is required")
	}
	rows, err := m.Store.ListAgentRuns(ctx)
	if err != nil {
		return CommandRecord{}, err
	}
	row, ok := findAgentRun(rows, runID)
	if !ok {
		return CommandRecord{}, fmt.Errorf("unknown agent run: %s", runID)
	}
	runDir, err := m.managedRunDir(row)
	if err != nil {
		return CommandRecord{}, err
	}
	if err := m.ensureManagedRunStorage(runDir, row.WorkspacePath); err != nil {
		return CommandRecord{}, err
	}
	unlock, err := acquireRunLock(runDir)
	if err != nil {
		return CommandRecord{}, err
	}
	defer unlock()
	rows, err = m.Store.ListAgentRuns(ctx)
	if err != nil {
		return CommandRecord{}, err
	}
	row, ok = findAgentRun(rows, runID)
	if !ok {
		return CommandRecord{}, fmt.Errorf("unknown agent run: %s", runID)
	}
	lockedRunDir, err := m.managedRunDir(row)
	if err != nil {
		return CommandRecord{}, err
	}
	if lockedRunDir != runDir {
		return CommandRecord{}, fmt.Errorf("agent run %q workspace changed while acquiring lock", runID)
	}
	if err := m.ensureManagedRunStorage(lockedRunDir, row.WorkspacePath); err != nil {
		return CommandRecord{}, err
	}
	var metadata runMetadata
	if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil {
		return CommandRecord{}, fmt.Errorf("decode agent run %q metadata: %w", row.ID, err)
	}
	outputDir := filepath.Join(runDir, "outputs")
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return CommandRecord{}, fmt.Errorf("create command output directory: %w", err)
	}
	if err := ensureManagedOutputDir(runDir, outputDir); err != nil {
		return CommandRecord{}, err
	}
	ordinal := len(metadata.Commands) + 1
	fileStem := outputFileStem(ordinal, label)
	stdoutPath := filepath.Join(outputDir, fileStem+".stdout.txt")
	stderrPath := filepath.Join(outputDir, fileStem+".stderr.txt")
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("create command stdout: %w", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("create command stderr: %w", err)
	}
	defer stderrFile.Close()

	timeout := req.Timeout
	if timeout == 0 {
		timeout = defaultExecuteTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.Command(req.Command[0], req.Command[1:]...)
	command.Dir = row.WorkspacePath
	configureCommandProcessGroup(command)
	command.Env = append(command.Environ(), req.Env...)
	command.Stdout = stdoutFile
	command.Stderr = stderrFile

	start := time.Now()
	timedOut := false
	canceled := false
	err = command.Start()
	if err == nil {
		done := make(chan error, 1)
		go func() {
			done <- command.Wait()
		}()
		select {
		case err = <-done:
		case <-runCtx.Done():
			if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
				timedOut = true
			} else {
				canceled = true
			}
			_ = killCommandProcessGroup(command)
			err = <-done
		}
	}
	duration := time.Since(start)
	if closeErr := stdoutFile.Close(); closeErr != nil {
		return CommandRecord{}, fmt.Errorf("close command stdout: %w", closeErr)
	}
	if closeErr := stderrFile.Close(); closeErr != nil {
		return CommandRecord{}, fmt.Errorf("close command stderr: %w", closeErr)
	}
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			if exitCode < 0 {
				exitCode = 1
			}
			if timedOut {
				exitCode = 124
			} else if canceled {
				exitCode = 130
			}
		} else {
			return CommandRecord{}, fmt.Errorf("run command %q: %w", label, err)
		}
	}

	record := CommandRecord{
		Label: label,
		CWD:   row.WorkspacePath,
		Env:   summarizeEnv(req.Env),
		Base: BaseProvenance{
			Base:           metadata.Base,
			ResolvedCommit: metadata.ResolvedCommit,
			Remote:         metadata.Project.Remote,
		},
		ExitCode:   exitCode,
		Duration:   duration.Round(time.Millisecond).String(),
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		ExecutedAt: m.now().UTC().Format(time.RFC3339),
	}
	metadata.Commands = append(metadata.Commands, record)
	metadataJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return CommandRecord{}, fmt.Errorf("encode agent run metadata: %w", err)
	}
	metadataJSON = append(metadataJSON, '\n')
	if err := m.ensureManagedRunStorage(lockedRunDir, row.WorkspacePath); err != nil {
		return CommandRecord{}, err
	}
	if err := writeMetadataFile(row.WorkspacePath, metadataJSON); err != nil {
		return CommandRecord{}, fmt.Errorf("write agent run metadata: %w", err)
	}
	persistCtx, persistCancel := context.WithTimeout(context.Background(), metadataPersistTimeout)
	defer persistCancel()
	if err := m.Store.UpdateAgentRunMetadata(persistCtx, row.ID, string(metadataJSON)); err != nil {
		return CommandRecord{}, err
	}
	return record, nil
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

func findAgentRun(rows []state.AgentRun, id string) (state.AgentRun, bool) {
	for _, row := range rows {
		if row.ID == id {
			return row, true
		}
	}
	return state.AgentRun{}, false
}

func (m Manager) ensureManagedRunStorage(runDir, workspace string) error {
	runInfo, err := os.Lstat(runDir)
	if err != nil {
		return fmt.Errorf("check agent run directory %q: %w", runDir, err)
	}
	if runInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to run in symlinked agent run directory: %s", runDir)
	}
	if !runInfo.IsDir() {
		return fmt.Errorf("refusing to run in non-directory agent run path: %s", runDir)
	}
	info, err := os.Lstat(workspace)
	if err != nil {
		return fmt.Errorf("check agent workspace %q: %w", workspace, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to run in symlinked agent workspace: %s", workspace)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to run in non-directory agent workspace: %s", workspace)
	}
	agents, err := filepath.Abs(filepath.Clean(m.AgentsDir))
	if err != nil {
		return err
	}
	realAgents, err := filepath.EvalSymlinks(agents)
	if err != nil {
		return fmt.Errorf("resolve agents directory %q: %w", agents, err)
	}
	realRunDir, err := filepath.EvalSymlinks(runDir)
	if err != nil {
		return fmt.Errorf("resolve agent run directory %q: %w", runDir, err)
	}
	inside, err := pathInside(realAgents, realRunDir)
	if err != nil {
		return err
	}
	if !inside {
		return fmt.Errorf("refusing to run outside CodeMesh-managed agents storage: %s", runDir)
	}
	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return fmt.Errorf("resolve agent workspace %q: %w", workspace, err)
	}
	inside, err = pathInside(realRunDir, realWorkspace)
	if err != nil {
		return err
	}
	if !inside {
		return fmt.Errorf("refusing to run workspace outside managed run directory: %s", workspace)
	}
	return nil
}

func pathInside(parent, child string) (bool, error) {
	parentPath, err := filepath.Abs(filepath.Clean(parent))
	if err != nil {
		return false, err
	}
	childPath, err := filepath.Abs(filepath.Clean(child))
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(parentPath, childPath)
	if err != nil {
		return false, err
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."), nil
}

func ensureManagedOutputDir(runDir, outputDir string) error {
	info, err := os.Lstat(outputDir)
	if err != nil {
		return fmt.Errorf("check command output directory %q: %w", outputDir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to capture output through symlinked directory: %s", outputDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("refusing to capture output through non-directory path: %s", outputDir)
	}
	realRunDir, err := filepath.EvalSymlinks(runDir)
	if err != nil {
		return fmt.Errorf("resolve agent run directory %q: %w", runDir, err)
	}
	realOutputDir, err := filepath.EvalSymlinks(outputDir)
	if err != nil {
		return fmt.Errorf("resolve command output directory %q: %w", outputDir, err)
	}
	inside, err := pathInside(realRunDir, realOutputDir)
	if err != nil {
		return err
	}
	if !inside {
		return fmt.Errorf("refusing to capture output outside managed run directory: %s", outputDir)
	}
	return nil
}

func runLockPath(runDir string) (string, error) {
	locksDir := filepath.Join(filepath.Dir(runDir), ".locks")
	if err := os.MkdirAll(locksDir, 0o700); err != nil {
		return "", fmt.Errorf("create agent run locks directory: %w", err)
	}
	info, err := os.Lstat(locksDir)
	if err != nil {
		return "", fmt.Errorf("check agent run locks directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("refusing symlinked agent run locks directory: %s", locksDir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("agent run locks path is not a directory: %s", locksDir)
	}
	return filepath.Join(locksDir, filepath.Base(runDir)+".lock"), nil
}

func openLockFile(lockPath string) (*os.File, error) {
	if info, err := os.Lstat(lockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing symlinked agent run lock: %s", lockPath)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("agent run lock is not a regular file: %s", lockPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("check agent run lock: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	pathInfo, err := os.Lstat(lockPath)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !os.SameFile(fileInfo, pathInfo) {
		_ = file.Close()
		return nil, fmt.Errorf("refusing symlinked agent run lock: %s", lockPath)
	}
	return file, nil
}

func writeMetadataFile(workspace string, data []byte) error {
	metadataPath := filepath.Join(workspace, "codemesh-run.json")
	if info, err := os.Lstat(metadataPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to replace symlinked metadata file: %s", metadataPath)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular metadata file: %s", metadataPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("check metadata file: %w", err)
	}
	tmp, err := os.CreateTemp(workspace, ".codemesh-run-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, metadataPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func safeOutputLabel(label string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	clean := strings.Trim(builder.String(), "-")
	if clean == "" {
		clean = "command"
	}
	if len(clean) > 48 {
		clean = strings.TrimRight(clean[:48], "-")
	}
	return clean
}

func outputFileStem(ordinal int, label string) string {
	return fmt.Sprintf("%03d-%s-%s-%s", ordinal, safeOutputLabel(label), time.Now().UTC().Format("20060102T150405.000000000Z"), randomHex(4))
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	const digits = "0123456789abcdef"
	out := make([]byte, len(buf)*2)
	for i, b := range buf {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&0x0f]
	}
	return string(out)
}

func summarizeEnv(bindings []string) EnvSummary {
	keys := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		key := strings.TrimSpace(binding)
		if idx := strings.Index(key, "="); idx >= 0 {
			key = key[:idx]
		}
		if key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	mode := "process-inherited"
	if len(keys) != 0 {
		mode = "process-inherited+bindings"
	}
	return EnvSummary{
		Mode:   mode,
		Keys:   keys,
		Values: "not-recorded",
	}
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
		State:         runState(metadata),
		CreatedAt:     created,
		WorkspacePath: row.WorkspacePath,
	}, nil
}

func runState(metadata runMetadata) string {
	if len(metadata.Commands) != 0 {
		return "executed"
	}
	return "prepared"
}

func (m Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}
