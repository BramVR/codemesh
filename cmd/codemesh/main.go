package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BramVR/codemesh/internal/agentcontract"
	"github.com/BramVR/codemesh/internal/agentprep"
	"github.com/BramVR/codemesh/internal/agentruns"
	"github.com/BramVR/codemesh/internal/bootstrap"
	"github.com/BramVR/codemesh/internal/clonestrategy"
	"github.com/BramVR/codemesh/internal/commandresult"
	"github.com/BramVR/codemesh/internal/config"
	"github.com/BramVR/codemesh/internal/envbinding"
	"github.com/BramVR/codemesh/internal/machineregistry"
	"github.com/BramVR/codemesh/internal/presentation"
	"github.com/BramVR/codemesh/internal/readiness"
	"github.com/BramVR/codemesh/internal/reconciliation"
	"github.com/BramVR/codemesh/internal/registry"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/toolchain"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
	"github.com/BramVR/codemesh/internal/workspacetarget"
)

const version = "0.1.0"

const statusReadinessTimeout = 30 * time.Second
const hydrateTimeout = 10 * time.Minute
const agentRunTimeout = 10 * time.Minute

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "--version", "version":
		fmt.Fprintf(stdout, "codemesh %s\n", version)
		return 0
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "add":
		return runAdd(args[1:], stdout, stderr)
	case "scan":
		return runScan(args[1:], stdout, stderr)
	case "tree":
		return runTree(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "hydrate":
		return runHydrate(args[1:], stdout, stderr)
	case "bootstrap":
		return runBootstrap(args[1:], stdout, stderr)
	case "manifest":
		return runManifest(args[1:], stdout, stderr)
	case "target":
		return runTarget(args[1:], stdout, stderr)
	case "env":
		return runEnv(args[1:], stdout, stderr)
	case "machine":
		return runMachine(args[1:], stdout, stderr)
	case "agent":
		return runAgent(args[1:], stdout, stderr)
	case "runs":
		return runRuns(args[1:], stdout, stderr)
	case "clean":
		return runClean(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func runManifest(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printManifestHelp(stdout)
		return 0
	}
	switch args[0] {
	case "export":
		return runManifestExport(args[1:], stdout, stderr)
	case "import":
		return runManifestImport(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown manifest command: %s\n\n", args[0])
		printManifestHelp(stderr)
		return 2
	}
}

func runManifestExport(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printManifestExportHelp(stdout)
		return 0
	}
	outputPath, ok := parseManifestExportArgs(args, stderr)
	if !ok {
		printManifestExportHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	machine, err := currentMachine(context.Background(), store)
	if err != nil {
		fmt.Fprintf(stderr, "export workspace manifest: %v\n", err)
		return 1
	}
	projects, err := store.ListProjects(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	manifest, err := workspacemanifest.ExportWorkspace(projects, machine.WorkspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "export workspace manifest: %v\n", err)
		return 1
	}
	data, err := workspacemanifest.EncodeWorkspace(manifest)
	if err != nil {
		fmt.Fprintf(stderr, "encode workspace manifest: %v\n", err)
		return 1
	}
	if outputPath == "" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "write workspace manifest: %v\n", err)
			return 1
		}
		return 0
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fmt.Fprintf(stderr, "create manifest output parent: %v\n", err)
		return 1
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "write workspace manifest: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "manifest exported\npath: %s\nprojects: %d\n", outputPath, len(manifest.Projects))
	return 0
}

func runManifestImport(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printManifestImportHelp(stdout)
		return 0
	}
	manifestPath, ok := parseManifestImportArgs(args, stderr)
	if !ok {
		printManifestImportHelp(stderr)
		return 2
	}
	manifest, err := workspacemanifest.LoadWorkspace(manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "read workspace manifest: %v\n", err)
		return 1
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	machine, err := currentMachine(context.Background(), store)
	if err != nil {
		fmt.Fprintf(stderr, "import workspace manifest: %v\n", err)
		return 1
	}
	result, err := workspacemanifest.ApplyImport(context.Background(), store, manifest, machine.WorkspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "import workspace manifest: %v\n", err)
		return 1
	}
	renderManifestImportHuman(stdout, machine.WorkspaceRoot, manifest, result)
	return 0
}

func parseManifestExportArgs(args []string, stderr io.Writer) (string, bool) {
	var outputPath string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output":
			if i+1 >= len(args) || strings.TrimSpace(args[i+1]) == "" || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "manifest export --output requires a path\n\n")
				return "", false
			}
			outputPath = args[i+1]
			i++
		default:
			fmt.Fprintf(stderr, "unknown manifest export argument: %s\n\n", args[i])
			return "", false
		}
	}
	return outputPath, true
}

func parseManifestImportArgs(args []string, stderr io.Writer) (string, bool) {
	if len(args) != 1 {
		fmt.Fprint(stderr, "manifest import requires exactly one manifest path\n\n")
		return "", false
	}
	return args[0], true
}

func renderManifestImportHuman(stdout io.Writer, workspaceRoot string, manifest workspacemanifest.WorkspaceManifest, result workspacemanifest.ApplyImportResult) {
	fmt.Fprintf(stdout, "manifest imported\nworkspace_root: %s\nprojects: %d\n", workspaceRoot, len(manifest.Projects))
	renderManifestImportProjects(stdout, "added", result.AddedProjects)
	renderManifestImportProjects(stdout, "updated", result.UpdatedProjects)
	unchanged := make([]state.Project, 0)
	for _, change := range result.Plan.Changes {
		if change.Action == workspacemanifest.ChangeUnchanged {
			unchanged = append(unchanged, state.Project{Alias: change.Alias, LocalPath: change.LocalPath})
		}
	}
	renderManifestImportProjects(stdout, "unchanged", unchanged)
}

func renderManifestImportProjects(stdout io.Writer, label string, projects []state.Project) {
	if len(projects) == 0 {
		fmt.Fprintf(stdout, "%s: none\n", label)
		return
	}
	for _, project := range projects {
		fmt.Fprintf(stdout, "%s: %s %s\n", label, project.Alias, project.LocalPath)
	}
}

func runTarget(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printTargetHelp(stdout)
		return 0
	}
	switch args[0] {
	case "export":
		return runTargetExport(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown target command: %s\n\n", args[0])
		printTargetHelp(stderr)
		return 2
	}
}

func runTargetExport(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printTargetExportHelp(stdout)
		return 0
	}
	parsed, ok := parseTargetExportArgs(args, stderr)
	if !ok {
		printTargetExportHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	spec, err := workspacetarget.Export(context.Background(), store, workspacetarget.Options{
		ProducerVersion: version,
		TargetName:      parsed.TargetName,
		TargetKind:      parsed.Kind,
		WorkspaceRoot:   parsed.WorkspaceRoot,
		Scopes:          parsed.Scopes,
	})
	if err != nil {
		fmt.Fprintf(stderr, "export workspace target: %v\n", err)
		return 1
	}
	result := commandresult.New("target export", commandresult.ExitSuccess, commandresult.Diagnostics{}, spec)
	if parsed.JSON {
		if err := presentation.RenderJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "encode target export result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return 0
	}
	if err := presentation.RenderHuman(stdout, result, renderTargetExportPayloadHuman); err != nil {
		fmt.Fprintf(stderr, "render target export result: %v\n", err)
		return commandresult.ExitInternalError.Code()
	}
	return 0
}

type parsedTargetExportArgs struct {
	TargetName    string
	Kind          string
	WorkspaceRoot string
	Scopes        []string
	JSON          bool
}

func parseTargetExportArgs(args []string, stderr io.Writer) (parsedTargetExportArgs, bool) {
	var names []string
	parsed := parsedTargetExportArgs{Kind: "agent"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--kind":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "target export --kind requires a kind\n\n")
				return parsedTargetExportArgs{}, false
			}
			parsed.Kind = args[i+1]
			i++
		case "--scope":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "target export --scope requires a scope\n\n")
				return parsedTargetExportArgs{}, false
			}
			parsed.Scopes = append(parsed.Scopes, args[i+1])
			i++
		case "--workspace-root":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "target export --workspace-root requires a path\n\n")
				return parsedTargetExportArgs{}, false
			}
			parsed.WorkspaceRoot = args[i+1]
			i++
		case "--json":
			parsed.JSON = true
		default:
			names = append(names, args[i])
		}
	}
	if len(names) != 1 {
		fmt.Fprint(stderr, "target export requires exactly one target name\n\n")
		return parsedTargetExportArgs{}, false
	}
	if len(parsed.Scopes) == 0 {
		fmt.Fprint(stderr, "target export requires at least one --scope\n\n")
		return parsedTargetExportArgs{}, false
	}
	parsed.TargetName = names[0]
	return parsed, true
}

func renderTargetExportPayloadHuman(w io.Writer, payload workspacetarget.Spec) error {
	fmt.Fprintf(w, "workspace target export\n")
	fmt.Fprintf(w, "target: %s\n", payload.Target.Name)
	fmt.Fprintf(w, "kind: %s\n", payload.Target.Kind)
	fmt.Fprintf(w, "workspace_root: %s\n", payload.Target.WorkspaceRoot)
	fmt.Fprintf(w, "scopes: %s\n", strings.Join(payload.Target.Scopes, ","))
	fmt.Fprintf(w, "machine: %s %s/%s\n", payload.Machine.ID, payload.Machine.OS, payload.Machine.Architecture)
	fmt.Fprintf(w, "projects: %d\n", len(payload.Topology))
	fmt.Fprintf(w, "env_policy: %d\n", len(payload.EnvPolicy))
	return nil
}

