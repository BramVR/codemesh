package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/BramVR/codemesh/internal/envbinding"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

func TestHelpIdentifiesCodeMesh(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "CodeMesh") {
		t.Fatalf("help output did not identify CodeMesh:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unknown"}, &stdout, &stderr)

	if code == 0 {
		t.Fatal("exit code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "unknown command: unknown") {
		t.Fatalf("stderr did not explain the failure:\n%s", stderr.String())
	}
}

func TestVersionReportsReleaseVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := strings.TrimSpace(stdout.String()), "codemesh 0.1.0"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandCatalogMatchesTopLevelHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	helpCommands := commandUsageLines(stdout.String())
	catalogCommands := catalogCurrentCommands(t)
	if strings.Join(catalogCommands, "\n") != strings.Join(helpCommands, "\n") {
		t.Fatalf("docs command catalog drifted from CLI help\ncatalog:\n%s\nhelp:\n%s", strings.Join(catalogCommands, "\n"), strings.Join(helpCommands, "\n"))
	}
}

func TestCommandReferencePagesMatchCatalog(t *testing.T) {
	refs := catalogCommandReferences(t)
	if len(refs) == 0 {
		t.Fatal("docs/commands.md did not link command reference pages")
	}

	expectedPaths := map[string]string{}
	for _, ref := range refs {
		expectedPaths[ref.path] = ref.command
		rawBytes, err := os.ReadFile(filepath.Join("..", "..", "docs", ref.path))
		if err != nil {
			t.Fatalf("read command reference %s: %v", ref.path, err)
		}
		raw := string(rawBytes)
		for _, want := range []string{
			"# " + commandHeading(ref.command),
			"## Syntax",
			"```sh\n" + ref.command + "\n```",
			"## Purpose",
			"## Safe Example",
			"CODEMESH_HOME",
			"## Current Limitations",
			"[Command Catalog](../commands.md)",
		} {
			if !strings.Contains(raw, want) {
				t.Fatalf("%s missing %q", ref.path, want)
			}
		}
		for _, unsafe := range []string{"/Users/bram", "~/Projects", "~/.codemesh", "GITHUB_TOKEN", "GH_TOKEN", "TOKEN=", "git@github.com", "https://github.com"} {
			if strings.Contains(raw, unsafe) {
				t.Fatalf("%s contains unsafe public example text %q", ref.path, unsafe)
			}
		}
	}

	files, err := filepath.Glob(filepath.Join("..", "..", "docs", "commands", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(expectedPaths) {
		t.Fatalf("command reference page count = %d, want %d", len(files), len(expectedPaths))
	}
	for _, file := range files {
		rel, err := filepath.Rel(filepath.Join("..", "..", "docs"), file)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		if _, ok := expectedPaths[rel]; !ok {
			t.Fatalf("unlisted command reference page: %s", rel)
		}
	}
}

func TestInitCreatesLocalState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"init", workspace}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "codemesh.db")); err != nil {
		t.Fatalf("database missing: %v", err)
	}
	if !strings.Contains(stdout.String(), "initialized CodeMesh") {
		t.Fatalf("stdout missing init message:\n%s", stdout.String())
	}
}

func TestInitHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"init", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "codemesh init [workspace-root]") {
		t.Fatalf("init help missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestMachineRegisterCreatesStableIdentityAndUpdatesFacts(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	firstRoot := filepath.Join(t.TempDir(), "workspace-one")
	secondRoot := filepath.Join(t.TempDir(), "workspace-two")
	if err := os.MkdirAll(firstRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secondRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"machine", "register", firstRoot}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("machine register exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"machine registered", "id: ", "hostname: ", "os: ", "architecture: ", "workspace_root: " + firstRoot, "registered_at: ", "updated_at: "} {
		if !strings.Contains(output, want) {
			t.Fatalf("machine register output missing %q:\n%s", want, output)
		}
	}
	firstID := valueAfterPrefix(t, output, "id: ")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"machine", "register", secondRoot}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("machine register rerun exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if secondID := valueAfterPrefix(t, stdout.String(), "id: "); secondID != firstID {
		t.Fatalf("machine id changed on rerun: first %q second %q", firstID, secondID)
	}
	if !strings.Contains(stdout.String(), "workspace_root: "+secondRoot) {
		t.Fatalf("rerun did not update workspace root:\n%s", stdout.String())
	}
}

