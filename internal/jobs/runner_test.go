package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/ingest"
)

func TestRunnerIngestsOnlyChangedDocuments(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Root\n\nUse cited claims for durable memory.")
	writeTestFile(t, root, "docs/changed.md", "# Changed\n\nFrontend apps should use Playwright for E2E tests.")

	unchangedChecksum := ingest.Checksum([]byte("# Root\n\nUse cited claims for durable memory."))
	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "team:example",
			SourceType: ingest.SourceTypeLocalRepo,
			Name:       "Docs",
			Config: map[string]any{
				"root":           root,
				"include":        []any{"**/*.md"},
				"repository_url": "https://github.com/acme/frontend-docs.git",
				"branch":         "main",
				"commit":         "abc1234",
				"provider":       "github",
			},
		}},
		states: map[string]DocumentState{
			"README.md": {
				Found:             true,
				IngestFingerprint: ingest.Fingerprint("docs", "README.md", unchangedChecksum),
				IngestComplete:    true,
			},
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsSkipped != 1 || stats.DocumentsChanged != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	input := brain.inputs[0]
	if input.Title != "Changed" {
		t.Fatalf("title = %q", input.Title)
	}
	if input.Metadata["ingest_checksum"] == "" || input.Metadata["ingest_fingerprint"] == "" {
		t.Fatalf("missing checksum/fingerprint metadata: %#v", input.Metadata)
	}
	if input.Metadata["ingestion_job_id"] != "job" {
		t.Fatalf("missing ingestion job lineage: %#v", input.Metadata)
	}
	if input.SourceURL != "https://github.com/acme/frontend-docs/blob/abc1234/docs/changed.md" {
		t.Fatalf("source url = %q", input.SourceURL)
	}
	if input.Metadata["git_remote_url"] != "https://github.com/acme/frontend-docs.git" ||
		input.Metadata["git_ref"] != "main" ||
		input.Metadata["git_revision"] != "abc1234" ||
		input.Metadata["git_path"] != "docs/changed.md" {
		t.Fatalf("missing git metadata: %#v", input.Metadata)
	}
	if !store.success {
		t.Fatal("source success was not recorded")
	}
	if store.batchStateCalls != 1 || store.stateCalls != 0 {
		t.Fatalf("state lookups: batch=%d single=%d, want one batch lookup", store.batchStateCalls, store.stateCalls)
	}
}

func TestRunnerRecordsSuccessfulSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "docs/current.md", "# Current\n\nKeep this upstream document.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "repo:snapshot",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.successStats.SourceDocuments) != 1 {
		t.Fatalf("source snapshot = %#v, want one document", store.successStats.SourceDocuments)
	}
	ref := store.successStats.SourceDocuments[0]
	if ref.SourceType != string(ingest.SourceTypeMarkdown) || ref.Scope != "repo:snapshot" || !strings.HasSuffix(ref.SourceURL, "/docs/current.md") {
		t.Fatalf("source snapshot ref = %#v", ref)
	}
}

func TestRetireMissingSourceDocumentsSQLTombstonesSourceMemory(t *testing.T) {
	query := retireMissingSourceDocumentsSQL()
	for _, fragment := range []string{
		"FROM unnest($2::text[], $3::text[], $4::text[])",
		"UPDATE documents d",
		"SET status = 'deleted'",
		"d.source_config_id = $1",
		"NOT EXISTS",
		"UPDATE claims c",
		"SET status = 'deprecated'",
		"UPDATE relations r",
		"DELETE FROM memory_summaries",
		"source_sync_deleted",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("retire missing source documents SQL missing %q:\n%s", fragment, query)
		}
	}
}

func TestRunnerBatchesChangedSourceDocuments(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n\nA should be ingested.")
	writeTestFile(t, root, "b.md", "# B\n\nB should be ingested.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "repo:batch",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsChanged != 2 || stats.ChunksWritten != 4 || stats.ClaimsWritten != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.batchInputs) != 1 || len(brain.batchInputs[0]) != 2 {
		t.Fatalf("batch inputs = %+v", brain.batchInputs)
	}
	if len(brain.inputs) != 2 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
}

