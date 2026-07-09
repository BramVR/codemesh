package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProofBundleRejectsMissingRequiredVisual(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "proof-manifest.json"), []byte(`{
		"schema_version": 1,
		"kind": "codemesh-crabbox-pr-proof",
		"status": "pass",
		"runner": "github-hosted-free",
		"fixture": "isolated-local",
		"source": "real-codemesh-cli",
		"coverage": ["canonical-workspace-tree", "machine-placement-presence", "bootstrap-hydration-plan", "source-less-agent-prep", "placeholder-workspace-structure", "access-triggered-hydration", "mutating-flow-before-after"],
		"commands": [{"name": "codemesh tree", "exit_code": 0}],
		"artifacts": [
			{"name": "canonical-workspace-tree.svg", "path": "canonical-workspace-tree.svg", "kind": "visual", "required": true}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateProofBundle(root)
	if err == nil {
		t.Fatal("validateProofBundle passed with a missing required SVG")
	}
	if !strings.Contains(err.Error(), "required artifact missing") {
		t.Fatalf("error = %v, want required artifact missing", err)
	}
}

func TestValidateProofBundleRejectsFakeOnlyProof(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "proof-manifest.json"), []byte(`{
		"schema_version": 1,
		"kind": "codemesh-crabbox-pr-proof",
		"status": "pass",
		"runner": "github-hosted-free",
		"fixture": "isolated-local",
		"source": "fake-only",
		"coverage": ["canonical-workspace-tree", "machine-placement-presence", "bootstrap-hydration-plan", "source-less-agent-prep", "placeholder-workspace-structure", "access-triggered-hydration", "mutating-flow-before-after"],
		"commands": [],
		"artifacts": []
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateProofBundle(root)
	if err == nil {
		t.Fatal("validateProofBundle passed with fake-only metadata")
	}
	if !strings.Contains(err.Error(), "real-codemesh-cli") {
		t.Fatalf("error = %v, want real CLI provenance failure", err)
	}
}

func TestPublicArtifactConfidentialityRejectsSensitiveStrings(t *testing.T) {
	root := t.TempDir()
	content := "workspace /Users/example/Projects/codemesh endpoint https://private-host.local employee-only model"
	if err := os.WriteFile(filepath.Join(root, "proof.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	err := auditPublicArtifacts(root)
	if err == nil {
		t.Fatal("auditPublicArtifacts passed sensitive public proof text")
	}
	for _, want := range []string{"personal local path", "private endpoint", "internal model name"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
}

func TestPublicArtifactConfidentialityAllowsIsolatedFixtureProof(t *testing.T) {
	root := t.TempDir()
	content := "workspace <FIXTURE_ROOT>/machine-b/workspace/projects/mesh-target token: [redacted]"
	if err := os.WriteFile(filepath.Join(root, "proof.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := auditPublicArtifacts(root); err != nil {
		t.Fatalf("auditPublicArtifacts rejected sanitized fixture proof: %v", err)
	}
}
