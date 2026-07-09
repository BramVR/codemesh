package workspacemanifest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
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

type WorkspaceManifest struct {
	ManifestVersion int            `json:"manifest_version"`
	Projects        []ProjectEntry `json:"projects"`
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

type ApplyImportResult struct {
	Plan            ImportPlan      `json:"plan"`
	AddedProjects   []state.Project `json:"added_projects"`
	UpdatedProjects []state.Project `json:"updated_projects"`
}

type ImportStore interface {
	ListProjects(context.Context) ([]state.Project, error)
	AddProject(context.Context, state.Project) (state.Project, error)
	UpdateProject(context.Context, int64, state.Project) (state.Project, error)
	DeleteProject(context.Context, int64) error
}

type TransactionalImportStore interface {
	ImportStore
	WithTransaction(context.Context, func(*state.SQLiteStore) error) error
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

func NewWorkspaceManifest(projects []ProjectEntry) WorkspaceManifest {
	return normalizeWorkspaceManifest(WorkspaceManifest{ManifestVersion: ManifestVersion, Projects: projects})
}

func ExportWorkspace(projects []state.Project, workspaceRoot string) (WorkspaceManifest, error) {
	entries, err := ExportProjects(projects, workspaceRoot)
	if err != nil {
		return WorkspaceManifest{}, err
	}
	manifestProjects := make([]ProjectEntry, 0, len(entries))
	for _, entry := range entries {
		manifestProjects = append(manifestProjects, entry.Project)
	}
	return NewWorkspaceManifest(manifestProjects), nil
}

func EncodeWorkspace(manifest WorkspaceManifest) ([]byte, error) {
	manifest = normalizeWorkspaceManifest(manifest)
	if err := ValidateWorkspace(manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func DecodeWorkspace(data []byte) (WorkspaceManifest, error) {
	var manifest WorkspaceManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("decode workspace manifest: %w", err)
	}
	if err := requireDecoderEOF(decoder); err != nil {
		return WorkspaceManifest{}, fmt.Errorf("decode workspace manifest: %w", err)
	}
	manifest = normalizeWorkspaceManifest(manifest)
	if err := ValidateWorkspace(manifest); err != nil {
		return WorkspaceManifest{}, err
	}
	return manifest, nil
}

func requireDecoderEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("trailing data after JSON document")
	default:
		return err
	}
}

func ValidateWorkspace(manifest WorkspaceManifest) error {
	if manifest.ManifestVersion != ManifestVersion {
		return fmt.Errorf("workspace manifest version must be %d", ManifestVersion)
	}
	seenAliases := map[string]string{}
	seenIdentities := map[string]string{}
	seenPaths := map[string]string{}
	for _, project := range manifest.Projects {
		entry := Entry{ManifestVersion: ManifestVersion, Project: project}
		entry = normalizeEntry(entry)
		if err := ValidateEntry(entry); err != nil {
			return err
		}
		if _, ok := seenAliases[entry.Project.Alias]; ok {
			return fmt.Errorf("workspace manifest alias %q appears more than once", entry.Project.Alias)
		}
		if _, ok := seenIdentities[entry.Project.Identity]; ok {
			return fmt.Errorf("workspace manifest identity %q appears more than once", entry.Project.Identity)
		}
		if _, ok := seenPaths[entry.Project.DesiredPath]; ok {
			return fmt.Errorf("workspace manifest desired path %q appears more than once", entry.Project.DesiredPath)
		}
		for seenPath, owner := range seenPaths {
			if desiredPathNests(seenPath, entry.Project.DesiredPath) || desiredPathNests(entry.Project.DesiredPath, seenPath) {
				return fmt.Errorf("workspace manifest desired path %q nests with %q owned by %q", entry.Project.DesiredPath, seenPath, owner)
			}
		}
		seenAliases[entry.Project.Alias] = entry.Project.Identity
		seenIdentities[entry.Project.Identity] = entry.Project.Alias
		seenPaths[entry.Project.DesiredPath] = entry.Project.Identity
	}
	return nil
}

func desiredPathNests(parent, child string) bool {
	parent = filepath.Clean(filepath.FromSlash(parent))
	child = filepath.Clean(filepath.FromSlash(child))
	return pathIsAncestor(parent, child)
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

func EntriesFromWorkspace(manifest WorkspaceManifest) []Entry {
	manifest = normalizeWorkspaceManifest(manifest)
	entries := make([]Entry, 0, len(manifest.Projects))
	for _, project := range manifest.Projects {
		entries = append(entries, Entry{ManifestVersion: ManifestVersion, Project: project})
	}
	return entries
}

func ApplyImport(ctx context.Context, store ImportStore, manifest WorkspaceManifest, workspaceRoot string) (ApplyImportResult, error) {
	if store == nil {
		return ApplyImportResult{}, errors.New("workspace manifest import store is required")
	}
	if err := ValidateWorkspace(manifest); err != nil {
		return ApplyImportResult{}, err
	}
	projects, err := store.ListProjects(ctx)
	if err != nil {
		return ApplyImportResult{}, err
	}
	plan, err := PlanImport(EntriesFromWorkspace(manifest), projects, workspaceRoot)
	if err != nil {
		return ApplyImportResult{}, err
	}
	return ApplyImportPlan(ctx, store, plan, projects)
}

func ApplyImportPlan(ctx context.Context, store ImportStore, plan ImportPlan, projects []state.Project) (ApplyImportResult, error) {
	if store == nil {
		return ApplyImportResult{}, errors.New("workspace manifest import store is required")
	}
	result := ApplyImportResult{Plan: plan}
	for _, change := range plan.Changes {
		if change.Action == ChangeConflict {
			return result, fmt.Errorf("workspace manifest import conflict for %q: %s", change.Alias, change.ConflictReason)
		}
	}
	if txStore, ok := store.(TransactionalImportStore); ok {
		err := txStore.WithTransaction(ctx, func(tx *state.SQLiteStore) error {
			mutated, err := applyImportChanges(ctx, tx, plan.Changes, projects, false)
			if err != nil {
				return err
			}
			result.AddedProjects = mutated.AddedProjects
			result.UpdatedProjects = mutated.UpdatedProjects
			return nil
		})
		return result, err
	}
	mutated, err := applyImportChanges(ctx, store, plan.Changes, projects, true)
	if err != nil {
		return result, err
	}
	result.AddedProjects = mutated.AddedProjects
	result.UpdatedProjects = mutated.UpdatedProjects
	return result, nil
}

func applyImportChanges(ctx context.Context, store ImportStore, changes []ImportChange, projects []state.Project, rollbackOnError bool) (ApplyImportResult, error) {
	result := ApplyImportResult{}
	updated, updateRollback, err := applyImportUpdates(ctx, store, changes, projects, rollbackOnError)
	if err != nil {
		return result, err
	}
	result.UpdatedProjects = updated
	for _, change := range changes {
		if change.Action != ChangeAdd {
			continue
		}
		project, err := store.AddProject(ctx, projectFromImportChange(change))
		if err != nil {
			if rollbackOnError {
				rollbackErr := rollbackImportAdds(ctx, store, result.AddedProjects)
				updateRollbackErr := updateRollback.rollback(ctx, store)
				if rollbackErr != nil || updateRollbackErr != nil {
					return result, fmt.Errorf("add manifest project %q: %w; rollback failed: %v", change.Alias, err, joinRollbackErrors(rollbackErr, updateRollbackErr))
				}
			}
			return result, fmt.Errorf("add manifest project %q: %w", change.Alias, err)
		}
		result.AddedProjects = append(result.AddedProjects, project)
	}
	return result, nil
}

func ExportProjects(projects []state.Project, workspaceRoot string) ([]Entry, error) {
	root, err := canonicalWorkspaceRoot(workspaceRoot)
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

func normalizeWorkspaceManifest(manifest WorkspaceManifest) WorkspaceManifest {
	normalized := WorkspaceManifest{
		ManifestVersion: manifest.ManifestVersion,
		Projects:        make([]ProjectEntry, 0, len(manifest.Projects)),
	}
	for _, project := range manifest.Projects {
		entry := normalizeEntry(Entry{ManifestVersion: ManifestVersion, Project: project})
		normalized.Projects = append(normalized.Projects, entry.Project)
	}
	sort.SliceStable(normalized.Projects, func(i, j int) bool {
		if normalized.Projects[i].DesiredPath == normalized.Projects[j].DesiredPath {
			return normalized.Projects[i].Identity < normalized.Projects[j].Identity
		}
		return normalized.Projects[i].DesiredPath < normalized.Projects[j].DesiredPath
	})
	if normalized.Projects == nil {
		normalized.Projects = []ProjectEntry{}
	}
	return normalized
}

func PlanImport(entries []Entry, projects []state.Project, workspaceRoot string) (ImportPlan, error) {
	root, err := cleanWorkspaceRoot(workspaceRoot)
	if err != nil {
		return ImportPlan{}, err
	}
	compareRoot := canonicalPathForComparison(root)
	normalizedEntries := make([]Entry, 0, len(entries))
	desiredAliasByIdentity := make(map[string]string, len(entries))
	desiredIdentity := make(map[string]bool, len(entries))
	for _, raw := range entries {
		entry := normalizeEntry(raw)
		if err := ValidateEntry(entry); err != nil {
			return ImportPlan{}, err
		}
		if _, exists := desiredAliasByIdentity[entry.Project.Identity]; !exists {
			desiredAliasByIdentity[entry.Project.Identity] = entry.Project.Alias
		}
		desiredIdentity[entry.Project.Identity] = true
		normalizedEntries = append(normalizedEntries, entry)
	}
	byIdentity := make(map[string]state.Project, len(projects))
	byPath := make(map[string]state.Project, len(projects))
	aliasOwner := make(map[string]string, len(projects))
	for _, project := range projects {
		byIdentity[project.NormalizedRemote] = project
		if strings.TrimSpace(project.LocalPath) != "" {
			byPath[canonicalPathForComparison(project.LocalPath)] = project
		}
		if desiredAlias, ok := desiredAliasByIdentity[project.NormalizedRemote]; ok && desiredAlias != project.Alias {
			continue
		}
		aliasOwner[project.Alias] = project.NormalizedRemote
	}

	plan := ImportPlan{Changes: []ImportChange{}}
	seenManifestIdentities := map[string]bool{}
	seenDesiredPaths := map[string]string{}
	for _, project := range projects {
		if desiredIdentity[project.NormalizedRemote] || strings.TrimSpace(project.LocalPath) == "" {
			continue
		}
		seenDesiredPaths[canonicalPathForComparison(project.LocalPath)] = project.NormalizedRemote
	}
	for _, entry := range normalizedEntries {
		desiredLocalPath := filepath.Join(root, filepath.FromSlash(entry.Project.DesiredPath))
		desiredComparePath := filepath.Join(compareRoot, filepath.FromSlash(entry.Project.DesiredPath))
		desiredComparePath = canonicalPathForComparison(desiredComparePath)
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
		if !PathWithinRoot(compareRoot, desiredComparePath) {
			change.Action = ChangeConflict
			change.ConflictReason = "desired path resolves outside workspace root"
			plan.Changes = append(plan.Changes, change)
			continue
		}
		if owner, ok := aliasOwner[entry.Project.Alias]; ok && owner != entry.Project.Identity {
			change.Action = ChangeConflict
			change.ConflictReason = fmt.Sprintf("alias %q already belongs to %q", entry.Project.Alias, owner)
			plan.Changes = append(plan.Changes, change)
			continue
		}
		pathOwnerIdentity, pathTaken := seenDesiredPaths[desiredComparePath]
		pathConflict := pathTaken && pathOwnerIdentity != entry.Project.Identity
		nestedPathOwnerIdentity, nestedPath, nestedPathConflict := nestedDesiredPathConflict(seenDesiredPaths, desiredComparePath, entry.Project.Identity)
		if pathConflict {
			change.Action = ChangeConflict
			change.ConflictReason = fmt.Sprintf("desired path already requested by %q", pathOwnerIdentity)
			plan.Changes = append(plan.Changes, change)
			continue
		}
		if nestedPathConflict {
			change.Action = ChangeConflict
			change.ConflictReason = fmt.Sprintf("desired path nests with project path %q owned by %q", nestedPath, nestedPathOwnerIdentity)
			plan.Changes = append(plan.Changes, change)
			continue
		}
		seenDesiredPaths[desiredComparePath] = entry.Project.Identity
		project, ok := byIdentity[entry.Project.Identity]
		if !ok {
			if owner, ok := byPath[desiredComparePath]; ok && owner.NormalizedRemote != entry.Project.Identity {
				change.Action = ChangeConflict
				change.ConflictReason = fmt.Sprintf("desired path is already registered to %q", owner.NormalizedRemote)
				plan.Changes = append(plan.Changes, change)
				continue
			}
			if reason, blocked := pathConflictReason(desiredLocalPath, root); blocked {
				change.Action = ChangeConflict
				change.ConflictReason = reason
				plan.Changes = append(plan.Changes, change)
				continue
			}
			change.Action = ChangeAdd
			aliasOwner[entry.Project.Alias] = entry.Project.Identity
			plan.Changes = append(plan.Changes, change)
			continue
		}
		change.ProjectID = project.ID
		if entry.Project.CloneHints.URL == "" {
			change.CloneURL = project.CloneURL
		}
		if project.Alias != entry.Project.Alias {
			change.Fields = append(change.Fields, FieldChange{Field: "alias", Current: project.Alias, Desired: entry.Project.Alias})
		}
		observedComparePath := canonicalPathForComparison(project.LocalPath)
		if observedComparePath != desiredComparePath {
			if owner, ok := byPath[desiredComparePath]; ok && owner.NormalizedRemote != entry.Project.Identity {
				change.Action = ChangeConflict
				change.ConflictReason = fmt.Sprintf("desired path is already registered to %q", owner.NormalizedRemote)
				plan.Changes = append(plan.Changes, change)
				continue
			}
			if reason, blocked := pathConflictReason(desiredLocalPath, root); blocked {
				change.Action = ChangeConflict
				change.ConflictReason = reason
				plan.Changes = append(plan.Changes, change)
				continue
			}
			change.Fields = append(change.Fields, FieldChange{Field: "local_path", Current: project.LocalPath, Desired: desiredLocalPath})
		} else {
			change.LocalPath = project.LocalPath
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

type importUpdateRollback struct {
	originals    map[int64]state.Project
	tempRenamed  []int64
	finalUpdated []state.Project
}

func (r importUpdateRollback) rollback(ctx context.Context, store ImportStore) error {
	if len(r.originals) == 0 {
		return nil
	}
	return rollbackImportUpdates(ctx, store, r.originals, r.tempRenamed, r.finalUpdated)
}

func applyImportUpdates(ctx context.Context, store ImportStore, changes []ImportChange, projects []state.Project, rollbackOnError bool) ([]state.Project, importUpdateRollback, error) {
	updates := make([]ImportChange, 0, len(changes))
	for _, change := range changes {
		if change.Action == ChangeUpdate {
			updates = append(updates, change)
		}
	}
	if len(updates) == 0 {
		return nil, importUpdateRollback{}, nil
	}

	byID := make(map[int64]state.Project, len(projects))
	usedAliases := make(map[string]bool, len(projects)+len(updates))
	for _, project := range projects {
		byID[project.ID] = project
		usedAliases[project.Alias] = true
	}
	for _, change := range updates {
		usedAliases[change.Alias] = true
	}

	tempAliases := map[int64]string{}
	originals := make(map[int64]state.Project, len(updates))
	for _, change := range updates {
		current, ok := byID[change.ProjectID]
		if !ok {
			continue
		}
		originals[change.ProjectID] = current
		if current.Alias == change.Alias {
			continue
		}
		tempAliases[change.ProjectID] = importTempAlias(change.ProjectID, usedAliases)
	}
	var tempRenamed []int64
	for _, change := range updates {
		tempAlias, ok := tempAliases[change.ProjectID]
		if !ok {
			continue
		}
		current := byID[change.ProjectID]
		current.Alias = tempAlias
		if _, err := store.UpdateProject(ctx, change.ProjectID, current); err != nil {
			if !rollbackOnError {
				return nil, importUpdateRollback{}, fmt.Errorf("free manifest project alias %q: %w", change.Alias, err)
			}
			rollbackErr := rollbackImportUpdates(ctx, store, originals, tempRenamed, nil)
			if rollbackErr != nil {
				return nil, importUpdateRollback{}, fmt.Errorf("free manifest project alias %q: %w; rollback failed: %v", change.Alias, err, rollbackErr)
			}
			return nil, importUpdateRollback{}, fmt.Errorf("free manifest project alias %q: %w", change.Alias, err)
		}
		tempRenamed = append(tempRenamed, change.ProjectID)
	}

	updated := make([]state.Project, 0, len(updates))
	finalUpdated := make([]state.Project, 0, len(updates))
	for _, change := range updates {
		project, err := store.UpdateProject(ctx, change.ProjectID, projectFromImportChange(change))
		if err != nil {
			if !rollbackOnError {
				return nil, importUpdateRollback{}, fmt.Errorf("update manifest project %q: %w", change.Alias, err)
			}
			rollbackErr := rollbackImportUpdates(ctx, store, originals, tempRenamed, finalUpdated)
			if rollbackErr != nil {
				return nil, importUpdateRollback{}, fmt.Errorf("update manifest project %q: %w; rollback failed: %v", change.Alias, err, rollbackErr)
			}
			return nil, importUpdateRollback{}, fmt.Errorf("update manifest project %q: %w", change.Alias, err)
		}
		updated = append(updated, project)
		finalUpdated = append(finalUpdated, project)
	}
	return updated, importUpdateRollback{originals: originals, tempRenamed: tempRenamed, finalUpdated: finalUpdated}, nil
}

func rollbackImportAdds(ctx context.Context, store ImportStore, added []state.Project) error {
	if len(added) == 0 {
		return nil
	}
	var rollbackErrors []string
	for i := len(added) - 1; i >= 0; i-- {
		project := added[i]
		if err := store.DeleteProject(ctx, project.ID); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("delete project %d: %v", project.ID, err))
		}
	}
	if len(rollbackErrors) != 0 {
		return errors.New(strings.Join(rollbackErrors, "; "))
	}
	return nil
}

func joinRollbackErrors(errs ...error) error {
	var messages []string
	for _, err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return errors.New(strings.Join(messages, "; "))
}

func rollbackImportUpdates(ctx context.Context, store ImportStore, originals map[int64]state.Project, tempRenamed []int64, finalUpdated []state.Project) error {
	var rollbackErrors []string
	used := map[string]bool{}
	for _, original := range originals {
		used[original.Alias] = true
	}
	for _, project := range finalUpdated {
		temp := project
		temp.Alias = importRollbackAlias(project.ID, used)
		if _, err := store.UpdateProject(ctx, project.ID, temp); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("free rollback alias for project %d: %v", project.ID, err))
		}
	}
	restoreIDs := make(map[int64]bool, len(tempRenamed)+len(finalUpdated))
	for _, id := range tempRenamed {
		restoreIDs[id] = true
	}
	for _, project := range finalUpdated {
		restoreIDs[project.ID] = true
	}
	ids := make([]int64, 0, len(restoreIDs))
	for id := range restoreIDs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		original, ok := originals[id]
		if !ok {
			continue
		}
		if _, err := store.UpdateProject(ctx, id, original); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("restore project %d: %v", id, err))
		}
	}
	if len(rollbackErrors) != 0 {
		return errors.New(strings.Join(rollbackErrors, "; "))
	}
	return nil
}

