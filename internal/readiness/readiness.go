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

type HandoffDecision struct {
	Report            ProjectReport
	Policy            policy.Policy
	SourcePathMissing bool
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
	remote, ok := evaluateSourceRemote(ctx, &report)
	if !ok {
		return report, nil
	}
	fetchSourceBase(ctx, &report, remote)
	return report, nil
}

func EvaluateHandoff(ctx context.Context, project state.Project, opts Options) (HandoffDecision, error) {
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
			return evaluateMissingSourceHandoff(ctx, report, requestedBase, opts)
		}
		return HandoffDecision{}, fmt.Errorf("check project path %q: %w", project.LocalPath, err)
	}
	report.LocalPathPresent = true
	if !info.IsDir() {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "path-not-directory",
			Message: fmt.Sprintf("local path is not a directory: %s", project.LocalPath),
		})
		return HandoffDecision{Report: report, Policy: policy.Defaults()}, nil
	}

	sourcePolicy := policy.Defaults()
	if requestedBase == "" {
		var err error
		sourcePolicy, err = policy.Resolve(project.LocalPath)
		if err != nil {
			report.State = StateBlocked
			report.Blockers = append(report.Blockers, Diagnostic{
				Code:    "invalid-policy",
				Message: err.Error(),
			})
			return HandoffDecision{Report: report}, nil
		}
		base = sourcePolicy.BaseBranch
		report.BaseBranch = base
	}

	dirty, err := gitOutput(ctx, project.LocalPath, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "git-status-failed",
			Message: err.Error(),
		})
		return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
	}
	if strings.TrimSpace(dirty) != "" {
		report.State = StateDirty
		report.Warnings = append(report.Warnings, Diagnostic{
			Code:    "dirty-checkout",
			Message: "source checkout has uncommitted or untracked changes",
		})
	}

	remote, ok := evaluateSourceRemote(ctx, &report)
	if !ok {
		return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
	}
	if !fetchSourceBase(ctx, &report, remote) {
		return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
	}
	readyPolicy, err := policyFromFetchedBase(ctx, project.LocalPath, report.BaseBranch)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "invalid-policy",
			Message: err.Error(),
		})
		return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
	}
	checkEnvReadiness(&report, readyPolicy, envLookup(opts.Env))
	return HandoffDecision{Report: report, Policy: readyPolicy}, nil
}

func evaluateMissingSourceHandoff(ctx context.Context, report ProjectReport, requestedBase string, opts Options) (HandoffDecision, error) {
	base := requestedBase
	if base == "" {
		base = policy.Defaults().BaseBranch
		if defaultBranch, err := remoteDefaultBranch(ctx, cloneURL(report.Project)); err == nil {
			base = defaultBranch
			if remotePolicy, err := policyFromRemoteBase(ctx, cloneURL(report.Project), defaultBranch); err == nil {
				base = remotePolicy.BaseBranch
			} else if !strings.Contains(err.Error(), "clone policy probe") {
				return HandoffDecision{}, err
			}
		}
	}
	report.BaseBranch = base
	if err := validateBaseBranch(ctx, base); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-base", Message: err.Error()})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	clone := cloneURL(report.Project)
	if clone == "" {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "origin-missing", Message: "registered project has no clone URL"})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	refs, err := gitOutput(ctx, "", "ls-remote", clone, "refs/heads/"+base)
	if err != nil {
		report.State = StateStale
		report.Blockers = append(report.Blockers, Diagnostic{Code: "fetch-failed", Message: gitops.RedactCloneOutput(gitops.CommandDetail(err), clone)})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	if strings.TrimSpace(refs) == "" {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "missing-base",
			Message: fmt.Sprintf("base branch %q was not found on origin", base),
		})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	readyPolicy, err := policyFromRemoteBase(ctx, clone, base)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-policy", Message: err.Error()})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	checkEnvReadiness(&report, readyPolicy, envLookup(opts.Env))
	return HandoffDecision{Report: report, Policy: readyPolicy, SourcePathMissing: true}, nil
}