func runEnv(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printEnvHelp(stdout)
		return 0
	}
	switch args[0] {
	case "bind":
		return runEnvBind(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown env command: %s\n\n", args[0])
		printEnvHelp(stderr)
		return 2
	}
}

func runEnvBind(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printEnvBindHelp(stdout)
		return 0
	}
	parsed, ok := parseEnvBindArgs(args, stderr)
	if !ok {
		printEnvBindHelp(stderr)
		return 2
	}
	if parsed.Provider != envbinding.ProviderFake {
		fmt.Fprintf(stderr, "env bind --provider supports %q in this slice\n\n", envbinding.ProviderFake)
		printEnvBindHelp(stderr)
		return 2
	}
	if err := envbinding.ValidateFakeReference(parsed.SecretRef); err != nil {
		fmt.Fprintf(stderr, "env bind --ref: %v\n\n", err)
		printEnvBindHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	project, ok, err := lookupProject(context.Background(), store, parsed.Project)
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintf(stderr, "unknown project: %s\n", parsed.Project)
		return 1
	}
	binding, err := store.UpsertEnvBinding(context.Background(), state.EnvBinding{
		ProjectID:   project.ID,
		Requirement: parsed.Requirement,
		Provider:    parsed.Provider,
		SecretRef:   parsed.SecretRef,
		Scopes:      parsed.Scopes,
	})
	if err != nil {
		fmt.Fprintf(stderr, "bind env requirement: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "bound env requirement: %s\nproject: %s\nprovider: %s\nscopes: %s\n", binding.Requirement, project.Alias, binding.Provider, strings.Join(binding.Scopes, ","))
	return 0
}

type parsedEnvBindArgs struct {
	Project     string
	Requirement string
	Provider    string
	SecretRef   string
	Scopes      []string
}

func parseEnvBindArgs(args []string, stderr io.Writer) (parsedEnvBindArgs, bool) {
	var positional []string
	parsed := parsedEnvBindArgs{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "env bind --provider requires a provider\n\n")
				return parsedEnvBindArgs{}, false
			}
			parsed.Provider = args[i+1]
			i++
		case "--ref":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "env bind --ref requires a secret reference\n\n")
				return parsedEnvBindArgs{}, false
			}
			parsed.SecretRef = args[i+1]
			i++
		case "--scope":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "env bind --scope requires a scope\n\n")
				return parsedEnvBindArgs{}, false
			}
			parsed.Scopes = append(parsed.Scopes, args[i+1])
			i++
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 2 {
		fmt.Fprint(stderr, "env bind requires exactly one project and one requirement\n\n")
		return parsedEnvBindArgs{}, false
	}
	parsed.Project = positional[0]
	parsed.Requirement = positional[1]
	if strings.TrimSpace(parsed.Provider) == "" {
		fmt.Fprint(stderr, "env bind --provider is required\n\n")
		return parsedEnvBindArgs{}, false
	}
	if strings.TrimSpace(parsed.SecretRef) == "" {
		fmt.Fprint(stderr, "env bind --ref is required\n\n")
		return parsedEnvBindArgs{}, false
	}
	if len(parsed.Scopes) == 0 {
		fmt.Fprint(stderr, "env bind --scope is required\n\n")
		return parsedEnvBindArgs{}, false
	}
	return parsed, true
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printDoctorHelp(stdout)
		return 0
	}
	doctorArgs, ok := parseDoctorArgs(args, stderr)
	if !ok {
		printDoctorHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	projects, err := store.ListProjects(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	for _, project := range projects {
		if project.Alias != doctorArgs.ProjectName {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), hydrateTimeout)
		defer cancel()
		decision, err := readiness.EvaluateHandoff(ctx, project, readiness.Options{BaseBranch: doctorArgs.Base, Toolchain: toolchain.HostDetector{}})
		if err != nil {
			fmt.Fprintf(stderr, "check handoff readiness: %v\n", err)
			return 1
		}
		result := newDoctorResult(doctorArgs, decision)
		if doctorArgs.JSON {
			if err := presentation.RenderJSON(stdout, result); err != nil {
				fmt.Fprintf(stderr, "encode doctor result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return doctorExitCode(result)
		}
		if err := presentation.RenderHuman(stdout, result, renderDoctorPayloadHuman); err != nil {
			fmt.Fprintf(stderr, "render doctor result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return doctorExitCode(result)
	}
	fmt.Fprintf(stderr, "unknown project: %s\n", doctorArgs.ProjectName)
	return 1
}

type parsedDoctorArgs struct {
	ProjectName string
	Base        string
	Strict      bool
	JSON        bool
}

func parseDoctorArgs(args []string, stderr io.Writer) (parsedDoctorArgs, bool) {
	var base string
	var projects []string
	var strict bool
	var jsonOutput bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "doctor --base requires a branch\n\n")
				return parsedDoctorArgs{}, false
			}
			base = args[i+1]
			i++
		case "--strict":
			strict = true
		case "--json":
			jsonOutput = true
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) != 1 {
		fmt.Fprint(stderr, "doctor requires exactly one project\n\n")
		return parsedDoctorArgs{}, false
	}
	return parsedDoctorArgs{ProjectName: projects[0], Base: base, Strict: strict, JSON: jsonOutput}, true
}

type doctorPayload struct {
	Project           string                    `json:"project"`
	Handoff           string                    `json:"handoff"`
	Strict            bool                      `json:"strict"`
	State             string                    `json:"state"`
	Path              string                    `json:"path"`
	PathPresent       bool                      `json:"path_present"`
	Remote            string                    `json:"remote"`
	Base              string                    `json:"base"`
	SourcePathMissing bool                      `json:"source_path_missing"`
	Toolchain         []toolchain.Result        `json:"toolchain"`
	Diagnostics       commandresult.Diagnostics `json:"diagnostics"`
}

func newDoctorResult(args parsedDoctorArgs, decision readiness.HandoffDecision) commandresult.Result[doctorPayload] {
	diagnostics := statusDiagnostics(decision.Report)
	handoff := "green"
	if len(diagnostics.Blockers) != 0 {
		handoff = "blocked"
	} else if len(diagnostics.Warnings) != 0 {
		handoff = "warning"
	}
	return commandresult.New("doctor", commandresult.ReadinessExitClass(len(diagnostics.Warnings), len(diagnostics.Blockers)), commandresult.Diagnostics{}, doctorPayload{
		Project:           decision.Report.Project.Alias,
		Handoff:           handoff,
		Strict:            args.Strict,
		State:             string(decision.Report.State),
		Path:              decision.Report.Project.LocalPath,
		PathPresent:       decision.Report.LocalPathPresent,
		Remote:            decision.Report.Project.NormalizedRemote,
		Base:              decision.Report.BaseBranch,
		SourcePathMissing: decision.SourcePathMissing,
		Toolchain:         toolchainResults(decision.Report.Toolchain),
		Diagnostics:       diagnostics,
	})
}

func renderDoctorPayloadHuman(w io.Writer, payload doctorPayload) error {
	fmt.Fprintf(w, "handoff: %s\n", payload.Handoff)
	fmt.Fprintf(w, "project: %s\n", payload.Project)
	fmt.Fprintf(w, "state: %s\n", payload.State)
	fmt.Fprintf(w, "path: %s\n", payload.Path)
	fmt.Fprintf(w, "path_present: %t\n", payload.PathPresent)
	fmt.Fprintf(w, "remote: %s\n", payload.Remote)
	fmt.Fprintf(w, "base: %s\n", payload.Base)
	fmt.Fprintf(w, "source_path_missing: %t\n", payload.SourcePathMissing)
	if payload.Strict {
		fmt.Fprintln(w, "strict: true")
	}
	for _, item := range payload.Toolchain {
		fmt.Fprintf(w, "toolchain: %s %s\n", item.Name, item.Status)
	}
	printCommandDiagnostics(w, payload.Diagnostics)
	return nil
}

func toolchainResults(results []toolchain.Result) []toolchain.Result {
	if results == nil {
		return []toolchain.Result{}
	}
	return append([]toolchain.Result(nil), results...)
}

func doctorExitCode(result commandresult.Result[doctorPayload]) int {
	switch result.ExitClass {
	case commandresult.ExitSuccess:
		return 0
	case commandresult.ExitReadinessWarning:
		if result.Payload.Strict {
			return 1
		}
		return 0
	case commandresult.ExitReadinessBlocked:
		return 1
	default:
		return result.ExitClass.Code()
	}
}

func runMachine(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printMachineHelp(stdout)
		return 0
	}
	switch args[0] {
	case "register":
		return runMachineRegister(args[1:], stdout, stderr)
	case "status":
		return runMachineStatus(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown machine command: %s\n\n", args[0])
		printMachineHelp(stderr)
		return 2
	}
}

func runMachineRegister(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printMachineRegisterHelp(stdout)
		return 0
	}
	parsed, ok := parseMachineRegisterArgs(args, stderr)
	if !ok {
		printMachineRegisterHelp(stderr)
		return 2
	}
	workspaceRoot, err := config.ResolveWorkspaceRoot(parsed.WorkspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "resolve workspace root: %v\n", err)
		return 1
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	machine, err := machineregistry.Registry{Store: store}.Register(context.Background(), machineregistry.RegisterOptions{
		Name:          parsed.Name,
		CodeMeshHome:  paths.Home,
		WorkspaceRoot: workspaceRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "register machine: %v\n", err)
		return 1
	}
	if parsed.JSON {
		if err := json.NewEncoder(stdout).Encode(newMachineJSON(machine)); err != nil {
			fmt.Fprintf(stderr, "encode machine registration: %v\n", err)
			return 1
		}
		return 0
	}
	renderMachineHuman(stdout, "machine registered", machine)
	return 0
}

func runMachineStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printMachineStatusHelp(stdout)
		return 0
	}
	jsonOutput, ok := parseMachineStatusArgs(args, stderr)
	if !ok {
		printMachineStatusHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	machine, err := currentMachine(context.Background(), store)
	if err != nil {
		fmt.Fprintf(stderr, "read machine status: %v\n", err)
		return 1
	}
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(newMachineJSON(machine)); err != nil {
			fmt.Fprintf(stderr, "encode machine status: %v\n", err)
			return 1
		}
		return 0
	}
	renderMachineHuman(stdout, "machine status", machine)
	return 0
}

func currentMachine(ctx context.Context, store interface {
	ListMachines(context.Context) ([]state.Machine, error)
}) (state.Machine, error) {
	machines, err := store.ListMachines(ctx)
	if err != nil {
		return state.Machine{}, err
	}
	if len(machines) == 0 {
		return state.Machine{}, errors.New("machine is not registered")
	}
	return machines[0], nil
}

type machineJSON struct {
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

func newMachineJSON(machine state.Machine) machineJSON {
	return machineJSON{
		ID:            machine.ID,
		Name:          machine.Name,
		Hostname:      machine.Hostname,
		OS:            machine.OS,
		Architecture:  machine.Architecture,
		CodeMeshHome:  machine.CodeMeshHome,
		WorkspaceRoot: machine.WorkspaceRoot,
		RegisteredAt:  machine.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     machine.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func renderMachineHuman(stdout io.Writer, title string, machine state.Machine) {
	fmt.Fprintf(stdout, "%s\nid: %s\nname: %s\nhostname: %s\nos: %s\narchitecture: %s\ncodemesh_home: %s\nworkspace_root: %s\nregistered_at: %s\nupdated_at: %s\n",
		title,
		machine.ID,
		machine.Name,
		machine.Hostname,
		machine.OS,
		machine.Architecture,
		machine.CodeMeshHome,
		machine.WorkspaceRoot,
		machine.CreatedAt.UTC().Format(time.RFC3339),
		machine.UpdatedAt.UTC().Format(time.RFC3339),
	)
}

type parsedMachineRegisterArgs struct {
	WorkspaceRoot string
	Name          string
	JSON          bool
}

func parseMachineRegisterArgs(args []string, stderr io.Writer) (parsedMachineRegisterArgs, bool) {
	var workspaceRoots []string
	var parsed parsedMachineRegisterArgs
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			parsed.JSON = true
		case "--name":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "machine register --name requires a name\n\n")
				return parsedMachineRegisterArgs{}, false
			}
			parsed.Name = args[i+1]
			i++
		default:
			workspaceRoots = append(workspaceRoots, args[i])
		}
	}
	if len(workspaceRoots) > 1 {
		fmt.Fprint(stderr, "machine register accepts at most one workspace root\n\n")
		return parsedMachineRegisterArgs{}, false
	}
	if len(workspaceRoots) == 1 {
		parsed.WorkspaceRoot = workspaceRoots[0]
	}
	return parsed, true
}

func parseMachineStatusArgs(args []string, stderr io.Writer) (bool, bool) {
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(stderr, "unknown machine status argument: %s\n\n", arg)
			return false, false
		}
	}
	return jsonOutput, true
}

func runRuns(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printRunsHelp(stdout)
		return 0
	}
	if len(args) > 0 {
		fmt.Fprint(stderr, "runs accepts no arguments\n\n")
		printRunsHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	runs, err := agentruns.Manager{Store: store, AgentsDir: paths.AgentsDir}.List(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "list agent runs: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "agent runs:")
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "(empty)")
		return 0
	}
	for _, run := range runs {
		fmt.Fprintf(stdout, "- %s project=%s base=%s profile=%s state=%s created=%s workspace=%s\n", run.ID, run.ProjectAlias, run.Base, run.Profile, run.State, run.CreatedAt.UTC().Format(time.RFC3339), run.WorkspacePath)
	}
	return 0
}

func runClean(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printCleanHelp(stdout)
		return 0
	}
	olderThan, ok := parseCleanArgs(args, stderr)
	if !ok {
		printCleanHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	result, err := agentruns.Manager{Store: store, AgentsDir: paths.AgentsDir}.Clean(context.Background(), olderThan)
	if err != nil {
		fmt.Fprintf(stderr, "clean agent runs: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "clean complete\ndeleted: %d\nkept: %d\n", result.Deleted, result.Kept)
	return 0
}

func parseCleanArgs(args []string, stderr io.Writer) (time.Duration, bool) {
	olderThan := 7 * 24 * time.Hour
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--older-than":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "clean --older-than requires an age\n\n")
				return 0, false
			}
			parsed, err := parseAge(args[i+1])
			if err != nil {
				fmt.Fprintf(stderr, "clean --older-than: %v\n\n", err)
				return 0, false
			}
			olderThan = parsed
			i++
		default:
			fmt.Fprintf(stderr, "unknown clean argument: %s\n\n", args[i])
			return 0, false
		}
	}
	return olderThan, true
}

func parseAge(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("age is required")
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid day age %q", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid age %q", raw)
	}
	if duration < 0 {
		return 0, fmt.Errorf("age must be non-negative")
	}
	return duration, nil
}

func runHydrate(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printHydrateHelp(stdout)
		return 0
	}
	hydrateArgs, ok := parseHydrateArgs(args, stderr)
	if !ok {
		printHydrateHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), hydrateTimeout)
	defer cancel()
	result, err := registry.New(store).Hydrate(ctx, hydrateArgs.Project, hydrateArgs.CloneOptions)
	if err != nil {
		if hydrateArgs.JSON {
			commandResult := newHydrateErrorResult(hydrateArgs.Project, result, err)
			if err := presentation.RenderJSON(stdout, commandResult); err != nil {
				fmt.Fprintf(stderr, "encode hydrate result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return hydrateExitCode(commandResult)
		}
		fmt.Fprintf(stderr, "hydrate project: %v\n", err)
		return 1
	}
	commandResult := newHydrateResult(result)
	if hydrateArgs.JSON {
		if err := presentation.RenderJSON(stdout, commandResult); err != nil {
			fmt.Fprintf(stderr, "encode hydrate result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return hydrateExitCode(commandResult)
	}
	if err := presentation.RenderHuman(stdout, commandResult, renderHydratePayloadHuman); err != nil {
		fmt.Fprintf(stderr, "render hydrate result: %v\n", err)
		return commandresult.ExitInternalError.Code()
	}
	return 0
}

type parsedHydrateArgs struct {
	Project      string
	CloneOptions clonestrategy.Options
	JSON         bool
}

func parseHydrateArgs(args []string, stderr io.Writer) (parsedHydrateArgs, bool) {
	var projects []string
	var jsonOutput bool
	var cloneOptions clonestrategy.Options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOutput = true
		case "--partial-clone":
			cloneOptions.Partial = true
		case "--sparse":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "hydrate --sparse requires a project-relative path\n\n")
				return parsedHydrateArgs{}, false
			}
			path, ok := parseSparsePath(args[i+1])
			if !ok {
				fmt.Fprintf(stderr, "hydrate --sparse path must be project-relative and outside .git: %s\n\n", args[i+1])
				return parsedHydrateArgs{}, false
			}
			cloneOptions.SparsePaths = append(cloneOptions.SparsePaths, path)
			i++
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) != 1 {
		fmt.Fprint(stderr, "hydrate requires exactly one project\n\n")
		return parsedHydrateArgs{}, false
	}
	return parsedHydrateArgs{Project: projects[0], CloneOptions: cloneOptions, JSON: jsonOutput}, true
}

type hydratePayload struct {
	Project       string                   `json:"project"`
	Outcome       string                   `json:"outcome"`
	Path          string                   `json:"path"`
	PathPresent   bool                     `json:"path_present"`
	Remote        string                   `json:"remote"`
	CloneStrategy *clonestrategy.Selection `json:"clone_strategy,omitempty"`
}

func newHydrateResult(result registry.HydrateResult) commandresult.Result[hydratePayload] {
	outcome := "hydrated"
	if result.AlreadyPresent {
		outcome = "already-present"
	}
	return commandresult.New("hydrate", commandresult.ExitSuccess, commandresult.Diagnostics{}, hydratePayload{
		Project:       result.Project.Alias,
		Outcome:       outcome,
		Path:          result.Project.LocalPath,
		PathPresent:   true,
		Remote:        result.Project.NormalizedRemote,
		CloneStrategy: cloneStrategyPayload(result.CloneStrategy),
	})
}

