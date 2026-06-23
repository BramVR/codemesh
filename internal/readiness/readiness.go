package readiness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BramVR/codemesh/internal/registry"
	"github.com/BramVR/codemesh/internal/state"
)

type State string

const (
	StatePresent State = "present"
	StateMissing State = "missing"
	StateDirty   State = "dirty"
	StateStale   State = "stale"
	StateBlocked State = "blocked"
)

type Options struct {
	BaseBranch  string
	CheckRemote bool
}

type Diagnostic struct {
	Code    string
	Message string
}

type ProjectReport struct {
	Project          state.Project
	State            State
	LocalPathPresent bool
	BaseBranch       string
	Warnings         []Diagnostic
	Blockers         []Diagnostic
}

func EvaluateProject(ctx context.Context, project state.Project, opts Options) (ProjectReport, error) {
	base := strings.TrimSpace(opts.BaseBranch)
	if base == "" {
		base = "main"
	}
	report := ProjectReport{
		Project:    project,
		State:      StatePresent,
		BaseBranch: base,
	}

	info, err := os.Stat(project.LocalPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.State = StateMissing
			report.Blockers = append(report.Blockers, Diagnostic{
				Code:    "missing-path",
				Message: fmt.Sprintf("local path does not exist: %s", project.LocalPath),
			})
			return report, nil
		}
		return ProjectReport{}, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}
	report.LocalPathPresent = true
	if !info.IsDir() {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "path-not-directory",
			Message: fmt.Sprintf("local path is not a directory: %s", project.LocalPath),
		})
		return report, nil
	}

	dirty, err := gitOutput(ctx, project.LocalPath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "git-status-failed",
			Message: err.Error(),
		})
		return report, nil
	}
	if strings.TrimSpace(dirty) != "" {
		report.State = StateDirty
		report.Warnings = append(report.Warnings, Diagnostic{
			Code:    "dirty-checkout",
			Message: "source checkout has uncommitted or untracked changes",
		})
	}

	if !opts.CheckRemote {
		return report, nil
	}
	if err := validateBaseBranch(ctx, base); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "invalid-base",
			Message: err.Error(),
		})
		return report, nil
	}
	if remote, err := gitOutput(ctx, project.LocalPath, "config", "--get", "remote.origin.url"); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "origin-missing",
			Message: err.Error(),
		})
		return report, nil
	} else if normalized, err := registry.NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "origin-unsupported",
			Message: err.Error(),
		})
		return report, nil
	} else if normalized != project.NormalizedRemote {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "remote-mismatch",
			Message: fmt.Sprintf("origin remote %q does not match registered remote %q", normalized, project.NormalizedRemote),
		})
		return report, nil
	}
	if _, err := gitOutput(ctx, project.LocalPath, "fetch", "--quiet", "origin", "refs/heads/"+base); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			report.State = StateBlocked
			report.Blockers = append(report.Blockers, Diagnostic{
				Code:    "missing-base",
				Message: fmt.Sprintf("base branch %q was not found on origin", base),
			})
			return report, nil
		}
		report.State = StateStale
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "fetch-failed",
			Message: err.Error(),
		})
		return report, nil
	}
	remoteCommit, err := gitOutput(ctx, project.LocalPath, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "missing-base",
			Message: fmt.Sprintf("base branch %q was not found on origin", base),
		})
		return report, nil
	}
	localRef := "refs/heads/" + base
	if _, err := gitOutput(ctx, project.LocalPath, "rev-parse", "--verify", "--quiet", localRef); err == nil {
		if _, err := gitOutput(ctx, project.LocalPath, "merge-base", "--is-ancestor", strings.TrimSpace(remoteCommit), localRef); err != nil {
			report.State = StateStale
			report.Warnings = append(report.Warnings, Diagnostic{
				Code:    "stale-checkout",
				Message: fmt.Sprintf("local base branch %q is behind origin/%s", base, base),
			})
		}
	}
	return report, nil
}

func validateBaseBranch(ctx context.Context, base string) error {
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", base)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("invalid base branch %q: %s", base, detail)
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}
	return string(output), nil
}
