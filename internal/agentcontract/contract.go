package agentcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BramVR/codemesh/internal/gitops"
)

const (
	FileName        = "codemesh-run.json"
	ContractVersion = 1
	ProducerName    = "codemesh"
)

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Input struct {
	Producer          Producer
	RunID             string
	ReadyPath         string
	Project           ProjectInput
	Base              string
	Profile           string
	ResolvedCommit    string
	BaseProvenance    BaseProvenance
	ReadinessDecision string
	HandoffDocs       []HandoffDoc
	Diagnostics       Diagnostics
	CreatedAt         time.Time
}

type ProjectInput struct {
	Alias      string
	Remote     string
	CloneURL   string
	SourcePath string
	LocalPath  string
	ProjectID  int64
}

type Contract struct {
	ContractVersion   int             `json:"contract_version"`
	Producer          Producer        `json:"producer"`
	RunID             string          `json:"run_id"`
	ReadyPath         string          `json:"ready_path"`
	Project           ProjectInfo     `json:"project"`
	Base              string          `json:"base"`
	Profile           string          `json:"profile"`
	ResolvedCommit    string          `json:"resolved_commit"`
	BaseProvenance    BaseProvenance  `json:"base_provenance"`
	ReadinessDecision string          `json:"readiness_decision"`
	HandoffDocs       []HandoffDoc    `json:"handoff_docs"`
	Diagnostics       Diagnostics     `json:"diagnostics"`
	CreatedAt         string          `json:"created_at"`
	Commands          []CommandRecord `json:"commands,omitempty"`
}

type ProjectInfo struct {
	Alias      string `json:"alias"`
	Remote     string `json:"remote"`
	CloneURL   string `json:"clone_url"`
	SourcePath string `json:"source_path"`
	LocalPath  string `json:"local_path"`
	ProjectID  int64  `json:"project_id,omitempty"`
}

type HandoffDoc struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Pattern string `json:"pattern,omitempty"`
}

type Diagnostics struct {
	Warnings []Diagnostic `json:"warnings"`
	Blockers []Diagnostic `json:"blockers"`
}

type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
	FetchedBase    string `json:"fetched_base"`
	FetchedCommit  string `json:"fetched_commit"`
	PreparedHEAD   string `json:"prepared_head"`
	MatchesFetched bool   `json:"matches_fetched"`
}

type ListProjection struct {
	ProjectAlias  string
	Base          string
	Profile       string
	State         string
	CreatedAt     time.Time
	WorkspacePath string
}

func DefaultProducer(version string) Producer {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "unknown"
	}
	return Producer{Name: ProducerName, Version: version}
}

