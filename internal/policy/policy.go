package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/toolchain"
	"gopkg.in/yaml.v3"
)

const FileName = ".codemesh.yml"

type EnvMode string

const (
	EnvModeWarn  EnvMode = "warn"
	EnvModeBlock EnvMode = "block"
)

type Policy struct {
	BaseBranch    string
	BaseBranchSet bool
	Env           EnvPolicy
	Toolchain     ToolchainPolicy
	IncludeDocs   []string
	LocalOnly     LocalOnlyPolicy
}

type EnvPolicy struct {
	Mode          EnvMode
	RequiredFiles []string
	RequiredKeys  []string
}

type ToolchainPolicy struct {
	Mode         EnvMode
	Requirements []string
}

type LocalOnlyPolicy struct {
	Paths []PathRule
}

type PathRule struct {
	Path     string       `json:"path"`
	Category PathCategory `json:"category"`
}

type PathCategory string

const (
	PathCategoryDependency PathCategory = "dependency"
	PathCategoryBuild      PathCategory = "build"
	PathCategoryCache      PathCategory = "cache"
	PathCategoryGenerated  PathCategory = "generated"
	PathCategoryEnvConfig  PathCategory = "env-config"
	PathCategoryOSSpecific PathCategory = "os-specific"
	PathCategorySource     PathCategory = "source"
)

type policyFile struct {
	Agent     agentPolicy     `yaml:"agent"`
	LocalOnly localOnlyPolicy `yaml:"local_only"`
}

type agentPolicy struct {
	Base        string          `yaml:"base"`
	Env         envPolicy       `yaml:"env"`
	Toolchain   toolchainPolicy `yaml:"toolchain"`
	IncludeDocs []string        `yaml:"include_docs"`
}

type envPolicy struct {
	Mode          string   `yaml:"mode"`
	RequiredFiles []string `yaml:"required_files"`
	RequiredKeys  []string `yaml:"required_keys"`
}

type toolchainPolicy struct {
	Mode         string   `yaml:"mode"`
	Requirements []string `yaml:"requirements"`
}

type localOnlyPolicy struct {
	Paths []localOnlyPath `yaml:"paths"`
}

type localOnlyPath struct {
	Path     string `yaml:"path"`
	Category string `yaml:"category"`
}

func Defaults() Policy {
	return Policy{
		BaseBranch: "main",
		Env: EnvPolicy{
			Mode: EnvModeWarn,
		},
		Toolchain: ToolchainPolicy{
			Mode: EnvModeWarn,
		},
	}
}

func Resolve(root string) (Policy, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Policy{}, errors.New("project root is required")
	}
	path := filepath.Join(root, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Defaults(), nil
		}
		return Policy{}, fmt.Errorf("read %s: %w", path, err)
	}

	return ParseBytes(path, data)
}

func ParseBytes(path string, data []byte) (Policy, error) {
	var raw policyFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		return Policy{}, fmt.Errorf("invalid %s: parse YAML: %w", path, err)
	}
	p := Defaults()
	if base := strings.TrimSpace(raw.Agent.Base); base != "" {
		if err := validateBaseBranch(path, base); err != nil {
			return Policy{}, err
		}
		p.BaseBranch = base
		p.BaseBranchSet = true
	}
	if mode := strings.TrimSpace(raw.Agent.Env.Mode); mode != "" {
		switch EnvMode(mode) {
		case EnvModeWarn, EnvModeBlock:
			p.Env.Mode = EnvMode(mode)
		default:
			return Policy{}, fmt.Errorf("invalid %s: agent.env.mode must be %q or %q", path, EnvModeWarn, EnvModeBlock)
		}
	}
	if err := validateStrings(path, "agent.env.required_files", raw.Agent.Env.RequiredFiles); err != nil {
		return Policy{}, err
	}
	if err := validateRequiredFiles(path, raw.Agent.Env.RequiredFiles); err != nil {
		return Policy{}, err
	}
	if err := validateStrings(path, "agent.env.required_keys", raw.Agent.Env.RequiredKeys); err != nil {
		return Policy{}, err
	}
	if err := validateRequiredKeys(path, raw.Agent.Env.RequiredKeys); err != nil {
		return Policy{}, err
	}
	if mode := strings.TrimSpace(raw.Agent.Toolchain.Mode); mode != "" {
		switch EnvMode(mode) {
		case EnvModeWarn, EnvModeBlock:
			p.Toolchain.Mode = EnvMode(mode)
		default:
			return Policy{}, fmt.Errorf("invalid %s: agent.toolchain.mode must be %q or %q", path, EnvModeWarn, EnvModeBlock)
		}
	}
	if err := validateStrings(path, "agent.toolchain.requirements", raw.Agent.Toolchain.Requirements); err != nil {
		return Policy{}, err
	}
	if err := validateToolchainRequirements(path, raw.Agent.Toolchain.Requirements); err != nil {
		return Policy{}, err
	}
	if err := validateStrings(path, "agent.include_docs", raw.Agent.IncludeDocs); err != nil {
		return Policy{}, err
	}
	if err := validateIncludeDocs(path, raw.Agent.IncludeDocs); err != nil {
		return Policy{}, err
	}
	localOnlyPaths, err := parseLocalOnlyPaths(path, raw.LocalOnly.Paths)
	if err != nil {
		return Policy{}, err
	}
	p.Env.RequiredFiles = append([]string(nil), raw.Agent.Env.RequiredFiles...)
	p.Env.RequiredKeys = append([]string(nil), raw.Agent.Env.RequiredKeys...)
	p.Toolchain.Requirements = append([]string(nil), raw.Agent.Toolchain.Requirements...)
	p.IncludeDocs = append([]string(nil), raw.Agent.IncludeDocs...)
	p.LocalOnly.Paths = localOnlyPaths
	return p, nil
}

