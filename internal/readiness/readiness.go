package readiness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/policy"
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
	Env         EnvLookup
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

type EnvLookup interface {
	HasEnvKey(string) bool
}

type processEnv struct{}

func EvaluateProject(ctx context.Context, project state.Project, opts Options) (ProjectReport, error) {
	requestedBase := strings.TrimSpace(opts.BaseBranch)
	base := requestedBase
	if base == "" {
		base = policy.Defaults().BaseBranch
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

	projectPolicy, err := policy.Resolve(project.LocalPath)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "invalid-policy",
			Message: err.Error(),
		})
		return report, nil
	}
	if requestedBase == "" {
		base = projectPolicy.BaseBranch
	}
	report.BaseBranch = base

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

	if checkEnvReadiness(&report, projectPolicy, envLookup(opts.Env)); len(report.Blockers) != 0 {
		return report, nil
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
	remote, err := gitOutput(ctx, project.LocalPath, "remote", "get-url", "origin")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "origin-missing",
			Message: err.Error(),
		})
		return report, nil
	}
	normalized, err := gitops.NormalizeRemoteFrom(strings.TrimSpace(remote), project.LocalPath)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "origin-unsupported",
			Message: err.Error(),
		})
		return report, nil
	}
	if normalized != project.NormalizedRemote {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "remote-mismatch",
			Message: fmt.Sprintf("origin remote %q does not match registered remote %q", normalized, project.NormalizedRemote),
		})
		return report, nil
	}
	if _, err := gitOutput(ctx, project.LocalPath, "fetch", "--quiet", "origin", "refs/heads/"+base); err != nil {
		if gitops.IsMissingRemoteRef(err) {
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
			Message: gitops.RedactCloneOutput(err.Error(), strings.TrimSpace(remote)),
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
				Message: fmt.Sprintf("local base branch %q is behind or diverged from origin/%s", base, base),
			})
		}
	}
	return report, nil
}

func checkEnvReadiness(report *ProjectReport, projectPolicy policy.Policy, env EnvLookup) {
	var missing []Diagnostic
	for _, requiredFile := range projectPolicy.Env.RequiredFiles {
		path := filepath.Join(report.Project.LocalPath, requiredFile)
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, Diagnostic{
					Code:    "missing-env-file",
					Message: fmt.Sprintf("required env file is missing: %s", requiredFile),
				})
				continue
			}
			missing = append(missing, Diagnostic{
				Code:    "missing-env-file",
				Message: fmt.Sprintf("required env file cannot be checked: %s: %v", requiredFile, err),
			})
			continue
		}
		if !info.Mode().IsRegular() {
			missing = append(missing, Diagnostic{
				Code:    "invalid-env-file",
				Message: fmt.Sprintf("required env file is not a regular file: %s", requiredFile),
			})
		}
	}
	for _, key := range projectPolicy.Env.RequiredKeys {
		if !env.HasEnvKey(key) {
			missing = append(missing, Diagnostic{
				Code:    "missing-env-key",
				Message: fmt.Sprintf("required env key is missing: %s", key),
			})
		}
	}
	if len(missing) == 0 {
		return
	}
	if projectPolicy.Env.Mode == policy.EnvModeBlock {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, missing...)
		return
	}
	report.Warnings = append(report.Warnings, missing...)
}

func envLookup(env EnvLookup) EnvLookup {
	if env != nil {
		return env
	}
	return processEnv{}
}

func (processEnv) HasEnvKey(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

func validateBaseBranch(ctx context.Context, base string) error {
	err := gitops.Process().ValidateBranchName(ctx, base)
	if err != nil {
		return err
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	return gitops.Process().Output(ctx, dir, args...)
}