func newHydrateErrorResult(projectName string, result registry.HydrateResult, err error) commandresult.Result[hydratePayload] {
	project := result.Project
	alias := project.Alias
	if alias == "" {
		alias = projectName
	}
	payload := hydratePayload{
		Project:       alias,
		Outcome:       "failed",
		Path:          project.LocalPath,
		Remote:        project.NormalizedRemote,
		CloneStrategy: cloneStrategyPayload(result.CloneStrategy),
	}
	if project.LocalPath != "" {
		if _, statErr := os.Stat(project.LocalPath); statErr == nil {
			payload.PathPresent = true
		} else if !errors.Is(statErr, os.ErrNotExist) {
			payload.PathPresent = true
		}
	}
	exitClass := commandresult.ExitInternalError
	diagnostic := commandresult.Diagnostic{Code: "hydrate-failed", Message: err.Error(), Target: alias}
	var conflict registry.PathConflictError
	if errors.As(err, &conflict) {
		exitClass = commandresult.ExitReadinessBlocked
		payload.Outcome = "path-conflict"
		payload.Path = conflict.Path
		payload.PathPresent = true
		diagnostic = commandresult.Diagnostic{Code: "path-conflict", Message: err.Error(), Target: conflict.Path}
	} else if strings.HasPrefix(err.Error(), "unknown project:") {
		exitClass = commandresult.ExitReadinessBlocked
		payload.Outcome = "unknown-project"
		diagnostic = commandresult.Diagnostic{Code: "unknown-project", Message: err.Error(), Target: alias}
	}
	return commandresult.New("hydrate", exitClass, commandresult.Diagnostics{
		Blockers: []commandresult.Diagnostic{diagnostic},
	}, payload)
}

func hydrateExitCode(result commandresult.Result[hydratePayload]) int {
	if result.ExitClass == commandresult.ExitReadinessBlocked {
		return 1
	}
	return result.ExitClass.Code()
}

func renderHydratePayloadHuman(w io.Writer, payload hydratePayload) error {
	if payload.Outcome == "already-present" {
		fmt.Fprintf(w, "project already present: %s\npath: %s\n", payload.Project, payload.Path)
		return nil
	}
	fmt.Fprintf(w, "hydrated project: %s\npath: %s\nremote: %s\n", payload.Project, payload.Path, payload.Remote)
	return nil
}

func runBootstrap(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printBootstrapHelp(stdout)
		return 0
	}
	bootstrapArgs, ok := parseBootstrapArgs(args, stderr)
	if !ok {
		printBootstrapHelp(stderr)
		return 2
	}
	entries, err := workspacemanifest.LoadEntries(bootstrapArgs.ManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "read workspace manifest: %v\n", err)
		return 1
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	ctx := context.Background()
	var result bootstrap.Result
	if bootstrapArgs.Apply {
		result, err = bootstrap.Bootstrapper{Store: store}.Apply(ctx, entries)
	} else {
		plan, planErr := bootstrap.Bootstrapper{Store: store}.Plan(ctx, entries)
		result = bootstrap.Result{Plan: plan}
		err = planErr
	}
	if err != nil {
		var blocked bootstrap.BlockedError
		if !errors.As(err, &blocked) {
			fmt.Fprintf(stderr, "bootstrap workspace: %v\n", err)
			return 1
		}
		commandResult := newBootstrapResult(bootstrapArgs, result)
		if bootstrapArgs.JSON {
			if err := presentation.RenderJSON(stdout, commandResult); err != nil {
				fmt.Fprintf(stderr, "encode bootstrap result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return bootstrapExitCode(commandResult)
		}
		if err := presentation.RenderHuman(stdout, commandResult, renderBootstrapPayloadHuman); err != nil {
			fmt.Fprintf(stderr, "render bootstrap result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return bootstrapExitCode(commandResult)
	}
	commandResult := newBootstrapResult(bootstrapArgs, result)
	if bootstrapArgs.JSON {
		if err := presentation.RenderJSON(stdout, commandResult); err != nil {
			fmt.Fprintf(stderr, "encode bootstrap result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return bootstrapExitCode(commandResult)
	}
	if err := presentation.RenderHuman(stdout, commandResult, renderBootstrapPayloadHuman); err != nil {
		fmt.Fprintf(stderr, "render bootstrap result: %v\n", err)
		return commandresult.ExitInternalError.Code()
	}
	return bootstrapExitCode(commandResult)
}

type parsedBootstrapArgs struct {
	ManifestPath string
	Apply        bool
	JSON         bool
}

func parseBootstrapArgs(args []string, stderr io.Writer) (parsedBootstrapArgs, bool) {
	var manifestPaths []string
	var apply bool
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--apply":
			apply = true
		case "--json":
			jsonOutput = true
		default:
			manifestPaths = append(manifestPaths, arg)
		}
	}
	if len(manifestPaths) != 1 {
		fmt.Fprint(stderr, "bootstrap requires exactly one workspace manifest path\n\n")
		return parsedBootstrapArgs{}, false
	}
	return parsedBootstrapArgs{ManifestPath: manifestPaths[0], Apply: apply, JSON: jsonOutput}, true
}

type bootstrapPayload struct {
	Apply   bool                     `json:"apply"`
	Plan    reconciliation.DriftPlan `json:"plan"`
	Applied bootstrapAppliedPayload  `json:"applied"`
}

type bootstrapAppliedPayload struct {
	ParentDirectories []string                  `json:"parent_directories"`
	AddedProjects     []bootstrapProjectPayload `json:"added_projects"`
	UpdatedProjects   []bootstrapProjectPayload `json:"updated_projects"`
}

type bootstrapProjectPayload struct {
	Alias    string `json:"alias"`
	Remote   string `json:"remote"`
	CloneURL string `json:"clone_url"`
	Path     string `json:"path"`
}

func newBootstrapResult(args parsedBootstrapArgs, result bootstrap.Result) commandresult.Result[bootstrapPayload] {
	diagnostics := bootstrapDiagnostics(result.Plan.Blockers)
	exitClass := commandresult.ExitSuccess
	if len(diagnostics.Blockers) != 0 {
		exitClass = commandresult.ExitReadinessBlocked
	}
	return commandresult.New("bootstrap", exitClass, diagnostics, bootstrapPayload{
		Apply:   args.Apply,
		Plan:    result.Plan,
		Applied: bootstrapApplied(result.Applied),
	})
}

func bootstrapApplied(applied bootstrap.Applied) bootstrapAppliedPayload {
	return bootstrapAppliedPayload{
		ParentDirectories: append([]string(nil), applied.ParentDirectories...),
		AddedProjects:     bootstrapProjects(applied.AddedProjects),
		UpdatedProjects:   bootstrapProjects(applied.UpdatedProjects),
	}
}

func bootstrapProjects(projects []state.Project) []bootstrapProjectPayload {
	if projects == nil {
		return []bootstrapProjectPayload{}
	}
	payloads := make([]bootstrapProjectPayload, 0, len(projects))
	for _, project := range projects {
		payloads = append(payloads, bootstrapProjectPayload{
			Alias:    project.Alias,
			Remote:   project.NormalizedRemote,
			CloneURL: project.CloneURL,
			Path:     project.LocalPath,
		})
	}
	return payloads
}

func bootstrapDiagnostics(blockers []reconciliation.Blocker) commandresult.Diagnostics {
	diagnostics := commandresult.Diagnostics{}
	for _, blocker := range blockers {
		target := blocker.Path
		if target == "" {
			target = blocker.Alias
		}
		diagnostics.Blockers = append(diagnostics.Blockers, commandresult.Diagnostic{
			Code:    string(blocker.Kind),
			Message: blocker.Reason,
			Target:  target,
		})
	}
	return diagnostics
}

func bootstrapExitCode(result commandresult.Result[bootstrapPayload]) int {
	if result.ExitClass == commandresult.ExitReadinessBlocked {
		return 1
	}
	return result.ExitClass.Code()
}

func renderBootstrapPayloadHuman(w io.Writer, payload bootstrapPayload) error {
	fmt.Fprintln(w, "bootstrap plan")
	fmt.Fprintf(w, "workspace_root: %s\n", payload.Plan.WorkspaceRoot)
	fmt.Fprintf(w, "apply: %t\n", payload.Apply)
	fmt.Fprintf(w, "blocked: %t\n", payload.Plan.Blocked)
	if len(payload.Plan.Drifts) == 0 {
		fmt.Fprintln(w, "drifts: none")
	}
	for _, drift := range payload.Plan.Drifts {
		renderBootstrapDrift(w, drift)
	}
	for _, blocker := range payload.Plan.Blockers {
		fmt.Fprintf(w, "blocker: %s %s %s\n", blocker.Kind, blocker.Path, blocker.Reason)
	}
	if !payload.Apply || payload.Plan.Blocked {
		return nil
	}
	fmt.Fprintln(w, "applied")
	if len(payload.Applied.ParentDirectories) == 0 {
		fmt.Fprintln(w, "parents: none")
	} else {
		for _, parent := range payload.Applied.ParentDirectories {
			fmt.Fprintf(w, "parent: %s\n", parent)
		}
	}
	if len(payload.Applied.AddedProjects) == 0 {
		fmt.Fprintln(w, "added: none")
	} else {
		for _, project := range payload.Applied.AddedProjects {
			fmt.Fprintf(w, "added: %s %s\n", project.Alias, project.Path)
		}
	}
	if len(payload.Applied.UpdatedProjects) == 0 {
		fmt.Fprintln(w, "updated: none")
	} else {
		for _, project := range payload.Applied.UpdatedProjects {
			fmt.Fprintf(w, "updated: %s %s\n", project.Alias, project.Path)
		}
	}
	return nil
}

func renderBootstrapDrift(w io.Writer, drift reconciliation.Drift) {
	switch drift.Kind {
	case reconciliation.DriftMissing:
		fmt.Fprintf(w, "missing: %s %s\n", drift.Alias, drift.DesiredLocalPath)
	case reconciliation.DriftMoved:
		fmt.Fprintf(w, "moved: %s %s -> %s\n", drift.Alias, drift.ObservedLocalPath, drift.DesiredLocalPath)
	case reconciliation.DriftUnchanged:
		fmt.Fprintf(w, "unchanged: %s %s\n", drift.Alias, drift.DesiredLocalPath)
	case reconciliation.DriftAdded:
		fmt.Fprintf(w, "local-only: %s %s\n", drift.Alias, drift.ObservedLocalPath)
	case reconciliation.DriftConflicting:
		fmt.Fprintf(w, "conflict: %s %s %s\n", drift.Alias, drift.DesiredLocalPath, drift.Reason)
	default:
		fmt.Fprintf(w, "%s: %s %s\n", drift.Kind, drift.Alias, drift.DesiredLocalPath)
	}
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printAgentHelp(stdout)
		return 0
	}
	switch args[0] {
	case "prepare":
		return runAgentPrepare(args[1:], stdout, stderr)
	case "run":
		return runAgentRun(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown agent command: %s\n\n", args[0])
		printAgentHelp(stderr)
		return 2
	}
}

func runAgentRun(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printAgentRunHelp(stdout)
		return 0
	}
	runID, label, timeout, command, ok := parseAgentRunArgs(args, stderr)
	if !ok {
		printAgentRunHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := agentruns.Manager{
		Store:     store,
		AgentsDir: paths.AgentsDir,
	}.Execute(ctx, agentruns.ExecuteRequest{
		RunID:   runID,
		Label:   label,
		Command: command,
		Timeout: timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "run agent command: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "agent command complete\nrun: %s\nlabel: %s\nexit_code: %d\nduration: %s\nstdout_path: %s\nstderr_path: %s\n",
		runID,
		result.Label,
		result.ExitCode,
		result.Duration,
		result.StdoutPath,
		result.StderrPath,
	)
	if result.ExitCode != 0 {
		return result.ExitCode
	}
	return 0
}

func parseAgentRunArgs(args []string, stderr io.Writer) (string, string, time.Duration, []string, bool) {
	var runIDs []string
	var label string
	timeout := agentRunTimeout
	var command []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			command = append([]string(nil), args[i+1:]...)
			i = len(args)
		case "--label":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "agent run --label requires a label\n\n")
				return "", "", 0, nil, false
			}
			label = args[i+1]
			i++
		case "--timeout":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "agent run --timeout requires a duration\n\n")
				return "", "", 0, nil, false
			}
			parsed, err := time.ParseDuration(args[i+1])
			if err != nil || parsed <= 0 {
				fmt.Fprintf(stderr, "agent run --timeout must be a positive Go duration: %s\n\n", args[i+1])
				return "", "", 0, nil, false
			}
			timeout = parsed
			i++
		default:
			runIDs = append(runIDs, args[i])
		}
	}
	if len(runIDs) != 1 {
		fmt.Fprint(stderr, "agent run requires exactly one run id\n\n")
		return "", "", 0, nil, false
	}
	if strings.TrimSpace(label) == "" {
		fmt.Fprint(stderr, "agent run --label is required\n\n")
		return "", "", 0, nil, false
	}
	if len(command) == 0 {
		fmt.Fprint(stderr, "agent run requires -- followed by a command\n\n")
		return "", "", 0, nil, false
	}
	return runIDs[0], label, timeout, command, true
}