func validateBaseBranch(path, base string) error {
	_, err := gitops.Process().Output(context.Background(), "", "check-ref-format", "--branch", base)
	if err != nil {
		detail := gitops.CommandDetail(err)
		return fmt.Errorf("invalid %s: agent.base %q is not a valid Git branch name: %s", path, base, detail)
	}
	return nil
}

func validateRequiredFiles(path string, values []string) error {
	for i, value := range values {
		clean := filepath.Clean(value)
		if filepath.IsAbs(value) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid %s: agent.env.required_files[%d] must be relative to the project checkout", path, i)
		}
	}
	return nil
}

func validateRequiredKeys(path string, values []string) error {
	for i, value := range values {
		if strings.Contains(value, "=") || strings.TrimSpace(value) != value {
			return fmt.Errorf("invalid %s: agent.env.required_keys[%d] must be an env key name, not a value assignment", path, i)
		}
	}
	return nil
}

func validateToolchainRequirements(path string, values []string) error {
	for i, value := range values {
		if !toolchain.ValidRequirementName(value) {
			return fmt.Errorf("invalid %s: agent.toolchain.requirements[%d] must be a toolchain requirement name, not a command or path", path, i)
		}
	}
	return nil
}

func validateIncludeDocs(path string, values []string) error {
	for i, value := range values {
		clean := filepath.Clean(filepath.FromSlash(value))
		if filepath.IsAbs(value) || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid %s: agent.include_docs[%d] %q must be relative to the project checkout", path, i, value)
		}
	}
	return nil
}

func parseLocalOnlyPaths(path string, values []localOnlyPath) ([]PathRule, error) {
	rules := make([]PathRule, 0, len(values))
	for i, value := range values {
		field := fmt.Sprintf("local_only.paths[%d]", i)
		policyPath := strings.TrimSpace(value.Path)
		if policyPath == "" {
			return nil, fmt.Errorf("invalid %s: %s.path must not be empty", path, field)
		}
		if policyPath != value.Path {
			return nil, fmt.Errorf("invalid %s: %s.path %q must not have leading or trailing whitespace", path, field, value.Path)
		}
		clean := filepath.Clean(filepath.FromSlash(policyPath))
		if filepath.IsAbs(policyPath) || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("invalid %s: %s.path %q must be relative to the project checkout", path, field, policyPath)
		}
		category := PathCategory(strings.TrimSpace(value.Category))
		if category == "" {
			return nil, fmt.Errorf("invalid %s: %s.category must not be empty", path, field)
		}
		if category == PathCategorySource {
			return nil, fmt.Errorf("invalid %s: %s.category %q is ambiguous for local-only paths; source content must stay in Git-managed source", path, field, category)
		}
		if !validLocalOnlyCategory(category) {
			return nil, fmt.Errorf("invalid %s: %s.category %q must be dependency, build, cache, generated, env-config, os-specific, or source", path, field, category)
		}
		rules = append(rules, PathRule{Path: filepath.ToSlash(clean), Category: category})
	}
	return rules, nil
}

func validLocalOnlyCategory(category PathCategory) bool {
	switch category {
	case PathCategoryDependency, PathCategoryBuild, PathCategoryCache, PathCategoryGenerated, PathCategoryEnvConfig, PathCategoryOSSpecific, PathCategorySource:
		return true
	default:
		return false
	}
}

func validateStrings(path, field string, values []string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid %s: %s[%d] must not be empty", path, field, i)
		}
	}
	return nil
}
