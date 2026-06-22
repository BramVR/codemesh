package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/BramVR/codemesh/internal/config"
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
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
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

Commands:
  init       create local CodeMesh state

Planned MVP commands:
  scan, add, tree, status, hydrate, agent prepare, runs, clean
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
