package brain

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hermawan22/abra/internal/graph"
	"github.com/hermawan22/abra/internal/store"
)

type summaryInput struct {
	DocumentID     string
	Input          IngestDocumentInput
	Content        string
	CodePath       string
	Metadata       map[string]any
	CodeCandidates graph.CandidateSet
}

type codeSummaryBuckets struct {
	Routes     []string
	Symbols    []string
	Components []string
	Packages   []string
	Imports    []string
	Exports    []string
}

func (s *Service) RebuildSummaries(ctx context.Context, input RebuildSummariesInput) (RebuildSummariesResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		return RebuildSummariesResult{}, fmt.Errorf("scope is required")
	}
	if input.Limit < 1 || input.Limit > 10000 {
		input.Limit = 1000
	}
	docs, err := s.db.ListDocumentsForSummary(ctx, input.Scope, input.Limit)
	if err != nil {
		return RebuildSummariesResult{}, err
	}
	total := 0
	for _, doc := range docs {
		summaries, err := s.upsertMemorySummaries(ctx, summaryInput{
			DocumentID: doc.DocumentID,
			Input: IngestDocumentInput{
				SourceType: doc.SourceType,
				SourceURL:  doc.SourceURL,
				SourceID:   doc.SourceID,
				Title:      doc.Title,
				Scope:      doc.Scope,
				Content:    doc.Content,
				Metadata:   doc.Metadata,
			},
			Content:  doc.Content,
			CodePath: codeGraphPath(IngestDocumentInput{Metadata: doc.Metadata}),
			Metadata: map[string]any{
				"rebuilt":        true,
				"document_id":    doc.DocumentID,
				"source_type":    doc.SourceType,
				"relation_count": doc.Relations,
				"chunk_count":    doc.Chunks,
			},
		})
		if err != nil {
			return RebuildSummariesResult{}, err
		}
		total += summaries
	}
	_ = s.db.InsertAuditEvent(ctx, "memory_summaries.rebuilt", "scope", input.Scope, input.Scope, "", map[string]any{"documents": len(docs), "summaries": total, "limit": input.Limit})
	return RebuildSummariesResult{Scope: input.Scope, Documents: len(docs), Summaries: total}, nil
}

func (s *Service) upsertMemorySummaries(ctx context.Context, input summaryInput) (int, error) {
	path := input.CodePath
	if path == "" {
		path = firstNonEmpty(metadataString(input.Input.Metadata, "git_path"), metadataString(input.Input.Metadata, "ingest_path"), input.Input.Title)
	}
	path = normalizeSummaryPath(path)
	if path == "" {
		path = input.Input.SourceURL
	}
	input.CodeCandidates = codeCandidatesForSummary(input, path)
	summaries := []store.MemorySummaryRecord{
		documentSummary(input, path),
		sourceSummary(input),
	}
	if module := moduleSummary(input, path); module.Key != "" {
		summaries = append(summaries, module)
	}
	summaries = append(summaries, codeIntelligenceSummaries(input, path)...)

	count := 0
	for _, summary := range summaries {
		if summary.Key == "" || summary.Summary == "" {
			continue
		}
		if _, err := s.db.UpsertMemorySummary(ctx, summary); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func documentSummary(input summaryInput, path string) store.MemorySummaryRecord {
	contentKind := metadataString(input.Input.Metadata, "content_kind")
	summary := ""
	relationCount := 0
	if contentKind == "code" || (input.CodePath != "" && graph.IsCodeGraphPath(input.CodePath)) {
		relationCount = len(input.CodeCandidates.Relations)
		summary = codeSummary(path, input.CodeCandidates)
	} else {
		summary = textSummary(input.Input.Title, input.Content)
	}
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "file",
		Key:           path,
		Title:         path,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: relationCount,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"source_type":   input.Input.SourceType,
			"content_kind":  contentKind,
			"summary_kind":  "deterministic",
			"summary_level": "file",
		}),
	}
}

func codeCandidatesForSummary(input summaryInput, path string) graph.CandidateSet {
	contentKind := metadataString(input.Input.Metadata, "content_kind")
	if contentKind != "code" && (input.CodePath == "" || !graph.IsCodeGraphPath(input.CodePath)) {
		return graph.CandidateSet{}
	}
	return graph.ExtractCodeFile(graph.CodeFile{
		Path:      path,
		Content:   input.Content,
		SourceID:  input.Input.SourceID,
		SourceURL: input.Input.SourceURL,
	})
}

