package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/BramVR/codemesh/internal/agentprep"
	"github.com/BramVR/codemesh/internal/config"
	"github.com/BramVR/codemesh/internal/readiness"
	"github.com/BramVR/codemesh/internal/registry"
	"github.com/BramVR/codemesh/internal/state"
)

const version = "0.0.0-dev"

const statusReadinessTimeout = 30 * time.Second
const hydrateTimeout = 10 * time.Minute

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
	case "hydrate":
		return runHydrate(args[1:], stdout, stderr)
	case "agent":
		return runAgent(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func runHydrate(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printHydrateHelp(stdout)
		return 0
	}
	if len(args) != 1 {
		fmt.Fprint(stderr, "hydrate requires exactly one project\n\n")
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
	result, err := registry.New(store).Hydrate(ctx, args[0])
	if err != nil {
		fmt.Fprintf(stderr, "hydrate project: %v\n", err)
		return 1
	}
	if result.AlreadyPresent {
		fmt.Fprintf(stdout, "project already present: %s\npath: %s\n", result.Project.Alias, result.Project.LocalPath)
		return 0
	}
	fmt.Fprintf(stdout, "hydrated project: %s\npath: %s\nremote: %s\n", result.Project.Alias, result.Project.LocalPath, result.Project.NormalizedRemote)
	return 0
}

func runAgent(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printAgentHelp(stdout)
		return 0
	}
	switch args[0] {
	case "prepare":
		return runAgentPrepare(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown agent command: %s\n\n", args[0])
		printAgentHelp(stderr)
		return 2
	}
}

func runAgentPrepare(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printAgentPrepareHelp(stdout)
		return 0
	}
	project, base, profile, ok := parseAgentPrepareArgs(args, stderr)
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
	}.Prepare(ctx, agentprep.Request{
		Project: project,
		Base:    base,
		Profile: profile,
	})
	if err != nil {
		if _, ok := err.(agentprep.BlockedError); ok {
			printAgentDiagnostics(stderr, result.Diagnostics)
			return 1
		}
		fmt.Fprintf(stderr, "prepare agent workspace: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "agent workspace ready\nproject: %s\nbase: %s\n", project, result.Base)
	if result.Profile != "" {
		fmt.Fprintf(stdout, "profile: %s\n", result.Profile)
	}
	printAgentDiagnostics(stdout, result.Diagnostics)
	fmt.Fprintf(stdout, "ready_path: %s\n", result.ReadyPath)
	return 0
}

func parseAgentPrepareArgs(args []string, stderr io.Writer) (string, string, string, bool) {
	var base string
	var profile string
	var projects []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "agent prepare --base requires a branch\n\n")
				return "", "", "", false
			}
			base = args[i+1]
			i++
		case "--profile":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "agent prepare --profile requires a name\n\n")
				return "", "", "", false
			}
			profile = args[i+1]
			i++
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) != 1 {
		fmt.Fprint(stderr, "agent prepare requires exactly one project\n\n")
		return "", "", "", false
	}
	return projects[0], base, profile, true
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
	if len(args) > 0 {
		fmt.Fprint(stderr, "tree accepts no arguments\n\n")
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
	fmt.Fprintln(stdout, "canonical workspace:")
	if len(projects) == 0 {
		fmt.Fprintln(stdout, "(empty)")
		return 0
	}
	for _, project := range projects {
		ctx, cancel := context.WithTimeout(context.Background(), statusReadinessTimeout)
		report, err := readiness.EvaluateProject(ctx, project, readiness.Options{CheckRemote: false})
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "check project readiness: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "- %s %s %s\n", report.Project.Alias, report.State, report.Project.LocalPath)
	}
	return 0
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		printStatusHelp(stdout)
		return 0
	}
	projectName, base, ok := parseStatusArgs(args, stderr)
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
	if projectName == "" {
		printStatusSummary(context.Background(), stdout, stderr, projects, base)
		return 0
	}
	for _, project := range projects {
		if project.Alias == projectName {
			ctx, cancel := context.WithTimeout(context.Background(), statusReadinessTimeout)
			defer cancel()
			report, err := readiness.EvaluateProject(ctx, project, readiness.Options{BaseBranch: base, CheckRemote: true})
			if err != nil {
				fmt.Fprintf(stderr, "check project readiness: %v\n", err)
				return 1
			}
			printProjectStatus(stdout, report)
			return 0
		}
	}
	fmt.Fprintf(stderr, "unknown project: %s\n", projectName)
	return 1
}

