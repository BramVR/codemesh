package workspacemanifest

import (
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
