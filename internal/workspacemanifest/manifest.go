package workspacemanifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BramVR/codemesh/internal/gitops"
	"github.com/BramVR/codemesh/internal/state"
)

const ManifestVersion = 1

type Entry struct {
	ManifestVersion int          `json:"manifest_version"`
	Project         ProjectEntry `json:"project"`
}

type ProjectEntry struct {
	Identity    string     `json:"identity"`
	Alias       string     `json:"alias"`
	DesiredPath string     `json:"desired_path"`
	CloneHints  CloneHints `json:"clone_hints"`
	Groups      []string   `json:"groups"`
}

type CloneHints struct {
	URL string `json:"url,omitempty"`
}

type ImportPlan struct {
	Changes []ImportChange `json:"changes"`
}

type ChangeAction string

const (
	ChangeAdd       ChangeAction = "add"
	ChangeUpdate    ChangeAction = "update"
	ChangeUnchanged ChangeAction = "unchanged"
	ChangeConflict  ChangeAction = "conflict"
)

type ImportChange struct {
	Action         ChangeAction  `json:"action"`
	ProjectID      int64         `json:"project_id,omitempty"`
	Identity       string        `json:"identity"`
	Alias          string        `json:"alias"`
	DesiredPath    string        `json:"desired_path"`
	LocalPath      string        `json:"local_path"`
	CloneURL       string        `json:"clone_url"`
	Fields         []FieldChange `json:"fields"`
	ConflictReason string        `json:"conflict_reason,omitempty"`
}

type FieldChange struct {
	Field   string `json:"field"`
	Current string `json:"current"`
	Desired string `json:"desired"`
}

func NewEntry(project ProjectEntry) Entry {
	return normalizeEntry(Entry{ManifestVersion: ManifestVersion, Project: project})
}

func EncodeEntry(entry Entry) ([]byte, error) {
	entry = normalizeEntry(entry)
	if err := ValidateEntry(entry); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace manifest entry: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeEntry(data []byte) (Entry, error) {
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, fmt.Errorf("decode workspace manifest entry: %w", err)
	}
	entry = normalizeEntry(entry)
	if err := ValidateEntry(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func ValidateEntry(entry Entry) error {
	if entry.ManifestVersion != ManifestVersion {
		return fmt.Errorf("workspace manifest entry version must be %d", ManifestVersion)
	}
	if strings.TrimSpace(entry.Project.Identity) == "" {
		return errors.New("workspace manifest project identity is required")
	}
	if hasUnsafeURLUserinfo(entry.Project.Identity) {
		return errors.New("workspace manifest project identity must not contain credentials")
	}
	if hasURLQueryOrFragment(entry.Project.Identity) {
		return errors.New("workspace manifest project identity must not contain query strings or fragments")
	}
	if isLocalReference(entry.Project.Identity) {
		return errors.New("workspace manifest project identity must not be a machine-local path")
	}
	if strings.TrimSpace(entry.Project.Alias) == "" {
		return errors.New("workspace manifest project alias is required")
	}
	if err := validateDesiredPath(entry.Project.DesiredPath); err != nil {
		return err
	}
	if err := validateCloneHint(entry.Project.CloneHints.URL); err != nil {
		return err
	}
	return nil
}

func ExportProjects(projects []state.Project, workspaceRoot string) ([]Entry, error) {
	root, err := cleanWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(projects))
	for _, project := range projects {
		desiredPath, err := relativeDesiredPath(root, project.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("export project %q: %w", project.Alias, err)
		}
		entry := NewEntry(ProjectEntry{
			Identity:    project.NormalizedRemote,
			Alias:       project.Alias,
			DesiredPath: desiredPath,
			CloneHints:  CloneHints{URL: manifestCloneHint(project.CloneURL)},
			Groups:      []string{},
		})
		if err := ValidateEntry(entry); err != nil {
			return nil, fmt.Errorf("export project %q: %w", project.Alias, err)
		}
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Project.DesiredPath == entries[j].Project.DesiredPath {
			return entries[i].Project.Identity < entries[j].Project.Identity
		}
		return entries[i].Project.DesiredPath < entries[j].Project.DesiredPath
	})
	return entries, nil
}

