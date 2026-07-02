package agentcontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/toolchain"
)

func TestEncodeWritesVersionProducerAndRedactsMetadataBytes(t *testing.T) {
	workspace := t.TempDir()
	secret := "contract-secret-value"
	contract := New(Input{
		Producer:  Producer{Name: "codemesh", Version: "0.0.0-test"},
		RunID:     "run-test",
		ReadyPath: workspace,
		Project: ProjectInput{
			Alias:             "codemesh",
			Remote:            "https://example.invalid/org/repo",
			CloneURL:          "https://user:" + secret + "@example.invalid/org/repo.git?token=" + secret + "#frag",
			SourcePath:        "/tmp/source",
			LocalPath:         "/tmp/source",
			SourcePathMissing: true,
			ProjectID:         42,
		},
		Base:           "main",
		Profile:        "codex",
		ResolvedCommit: "abc123",
		BaseProvenance: BaseProvenance{
			Base:           "main",
			ResolvedCommit: "abc123",
			Remote:         "https://example.invalid/org/repo",
			FetchedBase:    "main",
			FetchedCommit:  "abc123",
			PreparedHEAD:   "abc123",
			MatchesFetched: true,
		},
		Env: EnvInfo{
			Requirements:          []EnvRequirement{{Name: "CODEMESH_TOKEN", Kind: "env_key"}},
			AllowedScopes:         []string{"codex"},
			MaterializationStatus: "materialized",
			Bundle: EnvBundle{
				Present: true,
				Path:    filepath.Join(filepath.Dir(workspace), "env", "env.bundle"),
				Format:  "dotenv",
				Values:  "not-recorded",
			},
		},
		Toolchain:         []toolchain.Result{{Name: "go", Status: toolchain.StatusPresent}},
		ReadinessDecision: "ready",
		CreatedAt:         time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	})
	contract.Commands = append(contract.Commands, CommandRecord{
		Label: "check",
		CWD:   workspace,
		Env:   EnvSummaryFromBindings([]string{"CODEMESH_TOKEN=" + secret}),
		Base: BaseProvenance{
			Base:           "main",
			ResolvedCommit: "abc123",
			Remote:         "https://example.invalid/org/repo",
			FetchedBase:    "main",
			FetchedCommit:  "abc123",
			PreparedHEAD:   "abc123",
			MatchesFetched: true,
		},
		ExitCode:   0,
		Duration:   "1ms",
		StdoutPath: filepath.Join(workspace, "stdout.txt"),
		StderrPath: filepath.Join(workspace, "stderr.txt"),
		ExecutedAt: "2026-06-23T12:00:01Z",
	})

	stateBytes, err := Encode(contract)
	if err != nil {
		t.Fatalf("Encode error = %v", err)
	}
	fileBytes, err := WriteNew(workspace, contract)
	if err != nil {
		t.Fatalf("WriteNew error = %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(workspace, FileName))
	if err != nil {
		t.Fatalf("read contract file: %v", err)
	}
	for label, data := range map[string][]byte{
		"state": stateBytes,
		"file":  fileBytes,
		"disk":  onDisk,
	} {
		raw := string(data)
		if strings.Contains(raw, secret) || strings.Contains(raw, "token") || strings.Contains(raw, "frag") {
			t.Fatalf("%s metadata leaked secret-bearing data:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"contract_version": 1`) {
			t.Fatalf("%s metadata missing contract version:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"producer": {`) || !strings.Contains(raw, `"version": "0.0.0-test"`) {
			t.Fatalf("%s metadata missing producer/version:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"clone_url": "https://redacted@example.invalid/org/repo.git"`) {
			t.Fatalf("%s metadata missing redacted clone URL:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"clone_strategy": {`) || !strings.Contains(raw, `"name": "full-clone"`) || !strings.Contains(raw, `"history": "full"`) || !strings.Contains(raw, `"working_tree": "complete"`) {
			t.Fatalf("%s metadata missing full clone strategy:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"source_path_missing": true`) {
			t.Fatalf("%s metadata missing source checkout absence flag:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"base_provenance": {`) || !strings.Contains(raw, `"fetched_commit": "abc123"`) || !strings.Contains(raw, `"matches_fetched": true`) {
			t.Fatalf("%s metadata missing checkout provenance:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"CODEMESH_TOKEN"`) || !strings.Contains(raw, `"values": "not-recorded"`) {
			t.Fatalf("%s metadata missing env summary:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"materialization_status": "materialized"`) || !strings.Contains(raw, `"allowed_scopes": [`) || !strings.Contains(raw, `"present": true`) {
			t.Fatalf("%s metadata missing env materialization summary:\n%s", label, raw)
		}
		if !strings.Contains(raw, `"toolchain": [`) || !strings.Contains(raw, `"name": "go"`) || !strings.Contains(raw, `"status": "present"`) {
			t.Fatalf("%s metadata missing toolchain readiness:\n%s", label, raw)
		}
	}
	if string(fileBytes) != string(stateBytes) || string(onDisk) != string(stateBytes) {
		t.Fatal("contract file bytes and state metadata bytes diverged")
	}
}

func TestEncodeRedactsCloneURLEvenWhenContractBypassesConstructor(t *testing.T) {
	secret := "direct-contract-secret"
	contract := Contract{
		ContractVersion: ContractVersion,
		Producer:        Producer{Name: "codemesh", Version: "0.0.0-test"},
		RunID:           "run-direct",
		ReadyPath:       "/tmp/codemesh/agents/run-direct/workspace",
		Project: ProjectInfo{
			Alias:    "codemesh",
			Remote:   "https://example.invalid/org/repo",
			CloneURL: "https://user:" + secret + "@example.invalid/org/repo.git?token=" + secret + "#frag",
		},
		Base:      "main",
		CreatedAt: "2026-06-23T12:00:00Z",
	}

	data, err := Encode(contract)
	if err != nil {
		t.Fatalf("Encode error = %v", err)
	}
	raw := string(data)
	if strings.Contains(raw, secret) || strings.Contains(raw, "token") || strings.Contains(raw, "frag") {
		t.Fatalf("encoded contract leaked credential-bearing clone URL:\n%s", raw)
	}
	if !strings.Contains(raw, `"clone_url": "https://redacted@example.invalid/org/repo.git"`) {
		t.Fatalf("encoded contract missing redacted clone URL:\n%s", raw)
	}
}

func TestEncodePreservesEmptyContractListsAsArrays(t *testing.T) {
	contract := New(Input{
		Producer:          Producer{Name: "codemesh", Version: "0.0.0-test"},
		RunID:             "run-empty-lists",
		ReadyPath:         "/tmp/codemesh/agents/run-empty-lists/workspace",
		Project:           ProjectInput{Alias: "codemesh", Remote: "https://example.invalid/org/repo", CloneURL: "https://example.invalid/org/repo"},
		Base:              "main",
		ReadinessDecision: "ready",
		CreatedAt:         time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	})

	data, err := Encode(contract)
	if err != nil {
		t.Fatalf("Encode error = %v", err)
	}
	raw := string(data)
	for _, want := range []string{`"requirements": []`, `"allowed_scopes": []`, `"toolchain": []`, `"handoff_docs": []`, `"warnings": []`, `"blockers": []`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("encoded contract missing %s:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, `"requirements": null`) || strings.Contains(raw, `"allowed_scopes": null`) || strings.Contains(raw, `"toolchain": null`) || strings.Contains(raw, `"handoff_docs": null`) || strings.Contains(raw, `"warnings": null`) || strings.Contains(raw, `"blockers": null`) {
		t.Fatalf("encoded contract used null list fields:\n%s", raw)
	}
}

func TestListProjectionDerivesLifecycleFromContractCommands(t *testing.T) {
	contract := New(Input{
		Producer:          Producer{Name: "codemesh", Version: "0.0.0-test"},
		RunID:             "run-test",
		ReadyPath:         "/tmp/codemesh/agents/run-test/workspace",
		Project:           ProjectInput{Alias: "codemesh", Remote: "https://example.invalid/org/repo", CloneURL: "https://example.invalid/org/repo"},
		Base:              "main",
		Profile:           "codex",
		ReadinessDecision: "ready",
		CreatedAt:         time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC),
	})
	prepared, err := contract.ListProjection(time.Time{}, contract.ReadyPath)
	if err != nil {
		t.Fatalf("prepared ListProjection error = %v", err)
	}
	if prepared.ProjectAlias != "codemesh" || prepared.Base != "main" || prepared.Profile != "codex" || prepared.State != "prepared" || prepared.WorkspacePath != contract.ReadyPath {
		t.Fatalf("prepared projection = %#v", prepared)
	}

	contract.Commands = append(contract.Commands, CommandRecord{Label: "test"})
	executed, err := contract.ListProjection(time.Time{}, contract.ReadyPath)
	if err != nil {
		t.Fatalf("executed ListProjection error = %v", err)
	}
	if executed.State != "executed" {
		t.Fatalf("executed projection state = %q, want executed", executed.State)
	}
}

func TestDecodeAcceptsLegacyMetadataForLocalRunListing(t *testing.T) {
	contract, err := Decode([]byte(`{
  "run_id": "run-legacy",
  "ready_path": "/tmp/codemesh/agents/run-legacy/workspace",
  "project": {"alias": "legacy"},
  "base": "main",
  "created_at": "2026-06-23T12:00:00Z"
}`))
	if err != nil {
		t.Fatalf("Decode legacy error = %v", err)
	}
	if contract.ContractVersion != ContractVersion || contract.Producer.Name != ProducerName || contract.Producer.Version == "" {
		t.Fatalf("legacy contract defaults = version %d producer %#v", contract.ContractVersion, contract.Producer)
	}
	projection, err := contract.ListProjection(time.Time{}, contract.ReadyPath)
	if err != nil {
		t.Fatalf("legacy ListProjection error = %v", err)
	}
	if projection.ProjectAlias != "legacy" || projection.State != "prepared" {
		t.Fatalf("legacy projection = %#v", projection)
	}
}
