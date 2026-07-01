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
	"gopkg.in/yaml.v3"
)

const FileName = ".codemesh.yml"

type EnvMode string

const (
	EnvModeWarn  EnvMode = "warn"
	EnvModeBlock EnvMode = "block"
)

type Policy struct {
	BaseBranch  string
	Env         EnvPolicy
	IncludeDocs []string
}

type EnvPolicy struct {
	Mode          EnvMode
	RequiredFiles []string
	RequiredKeys  []string
}

type policyFile struct {
	Agent agentPolicy `yaml:"agent"`
}

type agentPolicy struct {
	Base        string    `yaml:"base"`
	Env         envPolicy `yaml:"env"`
	IncludeDocs []string  `yaml:"include_docs"`
}

type envPolicy struct {
	Mode          string   `yaml:"mode"`
	RequiredFiles []string `yaml:"required_files"`
	RequiredKeys  []string `yaml:"required_keys"`
}

func Defaults() Policy {
	return Policy{
		BaseBranch: "main",
		Env: EnvPolicy{
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
	if err := validateStrings(path, "agent.include_docs", raw.Agent.IncludeDocs); err != nil {
		return Policy{}, err
	}
	if err := validateIncludeDocs(path, raw.Agent.IncludeDocs); err != nil {
		return Policy{}, err
	}
	p.Env.RequiredFiles = append([]string(nil), raw.Agent.Env.RequiredFiles...)
	p.Env.RequiredKeys = append([]string(nil), raw.Agent.Env.RequiredKeys...)
	p.IncludeDocs = append([]string(nil), raw.Agent.IncludeDocs...)
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

func validateIncludeDocs(path string, values []string) error {
	for i, value := range values {
		clean := filepath.Clean(filepath.FromSlash(value))
		if filepath.IsAbs(value) || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid %s: agent.include_docs[%d] %q must be relative to the project checkout", path, i, value)
		}
	}
	return nil
}

func validateStrings(path, field string, values []string) error {
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("invalid %s: %s[%d] must not be empty", path, field, i)
		}
	}
	return nil
}