func codeIntelligenceSummaries(input summaryInput, path string) []store.MemorySummaryRecord {
	if len(input.CodeCandidates.Entities) == 0 && len(input.CodeCandidates.Relations) == 0 {
		return nil
	}
	buckets := codeIntelligenceBuckets(input.CodeCandidates)
	repo := repoSummary(input, path, buckets)
	out := []store.MemorySummaryRecord{repo}
	out = append(out, entitySummaries(input, path, buckets)...)
	return out
}

func codeIntelligenceBuckets(candidates graph.CandidateSet) codeSummaryBuckets {
	buckets := codeSummaryBuckets{}
	for _, entity := range candidates.Entities {
		switch entity.Type {
		case "route":
			buckets.Routes = append(buckets.Routes, entity.Name)
		case "symbol":
			buckets.Symbols = append(buckets.Symbols, entity.Name)
		case "component":
			buckets.Components = append(buckets.Components, entity.Name)
		case "package":
			buckets.Packages = append(buckets.Packages, entity.Name)
		}
	}
	for _, relation := range candidates.Relations {
		switch relation.Type {
		case "imports":
			buckets.Imports = append(buckets.Imports, relation.To)
		case "exports":
			buckets.Exports = append(buckets.Exports, relation.To)
		}
	}
	buckets.Routes = uniqueSortedStrings(buckets.Routes)
	buckets.Symbols = uniqueSortedStrings(buckets.Symbols)
	buckets.Components = uniqueSortedStrings(buckets.Components)
	buckets.Packages = uniqueSortedStrings(buckets.Packages)
	buckets.Imports = uniqueSortedStrings(buckets.Imports)
	buckets.Exports = uniqueSortedStrings(buckets.Exports)
	return buckets
}

func repoSummary(input summaryInput, path string, buckets codeSummaryBuckets) store.MemorySummaryRecord {
	key := repoSummaryKey(input.Input)
	module := moduleKey(path)
	parts := []string{"Repository " + key + " code intelligence includes " + path + "."}
	if module != "" {
		parts = append(parts, "Area "+module+".")
	}
	if len(buckets.Routes) > 0 {
		parts = append(parts, "Routes "+strings.Join(limitStrings(buckets.Routes, 6), ", ")+".")
	}
	if len(buckets.Components) > 0 {
		parts = append(parts, "Components "+strings.Join(limitStrings(buckets.Components, 6), ", ")+".")
	}
	if len(buckets.Exports) > 0 {
		parts = append(parts, "Exports "+strings.Join(limitStrings(buckets.Exports, 8), ", ")+".")
	}
	if len(buckets.Packages) > 0 {
		parts = append(parts, "Packages "+strings.Join(limitStrings(buckets.Packages, 8), ", ")+".")
	}
	summary := strings.Join(parts, " ")
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "repo",
		Key:           key,
		Title:         key,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: len(input.CodeCandidates.Relations),
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"source_type":   input.Input.SourceType,
			"summary_kind":  "deterministic",
			"summary_level": "repo",
			"code_path":     path,
		}),
	}
}

func entitySummaries(input summaryInput, path string, buckets codeSummaryBuckets) []store.MemorySummaryRecord {
	type entityGroup struct {
		level  string
		values []string
		text   string
	}
	groups := []entityGroup{
		{level: "route", values: buckets.Routes, text: "Route"},
		{level: "component", values: buckets.Components, text: "Component"},
		{level: "symbol", values: buckets.Symbols, text: "Symbol"},
		{level: "package", values: buckets.Packages, text: "Package"},
	}
	out := []store.MemorySummaryRecord{}
	for _, group := range groups {
		for _, value := range limitStrings(group.values, 24) {
			summary := group.text + " " + value + " is connected to " + path + "."
			if len(buckets.Imports) > 0 && group.level != "package" {
				summary += " Nearby imports: " + strings.Join(limitStrings(buckets.Imports, 5), ", ") + "."
			}
			if len(buckets.Exports) > 0 && (group.level == "route" || group.level == "component") {
				summary += " Nearby exports: " + strings.Join(limitStrings(buckets.Exports, 5), ", ") + "."
			}
			out = append(out, store.MemorySummaryRecord{
				Scope:         input.Input.Scope,
				Level:         group.level,
				Key:           value,
				Title:         value,
				Summary:       summary,
				SourceCount:   1,
				RelationCount: len(input.CodeCandidates.Relations),
				TokenEstimate: tokenEstimate(summary),
				SourceURLs:    []string{input.Input.SourceURL},
				Metadata: mergeMetadata(input.Metadata, map[string]any{
					"document_id":   input.DocumentID,
					"source_type":   input.Input.SourceType,
					"summary_kind":  "deterministic",
					"summary_level": group.level,
					"code_path":     path,
				}),
			})
		}
	}
	return out
}

