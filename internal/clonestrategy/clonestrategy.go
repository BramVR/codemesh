package clonestrategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/BramVR/codemesh/internal/gitops"
)

const FullCloneName = "full-clone"

type Selection struct {
	Name        string `json:"name"`
	History     string `json:"history"`
	WorkingTree string `json:"working_tree"`
}

type Request struct {
	CloneURL    string
	Destination string
	Branch      string
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
	if selection.Name == "" {
		return FullCloneSelection()
	}
	return selection
}

func (s FullClone) Clone(ctx context.Context, req Request) (Result, error) {
	selection := FullCloneSelection()
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
	if branch := strings.TrimSpace(req.Branch); branch != "" {
		args = append(args, "--branch", branch, "--single-branch")
	}
	args = append(args, cloneURL, destination)
	if _, err := git.Output(ctx, "", args...); err != nil {
		detail := gitops.RedactCloneOutput(gitops.CommandDetail(err), cloneURL)
		return Result{Strategy: selection}, CloneError{
			Strategy: selection,
			CloneURL: gitops.RedactURLForMetadata(cloneURL),
			Detail:   detail,
		}
	}
	return Result{Strategy: selection}, nil
}