func evaluateSourceRemote(ctx context.Context, report *ProjectReport) (string, bool) {
	if err := validateBaseBranch(ctx, report.BaseBranch); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-base", Message: err.Error()})
		return "", false
	}
	remote, err := gitOutput(ctx, report.Project.LocalPath, "remote", "get-url", "origin")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "origin-missing", Message: err.Error()})
		return "", false
	}
	remote = strings.TrimSpace(remote)
	normalized, err := gitops.NormalizeRemoteFrom(remote, report.Project.LocalPath)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "origin-unsupported", Message: err.Error()})
		return "", false
	}
	if normalized != report.Project.NormalizedRemote {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "remote-mismatch",
			Message: fmt.Sprintf("origin remote %q does not match registered remote %q", normalized, report.Project.NormalizedRemote),
		})
		return "", false
	}
	return remote, true
}

func fetchSourceBase(ctx context.Context, report *ProjectReport, remote string) bool {
	base := report.BaseBranch
	if _, err := gitOutput(ctx, report.Project.LocalPath, "fetch", "--quiet", "origin", "refs/heads/"+base); err != nil {
		if gitops.IsMissingRemoteRef(err) {
			report.State = StateBlocked
			report.Blockers = append(report.Blockers, Diagnostic{
				Code:    "missing-base",
				Message: fmt.Sprintf("base branch %q was not found on origin", base),
			})
			return false
		}
		report.State = StateStale
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "fetch-failed",
			Message: gitops.RedactCloneOutput(err.Error(), remote),
		})
		return false
	}
	remoteCommit, err := gitOutput(ctx, report.Project.LocalPath, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{
			Code:    "missing-base",
			Message: fmt.Sprintf("base branch %q was not found on origin", base),
		})
		return false
	}
	localRef := "refs/heads/" + base
	if _, err := gitOutput(ctx, report.Project.LocalPath, "rev-parse", "--verify", "--quiet", localRef); err == nil {
		if _, err := gitOutput(ctx, report.Project.LocalPath, "merge-base", "--is-ancestor", strings.TrimSpace(remoteCommit), localRef); err != nil {
			report.State = StateStale
			report.Warnings = append(report.Warnings, Diagnostic{
				Code:    "stale-checkout",
				Message: fmt.Sprintf("local base branch %q is behind or diverged from origin/%s", base, base),
			})
		}
	}
	return true
}

func policyFromFetchedBase(ctx context.Context, repo, base string) (policy.Policy, error) {
	data, err := gitOutput(ctx, repo, "show", "FETCH_HEAD:"+policy.FileName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return policy.Defaults(), nil
		}
		return policy.Policy{}, err
	}
	return policy.ParseBytes("origin/"+base+":"+policy.FileName, []byte(data))
}

func PolicyFromCheckout(ctx context.Context, repo string) (policy.Policy, error) {
	data, err := gitOutput(ctx, repo, "show", "HEAD:"+policy.FileName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "exists on disk, but not in") {
			return policy.Defaults(), nil
		}
		return policy.Policy{}, err
	}
	return policy.ParseBytes("HEAD:"+policy.FileName, []byte(data))
}

func policyFromRemoteBase(ctx context.Context, clone, base string) (policy.Policy, error) {
	probeRoot, err := os.MkdirTemp("", "codemesh-readiness-policy-*")
	if err != nil {
		return policy.Policy{}, fmt.Errorf("create policy probe: %w", err)
	}
	defer os.RemoveAll(probeRoot)
	probePath := filepath.Join(probeRoot, "workspace")
	if _, err := gitOutput(ctx, "", "clone", "--quiet", "--branch", base, "--single-branch", clone, probePath); err != nil {
		return policy.Policy{}, fmt.Errorf("clone policy probe: %s", gitops.RedactCloneOutput(gitops.CommandDetail(err), clone))
	}
	return PolicyFromCheckout(ctx, probePath)
}

func remoteDefaultBranch(ctx context.Context, clone string) (string, error) {
	if clone == "" {
		return "", errors.New("registered project has no clone URL")
	}
	output, err := gitOutput(ctx, "", "ls-remote", "--symref", clone, "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "ref: refs/heads/") || !strings.HasSuffix(line, "\tHEAD") {
			continue
		}
		branch := strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD")
		if branch != "" {
			return branch, nil
		}
	}
	return "", errors.New("remote HEAD did not advertise a default branch")
}

func cloneURL(project state.Project) string {
	if project.CloneURL != "" {
		return project.CloneURL
	}
	return project.NormalizedRemote
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
