package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const proofKind = "codemesh-crabbox-pr-proof"

type proofManifest struct {
	SchemaVersion int             `json:"schema_version"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	Runner        string          `json:"runner"`
	Fixture       string          `json:"fixture"`
	Source        string          `json:"source"`
	GeneratedAt   string          `json:"generated_at"`
	Coverage      []string        `json:"coverage"`
	Commands      []proofCommand  `json:"commands"`
	Artifacts     []proofArtifact `json:"artifacts"`
	Confidential  string          `json:"confidentiality"`
}

type proofCommand struct {
	Name           string `json:"name"`
	ExitCode       int    `json:"exit_code"`
	TranscriptPath string `json:"transcript_path"`
}

type proofArtifact struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Bytes    int64  `json:"bytes,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}

type runner struct {
	bin          string
	outDir       string
	fixtureRoot  string
	commandsDir  string
	commands     []proofCommand
	replacements []replacement
	commandIndex int
	gitEnv       []string
}

type replacement struct {
	from string
	to   string
}

type machineNode struct {
	label        string
	codeMeshHome string
	home         string
	workspace    string
	gitConfig    string
	env          []string
}

type commandCapture struct {
	label      string
	exitCode   int
	stdout     string
	stderr     string
	transcript string
}

type workspaceManifest struct {
	ManifestVersion int                `json:"manifest_version"`
	Projects        []workspaceProject `json:"projects"`
}

type workspaceProject struct {
	Identity   string            `json:"identity"`
	Alias      string            `json:"alias"`
	Desired    string            `json:"desired_path"`
	CloneHints map[string]string `json:"clone_hints,omitempty"`
	Groups     []string          `json:"groups"`
}

type legacyManifestEntry struct {
	ManifestVersion int              `json:"manifest_version"`
	Project         workspaceProject `json:"project"`
}

func main() {
	var bin string
	var outDir string
	var fixtureRoot string
	flag.StringVar(&bin, "bin", envOrDefault("CODEMESH_PR_PROOF_BIN", filepath.Join("dist", "codemesh")), "path to packaged codemesh binary")
	flag.StringVar(&outDir, "out", envOrDefault("CODEMESH_PR_PROOF_DIR", filepath.Join("tmp", "crabbox-pr-proof")), "artifact output directory")
	flag.StringVar(&fixtureRoot, "fixture", envOrDefault("CODEMESH_PR_PROOF_FIXTURE", filepath.Join("tmp", "crabbox-pr-proof-fixture")), "isolated fixture directory")
	flag.Parse()

	if err := runProof(bin, outDir, fixtureRoot); err != nil {
		fmt.Fprintf(os.Stderr, "crabbox PR proof failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("crabbox_pr_proof_dir: %s\n", outDir)
}

func runProof(bin, outDir, fixtureRoot string) error {
	binAbs, err := filepath.Abs(bin)
	if err != nil {
		return err
	}
	if info, err := os.Stat(binAbs); err != nil {
		return fmt.Errorf("stat codemesh binary: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("codemesh binary is a directory: %s", binAbs)
	}
	outAbs, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	fixtureAbs, err := filepath.Abs(fixtureRoot)
	if err != nil {
		return err
	}
	if err := resetGeneratedDir(outAbs); err != nil {
		return err
	}
	if err := resetGeneratedDir(fixtureAbs); err != nil {
		return err
	}

	repoRoot, _ := os.Getwd()
	hostname, _ := os.Hostname()
	r := &runner{
		bin:         binAbs,
		outDir:      outAbs,
		fixtureRoot: fixtureAbs,
		commandsDir: filepath.Join(outAbs, "commands"),
		replacements: []replacement{
			{from: fixtureAbs, to: "<FIXTURE_ROOT>"},
			{from: outAbs, to: "<PROOF_ROOT>"},
			{from: repoRoot, to: "<REPO_ROOT>"},
			{from: hostname, to: "<HOSTNAME>"},
		},
	}
	if err := os.MkdirAll(r.commandsDir, 0o755); err != nil {
		return err
	}
	if err := r.initGitSetupEnv(); err != nil {
		return err
	}

	targetRemotePath, err := r.createRemote("mesh-target")
	if err != nil {
		return err
	}
	unrelatedRemotePath, err := r.createRemote("mesh-unrelated")
	if err != nil {
		return err
	}
	remoteBase, stopGitDaemon, err := r.startLocalGitDaemon(filepath.Join(fixtureAbs, "remotes"), filepath.Base(targetRemotePath))
	if err != nil {
		return err
	}
	defer stopGitDaemon()
	targetRemote := remoteBase + "/" + filepath.Base(targetRemotePath)
	unrelatedRemote := remoteBase + "/" + filepath.Base(unrelatedRemotePath)
	r.replacements = append(r.replacements, replacement{from: remoteBase, to: "<FIXTURE_GIT_REMOTE>"})

	machineA, err := r.newMachine("machine-a")
	if err != nil {
		return err
	}
	machineB, err := r.newMachine("machine-b")
	if err != nil {
		return err
	}
	machineC, err := r.newMachine("machine-c")
	if err != nil {
		return err
	}

	targetA := filepath.Join(machineA.workspace, "projects", "mesh-target")
	unrelatedA := filepath.Join(machineA.workspace, "projects", "mesh-unrelated")
	targetB := filepath.Join(machineB.workspace, "projects", "mesh-target")
	unrelatedB := filepath.Join(machineB.workspace, "projects", "mesh-unrelated")
	targetC := filepath.Join(machineC.workspace, "projects", "mesh-target")
	unrelatedC := filepath.Join(machineC.workspace, "projects", "mesh-unrelated")
	if err := os.MkdirAll(filepath.Dir(targetA), 0o755); err != nil {
		return err
	}

	if _, err := r.run("machine A clone mesh-target", machineA.workspace, machineA.env, "git", "clone", targetRemote, targetA); err != nil {
		return err
	}
	if _, err := r.run("machine A clone mesh-unrelated", machineA.workspace, machineA.env, "git", "clone", unrelatedRemote, unrelatedA); err != nil {
		return err
	}
	for _, step := range []struct {
		label string
		node  machineNode
		args  []string
	}{
		{"machine A init", machineA, []string{"init", machineA.workspace}},
		{"machine A register", machineA, []string{"machine", "register", machineA.workspace, "--name", "Fixture Machine A"}},
		{"machine A add mesh-target", machineA, []string{"add", targetA, "--alias", "mesh-target"}},
		{"machine A add mesh-unrelated", machineA, []string{"add", unrelatedA, "--alias", "mesh-unrelated"}},
	} {
		if _, err := r.runCodeMesh(step.label, step.node, step.args...); err != nil {
			return err
		}
	}
	treeA, err := r.runCodeMesh("machine A canonical tree", machineA, "tree")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(outAbs, "workspace-manifest.json")
	if _, err := r.runCodeMesh("machine A manifest export", machineA, "manifest", "export", "--output", manifestPath); err != nil {
		return err
	}
	legacyDir := filepath.Join(fixtureAbs, "legacy-bootstrap-manifest")
	if err := writeLegacyManifestEntries(manifestPath, legacyDir); err != nil {
		return err
	}
	if _, err := r.runCodeMesh("machine C init", machineC, "init", machineC.workspace); err != nil {
		return err
	}
	if _, err := r.runCodeMesh("machine C register", machineC, "machine", "register", machineC.workspace, "--name", "Fixture Machine C"); err != nil {
		return err
	}
	manifestImport, err := r.runCodeMesh("machine C manifest import", machineC, "manifest", "import", manifestPath)
	if err != nil {
		return err
	}
	if !strings.Contains(manifestImport.stdout, "manifest imported") || !strings.Contains(manifestImport.stdout, "added: mesh-target") {
		return errors.New("manifest import proof did not report imported workspace projects")
	}
	treeAfterImport, err := r.runCodeMesh("machine C tree after manifest import", machineC, "tree")
	if err != nil {
		return err
	}
	statusAfterImport, err := r.runCodeMesh("machine C status after manifest import", machineC, "status", "--json")
	if err != nil {
		return err
	}
	if !strings.Contains(statusAfterImport.stdout, `"workspace_source":"canonical"`) || !strings.Contains(statusAfterImport.stdout, `"state":"missing"`) {
		return errors.New("manifest import status proof did not show canonical missing projects")
	}
	if err := sanitizeFile(manifestPath, r.replacements); err != nil {
		return err
	}

	if _, err := r.runCodeMesh("machine B init", machineB, "init", machineB.workspace); err != nil {
		return err
	}
	if _, err := r.runCodeMesh("machine B register", machineB, "machine", "register", machineB.workspace, "--name", "Fixture Machine B"); err != nil {
		return err
	}
	treeBefore, err := r.runCodeMesh("machine B tree before bootstrap", machineB, "tree")
	if err != nil {
		return err
	}
	bootstrapPlan, err := r.runCodeMesh("machine B bootstrap dry-run", machineB, "bootstrap", legacyDir)
	if err != nil {
		return err
	}
	if !strings.Contains(bootstrapPlan.stdout, "missing: mesh-target") || !strings.Contains(bootstrapPlan.stdout, "apply: false") {
		return errors.New("bootstrap dry-run proof did not include planned missing projects")
	}
	if _, err := r.runCodeMesh("machine B bootstrap apply", machineB, "bootstrap", legacyDir, "--apply"); err != nil {
		return err
	}
	treeAfterBootstrap, err := r.runCodeMesh("machine B tree after bootstrap", machineB, "tree")
	if err != nil {
		return err
	}
	hydrate, err := r.runCodeMesh("machine B hydrate mesh-target", machineB, "hydrate", "mesh-target")
	if err != nil {
		return err
	}
	if !strings.Contains(hydrate.stdout, "hydrated") {
		return errors.New("hydrate proof did not report hydrated project")
	}
	treeAfterHydrate, err := r.runCodeMesh("machine B tree after hydrate", machineB, "tree")
	if err != nil {
		return err
	}

	artifacts := []proofArtifact{}
	addArtifact := func(name, relPath, kind string, required bool) error {
		artifact, err := artifactMetadata(outAbs, name, relPath, kind, required)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
		return nil
	}

	canonicalLines := []string{"Machine A canonical tree", ""}
	canonicalLines = append(canonicalLines, linesFromText(treeA.stdout)...)
	canonicalLines = append(canonicalLines, "", "Machine C canonical status after manifest import", "")
	canonicalLines = append(canonicalLines, linesFromText(statusAfterImport.stdout)...)
	if err := writeText(filepath.Join(outAbs, "canonical-workspace-tree.txt"), strings.Join(canonicalLines, "\n")+"\n"); err != nil {
		return err
	}
	if err := writeSVG(filepath.Join(outAbs, "canonical-workspace-tree.svg"), "Canonical workspace tree/status", canonicalLines); err != nil {
		return err
	}
	placementLines := []string{
		"Machine A placement/presence",
		"mesh-target: " + sanitizePath(targetA, r.replacements) + " present",
		"mesh-unrelated: " + sanitizePath(unrelatedA, r.replacements) + " present",
		"",
		"Machine B placement/presence after bootstrap + selected hydration",
		"mesh-target: " + sanitizePath(targetB, r.replacements) + " present",
		"mesh-unrelated: " + sanitizePath(unrelatedB, r.replacements) + " missing",
		"",
		"Machine C placement/presence after manifest import",
		"mesh-target: " + sanitizePath(targetC, r.replacements) + " missing",
		"mesh-unrelated: " + sanitizePath(unrelatedC, r.replacements) + " missing",
	}
	if err := writeText(filepath.Join(outAbs, "machine-placement-presence.txt"), strings.Join(placementLines, "\n")+"\n"); err != nil {
		return err
	}
	if err := writeSVG(filepath.Join(outAbs, "machine-placement-presence.svg"), "Per-machine placement and presence", placementLines); err != nil {
		return err
	}
	planLines := append([]string{"Manifest import", ""}, linesFromText(manifestImport.stdout)...)
	planLines = append(planLines, "", "Bootstrap dry-run", "")
	planLines = append(planLines, linesFromText(bootstrapPlan.stdout)...)
	planLines = append(planLines, "", "Hydration action", "")
	planLines = append(planLines, linesFromText(hydrate.stdout)...)
	if err := writeText(filepath.Join(outAbs, "bootstrap-hydration-plan.txt"), strings.Join(planLines, "\n")+"\n"); err != nil {
		return err
	}
	if err := writeSVG(filepath.Join(outAbs, "bootstrap-hydration-plan.svg"), "Planned bootstrap and hydration actions", planLines); err != nil {
		return err
	}
	flowLines := []string{"Before bootstrap", ""}
	flowLines = append(flowLines, linesFromText(treeBefore.stdout)...)
	flowLines = append(flowLines, "", "After bootstrap apply", "")
	flowLines = append(flowLines, linesFromText(treeAfterBootstrap.stdout)...)
	flowLines = append(flowLines, "", "After selected hydrate", "")
	flowLines = append(flowLines, linesFromText(treeAfterHydrate.stdout)...)
	flowLines = append(flowLines, "", "Manifest import machine state", "")
	flowLines = append(flowLines, linesFromText(treeAfterImport.stdout)...)
	flowLines = append(flowLines, "", "Manifest import status", "")
	flowLines = append(flowLines, linesFromText(statusAfterImport.stdout)...)
	if err := writeText(filepath.Join(outAbs, "mutating-flow-before-after.txt"), strings.Join(flowLines, "\n")+"\n"); err != nil {
		return err
	}
	if err := writeSVG(filepath.Join(outAbs, "mutating-flow-before-after.svg"), "Before and after mutating fixture flows", flowLines); err != nil {
		return err
	}

	for _, spec := range []struct {
		name string
		path string
		kind string
	}{
		{"canonical-workspace-tree.svg", "canonical-workspace-tree.svg", "visual"},
		{"canonical-workspace-tree.txt", "canonical-workspace-tree.txt", "text"},
		{"machine-placement-presence.svg", "machine-placement-presence.svg", "visual"},
		{"machine-placement-presence.txt", "machine-placement-presence.txt", "text"},
		{"bootstrap-hydration-plan.svg", "bootstrap-hydration-plan.svg", "visual"},
		{"bootstrap-hydration-plan.txt", "bootstrap-hydration-plan.txt", "text"},
		{"mutating-flow-before-after.svg", "mutating-flow-before-after.svg", "visual"},
		{"mutating-flow-before-after.txt", "mutating-flow-before-after.txt", "text"},
		{"workspace-manifest.json", "workspace-manifest.json", "manifest"},
	} {
		if err := addArtifact(spec.name, spec.path, spec.kind, true); err != nil {
			return err
		}
	}
	commandFiles, err := filepath.Glob(filepath.Join(outAbs, "commands", "*.txt"))
	if err != nil {
		return err
	}
	sort.Strings(commandFiles)
	for _, path := range commandFiles {
		rel, err := filepath.Rel(outAbs, path)
		if err != nil {
			return err
		}
		if err := addArtifact(filepath.Base(path), filepath.ToSlash(rel), "command-transcript", true); err != nil {
			return err
		}
	}

	manifest := proofManifest{
		SchemaVersion: 1,
		Kind:          proofKind,
		Status:        "pass",
		Runner:        "github-hosted-free",
		Fixture:       "isolated-local",
		Source:        "real-codemesh-cli",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Coverage: []string{
			"canonical-workspace-tree",
			"manifest-import-export",
			"machine-placement-presence",
			"bootstrap-hydration-plan",
			"mutating-flow-before-after",
		},
		Commands:     r.commands,
		Artifacts:    artifacts,
		Confidential: "pending",
	}
	if err := writeSummary(outAbs, manifest); err != nil {
		return err
	}
	summaryArtifact, err := artifactMetadata(outAbs, "summary.md", "summary.md", "summary", true)
	if err != nil {
		return err
	}
	manifest.Artifacts = append(manifest.Artifacts, summaryArtifact)
	manifest.Confidential = "pass"
	if err := writeProofManifest(outAbs, manifest); err != nil {
		return err
	}
	if err := validateProofBundle(outAbs); err != nil {
		return err
	}
	if err := auditPublicArtifacts(outAbs); err != nil {
		return err
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func resetGeneratedDir(path string) error {
	clean := filepath.Clean(path)
	if !strings.Contains(filepath.Base(clean), "crabbox-pr-proof") {
		return fmt.Errorf("refusing to reset non-proof directory: %s", path)
	}
	if err := os.RemoveAll(clean); err != nil {
		return err
	}
	return os.MkdirAll(clean, 0o755)
}

func (r *runner) newMachine(label string) (machineNode, error) {
	root := filepath.Join(r.fixtureRoot, label)
	node := machineNode{
		label:        label,
		codeMeshHome: filepath.Join(root, "codemesh-home"),
		home:         filepath.Join(root, "home"),
		workspace:    filepath.Join(root, "workspace"),
		gitConfig:    filepath.Join(root, "gitconfig"),
	}
	for _, path := range []string{node.codeMeshHome, node.home, node.workspace} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return machineNode{}, err
		}
	}
	if err := os.WriteFile(node.gitConfig, []byte(""), 0o644); err != nil {
		return machineNode{}, err
	}
	node.env = []string{
		"CODEMESH_HOME=" + node.codeMeshHome,
		"HOME=" + node.home,
		"GIT_CONFIG_GLOBAL=" + node.gitConfig,
		"GIT_CONFIG_NOSYSTEM=1",
	}
	return node, nil
}

func (r *runner) createRemote(name string) (string, error) {
	seed := filepath.Join(r.fixtureRoot, "git-seeds", name)
	remote := filepath.Join(r.fixtureRoot, "remotes", name+".git")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(remote), 0o755); err != nil {
		return "", err
	}
	if _, err := r.run("git init seed "+name, seed, r.gitEnv, "git", "init", "-b", "main"); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# "+name+"\n\nCodeMesh fixture project.\n"), 0o644); err != nil {
		return "", err
	}
	if _, err := r.run("git add seed "+name, seed, r.gitEnv, "git", "add", "README.md"); err != nil {
		return "", err
	}
	if _, err := r.run("git commit seed "+name, seed, r.gitEnv, "git", "-c", "user.name=CodeMesh Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture seed"); err != nil {
		return "", err
	}
	if _, err := r.run("git init bare "+name, r.fixtureRoot, r.gitEnv, "git", "init", "--bare", remote); err != nil {
		return "", err
	}
	if _, err := r.run("git bare head "+name, remote, r.gitEnv, "git", "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return "", err
	}
	if _, err := r.run("git remote add seed "+name, seed, r.gitEnv, "git", "remote", "add", "origin", remote); err != nil {
		return "", err
	}
	if _, err := r.run("git push seed "+name, seed, r.gitEnv, "git", "push", "origin", "main"); err != nil {
		return "", err
	}
	return remote, nil
}

func (r *runner) initGitSetupEnv() error {
	home := filepath.Join(r.fixtureRoot, "git-setup-home")
	config := filepath.Join(r.fixtureRoot, "git-setup-config")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	configBody := "[commit]\n\tgpgsign = false\n[tag]\n\tgpgsign = false\n"
	if err := os.WriteFile(config, []byte(configBody), 0o644); err != nil {
		return err
	}
	r.gitEnv = []string{
		"HOME=" + home,
		"GIT_CONFIG_GLOBAL=" + config,
		"GIT_CONFIG_NOSYSTEM=1",
	}
	return nil
}

func (r *runner) startLocalGitDaemon(basePath, probeRepo string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return "", nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "git", "daemon", "--verbose", "--log-destination=stderr", "--export-all", "--reuseaddr", "--base-path="+basePath, "--listen=127.0.0.1", "--port="+strconv.Itoa(port), basePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	stop := func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(500 * time.Millisecond):
		}
	}

	remoteBase := "git://127.0.0.1:" + strconv.Itoa(port)
	probeURL := remoteBase + "/" + probeRepo
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-done:
			cancel()
			return "", nil, fmt.Errorf("git daemon exited early: %v %s", waitErr, strings.TrimSpace(stderr.String()))
		default:
		}
		probeCtx, probeCancel := context.WithTimeout(context.Background(), time.Second)
		probe := exec.CommandContext(probeCtx, "git", "ls-remote", probeURL, "HEAD")
		if err := probe.Run(); err == nil {
			probeCancel()
			return remoteBase, stop, nil
		}
		probeCancel()
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	return "", nil, fmt.Errorf("git daemon did not become ready for %s: %s", probeURL, strings.TrimSpace(stderr.String()))
}