func TestRunnerSplitsChangedDocumentsIntoIngestBatches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 51; i++ {
		writeTestFile(t, root, fmt.Sprintf("doc-%02d.md", i), fmt.Sprintf("# Doc %02d\n\nDocument %02d should be ingested.", i, i))
	}

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "repo:batch",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 51,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 51 || stats.DocumentsChanged != 51 || stats.DocumentsDeferred != 0 || stats.ChunksWritten != 102 || stats.ClaimsWritten != 51 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.batchInputs) != 2 {
		t.Fatalf("batch count = %d, want 2: %+v", len(brain.batchInputs), brain.batchInputs)
	}
	if len(brain.batchInputs[0]) != DefaultWorkerIngestBatchSize || len(brain.batchInputs[1]) != 1 {
		t.Fatalf("batch sizes = [%d %d], want [%d 1]", len(brain.batchInputs[0]), len(brain.batchInputs[1]), DefaultWorkerIngestBatchSize)
	}
	if !store.success || store.successStats.DocumentsChanged != 51 || store.successStats.DocumentsDeferred != 0 {
		t.Fatalf("source success stats = %+v success=%v", store.successStats, store.success)
	}
}

func TestRunnerFallsBackToSingleDocumentStateLookup(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n\nA should be ingested.")
	writeTestFile(t, root, "b.md", "# B\n\nB should be ingested.")

	store := &singleStateStore{
		base: fakeStore{
			sources: []SourceConfig{{
				ID:         "docs",
				Scope:      "repo:fallback",
				SourceType: ingest.SourceTypeMarkdown,
				Name:       "Docs",
				Config:     map[string]any{"root": root},
			}},
			states: map[string]DocumentState{},
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsChanged != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if store.base.stateCalls != 2 {
		t.Fatalf("single state lookups = %d, want 2", store.base.stateCalls)
	}
}

func TestProviderFailureMetadataUsesStructuredProviderError(t *testing.T) {
	err := fmt.Errorf("ingest failed: %w", &ai.ProviderError{
		Operation:   "embedding",
		Provider:    "local",
		Model:       "qwen",
		Code:        "provider_timeout",
		Status:      504,
		Retryable:   true,
		Attempts:    3,
		BatchStart:  10,
		BatchEnd:    12,
		BatchSize:   2,
		BatchTokens: 77,
	})
	metadata := providerFailureMetadata(err)

	if metadata["error_component"] != "ai_provider" || metadata["error_class"] != "provider_timeout" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if metadata["provider_operation"] != "embedding" || metadata["provider_name"] != "local" || metadata["provider_model"] != "qwen" {
		t.Fatalf("provider metadata = %#v", metadata)
	}
	if metadata["provider_status"] != 504 || metadata["provider_retryable"] != true || metadata["provider_attempts"] != 3 {
		t.Fatalf("status metadata = %#v", metadata)
	}
	if metadata["provider_batch_start"] != 10 || metadata["provider_batch_end"] != 12 || metadata["provider_batch_size"] != 2 || metadata["provider_batch_tokens"] != 77 {
		t.Fatalf("batch metadata = %#v", metadata)
	}
}

func TestShouldRetryIngestionJobHonorsProviderRetryability(t *testing.T) {
	if !shouldRetryIngestionJob(errors.New("temporary filesystem failure")) {
		t.Fatal("generic errors should remain retryable")
	}
	if !shouldRetryIngestionJob(fmt.Errorf("wrapped: %w", &ai.ProviderError{Code: "provider_timeout", Retryable: true})) {
		t.Fatal("retryable provider errors should retry")
	}
	if shouldRetryIngestionJob(fmt.Errorf("wrapped: %w", &ai.ProviderError{Code: "auth_failed", Retryable: false})) {
		t.Fatal("non-retryable provider errors should fail without queue churn")
	}
}

func TestRunnerCountsPreReadSkippedFilesByReason(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "src/app.ts", "export const app = true\n")
	writeTestFile(t, root, "src/huge.ts", strings.Repeat("x", 128))
	writeTestFile(t, root, "src/generated/client.ts", "export const generated = true\n")
	if err := os.WriteFile(filepath.Join(root, "src", "binary.ts"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "code",
			Scope:      "repo:app",
			SourceType: ingest.SourceTypeLocalRepo,
			Name:       "Code",
			Config: map[string]any{
				"root":           root,
				"include":        []any{"README.md"},
				"include_code":   true,
				"max_file_bytes": 64,
			},
		}},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 1 || stats.DocumentsChanged != 1 ||
		stats.FilesSkippedLarge != 1 || stats.FilesSkippedBinary != 1 || stats.FilesSkippedGenerated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 || brain.inputs[0].Title != "src/app.ts" {
		t.Fatalf("inputs = %+v", brain.inputs)
	}
}

func TestRunnerIngestsMCPSourceDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["method"] != "tools/call" {
			t.Fatalf("method = %v, want tools/call", body["method"])
		}
		params, _ := body["params"].(map[string]any)
		if params["name"] != "export_documents" {
			t.Fatalf("tool = %v", params["name"])
		}
		writeJSONResponse(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_type":       "confluence",
						"source_url":        "https://wiki.example/pages/123",
						"source_id":         "123",
						"title":             "Platform Decision",
						"scope":             "team:platform",
						"content":           "Use Abra for governed agent memory.",
						"source_updated_at": "2026-06-21T10:00:00Z",
						"metadata":          map[string]any{"space": "ENG"},
					}},
				},
			},
		})
	}))
	defer server.Close()

	store := &fakeStore{
		states: map[string]DocumentState{},
		sources: []SourceConfig{{
			ID:            "mcp-confluence",
			Scope:         "team:platform",
			SourceType:    ingest.SourceTypeMCP,
			Name:          "Confluence MCP",
			BaseURL:       server.URL,
			ConnectorKind: "confluence",
			Authority:     "engineering-docs",
			Config: map[string]any{
				"tool": "export_documents",
				"arguments": map[string]any{
					"space": "ENG",
				},
				"allow_private_network": true,
			},
		}},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 1 || stats.DocumentsChanged != 1 || stats.ChunksWritten != 2 || stats.ClaimsWritten != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	input := brain.inputs[0]
	if input.SourceType != "confluence" || input.SourceURL != "https://wiki.example/pages/123" || input.Title != "Platform Decision" {
		t.Fatalf("input = %+v", input)
	}
	if input.SourceUpdatedAt != "2026-06-21T10:00:00Z" {
		t.Fatalf("source_updated_at = %q", input.SourceUpdatedAt)
	}
	if input.Metadata["source_config_id"] != "mcp-confluence" ||
		input.Metadata["connector_kind"] != "confluence" ||
		input.Metadata["authority"] != "engineering-docs" ||
		input.Metadata["space"] != "ENG" {
		t.Fatalf("metadata = %#v", input.Metadata)
	}
	if !store.success {
		t.Fatal("source success was not recorded")
	}
}

func TestRunnerBatchesChangedMCPDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeJSONResponse(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{
						{
							"source_type": "confluence",
							"source_url":  "https://wiki.example/pages/1",
							"source_id":   "1",
							"title":       "One",
							"scope":       "team:platform",
							"content":     "First source-backed document.",
						},
						{
							"source_type": "confluence",
							"source_url":  "https://wiki.example/pages/2",
							"source_id":   "2",
							"title":       "Two",
							"scope":       "team:platform",
							"content":     "Second source-backed document.",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	store := &fakeStore{
		states: map[string]DocumentState{},
		sources: []SourceConfig{{
			ID:         "mcp-confluence",
			Scope:      "team:platform",
			SourceType: ingest.SourceTypeMCP,
			Name:       "Confluence MCP",
			BaseURL:    server.URL,
			Config:     map[string]any{"tool": "export_documents", "allow_private_network": true},
		}},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsChanged != 2 || stats.ChunksWritten != 4 || stats.ClaimsWritten != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.batchInputs) != 1 || len(brain.batchInputs[0]) != 2 {
		t.Fatalf("batch inputs = %+v", brain.batchInputs)
	}
}

func TestRunnerFallsBackWhenIngestorDoesNotSupportBatch(t *testing.T) {
	brain := &singleOnlyIngestor{}
	runner := NewRunner(&fakeStore{}, brain, Options{})

	results, err := runner.ingestDocumentBatch(context.Background(), []IngestDocumentInput{
		{SourceURL: "https://example.invalid/1"},
		{SourceURL: "https://example.invalid/2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(brain.inputs) != 2 {
		t.Fatalf("results = %+v inputs = %+v", results, brain.inputs)
	}
}

func TestRunnerBatchResultCountMismatchFailsJob(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n\nA should be ingested.")
	writeTestFile(t, root, "b.md", "# B\n\nB should be ingested.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "repo:batch",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:              map[string]DocumentState{},
		finishStatusOnError: "failed",
	}
	brain := &fakeIngestor{batchResults: []IngestDocumentResult{{DocumentID: "only-one"}}}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesFailed != 1 || stats.DocumentsChanged != 0 || !store.markedError {
		t.Fatalf("stats = %+v markedError=%v", stats, store.markedError)
	}
}

func TestUnchangedRequiresCompletedIngest(t *testing.T) {
	doc := ingest.Document{
		Checksum:    "checksum",
		Fingerprint: "fingerprint",
	}
	state := DocumentState{
		Found:             true,
		ContentChecksum:   doc.Checksum,
		IngestChecksum:    doc.Checksum,
		IngestFingerprint: doc.Fingerprint,
		IngestComplete:    false,
	}
	if unchanged(doc, state) {
		t.Fatal("document with incomplete prior ingest must not be skipped")
	}
	state.IngestComplete = true
	if !unchanged(doc, state) {
		t.Fatal("completed matching document should be skipped")
	}
}

func TestRunnerDefersChangedDocumentsAtLimit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "# A\n\nA should be ingested.")
	writeTestFile(t, root, "b.md", "# B\n\nB should be deferred.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 1,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsChanged != 1 || stats.DocumentsDeferred != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	if !store.success || store.successStats.DocumentsDeferred != 1 {
		t.Fatalf("source success stats = %+v success=%v", store.successStats, store.success)
	}
}

func TestSourceFullyDrainedRequiresNoDeferredDocuments(t *testing.T) {
	if !sourceFullyDrained(SourceStats{DocumentsChanged: 10, DocumentsDeferred: 0}) {
		t.Fatal("source with no deferred documents should be fully drained")
	}
	if sourceFullyDrained(SourceStats{DocumentsChanged: 10, DocumentsDeferred: 1}) {
		t.Fatal("source with deferred documents must remain due for another worker pass")
	}
}

func TestRunnerDoesNotMarkSourceErrorForRetryableJob(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Retry\n\nThis document should retry when ingestion fails.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:              map[string]DocumentState{},
		finishStatusOnError: "retry",
	}
	brain := &fakeIngestor{err: errors.New("temporary failure")}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesFailed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if store.markedError {
		t.Fatal("source was marked error while job remained retryable")
	}
}

func TestRunnerStopsWhenJobLeaseIsLost(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Lease\n\nLong ingestion should stop when the worker loses its job lease.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:       map[string]DocumentState{},
		heartbeatErr: errors.New("lease lost"),
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
		LeaseOwner:                   "worker-a",
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesFailed != 1 || stats.DocumentsChanged != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 0 {
		t.Fatalf("ingested %d documents after lease loss", len(brain.inputs))
	}
	if store.success {
		t.Fatal("source was marked successful after lease loss")
	}
	if store.finishLeaseOwner != "worker-a" {
		t.Fatalf("finish lease owner = %q", store.finishLeaseOwner)
	}
}

func TestRunnerDoesNotCountSourceSucceededWhenJobFinishStatusIsNotSucceeded(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "README.md", "# Finish\n\nThe source should not be marked successful unless the job is succeeded.")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "docs",
			Scope:      "company",
			SourceType: ingest.SourceTypeMarkdown,
			Name:       "Docs",
			Config:     map[string]any{"root": root},
		}},
		states:       map[string]DocumentState{},
		finishStatus: "running",
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesSucceeded != 0 || stats.SourcesFailed != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if store.success {
		t.Fatal("source was marked successful despite non-succeeded job status")
	}
}

