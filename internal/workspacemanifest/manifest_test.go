package workspacemanifest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BramVR/codemesh/internal/state"
)

func TestEntryRoundTripPreservesDesiredTopology(t *testing.T) {
	entry := Entry{
		ManifestVersion: ManifestVersion,
		Project: ProjectEntry{
			Identity:    "https://github.com/BramVR/codemesh",
			Alias:       "codemesh",
			DesiredPath: "tools/codemesh",
			CloneHints:  CloneHints{URL: "git@github.com:BramVR/codemesh.git"},
			Groups:      []string{"agents", "oss"},
		},
	}

	data, err := EncodeEntry(entry)
	if err != nil {
		t.Fatalf("EncodeEntry error = %v", err)
	}
	decoded, err := DecodeEntry(data)
	if err != nil {
		t.Fatalf("DecodeEntry error = %v", err)
	}

	if decoded.Project.Identity != entry.Project.Identity ||
		decoded.Project.Alias != entry.Project.Alias ||
		decoded.Project.DesiredPath != entry.Project.DesiredPath ||
		decoded.Project.CloneHints.URL != entry.Project.CloneHints.URL ||
		strings.Join(decoded.Project.Groups, ",") != "agents,oss" {
		t.Fatalf("decoded entry = %#v, want %#v", decoded, entry)
	}
}

func TestDecodeEntryKeepsLegacyBootstrapUnknownFieldsCompatible(t *testing.T) {
	raw := []byte(`{
  "manifest_version": 1,
  "project": {
    "identity": "https://github.com/BramVR/alpha",
    "alias": "alpha",
    "desired_path": "alpha",
    "clone_hints": {},
    "groups": [],
    "annotation": "legacy bootstrap note"
  },
  "legacy": true
}`)

	entry, err := DecodeEntry(raw)
	if err != nil {
		t.Fatalf("DecodeEntry error = %v", err)
	}
	if entry.Project.Alias != "alpha" || entry.Project.DesiredPath != "alpha" {
		t.Fatalf("entry = %#v, want decoded legacy-compatible project fields", entry)
	}
}

