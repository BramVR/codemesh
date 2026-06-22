package config

import (
	"path/filepath"
	"testing"
)

func TestResolveHomeUsesOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "env-home")
	t.Setenv("CODEMESH_HOME", want)

	home, err := ResolveHome()

	if err != nil {
		t.Fatalf("ResolveHome error = %v", err)
	}
	if home != want {
		t.Fatalf("home = %q, want %q", home, want)
	}
}

func TestResolveHomeDefaultsUnderUserHome(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("CODEMESH_HOME", "")
	t.Setenv("HOME", homeRoot)

	home, err := ResolveHome()

	if err != nil {
		t.Fatalf("ResolveHome error = %v", err)
	}
	if want := filepath.Join(homeRoot, ".codemesh"); home != want {
		t.Fatalf("home = %q, want %q", home, want)
	}
}

func TestResolvePathsDerivesDatabaseAndAgentsDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEMESH_HOME", root)

	paths, err := ResolvePaths()

	if err != nil {
		t.Fatalf("ResolvePaths error = %v", err)
	}
	if paths.Home != root {
		t.Fatalf("home = %q, want %q", paths.Home, root)
	}
	if want := filepath.Join(root, "codemesh.db"); paths.Database != want {
		t.Fatalf("database = %q, want %q", paths.Database, want)
	}
	if want := filepath.Join(root, "agents"); paths.AgentsDir != want {
		t.Fatalf("agents dir = %q, want %q", paths.AgentsDir, want)
	}
}
