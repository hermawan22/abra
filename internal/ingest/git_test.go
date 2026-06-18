package ingest

import "testing"

func TestGitFileURLSupportsBitbucket(t *testing.T) {
	got := gitFileURL(gitIdentity{
		RemoteURL: "https://bitbucket.org/acme/frontend.git",
		Ref:       "main",
		Provider:  "bitbucket",
	}, "src/app.tsx")
	want := "https://bitbucket.org/acme/frontend/src/main/src/app.tsx"
	if got != want {
		t.Fatalf("git file url = %q, want %q", got, want)
	}
}