func repoSummaryKey(input IngestDocumentInput) string {
	for _, key := range []string{"repo", "repository", "repository_slug", "repo_slug", "project", "source_id"} {
		if value := metadataString(input.Metadata, key); value != "" {
			return normalizeSummaryPath(value)
		}
	}
	if strings.TrimSpace(input.SourceID) != "" {
		return normalizeSummaryPath(input.SourceID)
	}
	if strings.TrimSpace(input.SourceURL) != "" {
		value := strings.TrimSuffix(strings.TrimSpace(input.SourceURL), "/")
		if base := filepath.Base(value); base != "." && base != "/" && base != "" {
			return normalizeSummaryPath(base)
		}
	}
	return "repository"
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func moduleSummary(input summaryInput, path string) store.MemorySummaryRecord {
	module := moduleKey(path)
	if module == "" {
		return store.MemorySummaryRecord{}
	}
	summary := "Module " + module + " includes " + path + ". Latest observed source: " + input.Input.Title + "."
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "module",
		Key:           module,
		Title:         module,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: 0,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"summary_kind":  "deterministic",
			"summary_level": "module",
		}),
	}
}

func sourceSummary(input summaryInput) store.MemorySummaryRecord {
	key := strings.TrimSpace(input.Input.SourceType)
	if key == "" {
		key = "source"
	}
	summary := "Source type " + key + " contributed " + input.Input.Title + " to scope " + input.Input.Scope + "."
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "source",
		Key:           key,
		Title:         key,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: 0,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"summary_kind":  "deterministic",
			"summary_level": "source",
		}),
	}
}

func codeSummary(path string, candidates graph.CandidateSet) string {
	imports := []string{}
	exports := []string{}
	symbols := []string{}
	components := []string{}
	routes := []string{}
	for _, relation := range candidates.Relations {
		switch relation.Type {
		case "defines_symbol":
			symbols = append(symbols, relation.To)
		case "imports":
			imports = append(imports, relation.To)
		case "exports":
			exports = append(exports, relation.To)
		case "defines_component":
			components = append(components, relation.To)
		case "implemented_by":
			routes = append(routes, relation.From)
		}
	}
	parts := []string{"Code file " + path + "."}
	if len(routes) > 0 {
		parts = append(parts, "Implements route "+strings.Join(limitStrings(routes, 5), ", ")+".")
	}
	if len(imports) > 0 {
		parts = append(parts, "Imports "+strings.Join(limitStrings(imports, 8), ", ")+".")
	}
	if len(exports) > 0 {
		parts = append(parts, "Exports "+strings.Join(limitStrings(exports, 8), ", ")+".")
	}
	if len(symbols) > 0 {
		parts = append(parts, "Defines symbols "+strings.Join(limitStrings(uniqueSortedStrings(symbols), 8), ", ")+".")
	}
	if len(components) > 0 {
		parts = append(parts, "Defines component "+strings.Join(limitStrings(components, 5), ", ")+".")
	}
	return strings.Join(parts, " ")
}

func textSummary(title, content string) string {
	content = cleanClaim(content)
	if len(content) > 360 {
		content = strings.TrimSpace(content[:360]) + "..."
	}
	if title == "" {
		return content
	}
	return title + ": " + content
}

func moduleKey(path string) string {
	path = normalizeSummaryPath(path)
	if path == "" || !strings.Contains(path, "/") {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 3 && parts[0] == "src" {
		return strings.Join(parts[:2], "/")
	}
	return parts[0]
}

func normalizeSummaryPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	return strings.Trim(path, "/")
}

func tokenEstimate(value string) int {
	words := len(strings.Fields(value))
	runes := utf8.RuneCountInString(value)
	charEstimate := (runes + 3) / 4
	if words == 0 {
		return charEstimate
	}
	wordEstimate := (words * 4) / 3
	return max(1, max(wordEstimate, charEstimate))
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