func runAgentPrepare(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printAgentPrepareHelp(stdout)
		return 0
	}
	agentArgs, ok := parseAgentPrepareArgs(args, stderr)
	if !ok {
		printAgentPrepareHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), hydrateTimeout)
	defer cancel()
	result, err := agentprep.Preparer{
		Store:     store,
		AgentsDir: paths.AgentsDir,
		Producer:  agentcontract.DefaultProducer(version),
		Toolchain: toolchain.HostDetector{},
	}.Prepare(ctx, agentprep.Request{
		Project:          agentArgs.Project,
		Base:             agentArgs.Base,
		Profile:          agentArgs.Profile,
		EnvProvider:      agentArgs.EnvProvider,
		AllowedEnvScopes: agentArgs.AllowedEnvScopes,
		CloneOptions:     agentArgs.CloneOptions,
	})
	if err != nil {
		if _, ok := err.(agentprep.BlockedError); ok {
			if agentArgs.JSON {
				commandResult := newAgentPrepareResult(agentArgs, result, false)
				if err := presentation.RenderJSON(stdout, commandResult); err != nil {
					fmt.Fprintf(stderr, "encode agent prepare result: %v\n", err)
					return commandresult.ExitInternalError.Code()
				}
				return agentPrepareExitCode(commandResult)
			}
			printAgentDiagnostics(stderr, result.Diagnostics)
			return 1
		}
		if agentArgs.JSON {
			commandResult := newAgentPrepareErrorResult(agentArgs, result, err)
			if err := presentation.RenderJSON(stdout, commandResult); err != nil {
				fmt.Fprintf(stderr, "encode agent prepare result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return agentPrepareExitCode(commandResult)
		}
		fmt.Fprintf(stderr, "prepare agent workspace: %v\n", err)
		return 1
	}
	commandResult := newAgentPrepareResult(agentArgs, result, true)
	if agentArgs.JSON {
		if err := presentation.RenderJSON(stdout, commandResult); err != nil {
			fmt.Fprintf(stderr, "encode agent prepare result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return agentPrepareExitCode(commandResult)
	}
	if err := presentation.RenderHuman(stdout, commandResult, renderAgentPreparePayloadHuman); err != nil {
		fmt.Fprintf(stderr, "render agent prepare result: %v\n", err)
		return commandresult.ExitInternalError.Code()
	}
	return 0
}

type parsedAgentPrepareArgs struct {
	Project          string
	Base             string
	Profile          string
	EnvProvider      string
	AllowedEnvScopes []string
	CloneOptions     clonestrategy.Options
	JSON             bool
}

func parseAgentPrepareArgs(args []string, stderr io.Writer) (parsedAgentPrepareArgs, bool) {
	var base string
	var profile string
	var envProvider string
	var allowedEnvScopes []string
	var cloneOptions clonestrategy.Options
	var projects []string
	var jsonOutput bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "agent prepare --base requires a branch\n\n")
				return parsedAgentPrepareArgs{}, false
			}
			base = args[i+1]
			i++
		case "--profile":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "agent prepare --profile requires a name\n\n")
				return parsedAgentPrepareArgs{}, false
			}
			profile = args[i+1]
			i++
		case "--env-provider":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "agent prepare --env-provider requires a provider\n\n")
				return parsedAgentPrepareArgs{}, false
			}
			envProvider = args[i+1]
			i++
		case "--allow-env-scope":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "agent prepare --allow-env-scope requires a scope\n\n")
				return parsedAgentPrepareArgs{}, false
			}
			allowedEnvScopes = append(allowedEnvScopes, args[i+1])
			i++
		case "--partial-clone":
			cloneOptions.Partial = true
		case "--sparse":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "agent prepare --sparse requires a project-relative path\n\n")
				return parsedAgentPrepareArgs{}, false
			}
			path, ok := parseSparsePath(args[i+1])
			if !ok {
				fmt.Fprintf(stderr, "agent prepare --sparse path must be project-relative and outside .git: %s\n\n", args[i+1])
				return parsedAgentPrepareArgs{}, false
			}
			cloneOptions.SparsePaths = append(cloneOptions.SparsePaths, path)
			i++
		case "--json":
			jsonOutput = true
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) != 1 {
		fmt.Fprint(stderr, "agent prepare requires exactly one project\n\n")
		return parsedAgentPrepareArgs{}, false
	}
	return parsedAgentPrepareArgs{Project: projects[0], Base: base, Profile: profile, EnvProvider: envProvider, AllowedEnvScopes: allowedEnvScopes, CloneOptions: cloneOptions, JSON: jsonOutput}, true
}

func parseSparsePath(raw string) (string, bool) {
	normalized := clonestrategy.NormalizeSparsePaths([]string{raw})
	if len(normalized) != 1 {
		return "", false
	}
	return normalized[0], true
}

type agentPreparePayload struct {
	Project          string                    `json:"project"`
	Ready            bool                      `json:"ready"`
	RunID            string                    `json:"run_id,omitempty"`
	Base             string                    `json:"base"`
	Profile          string                    `json:"profile"`
	HandoffDocsCount int                       `json:"handoff_docs_count"`
	RunContractPath  string                    `json:"run_contract_path,omitempty"`
	ReadyPath        string                    `json:"ready_path,omitempty"`
	ResolvedCommit   string                    `json:"resolved_commit,omitempty"`
	CloneStrategy    *clonestrategy.Selection  `json:"clone_strategy,omitempty"`
	Env              agentcontract.EnvInfo     `json:"env"`
	Diagnostics      commandresult.Diagnostics `json:"diagnostics"`
}