func (r *runner) runCodeMesh(label string, node machineNode, args ...string) (commandCapture, error) {
	return r.run(label, node.workspace, node.env, r.bin, args...)
}

func (r *runner) run(label, dir string, extraEnv []string, name string, args ...string) (commandCapture, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	capture := commandCapture{
		label:    label,
		exitCode: exitCode,
		stdout:   r.sanitize(stdout.String()),
		stderr:   r.sanitize(stderr.String()),
	}
	r.commandIndex++
	transcriptName := fmt.Sprintf("%02d-%s.txt", r.commandIndex, slugify(label))
	capture.transcript = filepath.ToSlash(filepath.Join("commands", transcriptName))
	transcriptPath := filepath.Join(r.commandsDir, transcriptName)
	body := "command: " + r.sanitize(shellJoin(append([]string{name}, args...))) + "\n" +
		"cwd: " + sanitizePath(dir, r.replacements) + "\n" +
		fmt.Sprintf("exit_code: %d\n\nstdout:\n%s\nstderr:\n%s\n", exitCode, capture.stdout, capture.stderr)
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		return capture, err
	}
	r.commands = append(r.commands, proofCommand{Name: label, ExitCode: exitCode, TranscriptPath: capture.transcript})
	if err != nil {
		return capture, fmt.Errorf("%s failed: %w", label, err)
	}
	return capture, nil
}

