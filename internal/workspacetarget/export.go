package workspacetarget

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/policy"
	"github.com/BramVR/codemesh/internal/state"
	"github.com/BramVR/codemesh/internal/workspacemanifest"
)

const SpecVersion = 1

type Store interface {
	ListProjects(context.Context) ([]state.Project, error)
	ListMachines(context.Context) ([]state.Machine, error)
	ListEnvBindings(context.Context, int64) ([]state.EnvBinding, error)
}

type Options struct {
	ProducerVersion string
	TargetName      string
	TargetKind      string
	WorkspaceRoot   string
	Scopes          []string
}

type Spec struct {
	TargetSpecVersion int                       `json:"target_spec_version"`
	Producer          Producer                  `json:"producer"`
	Target            Target                    `json:"target"`
	Machine           MachineFacts              `json:"machine"`
	Topology          []workspacemanifest.Entry `json:"topology"`
	EnvPolicy         []ProjectEnvPolicy        `json:"env_policy"`
}

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Target struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	WorkspaceRoot string   `json:"workspace_root"`
	Scopes        []string `json:"scopes"`
}

type MachineFacts struct {
	ID            string `json:"id"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	WorkspaceRoot string `json:"workspace_root"`
}

type ProjectEnvPolicy struct {
	Project ProjectRef `json:"project"`
	Env     EnvPolicy  `json:"env"`
}

type ProjectRef struct {
	Identity    string `json:"identity"`
	Alias       string `json:"alias"`
	DesiredPath string `json:"desired_path"`
}

type EnvPolicy struct {
	Mode          string          `json:"mode"`
	RequiredFiles []string        `json:"required_files"`
	RequiredKeys  []string        `json:"required_keys"`
	Bindings      []EnvBindingRef `json:"bindings"`
}

type EnvBindingRef struct {
	Requirement string   `json:"requirement"`
	Provider    string   `json:"provider"`
	SecretRef   string   `json:"secret_ref"`
	Scopes      []string `json:"scopes"`
	Values      string   `json:"values"`
}

func Export(ctx context.Context, store Store, opts Options) (Spec, error) {
	if store == nil {
		return Spec{}, errors.New("workspace target store is required")
	}
	targetName := strings.TrimSpace(opts.TargetName)
	if targetName == "" {
		return Spec{}, errors.New("target name is required")
	}
	targetKind := strings.TrimSpace(opts.TargetKind)
	if targetKind == "" {
		targetKind = "agent"
	}
	scopes := uniqueSorted(opts.Scopes)
	if len(scopes) == 0 {
		return Spec{}, errors.New("at least one target scope is required")
	}

	machines, err := store.ListMachines(ctx)
	if err != nil {
		return Spec{}, err
	}
	if len(machines) == 0 {
		return Spec{}, errors.New("target export requires machine registration")
	}
	machine := machines[0]
	targetRoot := strings.TrimSpace(opts.WorkspaceRoot)
	if targetRoot == "" {
		targetRoot = machine.WorkspaceRoot
	}

	projects, err := store.ListProjects(ctx)
	if err != nil {
		return Spec{}, err
	}
	topology, err := workspacemanifest.ExportProjects(projects, machine.WorkspaceRoot)
	if err != nil {
		return Spec{}, err
	}
	byIdentity := make(map[string]state.Project, len(projects))
	for _, project := range projects {
		byIdentity[project.NormalizedRemote] = project
	}

	envPolicies := make([]ProjectEnvPolicy, 0, len(topology))
	for _, entry := range topology {
		project, ok := byIdentity[entry.Project.Identity]
		if !ok {
			return Spec{}, fmt.Errorf("target export project %q missing from registry", entry.Project.Alias)
		}
		projectPolicy, err := exportPolicy(project.LocalPath)
		if err != nil {
			return Spec{}, fmt.Errorf("target export policy for %q: %w", project.Alias, err)
		}
		bindings, err := store.ListEnvBindings(ctx, project.ID)
		if err != nil {
			return Spec{}, err
		}
		envPolicies = append(envPolicies, ProjectEnvPolicy{
			Project: ProjectRef{
				Identity:    entry.Project.Identity,
				Alias:       entry.Project.Alias,
				DesiredPath: entry.Project.DesiredPath,
			},
			Env: EnvPolicy{
				Mode:          string(projectPolicy.Env.Mode),
				RequiredFiles: uniqueSorted(projectPolicy.Env.RequiredFiles),
				RequiredKeys:  uniqueSorted(projectPolicy.Env.RequiredKeys),
				Bindings:      scopedBindingRefs(bindings, scopes),
			},
		})
	}

	return Spec{
		TargetSpecVersion: SpecVersion,
		Producer: Producer{
			Name:    "codemesh",
			Version: opts.ProducerVersion,
		},
		Target: Target{
			Name:          targetName,
			Kind:          targetKind,
			WorkspaceRoot: targetRoot,
			Scopes:        scopes,
		},
		Machine: MachineFacts{
			ID:            machine.ID,
			Hostname:      machine.Hostname,
			OS:            machine.OS,
			Architecture:  machine.Architecture,
			WorkspaceRoot: machine.WorkspaceRoot,
		},
		Topology:  topology,
		EnvPolicy: envPolicies,
	}, nil
}

func exportPolicy(projectPath string) (policy.Policy, error) {
	info, err := os.Stat(projectPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return policy.Defaults(), nil
		}
		return policy.Policy{}, err
	}
	if !info.IsDir() {
		return policy.Defaults(), nil
	}
	return policy.Resolve(projectPath)
}

func scopedBindingRefs(bindings []state.EnvBinding, targetScopes []string) []EnvBindingRef {
	refs := make([]EnvBindingRef, 0, len(bindings))
	for _, binding := range bindings {
		scopes := uniqueSorted(binding.Scopes)
		if !scopesIntersect(targetScopes, scopes) {
			continue
		}
		refs = append(refs, EnvBindingRef{
			Requirement: strings.TrimSpace(binding.Requirement),
			Provider:    strings.TrimSpace(binding.Provider),
			SecretRef:   strings.TrimSpace(binding.SecretRef),
			Scopes:      scopes,
			Values:      "not-recorded",
		})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Requirement == refs[j].Requirement {
			if refs[i].Provider == refs[j].Provider {
				return refs[i].SecretRef < refs[j].SecretRef
			}
			return refs[i].Provider < refs[j].Provider
		}
		return refs[i].Requirement < refs[j].Requirement
	})
	return refs
}

func scopesIntersect(targetScopes, bindingScopes []string) bool {
	set := make(map[string]bool, len(targetScopes))
	for _, scope := range targetScopes {
		set[scope] = true
	}
	for _, scope := range bindingScopes {
		if set[scope] {
			return true
		}
	}
	return false
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
