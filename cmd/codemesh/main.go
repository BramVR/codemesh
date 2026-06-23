package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/BramVR/codemesh/internal/config"
	"github.com/BramVR/codemesh/internal/registry"
	"github.com/BramVR/codemesh/internal/state"
)

const version = "0.0.0-dev"

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
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
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

	entries, err := registry.New(store).Entries(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "read project registry: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "canonical workspace:")
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "(empty)")
		return 0
	}
	for _, entry := range entries {
		fmt.Fprintf(stdout, "- %s %s %s\n", entry.Project.Alias, entry.State, entry.Project.LocalPath)
	}
	return 0
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

Commands:
  init       create local CodeMesh state
  add        add one Git project to the registry
  scan       scan a workspace root for Git projects
  tree       show the canonical workspace

Planned MVP commands:
  status, hydrate, agent prepare, runs, clean
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
