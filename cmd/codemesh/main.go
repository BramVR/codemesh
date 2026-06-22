package main

import (
	"fmt"
	"io"
	"os"
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
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `CodeMesh augments Git and local filesystems for agent-ready workspaces.

Usage:
  codemesh [--help]
  codemesh [--version]

Planned MVP commands:
  init, scan, add, tree, status, hydrate, agent prepare, runs, clean
`)
}
