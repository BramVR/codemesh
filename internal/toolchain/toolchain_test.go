package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeDetectorReportsPresentMissingAndUnknown(t *testing.T) {
	results, err := Check(context.Background(), []string{"go", "node", "mise"}, FakeDetector{
		Statuses: map[string]Status{
			"go":   StatusPresent,
			"node": StatusMissing,
		},
	})
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if got := statusByName(results, "go"); got != StatusPresent {
		t.Fatalf("go status = %q, want %q", got, StatusPresent)
	}
	if got := statusByName(results, "node"); got != StatusMissing {
		t.Fatalf("node status = %q, want %q", got, StatusMissing)
	}
	if got := statusByName(results, "mise"); got != StatusUnknown {
		t.Fatalf("mise status = %q, want %q", got, StatusUnknown)
	}
}

func TestCheckNormalizesRequirementNames(t *testing.T) {
	results, err := Check(context.Background(), []string{" go ", "", "go"}, FakeDetector{
		Statuses: map[string]Status{"go": StatusPresent},
	})
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Name != "go" || results[0].Status != StatusPresent {
		t.Fatalf("results = %#v, want one present go requirement", results)
	}
}

func TestFakeDetectorRejectsUnsupportedStatus(t *testing.T) {
	_, err := Check(context.Background(), []string{"go"}, FakeDetector{
		Statuses: map[string]Status{"go": "installed"},
	})
	if err == nil {
		t.Fatal("Check error = nil, want unsupported status error")
	}
	if !strings.Contains(err.Error(), "go") || !strings.Contains(err.Error(), "installed") {
		t.Fatalf("error is not actionable: %v", err)
	}
}

func TestHostDetectorRecordsPresentCommandAndVersion(t *testing.T) {
	detector := HostDetector{
		Lookup: func(command string) (string, error) {
			if command != "go" {
				t.Fatalf("lookup command = %q, want go", command)
			}
			return "/host/bin/go", nil
		},
		Version: func(ctx context.Context, command string) (string, error) {
			if command != "go" {
				t.Fatalf("version command = %q, want go", command)
			}
			return "go version go1.26.0 host/arch\n", nil
		},
	}

	results, err := Check(context.Background(), []string{"go"}, detector)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("results = %#v, want one result", results)
	}
	got := results[0]
	if got.Name != "go" || got.Project.Requirement != "go" || got.Status != StatusPresent {
		t.Fatalf("project result = %#v, want present go requirement", got)
	}
	if got.Host.Command != "go" || got.Host.Version != "go version go1.26.0 host/arch" {
		t.Fatalf("host facts = %#v, want command and trimmed version", got.Host)
	}
}

func TestHostDetectorReportsMissingWithoutRunningVersion(t *testing.T) {
	detector := HostDetector{
		Lookup: func(command string) (string, error) {
			return "", errors.New("not found")
		},
		Version: func(context.Context, string) (string, error) {
			t.Fatal("version should not run for a missing command")
			return "", nil
		},
	}

	results, err := Check(context.Background(), []string{"npm"}, detector)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Status != StatusMissing || results[0].Project.Requirement != "npm" {
		t.Fatalf("results = %#v, want missing npm project requirement", results)
	}
	if results[0].Host.Command != "npm" || results[0].Host.Version != "" {
		t.Fatalf("host facts = %#v, want command name without version", results[0].Host)
	}
}

func TestHostDetectorDoesNotExecutePathLikeRequirements(t *testing.T) {
	detector := HostDetector{
		Lookup: func(command string) (string, error) {
			t.Fatalf("lookup should not run for unsafe requirement %q", command)
			return "", nil
		},
		Version: func(context.Context, string) (string, error) {
			t.Fatal("version should not run for unsafe requirements")
			return "", nil
		},
	}

	results, err := Check(context.Background(), []string{"./scripts/probe"}, detector)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Status != StatusUnknown || results[0].Host.Command != "" || results[0].Host.Version != "" {
		t.Fatalf("results = %#v, want unknown without host execution facts", results)
	}
}

func TestHostDetectorDoesNotVersionCommandsFromDeniedDirectories(t *testing.T) {
	projectRoot := t.TempDir()
	projectTool := filepath.Join(projectRoot, "bin", "go")
	detector := HostDetector{
		DenyDirs: []string{projectRoot},
		Lookup: func(command string) (string, error) {
			if command != "go" {
				t.Fatalf("lookup command = %q, want go", command)
			}
			return projectTool, nil
		},
		Version: func(context.Context, string) (string, error) {
			t.Fatal("version should not run for commands resolved inside denied directories")
			return "", nil
		},
	}

	results, err := Check(context.Background(), []string{"go"}, detector)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Status != StatusUnknown || results[0].Host.Command != "go" || results[0].Host.Version != "" {
		t.Fatalf("results = %#v, want unknown go without version", results)
	}
}

func TestHostDetectorDeniesCommandsUnderSymlinkedProjectRoot(t *testing.T) {
	realProjectRoot := t.TempDir()
	linkParent := t.TempDir()
	linkedProjectRoot := filepath.Join(linkParent, "project-link")
	if err := os.Symlink(realProjectRoot, linkedProjectRoot); err != nil {
		t.Skipf("create symlink: %v", err)
	}
	projectTool := filepath.Join(realProjectRoot, "bin", "go")
	if err := os.MkdirAll(filepath.Dir(projectTool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectTool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	detector := HostDetector{
		DenyDirs: []string{linkedProjectRoot},
		Lookup: func(command string) (string, error) {
			if command != "go" {
				t.Fatalf("lookup command = %q, want go", command)
			}
			return projectTool, nil
		},
		Version: func(context.Context, string) (string, error) {
			t.Fatal("version should not run for commands under canonical denied directories")
			return "", nil
		},
	}

	results, err := Check(context.Background(), []string{"go"}, detector)
	if err != nil {
		t.Fatalf("Check error = %v", err)
	}

	if len(results) != 1 || results[0].Status != StatusUnknown || results[0].Host.Command != "go" || results[0].Host.Version != "" {
		t.Fatalf("results = %#v, want unknown go without version", results)
	}
}

func statusByName(results []Result, name string) Status {
	for _, result := range results {
		if result.Name == name {
			return result.Status
		}
	}
	return ""
}
