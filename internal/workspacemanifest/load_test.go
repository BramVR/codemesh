package workspacemanifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEntriesReadsManifestDirectory(t *testing.T) {
	dir := t.TempDir()
	first := NewEntry(ProjectEntry{
		Identity:    "https://github.com/BramVR/alpha",
		Alias:       "alpha",
		DesiredPath: "tools/alpha",
	})
	second := NewEntry(ProjectEntry{
		Identity:    "https://github.com/BramVR/beta",
		Alias:       "beta",
		DesiredPath: "beta",
	})
	writeEntry(t, filepath.Join(dir, "b-beta.json"), second)
	writeEntry(t, filepath.Join(dir, "a-alpha.json"), first)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadEntries(dir)
	if err != nil {
		t.Fatalf("LoadEntries error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(entries))
	}
	if entries[0].Project.Alias != "alpha" || entries[1].Project.Alias != "beta" {
		t.Fatalf("entries = %#v, want sorted JSON entries", entries)
	}
}

func writeEntry(t *testing.T, path string, entry Entry) {
	t.Helper()
	data, err := EncodeEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