func TestRunnerIngestsGitRepoSource(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git executable is not available")
	}
	remote := t.TempDir()
	runGitTest(t, gitPath, remote, "init", "--initial-branch", "main")
	runGitTest(t, gitPath, remote, "config", "user.email", "abra@example.local")
	runGitTest(t, gitPath, remote, "config", "user.name", "Abra Test")
	writeTestFile(t, remote, "README.md", "# Remote Repo\n\nRemote repo ingestion should work.")
	writeTestFile(t, remote, "src/app.tsx", "export function App() { return <main>Abra</main>; }\n")
	runGitTest(t, gitPath, remote, "add", ".")
	runGitTest(t, gitPath, remote, "commit", "-m", "seed")

	store := &fakeStore{
		sources: []SourceConfig{{
			ID:         "remote",
			Scope:      "team:example",
			SourceType: ingest.SourceTypeGitRepo,
			Name:       "Remote",
			BaseURL:    remote,
			Config: map[string]any{
				"branch":       "main",
				"include":      []any{"README.md"},
				"include_code": true,
				"code_include": []any{"src/**/*.tsx"},
				"provider":     "bitbucket",
			},
		}},
		states: map[string]DocumentState{},
	}
	brain := &fakeIngestor{}
	cache := filepath.Join(t.TempDir(), "git-cache")
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                5 * time.Second,
		GitCacheDir:                  cache,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.DocumentsSeen != 2 || stats.DocumentsChanged != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 2 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	for _, input := range brain.inputs {
		if input.Metadata["git_remote_url"] != remote ||
			input.Metadata["git_ref"] != "main" ||
			input.Metadata["git_revision"] == "" ||
			input.Metadata["git_cache_key"] == "" {
			t.Fatalf("missing git metadata for %s: %#v", input.SourceURL, input.Metadata)
		}
		if input.SourceType != string(ingest.SourceTypeGitRepo) {
			t.Fatalf("source type = %q", input.SourceType)
		}
	}
	if !store.success {
		t.Fatal("source success was not recorded")
	}
}