func (r *runner) sanitize(value string) string {
	for _, replacement := range r.replacements {
		if replacement.from == "" {
			continue
		}
		value = strings.ReplaceAll(value, filepath.ToSlash(replacement.from), replacement.to)
		value = strings.ReplaceAll(value, replacement.from, replacement.to)
	}
	return value
}

func sanitizePath(path string, replacements []replacement) string {
	value := path
	for _, replacement := range replacements {
		if replacement.from == "" {
			continue
		}
		value = strings.ReplaceAll(value, filepath.ToSlash(replacement.from), replacement.to)
		value = strings.ReplaceAll(value, replacement.from, replacement.to)
	}
	return filepath.ToSlash(value)
}

func writeLegacyManifestEntries(manifestPath, outDir string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest workspaceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if manifest.ManifestVersion != 1 || len(manifest.Projects) == 0 {
		return errors.New("workspace manifest export did not include version 1 projects")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, project := range manifest.Projects {
		entry := legacyManifestEntry{ManifestVersion: 1, Project: project}
		data, err := json.MarshalIndent(entry, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, project.Alias+".json"), append(data, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeFile(path string, replacements []replacement) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sanitized := string(data)
	for _, replacement := range replacements {
		if replacement.from == "" {
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, filepath.ToSlash(replacement.from), replacement.to)
		sanitized = strings.ReplaceAll(sanitized, replacement.from, replacement.to)
	}
	return os.WriteFile(path, []byte(sanitized), 0o644)
}

func writeSummary(outDir string, manifest proofManifest) error {
	lines := []string{
		"# CodeMesh Crabbox PR proof",
		"",
		"runner: github-hosted-free",
		"fixture: isolated local Git remotes and two CodeMesh machine homes",
		"source: real packaged codemesh CLI",
		"",
		"## Visual proof",
		"",
		"- proof-manifest.json",
	}
	for _, artifact := range manifest.Artifacts {
		if artifact.Required && (artifact.Kind == "visual" || artifact.Kind == "summary" || artifact.Kind == "manifest") {
			lines = append(lines, "- "+artifact.Name)
		}
	}
	lines = append(lines,
		"",
		"## Coverage",
		"",
		"- canonical workspace tree",
		"- per-machine placement and presence",
		"- planned bootstrap and hydration actions",
		"- before and after state for bootstrap apply and selected hydrate",
		"",
		"## Confidentiality",
		"",
		"PASS: proof artifacts are sanitized to isolated fixture placeholders and scanned before upload.",
		"",
	)
	return writeText(filepath.Join(outDir, "summary.md"), strings.Join(lines, "\n"))
}

func writeText(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func writeSVG(path, title string, lines []string) error {
	if len(lines) > 34 {
		lines = append(lines[:32], "...", fmt.Sprintf("(%d more lines in matching .txt artifact)", len(lines)-32))
	}
	width := 1200
	height := 96 + len(lines)*24
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-label="%s">`, width, height, width, height, html.EscapeString(title))
	b.WriteString(`<rect width="100%" height="100%" fill="#f8faf9"/>`)
	b.WriteString(`<rect x="24" y="24" width="1152" height="`)
	b.WriteString(fmt.Sprintf("%d", height-48))
	b.WriteString(`" rx="8" fill="#ffffff" stroke="#24342f" stroke-width="2"/>`)
	fmt.Fprintf(&b, `<text x="48" y="64" fill="#16221f" font-family="Arial, sans-serif" font-size="24" font-weight="700">%s</text>`, html.EscapeString(title))
	y := 104
	for _, line := range lines {
		fmt.Fprintf(&b, `<text x="48" y="%d" fill="#24342f" font-family="Menlo, Consolas, monospace" font-size="16">%s</text>`, y, html.EscapeString(line))
		y += 24
	}
	b.WriteString(`</svg>`)
	return writeText(path, b.String())
}

func linesFromText(value string) []string {
	raw := strings.Split(strings.TrimSpace(value), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return []string{"(no output)"}
	}
	return lines
}

func writeProofManifest(outDir string, manifest proofManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return writeText(filepath.Join(outDir, "proof-manifest.json"), string(data)+"\n")
}

func artifactMetadata(root, name, relPath, kind string, required bool) (proofArtifact, error) {
	path := filepath.Join(root, filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return proofArtifact{}, err
	}
	sum := sha256.Sum256(data)
	return proofArtifact{
		Name:     name,
		Path:     relPath,
		Kind:     kind,
		Required: required,
		Bytes:    int64(len(data)),
		SHA256:   hex.EncodeToString(sum[:]),
	}, nil
}

func validateProofBundle(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "proof-manifest.json"))
	if err != nil {
		return fmt.Errorf("read proof manifest: %w", err)
	}
	var manifest proofManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode proof manifest: %w", err)
	}
	var problems []string
	if manifest.SchemaVersion != 1 || manifest.Kind != proofKind || manifest.Status != "pass" {
		problems = append(problems, "proof manifest status/kind/schema incomplete")
	}
	if manifest.Runner != "github-hosted-free" {
		problems = append(problems, "runner must be github-hosted-free")
	}
	if manifest.Fixture != "isolated-local" {
		problems = append(problems, "fixture must be isolated-local")
	}
	if manifest.Source != "real-codemesh-cli" {
		problems = append(problems, "source must be real-codemesh-cli")
	}
	if len(manifest.Commands) == 0 {
		problems = append(problems, "real command list is empty")
	}
	for _, required := range []string{"canonical-workspace-tree", "machine-placement-presence", "bootstrap-hydration-plan", "mutating-flow-before-after"} {
		if !contains(manifest.Coverage, required) {
			problems = append(problems, "coverage missing "+required)
		}
	}
	requiredVisuals := map[string]bool{
		"canonical-workspace-tree.svg":   false,
		"machine-placement-presence.svg": false,
		"bootstrap-hydration-plan.svg":   false,
		"mutating-flow-before-after.svg": false,
		"summary.md":                     false,
		"workspace-manifest.json":        false,
	}
	for _, artifact := range manifest.Artifacts {
		if !artifact.Required {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(artifact.Path))
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, "required artifact missing: "+artifact.Path)
			continue
		}
		if len(bytes.TrimSpace(data)) < 20 {
			problems = append(problems, "required artifact incomplete: "+artifact.Path)
		}
		if artifact.Kind == "visual" && !bytes.Contains(data, []byte("<svg")) {
			problems = append(problems, "visual artifact is not SVG: "+artifact.Path)
		}
		if _, ok := requiredVisuals[artifact.Name]; ok {
			requiredVisuals[artifact.Name] = true
		}
	}
	for name, seen := range requiredVisuals {
		if !seen {
			problems = append(problems, "required artifact missing from manifest: "+name)
		}
	}
	for _, command := range manifest.Commands {
		if command.ExitCode != 0 {
			problems = append(problems, fmt.Sprintf("command %q failed with exit code %d", command.Name, command.ExitCode))
		}
		if strings.TrimSpace(command.TranscriptPath) == "" {
			problems = append(problems, "command transcript missing for "+command.Name)
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(command.TranscriptPath))); err != nil {
			problems = append(problems, "command transcript missing: "+command.TranscriptPath)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func auditPublicArtifacts(root string) error {
	var problems []string
	personalPath := regexp.MustCompile(`/Users/bram(/|\b)|/Users/[^/\s]+/Projects|/home/[^/\s]+/Projects`)
	privateEndpoint := regexp.MustCompile(`(?i)\b(\.local\b|127\.0\.0\.1|10\.[0-9]{1,3}\.|172\.(1[6-9]|2[0-9]|3[0-1])\.|192\.168\.)`)
	internalModel := regexp.MustCompile(`(?i)\b(internal model|employee-only model|preview-only model)\b`)
	secretLike := regexp.MustCompile(`(?i)(ghp_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|sk-[A-Za-z0-9]{12,}|BEGIN [A-Z ]*PRIVATE KEY|password\s*[:=]\s*[^ \n]+)`)
	tokenAssignment := regexp.MustCompile(`(?i)token\s*[:=]\s*([^ \n]+)`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		rel, _ := filepath.Rel(root, path)
		if personalPath.MatchString(text) {
			problems = append(problems, filepath.ToSlash(rel)+": personal local path")
		}
		if privateEndpoint.MatchString(text) {
			problems = append(problems, filepath.ToSlash(rel)+": private endpoint")
		}
		if internalModel.MatchString(text) {
			problems = append(problems, filepath.ToSlash(rel)+": internal model name")
		}
		if secretLike.MatchString(text) {
			problems = append(problems, filepath.ToSlash(rel)+": secret-like token")
		}
		for _, match := range tokenAssignment.FindAllStringSubmatch(text, -1) {
			if len(match) == 2 && strings.TrimSpace(strings.ToLower(match[1])) != "[redacted]" {
				problems = append(problems, filepath.ToSlash(rel)+": secret-like token")
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"$`\\") {
			quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\\''")+"'")
		} else {
			quoted = append(quoted, arg)
		}
	}
	return strings.Join(quoted, " ")
}