func importTempAlias(projectID int64, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		alias := fmt.Sprintf("__codemesh_manifest_%d", projectID)
		if suffix > 0 {
			alias = fmt.Sprintf("%s_%d", alias, suffix)
		}
		if used[alias] {
			continue
		}
		used[alias] = true
		return alias
	}
}

func importRollbackAlias(projectID int64, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		alias := fmt.Sprintf("__codemesh_manifest_rollback_%d", projectID)
		if suffix > 0 {
			alias = fmt.Sprintf("%s_%d", alias, suffix)
		}
		if used[alias] {
			continue
		}
		used[alias] = true
		return alias
	}
}

func projectFromImportChange(change ImportChange) state.Project {
	return state.Project{
		ID:               change.ProjectID,
		Alias:            change.Alias,
		NormalizedRemote: change.Identity,
		CloneURL:         change.CloneURL,
		LocalPath:        change.LocalPath,
	}
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

func canonicalWorkspaceRoot(root string) (string, error) {
	clean, err := cleanWorkspaceRoot(root)
	if err != nil {
		return "", err
	}
	return canonicalPathForComparison(clean), nil
}

func relativeDesiredPath(root, localPath string) (string, error) {
	if strings.TrimSpace(localPath) == "" {
		return "", errors.New("local path is required")
	}
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return "", fmt.Errorf("resolve local path: %w", err)
	}
	absPath = canonicalPathForComparison(absPath)
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

func canonicalPathForComparison(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		missing = append(missing, filepath.Base(current))
	}
}

