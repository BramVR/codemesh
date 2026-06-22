package registry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/state"
)

type Store interface {
	AddProject(context.Context, state.Project) (state.Project, error)
	ListProjects(context.Context) ([]state.Project, error)
}

type Registry struct {
	store Store
}

type Entry struct {
	Project state.Project
	State   string
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