func TestRunnerProcessesWebhookDocumentJob(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{{
			ID:          "webhook-job",
			TriggerType: "webhook",
			Attempts:    1,
			MaxAttempts: 3,
		}},
		webhookDocument: IngestDocumentInput{
			SourceType: "jira",
			SourceURL:  "https://jira.example.invalid/browse/ABRA-1",
			SourceID:   "ABRA-1",
			Title:      "Webhook doc",
			Scope:      "repo:demo",
			Content:    "Webhook ingestion should be handled by the worker.",
			Metadata:   map[string]any{"connector_kind": "jira"},
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Sources != 1 || stats.SourcesSucceeded != 1 || stats.SourcesFailed != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.DocumentsSeen != 1 || stats.DocumentsChanged != 1 || stats.ChunksWritten != 2 || stats.ClaimsWritten != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.inputs) != 1 {
		t.Fatalf("ingested %d documents", len(brain.inputs))
	}
	input := brain.inputs[0]
	if input.SourceURL != "https://jira.example.invalid/browse/ABRA-1" {
		t.Fatalf("source url = %q", input.SourceURL)
	}
	if input.Metadata["connector_kind"] != "jira" || input.Metadata["ingestion_job_id"] != "webhook-job" {
		t.Fatalf("missing webhook metadata: %#v", input.Metadata)
	}
	if store.success {
		t.Fatal("webhook job should not mark a source config successful")
	}
}