func newAgentPrepareResult(args parsedAgentPrepareArgs, result agentprep.Result, ready bool) commandresult.Result[agentPreparePayload] {
	diagnostics := agentPrepareDiagnostics(result.Diagnostics)
	exitClass := commandresult.ReadinessExitClass(len(diagnostics.Warnings), len(diagnostics.Blockers))
	base := result.Base
	if base == "" {
		base = args.Base
	}
	profile := result.Profile
	if profile == "" {
		profile = strings.TrimSpace(args.Profile)
	}
	payload := agentPreparePayload{
		Project:          args.Project,
		Ready:            ready,
		Base:             base,
		Profile:          profile,
		HandoffDocsCount: len(result.Metadata.HandoffDocs),
		CloneStrategy:    cloneStrategyPayload(result.CloneStrategy),
		Env:              result.Metadata.Env,
		Diagnostics:      diagnostics,
	}
	if ready {
		payload.RunID = result.RunID
		payload.ReadyPath = result.ReadyPath
		payload.RunContractPath = filepath.Join(result.ReadyPath, agentprep.MetadataFileName)
		payload.ResolvedCommit = result.ResolvedCommit
	}
	return commandresult.New("agent prepare", exitClass, diagnostics, payload)
}

func newAgentPrepareErrorResult(args parsedAgentPrepareArgs, result agentprep.Result, err error) commandresult.Result[agentPreparePayload] {
	diagnostics := agentPrepareDiagnostics(result.Diagnostics)
	exitClass := commandresult.ExitInternalError
	diagnostic := commandresult.Diagnostic{Code: "agent-prepare-failed", Message: err.Error(), Target: args.Project}
	if strings.HasPrefix(err.Error(), "unknown project:") {
		exitClass = commandresult.ExitReadinessBlocked
		diagnostic.Code = "unknown-project"
	}
	diagnostics.Blockers = append(diagnostics.Blockers, diagnostic)
	base := result.Base
	if base == "" {
		base = strings.TrimSpace(args.Base)
	}
	profile := result.Profile
	if profile == "" {
		profile = strings.TrimSpace(args.Profile)
	}
	return commandresult.New("agent prepare", exitClass, diagnostics, agentPreparePayload{
		Project:       args.Project,
		Ready:         false,
		Base:          base,
		Profile:       profile,
		CloneStrategy: cloneStrategyPayload(result.CloneStrategy),
		Env:           result.Metadata.Env,
		Diagnostics:   diagnostics,
	})
}

func cloneStrategyPayload(selection clonestrategy.Selection) *clonestrategy.Selection {
	if strings.TrimSpace(selection.Name) == "" {
		return nil
	}
	normalized := clonestrategy.NormalizeSelection(selection)
	return &normalized
}

func agentPrepareDiagnostics(diagnostics agentprep.Diagnostics) commandresult.Diagnostics {
	return commandresult.Diagnostics{
		Warnings: agentPrepareDiagnosticList(diagnostics.Warnings),
		Blockers: agentPrepareDiagnosticList(diagnostics.Blockers),
	}
}

func agentPrepareDiagnosticList(items []agentprep.Diagnostic) []commandresult.Diagnostic {
	diagnostics := make([]commandresult.Diagnostic, 0, len(items))
	for _, item := range items {
		diagnostics = append(diagnostics, commandresult.Diagnostic{Code: item.Code, Message: item.Message})
	}
	return diagnostics
}

func agentPrepareExitCode(result commandresult.Result[agentPreparePayload]) int {
	if result.ExitClass == commandresult.ExitReadinessBlocked {
		return 1
	}
	return result.ExitClass.Code()
}

func renderAgentPreparePayloadHuman(w io.Writer, payload agentPreparePayload) error {
	fmt.Fprintf(w, "agent workspace ready\nproject: %s\nbase: %s\n", payload.Project, payload.Base)
	if payload.Profile != "" {
		fmt.Fprintf(w, "profile: %s\n", payload.Profile)
	}
	printCommandDiagnostics(w, payload.Diagnostics)
	fmt.Fprintf(w, "handoff_docs: %d\n", payload.HandoffDocsCount)
	if payload.Env.MaterializationStatus != "" && payload.Env.MaterializationStatus != "not_requested" {
		fmt.Fprintf(w, "env_materialization: %s\n", payload.Env.MaterializationStatus)
		if payload.Env.Bundle.Present {
			fmt.Fprintln(w, "env_bundle: present")
			fmt.Fprintf(w, "env_bundle_path: %s\n", payload.Env.Bundle.Path)
		} else {
			fmt.Fprintln(w, "env_bundle: absent")
		}
	}
	fmt.Fprintf(w, "ready_path: %s\n", payload.ReadyPath)
	return nil
}

func printAgentDiagnostics(w io.Writer, diagnostics agentprep.Diagnostics) {
	if len(diagnostics.Warnings) == 0 {
		fmt.Fprintln(w, "warnings: none")
	} else {
		for _, warning := range diagnostics.Warnings {
			fmt.Fprintf(w, "warning: %s %s\n", warning.Code, warning.Message)
		}
	}
	if len(diagnostics.Blockers) == 0 {
		fmt.Fprintln(w, "blockers: none")
	} else {
		for _, blocker := range diagnostics.Blockers {
			fmt.Fprintf(w, "blocker: %s %s\n", blocker.Code, blocker.Message)
		}
	}
}

func runAdd(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printAddHelp(stdout)
		return 0
	}
	alias, path, ok := parseAddArgs(args, stdout, stderr)
	if !ok {
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	project, err := registry.New(store).AddPath(context.Background(), path, alias)
	if err != nil {
		fmt.Fprintf(stderr, "add project: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "added project: %s\nremote: %s\npath: %s\n", project.Alias, project.NormalizedRemote, project.LocalPath)
	return 0
}

func parseAddArgs(args []string, stdout, stderr io.Writer) (string, string, bool) {
	var alias string
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--alias":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "add --alias requires a name\n\n")
				printAddHelp(stderr)
				return "", "", false
			}
			alias = args[i+1]
			i++
		default:
			paths = append(paths, args[i])
		}
	}
	if len(paths) != 1 {
		fmt.Fprint(stderr, "add requires exactly one project path\n\n")
		printAddHelp(stderr)
		return "", "", false
	}
	return alias, paths[0], true
}