func TestWorkspaceManifestRoundTripSortsAndRejectsUnsafeShape(t *testing.T) {
	manifest := NewWorkspaceManifest([]ProjectEntry{
		{
			Identity:    "git@github.com:BramVR/beta.git",
			Alias:       "beta",
			DesiredPath: "tools/beta",
			CloneHints:  CloneHints{URL: "https://example.invalid/org/beta.git"},
			Groups:      []string{"team"},
		},
		{
			Identity:    "git@github.com:BramVR/alpha.git",
			Alias:       "alpha",
			DesiredPath: "apps/alpha",
			CloneHints:  CloneHints{URL: "https://example.invalid/org/alpha.git"},
			Groups:      []string{},
		},
	})

	data, err := EncodeWorkspace(manifest)
	if err != nil {
		t.Fatalf("EncodeWorkspace error = %v", err)
	}
	decoded, err := DecodeWorkspace(data)
	if err != nil {
		t.Fatalf("DecodeWorkspace error = %v", err)
	}
	if decoded.ManifestVersion != ManifestVersion || len(decoded.Projects) != 2 {
		t.Fatalf("decoded manifest = %#v", decoded)
	}
	if decoded.Projects[0].Alias != "alpha" || decoded.Projects[0].Identity != "https://github.com/BramVR/alpha" || decoded.Projects[0].DesiredPath != "apps/alpha" {
		t.Fatalf("first project = %#v, want sorted normalized alpha", decoded.Projects[0])
	}
	if decoded.Projects[1].Alias != "beta" || decoded.Projects[1].Identity != "https://github.com/BramVR/beta" || decoded.Projects[1].DesiredPath != "tools/beta" {
		t.Fatalf("second project = %#v, want sorted normalized beta", decoded.Projects[1])
	}

	for name, raw := range map[string]string{
		"unknown field": `{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "alpha",
      "clone_hints": {},
      "groups": [],
      "unexpected": "leak-marker"
    }
  ]
}`,
		"bad version": `{"manifest_version":2,"projects":[]}`,
		"credential clone hint": `{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "alpha",
      "clone_hints": {"url": "https://user:leak-marker@example.invalid/org/alpha.git"},
      "groups": []
    }
  ]
}`,
		"absolute path": `{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://github.com/BramVR/alpha",
      "alias": "alpha",
      "desired_path": "/tmp/alpha",
      "clone_hints": {},
      "groups": []
    }
  ]
}`,
		"duplicate alias": `{
  "manifest_version": 1,
  "projects": [
    {"identity": "https://github.com/BramVR/alpha", "alias": "shared", "desired_path": "alpha", "clone_hints": {}, "groups": []},
    {"identity": "https://github.com/BramVR/beta", "alias": "shared", "desired_path": "beta", "clone_hints": {}, "groups": []}
  ]
}`,
		"duplicate identity": `{
  "manifest_version": 1,
  "projects": [
    {"identity": "https://github.com/BramVR/alpha", "alias": "alpha", "desired_path": "alpha", "clone_hints": {}, "groups": []},
    {"identity": "https://github.com/BramVR/alpha", "alias": "alpha", "desired_path": "alpha-copy", "clone_hints": {}, "groups": []}
  ]
}`,
		"nested desired path": `{
  "manifest_version": 1,
  "projects": [
    {"identity": "https://github.com/BramVR/alpha", "alias": "alpha", "desired_path": "alpha", "clone_hints": {}, "groups": []},
    {"identity": "https://github.com/BramVR/beta", "alias": "beta", "desired_path": "alpha/vendor", "clone_hints": {}, "groups": []}
  ]
}`,
		"trailing content": `{
  "manifest_version": 1,
  "projects": []
}
{
  "manifest_version": 1,
  "projects": []
}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeWorkspace([]byte(raw))
			if err == nil {
				t.Fatal("DecodeWorkspace error = nil, want validation failure")
			}
			if strings.Contains(err.Error(), "leak-marker") {
				t.Fatalf("DecodeWorkspace error leaked secret marker: %v", err)
			}
		})
	}
}

func TestExportProjectsRejectsSymlinkedProjectOutsideWorkspace(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := ExportProjects([]state.Project{{
		Alias:            "repo",
		NormalizedRemote: "https://github.com/BramVR/repo",
		CloneURL:         "https://github.com/BramVR/repo.git",
		LocalPath:        filepath.Join(workspace, "linked", "repo"),
	}}, workspace)
	if err == nil {
		t.Fatal("ExportProjects error = nil, want symlinked outside workspace rejection")
	}
	if !strings.Contains(err.Error(), "outside workspace root") {
		t.Fatalf("ExportProjects error = %v, want outside workspace root", err)
	}
}

func TestExportProjectsUsesRelativeDesiredPathsAndOmitsObservedState(t *testing.T) {
	projects := []state.Project{{
		ID:               42,
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "https://user:leak-marker@example.invalid/org/repo.git?marker=leak-marker#piece",
		LocalPath:        "/workspace/tools/codemesh",
	}}

	entries, err := ExportProjects(projects, "/workspace")
	if err != nil {
		t.Fatalf("ExportProjects error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Project.DesiredPath != "tools/codemesh" {
		t.Fatalf("desired path = %q, want relative path", entry.Project.DesiredPath)
	}
	if entry.Project.CloneHints.URL != "https://example.invalid/org/repo.git" {
		t.Fatalf("clone hint = %q, want sanitized clone URL", entry.Project.CloneHints.URL)
	}

	data, err := EncodeEntry(entry)
	if err != nil {
		t.Fatalf("EncodeEntry error = %v", err)
	}
	raw := string(data)
	for _, forbidden := range []string{
		`"id"`,
		`"local_path"`,
		"present",
		"dirty",
		"stale",
		"readiness",
		"agent_run",
		"hostname",
		"CODEMESH_TOKEN",
		"secret",
		"token",
		"frag",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("manifest entry leaked %q:\n%s", forbidden, raw)
		}
	}
}

func TestExportProjectsOmitsMachineLocalCloneHints(t *testing.T) {
	entries, err := ExportProjects([]state.Project{
		{
			Alias:            "local-path",
			NormalizedRemote: "https://github.com/BramVR/local-path",
			CloneURL:         "/tmp/remotes/local-path.git",
			LocalPath:        "/workspace/local-path",
		},
		{
			Alias:            "file-url",
			NormalizedRemote: "https://github.com/BramVR/file-url",
			CloneURL:         "file:///tmp/remotes/file-url.git",
			LocalPath:        "/workspace/file-url",
		},
		{
			Alias:            "file-url-short",
			NormalizedRemote: "https://github.com/BramVR/file-url-short",
			CloneURL:         "file:/tmp/remotes/file-url-short.git",
			LocalPath:        "/workspace/file-url-short",
		},
		{
			Alias:            "windows-path",
			NormalizedRemote: "https://github.com/BramVR/windows-path",
			CloneURL:         `C:\tmp\remotes\windows-path.git`,
			LocalPath:        "/workspace/windows-path",
		},
	}, "/workspace")
	if err != nil {
		t.Fatalf("ExportProjects error = %v", err)
	}

	for _, entry := range entries {
		if entry.Project.CloneHints.URL != "" {
			t.Fatalf("machine-local clone hint exported for %s: %#v", entry.Project.Alias, entry.Project.CloneHints)
		}
	}
}

func TestExportProjectsRejectsMachineLocalIdentityWithoutEchoingIt(t *testing.T) {
	localIdentity := "/tmp/remotes/local-identity.git"
	_, err := ExportProjects([]state.Project{{
		Alias:            "local-identity",
		NormalizedRemote: localIdentity,
		CloneURL:         "https://github.com/BramVR/local-identity.git",
		LocalPath:        "/workspace/local-identity",
	}}, "/workspace")
	if err == nil {
		t.Fatal("ExportProjects error = nil, want local identity rejection")
	}
	if strings.Contains(err.Error(), localIdentity) {
		t.Fatalf("ExportProjects error leaked local identity: %v", err)
	}
}

func TestExportProjectsRejectsCredentialBearingIdentityWithoutEchoingIt(t *testing.T) {
	for _, credentialIdentity := range []string{
		"https://leak-marker@example.invalid/org/repo",
		"git@example.invalid:org/repo.git?marker=leak-marker-value#piece",
	} {
		t.Run(credentialIdentity, func(t *testing.T) {
			_, err := ExportProjects([]state.Project{{
				Alias:            "credential-identity",
				NormalizedRemote: credentialIdentity,
				CloneURL:         "https://example.invalid/org/repo.git",
				LocalPath:        "/workspace/credential-identity",
			}}, "/workspace")
			if err == nil {
				t.Fatal("ExportProjects error = nil, want credential-bearing identity rejection")
			}
			if strings.Contains(err.Error(), "leak-marker") || strings.Contains(err.Error(), credentialIdentity) {
				t.Fatalf("ExportProjects error leaked credential identity: %v", err)
			}
		})
	}
}

func TestExportProjectsAllowsWorkspaceRootProject(t *testing.T) {
	entries, err := ExportProjects([]state.Project{{
		Alias:            "workspace",
		NormalizedRemote: "https://github.com/BramVR/workspace",
		CloneURL:         "https://github.com/BramVR/workspace.git",
		LocalPath:        "/workspace",
	}}, "/workspace")
	if err != nil {
		t.Fatalf("ExportProjects error = %v", err)
	}
	if len(entries) != 1 || entries[0].Project.DesiredPath != "." {
		t.Fatalf("entries = %#v, want root desired path", entries)
	}
}

func TestImportPlanReportsRegistryChangesWithoutMutating(t *testing.T) {
	entries := []Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/codemesh",
			Alias:       "codemesh",
			DesiredPath: "tools/codemesh",
			CloneHints:  CloneHints{URL: "git@github.com:BramVR/codemesh.git"},
		}),
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/agent-scripts",
			Alias:       "agents",
			DesiredPath: "tools/agent-scripts",
		}),
	}
	projects := []state.Project{{
		ID:               1,
		Alias:            "old-codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "https://github.com/BramVR/codemesh.git",
		LocalPath:        "/workspace/codemesh",
	}}

	plan, err := PlanImport(entries, projects, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}

	if len(plan.Changes) != 2 {
		t.Fatalf("change count = %d, want 2: %#v", len(plan.Changes), plan.Changes)
	}
	if plan.Changes[0].Action != ChangeUpdate || plan.Changes[0].ProjectID != 1 {
		t.Fatalf("first change = %#v, want update for existing project", plan.Changes[0])
	}
	if !hasFieldChange(plan.Changes[0], "alias", "old-codemesh", "codemesh") ||
		!hasFieldChange(plan.Changes[0], "local_path", "/workspace/codemesh", "/workspace/tools/codemesh") ||
		!hasFieldChange(plan.Changes[0], "clone_url", "https://github.com/BramVR/codemesh.git", "git@github.com:BramVR/codemesh.git") {
		t.Fatalf("update fields = %#v, want alias/path/clone URL changes", plan.Changes[0].Fields)
	}
	if plan.Changes[1].Action != ChangeAdd || plan.Changes[1].ProjectID != 0 {
		t.Fatalf("second change = %#v, want add for missing project", plan.Changes[1])
	}
	if projects[0].Alias != "old-codemesh" || projects[0].LocalPath != "/workspace/codemesh" {
		t.Fatalf("PlanImport mutated local project row: %#v", projects[0])
	}
}

func TestApplyImportRestoresTempAliasesAfterUpdateFailure(t *testing.T) {
	store := &failingUpdateStore{projects: []state.Project{
		{
			ID:               1,
			Alias:            "first",
			NormalizedRemote: "https://github.com/BramVR/first",
			CloneURL:         "https://github.com/BramVR/first.git",
			LocalPath:        "/workspace/first",
		},
		{
			ID:               2,
			Alias:            "second",
			NormalizedRemote: "https://github.com/BramVR/second",
			CloneURL:         "https://github.com/BramVR/second.git",
			LocalPath:        "/workspace/second",
		},
	}}
	manifest := NewWorkspaceManifest([]ProjectEntry{
		{
			Identity:    "https://github.com/BramVR/first",
			Alias:       "second",
			DesiredPath: "first",
			CloneHints:  CloneHints{URL: "https://github.com/BramVR/first.git"},
		},
		{
			Identity:    "https://github.com/BramVR/second",
			Alias:       "first",
			DesiredPath: "second",
			CloneHints:  CloneHints{URL: "https://github.com/BramVR/second.git"},
		},
	})

	_, err := ApplyImport(context.Background(), store, manifest, "/workspace")
	if err == nil {
		t.Fatal("ApplyImport error = nil, want injected update failure")
	}
	if store.projects[0].Alias != "first" || store.projects[1].Alias != "second" {
		t.Fatalf("projects not restored after failure: %#v", store.projects)
	}
}

func TestApplyImportPreservesCloneURLWhenManifestHintIsOmitted(t *testing.T) {
	workspace := t.TempDir()
	localMirror := filepath.Join(t.TempDir(), "alpha.git")
	store := &failingUpdateStore{projects: []state.Project{{
		ID:               1,
		Alias:            "old-alpha",
		NormalizedRemote: "https://github.com/BramVR/alpha",
		CloneURL:         localMirror,
		LocalPath:        filepath.Join(workspace, "old-alpha"),
	}}}
	manifest := NewWorkspaceManifest([]ProjectEntry{{
		Identity:    "https://github.com/BramVR/alpha",
		Alias:       "alpha",
		DesiredPath: "apps/alpha",
	}})

	result, err := ApplyImport(context.Background(), store, manifest, workspace)
	if err != nil {
		t.Fatalf("ApplyImport error = %v", err)
	}
	if len(result.UpdatedProjects) != 1 {
		t.Fatalf("updated projects = %#v, want one update", result.UpdatedProjects)
	}
	if store.projects[0].CloneURL != localMirror {
		t.Fatalf("clone URL = %q, want preserved local mirror %q", store.projects[0].CloneURL, localMirror)
	}
	for _, field := range result.Plan.Changes[0].Fields {
		if field.Field == "clone_url" {
			t.Fatalf("fields = %#v, want no clone_url field change", result.Plan.Changes[0].Fields)
		}
	}
}

func TestApplyImportPreservesLocalPathSpellingWhenCanonicalPathIsUnchanged(t *testing.T) {
	tmp := t.TempDir()
	realWorkspace := filepath.Join(tmp, "real-workspace")
	if err := os.MkdirAll(filepath.Join(realWorkspace, "apps", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkWorkspace := filepath.Join(tmp, "link-workspace")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	realProjectPath := filepath.Join(realWorkspace, "apps", "alpha")
	store := &failingUpdateStore{projects: []state.Project{{
		ID:               1,
		Alias:            "old-alpha",
		NormalizedRemote: "https://github.com/BramVR/alpha",
		CloneURL:         "https://github.com/BramVR/alpha.git",
		LocalPath:        realProjectPath,
	}}}
	manifest := NewWorkspaceManifest([]ProjectEntry{{
		Identity:    "https://github.com/BramVR/alpha",
		Alias:       "alpha",
		DesiredPath: "apps/alpha",
		CloneHints:  CloneHints{URL: "https://github.com/BramVR/alpha.git"},
	}})

	result, err := ApplyImport(context.Background(), store, manifest, linkWorkspace)
	if err != nil {
		t.Fatalf("ApplyImport error = %v", err)
	}
	if len(result.UpdatedProjects) != 1 {
		t.Fatalf("updated projects = %#v, want one alias update", result.UpdatedProjects)
	}
	if store.projects[0].LocalPath != realProjectPath {
		t.Fatalf("local path = %q, want preserved spelling %q", store.projects[0].LocalPath, realProjectPath)
	}
	for _, field := range result.Plan.Changes[0].Fields {
		if field.Field == "local_path" {
			t.Fatalf("fields = %#v, want no local_path field change", result.Plan.Changes[0].Fields)
		}
	}
}

func TestApplyImportRollsBackNonTransactionalAddsAfterFailure(t *testing.T) {
	workspace := t.TempDir()
	store := &failingAddStore{
		projects: []state.Project{{
			ID:               1,
			Alias:            "old-alpha",
			NormalizedRemote: "https://github.com/BramVR/alpha",
			CloneURL:         "https://github.com/BramVR/alpha.git",
			LocalPath:        filepath.Join(workspace, "old-alpha"),
		}},
		nextID:    2,
		failAlias: "gamma",
	}
	manifest := NewWorkspaceManifest([]ProjectEntry{
		{
			Identity:    "https://github.com/BramVR/alpha",
			Alias:       "alpha",
			DesiredPath: "apps/alpha",
			CloneHints:  CloneHints{URL: "https://github.com/BramVR/alpha.git"},
		},
		{
			Identity:    "https://github.com/BramVR/beta",
			Alias:       "beta",
			DesiredPath: "beta",
		},
		{
			Identity:    "https://github.com/BramVR/gamma",
			Alias:       "gamma",
			DesiredPath: "gamma",
		},
	})

	_, err := ApplyImport(context.Background(), store, manifest, workspace)
	if err == nil {
		t.Fatal("ApplyImport error = nil, want add failure")
	}
	if len(store.projects) != 1 {
		t.Fatalf("projects = %#v, want only original project after rollback", store.projects)
	}
	if store.projects[0].Alias != "old-alpha" || store.projects[0].LocalPath != filepath.Join(workspace, "old-alpha") {
		t.Fatalf("original project not restored: %#v", store.projects[0])
	}
}

func TestImportPlanNormalizesManifestIdentity(t *testing.T) {
	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "git@github.com:BramVR/codemesh.git",
			Alias:       "codemesh",
			DesiredPath: "codemesh",
		}),
	}, []state.Project{{
		ID:               1,
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "https://github.com/BramVR/codemesh.git",
		LocalPath:        "/workspace/codemesh",
	}}, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeUnchanged {
		t.Fatalf("changes = %#v, want unchanged for normalized identity match", plan.Changes)
	}
}

func TestImportPlanComparesCanonicalWorkspacePathsWithoutRewritingLocalPlacement(t *testing.T) {
	tmp := t.TempDir()
	realWorkspace := filepath.Join(tmp, "real-workspace")
	if err := os.MkdirAll(filepath.Join(realWorkspace, "apps", "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkWorkspace := filepath.Join(tmp, "link-workspace")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/alpha",
			Alias:       "alpha",
			DesiredPath: "apps/alpha",
		}),
	}, []state.Project{{
		ID:               1,
		Alias:            "alpha",
		NormalizedRemote: "https://github.com/BramVR/alpha",
		CloneURL:         "https://github.com/BramVR/alpha.git",
		LocalPath:        filepath.Join(realWorkspace, "apps", "alpha"),
	}}, linkWorkspace)
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeUnchanged {
		t.Fatalf("changes = %#v, want unchanged through symlinked workspace root", plan.Changes)
	}
}

func TestImportPlanAllowsAddsUnderSymlinkedWorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	realWorkspace := filepath.Join(tmp, "real-workspace")
	if err := os.MkdirAll(realWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	linkWorkspace := filepath.Join(tmp, "link-workspace")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/alpha",
			Alias:       "alpha",
			DesiredPath: "apps/alpha",
		}),
	}, nil, linkWorkspace)
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeAdd {
		t.Fatalf("changes = %#v, want add under symlinked workspace root", plan.Changes)
	}
	if plan.Changes[0].LocalPath != filepath.Join(linkWorkspace, "apps", "alpha") {
		t.Fatalf("local path = %q, want symlink-root placement", plan.Changes[0].LocalPath)
	}
}

func TestImportPlanRejectsSymlinkedDesiredPathOutsideWorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "shared")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/alpha",
			Alias:       "alpha",
			DesiredPath: "shared/alpha",
		}),
	}, nil, workspace)
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeConflict {
		t.Fatalf("changes = %#v, want outside-workspace conflict", plan.Changes)
	}
	if !strings.Contains(plan.Changes[0].ConflictReason, "outside workspace root") {
		t.Fatalf("conflict reason = %q, want outside workspace root", plan.Changes[0].ConflictReason)
	}
}

func TestImportPlanReportsPathConflictsAgainstRegistryAndFilesystem(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		entries  []Entry
		projects []state.Project
		want     string
	}{
		"registered exact path": {
			entries: []Entry{NewEntry(ProjectEntry{
				Identity:    "https://github.com/BramVR/beta",
				Alias:       "beta",
				DesiredPath: "apps/alpha",
			})},
			projects: []state.Project{{
				ID:               1,
				Alias:            "alpha",
				NormalizedRemote: "https://github.com/BramVR/alpha",
				CloneURL:         "https://github.com/BramVR/alpha.git",
				LocalPath:        filepath.Join(workspace, "apps", "alpha"),
			}},
			want: "already requested",
		},
		"registered nested path": {
			entries: []Entry{NewEntry(ProjectEntry{
				Identity:    "https://github.com/BramVR/beta",
				Alias:       "beta",
				DesiredPath: "apps/alpha/vendor",
			})},
			projects: []state.Project{{
				ID:               1,
				Alias:            "alpha",
				NormalizedRemote: "https://github.com/BramVR/alpha",
				CloneURL:         "https://github.com/BramVR/alpha.git",
				LocalPath:        filepath.Join(workspace, "apps", "alpha"),
			}},
			want: "nests",
		},
		"manifest nested path": {
			entries: []Entry{
				NewEntry(ProjectEntry{
					Identity:    "https://github.com/BramVR/alpha",
					Alias:       "alpha",
					DesiredPath: "apps/alpha",
				}),
				NewEntry(ProjectEntry{
					Identity:    "https://github.com/BramVR/beta",
					Alias:       "beta",
					DesiredPath: "apps/alpha/vendor",
				}),
			},
			want: "nests",
		},
		"filesystem path": {
			entries: []Entry{NewEntry(ProjectEntry{
				Identity:    "https://github.com/BramVR/taken",
				Alias:       "taken",
				DesiredPath: "taken",
			})},
			want: "exists outside",
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := PlanImport(tc.entries, tc.projects, workspace)
			if err != nil {
				t.Fatalf("PlanImport error = %v", err)
			}
			if len(plan.Changes) == 0 {
				t.Fatal("changes = empty, want conflict")
			}
			last := plan.Changes[len(plan.Changes)-1]
			if last.Action != ChangeConflict {
				t.Fatalf("last change = %#v, want conflict", last)
			}
			if !strings.Contains(last.ConflictReason, tc.want) {
				t.Fatalf("conflict reason = %q, want %q", last.ConflictReason, tc.want)
			}
		})
	}
}

func TestImportPlanAllowsWorkspaceRootProjectOnlyWhenRootIsEmpty(t *testing.T) {
	tmp := t.TempDir()
	workspace := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	rootEntry := []Entry{NewEntry(ProjectEntry{
		Identity:    "https://github.com/BramVR/root",
		Alias:       "root",
		DesiredPath: ".",
	})}

	plan, err := PlanImport(rootEntry, nil, workspace)
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeAdd {
		t.Fatalf("changes = %#v, want add for empty workspace root project", plan.Changes)
	}
	if plan.Changes[0].LocalPath != workspace {
		t.Fatalf("local path = %q, want workspace root %q", plan.Changes[0].LocalPath, workspace)
	}

	if err := os.WriteFile(filepath.Join(workspace, "local.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = PlanImport(rootEntry, nil, workspace)
	if err != nil {
		t.Fatalf("second PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeConflict {
		t.Fatalf("changes = %#v, want root path conflict", plan.Changes)
	}
	if !strings.Contains(plan.Changes[0].ConflictReason, "workspace root") {
		t.Fatalf("conflict reason = %q, want workspace root detail", plan.Changes[0].ConflictReason)
	}
}

type failingUpdateStore struct {
	projects []state.Project
	failed   bool
}

func (s *failingUpdateStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s *failingUpdateStore) AddProject(context.Context, state.Project) (state.Project, error) {
	return state.Project{}, errors.New("unexpected add")
}

func (s *failingUpdateStore) UpdateProject(_ context.Context, id int64, project state.Project) (state.Project, error) {
	if !s.failed && id == 2 && project.Alias == "first" && project.NormalizedRemote == "https://github.com/BramVR/second" {
		s.failed = true
		return state.Project{}, errors.New("injected update failure")
	}
	for i := range s.projects {
		if s.projects[i].ID == id {
			project.ID = id
			s.projects[i] = project
			return project, nil
		}
	}
	return state.Project{}, errors.New("project not found")
}

func (s *failingUpdateStore) DeleteProject(_ context.Context, id int64) error {
	for i := range s.projects {
		if s.projects[i].ID == id {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return nil
		}
	}
	return errors.New("project not found")
}

type failingAddStore struct {
	projects  []state.Project
	nextID    int64
	failAlias string
}

func (s *failingAddStore) ListProjects(context.Context) ([]state.Project, error) {
	return append([]state.Project(nil), s.projects...), nil
}

func (s *failingAddStore) AddProject(_ context.Context, project state.Project) (state.Project, error) {
	if project.Alias == s.failAlias {
		return state.Project{}, errors.New("injected add failure")
	}
	project.ID = s.nextID
	s.nextID++
	s.projects = append(s.projects, project)
	return project, nil
}

func (s *failingAddStore) UpdateProject(_ context.Context, id int64, project state.Project) (state.Project, error) {
	for i := range s.projects {
		if s.projects[i].ID == id {
			project.ID = id
			s.projects[i] = project
			return project, nil
		}
	}
	return state.Project{}, errors.New("project not found")
}

func (s *failingAddStore) DeleteProject(_ context.Context, id int64) error {
	for i := range s.projects {
		if s.projects[i].ID == id {
			s.projects = append(s.projects[:i], s.projects[i+1:]...)
			return nil
		}
	}
	return errors.New("project not found")
}

func TestImportPlanSanitizesCurrentCloneURLDiff(t *testing.T) {
	secretURL := "https://user:leak-marker@example.invalid/org/repo.git?marker=leak-marker-value#piece"
	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://example.invalid/org/repo",
			Alias:       "repo",
			DesiredPath: "repo",
			CloneHints:  CloneHints{URL: "https://example.invalid/org/repo.git"},
		}),
	}, []state.Project{{
		ID:               1,
		Alias:            "repo",
		NormalizedRemote: "https://example.invalid/org/repo",
		CloneURL:         secretURL,
		LocalPath:        "/workspace/repo",
	}}, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeUpdate {
		t.Fatalf("changes = %#v, want clone URL update", plan.Changes)
	}
	if len(plan.Changes[0].Fields) != 1 || plan.Changes[0].Fields[0].Field != "clone_url" {
		t.Fatalf("fields = %#v, want clone_url diff", plan.Changes[0].Fields)
	}
	current := plan.Changes[0].Fields[0].Current
	if strings.Contains(current, "leak-marker") || strings.Contains(current, "user:") || strings.Contains(current, "?") || strings.Contains(current, "#") {
		t.Fatalf("current clone URL diff leaked secret-bearing data: %q", current)
	}
}

func TestImportPlanReportsAliasConflict(t *testing.T) {
	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/new-project",
			Alias:       "codemesh",
			DesiredPath: "new-project",
		}),
	}, []state.Project{{
		ID:               1,
		Alias:            "codemesh",
		NormalizedRemote: "https://github.com/BramVR/codemesh",
		CloneURL:         "https://github.com/BramVR/codemesh.git",
		LocalPath:        "/workspace/codemesh",
	}}, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Action != ChangeConflict {
		t.Fatalf("changes = %#v, want one alias conflict", plan.Changes)
	}
	if !strings.Contains(plan.Changes[0].ConflictReason, "codemesh") {
		t.Fatalf("conflict reason = %q, want alias detail", plan.Changes[0].ConflictReason)
	}
}

func TestImportPlanAllowsAliasReuseAfterManifestRename(t *testing.T) {
	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/second",
			Alias:       "old",
			DesiredPath: "second",
		}),
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/first",
			Alias:       "new",
			DesiredPath: "first",
		}),
	}, []state.Project{{
		ID:               1,
		Alias:            "old",
		NormalizedRemote: "https://github.com/BramVR/first",
		CloneURL:         "https://github.com/BramVR/first.git",
		LocalPath:        "/workspace/first",
	}}, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("change count = %d, want 2: %#v", len(plan.Changes), plan.Changes)
	}
	if plan.Changes[0].Action != ChangeAdd || plan.Changes[1].Action != ChangeUpdate {
		t.Fatalf("changes = %#v, want add then update", plan.Changes)
	}
}

func TestImportPlanReportsManifestBatchAliasConflict(t *testing.T) {
	plan, err := PlanImport([]Entry{
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/first",
			Alias:       "shared",
			DesiredPath: "first",
		}),
		NewEntry(ProjectEntry{
			Identity:    "https://github.com/BramVR/second",
			Alias:       "shared",
			DesiredPath: "second",
		}),
	}, nil, "/workspace")
	if err != nil {
		t.Fatalf("PlanImport error = %v", err)
	}
	if len(plan.Changes) != 2 {
		t.Fatalf("change count = %d, want 2: %#v", len(plan.Changes), plan.Changes)
	}
	if plan.Changes[0].Action != ChangeAdd || plan.Changes[1].Action != ChangeConflict {
		t.Fatalf("changes = %#v, want add then conflict", plan.Changes)
	}
	if !strings.Contains(plan.Changes[1].ConflictReason, "shared") {
		t.Fatalf("conflict reason = %q, want alias detail", plan.Changes[1].ConflictReason)
	}
}

func TestImportPlanRejectsCredentialBearingCloneHintWithoutEchoingIt(t *testing.T) {
	for _, secretURL := range []string{
		"https://user:leak-marker@example.invalid/org/repo.git?marker=leak-marker-value#piece",
		"git@example.invalid:org/repo.git?marker=leak-marker-value#piece",
		"repo.git",
	} {
		t.Run(secretURL, func(t *testing.T) {
			_, err := PlanImport([]Entry{
				{
					ManifestVersion: ManifestVersion,
					Project: ProjectEntry{
						Identity:    "https://example.invalid/org/repo",
						Alias:       "repo",
						DesiredPath: "repo",
						CloneHints:  CloneHints{URL: secretURL},
						Groups:      []string{},
					},
				},
			}, nil, "/workspace")
			if err == nil {
				t.Fatal("PlanImport error = nil, want credential-bearing clone hint rejection")
			}
			if strings.Contains(err.Error(), "leak-marker") || strings.Contains(err.Error(), secretURL) {
				t.Fatalf("PlanImport error leaked clone hint: %v", err)
			}
		})
	}
}

func hasFieldChange(change ImportChange, field, current, desired string) bool {
	for _, candidate := range change.Fields {
		if candidate.Field == field && candidate.Current == current && candidate.Desired == desired {
			return true
		}
	}
	return false
}
