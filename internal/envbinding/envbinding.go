package envbinding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/state"
)

const (
	ProviderFake = "fake"

	StatusNotRequested   = "not_requested"
	StatusMaterialized   = "materialized"
	StatusMissingBinding = "missing_binding"
	StatusDenied         = "denied"
)

type Store interface {
	ListEnvBindings(context.Context, int64) ([]state.EnvBinding, error)
}

type Request struct {
	ProjectID     int64
	RequiredFiles []string
	RequiredKeys  []string
	Provider      string
	AllowedScopes []string
	BundlePath    string
}

type Summary struct {
	Requirements          []Requirement
	AllowedScopes         []string
	MaterializationStatus string
	Bundle                Bundle
}

type Requirement struct {
	Name string
	Kind string
}

type Bundle struct {
	Present bool
	Path    string
	Format  string
	Values  string
}

type Diagnostic struct {
	Code    string
	Message string
}

type Materializer struct {
	Store    Store
	Provider Provider
}

type Provider interface {
	Name() string
	Resolve(context.Context, []state.EnvBinding) (map[string]string, error)
}

func (m Materializer) Materialize(ctx context.Context, req Request) (Summary, []Diagnostic, error) {
	summary := SummaryForRequirements(req.RequiredFiles, req.RequiredKeys, req.AllowedScopes)
	providerName := strings.TrimSpace(req.Provider)
	if providerName == "" || len(req.RequiredKeys) == 0 {
		return summary, nil, nil
	}
	if m.Store == nil {
		return summary, nil, errors.New("env binding store is required")
	}
	provider := m.Provider
	if provider == nil {
		provider = FakeProvider{}
	}
	if provider.Name() != providerName {
		summary.MaterializationStatus = StatusDenied
		return summary, []Diagnostic{{
			Code:    "env-provider-unsupported",
			Message: fmt.Sprintf("env provider %q is not supported by this materialization path", providerName),
		}}, nil
	}
	if strings.TrimSpace(req.BundlePath) == "" {
		return summary, nil, errors.New("env bundle path is required")
	}
	bindings, err := m.Store.ListEnvBindings(ctx, req.ProjectID)
	if err != nil {
		return summary, nil, err
	}
	selected, diagnostics := selectBindings(req.RequiredKeys, providerName, summary.AllowedScopes, bindings)
	if len(diagnostics) != 0 {
		summary.MaterializationStatus = statusFromDiagnostics(diagnostics)
		return summary, diagnostics, nil
	}
	values, err := provider.Resolve(ctx, selected)
	if err != nil {
		return summary, nil, err
	}
	if err := WriteBundle(req.BundlePath, values); err != nil {
		return summary, nil, err
	}
	summary.MaterializationStatus = StatusMaterialized
	summary.Bundle = Bundle{Present: true, Path: req.BundlePath, Format: "dotenv", Values: "not-recorded"}
	return summary, nil, nil
}

func SummaryForRequirements(requiredFiles, requiredKeys, allowedScopes []string) Summary {
	requirements := make([]Requirement, 0, len(requiredFiles)+len(requiredKeys))
	for _, file := range uniqueSorted(requiredFiles) {
		requirements = append(requirements, Requirement{Name: file, Kind: "env_file"})
	}
	for _, key := range uniqueSorted(requiredKeys) {
		requirements = append(requirements, Requirement{Name: key, Kind: "env_key"})
	}
	return Summary{
		Requirements:          requirements,
		AllowedScopes:         uniqueSorted(allowedScopes),
		MaterializationStatus: StatusNotRequested,
		Bundle:                Bundle{Values: "not-recorded"},
	}
}

type FakeProvider struct{}

func (FakeProvider) Name() string {
	return ProviderFake
}

func (FakeProvider) Resolve(_ context.Context, bindings []state.EnvBinding) (map[string]string, error) {
	values := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if err := ValidateFakeReference(binding.SecretRef); err != nil {
			return nil, err
		}
		values[binding.Requirement] = FakeProviderValue(binding.SecretRef)
	}
	return values, nil
}

func ValidateFakeReference(ref string) error {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "fake://") || strings.TrimPrefix(ref, "fake://") == "" {
		return errors.New("fake provider secret reference must use fake://name")
	}
	return nil
}

func FakeProviderValue(ref string) string {
	name := strings.TrimPrefix(strings.TrimSpace(ref), "fake://")
	name = strings.Trim(regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(name, "_"), "_")
	if name == "" {
		name = "value"
	}
	return "codemesh_fake_secret_" + name
}

func WriteBundle(path string, values map[string]string) error {
	if len(values) == 0 {
		return errors.New("env bundle requires at least one value")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create env bundle directory: %w", err)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(values[key])
		builder.WriteByte('\n')
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create env bundle: %w", err)
	}
	_, writeErr := file.WriteString(builder.String())
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write env bundle: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close env bundle: %w", closeErr)
	}
	return nil
}

func selectBindings(requiredKeys []string, provider string, allowedScopes []string, bindings []state.EnvBinding) ([]state.EnvBinding, []Diagnostic) {
	byRequirement := make(map[string]state.EnvBinding, len(bindings))
	for _, binding := range bindings {
		if binding.Provider == provider {
			byRequirement[binding.Requirement] = binding
		}
	}
	selected := make([]state.EnvBinding, 0, len(requiredKeys))
	var diagnostics []Diagnostic
	for _, key := range uniqueSorted(requiredKeys) {
		binding, ok := byRequirement[key]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{
				Code:    "missing-env-binding",
				Message: fmt.Sprintf("env requirement %s has no private %s provider binding", key, provider),
			})
			continue
		}
		if !scopesIntersect(allowedScopes, binding.Scopes) {
			diagnostics = append(diagnostics, Diagnostic{
				Code:    "env-scope-denied",
				Message: fmt.Sprintf("env requirement %s is bound to scopes [%s], but allowed scopes are [%s]", key, strings.Join(uniqueSorted(binding.Scopes), ","), strings.Join(allowedScopes, ",")),
			})
			continue
		}
		selected = append(selected, binding)
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return selected, nil
}

func statusFromDiagnostics(diagnostics []Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "env-scope-denied" {
			return StatusDenied
		}
	}
	return StatusMissingBinding
}

func scopesIntersect(allowed, binding []string) bool {
	if len(allowed) == 0 || len(binding) == 0 {
		return false
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, scope := range allowed {
		allowedSet[scope] = true
	}
	for _, scope := range binding {
		if allowedSet[strings.TrimSpace(scope)] {
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