func runScan(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printScanHelp(stdout)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprint(stderr, "scan accepts at most one workspace root\n\n")
		printScanHelp(stderr)
		return 2
	}
	workspaceArg := ""
	if len(args) == 1 {
		workspaceArg = args[0]
	}
	workspaceRoot, err := config.ResolveWorkspaceRoot(workspaceArg)
	if err != nil {
		fmt.Fprintf(stderr, "resolve workspace root: %v\n", err)
		return 1
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	result, err := registry.New(store).ScanWorkspace(context.Background(), workspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "scan workspace: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "scan complete\nworkspace: %s\n", result.WorkspaceRoot)
	printScanProjects(stdout, "added", result.Added)
	printScanProjects(stdout, "updated", result.Updated)
	printScanProjects(stdout, "unchanged", result.Unchanged)
	if len(result.Skipped) == 0 {
		fmt.Fprintln(stdout, "skipped: none")
	} else {
		for _, skip := range result.Skipped {
			fmt.Fprintf(stdout, "skipped: %s (%s)\n", skip.Path, skip.Reason)
		}
	}
	return 0
}

func printScanProjects(w io.Writer, label string, projects []state.Project) {
	if len(projects) == 0 {
		fmt.Fprintf(w, "%s: none\n", label)
		return
	}
	for _, project := range projects {
		fmt.Fprintf(w, "%s: %s %s\n", label, project.Alias, project.LocalPath)
	}
}

func runTree(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printTreeHelp(stdout)
		return 0
	}
	treeArgs, ok := parseTreeArgs(args, stderr)
	if !ok {
		printTreeHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	result, err := buildTreeResult(context.Background(), projects)
	if err != nil {
		fmt.Fprintf(stderr, "check project readiness: %v\n", err)
		return 1
	}
	if treeArgs.JSON {
		if err := presentation.RenderJSON(stdout, result); err != nil {
			fmt.Fprintf(stderr, "encode tree result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return result.ExitClass.Code()
	}
	if err := presentation.RenderHuman(stdout, result, renderTreePayloadHuman); err != nil {
		fmt.Fprintf(stderr, "render tree result: %v\n", err)
		return commandresult.ExitInternalError.Code()
	}
	return result.ExitClass.Code()
}

type parsedTreeArgs struct {
	JSON bool
}

func parseTreeArgs(args []string, stderr io.Writer) (parsedTreeArgs, bool) {
	var jsonOutput bool
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			fmt.Fprintf(stderr, "unknown tree argument: %s\n\n", arg)
			return parsedTreeArgs{}, false
		}
	}
	return parsedTreeArgs{JSON: jsonOutput}, true
}

type treePayload struct {
	Projects []statusProject `json:"projects"`
}

func buildTreeResult(ctx context.Context, projects []state.Project) (commandresult.Result[treePayload], error) {
	treeProjects := make([]statusProject, 0, len(projects))
	warnings := 0
	blockers := 0
	for _, project := range projects {
		projectCtx, cancel := context.WithTimeout(ctx, statusReadinessTimeout)
		report, err := readiness.EvaluateProject(projectCtx, project, readiness.Options{CheckRemote: false})
		cancel()
		if err != nil {
			return commandresult.Result[treePayload]{}, err
		}
		diagnostics := statusDiagnostics(report)
		warnings += len(diagnostics.Warnings)
		blockers += len(diagnostics.Blockers)
		treeProjects = append(treeProjects, statusProject{
			Alias:       report.Project.Alias,
			State:       string(report.State),
			Path:        report.Project.LocalPath,
			PathPresent: report.LocalPathPresent,
			Remote:      report.Project.NormalizedRemote,
			Base:        report.BaseBranch,
			Diagnostics: diagnostics,
		})
	}
	return commandresult.New("tree", commandresult.ReadinessExitClass(warnings, blockers), commandresult.Diagnostics{}, treePayload{Projects: treeProjects}), nil
}

func renderTreePayloadHuman(w io.Writer, payload treePayload) error {
	fmt.Fprintln(w, "canonical workspace:")
	if len(payload.Projects) == 0 {
		fmt.Fprintln(w, "(empty)")
		return nil
	}
	for _, project := range payload.Projects {
		fmt.Fprintf(w, "- %s %s %s\n", project.Alias, project.State, project.Path)
	}
	return nil
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printStatusHelp(stdout)
		return 0
	}
	statusArgs, ok := parseStatusArgs(args, stderr)
	if !ok {
		printStatusHelp(stderr)
		return 2
	}
	store, err := openMigratedStore()
	if err != nil {
		fmt.Fprintf(stderr, "open CodeMesh state: %v\n", err)
		return 1
	}
	defer store.Close()

	projects, err := store.ListProjects(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	if statusArgs.ProjectName == "" {
		result := buildStatusSummaryResult(context.Background(), stderr, projects, statusArgs.Base)
		if statusArgs.JSON {
			if err := presentation.RenderJSON(stdout, result); err != nil {
				fmt.Fprintf(stderr, "encode status result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return result.ExitClass.Code()
		}
		if err := presentation.RenderHuman(stdout, result, renderStatusPayloadHuman); err != nil {
			fmt.Fprintf(stderr, "render status result: %v\n", err)
			return commandresult.ExitInternalError.Code()
		}
		return result.ExitClass.Code()
	}
	for _, project := range projects {
		if project.Alias == statusArgs.ProjectName {
			ctx, cancel := context.WithTimeout(context.Background(), statusReadinessTimeout)
			defer cancel()
			report, err := readiness.EvaluateProject(ctx, project, readiness.Options{BaseBranch: statusArgs.Base, CheckRemote: true})
			if err != nil {
				fmt.Fprintf(stderr, "check project readiness: %v\n", err)
				return 1
			}
			result := newStatusResult(statusArgs.ProjectName, []readiness.ProjectReport{report}, commandresult.Diagnostics{})
			if statusArgs.JSON {
				if err := presentation.RenderJSON(stdout, result); err != nil {
					fmt.Fprintf(stderr, "encode status result: %v\n", err)
					return commandresult.ExitInternalError.Code()
				}
				return result.ExitClass.Code()
			}
			if err := presentation.RenderHuman(stdout, result, renderStatusPayloadHuman); err != nil {
				fmt.Fprintf(stderr, "render status result: %v\n", err)
				return commandresult.ExitInternalError.Code()
			}
			return result.ExitClass.Code()
		}
	}
	fmt.Fprintf(stderr, "unknown project: %s\n", statusArgs.ProjectName)
	return 1
}

type parsedStatusArgs struct {
	ProjectName string
	Base        string
	JSON        bool
}

func parseStatusArgs(args []string, stderr io.Writer) (parsedStatusArgs, bool) {
	var base string
	var projects []string
	var jsonOutput bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprint(stderr, "status --base requires a branch\n\n")
				return parsedStatusArgs{}, false
			}
			base = args[i+1]
			i++
		case "--json":
			jsonOutput = true
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) > 1 {
		fmt.Fprint(stderr, "status accepts at most one project\n\n")
		return parsedStatusArgs{}, false
	}
	if len(projects) == 1 {
		return parsedStatusArgs{ProjectName: projects[0], Base: base, JSON: jsonOutput}, true
	}
	return parsedStatusArgs{Base: base, JSON: jsonOutput}, true
}

type statusPayload struct {
	Project  string          `json:"project,omitempty"`
	Projects []statusProject `json:"projects"`
}

type statusProject struct {
	Alias       string                    `json:"alias"`
	State       string                    `json:"state"`
	Path        string                    `json:"path"`
	PathPresent bool                      `json:"path_present"`
	Remote      string                    `json:"remote"`
	Base        string                    `json:"base"`
	Diagnostics commandresult.Diagnostics `json:"diagnostics"`
}

func buildStatusSummaryResult(ctx context.Context, stderr io.Writer, projects []state.Project, base string) commandresult.Result[statusPayload] {
	reports := make([]readiness.ProjectReport, 0, len(projects))
	commandDiagnostics := commandresult.Diagnostics{}
	if len(projects) == 0 {
		return newStatusResult("", reports, commandDiagnostics)
	}
	for _, project := range projects {
		projectCtx, cancel := context.WithTimeout(ctx, statusReadinessTimeout)
		report, err := readiness.EvaluateProject(projectCtx, project, readiness.Options{BaseBranch: base, CheckRemote: true})
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "check project readiness: %v\n", err)
			commandDiagnostics.Blockers = append(commandDiagnostics.Blockers, commandresult.Diagnostic{
				Code:    "readiness-evaluation-failed",
				Message: err.Error(),
				Target:  project.Alias,
			})
			continue
		}
		reports = append(reports, report)
	}
	return newStatusResult("", reports, commandDiagnostics)
}

func newStatusResult(projectName string, reports []readiness.ProjectReport, commandDiagnostics commandresult.Diagnostics) commandresult.Result[statusPayload] {
	projects := make([]statusProject, 0, len(reports))
	warnings := len(commandDiagnostics.Warnings)
	blockers := len(commandDiagnostics.Blockers)
	for _, report := range reports {
		projectDiagnostics := statusDiagnostics(report)
		warnings += len(projectDiagnostics.Warnings)
		blockers += len(projectDiagnostics.Blockers)
		projects = append(projects, statusProject{
			Alias:       report.Project.Alias,
			State:       string(report.State),
			Path:        report.Project.LocalPath,
			PathPresent: report.LocalPathPresent,
			Remote:      report.Project.NormalizedRemote,
			Base:        report.BaseBranch,
			Diagnostics: projectDiagnostics,
		})
	}
	return commandresult.New("status", commandresult.ReadinessExitClass(warnings, blockers), commandDiagnostics, statusPayload{
		Project:  projectName,
		Projects: projects,
	})
}

func statusDiagnostics(report readiness.ProjectReport) commandresult.Diagnostics {
	diagnostics := commandresult.Diagnostics{
		Warnings: make([]commandresult.Diagnostic, 0, len(report.Warnings)),
		Blockers: make([]commandresult.Diagnostic, 0, len(report.Blockers)),
	}
	for _, warning := range report.Warnings {
		diagnostics.Warnings = append(diagnostics.Warnings, commandresult.Diagnostic{
			Code:    warning.Code,
			Message: warning.Message,
		})
	}
	for _, blocker := range report.Blockers {
		diagnostics.Blockers = append(diagnostics.Blockers, commandresult.Diagnostic{
			Code:    blocker.Code,
			Message: blocker.Message,
		})
	}
	return diagnostics
}

func renderStatusPayloadHuman(w io.Writer, payload statusPayload) error {
	if payload.Project == "" {
		fmt.Fprintln(w, "readiness:")
		if len(payload.Projects) == 0 {
			fmt.Fprintln(w, "(empty)")
			return nil
		}
		for _, project := range payload.Projects {
			fmt.Fprintf(w, "- %s state=%s path_present=%t warnings=%d blockers=%d path=%s\n", project.Alias, project.State, project.PathPresent, len(project.Diagnostics.Warnings), len(project.Diagnostics.Blockers), project.Path)
		}
		return nil
	}
	if len(payload.Projects) == 0 {
		return nil
	}
	printProjectStatus(w, payload.Projects[0])
	return nil
}

func printProjectStatus(w io.Writer, project statusProject) {
	fmt.Fprintf(w, "project: %s\n", project.Alias)
	fmt.Fprintf(w, "state: %s\n", project.State)
	fmt.Fprintf(w, "path: %s\n", project.Path)
	fmt.Fprintf(w, "path_present: %t\n", project.PathPresent)
	fmt.Fprintf(w, "remote: %s\n", project.Remote)
	fmt.Fprintf(w, "base: %s\n", project.Base)
	printCommandDiagnostics(w, project.Diagnostics)
}

func printCommandDiagnostics(w io.Writer, diagnostics commandresult.Diagnostics) {
	if len(diagnostics.Warnings) == 0 {
		fmt.Fprintln(w, "warnings: none")
	} else {
		for _, warning := range diagnostics.Warnings {
			fmt.Fprintf(w, "warning: %s %s\n", warning.Code, warning.Message)
		}
	}
	if len(diagnostics.Blockers) == 0 {
		fmt.Fprintln(w, "blockers: none")
	} else {
		for _, blocker := range diagnostics.Blockers {
			fmt.Fprintf(w, "blocker: %s %s\n", blocker.Code, blocker.Message)
		}
	}
}

