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
	"github.com/BramVR/codemesh/internal/toolchain"
)

type State string

const (
	StatePresent State = "present"
	StateMissing State = "missing"
	StateDirty   State = "dirty"
	StateStale   State = "stale"
	StateBlocked State = "blocked"
)

var errRemoteDefaultNotAdvertised = errors.New("remote HEAD did not advertise a default branch")

type Options struct {
	BaseBranch  string
	CheckRemote bool
	Env         EnvLookup
	Toolchain   toolchain.Detector
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
	FetchedBase      string
	FetchedCommit    string
	Toolchain        []toolchain.Result
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
	if requestedBase == "" && projectPolicy.BaseBranchSet {
		base = projectPolicy.BaseBranch
		report.BaseBranch = base
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
		if checkEnvReadiness(&report, projectPolicy, envLookup(opts.Env)); len(report.Blockers) != 0 {
			return report, nil
		}
		if err := checkToolchainReadiness(ctx, &report, projectPolicy, opts.Toolchain); err != nil {
			return ProjectReport{}, err
		}
		return report, nil
	}
	remote, ok := sourceRemote(ctx, &report)
	if !ok {
		return report, nil
	}
	if requestedBase == "" && !projectPolicy.BaseBranchSet {
		if !selectSourceRemoteDefaultOrFallback(ctx, &report, remote) {
			return report, nil
		}
	}
	if checkEnvReadiness(&report, projectPolicy, envLookup(opts.Env)); len(report.Blockers) != 0 {
		return report, nil
	}
	if err := checkToolchainReadiness(ctx, &report, projectPolicy, opts.Toolchain); err != nil {
		return ProjectReport{}, err
	}
	if !validateSelectedBase(ctx, &report) {
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
		if sourcePolicy.BaseBranchSet {
			base = sourcePolicy.BaseBranch
			report.BaseBranch = base
		}
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

	remote, ok := sourceRemote(ctx, &report)
	if !ok {
		return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
	}
	if requestedBase == "" && !sourcePolicy.BaseBranchSet {
		if !selectSourceRemoteDefaultOrFallback(ctx, &report, remote) {
			return HandoffDecision{Report: report, Policy: sourcePolicy}, nil
		}
		base = report.BaseBranch
	}
	if !validateSelectedBase(ctx, &report) {
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
	if len(report.Blockers) == 0 {
		if err := checkToolchainReadiness(ctx, &report, readyPolicy, opts.Toolchain); err != nil {
			return HandoffDecision{}, err
		}
	}
	return HandoffDecision{Report: report, Policy: readyPolicy}, nil
}

func evaluateMissingSourceHandoff(ctx context.Context, report ProjectReport, requestedBase string, opts Options) (HandoffDecision, error) {
	clone := strings.TrimSpace(report.Project.CloneURL)
	if clone == "" {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "origin-missing", Message: "registered project has no clone URL"})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	base := requestedBase
	if base == "" {
		base = policy.Defaults().BaseBranch
		if defaultBranch, err := remoteDefaultBranch(ctx, clone); err == nil {
			base = defaultBranch
			remotePolicy, err := policyFromRemoteBase(ctx, clone, defaultBranch)
			if err != nil && !strings.Contains(err.Error(), "clone policy probe") {
				return HandoffDecision{}, err
			}
			if err == nil && remotePolicy.BaseBranchSet {
				base = remotePolicy.BaseBranch
			}
		} else if !errors.Is(err, errRemoteDefaultNotAdvertised) {
			report.State = StateStale
			report.Blockers = append(report.Blockers, Diagnostic{Code: "fetch-failed", Message: gitops.RedactCloneOutput(gitops.CommandDetail(err), clone)})
			return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
		}
	}
	report.BaseBranch = base
	if err := validateBaseBranch(ctx, base); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-base", Message: err.Error()})
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
	report.FetchedBase = base
	report.FetchedCommit = remoteRefCommit(refs, base)
	readyPolicy, err := policyFromRemoteBase(ctx, clone, base)
	if err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-policy", Message: err.Error()})
		return HandoffDecision{Report: report, Policy: policy.Defaults(), SourcePathMissing: true}, nil
	}
	checkEnvReadiness(&report, readyPolicy, envLookup(opts.Env))
	if len(report.Blockers) == 0 {
		if err := checkToolchainReadiness(ctx, &report, readyPolicy, opts.Toolchain); err != nil {
			return HandoffDecision{}, err
		}
	}
	return HandoffDecision{Report: report, Policy: readyPolicy, SourcePathMissing: true}, nil
}