func TestMachineRegisterJSONOutput(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"machine", "register", workspace, "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("machine register --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	var payload struct {
		ID            string `json:"id"`
		Hostname      string `json:"hostname"`
		OS            string `json:"os"`
		Architecture  string `json:"architecture"`
		WorkspaceRoot string `json:"workspace_root"`
		RegisteredAt  string `json:"registered_at"`
		UpdatedAt     string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("machine register JSON did not parse: %v\n%s", err, stdout.String())
	}
	if payload.ID == "" || payload.Hostname == "" || payload.OS == "" || payload.Architecture == "" || payload.WorkspaceRoot != workspace || payload.RegisteredAt == "" || payload.UpdatedAt == "" {
		t.Fatalf("machine register JSON missing facts: %#v", payload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestMachineRegisterNameAndStatusExposePersistedPlacement(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"machine", "register", workspace, "--name", "Build Laptop"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("machine register --name exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	registerOutput := stdout.String()
	for _, want := range []string{"name: Build Laptop", "codemesh_home: " + home, "workspace_root: " + workspace} {
		if !strings.Contains(registerOutput, want) {
			t.Fatalf("machine register output missing %q:\n%s", want, registerOutput)
		}
	}
	firstID := valueAfterPrefix(t, registerOutput, "id: ")

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"machine", "status", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("machine status --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	var statusPayload struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Hostname      string `json:"hostname"`
		OS            string `json:"os"`
		Architecture  string `json:"architecture"`
		CodeMeshHome  string `json:"codemesh_home"`
		WorkspaceRoot string `json:"workspace_root"`
		RegisteredAt  string `json:"registered_at"`
		UpdatedAt     string `json:"updated_at"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &statusPayload); err != nil {
		t.Fatalf("machine status JSON did not parse: %v\n%s", err, stdout.String())
	}
	if statusPayload.ID != firstID || statusPayload.Name != "Build Laptop" || statusPayload.CodeMeshHome != home || statusPayload.WorkspaceRoot != workspace || statusPayload.RegisteredAt == "" || statusPayload.UpdatedAt == "" {
		t.Fatalf("machine status payload = %#v", statusPayload)
	}
	if statusPayload.Hostname == "" || statusPayload.OS == "" || statusPayload.Architecture == "" {
		t.Fatalf("machine status missing host facts: %#v", statusPayload)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestBootstrapPlansThenAppliesTopologyWithoutProjectDirectories(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntry(t, manifestDir, "alpha.json", "https://github.com/BramVR/alpha", "alpha", "tools/alpha")
	writeManifestEntry(t, manifestDir, "beta.json", "https://github.com/BramVR/beta", "beta", "beta")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"bootstrap", manifestDir, "--dry-run"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap plan exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap plan stderr = %q, want empty", stderr.String())
	}
	for _, want := range []string{"bootstrap plan", "workspace_root: " + workspace, "blocked: false", "missing: alpha " + filepath.Join(workspace, "tools", "alpha"), "missing: beta " + filepath.Join(workspace, "beta"), "clone: alpha " + filepath.Join(workspace, "tools", "alpha"), "clone: beta " + filepath.Join(workspace, "beta")} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("bootstrap plan output missing %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run bootstrap created workspace or stat failed unexpectedly: %v", err)
	}
	assertProjectRows(t, home, 0)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"bootstrap", manifestDir, "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap apply exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"bootstrap plan", "applied", "parent: " + workspace, "parent: " + filepath.Join(workspace, "tools"), "added: alpha " + filepath.Join(workspace, "tools", "alpha"), "added: beta " + filepath.Join(workspace, "beta")} {
		if !strings.Contains(output, want) {
			t.Fatalf("bootstrap apply output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "tools")); err != nil {
		t.Fatalf("bootstrap parent missing: %v", err)
	}
	for _, path := range []string{filepath.Join(workspace, "tools", "alpha"), filepath.Join(workspace, "beta")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("bootstrap created project path %s or stat failed unexpectedly: %v", path, err)
		}
	}
	assertProjectRows(t, home, 2)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"tree"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tree exit code = %d, want 0 for human readiness report\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "- alpha missing "+filepath.Join(workspace, "tools", "alpha")) || !strings.Contains(stdout.String(), "- beta missing "+filepath.Join(workspace, "beta")) {
		t.Fatalf("tree output missing bootstrapped missing projects:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status alpha exit code = %d, want 0 for JSON readiness report\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"state":"missing"`) || !strings.Contains(stdout.String(), `"path_present":false`) {
		t.Fatalf("status JSON missing missing-state payload:\n%s", stdout.String())
	}
}

func TestBootstrapPlaceholdersCreatesHonestWorkspaceStructure(t *testing.T) {
	tmp := t.TempDir()
	runGit(t, tmp, "init", "-b", "main")
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	alphaPath := filepath.Join(workspace, "tools", "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "placeholder-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntryWithCloneURL(t, manifestDir, "alpha.json", "https://example.invalid/bram/alpha", "alpha", "tools/alpha", remote)
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", manifestDir, "--placeholders"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap placeholders exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"bootstrap plan",
		"placeholders: true",
		"placeholder: alpha " + alphaPath,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("bootstrap placeholders output missing %q:\n%s", want, stdout.String())
		}
	}
	if _, err := os.Stat(filepath.Join(alphaPath, ".codemesh-placeholder.json")); err != nil {
		t.Fatalf("placeholder metadata missing: %v", err)
	}
	if note, err := os.ReadFile(filepath.Join(alphaPath, "CODEMESH_PLACEHOLDER.txt")); err != nil || !strings.Contains(string(note), "not a Git checkout") {
		t.Fatalf("placeholder note = %q err %v", note, err)
	}
	if barrier, err := os.ReadFile(filepath.Join(alphaPath, ".git")); err != nil || !strings.Contains(string(barrier), "not a Git checkout") {
		t.Fatalf("placeholder Git barrier = %q err %v", barrier, err)
	}
	gitStatus := exec.Command("git", "-C", alphaPath, "status", "--porcelain")
	if err := gitStatus.Run(); err == nil {
		t.Fatalf("git status succeeded inside placeholder; ordinary Git must not see a checkout")
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"tree", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tree --json exit code = %d, want 0 for readiness report\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	treeProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, treeProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "placeholder",
		WorkspaceState:       "placeholder",
		Path:                 alphaPath,
		PathPresent:          true,
		CanonicalPath:        alphaPath,
		CanonicalPathPresent: true,
		MachinePath:          alphaPath,
		MachinePathPresent:   true,
		Blockers:             1,
	})

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status alpha --json exit code = %d, want 0 for readiness report\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	statusProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, statusProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "placeholder",
		WorkspaceState:       "placeholder",
		Path:                 alphaPath,
		PathPresent:          true,
		CanonicalPath:        alphaPath,
		CanonicalPathPresent: true,
		MachinePath:          alphaPath,
		MachinePathPresent:   true,
		Blockers:             1,
	})

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hydrate alpha from placeholder exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "alpha", "hydrated", alphaPath, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, "README.md")); err != nil {
		t.Fatalf("hydrated checkout missing README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, ".codemesh-placeholder.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("placeholder metadata survived hydration or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, ".git")); err != nil {
		t.Fatalf("hydrated checkout missing .git: %v", err)
	}
}

func TestAccessLazilyHydratesPlaceholderAndReportsTransition(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	alphaPath := filepath.Join(workspace, "tools", "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "access-placeholder-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntryWithCloneURL(t, manifestDir, "alpha.json", "https://example.invalid/bram/alpha", "alpha", "tools/alpha", remote)
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	if code := run([]string{"bootstrap", manifestDir, "--placeholders"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("bootstrap placeholders exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"access", "alpha", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("access alpha --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("access stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Outcome     string `json:"outcome"`
			Trigger     string `json:"trigger"`
			Path        string `json:"path"`
			PathPresent bool   `json:"path_present"`
			Transition  struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"transition"`
			Hydrate struct {
				Outcome string `json:"outcome"`
			} `json:"hydrate"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("access stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "access" || payload.ExitClass != "success" {
		t.Fatalf("access command metadata = %#v", payload)
	}
	if payload.Payload.Project != "alpha" || payload.Payload.Outcome != "hydrated" || payload.Payload.Trigger != "command-access" {
		t.Fatalf("access payload = %#v", payload.Payload)
	}
	if payload.Payload.Path != alphaPath || !payload.Payload.PathPresent {
		t.Fatalf("access path payload = %#v", payload.Payload)
	}
	if payload.Payload.Transition.From != "placeholder" || payload.Payload.Transition.To != "hydrated" {
		t.Fatalf("access transition = %#v", payload.Payload.Transition)
	}
	if payload.Payload.Hydrate.Outcome != "hydrated" {
		t.Fatalf("access hydrate payload = %#v", payload.Payload.Hydrate)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, "README.md")); err != nil {
		t.Fatalf("access-hydrated checkout missing README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, ".codemesh-placeholder.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("placeholder metadata survived access hydration or stat failed unexpectedly: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"tree", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tree --json exit code = %d, want 0 after access hydration\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	treeProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, treeProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "present",
		WorkspaceState:       "hydrated",
		Path:                 alphaPath,
		PathPresent:          true,
		CanonicalPath:        alphaPath,
		CanonicalPathPresent: true,
		MachinePath:          alphaPath,
		MachinePathPresent:   true,
	})

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status alpha --json exit code = %d, want 0 after access hydration\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	statusProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, statusProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "present",
		WorkspaceState:       "hydrated",
		Path:                 alphaPath,
		PathPresent:          true,
		CanonicalPath:        alphaPath,
		CanonicalPathPresent: true,
		MachinePath:          alphaPath,
		MachinePathPresent:   true,
	})
}

func TestAccessLazilyHydratesMissingCanonicalProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	alphaPath := filepath.Join(workspace, "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "access-missing-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		CanonicalPath:    alphaPath,
		LocalPath:        alphaPath,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"access", "alpha", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("access missing alpha --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Payload struct {
			Outcome    string `json:"outcome"`
			Transition struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"transition"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("access missing stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Payload.Outcome != "hydrated" || payload.Payload.Transition.From != "missing" || payload.Payload.Transition.To != "hydrated" {
		t.Fatalf("access missing payload = %#v", payload.Payload)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, "README.md")); err != nil {
		t.Fatalf("access-hydrated missing checkout missing README: %v", err)
	}
}

func TestHydrateRefusesModifiedPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	alphaPath := filepath.Join(workspace, "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "modified-placeholder-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntryWithCloneURL(t, manifestDir, "alpha.json", "https://example.invalid/bram/alpha", "alpha", "alpha", remote)
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	if code := run([]string{"bootstrap", manifestDir, "--placeholders"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("bootstrap placeholders exit code = %d, want 0", code)
	}
	notePath := filepath.Join(alphaPath, "CODEMESH_PLACEHOLDER.txt")
	if err := os.WriteFile(notePath, []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate modified placeholder exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate modified placeholder stderr = %q, want empty", stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "readiness-blocked", "alpha", "path-conflict", alphaPath, true, []string{"path-conflict"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(notePath); err != nil || string(got) != "user edit\n" {
		t.Fatalf("edited placeholder note changed or missing: got %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(alphaPath, "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hydrate wrote checkout content despite modified placeholder: %v", err)
	}
}

func TestStatusTreatsCheckoutWithPlaceholderNamedFileAsHydrated(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "checkout-placeholder-name")
	if realRepo, err := filepath.EvalSymlinks(repo); err == nil {
		repo = realRepo
	}
	if err := os.WriteFile(filepath.Join(repo, ".codemesh-placeholder.json"), []byte("{not placeholder metadata}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"add", repo, "--alias", "checkout-placeholder-name"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"status", "checkout-placeholder-name", "--base", "main", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status checkout exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	projects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, projects["checkout-placeholder-name"], workspaceProjectWant{
		Alias:                "checkout-placeholder-name",
		State:                "dirty",
		WorkspaceState:       "hydrated",
		Path:                 repo,
		PathPresent:          true,
		CanonicalPath:        repo,
		CanonicalPathPresent: true,
		MachinePath:          repo,
		MachinePathPresent:   true,
	})
}

func TestHydrateReplacesWorkspaceRootPlaceholder(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	remoteSource := createCommittedLocalRemoteClone(t, "root-placeholder-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntryWithCloneURL(t, manifestDir, "alpha.json", "https://example.invalid/bram/alpha", "alpha", ".", remote)
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	if code := run([]string{"bootstrap", manifestDir, "--placeholders"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("bootstrap root placeholder exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("hydrate root placeholder exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "alpha", "hydrated", workspace, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "README.md")); err != nil {
		t.Fatalf("root placeholder hydrate missing README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".codemesh-placeholder.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root placeholder metadata survived hydration or stat failed unexpectedly: %v", err)
	}
}

func TestBootstrapJSONReportsPathConflictWithoutMutation(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	conflictPath := filepath.Join(workspace, "tools", "alpha")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(conflictPath, "local.txt")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntry(t, manifestDir, "alpha.json", "https://github.com/BramVR/alpha", "alpha", "tools/alpha")
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", manifestDir, "--apply", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap conflict exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap conflict stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Apply bool `json:"apply"`
			Plan  struct {
				Blocked  bool `json:"blocked"`
				Blockers []struct {
					Kind string `json:"kind"`
					Path string `json:"path"`
				} `json:"blockers"`
			} `json:"plan"`
			Applied struct {
				AddedProjects []state.Project `json:"added_projects"`
			} `json:"applied"`
		} `json:"payload"`
		Diagnostics struct {
			Blockers []struct {
				Code   string `json:"code"`
				Target string `json:"target"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("bootstrap conflict stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "bootstrap" || payload.ExitClass != "readiness-blocked" || !payload.Payload.Apply || !payload.Payload.Plan.Blocked {
		t.Fatalf("bootstrap JSON metadata = %#v", payload)
	}
	if len(payload.Payload.Plan.Blockers) != 1 || payload.Payload.Plan.Blockers[0].Kind != "path-conflict" || payload.Payload.Plan.Blockers[0].Path != conflictPath {
		t.Fatalf("bootstrap plan blockers = %#v", payload.Payload.Plan.Blockers)
	}
	if len(payload.Diagnostics.Blockers) != 1 || payload.Diagnostics.Blockers[0].Code != "path-conflict" || payload.Diagnostics.Blockers[0].Target != conflictPath {
		t.Fatalf("bootstrap diagnostics = %#v", payload.Diagnostics)
	}
	if len(payload.Payload.Applied.AddedProjects) != 0 {
		t.Fatalf("bootstrap applied despite conflict: %#v", payload.Payload.Applied)
	}
	assertProjectRows(t, home, 0)
	if got, err := os.ReadFile(marker); err != nil || string(got) != "keep\n" {
		t.Fatalf("conflict marker changed or missing: got %q err %v", got, err)
	}
}

func TestBootstrapJSONReportsCloneFailureAsInternalError(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "alpha")
	missingRemote := filepath.Join(tmp, "missing.git")
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         missingRemote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap clone failure exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap clone failure stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command     string `json:"command"`
		ExitClass   string `json:"exit_class"`
		Diagnostics struct {
			Blockers []struct {
				Code string `json:"code"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("bootstrap clone failure stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "bootstrap" || payload.ExitClass != "internal-error" {
		t.Fatalf("bootstrap clone failure metadata = %#v", payload)
	}
	if len(payload.Diagnostics.Blockers) != 1 || payload.Diagnostics.Blockers[0].Code != "bootstrap-failed" {
		t.Fatalf("bootstrap clone failure diagnostics = %#v", payload.Diagnostics)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bootstrap failed clone left target or stat failed unexpectedly: %v", err)
	}
}

func TestBootstrapDryRunJSONCarriesSharedHydrationPlan(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntry(t, manifestDir, "alpha.json", "https://github.com/BramVR/alpha", "alpha", "tools/alpha")
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", manifestDir, "--dry-run", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap dry-run exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			Apply         bool `json:"apply"`
			HydrationPlan struct {
				Actions []struct {
					Project string `json:"project"`
					Action  string `json:"action"`
					State   string `json:"state"`
					Path    string `json:"path"`
				} `json:"actions"`
			} `json:"hydration_plan"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("bootstrap dry-run stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "bootstrap" || payload.Payload.Apply || len(payload.Payload.HydrationPlan.Actions) != 1 {
		t.Fatalf("bootstrap dry-run payload = %#v", payload)
	}
	action := payload.Payload.HydrationPlan.Actions[0]
	if action.Project != "alpha" || action.Action != "clone" || action.State != "missing" || action.Path != filepath.Join(workspace, "tools", "alpha") {
		t.Fatalf("bootstrap hydration action = %#v", action)
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run bootstrap created workspace or stat failed unexpectedly: %v", err)
	}
	assertProjectRows(t, home, 0)
}

func TestBootstrapAllClonesRegisteredMissingProjects(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	alphaPath := filepath.Join(workspace, "tools", "alpha")
	betaPath := filepath.Join(workspace, "beta")
	alphaObserved := createCommittedLocalRemoteClone(t, "bootstrap-alpha")
	betaObserved := createCommittedLocalRemoteClone(t, "bootstrap-beta")
	alphaRemote := strings.TrimSpace(runGitOutput(t, alphaObserved, "remote", "get-url", "origin"))
	betaRemote := strings.TrimSpace(runGitOutput(t, betaObserved, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, project := range []state.Project{
		{Alias: "alpha", NormalizedRemote: "https://example.invalid/bram/alpha", CloneURL: alphaRemote, LocalPath: alphaPath, CanonicalPath: alphaPath, Source: "canonical"},
		{Alias: "beta", NormalizedRemote: "https://example.invalid/bram/beta", CloneURL: betaRemote, LocalPath: betaPath, CanonicalPath: betaPath, Source: "canonical"},
	} {
		if _, err := store.AddProject(context.Background(), project); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap --all exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"bootstrap plan", "apply: true", "clone: alpha " + alphaPath, "clone: beta " + betaPath, "cloned: alpha " + alphaPath, "cloned: beta " + betaPath} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("bootstrap --all output missing %q:\n%s", want, stdout.String())
		}
	}
	for _, path := range []string{alphaPath, betaPath} {
		if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
			t.Fatalf("bootstrap clone missing README at %s: %v", path, err)
		}
	}
	if got := strings.TrimSpace(runGitOutput(t, alphaPath, "remote", "get-url", "origin")); got != alphaRemote {
		t.Fatalf("alpha origin = %q, want clone URL %q", got, alphaRemote)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hydrate alpha after bootstrap exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "alpha", "already-present", alphaPath, true, nil); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapProjectJSONRefusesUnknownProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "ghost-project", "--apply", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap unknown exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap unknown stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command     string `json:"command"`
		ExitClass   string `json:"exit_class"`
		Diagnostics struct {
			Blockers []struct {
				Code   string `json:"code"`
				Target string `json:"target"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("bootstrap unknown stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.Command != "bootstrap" || payload.ExitClass != "readiness-blocked" {
		t.Fatalf("bootstrap unknown payload = %#v", payload)
	}
	if len(payload.Diagnostics.Blockers) != 1 || payload.Diagnostics.Blockers[0].Code != "unknown-project" || payload.Diagnostics.Blockers[0].Target != "ghost-project" {
		t.Fatalf("bootstrap unknown diagnostics = %#v", payload.Diagnostics)
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown bootstrap created workspace or stat failed unexpectedly: %v", err)
	}
}

func TestBootstrapUnknownProjectHumanDoesNotPrintApplied(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "ghost-project", "--apply"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap unknown exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap unknown stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "refusal: unknown-project ghost-project") {
		t.Fatalf("bootstrap unknown output missing refusal:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "blocked: true") {
		t.Fatalf("bootstrap unknown output did not report blocked plan:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "applied") {
		t.Fatalf("bootstrap unknown output claimed apply:\n%s", stdout.String())
	}
}

func TestBootstrapProjectAliasWinsOverExistingCwdPath(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "registered-docs")
	remoteSource := createCommittedLocalRemoteClone(t, "bootstrap-docs")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "docs",
		NormalizedRemote: "https://example.invalid/bram/docs",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "docs", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap docs exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "cloned: docs "+target) {
		t.Fatalf("bootstrap docs output missing cloned project:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("bootstrap docs did not clone registered alias: %v", err)
	}
}

func TestBootstrapProjectAliasEndingJSONWinsOverBareManifestHeuristic(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "docs-json")
	remoteSource := createCommittedLocalRemoteClone(t, "bootstrap-docs-json")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "docs.json",
		NormalizedRemote: "https://example.invalid/bram/docs-json",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "docs.json", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap docs.json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "cloned: docs.json "+target) {
		t.Fatalf("bootstrap docs.json output missing cloned project:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("bootstrap docs.json did not clone registered alias: %v", err)
	}
}

func TestBootstrapDuplicateProjectTargetClonesOnce(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "bootstrap-duplicate-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "alpha", "alpha", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap duplicate exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if strings.Count(stdout.String(), "cloned: alpha "+target) != 1 {
		t.Fatalf("bootstrap duplicate output =\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("bootstrap duplicate did not clone target: %v", err)
	}
}

func TestBootstrapAllPersistsCanonicalPlacementAfterClone(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	staleObserved := filepath.Join(tmp, "old", "alpha")
	target := filepath.Join(workspace, "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "bootstrap-placement-alpha")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		LocalPath:        staleObserved,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap placement exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	projects := listProjectsForTest(t, home)
	if len(projects) != 1 || projects[0].LocalPath != target || projects[0].CanonicalPath != target || projects[0].Source != "canonical" {
		t.Fatalf("project placement after bootstrap = %#v, want local_path persisted to canonical target", projects)
	}
}

func TestBootstrapAllRefusesPresentCheckoutWithWrongRemote(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "alpha")
	registeredRemote := createBareRemoteForTest(t, "registered-alpha")
	wrongRemote := createBareRemoteForTest(t, "wrong-alpha")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, filepath.Dir(target), "clone", wrongRemote, target)
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: filepath.Clean(registeredRemote),
		CloneURL:         registeredRemote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap wrong checkout exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("bootstrap wrong checkout stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		ExitClass   string `json:"exit_class"`
		Diagnostics struct {
			Blockers []struct {
				Code   string `json:"code"`
				Target string `json:"target"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("bootstrap wrong checkout stdout was not JSON: %v\n%s", err, stdout.String())
	}
	if payload.ExitClass != "readiness-blocked" || len(payload.Diagnostics.Blockers) != 1 || payload.Diagnostics.Blockers[0].Code != "path-conflict" || payload.Diagnostics.Blockers[0].Target != target {
		t.Fatalf("bootstrap wrong checkout payload = %#v", payload)
	}
}

func TestBootstrapAllRefusesSymlinkedParentOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	link := filepath.Join(workspace, "link")
	target := filepath.Join(link, "sub", "alpha")
	remote := createBareRemoteForTest(t, "symlink-alpha")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap symlink exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String()+stderr.String(), "outside workspace root") {
		t.Fatalf("bootstrap symlink output missing unsafe path\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bootstrap cloned through symlink or stat failed unexpectedly: %v", err)
	}
}

func TestBootstrapFailedCloneKeepsPreexistingEmptyDestination(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "alpha")
	missingRemote := filepath.Join(tmp, "missing.git")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         missingRemote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "--all", "--apply"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("bootstrap missing remote exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("bootstrap removed pre-existing target or stat failed: %v", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Fatalf("pre-existing target entries = %#v err=%v, want empty dir preserved", entries, err)
	}
}

func TestBootstrapBareExistingPathFallsBackToManifestWhenNoAliasMatches(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	target := filepath.Join(workspace, "tools", "alpha")
	remoteSource := createCommittedLocalRemoteClone(t, "bootstrap-bare-manifest")
	remote := strings.TrimSpace(runGitOutput(t, remoteSource, "remote", "get-url", "origin"))
	manifestDir := filepath.Join(tmp, "manifest")
	writeManifestEntryWithCloneURL(t, manifestDir, "alpha.json", "https://example.invalid/bram/alpha", "alpha", "tools/alpha", remote)
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"bootstrap", "manifest", "--apply"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("bootstrap manifest exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "cloned: alpha "+target) {
		t.Fatalf("bootstrap manifest output missing cloned project:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(target, "README.md")); err != nil {
		t.Fatalf("bootstrap manifest did not clone target: %v", err)
	}
	if got := strings.TrimSpace(runGitOutput(t, target, "remote", "get-url", "origin")); got != remote {
		t.Fatalf("manifest bootstrap origin = %q, want clone URL %q", got, remote)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("hydrate manifest project after bootstrap exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "alpha", "already-present", target, true, nil); err != nil {
		t.Fatal(err)
	}
}

func TestManifestExportImportMovesCanonicalWorkspaceBetweenMachines(t *testing.T) {
	tmp := t.TempDir()
	homeA := filepath.Join(tmp, "codemesh-a")
	homeB := filepath.Join(tmp, "codemesh-b")
	workspaceA := filepath.Join(tmp, "workspace-a")
	workspaceB := filepath.Join(tmp, "workspace-b")
	alphaA := createGitRepoAt(t, filepath.Join(workspaceA, "tools", "alpha"), "git@github.com:BramVR/alpha.git")
	betaA := createGitRepoAt(t, filepath.Join(workspaceA, "apps", "beta"), "https://github.com/BramVR/beta.git")
	manifestPath := filepath.Join(tmp, "workspace-manifest.json")
	manifestPath2 := filepath.Join(tmp, "workspace-manifest-2.json")
	t.Setenv("CODEMESH_HOME", homeA)

	if code := run([]string{"init", workspaceA}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine A init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspaceA}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine A register exit code = %d, want 0", code)
	}
	if code := run([]string{"add", alphaA, "--alias", "alpha"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine A add alpha exit code = %d, want 0", code)
	}
	if code := run([]string{"add", betaA, "--alias", "beta"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine A add beta exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"manifest", "export", "--output", manifestPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("manifest export exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("manifest export stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "manifest exported") || !strings.Contains(stdout.String(), "projects: 2") {
		t.Fatalf("manifest export output missing summary:\n%s", stdout.String())
	}
	firstExport, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"manifest", "export", "--output", manifestPath2}, &stdout, &stderr); code != 0 {
		t.Fatalf("second manifest export exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	secondExport, err := os.ReadFile(manifestPath2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstExport, secondExport) {
		t.Fatalf("manifest export was not deterministic\nfirst:\n%s\nsecond:\n%s", firstExport, secondExport)
	}
	rawManifest := string(firstExport)
	for _, want := range []string{`"manifest_version": 1`, `"alias": "beta"`, `"desired_path": "apps/beta"`, `"alias": "alpha"`, `"desired_path": "tools/alpha"`} {
		if !strings.Contains(rawManifest, want) {
			t.Fatalf("manifest missing %q:\n%s", want, rawManifest)
		}
	}
	for _, forbidden := range []string{workspaceA, homeA, "local_path", "readiness", "agent_run", "secret"} {
		if strings.Contains(rawManifest, forbidden) {
			t.Fatalf("manifest leaked %q:\n%s", forbidden, rawManifest)
		}
	}

	t.Setenv("CODEMESH_HOME", homeB)
	if code := run([]string{"init", workspaceB}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine B init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspaceB}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine B register exit code = %d, want 0", code)
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"manifest", "import", manifestPath}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("manifest import exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("manifest import stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "manifest imported") || !strings.Contains(stdout.String(), "added: beta "+filepath.Join(workspaceB, "apps", "beta")) || !strings.Contains(stdout.String(), "added: alpha "+filepath.Join(workspaceB, "tools", "alpha")) {
		t.Fatalf("manifest import output missing machine B placement:\n%s", stdout.String())
	}
	projects := listProjectsForTest(t, homeB)
	if len(projects) != 2 {
		t.Fatalf("machine B projects = %#v, want 2", projects)
	}
	byAlias := map[string]state.Project{}
	for _, project := range projects {
		byAlias[project.Alias] = project
	}
	if byAlias["alpha"].LocalPath != filepath.Join(workspaceB, "tools", "alpha") || byAlias["alpha"].NormalizedRemote != "https://github.com/BramVR/alpha" {
		t.Fatalf("machine B alpha = %#v", byAlias["alpha"])
	}
	if byAlias["beta"].LocalPath != filepath.Join(workspaceB, "apps", "beta") || byAlias["beta"].NormalizedRemote != "https://github.com/BramVR/beta" {
		t.Fatalf("machine B beta = %#v", byAlias["beta"])
	}
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"manifest", "import", manifestPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("second manifest import exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("second manifest import stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "added: none") || !strings.Contains(stdout.String(), "updated: none") ||
		!strings.Contains(stdout.String(), "unchanged: beta "+filepath.Join(workspaceB, "apps", "beta")) ||
		!strings.Contains(stdout.String(), "unchanged: alpha "+filepath.Join(workspaceB, "tools", "alpha")) {
		t.Fatalf("second manifest import output missing unchanged projects:\n%s", stdout.String())
	}
	if projects = listProjectsForTest(t, homeB); len(projects) != 2 {
		t.Fatalf("machine B projects after second import = %#v, want 2", projects)
	}
}

func TestManifestImportedWorkspaceTreeAndStatusShowCanonicalPlacementPresence(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	manifestPath := filepath.Join(tmp, "workspace-manifest.json")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	var err error
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "tools/alpha",
      "clone_hints": {},
      "groups": []
    },
    {
      "identity": "https://github.com/BramVR/beta",
      "alias": "beta",
      "desired_path": "apps/beta",
      "clone_hints": {},
      "groups": []
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	if code := run([]string{"manifest", "import", manifestPath}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("manifest import exit code = %d, want 0", code)
	}
	alphaMoved := createGitRepoAt(t, filepath.Join(workspace, "moved", "alpha"), "https://github.com/BramVR/alpha.git")
	betaDesired := createGitRepoAt(t, filepath.Join(workspace, "apps", "beta"), "https://github.com/BramVR/beta.git")
	localOnly := createGitRepoAt(t, filepath.Join(workspace, "scratch", "local-only"), "https://github.com/BramVR/local-only.git")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"scan", workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("scan exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	storedProjects := listProjectsForTest(t, home)
	storedByAlias := map[string]state.Project{}
	for _, project := range storedProjects {
		storedByAlias[project.Alias] = project
	}
	if alpha := storedByAlias["alpha"]; alpha.CloneURL != "https://github.com/BramVR/alpha" || alpha.CanonicalPath != filepath.Join(workspace, "tools", "alpha") || alpha.LocalPath != alphaMoved || alpha.Source != "canonical" {
		t.Fatalf("alpha placement after scan = %#v", alpha)
	}
	if local := storedByAlias["local-only"]; local.Source != "local-only" || local.CanonicalPath != local.LocalPath {
		t.Fatalf("local-only placement after scan = %#v", local)
	}
	updatedManifestPath := filepath.Join(tmp, "workspace-manifest-with-clone-hints.json")
	if err := os.WriteFile(updatedManifestPath, []byte(`{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "tools/alpha",
      "clone_hints": {
        "url": "https://github.com/BramVR/alpha.git"
      },
      "groups": []
    },
    {
      "identity": "https://github.com/BramVR/beta",
      "alias": "beta",
      "desired_path": "apps/beta",
      "clone_hints": {},
      "groups": []
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"manifest", "import", updatedManifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("second manifest import exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	storedProjects = listProjectsForTest(t, home)
	storedByAlias = map[string]state.Project{}
	for _, project := range storedProjects {
		storedByAlias[project.Alias] = project
	}
	if alpha := storedByAlias["alpha"]; alpha.CloneURL != "https://github.com/BramVR/alpha.git" || alpha.CanonicalPath != filepath.Join(workspace, "tools", "alpha") || alpha.LocalPath != alphaMoved || alpha.Source != "canonical" {
		t.Fatalf("alpha placement after manifest update = %#v", alpha)
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"tree", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("tree --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	treeProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, treeProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "present",
		CanonicalPath:        filepath.Join(workspace, "tools", "alpha"),
		CanonicalPathPresent: false,
		MachinePath:          alphaMoved,
		MachinePathPresent:   true,
	})
	assertWorkspaceProject(t, treeProjects["beta"], workspaceProjectWant{
		Alias:                "beta",
		WorkspaceSource:      "canonical",
		State:                "present",
		CanonicalPath:        betaDesired,
		CanonicalPathPresent: true,
		MachinePath:          betaDesired,
		MachinePathPresent:   true,
	})
	assertWorkspaceProject(t, treeProjects["local-only"], workspaceProjectWant{
		Alias:                "local-only",
		WorkspaceSource:      "local-only",
		State:                "present",
		CanonicalPath:        localOnly,
		CanonicalPathPresent: true,
		MachinePath:          localOnly,
		MachinePathPresent:   true,
	})

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status", "--base", "main", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	statusProjects := decodeWorkspaceProjects(t, stdout.Bytes())
	assertWorkspaceProject(t, statusProjects["alpha"], workspaceProjectWant{
		Alias:                "alpha",
		WorkspaceSource:      "canonical",
		State:                "stale",
		CanonicalPath:        filepath.Join(workspace, "tools", "alpha"),
		CanonicalPathPresent: false,
		MachinePath:          alphaMoved,
		MachinePathPresent:   true,
	})
	assertWorkspaceProject(t, statusProjects["local-only"], workspaceProjectWant{
		Alias:                "local-only",
		WorkspaceSource:      "local-only",
		State:                "stale",
		CanonicalPath:        localOnly,
		CanonicalPathPresent: true,
		MachinePath:          localOnly,
		MachinePathPresent:   true,
	})
}

func TestHydrateCanonicalProjectPersistsDesiredMachinePath(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	canonicalPath := filepath.Join(workspace, "tools", "canonical-hydrate")
	observedPath := createCommittedLocalRemoteClone(t, "canonical-hydrate")
	remote := strings.TrimSpace(runGitOutput(t, observedPath, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "canonical-hydrate",
		NormalizedRemote: "https://example.invalid/bram/canonical-hydrate",
		CloneURL:         remote,
		LocalPath:        observedPath,
		CanonicalPath:    canonicalPath,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(canonicalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canonical path exists before hydrate or stat failed unexpectedly: %v", err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "canonical-hydrate", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("hydrate canonical exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "canonical-hydrate", "hydrated", canonicalPath, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(canonicalPath, "README.md")); err != nil {
		t.Fatalf("hydrated canonical checkout missing README: %v", err)
	}
	projects := listProjectsForTest(t, home)
	if len(projects) != 1 || projects[0].LocalPath != canonicalPath || projects[0].CanonicalPath != canonicalPath || projects[0].Source != "canonical" {
		t.Fatalf("project placement after hydrate = %#v", projects)
	}
}

func TestHydrateRefusesSymlinkedParentOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	link := filepath.Join(workspace, "link")
	target := filepath.Join(link, "sub", "alpha")
	remote := createBareRemoteForTest(t, "hydrate-symlink-alpha")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         remote,
		LocalPath:        target,
		CanonicalPath:    target,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "alpha", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate symlink exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "readiness-blocked", "alpha", "unsafe-path", target, false, []string{"unsafe-path"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "outside workspace root") {
		t.Fatalf("hydrate symlink output missing unsafe path:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hydrate cloned through symlink or stat failed unexpectedly: %v", err)
	}
}

func TestManifestExportRejectsEmptyOutputPath(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"manifest", "export", "--output", ""}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("manifest export exit code = %d, want usage failure", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--output requires a path") {
		t.Fatalf("stderr missing output path error:\n%s", stderr.String())
	}
}

func TestManifestExportWritesJSONToStdoutWithoutSummary(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	projectPath := createGitRepoAt(t, filepath.Join(workspace, "alpha"), "https://github.com/BramVR/alpha.git")
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	if code := run([]string{"add", projectPath, "--alias", "alpha"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"manifest", "export"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("manifest export exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if strings.Contains(stdout.String(), "manifest exported") {
		t.Fatalf("stdout included human summary in JSON mode:\n%s", stdout.String())
	}
	manifest, err := workspacemanifest.DecodeWorkspace(stdout.Bytes())
	if err != nil {
		t.Fatalf("DecodeWorkspace(stdout) error = %v\nstdout:\n%s", err, stdout.String())
	}
	if len(manifest.Projects) != 1 || manifest.Projects[0].Alias != "alpha" {
		t.Fatalf("manifest = %#v, want alpha project", manifest)
	}
}

func TestManifestImportRejectsUnsafeManifestBeforePersisting(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	manifestPath := filepath.Join(tmp, "bad-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "alpha",
      "clone_hints": {
        "url": "https://user:leak-marker@example.invalid/org/alpha.git"
      },
      "groups": [],
      "secret_value": "leak-marker"
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"manifest", "import", manifestPath}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("manifest import exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr missing schema validation error:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "leak-marker") || strings.Contains(stdout.String(), "leak-marker") {
		t.Fatalf("import validation leaked secret marker\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	assertProjectRows(t, home, 0)
}

func TestManifestImportRejectsValidPathConflictBeforePersisting(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	conflictPath := filepath.Join(workspace, "alpha")
	if err := os.MkdirAll(conflictPath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(tmp, "conflicting-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "alpha",
      "clone_hints": {},
      "groups": []
    }
  ]
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"manifest", "import", manifestPath}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("manifest import exit code = 0, want conflict failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "exists outside the Project Registry") {
		t.Fatalf("stderr missing path conflict:\n%s", stderr.String())
	}
	assertProjectRows(t, home, 0)
}

func TestTargetExportJSONIncludesTopologyMachineAndScopedEnvRefs(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	projectPath := filepath.Join(workspace, "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_ALPHA_TOKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	if _, err := state.Initialize(context.Background(), home, workspace); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	machine, err := store.RegisterMachine(context.Background(), state.MachineFacts{
		Hostname:      "local-fake-host",
		OS:            "linux",
		Architecture:  "amd64",
		CodeMeshHome:  home,
		WorkspaceRoot: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.AddProject(context.Background(), state.Project{
		Alias:            "alpha",
		NormalizedRemote: "https://example.invalid/bram/alpha",
		CloneURL:         "https://example.invalid/bram/alpha.git",
		LocalPath:        projectPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertEnvBinding(context.Background(), state.EnvBinding{
		ProjectID:   project.ID,
		Requirement: "CODEMESH_ALPHA_TOKEN",
		Provider:    envbinding.ProviderFake,
		SecretRef:   "fake://alpha-token-ref",
		Scopes:      []string{"codex"},
	}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"target", "export", "local-fake-target", "--kind", "agent", "--scope", "codex", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("target export exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			TargetSpecVersion int `json:"target_spec_version"`
			Target            struct {
				Name   string   `json:"name"`
				Kind   string   `json:"kind"`
				Scopes []string `json:"scopes"`
			} `json:"target"`
			Machine struct {
				ID            string `json:"id"`
				Hostname      string `json:"hostname"`
				WorkspaceRoot string `json:"workspace_root"`
			} `json:"machine"`
			Topology []struct {
				Project struct {
					DesiredPath string `json:"desired_path"`
				} `json:"project"`
			} `json:"topology"`
			EnvPolicy []struct {
				Env struct {
					Bindings []struct {
						Requirement string `json:"requirement"`
						SecretRef   string `json:"secret_ref"`
						Values      string `json:"values"`
					} `json:"bindings"`
				} `json:"env"`
			} `json:"env_policy"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("target export JSON did not parse: %v\n%s", err, stdout.String())
	}
	if payload.Command != "target export" || payload.Payload.TargetSpecVersion != 1 || payload.Payload.Target.Name != "local-fake-target" || payload.Payload.Target.Kind != "agent" {
		t.Fatalf("target export metadata = %#v", payload)
	}
	if payload.Payload.Machine.ID != machine.ID || payload.Payload.Machine.Hostname != "local-fake-host" || payload.Payload.Machine.WorkspaceRoot != workspace {
		t.Fatalf("machine facts = %#v, want local fake machine", payload.Payload.Machine)
	}
	if len(payload.Payload.Topology) != 1 || payload.Payload.Topology[0].Project.DesiredPath != "alpha" {
		t.Fatalf("topology = %#v, want alpha desired path", payload.Payload.Topology)
	}
	if len(payload.Payload.EnvPolicy) != 1 || len(payload.Payload.EnvPolicy[0].Env.Bindings) != 1 {
		t.Fatalf("env policy = %#v, want one scoped binding", payload.Payload.EnvPolicy)
	}
	binding := payload.Payload.EnvPolicy[0].Env.Bindings[0]
	if binding.Requirement != "CODEMESH_ALPHA_TOKEN" || binding.SecretRef != "fake://alpha-token-ref" || binding.Values != "not-recorded" {
		t.Fatalf("binding = %#v, want ref metadata without values", binding)
	}
	for _, forbidden := range []string{"codemesh_fake_secret", "local_path", "agent_run", "readiness"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("target export leaked %q:\n%s", forbidden, stdout.String())
		}
	}
}

func TestAddHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"add", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "codemesh add <path>") {
		t.Fatalf("add help missing usage:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestAddThenTreeShowsPresentProject(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createGitRepo(t, "git@github.com:BramVR/codemesh.git")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"add", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("add exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "added project: codemesh") {
		t.Fatalf("add stdout missing alias:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "codemesh") || !strings.Contains(stdout.String(), "present") || !strings.Contains(stdout.String(), repo) {
		t.Fatalf("tree output missing project state/path:\n%s", stdout.String())
	}
}

func TestTreeJSONReportsCanonicalWorkspace(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	present := createCommittedLocalRemoteClone(t, "tree-present")
	missing := createCommittedLocalRemoteClone(t, "tree-missing")
	missing, err := filepath.EvalSymlinks(missing)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", present}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add present exit code = %d, want 0", code)
	}
	if code := run([]string{"add", missing}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add missing exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(missing); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"tree", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("tree --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("tree --json stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				Alias       string `json:"alias"`
				State       string `json:"state"`
				Path        string `json:"path"`
				PathPresent bool   `json:"path_present"`
				Remote      string `json:"remote"`
				Base        string `json:"base"`
				Diagnostics struct {
					Warnings []struct {
						Code string `json:"code"`
					} `json:"warnings"`
					Blockers []struct {
						Code string `json:"code"`
					} `json:"blockers"`
				} `json:"diagnostics"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("tree --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.Command != "tree" || payload.ExitClass != "readiness-blocked" {
		t.Fatalf("tree --json command metadata = %#v", payload)
	}
	if len(payload.Payload.Projects) != 2 {
		t.Fatalf("tree --json project count = %d, want 2", len(payload.Payload.Projects))
	}
	byAlias := map[string]struct {
		State       string
		Path        string
		PathPresent bool
		Remote      string
		Base        string
		Blockers    int
	}{}
	for _, project := range payload.Payload.Projects {
		byAlias[project.Alias] = struct {
			State       string
			Path        string
			PathPresent bool
			Remote      string
			Base        string
			Blockers    int
		}{project.State, project.Path, project.PathPresent, project.Remote, project.Base, len(project.Diagnostics.Blockers)}
	}
	if got := byAlias["tree-present"]; got.State != "present" || !got.PathPresent || got.Remote == "" || got.Base != "main" {
		t.Fatalf("present tree project = %#v", got)
	}
	if got := byAlias["tree-missing"]; got.State != "missing" || got.Path != missing || got.PathPresent || got.Blockers != 1 {
		t.Fatalf("missing tree project = %#v", got)
	}
}

type workspaceProjectPayload struct {
	Alias                string `json:"alias"`
	WorkspaceSource      string `json:"workspace_source"`
	State                string `json:"state"`
	WorkspaceState       string `json:"workspace_state"`
	Path                 string `json:"path"`
	PathPresent          bool   `json:"path_present"`
	CanonicalPath        string `json:"canonical_path"`
	CanonicalPathPresent bool   `json:"canonical_path_present"`
	MachinePath          string `json:"machine_path"`
	MachinePathPresent   bool   `json:"machine_path_present"`
	Remote               string `json:"remote"`
	Base                 string `json:"base"`
	Diagnostics          struct {
		Warnings []struct {
			Code string `json:"code"`
		} `json:"warnings"`
		Blockers []struct {
			Code string `json:"code"`
		} `json:"blockers"`
	} `json:"diagnostics"`
}

type workspaceProjectWant struct {
	Alias                string
	WorkspaceSource      string
	State                string
	WorkspaceState       string
	Path                 string
	PathPresent          bool
	CanonicalPath        string
	CanonicalPathPresent bool
	MachinePath          string
	MachinePathPresent   bool
	Blockers             int
}

func decodeWorkspaceProjects(t *testing.T, data []byte) map[string]workspaceProjectPayload {
	t.Helper()
	var payload struct {
		Payload struct {
			Projects []workspaceProjectPayload `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("workspace command stdout was not JSON: %v\nstdout:\n%s", err, data)
	}
	projects := make(map[string]workspaceProjectPayload, len(payload.Payload.Projects))
	for _, project := range payload.Payload.Projects {
		projects[project.Alias] = project
	}
	return projects
}

func assertWorkspaceProject(t *testing.T, got workspaceProjectPayload, want workspaceProjectWant) {
	t.Helper()
	if (want.Alias != "" && got.Alias != want.Alias) ||
		(want.WorkspaceSource != "" && got.WorkspaceSource != want.WorkspaceSource) ||
		got.State != want.State ||
		(want.WorkspaceState != "" && got.WorkspaceState != want.WorkspaceState) ||
		(want.Path != "" && (got.Path != want.Path || got.PathPresent != want.PathPresent)) ||
		got.CanonicalPath != want.CanonicalPath ||
		got.CanonicalPathPresent != want.CanonicalPathPresent ||
		got.MachinePath != want.MachinePath ||
		got.MachinePathPresent != want.MachinePathPresent ||
		(want.Blockers != 0 && len(got.Diagnostics.Blockers) != want.Blockers) {
		t.Fatalf("workspace project = %#v, want %#v", got, want)
	}
}

func TestScanThenTreeShowsDiscoveredProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	workspace := t.TempDir()
	alpha := createGitRepoAt(t, filepath.Join(workspace, "alpha"), "https://github.com/BramVR/alpha.git")
	nested := createGitRepoAt(t, filepath.Join(alpha, "vendor", "nested"), "https://github.com/BramVR/nested.git")
	createGitRepoAt(t, filepath.Join(workspace, "no-remote"), "")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"scan", workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("scan exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "scan complete") || !strings.Contains(stdout.String(), "added: alpha") {
		t.Fatalf("scan stdout missing added report:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "skipped: "+nested+" (nested Git repo)") || !strings.Contains(stdout.String(), "unsupported") {
		t.Fatalf("scan stdout missing skips:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"scan", workspace}, &stdout, &stderr); code != 0 {
		t.Fatalf("rerun scan exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "unchanged: alpha") {
		t.Fatalf("rerun scan stdout missing unchanged report:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	// alpha contains the nested repo fixture, so local tree readiness should surface it as dirty.
	if !strings.Contains(stdout.String(), "alpha") || !strings.Contains(stdout.String(), "dirty") || !strings.Contains(stdout.String(), alpha) {
		t.Fatalf("tree output missing scanned project:\n%s", stdout.String())
	}
}

func TestStatusReportsDirtyCheckoutWarning(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "dirty-source")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	if code := run([]string{"add", repo}, &stdout, &stderr); code != 0 {
		t.Fatalf("add exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	code := run([]string{"status", "dirty-source", "--base", "main"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"project: dirty-source",
		"state: dirty",
		"path_present: true",
		"warning: dirty-checkout",
		"blockers: none",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusJSONReportsReadinessPayload(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "clean-repo")
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "clean-repo", "--base", "main", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				Alias       string `json:"alias"`
				State       string `json:"state"`
				Path        string `json:"path"`
				PathPresent bool   `json:"path_present"`
				Remote      string `json:"remote"`
				Base        string `json:"base"`
				Diagnostics struct {
					Warnings []struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"warnings"`
					Blockers []struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"blockers"`
				} `json:"diagnostics"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("status --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.Command != "status" || payload.ExitClass != "success" {
		t.Fatalf("status --json command metadata = %#v", payload)
	}
	if len(payload.Payload.Projects) != 1 {
		t.Fatalf("status --json project count = %d, want 1", len(payload.Payload.Projects))
	}
	project := payload.Payload.Projects[0]
	if project.Alias != "clean-repo" || project.State != "present" || project.Path != canonicalRepo || !project.PathPresent || project.Base != "main" {
		t.Fatalf("status --json project payload = %#v", project)
	}
	if project.Remote == "" {
		t.Fatalf("status --json remote empty: %#v", project)
	}
	if len(project.Diagnostics.Warnings) != 0 || len(project.Diagnostics.Blockers) != 0 {
		t.Fatalf("status --json diagnostics = %#v", project.Diagnostics)
	}
}

func TestStatusJSONClassifiesReadinessWarnings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "dirty-source")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "dirty-source", "--base", "main", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	var payload struct {
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				State       string `json:"state"`
				Diagnostics struct {
					Warnings []struct {
						Code string `json:"code"`
					} `json:"warnings"`
					Blockers []struct {
						Code string `json:"code"`
					} `json:"blockers"`
				} `json:"diagnostics"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("status --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.ExitClass != "readiness-warning" {
		t.Fatalf("exit_class = %q, want readiness-warning", payload.ExitClass)
	}
	if len(payload.Payload.Projects) != 1 || payload.Payload.Projects[0].State != "dirty" {
		t.Fatalf("project payload = %#v", payload.Payload.Projects)
	}
	diagnostics := payload.Payload.Projects[0].Diagnostics
	if len(diagnostics.Warnings) != 1 || diagnostics.Warnings[0].Code != "dirty-checkout" || len(diagnostics.Blockers) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestStatusJSONClassifiesReadinessBlockers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "missing-source")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"status", "missing-source", "--base", "main", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	var payload struct {
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Projects []struct {
				State       string `json:"state"`
				PathPresent bool   `json:"path_present"`
				Diagnostics struct {
					Warnings []struct {
						Code string `json:"code"`
					} `json:"warnings"`
					Blockers []struct {
						Code string `json:"code"`
					} `json:"blockers"`
				} `json:"diagnostics"`
			} `json:"projects"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("status --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.ExitClass != "readiness-blocked" {
		t.Fatalf("exit_class = %q, want readiness-blocked", payload.ExitClass)
	}
	if len(payload.Payload.Projects) != 1 || payload.Payload.Projects[0].State != "missing" || payload.Payload.Projects[0].PathPresent {
		t.Fatalf("project payload = %#v", payload.Payload.Projects)
	}
	diagnostics := payload.Payload.Projects[0].Diagnostics
	if len(diagnostics.Warnings) != 0 || len(diagnostics.Blockers) != 1 || diagnostics.Blockers[0].Code != "missing-path" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func TestStatusBaseRequiresBranchBeforeJSONFlag(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"status", "--base", "--json"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("status exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "status --base requires a branch") {
		t.Fatalf("stderr did not explain missing base:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "readiness:") {
		t.Fatalf("stdout rendered status instead of usage failure:\n%s", stdout.String())
	}
}

func TestDoctorReportsHandoffGreenWithoutCreatingAgentRun(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "doctor-ready")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "doctor-ready", "--base", "main"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"handoff: green",
		"project: doctor-ready",
		"state: present",
		"path_present: true",
		"base: main",
		"warnings: none",
		"blockers: none",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "ready_path: ") {
		t.Fatalf("doctor output included an agent workspace path:\n%s", output)
	}
	assertNoAgentRuns(t, home)
	if entries, err := os.ReadDir(filepath.Join(home, "agents")); err == nil && len(entries) != 0 {
		t.Fatalf("agents dir has entries after doctor: %v", entries)
	}
}

func TestDoctorStrictPromotesWarningsToFailureInCommandResultJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "doctor-dirty")
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "doctor-dirty", "--base", "main", "--strict", "--json"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("doctor --strict exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor --strict stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Handoff     string `json:"handoff"`
			Strict      bool   `json:"strict"`
			State       string `json:"state"`
			Base        string `json:"base"`
			Diagnostics struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("doctor --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.Command != "doctor" || payload.ExitClass != "readiness-warning" {
		t.Fatalf("doctor command metadata = %#v", payload)
	}
	if payload.Payload.Project != "doctor-dirty" || payload.Payload.Handoff != "warning" || !payload.Payload.Strict || payload.Payload.State != "dirty" || payload.Payload.Base != "main" {
		t.Fatalf("doctor payload = %#v", payload.Payload)
	}
	diagnostics := payload.Payload.Diagnostics
	if len(diagnostics.Warnings) != 1 || diagnostics.Warnings[0].Code != "dirty-checkout" || len(diagnostics.Blockers) != 0 {
		t.Fatalf("doctor diagnostics = %#v", diagnostics)
	}
	assertNoAgentRuns(t, home)
}

func TestDoctorReportsActionableBlockers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "doctor-blocked")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "doctor-blocked", "--base", "release/missing"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("doctor exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"handoff: blocked",
		"project: doctor-blocked",
		"base: release/missing",
		"blocker: missing-base",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor stderr = %q, want empty", stderr.String())
	}
	assertNoAgentRuns(t, home)
}

func TestDoctorReportsToolchainReadinessInHumanAndJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	repo := createCommittedLocalRemoteClone(t, "doctor-toolchain")
	if err := os.WriteFile(filepath.Join(repo, ".codemesh.yml"), []byte("agent:\n  toolchain:\n    mode: warn\n    requirements:\n      - git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".codemesh.yml")
	runGit(t, repo, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Declare toolchain")
	runGit(t, repo, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", repo}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "doctor-toolchain", "--base", "main"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "toolchain: git present") || strings.Contains(stdout.String(), "warning: unknown-toolchain") {
		t.Fatalf("doctor human output missing toolchain readiness:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor stderr = %q, want empty", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"doctor", "doctor-toolchain", "--base", "main", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("doctor --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Toolchain []struct {
				Name    string `json:"name"`
				Status  string `json:"status"`
				Project struct {
					Requirement string `json:"requirement"`
				} `json:"project"`
				Host struct {
					Command string `json:"command"`
					Version string `json:"version"`
				} `json:"host"`
			} `json:"toolchain"`
			Diagnostics struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("doctor --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.Command != "doctor" || payload.ExitClass != "success" {
		t.Fatalf("doctor JSON metadata = %#v", payload)
	}
	if len(payload.Payload.Toolchain) != 1 || payload.Payload.Toolchain[0].Name != "git" || payload.Payload.Toolchain[0].Status != "present" {
		t.Fatalf("toolchain payload = %#v", payload.Payload.Toolchain)
	}
	if payload.Payload.Toolchain[0].Project.Requirement != "git" || payload.Payload.Toolchain[0].Host.Command != "git" || payload.Payload.Toolchain[0].Host.Version == "" {
		t.Fatalf("toolchain facts = %#v", payload.Payload.Toolchain[0])
	}
	if len(payload.Payload.Diagnostics.Warnings) != 0 || len(payload.Payload.Diagnostics.Blockers) != 0 {
		t.Fatalf("doctor diagnostics = %#v", payload.Payload.Diagnostics)
	}
	assertNoAgentRuns(t, home)
}

func TestStatusWithoutProjectSummarizesKnownProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	clean := createCommittedLocalRemoteClone(t, "clean-repo")
	dirty := createCommittedLocalRemoteClone(t, "dirty-source")
	if err := os.WriteFile(filepath.Join(dirty, "dirty.txt"), []byte("local change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", clean}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add clean exit code = %d, want 0", code)
	}
	if code := run([]string{"add", dirty}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add dirty exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"status"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("status exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "- clean-repo state=present") || !strings.Contains(output, "- dirty-source state=dirty") {
		t.Fatalf("status summary missing normalized states:\n%s", output)
	}
}

func TestHydrateMissingProjectClonesDesiredPathAndUpdatesTree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "missing-source")
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "- missing-source missing "+source) {
		t.Fatalf("tree output missing missing project:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"hydrate", "missing-source"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hydrated project: missing-source") {
		t.Fatalf("hydrate stdout missing success:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(source, "README.md")); err != nil {
		t.Fatalf("hydrated checkout missing README: %v", err)
	}
	assertGitStatusClean(t, source)

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"tree"}, &stdout, &stderr); code != 0 {
		t.Fatalf("tree after hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "- missing-source present "+source) {
		t.Fatalf("tree output missing present hydrated project:\n%s", stdout.String())
	}
}

func TestHydratePresentProjectReportsNoCloneNeeded(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "present-source")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hydrate", "present-source"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "project already present: present-source") {
		t.Fatalf("hydrate stdout missing present report:\n%s", stdout.String())
	}
	assertGitStatusClean(t, source)
}

func TestHydrateJSONReportsHydratedAndAlreadyPresentOutcomes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "hydrate-json")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "hydrate-json", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("hydrate --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "hydrate-json", "hydrated", source, true, nil); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate --json stderr = %q, want empty", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"hydrate", "hydrate-json", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("hydrate present --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "success", "hydrate-json", "already-present", source, true, nil); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate present --json stderr = %q, want empty", stderr.String())
	}
}

func TestParseHydrateArgsRecordsCloneOptions(t *testing.T) {
	var stderr bytes.Buffer

	parsed, ok := parseHydrateArgs([]string{"demo", "--partial-clone", "--sparse", "README.md", "--sparse", "docs/adr", "--json"}, &stderr)

	if !ok {
		t.Fatalf("parseHydrateArgs failed: %s", stderr.String())
	}
	if parsed.Project != "demo" || !parsed.CloneOptions.Partial || strings.Join(parsed.CloneOptions.SparsePaths, ",") != "README.md,docs/adr" || !parsed.JSON {
		t.Fatalf("parsed hydrate args = %#v", parsed)
	}
}

func TestHydrateJSONReportsOptInPartialSparseStrategy(t *testing.T) {
	requireGitPartialSparseSupport(t)
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "hydrate-partial-sparse")
	if err := os.WriteFile(filepath.Join(source, "large.txt"), []byte("not selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "large.txt")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add sparse contrast")
	runGit(t, source, "push", "origin", "main")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "hydrate-partial-sparse", "--partial-clone", "--sparse", "README.md", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("hydrate partial sparse --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			CloneStrategy struct {
				Name        string   `json:"name"`
				History     string   `json:"history"`
				WorkingTree string   `json:"working_tree"`
				Filter      string   `json:"filter"`
				SparsePaths []string `json:"sparse_paths"`
			} `json:"clone_strategy"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("hydrate partial sparse --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	got := payload.Payload.CloneStrategy
	if payload.Command != "hydrate" || got.Name != "partial-sparse-clone" || got.History != "partial" || got.WorkingTree != "sparse" || got.Filter != "blob:none" || strings.Join(got.SparsePaths, ",") != "README.md" {
		t.Fatalf("partial sparse hydrate JSON = %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(source, "README.md")); err != nil {
		t.Fatalf("hydrated sparse README missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, "large.txt")); !os.IsNotExist(err) {
		t.Fatalf("hydrated sparse checkout included unselected file or stat failed: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate partial sparse --json stderr = %q, want empty", stderr.String())
	}
}

func TestHydrateRefusesExistingNonEmptyPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "conflict-source")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "local.txt"), []byte("do not overwrite\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "conflict-source"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("hydrate exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "path conflict") || !strings.Contains(stderr.String(), source) {
		t.Fatalf("stderr missing clear conflict:\n%s", stderr.String())
	}
	if got, err := os.ReadFile(filepath.Join(source, "local.txt")); err != nil || string(got) != "do not overwrite\n" {
		t.Fatalf("conflict file changed or missing: got %q err %v", got, err)
	}
}

func TestHydrateJSONReportsPathConflictOutcome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "hydrate-conflict-json")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "local.txt"), []byte("do not overwrite\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "hydrate-conflict-json", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate conflict --json exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "readiness-blocked", "hydrate-conflict-json", "path-conflict", source, true, []string{"path-conflict"}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate conflict --json stderr = %q, want empty", stderr.String())
	}
	if got, err := os.ReadFile(filepath.Join(source, "local.txt")); err != nil || string(got) != "do not overwrite\n" {
		t.Fatalf("conflict file changed or missing: got %q err %v", got, err)
	}
}

func TestHydrateJSONReportsUnknownProjectOutcome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "ghost-project", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate unknown --json exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "readiness-blocked", "ghost-project", "unknown-project", "", false, []string{"unknown-project"}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate unknown --json stderr = %q, want empty", stderr.String())
	}
}

func TestHydrateJSONReportsCloneFailureAsInternalError(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "hydrate-clone-failure")
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	remote := strings.TrimSpace(runGitOutput(t, source, "remote", "get-url", "origin"))
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"hydrate", "hydrate-clone-failure", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate clone failure --json exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "internal-error", "hydrate-clone-failure", "failed", source, true, []string{"hydrate-failed"}); err != nil {
		t.Fatal(err)
	}
	info, statErr := os.Stat(source)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("hydrate clone failure removed pre-existing target or stat failed: %v", statErr)
	}
	if entries, err := os.ReadDir(source); err != nil || len(entries) != 0 {
		t.Fatalf("hydrate clone failure target entries = %#v err=%v, want empty dir preserved", entries, err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate clone failure --json stderr = %q, want empty", stderr.String())
	}
}

func TestHydrateDoesNotCreatePlaceholderDirectoriesForOtherMissingProjects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	alpha := createCommittedLocalRemoteClone(t, "alpha-missing")
	beta := createCommittedLocalRemoteClone(t, "beta-missing")
	alpha, err := filepath.EvalSymlinks(alpha)
	if err != nil {
		t.Fatal(err)
	}
	beta, err = filepath.EvalSymlinks(beta)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", alpha}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add alpha exit code = %d, want 0", code)
	}
	if code := run([]string{"add", beta}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add beta exit code = %d, want 0", code)
	}
	if err := os.RemoveAll(alpha); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(beta); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"hydrate", "alpha-missing"}, &stdout, &stderr); code != 0 {
		t.Fatalf("hydrate exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if _, err := os.Stat(alpha); err != nil {
		t.Fatalf("hydrated path missing: %v", err)
	}
	if _, err := os.Stat(beta); !os.IsNotExist(err) {
		t.Fatalf("other missing project path was created or stat failed unexpectedly: %v", err)
	}
}

func TestAgentPreparePrintsReadyPathAndWritesRunMetadata(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-ready")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-ready", "--base", "main", "--profile", "codex"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("agent prepare exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "ready_path: ") {
		t.Fatalf("stdout missing ready path:\n%s", output)
	}
	if !strings.Contains(output, "handoff_docs: 1") {
		t.Fatalf("stdout missing handoff doc count:\n%s", output)
	}
	readyPath := valueAfterPrefix(t, output, "ready_path: ")
	if _, err := os.Stat(filepath.Join(readyPath, "README.md")); err != nil {
		t.Fatalf("ready checkout missing README: %v", err)
	}
	if _, err := os.Stat(filepath.Join(readyPath, "codemesh-run.json")); err != nil {
		t.Fatalf("metadata missing: %v", err)
	}
	metadataBytes, err := os.ReadFile(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.Contains(string(metadataBytes), `"contract_version": 1`) || !strings.Contains(string(metadataBytes), `"producer": {`) {
		t.Fatalf("metadata missing contract version/producer:\n%s", metadataBytes)
	}
}

func TestEnvBindAndAgentPrepareMaterializesFakeProviderBundle(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-env-bound")
	if err := os.WriteFile(filepath.Join(source, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: block\n    required_keys:\n      - CODEMESH_TEST_BOUND_TOKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".codemesh.yml")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require bound env")
	runGit(t, source, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"env", "bind", "agent-env-bound", "CODEMESH_TEST_BOUND_TOKEN", "--provider", "fake", "--ref", "fake://agent-token", "--scope", "codex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("env bind exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "bound env requirement: CODEMESH_TEST_BOUND_TOKEN") || strings.Contains(stdout.String(), "fake://agent-token") {
		t.Fatalf("env bind output missing requirement or leaked ref:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"agent", "prepare", "agent-env-bound", "--base", "main", "--env-provider", "fake", "--allow-env-scope", "codex"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent prepare exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "env_materialization: materialized") || !strings.Contains(output, "env_bundle: present") || !strings.Contains(output, "env_bundle_path: ") {
		t.Fatalf("agent prepare output missing env materialization metadata:\n%s", output)
	}
	fakeValue := envbinding.FakeProviderValue("fake://agent-token")
	if strings.Contains(output, fakeValue) {
		t.Fatalf("agent prepare output leaked fake provider value:\n%s", output)
	}
	readyPath := valueAfterPrefix(t, output, "ready_path: ")
	metadataBytes, err := os.ReadFile(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if strings.Contains(string(metadataBytes), fakeValue) {
		t.Fatalf("contract metadata leaked fake provider value:\n%s", metadataBytes)
	}
	var metadata struct {
		Env struct {
			MaterializationStatus string `json:"materialization_status"`
			Bundle                struct {
				Present bool   `json:"present"`
				Path    string `json:"path"`
				Values  string `json:"values"`
			} `json:"bundle"`
		} `json:"env"`
	}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.Env.MaterializationStatus != "materialized" || !metadata.Env.Bundle.Present || metadata.Env.Bundle.Path == "" || metadata.Env.Bundle.Values != "not-recorded" {
		t.Fatalf("env metadata = %#v", metadata.Env)
	}
	bundleBytes, err := os.ReadFile(metadata.Env.Bundle.Path)
	if err != nil {
		t.Fatalf("read env bundle: %v", err)
	}
	if !strings.Contains(string(bundleBytes), "CODEMESH_TEST_BOUND_TOKEN="+fakeValue) {
		t.Fatalf("env bundle = %q, want fake provider value", string(bundleBytes))
	}
}

func TestAgentHelpIncludesPrepareJSONFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"agent", "--help"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("agent help exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]") {
		t.Fatalf("agent help missing prepare JSON flag:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestAgentPrepareJSONReportsReadyContract(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-json-ready")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-json-ready", "--base", "main", "--profile", "codex", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("agent prepare --json exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent prepare --json stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project          string `json:"project"`
			Ready            bool   `json:"ready"`
			RunID            string `json:"run_id"`
			Base             string `json:"base"`
			Profile          string `json:"profile"`
			HandoffDocsCount int    `json:"handoff_docs_count"`
			RunContractPath  string `json:"run_contract_path"`
			ReadyPath        string `json:"ready_path"`
			ResolvedCommit   string `json:"resolved_commit"`
			CloneStrategy    struct {
				Name        string `json:"name"`
				History     string `json:"history"`
				WorkingTree string `json:"working_tree"`
			} `json:"clone_strategy"`
			Diagnostics struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("agent prepare --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	got := payload.Payload
	if payload.Command != "agent prepare" || payload.ExitClass != "success" || got.Project != "agent-json-ready" || !got.Ready || got.RunID == "" || got.Base != "main" || got.Profile != "codex" || got.HandoffDocsCount != 1 || got.ReadyPath == "" || got.ResolvedCommit == "" {
		t.Fatalf("agent prepare JSON payload = %#v", payload)
	}
	if got.CloneStrategy.Name != "full-clone" || got.CloneStrategy.History != "full" || got.CloneStrategy.WorkingTree != "complete" {
		t.Fatalf("clone_strategy = %#v, want full clone", got.CloneStrategy)
	}
	if got.RunContractPath != filepath.Join(got.ReadyPath, "codemesh-run.json") {
		t.Fatalf("run_contract_path = %q, want metadata under ready path %q", got.RunContractPath, got.ReadyPath)
	}
	if len(got.Diagnostics.Warnings) != 0 || len(got.Diagnostics.Blockers) != 0 {
		t.Fatalf("diagnostics = %#v, want empty", got.Diagnostics)
	}
	if _, err := os.Stat(got.RunContractPath); err != nil {
		t.Fatalf("run contract missing: %v", err)
	}
	if strings.Contains(stdout.String(), "agent workspace ready") {
		t.Fatalf("json output included human prose:\n%s", stdout.String())
	}
}

func TestParseAgentPrepareArgsRecordsCloneOptions(t *testing.T) {
	var stderr bytes.Buffer

	parsed, ok := parseAgentPrepareArgs([]string{"demo", "--base", "main", "--profile", "codex", "--partial-clone", "--sparse", "README.md", "--json"}, &stderr)

	if !ok {
		t.Fatalf("parseAgentPrepareArgs failed: %s", stderr.String())
	}
	if parsed.Project != "demo" || parsed.Base != "main" || parsed.Profile != "codex" || !parsed.CloneOptions.Partial || strings.Join(parsed.CloneOptions.SparsePaths, ",") != "README.md" || !parsed.JSON {
		t.Fatalf("parsed agent prepare args = %#v", parsed)
	}
}

func TestAgentPrepareJSONReportsOptInPartialSparseStrategy(t *testing.T) {
	requireGitPartialSparseSupport(t)
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-partial-sparse")
	if err := os.WriteFile(filepath.Join(source, "large.txt"), []byte("not selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "large.txt")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add sparse contrast")
	runGit(t, source, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-partial-sparse", "--base", "main", "--partial-clone", "--sparse", "README.md", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("agent prepare partial sparse --json exit code = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var payload struct {
		Command string `json:"command"`
		Payload struct {
			Ready           bool   `json:"ready"`
			RunContractPath string `json:"run_contract_path"`
			ReadyPath       string `json:"ready_path"`
			CloneStrategy   struct {
				Name        string   `json:"name"`
				History     string   `json:"history"`
				WorkingTree string   `json:"working_tree"`
				Filter      string   `json:"filter"`
				SparsePaths []string `json:"sparse_paths"`
			} `json:"clone_strategy"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("agent prepare partial sparse --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	got := payload.Payload.CloneStrategy
	if payload.Command != "agent prepare" || !payload.Payload.Ready || got.Name != "partial-sparse-clone" || got.History != "partial" || got.WorkingTree != "sparse" || got.Filter != "blob:none" || strings.Join(got.SparsePaths, ",") != "README.md" {
		t.Fatalf("partial sparse agent prepare JSON = %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(payload.Payload.ReadyPath, "README.md")); err != nil {
		t.Fatalf("ready sparse README missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(payload.Payload.ReadyPath, "large.txt")); !os.IsNotExist(err) {
		t.Fatalf("ready sparse checkout included unselected file or stat failed: %v", err)
	}
	metadataBytes, err := os.ReadFile(payload.Payload.RunContractPath)
	if err != nil {
		t.Fatalf("read run contract: %v", err)
	}
	if !strings.Contains(string(metadataBytes), `"filter": "blob:none"`) || !strings.Contains(string(metadataBytes), `"sparse_paths": [`) {
		t.Fatalf("run contract missing partial/sparse details:\n%s", metadataBytes)
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent prepare partial sparse --json stderr = %q, want empty", stderr.String())
	}
}

func TestRunsListsPreparedAgentRuns(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-listed")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	if code := run([]string{"agent", "prepare", "agent-listed", "--base", "main", "--profile", "codex"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("agent prepare exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"runs"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("runs exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"agent runs:", "project=agent-listed", "base=main", "profile=codex", "created=", "workspace="} {
		if !strings.Contains(output, want) {
			t.Fatalf("runs output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentRunExecutesCommandAndUpdatesRunLifecycle(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-executed")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"agent", "prepare", "agent-executed", "--base", "main", "--profile", "codex"}, &stdout, &stderr); code != 0 {
		t.Fatalf("agent prepare exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	readyPath := valueAfterPrefix(t, stdout.String(), "ready_path: ")

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"runs"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runs exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "state=prepared") {
		t.Fatalf("runs before execution missing prepared state:\n%s", stdout.String())
	}
	runID := firstRunID(t, stdout.String())

	stdout.Reset()
	stderr.Reset()
	code := run([]string{"agent", "run", runID, "--label", "workspace root", "--", "git", "rev-parse", "--show-toplevel"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent run exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"agent command complete", "run: " + runID, "label: workspace root", "exit_code: 0", "stdout_path: ", "stderr_path: "} {
		if !strings.Contains(output, want) {
			t.Fatalf("agent run output missing %q:\n%s", want, output)
		}
	}
	stdoutPath := valueAfterPrefix(t, output, "stdout_path: ")
	stdoutBytes, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read command stdout: %v", err)
	}
	canonicalReadyPath, err := filepath.EvalSymlinks(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stdoutBytes)) != canonicalReadyPath {
		t.Fatalf("command stdout = %q, want %q", stdoutBytes, canonicalReadyPath)
	}

	metadataBytes, err := os.ReadFile(filepath.Join(readyPath, "codemesh-run.json"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if !strings.Contains(string(metadataBytes), `"label": "workspace root"`) || !strings.Contains(string(metadataBytes), `"values": "not-recorded"`) || !strings.Contains(string(metadataBytes), `"exit_code": 0`) {
		t.Fatalf("metadata missing command contract:\n%s", metadataBytes)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"runs"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runs after execution exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "state=executed") {
		t.Fatalf("runs after execution missing executed state:\n%s", stdout.String())
	}
}

func TestCleanDeletesOnlyOldManagedAgentRuns(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	agents := filepath.Join(home, "agents")
	t.Setenv("CODEMESH_HOME", home)
	now := time.Now().UTC()
	oldRun := createRecordedAgentRun(t, home, "run-old", "old-project", now.Add(-8*24*time.Hour))
	newRun := createRecordedAgentRun(t, home, "run-new", "new-project", now.Add(-2*24*time.Hour))

	var stdout, stderr bytes.Buffer
	code := run([]string{"clean", "--older-than", "7d"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("clean exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted: 1") || !strings.Contains(stdout.String(), "kept: 1") {
		t.Fatalf("clean output missing counts:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(agents, oldRun)); !os.IsNotExist(err) {
		t.Fatalf("old run dir exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agents, newRun, "workspace", "README.md")); err != nil {
		t.Fatalf("new run workspace missing: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"runs"}, &stdout, &stderr); code != 0 {
		t.Fatalf("runs exit code = %d, want 0\nstderr:\n%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "old-project") || !strings.Contains(stdout.String(), "new-project") {
		t.Fatalf("runs output did not reflect cleaned metadata:\n%s", stdout.String())
	}
}

func TestCleanRefusesUnsafeAgentRunPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	now := time.Now().UTC()
	safeID := createRecordedAgentRun(t, home, "run-safe", "safe-project", now.Add(-8*24*time.Hour))
	outside := filepath.Join(t.TempDir(), "outside-workspace")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	recordAgentRun(t, home, state.AgentRun{
		ID:            "run-unsafe",
		WorkspacePath: outside,
		MetadataJSON:  agentRunMetadata("run-unsafe", "unsafe-project", outside, now.Add(-8*24*time.Hour)),
		CreatedAt:     now.Add(-8 * 24 * time.Hour),
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"clean", "--older-than", "7d"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("clean exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "outside CodeMesh-managed agents storage") {
		t.Fatalf("stderr missing unsafe refusal:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, "agents", safeID, "workspace", "README.md")); err != nil {
		t.Fatalf("safe run was deleted: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside path changed: %v", err)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runs, err := store.ListAgentRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("metadata rows = %d, want 2 after refusal", len(runs))
	}
}

func TestAgentPrepareBlocksOnReadinessBlockers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-blocked")
	if err := os.WriteFile(filepath.Join(source, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".codemesh.yml")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env")
	runGit(t, source, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-blocked"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("agent prepare exit code = 0, want failure\nstdout:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "blocker: missing-env-file") {
		t.Fatalf("stderr missing actionable blocker:\n%s", stderr.String())
	}
	if entries, err := os.ReadDir(filepath.Join(home, "agents")); err == nil && len(entries) != 0 {
		t.Fatalf("agents dir has entries after blocked prep: %v", entries)
	}
}

func TestAgentPrepareJSONReportsReadinessBlocker(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	source := createCommittedLocalRemoteClone(t, "agent-json-blocked")
	if err := os.WriteFile(filepath.Join(source, ".codemesh.yml"), []byte("agent:\n  env:\n    mode: block\n    required_files:\n      - .env.local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", ".codemesh.yml")
	runGit(t, source, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Require env")
	runGit(t, source, "push", "origin", "main")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", source}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"agent", "prepare", "agent-json-blocked", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("agent prepare blocked --json exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent prepare blocked --json stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project          string `json:"project"`
			Ready            bool   `json:"ready"`
			Base             string `json:"base"`
			HandoffDocsCount int    `json:"handoff_docs_count"`
			RunContractPath  string `json:"run_contract_path,omitempty"`
			ReadyPath        string `json:"ready_path,omitempty"`
			Diagnostics      struct {
				Warnings []struct {
					Code string `json:"code"`
				} `json:"warnings"`
				Blockers []struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("agent prepare blocked --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	got := payload.Payload
	if payload.Command != "agent prepare" || payload.ExitClass != "readiness-blocked" || got.Project != "agent-json-blocked" || got.Ready || got.Base != "main" || got.HandoffDocsCount != 0 || got.ReadyPath != "" || got.RunContractPath != "" {
		t.Fatalf("blocked agent prepare JSON payload = %#v", payload)
	}
	if len(got.Diagnostics.Blockers) != 1 || got.Diagnostics.Blockers[0].Code != "missing-env-file" || !strings.Contains(got.Diagnostics.Blockers[0].Message, ".env.local") {
		t.Fatalf("blocked diagnostics = %#v", got.Diagnostics)
	}
	if strings.Contains(stdout.String(), "=") {
		t.Fatalf("blocked JSON output contained an env assignment-looking value:\n%s", stdout.String())
	}
	if entries, err := os.ReadDir(filepath.Join(home, "agents")); err == nil && len(entries) != 0 {
		t.Fatalf("agents dir has entries after blocked prep: %v", entries)
	}
}

func TestAgentPrepareJSONReportsUnknownProjectAsJSON(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"agent", "prepare", "ghost-project", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("agent prepare unknown --json exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("agent prepare unknown --json stderr = %q, want empty", stderr.String())
	}
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project     string `json:"project"`
			Ready       bool   `json:"ready"`
			Diagnostics struct {
				Blockers []struct {
					Code string `json:"code"`
				} `json:"blockers"`
			} `json:"diagnostics"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("agent prepare unknown --json stdout was not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	if payload.Command != "agent prepare" || payload.ExitClass != "readiness-blocked" || payload.Payload.Project != "ghost-project" || payload.Payload.Ready {
		t.Fatalf("unknown project JSON payload = %#v", payload)
	}
	if len(payload.Payload.Diagnostics.Blockers) != 1 || payload.Payload.Diagnostics.Blockers[0].Code != "unknown-project" {
		t.Fatalf("unknown project diagnostics = %#v", payload.Payload.Diagnostics)
	}
}

func TestAgentPrepareBaseRequiresBranchBeforeJSONFlag(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"agent", "prepare", "ghost-project", "--base", "--json"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("agent prepare exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "agent prepare --base requires a branch") {
		t.Fatalf("stderr did not explain missing base:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "agent workspace ready") || strings.Contains(stdout.String(), `"command"`) {
		t.Fatalf("stdout rendered command output instead of usage failure:\n%s", stdout.String())
	}
}

func TestAgentPrepareProfileRequiresNameBeforeJSONFlag(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	t.Setenv("CODEMESH_HOME", home)
	var stdout, stderr bytes.Buffer

	code := run([]string{"agent", "prepare", "ghost-project", "--profile", "--json"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("agent prepare exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "agent prepare --profile requires a name") {
		t.Fatalf("stderr did not explain missing profile:\n%s", stderr.String())
	}
	if strings.Contains(stdout.String(), "agent workspace ready") || strings.Contains(stdout.String(), `"command"`) {
		t.Fatalf("stdout rendered command output instead of usage failure:\n%s", stdout.String())
	}
}

func TestAddAliasConflictFailsWithActionableError(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codemesh-home")
	first := createGitRepo(t, "https://github.com/BramVR/first.git")
	second := createGitRepo(t, "https://github.com/BramVR/second.git")
	t.Setenv("CODEMESH_HOME", home)

	if code := run([]string{"add", "--alias", "shared", first}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("first add exit code = %d, want 0", code)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"add", "--alias", "shared", second}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("second add exit code = 0, want failure")
	}
	if !strings.Contains(stderr.String(), "alias") || !strings.Contains(stderr.String(), "shared") || !strings.Contains(stderr.String(), "--alias") {
		t.Fatalf("stderr missing actionable conflict:\n%s", stderr.String())
	}
}

func valueAfterPrefix(t *testing.T, output, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	t.Fatalf("prefix %q missing in output:\n%s", prefix, output)
	return ""
}

func firstRunID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			fields := strings.Fields(strings.TrimPrefix(line, "- "))
			if len(fields) != 0 {
				return fields[0]
			}
		}
	}
	t.Fatalf("run id missing in output:\n%s", output)
	return ""
}

func TestHydrateJSONReportsAbsentUnsafePathAsNotPresent(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "codemesh-home")
	workspace := filepath.Join(tmp, "workspace")
	unsafePath := filepath.Join(tmp, "outside", "unsafe-project")
	t.Setenv("CODEMESH_HOME", home)
	if code := run([]string{"init", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}
	if code := run([]string{"machine", "register", workspace}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("machine register exit code = %d, want 0", code)
	}
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProject(context.Background(), state.Project{
		Alias:            "unsafe-project",
		NormalizedRemote: "https://example.invalid/org/unsafe-project",
		CloneURL:         "https://example.invalid/org/unsafe-project.git",
		LocalPath:        unsafePath,
		CanonicalPath:    unsafePath,
		Source:           "canonical",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	code := run([]string{"hydrate", "unsafe-project", "--json"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("hydrate unsafe exit code = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("hydrate unsafe stderr = %q, want empty", stderr.String())
	}
	if err := assertHydrateJSON(stdout.Bytes(), "readiness-blocked", "unsafe-project", "unsafe-path", unsafePath, false, []string{"unsafe-path"}); err != nil {
		t.Fatal(err)
	}
}

func assertHydrateJSON(data []byte, exitClass, alias, outcome, path string, pathPresent bool, blockerCodes []string) error {
	var payload struct {
		Command   string `json:"command"`
		ExitClass string `json:"exit_class"`
		Payload   struct {
			Project       string `json:"project"`
			Outcome       string `json:"outcome"`
			Path          string `json:"path"`
			PathPresent   bool   `json:"path_present"`
			Remote        string `json:"remote"`
			CloneStrategy struct {
				Name        string `json:"name"`
				History     string `json:"history"`
				WorkingTree string `json:"working_tree"`
			} `json:"clone_strategy"`
		} `json:"payload"`
		Diagnostics struct {
			Blockers []struct {
				Code string `json:"code"`
			} `json:"blockers"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Command != "hydrate" || payload.ExitClass != exitClass {
		return fmt.Errorf("command metadata = %#v, want command hydrate exit_class %s", payload, exitClass)
	}
	got := payload.Payload
	if got.Project != alias || got.Outcome != outcome || got.Path != path || got.PathPresent != pathPresent {
		return fmt.Errorf("hydrate payload = %#v", got)
	}
	if outcome != "unknown-project" && (got.CloneStrategy.Name != "full-clone" || got.CloneStrategy.History != "full" || got.CloneStrategy.WorkingTree != "complete") {
		return fmt.Errorf("hydrate clone_strategy = %#v, want full clone", got.CloneStrategy)
	}
	if got.Remote == "" && outcome != "unknown-project" && outcome != "failed" {
		return fmt.Errorf("hydrate remote empty for outcome %q: %#v", outcome, got)
	}
	if len(payload.Diagnostics.Blockers) != len(blockerCodes) {
		return fmt.Errorf("blockers = %#v, want codes %v", payload.Diagnostics.Blockers, blockerCodes)
	}
	for i, want := range blockerCodes {
		if payload.Diagnostics.Blockers[i].Code != want {
			return fmt.Errorf("blocker[%d] = %q, want %q", i, payload.Diagnostics.Blockers[i].Code, want)
		}
	}
	return nil
}

func writeManifestEntry(t *testing.T, dir, name, identity, alias, desiredPath string) {
	t.Helper()
	writeManifestEntryWithCloneURL(t, dir, name, identity, alias, desiredPath, identity+".git")
}

func writeManifestEntryWithCloneURL(t *testing.T, dir, name, identity, alias, desiredPath, cloneURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := workspacemanifest.NewEntry(workspacemanifest.ProjectEntry{
		Identity:    identity,
		Alias:       alias,
		DesiredPath: desiredPath,
		CloneHints:  workspacemanifest.CloneHints{URL: cloneURL},
	})
	data, err := workspacemanifest.EncodeEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertProjectRows(t *testing.T, home string, want int) {
	t.Helper()
	projects := listProjectsForTest(t, home)
	if len(projects) != want {
		t.Fatalf("project rows = %d, want %d: %#v", len(projects), want, projects)
	}
}

func listProjectsForTest(t *testing.T, home string) []state.Project {
	t.Helper()
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	projects, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return projects
}

func assertGitStatusClean(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(output)) != "" {
		t.Fatalf("git status not clean:\n%s", output)
	}
}

func requireGitPartialSparseSupport(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	seed := createGitRepoAt(t, filepath.Join(tmp, "seed"), "")
	if err := os.WriteFile(filepath.Join(seed, "large.txt"), []byte("not selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Add sparse probe")
	largeBlobBytes, err := exec.Command("git", "-C", seed, "rev-parse", "HEAD:large.txt").Output()
	if err != nil {
		t.Fatal(err)
	}
	largeBlob := strings.TrimSpace(string(largeBlobBytes))
	remote := filepath.Join(tmp, "remote.git")
	clone := filepath.Join(tmp, "clone")
	runGit(t, tmp, "clone", "--bare", seed, remote)
	output, err := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout", "--branch", "main", "--single-branch", "file://"+remote, clone).CombinedOutput()
	if err != nil {
		t.Skipf("git partial clone probe failed: %v: %s", err, output)
	}
	lower := strings.ToLower(string(output))
	if strings.Contains(lower, "filtering not recognized") || strings.Contains(lower, "filter") && strings.Contains(lower, "ignoring") {
		t.Skipf("git partial clone filter unsupported by local file transport: %s", output)
	}
	runGit(t, clone, "sparse-checkout", "set", "--no-cone", "--", "/README.md")
	runGit(t, clone, "checkout", "main")
	cmd := exec.Command("git", "-C", clone, "cat-file", "-e", largeBlob)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	if err := cmd.Run(); err == nil {
		t.Skipf("git partial clone filter fetched unselected blob %s", largeBlob)
	}
}

func commandUsageLines(help string) []string {
	var commands []string
	inUsage := false
	for _, line := range strings.Split(help, "\n") {
		switch {
		case strings.TrimSpace(line) == "Usage:":
			inUsage = true
		case inUsage && strings.TrimSpace(line) == "":
			return commands
		case inUsage:
			command := strings.TrimSpace(line)
			if strings.HasPrefix(command, "codemesh ") && !strings.Contains(command, "[--help]") && !strings.Contains(command, "[--version]") {
				commands = append(commands, command)
			}
		}
	}
	return commands
}

func catalogCurrentCommands(t *testing.T) []string {
	t.Helper()
	refs := catalogCommandReferences(t)
	commands := make([]string, 0, len(refs))
	for _, ref := range refs {
		commands = append(commands, ref.command)
	}
	return commands
}

type commandReference struct {
	command string
	path    string
}

func commandHeading(command string) string {
	return regexp.MustCompile(`\s+(?:\[|<).*$`).ReplaceAllString(command, "")
}

func catalogCommandReferences(t *testing.T) []commandReference {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "commands.md"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^- \[` + "`" + `(codemesh .*)` + "`" + `\]\((commands/[a-z0-9-]+\.md)\)$`)
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("docs/commands.md did not link current command references")
	}
	refs := make([]commandReference, 0, len(matches))
	for _, match := range matches {
		refs = append(refs, commandReference{command: match[1], path: match[2]})
	}
	return refs
}

func createGitRepo(t *testing.T, remote string) string {
	t.Helper()
	return createGitRepoAt(t, filepath.Join(t.TempDir(), "codemesh"), remote)
}

func createGitRepoAt(t *testing.T, repo, remote string) string {
	t.Helper()
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	if remote != "" {
		runGit(t, repo, "remote", "add", "origin", remote)
	}
	root, err := gitRoot(repo)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func createCommittedLocalRemoteClone(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	remote := filepath.Join(root, "remote.git")
	source := filepath.Join(root, name)
	runGit(t, root, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial fixture")
	runGit(t, root, "clone", "--bare", seed, remote)
	runGit(t, root, "clone", remote, source)
	return source
}

func createRecordedAgentRun(t *testing.T, home, id, project string, createdAt time.Time) string {
	t.Helper()
	workspace := filepath.Join(home, "agents", id, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# "+project+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recordAgentRun(t, home, state.AgentRun{
		ID:            id,
		WorkspacePath: workspace,
		MetadataJSON:  agentRunMetadata(id, project, workspace, createdAt),
		CreatedAt:     createdAt,
	})
	return id
}

func recordAgentRun(t *testing.T, home string, run state.AgentRun) {
	t.Helper()
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAgentRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
}

func assertNoAgentRuns(t *testing.T, home string) {
	t.Helper()
	store, err := state.Open(filepath.Join(home, "codemesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListAgentRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("agent runs = %d, want none: %#v", len(runs), runs)
	}
}

func agentRunMetadata(id, project, workspace string, createdAt time.Time) string {
	return `{
  "run_id": "` + id + `",
  "ready_path": "` + workspace + `",
  "project": {"alias": "` + project + `"},
  "base": "main",
  "profile": "codex",
  "created_at": "` + createdAt.UTC().Format(time.RFC3339) + `"
}`
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func createBareRemoteForTest(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, name+"-seed")
	remote := filepath.Join(root, name+".git")
	runGit(t, root, "init", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, seed, "add", "README.md")
	runGit(t, seed, "-c", "user.name=CodeMesh Test", "-c", "user.email=test@example.invalid", "commit", "-m", "Initial commit")
	runGit(t, root, "clone", "--bare", seed, remote)
	return remote
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return string(output)
}

func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
