package workspacetarget

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/state"
)

func TestExportBuildsStableTargetSpecWithoutObservedStateOrValues(t *testing.T) {
	workspace := t.TempDir()
	projectPath := filepath.Join(workspace, "tools", "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".codemesh.yml"), []byte(`agent:
  env:
    mode: block
    required_files:
      - .env.local
    required_keys:
      - CODEMESH_ALPHA_TOKEN
      - CODEMESH_ALPHA_REGION
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "dirty-local-marker.txt"), []byte("local dirty marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := fakeStore{
		projects: []state.Project{{
			ID:               7,
			Alias:            "alpha",
			NormalizedRemote: "https://example.invalid/bram/alpha",
			CloneURL:         "https://user:raw-secret@example.invalid/bram/alpha.git?token=raw-secret#frag",
			LocalPath:        projectPath,
		}},
		machines: []state.Machine{{
			ID:            "machine-fake",
			Hostname:      "local-fake-host",
			OS:            "linux",
			Architecture:  "amd64",
			WorkspaceRoot: workspace,
			CreatedAt:     time.Unix(100, 0),
			UpdatedAt:     time.Unix(200, 0),
		}},
		bindings: map[int64][]state.EnvBinding{
			7: {
				{Requirement: "CODEMESH_ALPHA_TOKEN", Provider: "fake", SecretRef: "fake://alpha-token-ref", Scopes: []string{"codex", "dev"}},
				{Requirement: "CODEMESH_ALPHA_REGION", Provider: "fake", SecretRef: "fake://alpha-region-ref", Scopes: []string{"readonly"}},
			},
		},
	}

	spec, err := Export(context.Background(), store, Options{
		ProducerVersion: "test",
		TargetName:      "local-fake-target",
		TargetKind:      "agent",
		Scopes:          []string{"codex"},
	})
	if err != nil {
		t.Fatalf("Export error = %v", err)
	}

	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(data)
	for _, want := range []string{
		`"target_spec_version": 1`,
		`"name": "local-fake-target"`,
		`"kind": "agent"`,
		`"workspace_root": "` + workspace + `"`,
		`"hostname": "local-fake-host"`,
		`"identity": "https://example.invalid/bram/alpha"`,
		`"desired_path": "tools/alpha"`,
		`"url": "https://example.invalid/bram/alpha.git"`,
		`"mode": "block"`,
		`"CODEMESH_ALPHA_TOKEN"`,
		`"CODEMESH_ALPHA_REGION"`,
		`"secret_ref": "fake://alpha-token-ref"`,
		`"values": "not-recorded"`,
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("target spec missing %q:\n%s", want, raw)
		}
	}
	for _, forbidden := range []string{
		"raw-secret",
		"dirty-local-marker",
		"readiness",
		"agent_run",
		"stale",
		`"local_path"`,
		`"updated_at"`,
		`"fake://alpha-region-ref"`,
		"codemesh_fake_secret",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("target spec leaked %q:\n%s", forbidden, raw)
		}
	}
}

func TestExportRequiresRegisteredMachine(t *testing.T) {
	_, err := Export(context.Background(), fakeStore{}, Options{TargetName: "local-fake-target", Scopes: []string{"codex"}})
	if err == nil {
		t.Fatal("Export error = nil, want machine registration error")
	}
}

type fakeStore struct {
	projects []state.Project
	machines []state.Machine
	bindings map[int64][]state.EnvBinding
}

func (s fakeStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s fakeStore) ListMachines(context.Context) ([]state.Machine, error) {
	return append([]state.Machine(nil), s.machines...), nil
}

func (s fakeStore) ListEnvBindings(_ context.Context, projectID int64) ([]state.EnvBinding, error) {
	return append([]state.EnvBinding(nil), s.bindings[projectID]...), nil
}
