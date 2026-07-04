package workspacemanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func LoadEntries(path string) ([]Entry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("workspace manifest path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat workspace manifest path: %w", err)
	}
	if !info.IsDir() {
		entry, err := loadEntryFile(path)
		if err != nil {
			return nil, err
		}
		return []Entry{entry}, nil
	}

	var files []string
	if err := filepath.WalkDir(path, func(candidate string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(candidate), ".json") {
			files = append(files, candidate)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk workspace manifest directory: %w", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("workspace manifest directory contains no JSON entries: %s", path)
	}
	entries := make([]Entry, 0, len(files))
	for _, file := range files {
		entry, err := loadEntryFile(file)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func loadEntryFile(path string) (Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read workspace manifest entry %q: %w", path, err)
	}
	entry, err := DecodeEntry(data)
	if err != nil {
		return Entry{}, fmt.Errorf("%s: %w", path, err)
	}
	return entry, nil
}