func evaluateSourceRemote(ctx context.Context, report *ProjectReport) (string, bool) {
	if !validateSelectedBase(ctx, report) {
		return "", false
	}
	return sourceRemote(ctx, report)
}

func validateSelectedBase(ctx context.Context, report *ProjectReport) bool {
	if err := validateBaseBranch(ctx, report.BaseBranch); err != nil {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, Diagnostic{Code: "invalid-base", Message: err.Error()})
		return false
	}
	return true
}

func sourceRemote(ctx context.Context, report *ProjectReport) (string, bool) {
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

func selectRemoteDefaultOrFallback(ctx context.Context, report *ProjectReport, remote string) bool {
	defaultBranch, err := remoteDefaultBranch(ctx, remote)
	return applyRemoteDefaultOrFallback(report, defaultBranch, err, remote)
}

func selectSourceRemoteDefaultOrFallback(ctx context.Context, report *ProjectReport, remote string) bool {
	defaultBranch, err := sourceRemoteDefaultBranch(ctx, report.Project.LocalPath)
	return applyRemoteDefaultOrFallback(report, defaultBranch, err, remote)
}

func applyRemoteDefaultOrFallback(report *ProjectReport, defaultBranch string, err error, redactedRemote string) bool {
	if err == nil {
		report.BaseBranch = defaultBranch
		return true
	}
	if errors.Is(err, errRemoteDefaultNotAdvertised) {
		report.BaseBranch = policy.Defaults().BaseBranch
		return true
	}
	report.State = StateStale
	report.Blockers = append(report.Blockers, Diagnostic{Code: "fetch-failed", Message: gitops.RedactCloneOutput(gitops.CommandDetail(err), redactedRemote)})
	return false
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
	report.FetchedBase = base
	report.FetchedCommit = strings.TrimSpace(remoteCommit)
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
	return remoteDefaultBranchFromOutput(output)
}

func sourceRemoteDefaultBranch(ctx context.Context, repo string) (string, error) {
	output, err := gitOutput(ctx, repo, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	return remoteDefaultBranchFromOutput(output)
}

func remoteDefaultBranchFromOutput(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "ref: refs/heads/") || !strings.HasSuffix(line, "\tHEAD") {
			continue
		}
		branch := strings.TrimSuffix(strings.TrimPrefix(line, "ref: refs/heads/"), "\tHEAD")
		if branch != "" {
			return branch, nil
		}
	}
	return "", errRemoteDefaultNotAdvertised
}

func remoteRefCommit(output, base string) string {
	wantRef := "refs/heads/" + base
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == wantRef {
			return fields[0]
		}
	}
	return ""
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

func checkToolchainReadiness(ctx context.Context, report *ProjectReport, projectPolicy policy.Policy, detector toolchain.Detector) error {
	detector = detectorWithProjectRoot(detector, report.Project.LocalPath)
	results, err := toolchain.Check(ctx, projectPolicy.Toolchain.Requirements, detector)
	if err != nil {
		return err
	}
	report.Toolchain = results
	var diagnostics []Diagnostic
	for _, result := range results {
		switch result.Status {
		case toolchain.StatusPresent:
			continue
		case toolchain.StatusMissing:
			diagnostics = append(diagnostics, Diagnostic{
				Code:    "missing-toolchain",
				Message: fmt.Sprintf("toolchain requirement is missing: %s; install or build environment setup is delegated outside CodeMesh", result.Name),
			})
		case toolchain.StatusUnknown:
			diagnostics = append(diagnostics, Diagnostic{
				Code:    "unknown-toolchain",
				Message: fmt.Sprintf("toolchain requirement status is unknown: %s; CodeMesh did not run installers or build environments", result.Name),
			})
		}
	}
	if len(diagnostics) == 0 {
		return nil
	}
	if projectPolicy.Toolchain.Mode == policy.EnvModeBlock {
		report.State = StateBlocked
		report.Blockers = append(report.Blockers, diagnostics...)
		return nil
	}
	report.Warnings = append(report.Warnings, diagnostics...)
	return nil
}

func detectorWithProjectRoot(detector toolchain.Detector, projectRoot string) toolchain.Detector {
	if strings.TrimSpace(projectRoot) == "" {
		return detector
	}
	switch d := detector.(type) {
	case toolchain.HostDetector:
		d.DenyDirs = append(append([]string(nil), d.DenyDirs...), projectRoot)
		return d
	case *toolchain.HostDetector:
		if d == nil {
			return detector
		}
		copy := *d
		copy.DenyDirs = append(append([]string(nil), copy.DenyDirs...), projectRoot)
		return copy
	default:
		return detector
	}
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