func New(input Input) Contract {
	base := strings.TrimSpace(input.Base)
	resolvedCommit := strings.TrimSpace(input.ResolvedCommit)
	remote := input.Project.Remote
	baseProvenance := normalizeBaseProvenance(input.BaseProvenance, base, resolvedCommit, remote)
	return Contract{
		ContractVersion: normalizeVersion(ContractVersion),
		Producer:        normalizeProducer(input.Producer),
		RunID:           strings.TrimSpace(input.RunID),
		ReadyPath:       input.ReadyPath,
		Project: ProjectInfo{
			Alias:      input.Project.Alias,
			Remote:     input.Project.Remote,
			CloneURL:   RedactCloneURL(input.Project.CloneURL),
			SourcePath: input.Project.SourcePath,
			LocalPath:  input.Project.LocalPath,
			ProjectID:  input.Project.ProjectID,
		},
		Base:              base,
		Profile:           strings.TrimSpace(input.Profile),
		ResolvedCommit:    resolvedCommit,
		BaseProvenance:    baseProvenance,
		ReadinessDecision: strings.TrimSpace(input.ReadinessDecision),
		HandoffDocs:       append([]HandoffDoc(nil), input.HandoffDocs...),
		Diagnostics: Diagnostics{
			Warnings: append([]Diagnostic(nil), input.Diagnostics.Warnings...),
			Blockers: append([]Diagnostic(nil), input.Diagnostics.Blockers...),
		},
		CreatedAt: input.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func RedactCloneURL(raw string) string {
	return gitops.RedactURLForMetadata(raw)
}

func EnvSummaryFromBindings(bindings []string) EnvSummary {
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

func Encode(contract Contract) ([]byte, error) {
	contract = normalizeContract(contract)
	if err := Validate(contract); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode agent run contract: %w", err)
	}
	return append(data, '\n'), nil
}

func Decode(data []byte) (Contract, error) {
	var contract Contract
	if err := json.Unmarshal(data, &contract); err != nil {
		return Contract{}, fmt.Errorf("decode agent run contract: %w", err)
	}
	if contract.ContractVersion == 0 {
		contract.ContractVersion = ContractVersion
	}
	if contract.ContractVersion != ContractVersion {
		return Contract{}, fmt.Errorf("unsupported agent run contract version: %d", contract.ContractVersion)
	}
	return normalizeContract(contract), nil
}

func Validate(contract Contract) error {
	if contract.ContractVersion != ContractVersion {
		return fmt.Errorf("agent run contract version must be %d", ContractVersion)
	}
	if strings.TrimSpace(contract.Producer.Name) == "" || strings.TrimSpace(contract.Producer.Version) == "" {
		return errors.New("agent run contract producer name and version are required")
	}
	if strings.TrimSpace(contract.RunID) == "" {
		return errors.New("agent run contract run id is required")
	}
	if strings.TrimSpace(contract.ReadyPath) == "" {
		return errors.New("agent run contract ready path is required")
	}
	if strings.TrimSpace(contract.Project.Alias) == "" {
		return errors.New("agent run contract project alias is required")
	}
	if strings.TrimSpace(contract.Base) == "" {
		return errors.New("agent run contract base is required")
	}
	if strings.TrimSpace(contract.CreatedAt) == "" {
		return errors.New("agent run contract created_at is required")
	}
	if _, err := time.Parse(time.RFC3339, contract.CreatedAt); err != nil {
		return fmt.Errorf("agent run contract created_at: %w", err)
	}
	return nil
}

func WriteNew(workspace string, contract Contract) ([]byte, error) {
	data, err := Encode(contract)
	if err != nil {
		return nil, err
	}
	if err := writeNewFile(filepath.Join(workspace, FileName), data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}

func Write(workspace string, contract Contract) ([]byte, error) {
	data, err := Encode(contract)
	if err != nil {
		return nil, err
	}
	if err := writeFile(workspace, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c Contract) ListProjection(fallbackCreated time.Time, workspacePath string) (ListProjection, error) {
	created := fallbackCreated
	if c.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, c.CreatedAt)
		if err != nil {
			return ListProjection{}, fmt.Errorf("parse agent run contract created_at: %w", err)
		}
		created = parsed
	}
	state := "prepared"
	if len(c.Commands) != 0 {
		state = "executed"
	}
	return ListProjection{
		ProjectAlias:  c.Project.Alias,
		Base:          c.Base,
		Profile:       c.Profile,
		State:         state,
		CreatedAt:     created,
		WorkspacePath: workspacePath,
	}, nil
}

func normalizeContract(contract Contract) Contract {
	if contract.ContractVersion == 0 {
		contract.ContractVersion = ContractVersion
	}
	contract.Producer = normalizeProducer(contract.Producer)
	contract.Project.CloneURL = RedactCloneURL(contract.Project.CloneURL)
	contract.BaseProvenance = normalizeBaseProvenance(contract.BaseProvenance, contract.Base, contract.ResolvedCommit, contract.Project.Remote)
	if contract.HandoffDocs == nil {
		contract.HandoffDocs = []HandoffDoc{}
	}
	if contract.Diagnostics.Warnings == nil {
		contract.Diagnostics.Warnings = []Diagnostic{}
	}
	if contract.Diagnostics.Blockers == nil {
		contract.Diagnostics.Blockers = []Diagnostic{}
	}
	return contract
}

func normalizeBaseProvenance(provenance BaseProvenance, base, resolvedCommit, remote string) BaseProvenance {
	provenance.Base = strings.TrimSpace(firstNonEmpty(provenance.Base, base))
	provenance.ResolvedCommit = strings.TrimSpace(firstNonEmpty(provenance.ResolvedCommit, resolvedCommit))
	provenance.Remote = strings.TrimSpace(firstNonEmpty(provenance.Remote, remote))
	if provenance.FetchedBase == "" && provenance.FetchedCommit == "" && provenance.PreparedHEAD == "" {
		provenance.FetchedBase = provenance.Base
		provenance.FetchedCommit = provenance.ResolvedCommit
		provenance.PreparedHEAD = provenance.ResolvedCommit
	} else {
		provenance.FetchedBase = strings.TrimSpace(provenance.FetchedBase)
		provenance.FetchedCommit = strings.TrimSpace(provenance.FetchedCommit)
		provenance.PreparedHEAD = strings.TrimSpace(firstNonEmpty(provenance.PreparedHEAD, provenance.ResolvedCommit))
	}
	provenance.MatchesFetched = provenance.FetchedCommit != "" && provenance.PreparedHEAD != "" && provenance.FetchedCommit == provenance.PreparedHEAD
	return provenance
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func normalizeVersion(version int) int {
	if version == 0 {
		return ContractVersion
	}
	return version
}

func normalizeProducer(producer Producer) Producer {
	producer.Name = strings.TrimSpace(producer.Name)
	producer.Version = strings.TrimSpace(producer.Version)
	if producer.Name == "" {
		producer.Name = ProducerName
	}
	if producer.Version == "" {
		producer.Version = "unknown"
	}
	return producer
}

func writeNewFile(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func writeFile(workspace string, data []byte) error {
	metadataPath := filepath.Join(workspace, FileName)
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
