package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) (string, error)
}

type ProcessRunner struct{}

type Client struct {
	runner Runner
}

type InspectedProject struct {
	Root   string
	Remote string
	Alias  string
}

type CommandError struct {
	Args   []string
	Output string
	Err    error
}

func New(runner Runner) Client {
	if runner == nil {
		runner = ProcessRunner{}
	}
	return Client{runner: runner}
}

func Process() Client {
	return New(ProcessRunner{})
}

func (ProcessRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	gitArgs := append([]string(nil), args...)
	if strings.TrimSpace(dir) != "" {
		gitArgs = append([]string{"-C", dir}, gitArgs...)
	}
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		output := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		return "", NewCommandError(args, []byte(output), err)
	}
	return stdout.String(), nil
}

func (c Client) Output(ctx context.Context, dir string, args ...string) (string, error) {
	return c.runner.Run(ctx, dir, args...)
}

func (c Client) InspectProject(ctx context.Context, path string) (InspectedProject, error) {
	if strings.TrimSpace(path) == "" {
		return InspectedProject{}, errors.New("project path is required")
	}
	root, err := c.Output(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return InspectedProject{}, fmt.Errorf("inspect Git project: %w", err)
	}
	root = strings.TrimSpace(root)
	remote, err := c.Output(ctx, root, "config", "--get", "remote.origin.url")
	if err != nil {
		return InspectedProject{}, fmt.Errorf("read origin remote: %w", err)
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return InspectedProject{}, errors.New("Git project has no origin remote")
	}
	return InspectedProject{
		Root:   root,
		Remote: remote,
		Alias:  strings.TrimSuffix(filepath.Base(root), ".git"),
	}, nil
}

func (c Client) ValidateBranchName(ctx context.Context, branch string) error {
	output, err := c.Output(ctx, "", "check-ref-format", "--branch", branch)
	if err == nil {
		return nil
	}
	detail := CommandDetail(err)
	if detail == "" {
		detail = strings.TrimSpace(output)
	}
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("invalid base branch %q: %s", branch, detail)
}

func NewCommandError(args []string, output []byte, err error) CommandError {
	return CommandError{
		Args:   append([]string(nil), args...),
		Output: strings.TrimSpace(string(output)),
		Err:    err,
	}
}

func (e CommandError) Error() string {
	detail := e.Output
	if detail == "" && e.Err != nil {
		detail = e.Err.Error()
	}
	return fmt.Sprintf("git %s failed: %s", strings.Join(e.Args, " "), detail)
}

func (e CommandError) Unwrap() error {
	return e.Err
}

func CommandDetail(err error) string {
	var commandErr CommandError
	if errors.As(err, &commandErr) {
		if commandErr.Output != "" {
			return commandErr.Output
		}
		if commandErr.Err != nil {
			return commandErr.Err.Error()
		}
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func IsMissingRemoteRef(err error) bool {
	return strings.Contains(CommandDetail(err), "couldn't find remote ref")
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

func CloneURLFor(remote, baseDir string) string {
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

func RedactURLForMetadata(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return raw
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func RedactCloneOutput(output, cloneURL string) string {
	redacted := RedactURLForMetadata(cloneURL)
	if redacted != cloneURL {
		output = strings.ReplaceAll(output, cloneURL, redacted)
		output = strings.ReplaceAll(output, cloneURL+"/", redacted+"/")
	}
	parsed, err := url.Parse(cloneURL)
	if err != nil || parsed.Scheme == "" {
		return strings.TrimSpace(output)
	}
	candidates := []string{}
	withoutUser := *parsed
	withoutUser.User = nil
	candidates = append(candidates, withoutUser.String())
	withoutFragment := *parsed
	withoutFragment.Fragment = ""
	candidates = append(candidates, withoutFragment.String())
	withoutUserFragment := withoutUser
	withoutUserFragment.Fragment = ""
	candidates = append(candidates, withoutUserFragment.String())
	withoutQuery := withoutFragment
	withoutQuery.RawQuery = ""
	candidates = append(candidates, withoutQuery.String())
	for _, candidate := range candidates {
		if candidate != "" && candidate != redacted {
			output = strings.ReplaceAll(output, candidate, redacted)
			output = strings.ReplaceAll(output, candidate+"/", redacted+"/")
		}
	}
	return strings.TrimSpace(output)
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

type Call struct {
	Dir  string
	Args []string
}

type FakeResponse struct {
	Output string
	Err    error
}

type FakeRunner struct {
	Calls     []Call
	Responses []FakeResponse
}

func (f *FakeRunner) Run(_ context.Context, dir string, args ...string) (string, error) {
	f.Calls = append(f.Calls, Call{Dir: dir, Args: append([]string(nil), args...)})
	if len(f.Responses) == 0 {
		return "", nil
	}
	response := f.Responses[0]
	f.Responses = f.Responses[1:]
	return response.Output, response.Err
}