func CanonicalPathForComparison(path string) string {
	return canonicalPathForComparison(path)
}

func PathWithinRoot(root, path string) bool {
	root = canonicalPathForComparison(root)
	path = canonicalPathForComparison(path)
	return path == root || pathIsAncestor(root, path)
}

func nestedDesiredPathConflict(seen map[string]string, desiredPath, desiredIdentity string) (string, string, bool) {
	for existingPath, existingIdentity := range seen {
		if existingIdentity == desiredIdentity {
			continue
		}
		if pathIsAncestor(existingPath, desiredPath) || pathIsAncestor(desiredPath, existingPath) {
			return existingIdentity, existingPath, true
		}
	}
	return "", "", false
}

func NestedDesiredPathConflict(seen map[string]string, desiredPath, desiredIdentity string) (string, string, bool) {
	return nestedDesiredPathConflict(seen, desiredPath, desiredIdentity)
}

func pathIsAncestor(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func PathConflictReason(path string) (string, bool) {
	return pathConflictReason(path, "")
}

func pathConflictReason(path, workspaceRoot string) (string, bool) {
	if strings.TrimSpace(workspaceRoot) != "" && canonicalPathForComparison(path) == canonicalPathForComparison(workspaceRoot) {
		return workspaceRootConflictReason(path)
	}
	_, err := os.Lstat(path)
	switch {
	case err == nil:
		return "desired path already exists outside the Project Registry", true
	case errors.Is(err, os.ErrNotExist):
		return parentPathConflictReason(path)
	default:
		return "desired path could not be checked safely: " + err.Error(), true
	}
}

func workspaceRootConflictReason(path string) (string, bool) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if !info.IsDir() {
			return "workspace root exists outside the Project Registry and is not a directory", true
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return "workspace root could not be checked safely: " + err.Error(), true
		}
		if len(entries) != 0 {
			return "workspace root already contains files outside the Project Registry", true
		}
		return "", false
	case errors.Is(err, os.ErrNotExist):
		return parentPathConflictReason(path)
	default:
		return "workspace root could not be checked safely: " + err.Error(), true
	}
}

func parentPathConflictReason(path string) (string, bool) {
	for parent := filepath.Dir(path); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
		info, err := os.Stat(parent)
		switch {
		case err == nil:
			if !info.IsDir() {
				return fmt.Sprintf("desired path parent %q exists and is not a directory", parent), true
			}
			return "", false
		case errors.Is(err, os.ErrNotExist):
			next := filepath.Dir(parent)
			if next == parent {
				return "", false
			}
			continue
		default:
			return "desired path parent could not be checked safely: " + err.Error(), true
		}
	}
	return "", false
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