func TestRunnerBatchesWebhookDocumentJobs(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{
			{ID: "webhook-job-1", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-2", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-3", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
		},
		webhookDocuments: map[string]IngestDocumentInput{
			"webhook-job-1": webhookInput("webhook-job-1"),
			"webhook-job-2": webhookInput("webhook-job-2"),
			"webhook-job-3": webhookInput("webhook-job-3"),
		},
	}
	brain := &fakeIngestor{}
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		SourceTimeout:                time.Second,
	})

	stats, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SourcesSucceeded != 3 || stats.DocumentsSeen != 3 || stats.DocumentsChanged != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if len(brain.batchInputs) != 1 || len(brain.batchInputs[0]) != 3 {
		t.Fatalf("batch inputs = %+v", brain.batchInputs)
	}
	for _, input := range brain.batchInputs[0] {
		if input.Metadata["ingestion_job_id"] != input.SourceID {
			t.Fatalf("missing webhook job metadata for %+v", input)
		}
	}
}

func TestRunnerHonorsWorkerConcurrency(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{
			{ID: "webhook-job-1", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-2", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "webhook-job-3", TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
		},
		webhookDocuments: map[string]IngestDocumentInput{
			"webhook-job-1": webhookInput("webhook-job-1"),
			"webhook-job-2": webhookInput("webhook-job-2"),
			"webhook-job-3": webhookInput("webhook-job-3"),
		},
	}
	brain := newBlockingIngestor()
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		Concurrency:                  2,
		SourceTimeout:                time.Second,
	})

	done := make(chan struct{})
	var stats RunStats
	var err error
	go func() {
		stats, err = runner.RunOnce(context.Background())
		close(done)
	}()

	waitStarted(t, brain.started)
	waitStarted(t, brain.started)
	select {
	case <-brain.started:
		t.Fatal("third job started while concurrency was limited to 2")
	case <-time.After(25 * time.Millisecond):
	}
	close(brain.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if maxActive := atomic.LoadInt32(&brain.maxActive); maxActive != 2 {
		t.Fatalf("max active ingests = %d, want 2", maxActive)
	}
	if stats.SourcesSucceeded != 3 || stats.DocumentsChanged != 3 || stats.ChunksWritten != 6 || stats.ClaimsWritten != 3 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRunnerSerializesSameSourceJobs(t *testing.T) {
	store := &fakeStore{
		queuedJobs: []QueuedIngestionJob{
			{ID: "job-1", Source: SourceConfig{ID: "docs"}, TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
			{ID: "job-2", Source: SourceConfig{ID: "docs"}, TriggerType: "webhook", Attempts: 1, MaxAttempts: 3},
		},
		webhookDocuments: map[string]IngestDocumentInput{
			"job-1": webhookInput("job-1"),
			"job-2": webhookInput("job-2"),
		},
	}
	brain := newBlockingIngestor()
	runner := NewRunner(store, brain, Options{
		MaxSourcesPerRun:             10,
		MaxChangedDocumentsPerSource: 10,
		Concurrency:                  2,
		SourceTimeout:                time.Second,
	})

	done := make(chan struct{})
	var err error
	go func() {
		_, err = runner.RunOnce(context.Background())
		close(done)
	}()

	waitStarted(t, brain.started)
	select {
	case <-brain.started:
		t.Fatal("same-source job started concurrently")
	case <-time.After(25 * time.Millisecond):
	}
	close(brain.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish")
	}
	if err != nil {
		t.Fatal(err)
	}
	if maxActive := atomic.LoadInt32(&brain.maxActive); maxActive != 1 {
		t.Fatalf("max active ingests = %d, want 1", maxActive)
	}
}

func webhookInput(id string) IngestDocumentInput {
	return IngestDocumentInput{
		SourceType: "webhook",
		SourceURL:  "https://example.invalid/" + id,
		SourceID:   id,
		Title:      id,
		Scope:      "repo:test",
		Content:    "synthetic webhook payload",
		Metadata:   map[string]any{},
	}
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ingestion to start")
	}
}

func writeJSONResponse(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

type fakeStore struct {
	mu                  sync.Mutex
	sources             []SourceConfig
	states              map[string]DocumentState
	queuedJobs          []QueuedIngestionJob
	webhookDocument     IngestDocumentInput
	webhookDocuments    map[string]IngestDocumentInput
	success             bool
	markedError         bool
	jobs                []string
	err                 error
	finishStatus        string
	finishStatusOnError string
	heartbeatErr        error
	heartbeats          int
	finishLeaseOwner    string
	successStats        SourceStats
	stateCalls          int
	batchStateCalls     int
}

func (f *fakeStore) RecoverStaleIngestionJobs(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func (f *fakeStore) EnqueueScheduledSources(context.Context, int) (int, error) {
	return len(f.sources), nil
}

func (f *fakeStore) ClaimQueuedIngestionJobs(context.Context, int, string) ([]QueuedIngestionJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queuedJobs) > 0 {
		return f.queuedJobs, nil
	}
	jobs := make([]QueuedIngestionJob, 0, len(f.sources))
	for _, source := range f.sources {
		f.jobs = append(f.jobs, "job")
		jobs = append(jobs, QueuedIngestionJob{ID: "job", Source: source, TriggerType: "schedule", Attempts: 1, MaxAttempts: 3})
	}
	return jobs, nil
}

func (f *fakeStore) HeartbeatIngestionJob(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeats++
	return f.heartbeatErr
}

func (f *fakeStore) FinishIngestionJob(_ context.Context, _ string, leaseOwner string, _ SourceStats, runErr error) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishLeaseOwner = leaseOwner
	if runErr != nil {
		if f.finishStatusOnError != "" {
			return f.finishStatusOnError, nil
		}
		return "failed", nil
	}
	if f.finishStatus != "" {
		return f.finishStatus, nil
	}
	return "succeeded", nil
}

func (f *fakeStore) DocumentState(_ context.Context, doc ingest.Document) (DocumentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateCalls++
	if f.err != nil {
		return DocumentState{}, f.err
	}
	return f.states[doc.Path], nil
}

func (f *fakeStore) DocumentStates(_ context.Context, docs []ingest.Document) (map[string]DocumentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batchStateCalls++
	if f.err != nil {
		return nil, f.err
	}
	states := make(map[string]DocumentState, len(docs))
	for _, doc := range docs {
		states[documentStateKey(doc)] = f.states[doc.Path]
	}
	return states, nil
}

type singleStateStore struct {
	base fakeStore
}

func (s *singleStateStore) RecoverStaleIngestionJobs(ctx context.Context, leaseTimeout time.Duration) (int64, error) {
	return s.base.RecoverStaleIngestionJobs(ctx, leaseTimeout)
}

func (s *singleStateStore) EnqueueScheduledSources(ctx context.Context, limit int) (int, error) {
	return s.base.EnqueueScheduledSources(ctx, limit)
}

func (s *singleStateStore) ClaimQueuedIngestionJobs(ctx context.Context, limit int, leaseOwner string) ([]QueuedIngestionJob, error) {
	return s.base.ClaimQueuedIngestionJobs(ctx, limit, leaseOwner)
}

func (s *singleStateStore) HeartbeatIngestionJob(ctx context.Context, jobID string, leaseOwner string) error {
	return s.base.HeartbeatIngestionJob(ctx, jobID, leaseOwner)
}

func (s *singleStateStore) FinishIngestionJob(ctx context.Context, jobID string, leaseOwner string, stats SourceStats, runErr error) (string, error) {
	return s.base.FinishIngestionJob(ctx, jobID, leaseOwner, stats, runErr)
}

func (s *singleStateStore) GetWebhookDocument(ctx context.Context, jobID string) (IngestDocumentInput, error) {
	return s.base.GetWebhookDocument(ctx, jobID)
}

func (s *singleStateStore) DocumentState(ctx context.Context, doc ingest.Document) (DocumentState, error) {
	return s.base.DocumentState(ctx, doc)
}

func (s *singleStateStore) MarkSourceSuccess(ctx context.Context, sourceID string, stats SourceStats) error {
	return s.base.MarkSourceSuccess(ctx, sourceID, stats)
}

func (s *singleStateStore) MarkSourceError(ctx context.Context, sourceID string, err error) error {
	return s.base.MarkSourceError(ctx, sourceID, err)
}

func (f *fakeStore) GetWebhookDocument(_ context.Context, jobID string) (IngestDocumentInput, error) {
	if f.err != nil {
		return IngestDocumentInput{}, f.err
	}
	if f.webhookDocuments != nil {
		if document, ok := f.webhookDocuments[jobID]; ok {
			return document, nil
		}
	}
	return f.webhookDocument, nil
}

func (f *fakeStore) MarkSourceSuccess(_ context.Context, _ string, stats SourceStats) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.success = true
	f.successStats = stats
	return nil
}

