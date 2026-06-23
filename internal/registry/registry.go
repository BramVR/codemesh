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

type HydrateResult struct {
	Project        state.Project
	AlreadyPresent bool
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
		CloneURL:         cloneURLFor(inspected.Remote, inspected.Root),
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
			CloneURL:         cloneURLFor(inspected.Remote, inspected.Root),
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

func (r *Registry) Hydrate(ctx context.Context, alias string) (HydrateResult, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return HydrateResult{}, errors.New("project name is required")
	}
	projects, err := r.store.ListProjects(ctx)
	if err != nil {
		return HydrateResult{}, err
	}
	for _, project := range projects {
		if project.Alias != alias {
			continue
		}
		alreadyPresent, err := hydrateProject(ctx, project)
		if err != nil {
			return HydrateResult{}, err
		}
		return HydrateResult{Project: project, AlreadyPresent: alreadyPresent}, nil
	}
	return HydrateResult{}, fmt.Errorf("unknown project: %s", alias)
}

func hydrateProject(ctx context.Context, project state.Project) (bool, error) {
	info, err := os.Stat(project.LocalPath)
	switch {
	case err == nil:
		if gitCheckoutMatches(project) {
			return true, nil
		}
		if !info.IsDir() {
			return false, fmt.Errorf("path conflict: %s exists and is not a directory", project.LocalPath)
		}
		empty, err := dirIsEmpty(project.LocalPath)
		if err != nil {
			return false, err
		}
		if !empty {
			return false, fmt.Errorf("path conflict: %s exists and is not empty", project.LocalPath)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(project.LocalPath), 0o755); err != nil {
			return false, fmt.Errorf("create project parent directory: %w", err)
		}
	default:
		return false, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}

	cloneURL := project.CloneURL
	if cloneURL == "" {
		cloneURL = project.NormalizedRemote
	}
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, project.LocalPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("clone %q into %q: %s", redactedCloneURL(cloneURL), project.LocalPath, redactedCloneOutput(string(output), cloneURL))
	}
	return false, nil
}

func gitCheckoutMatches(project state.Project) bool {
	inside, err := gitOutput(project.LocalPath, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(inside) != "true" {
		return false
	}
	root, err := gitOutput(project.LocalPath, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(strings.TrimSpace(root), project.LocalPath) {
		return false
	}
	remote, err := gitOutput(project.LocalPath, "remote", "get-url", "origin")
	if err != nil {
		return false
	}
	normalized, err := NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath)
	if err != nil {
		return false
	}
	return normalized == project.NormalizedRemote
}

func cloneURLFor(remote, baseDir string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return remote
	}
	if _, _, _, ok := splitSCPLikeRemote(remote); ok {
		return remote
	}
	parsed, err := url.Parse(remote)
	if err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if parsed.Scheme == "http" || parsed.Scheme == "https" {
				parsed.User = nil
				return parsed.String()
			}
			if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.User(parsed.User.Username())
				return parsed.String()
			}
		}
		return remote
	}
	if baseDir != "" && !filepath.IsAbs(remote) {
		return filepath.Clean(filepath.Join(baseDir, remote))
	}
	return remote
}

func redactedCloneURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.User == nil {
		return raw
	}
	parsed.User = url.User("redacted")
	return parsed.String()
}

func redactedCloneOutput(output, cloneURL string) string {
	detail := strings.TrimSpace(output)
	redacted := redactedCloneURL(cloneURL)
	if redacted != cloneURL {
		detail = strings.ReplaceAll(detail, cloneURL, redacted)
	}
	return detail
}

func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if realA, err := filepath.EvalSymlinks(a); err == nil {
		a = realA
	}
	if realB, err := filepath.EvalSymlinks(b); err == nil {
		b = realB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func dirIsEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, fmt.Errorf("read project path %q: %w", path, err)
	}
	return len(entries) == 0, nil
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
