package ingest

import "testing"

func TestRegistryClonesAndSortsSources(t *testing.T) {
	source := SourceSpec{
		ID:       "docs",
		Type:     SourceTypeLocalRepo,
		Root:     ".",
		Scope:    "team:platform",
		Include:  []string{"**/*.md"},
		Metadata: map[string]string{"authority": "high"},
	}
	registry, err := NewRegistry(source, SourceSpec{ID: "api", Type: SourceTypeMarkdown, Scope: "company"})
	if err != nil {
		t.Fatal(err)
	}

	source.Include[0] = "*.txt"
	source.Metadata["authority"] = "low"

	got, ok := registry.Get("docs")
	if !ok {
		t.Fatal("source was not registered")
	}
	if got.Include[0] != "**/*.md" {
		t.Fatalf("registry did not clone include patterns: %v", got.Include)
	}
	if got.Metadata["authority"] != "high" {
		t.Fatalf("registry did not clone metadata: %v", got.Metadata)
	}

	list := registry.List()
	if len(list) != 2 || list[0].ID != "api" || list[1].ID != "docs" {
		t.Fatalf("sources were not sorted by id: %+v", list)
	}
}

func TestRegistryRejectsDuplicateSources(t *testing.T) {
	registry, err := NewRegistry(SourceSpec{ID: "docs", Type: SourceTypeMarkdown, Scope: "company"})
	if err != nil {
		t.Fatal(err)
	}

	err = registry.Register(SourceSpec{ID: "docs", Type: SourceTypeMarkdown, Scope: "company"})
	if err == nil {
		t.Fatal("expected duplicate source error")
	}
}
