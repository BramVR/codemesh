package gitops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRemoteTreatsGitHubFormsAsOneIdentity(t *testing.T) {
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
		t.Fatalf("normalized identity = %q", want)
	}
}

func TestNormalizeRemotePreservesGenericSSHAndLocalRemotes(t *testing.T) {
	got, err := NormalizeRemote("ssh://git@git.example.com:2222/group/repo.git")
	if err != nil {
		t.Fatalf("NormalizeRemote error = %v", err)
	}
	if got != "ssh://git@git.example.com:2222/group/repo" {
		t.Fatalf("normalized remote = %q", got)
	}

	root := filepath.Join("tmp", "workspace", "source")
	got, err = NormalizeRemoteFrom("../remotes/repo.git", root)
	if err != nil {
		t.Fatalf("NormalizeRemoteFrom error = %v", err)
	}
	want := filepath.Clean(filepath.Join("tmp", "workspace", "remotes", "repo.git"))
	if got != want {
		t.Fatalf("normalized local remote = %q, want %q", got, want)
	}
}

func TestRemoteMatchesSourceAcceptsRegisteredRemoteOrCloneURL(t *testing.T) {
	baseDir := filepath.Join("tmp", "workspace", "project")
	registered := "https://github.com/BramVR/codemesh"
	cloneURL := "../remotes/codemesh.git"
	cloneIdentity, err := NormalizeRemoteFrom(cloneURL, baseDir)
	if err != nil {
		t.Fatalf("NormalizeRemoteFrom error = %v", err)
	}

	if !RemoteMatchesSource(registered, registered, cloneURL, baseDir) {
		t.Fatal("RemoteMatchesSource rejected registered remote identity")
	}
	if !RemoteMatchesSource(cloneIdentity, registered, cloneURL, baseDir) {
		t.Fatal("RemoteMatchesSource rejected preserved clone URL identity")
	}
	if RemoteMatchesSource("https://github.com/BramVR/other", registered, cloneURL, baseDir) {
		t.Fatal("RemoteMatchesSource accepted unrelated remote")
	}
}

