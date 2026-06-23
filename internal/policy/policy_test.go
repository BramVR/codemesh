package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAbsentPolicyUsesDefaults(t *testing.T) {
	root := t.TempDir()

	got, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}

	if got.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", got.BaseBranch)
	}
	if got.Env.Mode != EnvModeWarn {
		t.Fatalf("Env.Mode = %q, want %q", got.Env.Mode, EnvModeWarn)
	}
	if len(got.Env.RequiredFiles) != 0 || len(got.Env.RequiredKeys) != 0 {
		t.Fatalf("env requirements = files %v keys %v, want none", got.Env.RequiredFiles, got.Env.RequiredKeys)
	}
}

func TestResolveValidPolicy(t *testing.T) {
	root := t.TempDir()
	writePolicy(t, root, `agent:
  base: release/main
  env:
    mode: block
    required_files:
      - .env.local
    required_keys:
      - CODEMESH_TEST_REQUIRED
  include_docs:
    - AGENTS.md
    - docs/adr/**
`)

	got, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}

	if got.BaseBranch != "release/main" {
		t.Fatalf("BaseBranch = %q, want release/main", got.BaseBranch)
	}
	if got.Env.Mode != EnvModeBlock {
		t.Fatalf("Env.Mode = %q, want %q", got.Env.Mode, EnvModeBlock)
	}
	if strings.Join(got.Env.RequiredFiles, ",") != ".env.local" {
		t.Fatalf("RequiredFiles = %v", got.Env.RequiredFiles)
	}
	if strings.Join(got.Env.RequiredKeys, ",") != "CODEMESH_TEST_REQUIRED" {
		t.Fatalf("RequiredKeys = %v", got.Env.RequiredKeys)
	}
	if strings.Join(got.IncludeDocs, ",") != "AGENTS.md,docs/adr/**" {
		t.Fatalf("IncludeDocs = %v", got.IncludeDocs)
	}
}

func TestResolveInvalidPolicyReturnsActionableError(t *testing.T) {
	root := t.TempDir()
	writePolicy(t, root, `agent:
  env:
    mode: stop
`)

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("Resolve error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), ".codemesh.yml") || !strings.Contains(err.Error(), "agent.env.mode") || !strings.Contains(err.Error(), "warn") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestResolveInvalidBaseReturnsActionableError(t *testing.T) {
	root := t.TempDir()
	writePolicy(t, root, `agent:
  base: main*
`)

	_, err := Resolve(root)
	if err == nil {
		t.Fatal("Resolve error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), ".codemesh.yml") || !strings.Contains(err.Error(), "agent.base") || !strings.Contains(err.Error(), "main*") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func writePolicy(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".codemesh.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
