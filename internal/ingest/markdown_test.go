package ingest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRepoMarkdownIngestor(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Root\n\nUse source-backed claims.")
	writeFile(t, root, "docs/adr.md", "# Architecture Decision\n\nPrefer cited claims.")
	writeFile(t, root, "docs/private/secret.md", "# Secret\n\nDo not ingest.")
	writeFile(t, root, "src/app.ts", "console.log('skip')")

	ingestor, err := NewLocalRepoMarkdownIngestor(SourceSpec{
		ID:      "repo-docs",
		Type:    SourceTypeLocalRepo,
		Root:    root,
		Scope:   "team:platform",
		Include: []string{"**/*.md"},
		Exclude: []string{"docs/private/"},
		Metadata: map[string]string{
			"authority": "source",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	docs, err := ingestor.Ingest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d: %+v", len(docs), docs)
	}
	if docs[0].Path != "README.md" || docs[0].Title != "Root" {
		t.Fatalf("unexpected first doc: %+v", docs[0])
	}
	if docs[1].Path != "docs/adr.md" || docs[1].Title != "Architecture Decision" {
		t.Fatalf("unexpected second doc: %+v", docs[1])
	}
	for _, doc := range docs {
		if doc.Checksum == "" || doc.Fingerprint == "" {
			t.Fatalf("missing checksum or fingerprint: %+v", doc)
		}
		if doc.Scope != "team:platform" {
			t.Fatalf("scope not copied: %+v", doc)
		}
		if doc.Metadata["authority"] != "source" {
			t.Fatalf("metadata not copied: %+v", doc.Metadata)
		}
		if doc.SourceURL == "" {
			t.Fatalf("source url missing: %+v", doc)
		}
	}
}

func TestLocalRepoMarkdownIngestorAddsGitIdentity(t *testing.T) {
	repo := t.TempDir()
	const revision = "0123456789abcdef0123456789abcdef01234567"
	writeFile(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFile(t, repo, ".git/refs/heads/main", revision+"\n")
	writeFile(t, repo, ".git/config", "[remote \"origin\"]\n\turl = git@github.com:acme/docs.git\n")
	writeFile(t, repo, "docs/handbook.md", "# Handbook\n\nUse repo-backed source identity.")

	ingestor, err := NewLocalRepoMarkdownIngestor(SourceSpec{
		ID:      "repo-docs",
		Type:    SourceTypeLocalRepo,
		Root:    filepath.Join(repo, "docs"),
		Scope:   "team:platform",
		Include: []string{"**/*.md"},
	})
	if err != nil {
		t.Fatal(err)
	}

	docs, err := ingestor.Ingest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d: %+v", len(docs), docs)
	}
	doc := docs[0]
	if !strings.HasPrefix(doc.SourceURL, "https://github.com/acme/docs/blob/"+revision+"/docs/handbook.md") {
		t.Fatalf("source url = %q", doc.SourceURL)
	}
	if doc.Metadata["git_remote_url"] != "git@github.com:acme/docs.git" {
		t.Fatalf("git remote metadata = %#v", doc.Metadata)
	}
	if doc.Metadata["git_ref"] != "main" || doc.Metadata["git_revision"] != revision {
		t.Fatalf("git ref/revision metadata = %#v", doc.Metadata)
	}
	if doc.Metadata["git_path"] != "docs/handbook.md" {
		t.Fatalf("git path metadata = %#v", doc.Metadata)
	}
}

func TestLocalRepoMarkdownIngestorCanIncludeCode(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "README.md", "# Root\n\nUse source-backed claims.")
	writeFile(t, root, "src/pages/users/[id]/index.tsx", "import { Button } from 'example-ui-kit';\nexport default function UserPage() { return <Button /> }")
	writeFile(t, root, "internal/server/server.go", "package server\n\nfunc New() {}\n")
	writeFile(t, root, "node_modules/pkg/index.ts", "export const skip = true")

	ingestor, err := NewLocalRepoMarkdownIngestor(SourceSpec{
		ID:          "repo-code",
		Type:        SourceTypeLocalRepo,
		Root:        root,
		Scope:       "repo:app",
		Include:     []string{"README.md"},
		IncludeCode: true,
		CodeInclude: []string{"src/**/*.tsx", "internal/**/*.go"},
	})
	if err != nil {
		t.Fatal(err)
	}

	docs, err := ingestor.Ingest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected markdown and code docs, got %d: %+v", len(docs), docs)
	}
	goCode := docs[1]
	if goCode.Path != "internal/server/server.go" || goCode.Title != goCode.Path {
		t.Fatalf("unexpected Go code doc: %+v", goCode)
	}
	if goCode.Metadata["content_kind"] != "code" || goCode.Metadata["language"] != "go" {
		t.Fatalf("Go code metadata = %#v", goCode.Metadata)
	}
	tsxCode := docs[2]
	if tsxCode.Path != "src/pages/users/[id]/index.tsx" || tsxCode.Title != tsxCode.Path {
		t.Fatalf("unexpected TSX code doc: %+v", tsxCode)
	}
	if tsxCode.Metadata["content_kind"] != "code" || tsxCode.Metadata["language"] != "typescriptreact" {
		t.Fatalf("code metadata = %#v", tsxCode.Metadata)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