func openMigratedStore() (*state.SQLiteStore, error) {
	paths, err := config.ResolvePaths()
	if err != nil {
		return nil, err
	}
	store, err := state.Open(paths.Database)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(context.Background()); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func lookupProject(ctx context.Context, store interface {
	ListProjects(context.Context) ([]state.Project, error)
}, alias string) (state.Project, bool, error) {
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return state.Project{}, false, err
	}
	for _, project := range projects {
		if project.Alias == alias {
			return project, true, nil
		}
	}
	return state.Project{}, false, nil
}

func runInit(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printInitHelp(stdout)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprint(stderr, "init accepts at most one workspace root\n\n")
		printInitHelp(stderr)
		return 2
	}
	workspaceArg := ""
	if len(args) == 1 {
		workspaceArg = args[0]
	}
	paths, err := config.ResolvePaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve CodeMesh home: %v\n", err)
		return 1
	}
	workspaceRoot, err := config.ResolveWorkspaceRoot(workspaceArg)
	if err != nil {
		fmt.Fprintf(stderr, "resolve workspace root: %v\n", err)
		return 1
	}
	result, err := state.Initialize(context.Background(), paths.Home, workspaceRoot)
	if err != nil {
		fmt.Fprintf(stderr, "initialize CodeMesh state: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized CodeMesh\nhome: %s\ndatabase: %s\nworkspace: %s\n", result.Home, result.Database, result.WorkspaceRoot)
	return 0
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `CodeMesh augments Git and local filesystems for agent-ready workspaces.

Usage:
  codemesh [--help]
  codemesh [--version]
  codemesh init [workspace-root]
  codemesh add <path> [--alias name]
  codemesh scan [workspace-root]
  codemesh tree [--json]
  codemesh status [project] [--base branch] [--json]
  codemesh doctor <project> [--base branch] [--strict] [--json]
  codemesh hydrate <project> [--partial-clone] [--sparse path] [--json]
  codemesh bootstrap <manifest-path> [--apply] [--json]
  codemesh manifest export [--output path]
  codemesh manifest import <path>
  codemesh target export <target-name> --scope scope [--kind kind] [--workspace-root path] [--json]
  codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope
  codemesh machine register [workspace-root] [--name name] [--json]
  codemesh machine status [--json]
  codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]
  codemesh agent run <run-id> --label label [--timeout duration] -- <command...>
  codemesh runs
  codemesh clean [--older-than age]

Commands:
  init       create local CodeMesh state
  add        add one Git project to the registry
  scan       scan a workspace root for Git projects
  tree       show the canonical workspace
  status     report project readiness
  doctor     preflight agent handoff readiness without creating a run
  hydrate    clone a missing project into its desired path
  bootstrap  plan or apply workspace topology without cloning
  manifest   export or import portable workspace manifests
  target     export target-ready workspace specs
  env        manage private env bindings
  machine    register this machine locally
  agent      prepare and run agent workspaces
  runs       list prepared agent runs
  clean      delete old CodeMesh-managed agent runs
`)
}

func printManifestHelp(w io.Writer) {
	fmt.Fprint(w, `Export or import portable workspace manifests.

Usage:
  codemesh manifest export [--output path]
  codemesh manifest import <path>
`)
}

func printManifestExportHelp(w io.Writer) {
	fmt.Fprint(w, `Export a portable Workspace Manifest.

Usage:
  codemesh manifest export [--output path]

Writes JSON to stdout unless --output is provided.
Requires machine registration so canonical paths are relative to the registered workspace root.
`)
}

func printManifestImportHelp(w io.Writer) {
	fmt.Fprint(w, `Import a portable Workspace Manifest.

Usage:
  codemesh manifest import <path>

Validates manifest schema and stores Project Registry rows under this machine's registered workspace root.
`)
}

func printEnvHelp(w io.Writer) {
	fmt.Fprint(w, `Manage private env bindings.

Usage:
  codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope
`)
}

func printEnvBindHelp(w io.Writer) {
	fmt.Fprint(w, `Bind one logical env requirement to a private provider reference.

Usage:
  codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope

Stores provider references in local CodeMesh state, outside repo-local Project Policy.
`)
}

func printInitHelp(w io.Writer) {
	fmt.Fprint(w, `Create local CodeMesh state.

Usage:
  codemesh init [workspace-root]

Creates CodeMesh home, codemesh.db, and the agents directory.
Uses CODEMESH_HOME when set; otherwise uses $HOME/.codemesh.
`)
}

func printAddHelp(w io.Writer) {
	fmt.Fprint(w, `Add one Git project to the Project Registry.

Usage:
  codemesh add <path> [--alias name]

Alias defaults to the checkout directory name.
`)
}

func printScanHelp(w io.Writer) {
	fmt.Fprint(w, `Scan a workspace root into the Project Registry.

Usage:
  codemesh scan [workspace-root]

Workspace root defaults to the current directory.
Nested and unsupported Git candidates are reported as skipped.
`)
}

func printTreeHelp(w io.Writer) {
	fmt.Fprint(w, `Show the canonical workspace.

Usage:
  codemesh tree [--json]

Use --json for the stable command result shape.
`)
}

func printStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Report project readiness.

Usage:
  codemesh status [project] [--base branch] [--json]

Project defaults to all known projects.
Base defaults to main.
Use --json for the stable command result shape.
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Preflight agent handoff readiness without creating a run.

Usage:
  codemesh doctor <project> [--base branch] [--strict] [--json]

Checks the same handoff readiness gate as agent prepare.
Use --strict to fail warning-only readiness for automation.
Use --json for the stable command result shape.
`)
}

func printHydrateHelp(w io.Writer) {
	fmt.Fprint(w, `Hydrate one missing project.

Usage:
  codemesh hydrate <project> [--partial-clone] [--sparse path] [--json]

Clones the registered remote into the desired local path.
Refuses existing non-empty non-Git paths.
Use --partial-clone and repeatable --sparse path for explicit Git-native laziness.
Use --json for the stable command result shape.
`)
}

func printBootstrapHelp(w io.Writer) {
	fmt.Fprint(w, `Bootstrap workspace topology from a Workspace Manifest.

Usage:
  codemesh bootstrap <manifest-path> [--apply] [--json]

Reads one manifest entry file or a directory of JSON entries.
Default mode reports the plan only.
--apply creates parent directories and local Project Registry rows.
Bootstrap does not clone project content or create project placeholders.
Use --json for the stable command result shape.
`)
}

func printTargetHelp(w io.Writer) {
	fmt.Fprint(w, `Export target-ready workspace specs.

Usage:
  codemesh target export <target-name> --scope scope [--kind kind] [--workspace-root path] [--json]
`)
}

func printTargetExportHelp(w io.Writer) {
	fmt.Fprint(w, `Export a Workspace Target spec.

Usage:
  codemesh target export <target-name> --scope scope [--kind kind] [--workspace-root path] [--json]

Packages manifest topology, machine and target facts, and scoped env binding references.
Does not contact Coder, DevPod, Daytona, or any live provider.
Use --json for the stable command result shape.
`)
}

func printMachineHelp(w io.Writer) {
	fmt.Fprint(w, `Register local machine identity.

Usage:
  codemesh machine register [workspace-root] [--name name] [--json]
  codemesh machine status [--json]
`)
}

func printMachineRegisterHelp(w io.Writer) {
	fmt.Fprint(w, `Register local machine identity.

Usage:
  codemesh machine register [workspace-root] [--name name] [--json]

Creates or reuses a persistent local machine ID and updates mutable local facts.
`)
}

func printMachineStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Show local machine registration.

Usage:
  codemesh machine status [--json]
`)
}

func printAgentHelp(w io.Writer) {
	fmt.Fprint(w, `Prepare and run agent workspaces.

Usage:
  codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]
  codemesh agent run <run-id> --label label [--timeout duration] -- <command...>
`)
}

func printAgentPrepareHelp(w io.Writer) {
	fmt.Fprint(w, `Prepare one agent workspace.

Usage:
  codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]

Creates a temporary clone under CodeMesh-managed agents storage.
Prints ready_path when the workspace is ready.
Use --partial-clone and repeatable --sparse path for explicit Git-native laziness.
Use --env-provider fake with --allow-env-scope to materialize a fake-provider env bundle.
Use --json for the stable command result shape.
`)
}

func printAgentRunHelp(w io.Writer) {
	fmt.Fprint(w, `Run one command in a prepared agent workspace.

Usage:
  codemesh agent run <run-id> --label label [--timeout duration] -- <command...>

Captures stdout and stderr under the managed run directory.
Records command metadata in codemesh-run.json and local state.
Timeout defaults to 10m and accepts Go durations such as 30s or 5m.
`)
}

func printRunsHelp(w io.Writer) {
	fmt.Fprint(w, `List prepared agent runs.

Usage:
  codemesh runs
`)
}

func printCleanHelp(w io.Writer) {
	fmt.Fprint(w, `Delete old CodeMesh-managed agent runs.

Usage:
  codemesh clean [--older-than age]

Age supports Go durations and day values such as 7d.
`)
}
