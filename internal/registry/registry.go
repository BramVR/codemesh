package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/state"
)

type Store interface {
	AddProject(context.Context, state.Project) (state.Project, error)
	UpsertProject(context.Context, state.Project) (state.Project, state.ProjectUpsertAction, error)
	ListProjects(context.Context) ([]state.Project, error)
}

type Registry struct {
	store Store
}

type Entry struct {
	Project state.Project
	State   string
}

type ScanResult struct {
	WorkspaceRoot string
	Added         []state.Project
	Updated       []state.Project
	Unchanged     []state.Project
	Skipped       []ScanSkip
}

type ScanSkip struct {
	Path   string
	Reason string
}

func New(store Store) *Registry {
	return &Registry{store: store}
}

func (r *Registry) AddPath(ctx context.Context, path, alias string) (state.Project, error) {
	inspected, err := InspectGitProject(path)
	if err != nil {
		return state.Project{}, err
	}
	if alias == "" {
		alias = inspected.Alias
	}
	remote, err := NormalizeRemoteFrom(inspected.Remote, inspected.Root)
	if err != nil {
		return state.Project{}, err
	}
	return r.store.AddProject(ctx, state.Project{
		Alias:            alias,
		NormalizedRemote: remote,
		LocalPath:        inspected.Root,
	})
}

func (r *Registry) ScanWorkspace(ctx context.Context, root string) (ScanResult, error) {
	if strings.TrimSpace(root) == "" {
		return ScanResult{}, errors.New("workspace root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ScanResult{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = realRoot
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return ScanResult{}, fmt.Errorf("stat workspace root %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return ScanResult{}, fmt.Errorf("workspace root is not a directory: %s", absRoot)
	}

	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	aliases := make(map[string]bool, len(projects))
	for _, project := range projects {
		aliases[project.Alias] = true
	}

	result := ScanResult{WorkspaceRoot: absRoot}
	var discoveredRoots []string
	seenRemotes := make(map[string]string)
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: path, Reason: walkErr.Error()})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}

		projectPath := filepath.Dir(path)
		if isNestedProject(projectPath, discoveredRoots) {
			result.Skipped = append(result.Skipped, ScanSkip{Path: projectPath, Reason: "nested Git repo"})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		discoveredRoots = append(discoveredRoots, projectPath)

		inspected, err := InspectGitProject(projectPath)
		if err != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: projectPath, Reason: "unsupported Git repo: " + err.Error()})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		remote, err := NormalizeRemoteFrom(inspected.Remote, inspected.Root)
		if err != nil {
			result.Skipped = append(result.Skipped, ScanSkip{Path: inspected.Root, Reason: "unsupported Git remote: " + err.Error()})
			return filepath.SkipDir
		}
		if firstPath, ok := seenRemotes[remote]; ok {
			result.Skipped = append(result.Skipped, ScanSkip{Path: inspected.Root, Reason: "duplicate remote already scanned at " + firstPath})
			return filepath.SkipDir
		}
		alias := uniqueAlias(inspected.Alias, aliases)
		project, action, err := r.store.UpsertProject(ctx, state.Project{
			Alias:            alias,
			NormalizedRemote: remote,
			LocalPath:        inspected.Root,
		})
		if err != nil {
			return err
		}
		aliases[project.Alias] = true
		seenRemotes[remote] = inspected.Root
		discoveredRoots = append(discoveredRoots, inspected.Root)
		switch action {
		case state.ProjectUpsertAdded:
			result.Added = append(result.Added, project)
		case state.ProjectUpsertUpdated:
			result.Updated = append(result.Updated, project)
		case state.ProjectUpsertUnchanged:
			result.Unchanged = append(result.Unchanged, project)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return ScanResult{}, err
	}
	return result, nil
}

func (r *Registry) Entries(ctx context.Context) ([]Entry, error) {
	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(projects))
	for _, project := range projects {
		entryState := "present"
		if _, err := os.Stat(project.LocalPath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
			}
			entryState = "missing"
		}
		entries = append(entries, Entry{Project: project, State: entryState})
	}
	return entries, nil
}

func isNestedProject(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func uniqueAlias(alias string, used map[string]bool) string {
	if alias == "" {
		alias = "project"
	}
	if !used[alias] {
		return alias
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", alias, i)
		if !used[candidate] {
			return candidate
		}
	}
}

type InspectedGitProject struct {
	Root   string
	Remote string
	Alias  string
}

func InspectGitProject(path string) (InspectedGitProject, error) {
	if strings.TrimSpace(path) == "" {
		return InspectedGitProject{}, errors.New("project path is required")
	}
	root, err := gitOutput(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return InspectedGitProject{}, fmt.Errorf("inspect Git project: %w", err)
	}
	root = strings.TrimSpace(root)
	remote, err := gitOutput(root, "config", "--get", "remote.origin.url")
	if err != nil {
		return InspectedGitProject{}, fmt.Errorf("read origin remote: %w", err)
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return InspectedGitProject{}, errors.New("Git project has no origin remote")
	}
	return InspectedGitProject{
		Root:   root,
		Remote: remote,
		Alias:  strings.TrimSuffix(filepath.Base(strings.TrimSpace(root)), ".git"),
	}, nil
}

func NormalizeRemote(remote string) (string, error) {
	return NormalizeRemoteFrom(remote, "")
}

func NormalizeRemoteFrom(remote, baseDir string) (string, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return "", errors.New("remote is required")
	}
	if user, host, path, ok := splitSCPLikeRemote(remote); ok {
		if host == "github.com" {
			return normalizeGitHubPath(path)
		}
		path = strings.TrimPrefix(path, "/")
		path = strings.TrimSuffix(path, ".git")
		return fmt.Sprintf("ssh://%s@%s/%s", user, host, path), nil
	}

	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" {
		host := strings.ToLower(parsed.Hostname())
		if host == "github.com" {
			return normalizeGitHubPath(parsed.Path)
		}
		if parsed.Scheme == "file" {
			return filepath.Clean(parsed.Path), nil
		}
		host = strings.ToLower(parsed.Host)
		path := strings.TrimSuffix(parsed.EscapedPath(), ".git")
		if parsed.User != nil {
			return fmt.Sprintf("%s://%s@%s%s", parsed.Scheme, parsed.User.Username(), host, path), nil
		}
		return fmt.Sprintf("%s://%s%s", parsed.Scheme, host, path), nil
	}

	if baseDir != "" && !filepath.IsAbs(remote) {
		return filepath.Clean(filepath.Join(baseDir, remote)), nil
	}
	abs, err := filepath.Abs(remote)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func splitSCPLikeRemote(remote string) (string, string, string, bool) {
	if strings.Contains(remote, "://") {
		return "", "", "", false
	}
	at := strings.Index(remote, "@")
	if at <= 0 {
		return "", "", "", false
	}
	rest := remote[at+1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 || colon == len(rest)-1 {
		return "", "", "", false
	}
	user := remote[:at]
	host := strings.ToLower(rest[:colon])
	path := rest[colon+1:]
	return user, host, path, true
}

func normalizeGitHubPath(path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if path == "" || !strings.Contains(path, "/") {
		return "", fmt.Errorf("invalid GitHub remote path %q", path)
	}
	return "https://github.com/" + path, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return string(output), nil
}