func PlanImport(entries []Entry, projects []state.Project, workspaceRoot string) (ImportPlan, error) {
	root, err := cleanWorkspaceRoot(workspaceRoot)
	if err != nil {
		return ImportPlan{}, err
	}
	normalizedEntries := make([]Entry, 0, len(entries))
	desiredAliasByIdentity := make(map[string]string, len(entries))
	for _, raw := range entries {
		entry := normalizeEntry(raw)
		if err := ValidateEntry(entry); err != nil {
			return ImportPlan{}, err
		}
		if _, exists := desiredAliasByIdentity[entry.Project.Identity]; !exists {
			desiredAliasByIdentity[entry.Project.Identity] = entry.Project.Alias
		}
		normalizedEntries = append(normalizedEntries, entry)
	}
	byIdentity := make(map[string]state.Project, len(projects))
	aliasOwner := make(map[string]string, len(projects))
	for _, project := range projects {
		byIdentity[project.NormalizedRemote] = project
		if desiredAlias, ok := desiredAliasByIdentity[project.NormalizedRemote]; ok && desiredAlias != project.Alias {
			continue
		}
		aliasOwner[project.Alias] = project.NormalizedRemote
	}

	plan := ImportPlan{Changes: []ImportChange{}}
	seenManifestIdentities := map[string]bool{}
	for _, entry := range normalizedEntries {
		desiredLocalPath := filepath.Join(root, filepath.FromSlash(entry.Project.DesiredPath))
		desiredCloneURL := entry.Project.CloneHints.URL
		if desiredCloneURL == "" {
			desiredCloneURL = entry.Project.Identity
		}
		change := ImportChange{
			Identity:    entry.Project.Identity,
			Alias:       entry.Project.Alias,
			DesiredPath: entry.Project.DesiredPath,
			LocalPath:   desiredLocalPath,
			CloneURL:    desiredCloneURL,
		}
		if seenManifestIdentities[entry.Project.Identity] {
			change.Action = ChangeConflict
			change.ConflictReason = fmt.Sprintf("identity %q appears more than once in the manifest", entry.Project.Identity)
			plan.Changes = append(plan.Changes, change)
			continue
		}
		seenManifestIdentities[entry.Project.Identity] = true
		if owner, ok := aliasOwner[entry.Project.Alias]; ok && owner != entry.Project.Identity {
			change.Action = ChangeConflict
			change.ConflictReason = fmt.Sprintf("alias %q already belongs to %q", entry.Project.Alias, owner)
			plan.Changes = append(plan.Changes, change)
			continue
		}
		project, ok := byIdentity[entry.Project.Identity]
		if !ok {
			change.Action = ChangeAdd
			aliasOwner[entry.Project.Alias] = entry.Project.Identity
			plan.Changes = append(plan.Changes, change)
			continue
		}
		change.ProjectID = project.ID
		if project.Alias != entry.Project.Alias {
			change.Fields = append(change.Fields, FieldChange{Field: "alias", Current: project.Alias, Desired: entry.Project.Alias})
		}
		if filepath.Clean(project.LocalPath) != filepath.Clean(desiredLocalPath) {
			change.Fields = append(change.Fields, FieldChange{Field: "local_path", Current: project.LocalPath, Desired: desiredLocalPath})
		}
		if entry.Project.CloneHints.URL != "" && project.CloneURL != entry.Project.CloneHints.URL {
			change.Fields = append(change.Fields, FieldChange{Field: "clone_url", Current: cloneURLForPlan(project.CloneURL), Desired: entry.Project.CloneHints.URL})
		}
		if len(change.Fields) == 0 {
			change.Action = ChangeUnchanged
		} else {
			change.Action = ChangeUpdate
		}
		aliasOwner[entry.Project.Alias] = entry.Project.Identity
		plan.Changes = append(plan.Changes, change)
	}
	return plan, nil
}

func normalizeEntry(entry Entry) Entry {
	if entry.ManifestVersion == 0 {
		entry.ManifestVersion = ManifestVersion
	}
	entry.Project.Identity = strings.TrimSpace(entry.Project.Identity)
	if entry.Project.Identity != "" {
		if normalized, err := gitops.NormalizeRemote(entry.Project.Identity); err == nil {
			entry.Project.Identity = normalized
		}
	}
	entry.Project.Alias = strings.TrimSpace(entry.Project.Alias)
	entry.Project.DesiredPath = normalizeDesiredPath(entry.Project.DesiredPath)
	entry.Project.CloneHints.URL = strings.TrimSpace(entry.Project.CloneHints.URL)
	entry.Project.Groups = normalizedGroups(entry.Project.Groups)
	return entry
}

