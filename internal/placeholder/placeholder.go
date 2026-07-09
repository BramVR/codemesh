package placeholder

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/state"
)

const (
	Kind         = "codemesh-placeholder"
	Schema       = 1
	MetadataFile = ".codemesh-placeholder.json"
	NoteFile     = "CODEMESH_PLACEHOLDER.txt"
	GitBarrier   = ".git"
)

type Metadata struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Project       string `json:"project"`
	Identity      string `json:"identity"`
	CanonicalPath string `json:"canonical_path"`
}

type MaterializedProject struct {
	Project string `json:"project"`
	Path    string `json:"path"`
}

func SentinelFiles() []string {
	return []string{MetadataFile, GitBarrier, NoteFile}
}

func Is(path string) (Metadata, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, false, nil
		}
		return Metadata{}, false, err
	}
	if !info.IsDir() {
		return Metadata{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(path, MetadataFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, false, nil
		}
		return Metadata{}, true, fmt.Errorf("read placeholder metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, true, fmt.Errorf("parse placeholder metadata: %w", err)
	}
	if err := validate(metadata); err != nil {
		return Metadata{}, true, err
	}
	return metadata, true, nil
}

func OwnedBy(path, alias, identity string) (bool, error) {
	return OwnedByAllowing(path, alias, identity, "")
}

func OwnedByAllowing(path, alias, identity, allowedPath string) (bool, error) {
	metadata, ok, err := Is(path)
	if err != nil || !ok {
		return false, err
	}
	expected := Metadata{
		SchemaVersion: Schema,
		Kind:          Kind,
		Project:       alias,
		Identity:      identity,
		CanonicalPath: path,
	}
	if metadata != expected {
		return false, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read placeholder directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if allowedPath != "" && filepath.Clean(filepath.Join(path, entry.Name())) == filepath.Clean(allowedPath) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if strings.Join(names, "\x00") != strings.Join(SentinelFiles(), "\x00") {
		return false, nil
	}
	data, err := os.ReadFile(filepath.Join(path, MetadataFile))
	if err != nil {
		return false, fmt.Errorf("read placeholder metadata: %w", err)
	}
	expectedData, err := metadataBytes(expected)
	if err != nil {
		return false, err
	}
	if string(data) != string(expectedData) {
		return false, nil
	}
	note, err := os.ReadFile(filepath.Join(path, NoteFile))
	if err != nil {
		return false, fmt.Errorf("read placeholder note: %w", err)
	}
	if string(note) != noteText(alias) {
		return false, nil
	}
	barrier, err := os.ReadFile(filepath.Join(path, GitBarrier))
	if err != nil {
		return false, fmt.Errorf("read placeholder Git barrier: %w", err)
	}
	return string(barrier) == gitBarrierText(alias), nil
}

func Write(project state.Project) (MaterializedProject, error) {
	path := strings.TrimSpace(project.LocalPath)
	if path == "" {
		path = strings.TrimSpace(project.CanonicalPath)
	}
	if path == "" {
		return MaterializedProject{}, errors.New("placeholder path is required")
	}
	metadata := Metadata{
		SchemaVersion: Schema,
		Kind:          Kind,
		Project:       project.Alias,
		Identity:      project.NormalizedRemote,
		CanonicalPath: path,
	}
	if err := validate(metadata); err != nil {
		return MaterializedProject{}, err
	}
	if existing, ok, err := Is(path); err != nil {
		return MaterializedProject{}, err
	} else if ok {
		if existing.Project != metadata.Project || existing.Identity != metadata.Identity {
			return MaterializedProject{}, fmt.Errorf("placeholder belongs to %q", existing.Project)
		}
		owned, err := OwnedBy(path, metadata.Project, metadata.Identity)
		if err != nil {
			return MaterializedProject{}, err
		}
		if !owned {
			return MaterializedProject{}, errors.New("placeholder directory has local changes")
		}
		return MaterializedProject{Project: project.Alias, Path: path}, nil
	}
	if entries, err := os.ReadDir(path); err == nil && len(entries) != 0 {
		return MaterializedProject{}, errors.New("desired path exists and is not empty")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return MaterializedProject{}, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return MaterializedProject{}, fmt.Errorf("create placeholder directory: %w", err)
	}
	data, err := metadataBytes(metadata)
	if err != nil {
		return MaterializedProject{}, err
	}
	if err := os.WriteFile(filepath.Join(path, MetadataFile), data, 0o644); err != nil {
		return MaterializedProject{}, fmt.Errorf("write placeholder metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(path, NoteFile), []byte(noteText(project.Alias)), 0o644); err != nil {
		return MaterializedProject{}, fmt.Errorf("write placeholder note: %w", err)
	}
	if err := os.WriteFile(filepath.Join(path, GitBarrier), []byte(gitBarrierText(project.Alias)), 0o644); err != nil {
		return MaterializedProject{}, fmt.Errorf("write placeholder Git barrier: %w", err)
	}
	return MaterializedProject{Project: project.Alias, Path: path}, nil
}

func metadataBytes(metadata Metadata) ([]byte, error) {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func noteText(alias string) string {
	return "CodeMesh placeholder\n\nThis directory is metadata-only and is not a Git checkout. Run codemesh hydrate " + alias + " to clone source content.\n"
}

func gitBarrierText(alias string) string {
	return "CodeMesh placeholder for " + alias + ": this path is not a Git checkout.\n"
}

func validate(metadata Metadata) error {
	if metadata.SchemaVersion != Schema || metadata.Kind != Kind {
		return errors.New("invalid CodeMesh placeholder metadata")
	}
	if strings.TrimSpace(metadata.Project) == "" || strings.TrimSpace(metadata.Identity) == "" {
		return errors.New("incomplete CodeMesh placeholder metadata")
	}
	return nil
}