func parseStatusArgs(args []string, stderr io.Writer) (string, string, bool) {
	var base string
	var projects []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--base":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, "status --base requires a branch\n\n")
				return "", "", false
			}
			base = args[i+1]
			i++
		default:
			projects = append(projects, args[i])
		}
	}
	if len(projects) > 1 {
		fmt.Fprint(stderr, "status accepts at most one project\n\n")
		return "", "", false
	}
	if len(projects) == 1 {
		return projects[0], base, true
	}
	return "", base, true
}

func printStatusSummary(ctx context.Context, stdout, stderr io.Writer, projects []state.Project, base string) {
	fmt.Fprintln(stdout, "readiness:")
	if len(projects) == 0 {
		fmt.Fprintln(stdout, "(empty)")
		return
	}
	for _, project := range projects {
		projectCtx, cancel := context.WithTimeout(ctx, statusReadinessTimeout)
		report, err := readiness.EvaluateProject(projectCtx, project, readiness.Options{BaseBranch: base, CheckRemote: true})
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "check project readiness: %v\n", err)
			continue
		}
		fmt.Fprintf(stdout, "- %s state=%s path_present=%t warnings=%d blockers=%d path=%s\n", report.Project.Alias, report.State, report.LocalPathPresent, len(report.Warnings), len(report.Blockers), report.Project.LocalPath)
	}
}

func printProjectStatus(w io.Writer, report readiness.ProjectReport) {
	fmt.Fprintf(w, "project: %s\n", report.Project.Alias)
	fmt.Fprintf(w, "state: %s\n", report.State)
	fmt.Fprintf(w, "path: %s\n", report.Project.LocalPath)
	fmt.Fprintf(w, "path_present: %t\n", report.LocalPathPresent)
	fmt.Fprintf(w, "remote: %s\n", report.Project.NormalizedRemote)
	fmt.Fprintf(w, "base: %s\n", report.BaseBranch)
	if len(report.Warnings) == 0 {
		fmt.Fprintln(w, "warnings: none")
	} else {
		for _, warning := range report.Warnings {
			fmt.Fprintf(w, "warning: %s %s\n", warning.Code, warning.Message)
		}
	}
	if len(report.Blockers) == 0 {
		fmt.Fprintln(w, "blockers: none")
	} else {
		for _, blocker := range report.Blockers {
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
  codemesh tree
  codemesh status [project] [--base branch]
  codemesh hydrate <project>
  codemesh agent prepare <project> [--base branch] [--profile name]

Commands:
  init       create local CodeMesh state
  add        add one Git project to the registry
  scan       scan a workspace root for Git projects
  tree       show the canonical workspace
  status     report project readiness
  hydrate    clone a missing project into its desired path
  agent      prepare agent workspaces

Planned MVP commands:
  runs, clean
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
  codemesh tree
`)
}

func printStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Report project readiness.

Usage:
  codemesh status [project] [--base branch]

Project defaults to all known projects.
Base defaults to main.
`)
}

func printHydrateHelp(w io.Writer) {
	fmt.Fprint(w, `Hydrate one missing project.

Usage:
  codemesh hydrate <project>

Clones the registered remote into the desired local path.
Refuses existing non-empty non-Git paths.
`)
}

func printAgentHelp(w io.Writer) {
	fmt.Fprint(w, `Prepare agent workspaces.

Usage:
  codemesh agent prepare <project> [--base branch] [--profile name]
`)
}

func printAgentPrepareHelp(w io.Writer) {
	fmt.Fprint(w, `Prepare one agent workspace.

Usage:
  codemesh agent prepare <project> [--base branch] [--profile name]

Creates a temporary clone under CodeMesh-managed agents storage.
Prints ready_path when the workspace is ready.
`)
}