func TestCloneURLForStripsCredentialsButPreservesTransport(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "https://user:redactme@example.invalid/org/repo.git", want: "https://example.invalid/org/repo.git"},
		{raw: "ssh://git:redactme@example.invalid/org/repo.git", want: "ssh://git@example.invalid/org/repo.git"},
		{raw: "git@example.invalid:org/repo.git", want: "git@example.invalid:org/repo.git"},
	}
	for _, tt := range tests {
		if got := CloneURLFor(tt.raw, ""); got != tt.want {
			t.Fatalf("CloneURLFor(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestValidateCloneSourceAllowsSecretFreeSources(t *testing.T) {
	for _, source := range []string{
		"/tmp/codemesh-fixtures/repo.git",
		`C:\tmp\codemesh-fixtures\repo.git`,
		"C:/tmp/codemesh-fixtures/repo.git",
		"file:///tmp/codemesh-fixtures/repo.git",
		"file:///C:/tmp/codemesh-fixtures/repo.git",
		"git@example.invalid:org/repo.git",
		"ssh://git@example.invalid/org/repo.git",
		"HTTPS://example.invalid/org/repo.git",
		"https://example.invalid/org/repo.git",
	} {
		t.Run(source, func(t *testing.T) {
			if err := ValidateCloneSource(source); err != nil {
				t.Fatalf("ValidateCloneSource error = %v", err)
			}
		})
	}
}

func TestValidateCloneSourceRejectsUnsafeSourcesWithoutEchoing(t *testing.T) {
	for _, source := range []string{
		"https://user:redaction-fixture@example.invalid/org/repo.git?credential=redaction-fixture#piece",
		"HTTPS://redaction-fixture@example.invalid/org/repo.git",
		"ssh://git:redaction-fixture@example.invalid/org/repo.git",
		"ssh://-oProxyCommand=redaction-fixture.example.invalid/org/repo.git",
		"ssh://git@example.invalid/-repo.git",
		"ssh://-git@example.invalid/org/repo.git",
		"git@example.invalid:org/repo.git?credential=redaction-fixture#piece",
		"-oProxyCommand=redaction-fixture@example.invalid:org/repo.git",
		"git@example.invalid:-repo.git",
		"git@example.invalid:org/repo with space.git",
		"http://example.invalid/org/repo.git",
		"ext::ssh -oProxyCommand=redaction-fixture example.invalid/org/repo.git",
		"file://example.invalid/tmp/repo.git",
		"file:repo.git",
		"C:repo.git",
		"C:repo.git?credential=redaction-fixture",
		"repo.git",
	} {
		t.Run(source, func(t *testing.T) {
			err := ValidateCloneSource(source)
			if err == nil {
				t.Fatal("ValidateCloneSource error = nil, want rejection")
			}
			if strings.Contains(err.Error(), "redaction-fixture") || strings.Contains(err.Error(), source) {
				t.Fatalf("validation error leaked source: %v", err)
			}
		})
	}
}

func TestRedactionRemovesCredentialBearingURLParts(t *testing.T) {
	raw := "https://redactuser:redactme@example.invalid/org/repo.git?credential=redactme#fragment"

	metadata := RedactURLForMetadata(raw)
	output := RedactCloneOutput("fatal: could not read "+raw+" or https://redactuser@example.invalid/org/repo.git or https://redactuser@example.invalid/org/repo.git#fragment", raw)

	for _, got := range []string{metadata, output} {
		if strings.Contains(got, "redactuser") || strings.Contains(got, "redactme") || strings.Contains(got, "credential") || strings.Contains(got, "fragment") {
			t.Fatalf("redaction leaked credential-bearing part: %s", got)
		}
	}
	if metadata != "https://redacted@example.invalid/org/repo.git" {
		t.Fatalf("metadata redaction = %q", metadata)
	}
	if !strings.Contains(output, "https://redacted@example.invalid/org/repo.git") {
		t.Fatalf("clone output redaction = %q", output)
	}
}

func TestCommandErrorNormalizesDetailAndMissingRef(t *testing.T) {
	err := NewCommandError([]string{"fetch", "--quiet", "origin", "refs/heads/main"}, []byte("fatal: couldn't find remote ref refs/heads/main\n"), errors.New("exit status 128"))

	if err.Error() != "git fetch --quiet origin refs/heads/main failed: fatal: couldn't find remote ref refs/heads/main" {
		t.Fatalf("error = %q", err.Error())
	}
	if CommandDetail(err) != "fatal: couldn't find remote ref refs/heads/main" {
		t.Fatalf("detail = %q", CommandDetail(err))
	}
	if !IsMissingRemoteRef(err) {
		t.Fatalf("IsMissingRemoteRef = false, want true")
	}
}

func TestFakeRunnerCapturesCommands(t *testing.T) {
	fake := &FakeRunner{Responses: []FakeResponse{{Output: "main\n"}}}
	client := New(fake)

	got, err := client.Output(context.Background(), "/repo", "branch", "--show-current")

	if err != nil {
		t.Fatalf("Output error = %v", err)
	}
	if got != "main\n" {
		t.Fatalf("Output = %q", got)
	}
	if len(fake.Calls) != 1 || fake.Calls[0].Dir != "/repo" || strings.Join(fake.Calls[0].Args, " ") != "branch --show-current" {
		t.Fatalf("calls = %#v", fake.Calls)
	}
}

func TestProcessRunnerReturnsOnlyStdoutOnSuccess(t *testing.T) {
	output, err := ProcessRunner{}.Run(
		context.Background(),
		"",
		"-c",
		`alias.noisy=!f() { printf "stdout\n"; printf "stderr\n" >&2; }; f`,
		"noisy",
	)

	if err != nil {
		t.Fatalf("Run error = %v", err)
	}
	if output != "stdout\n" {
		t.Fatalf("output = %q, want stdout only", output)
	}
}
