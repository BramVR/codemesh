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
	if got.BaseBranchSet {
		t.Fatalf("BaseBranchSet = true, want false for absent policy")
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
  toolchain:
    mode: block
    requirements:
      - go
      - mise
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
	if !got.BaseBranchSet {
		t.Fatalf("BaseBranchSet = false, want true")
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
	if got.Toolchain.Mode != EnvModeBlock {
		t.Fatalf("Toolchain.Mode = %q, want %q", got.Toolchain.Mode, EnvModeBlock)
	}
	if strings.Join(got.Toolchain.Requirements, ",") != "go,mise" {
		t.Fatalf("Toolchain.Requirements = %v", got.Toolchain.Requirements)
	}
	if strings.Join(got.IncludeDocs, ",") != "AGENTS.md,docs/adr/**" {
		t.Fatalf("IncludeDocs = %v", got.IncludeDocs)
	}
}

func TestResolveInvalidIncludeDocsPathReturnsActionableError(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{name: "absolute", entry: "/tmp/outside.md"},
		{name: "parent escape", entry: "../outside.md"},
		{name: "nested parent escape", entry: "docs/../../outside.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writePolicy(t, root, "agent:\n  include_docs:\n    - "+tt.entry+"\n")

			_, err := Resolve(root)
			if err == nil {
				t.Fatal("Resolve error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), ".codemesh.yml") ||
				!strings.Contains(err.Error(), "agent.include_docs") ||
				!strings.Contains(err.Error(), tt.entry) {
				t.Fatalf("error is not actionable: %v", err)
			}
		})
	}
}

func TestResolveInvalidToolchainRequirementReturnsActionableError(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{name: "path", entry: "./scripts/probe"},
		{name: "nested path", entry: "tools/probe"},
		{name: "command", entry: "go version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writePolicy(t, root, "agent:\n  toolchain:\n    requirements:\n      - "+tt.entry+"\n")

			_, err := Resolve(root)
			if err == nil {
				t.Fatal("Resolve error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), ".codemesh.yml") ||
				!strings.Contains(err.Error(), "agent.toolchain.requirements") ||
				!strings.Contains(err.Error(), "command or path") {
				t.Fatalf("error is not actionable: %v", err)
			}
		})
	}
}

func TestDocumentedPolicyExampleParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "project-policy.md"))
	if err != nil {
		t.Fatal(err)
	}
	example := extractMarkedPolicyExample(t, string(data))

	got, err := ParseBytes("docs/project-policy.md example", []byte(example))
	if err != nil {
		t.Fatalf("ParseBytes error = %v", err)
	}

	if got.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", got.BaseBranch)
	}
	if !got.BaseBranchSet {
		t.Fatalf("BaseBranchSet = false, want true for documented agent.base")
	}
	if got.Env.Mode != EnvModeBlock {
		t.Fatalf("Env.Mode = %q, want %q", got.Env.Mode, EnvModeBlock)
	}
	if strings.Join(got.Env.RequiredFiles, ",") != ".env.local,.env.agent" {
		t.Fatalf("RequiredFiles = %v", got.Env.RequiredFiles)
	}
	if strings.Join(got.Env.RequiredKeys, ",") != "CODEMESH_AGENT_TOKEN,CODEMESH_PROVIDER_PROFILE" {
		t.Fatalf("RequiredKeys = %v", got.Env.RequiredKeys)
	}
	if got.Toolchain.Mode != EnvModeWarn {
		t.Fatalf("Toolchain.Mode = %q, want %q", got.Toolchain.Mode, EnvModeWarn)
	}
	if strings.Join(got.Toolchain.Requirements, ",") != "go,mise" {
		t.Fatalf("Toolchain.Requirements = %v", got.Toolchain.Requirements)
	}
	if strings.Join(got.IncludeDocs, ",") != "AGENTS.md,CONTEXT.md,docs/adr/**" {
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

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

func extractMarkedPolicyExample(t *testing.T, doc string) string {
	t.Helper()
	const start = "<!-- codemesh-policy-example:start -->"
	const end = "<!-- codemesh-policy-example:end -->"
	startIndex := strings.Index(doc, start)
	endIndex := strings.Index(doc, end)
	if startIndex == -1 || endIndex == -1 || endIndex <= startIndex {
		t.Fatalf("documented policy example markers not found")
	}
	block := strings.TrimSpace(doc[startIndex+len(start) : endIndex])
	block = strings.TrimPrefix(block, "```yaml")
	block = strings.TrimPrefix(block, "```yml")
	block = strings.TrimSuffix(block, "```")
	return strings.TrimSpace(block) + "\n"
}

func writePolicy(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".codemesh.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
