package clonestrategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/BramVR/codemesh/internal/gitops"
)

const (
	FullCloneName          = "full-clone"
	PartialCloneName       = "partial-clone"
	SparseCheckoutName     = "sparse-checkout"
	PartialSparseCloneName = "partial-sparse-clone"
)

type Selection struct {
	Name        string   `json:"name"`
	History     string   `json:"history"`
	WorkingTree string   `json:"working_tree"`
	Filter      string   `json:"filter,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
}

type Request struct {
	CloneURL    string
	Destination string
	Branch      string
	Options     Options
}

type Options struct {
	Partial     bool
	SparsePaths []string
}

type Result struct {
	Strategy Selection
}

type CloneError struct {
	Strategy Selection
	CloneURL string
	Detail   string
}

func (e CloneError) Error() string {
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		detail = "git clone failed"
	}
	return detail
}

type FullClone struct {
	Git gitops.Client
}

func FullCloneSelection() Selection {
	return Selection{
		Name:        FullCloneName,
		History:     "full",
		WorkingTree: "complete",
	}
}

func NormalizeSelection(selection Selection) Selection {
	selection.Name = strings.TrimSpace(selection.Name)
	selection.History = strings.TrimSpace(selection.History)
	selection.WorkingTree = strings.TrimSpace(selection.WorkingTree)
	selection.Filter = strings.TrimSpace(selection.Filter)
	selection.SparsePaths = NormalizeSparsePaths(selection.SparsePaths)
	if selection.Name == "" {
		return FullCloneSelection()
	}
	return selection
}

func SelectionForOptions(options Options) Selection {
	sparsePaths := NormalizeSparsePaths(options.SparsePaths)
	switch {
	case options.Partial && len(sparsePaths) != 0:
		return Selection{
			Name:        PartialSparseCloneName,
			History:     "partial",
			WorkingTree: "sparse",
			Filter:      "blob:none",
			SparsePaths: sparsePaths,
		}
	case options.Partial:
		return Selection{
			Name:        PartialCloneName,
			History:     "partial",
			WorkingTree: "complete",
			Filter:      "blob:none",
		}
	case len(sparsePaths) != 0:
		return Selection{
			Name:        SparseCheckoutName,
			History:     "full",
			WorkingTree: "sparse",
			SparsePaths: sparsePaths,
		}
	default:
		return FullCloneSelection()
	}
}

func NormalizeSparsePaths(paths []string) []string {
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, item := range paths {
		path := strings.TrimSpace(strings.ReplaceAll(item, "\\", "/"))
		path = strings.Trim(path, "/")
		if path == "" || path == "." || strings.HasPrefix(path, "../") || path == ".." {
			continue
		}
		if strings.ContainsAny(path, "*?[") {
			continue
		}
		parts := strings.Split(path, "/")
		unsafe := false
		for _, part := range parts {
			if part == "" || part == "." || part == ".." || part == ".git" {
				unsafe = true
				break
			}
		}
		if unsafe || seen[path] {
			continue
		}
		normalized = append(normalized, path)
		seen[path] = true
	}
	return normalized
}

func (s FullClone) Clone(ctx context.Context, req Request) (Result, error) {
	selection := SelectionForOptions(req.Options)
	cloneURL := req.CloneURL
	destination := req.Destination
	if strings.TrimSpace(cloneURL) == "" {
		return Result{Strategy: selection}, fmt.Errorf("clone URL is required")
	}
	if strings.TrimSpace(destination) == "" {
		return Result{Strategy: selection}, fmt.Errorf("clone destination is required")
	}
	git := s.Git
	if git == (gitops.Client{}) {
		git = gitops.Process()
	}
	args := []string{"clone"}
	if selection.Filter != "" {
		args = append(args, "--filter="+selection.Filter)
	}
	if len(selection.SparsePaths) != 0 {
		args = append(args, "--no-checkout")
	}
	if branch := strings.TrimSpace(req.Branch); branch != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, "--", cloneURL, destination)
	output, err := git.OutputDetail(ctx, "", args...)
	if err != nil {
		detail := gitops.RedactCloneOutput(gitops.CommandDetail(err), cloneURL)
		return Result{Strategy: selection}, CloneError{
			Strategy: selection,
			CloneURL: gitops.RedactURLForMetadata(cloneURL),
			Detail:   detail,
		}
	}
	if selection.Filter != "" {
		if filterWarningIgnored(output.Stderr) {
			return Result{Strategy: selection}, CloneError{
				Strategy: selection,
				CloneURL: gitops.RedactURLForMetadata(cloneURL),
				Detail:   "git partial clone filter was not honored by remote",
			}
		}
		if err := verifyPartialFilter(ctx, git, destination, selection.Filter); err != nil {
			return Result{Strategy: selection}, CloneError{
				Strategy: selection,
				CloneURL: gitops.RedactURLForMetadata(cloneURL),
				Detail:   err.Error(),
			}
		}
	}
	if len(selection.SparsePaths) != 0 {
		sparseArgs := append([]string{"sparse-checkout", "set", "--no-cone", "--"}, sparseCheckoutPatterns(selection.SparsePaths)...)
		if _, err := git.Output(ctx, destination, sparseArgs...); err != nil {
			detail := gitops.RedactCloneOutput(gitops.CommandDetail(err), cloneURL)
			return Result{Strategy: selection}, CloneError{
				Strategy: selection,
				CloneURL: gitops.RedactURLForMetadata(cloneURL),
				Detail:   detail,
			}
		}
		checkoutArgs := []string{"checkout"}
		if branch := strings.TrimSpace(req.Branch); branch != "" {
			checkoutArgs = append(checkoutArgs, branch)
		}
		if _, err := git.Output(ctx, destination, checkoutArgs...); err != nil {
			detail := gitops.RedactCloneOutput(gitops.CommandDetail(err), cloneURL)
			return Result{Strategy: selection}, CloneError{
				Strategy: selection,
				CloneURL: gitops.RedactURLForMetadata(cloneURL),
				Detail:   detail,
			}
		}
	}
	return Result{Strategy: selection}, nil
}

func sparseCheckoutPatterns(paths []string) []string {
	patterns := make([]string, 0, len(paths))
	for _, path := range paths {
		patterns = append(patterns, "/"+path)
	}
	return patterns
}

func verifyPartialFilter(ctx context.Context, git gitops.Client, destination, filter string) error {
	promisor, err := git.Output(ctx, destination, "config", "--get", "remote.origin.promisor")
	if err != nil || strings.TrimSpace(promisor) != "true" {
		return fmt.Errorf("git partial clone filter was not recorded by clone")
	}
	recordedFilter, err := git.Output(ctx, destination, "config", "--get", "remote.origin.partialclonefilter")
	if err != nil || strings.TrimSpace(recordedFilter) != filter {
		return fmt.Errorf("git partial clone filter = %q, want %q", strings.TrimSpace(recordedFilter), filter)
	}
	return nil
}

func filterWarningIgnored(stderr string) bool {
	detail := strings.ToLower(stderr)
	if !strings.Contains(detail, "filter") {
		return false
	}
	return strings.Contains(detail, "ignored") || strings.Contains(detail, "not recognized") || strings.Contains(detail, "not supported")
}
