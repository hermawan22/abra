package jobs

import (
	"path/filepath"
	"testing"

	"github.com/hermawan22/abra/internal/ingest"
)

func TestSourceConfigIngestSpecFromConfig(t *testing.T) {
	source := SourceConfig{
		ID:         "docs",
		Scope:      "team:example",
		SourceType: ingest.SourceTypeLocalRepo,
		Name:       "Docs",
		Config: map[string]any{
			"root":           "/repo",
			"include":        []any{"README.md", "docs/**/*.md"},
			"exclude":        "private/**,vendor/**",
			"include_code":   true,
			"code_include":   []any{"src/**/*.ts", "src/**/*.tsx"},
			"code_exclude":   "src/**/*.test.tsx",
			"repository_url": "https://gitlab.example.com/platform/frontend.git",
			"branch":         "main",
			"commit":         "abc1234",
			"provider":       "gitlab",
			"project_path":   "platform/frontend",
		},
		Metadata: map[string]any{"owner": "frontend"},
	}

	spec, err := source.IngestSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Root != "/repo" {
		t.Fatalf("root = %q", spec.Root)
	}
	if len(spec.Include) != 2 || spec.Include[1] != "docs/**/*.md" {
		t.Fatalf("include = %#v", spec.Include)
	}
	if len(spec.Exclude) != 2 || spec.Exclude[0] != "private/**" {
		t.Fatalf("exclude = %#v", spec.Exclude)
	}
	if !spec.IncludeCode || len(spec.CodeInclude) != 2 || spec.CodeInclude[1] != "src/**/*.tsx" {
		t.Fatalf("code include = %v %#v", spec.IncludeCode, spec.CodeInclude)
	}
	if len(spec.CodeExclude) != 1 || spec.CodeExclude[0] != "src/**/*.test.tsx" {
		t.Fatalf("code exclude = %#v", spec.CodeExclude)
	}
	if spec.Metadata["source_config_id"] != "docs" || spec.Metadata["owner"] != "frontend" {
		t.Fatalf("metadata = %#v", spec.Metadata)
	}
	if spec.GitRemoteURL != "https://gitlab.example.com/platform/frontend.git" ||
		spec.GitRef != "main" ||
		spec.GitRevision != "abc1234" ||
		spec.GitProvider != "gitlab" ||
		spec.GitProjectPath != "platform/frontend" {
		t.Fatalf("git overlay = %+v", spec)
	}
}

func TestSourceConfigIngestSpecFromFileBaseURL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "knowledge")
	source := SourceConfig{
		ID:         "knowledge",
		Scope:      "company",
		SourceType: ingest.SourceTypeMarkdown,
		Name:       "Knowledge",
		BaseURL:    "file://" + filepath.ToSlash(root),
		Config:     map[string]any{},
	}

	spec, err := source.IngestSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Root != root {
		t.Fatalf("root = %q, want %q", spec.Root, root)
	}
}

func TestSourceConfigIngestSpecFromGitRepoBaseURL(t *testing.T) {
	source := SourceConfig{
		ID:         "frontend",
		Scope:      "team:example",
		SourceType: ingest.SourceTypeGitRepo,
		Name:       "Frontend",
		BaseURL:    "https://bitbucket.org/acme/frontend.git",
		Config: map[string]any{
			"branch":       "main",
			"git_depth":    3,
			"include_code": true,
			"code_include": []any{"src/**/*.tsx"},
		},
	}

	spec, err := source.IngestSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Root != "" {
		t.Fatalf("git_repo root before checkout = %q", spec.Root)
	}
	if spec.GitRemoteURL != "https://bitbucket.org/acme/frontend.git" || spec.GitRef != "main" || spec.GitDepth != 3 {
		t.Fatalf("git repo spec = %+v", spec)
	}
	if !spec.IncludeCode || len(spec.CodeInclude) != 1 || spec.CodeInclude[0] != "src/**/*.tsx" {
		t.Fatalf("code include = %v %#v", spec.IncludeCode, spec.CodeInclude)
	}
}

func TestSourceConfigValidateIngestContractRejectsInvalidCoreSource(t *testing.T) {
	source := SourceConfig{
		ID:         "repo",
		Scope:      "team:example",
		SourceType: ingest.SourceTypeLocalRepo,
		Name:       "Frontend",
		BaseURL:    "https://example.com/frontend",
		Config:     map[string]any{},
	}
	if err := source.ValidateIngestContract(); err == nil {
		t.Fatal("expected invalid remote base_url to fail for core local_repo ingestion")
	}
}

func TestSourceConfigValidateIngestContractAllowsOverlaySource(t *testing.T) {
	source := SourceConfig{
		ID:         "jira",
		Scope:      "team:platform",
		SourceType: ingest.SourceType("jira"),
		Name:       "Jira Project",
		BaseURL:    "https://jira.example.local",
		Config:     map[string]any{"project": "ABRA"},
	}
	if err := source.ValidateIngestContract(); err != nil {
		t.Fatalf("overlay source should not be validated as core worker source: %v", err)
	}
}
