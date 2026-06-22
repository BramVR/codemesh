package registry

import (
	"path/filepath"
	"testing"
)

func TestNormalizeRemoteTreatsGitHubSSHandHTTPSAsSameIdentity(t *testing.T) {
	forms := []string{
		"git@github.com:BramVR/codemesh.git",
		"ssh://git@github.com/BramVR/codemesh.git",
		"https://github.com/BramVR/codemesh.git",
		"https://github.com/BramVR/codemesh",
	}

	var want string
	for _, form := range forms {
		got, err := NormalizeRemote(form)
		if err != nil {
			t.Fatalf("NormalizeRemote(%q) error = %v", form, err)
		}
		if want == "" {
			want = got
		}
		if got != want {
			t.Fatalf("NormalizeRemote(%q) = %q, want %q", form, got, want)
		}
	}
	if want != "https://github.com/BramVR/codemesh" {
		t.Fatalf("normalized GitHub identity = %q", want)
	}
}

func TestNormalizeRemotePreservesGenericSCPLikeSSHRemote(t *testing.T) {
	got, err := NormalizeRemote("git@gitlab.com:group/repo.git")
	if err != nil {
		t.Fatalf("NormalizeRemote error = %v", err)
	}

	if got != "ssh://git@gitlab.com/group/repo" {
		t.Fatalf("normalized remote = %q", got)
	}
}

func TestNormalizeRemotePreservesURLPort(t *testing.T) {
	got, err := NormalizeRemote("ssh://git@git.example.com:2222/group/repo.git")
	if err != nil {
		t.Fatalf("NormalizeRemote error = %v", err)
	}

	if got != "ssh://git@git.example.com:2222/group/repo" {
		t.Fatalf("normalized remote = %q", got)
	}
}

func TestNormalizeRemoteFromResolvesRelativeLocalRemoteAgainstProjectRoot(t *testing.T) {
	root := filepath.Join("tmp", "workspace", "source")
	got, err := NormalizeRemoteFrom("../remotes/repo.git", root)
	if err != nil {
		t.Fatalf("NormalizeRemoteFrom error = %v", err)
	}

	want := filepath.Clean(filepath.Join("tmp", "workspace", "remotes", "repo.git"))
	if got != want {
		t.Fatalf("normalized remote = %q, want %q", got, want)
	}
}