func normalizedGroups(groups []string) []string {
	normalized := make([]string, 0, len(groups))
	seen := map[string]bool{}
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" || seen[group] {
			continue
		}
		seen[group] = true
		normalized = append(normalized, group)
	}
	if normalized == nil {
		return []string{}
	}
	return normalized
}

func validateDesiredPath(path string) error {
	if path == "" {
		return errors.New("workspace manifest desired path is required")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") {
		return fmt.Errorf("workspace manifest desired path must be relative: %s", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("workspace manifest desired path must stay inside the workspace: %s", path)
	}
	return nil
}

func normalizeDesiredPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
}

func cleanWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	return filepath.Clean(abs), nil
}

func relativeDesiredPath(root, localPath string) (string, error) {
	if strings.TrimSpace(localPath) == "" {
		return "", errors.New("local path is required")
	}
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return "", fmt.Errorf("resolve local path: %w", err)
	}
	rel, err := filepath.Rel(root, filepath.Clean(absPath))
	if err != nil {
		return "", fmt.Errorf("derive desired path: %w", err)
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("local path %q is outside workspace root %q", localPath, root)
	}
	return filepath.ToSlash(rel), nil
}

func manifestCloneHint(cloneURL string) string {
	cloneURL = strings.TrimSpace(cloneURL)
	if cloneURL == "" || isLocalCloneURL(cloneURL) {
		return ""
	}
	hint, err := sanitizedCloneHint(cloneURL)
	if err != nil {
		return ""
	}
	return hint
}

func isLocalCloneURL(cloneURL string) bool {
	return isLocalReference(cloneURL)
}

func isLocalReference(value string) bool {
	if isWindowsDrivePath(value) {
		return true
	}
	parsed, err := url.Parse(value)
	if err == nil && strings.EqualFold(parsed.Scheme, "file") {
		return true
	}
	if err == nil && parsed.Scheme != "" {
		return false
	}
	if _, _, _, ok := splitSCPLike(value); ok {
		return false
	}
	return true
}

func hasUnsafeURLUserinfo(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.User == nil {
		return false
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		return true
	}
	_, hasPassword := parsed.User.Password()
	return hasPassword
}

func hasURLQueryOrFragment(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != "" && (parsed.RawQuery != "" || parsed.Fragment != "")
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 2 || path[1] != ':' {
		return false
	}
	drive := path[0]
	return drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z'
}

func validateCloneHint(cloneURL string) error {
	cloneURL = strings.TrimSpace(cloneURL)
	if cloneURL == "" {
		return nil
	}
	sanitized, err := sanitizedCloneHint(cloneURL)
	if err != nil {
		return err
	}
	if sanitized != cloneURL {
		return errors.New("workspace manifest clone hint must not contain credentials, query strings, or fragments")
	}
	return nil
}

func sanitizedCloneHint(cloneURL string) (string, error) {
	if isLocalCloneURL(cloneURL) {
		return "", errors.New("workspace manifest clone hint must not be a machine-local path")
	}
	parsed, err := url.Parse(cloneURL)
	if err == nil && parsed.Scheme != "" {
		if parsed.User != nil {
			if parsed.Scheme == "http" || parsed.Scheme == "https" {
				parsed.User = nil
			} else if _, hasPassword := parsed.User.Password(); hasPassword {
				parsed.User = url.User(parsed.User.Username())
			}
		}
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String(), nil
	}
	if strings.ContainsAny(cloneURL, "?#") {
		return "", errors.New("workspace manifest clone hint must not contain query strings or fragments")
	}
	return cloneURL, nil
}

func cloneURLForPlan(cloneURL string) string {
	cloneURL = strings.TrimSpace(cloneURL)
	if cloneURL == "" {
		return ""
	}
	sanitized, err := sanitizedCloneHint(cloneURL)
	if err != nil {
		return "not-exported"
	}
	return sanitized
}

func splitSCPLike(raw string) (string, string, string, bool) {
	if strings.Contains(raw, "://") {
		return "", "", "", false
	}
	at := strings.Index(raw, "@")
	colon := strings.Index(raw, ":")
	if at <= 0 || colon <= at+1 || colon == len(raw)-1 {
		return "", "", "", false
	}
	return raw[:at], raw[at+1 : colon], raw[colon+1:], true
}