func (f *fakeStore) MarkSourceError(context.Context, string, error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markedError = true
	return nil
}

type fakeIngestor struct {
	mu           sync.Mutex
	inputs       []IngestDocumentInput
	batchInputs  [][]IngestDocumentInput
	err          error
	batchErr     error
	batchResults []IngestDocumentResult
}

func (f *fakeIngestor) IngestDocument(_ context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	if f.err != nil {
		return IngestDocumentResult{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, input)
	return IngestDocumentResult{DocumentID: "doc", Chunks: 2, Claims: 1}, nil
}

func (f *fakeIngestor) IngestDocuments(_ context.Context, inputs []IngestDocumentInput) ([]IngestDocumentResult, error) {
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := append([]IngestDocumentInput(nil), inputs...)
	f.batchInputs = append(f.batchInputs, copied)
	f.inputs = append(f.inputs, copied...)
	if f.batchResults != nil {
		return f.batchResults, nil
	}
	results := make([]IngestDocumentResult, 0, len(inputs))
	for range inputs {
		results = append(results, IngestDocumentResult{DocumentID: "doc", Chunks: 2, Claims: 1})
	}
	return results, nil
}

type singleOnlyIngestor struct {
	inputs []IngestDocumentInput
	err    error
}

func (s *singleOnlyIngestor) IngestDocument(_ context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	if s.err != nil {
		return IngestDocumentResult{}, s.err
	}
	s.inputs = append(s.inputs, input)
	return IngestDocumentResult{DocumentID: "doc", Chunks: 2, Claims: 1}, nil
}

type blockingIngestor struct {
	started   chan struct{}
	release   chan struct{}
	active    int32
	maxActive int32
}

func newBlockingIngestor() *blockingIngestor {
	return &blockingIngestor{
		started: make(chan struct{}, 10),
		release: make(chan struct{}),
	}
}

func (b *blockingIngestor) IngestDocument(ctx context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	active := atomic.AddInt32(&b.active, 1)
	for {
		maxActive := atomic.LoadInt32(&b.maxActive)
		if active <= maxActive || atomic.CompareAndSwapInt32(&b.maxActive, maxActive, active) {
			break
		}
	}
	b.started <- struct{}{}
	select {
	case <-ctx.Done():
		atomic.AddInt32(&b.active, -1)
		return IngestDocumentResult{}, ctx.Err()
	case <-b.release:
	}
	atomic.AddInt32(&b.active, -1)
	return IngestDocumentResult{DocumentID: input.SourceID, Chunks: 2, Claims: 1}, nil
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGitTest(t *testing.T, gitPath, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitPath, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
