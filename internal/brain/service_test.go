package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
)

func TestNewEmbeddingProviderRejectsCustomHTTPProvider(t *testing.T) {
	_, err := newEmbeddingProvider(config.Config{
		Embedding: config.AIProviderConfig{
			Provider:   "custom-http",
			BaseURL:    "https://provider.example/embed",
			Model:      "custom-model",
			Dimensions: 1024,
			Timeout:    time.Second,
		},
	})
	if err == nil || !strings.Contains(err.Error(), `unsupported embedding provider "custom-http"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestCodeIntelligenceSummariesAddRepoAndEntityLayers(t *testing.T) {
	input := summaryInput{
		DocumentID: "doc-1",
		Input: IngestDocumentInput{
			SourceType: "local_repo",
			SourceURL:  "file:///repos/sample-repo/src/pages/accounts/index.tsx",
			SourceID:   "sample-repo",
			Title:      "src/pages/accounts/index.tsx",
			Scope:      "team:example",
			Content: `
import React from "react";
import AccountTable from "../../components/AccountTable";

export default function AccountsPage() {
  return <AccountTable />;
}
`,
			Metadata: map[string]any{
				"content_kind": "code",
				"git_path":     "src/pages/accounts/index.tsx",
				"repo":         "sample-repo",
			},
		},
		Content: `
import React from "react";
import AccountTable from "../../components/AccountTable";

export default function AccountsPage() {
  return <AccountTable />;
}
`,
		CodePath: "src/pages/accounts/index.tsx",
	}
	input.CodeCandidates = codeCandidatesForSummary(input, input.CodePath)

	fileSummary := documentSummary(input, input.CodePath)
	summaries := append([]storeLevelKeySummary{
		{
			level:   fileSummary.Level,
			key:     fileSummary.Key,
			summary: fileSummary.Summary,
		},
	}, levelKeySummaries(codeIntelligenceSummaries(input, input.CodePath))...)

	assertSummary(t, summaries, "file", "src/pages/accounts/index.tsx", "Implements route /accounts.")
	assertSummary(t, summaries, "repo", "sample-repo", "Repository sample-repo code intelligence includes src/pages/accounts/index.tsx.")
	assertSummary(t, summaries, "route", "/accounts", "Route /accounts is connected to src/pages/accounts/index.tsx.")
	assertSummary(t, summaries, "component", "AccountsPage", "Component AccountsPage is connected to src/pages/accounts/index.tsx.")
	assertSummary(t, summaries, "symbol", "AccountsPage", "Symbol AccountsPage is connected to src/pages/accounts/index.tsx.")
	assertSummary(t, summaries, "package", "react", "Package react is connected to src/pages/accounts/index.tsx.")
}

func TestExtractClaimsForDocumentSkipsCodeDocuments(t *testing.T) {
	claims := extractClaimsForDocument(IngestDocumentInput{
		Metadata: map[string]any{
			"content_kind": "code",
			"git_path":     "internal/store/store.go",
		},
	}, `
func (s *Store) AddEvidence() error {
  return nil
}

- Frontend apps must use Playwright for browser tests.
`)
	if len(claims) != 0 {
		t.Fatalf("code document claims = %#v, want none", claims)
	}
}

func TestExtractClaimsIgnoresFencedCodeAndReturnsDeterministicClaims(t *testing.T) {
	claims := extractClaimsForDocument(IngestDocumentInput{}, ""+
		"# Knowledge\n\n"+
		"- Zebra services must use Postgres for durable memory.\n"+
		"- Alpha agents should use source-backed claims for decisions.\n\n"+
		"```go\n"+
		"- Code comments must not become trusted memory.\n"+
		"func Example() {\n"+
		"  return\n"+
		"}\n"+
		"```\n",
	)

	want := []string{
		"Alpha agents should use source-backed claims for decisions.",
		"Zebra services must use Postgres for durable memory.",
	}
	if len(claims) != len(want) {
		t.Fatalf("claims = %#v, want %#v", claims, want)
	}
	for i := range want {
		if claims[i] != want[i] {
			t.Fatalf("claims = %#v, want deterministic %#v", claims, want)
		}
	}
}

func TestProviderMetricStatusUsesStructuredProviderError(t *testing.T) {
	err := fmt.Errorf("embedding batch failed: %w", &ai.ProviderError{Code: "rate_limited"})
	if got := providerMetricStatus(err); got != "rate_limited" {
		t.Fatalf("providerMetricStatus() = %q, want rate_limited", got)
	}
}

func TestChunkTextHardSplitsOversizedParagraph(t *testing.T) {
	chunks := chunkText(strings.Repeat("a", 10000), 1200)
	if len(chunks) < 8 {
		t.Fatalf("chunks = %d, want hard split", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 1200 {
			t.Fatalf("chunk len = %d, want <= 1200", len(chunk))
		}
	}
}

func TestChunkTextSplitsMinifiedJSON(t *testing.T) {
	content := `{"items":[` + strings.Repeat(`{"name":"alpha","value":"`+strings.Repeat("x", 80)+`"},`, 120) + `{}]}`
	chunks := chunkText(content, 1200)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want split minified json", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 1200 {
			t.Fatalf("chunk len = %d, want <= 1200", len(chunk))
		}
	}
}

func TestChunkTextHardSplitsNonASCIIOnUTF8Boundaries(t *testing.T) {
	chunks := chunkText(strings.Repeat("東京", 900), 1200)
	if len(chunks) < 2 {
		t.Fatalf("chunks = %d, want hard split", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > 1200 {
			t.Fatalf("chunk len = %d, want <= 1200", len(chunk))
		}
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk is not valid utf8: %q", chunk)
		}
	}
}

func TestTokenEstimateCountsDenseText(t *testing.T) {
	if got := tokenEstimate(strings.Repeat("x", 1200)); got < 250 {
		t.Fatalf("token estimate = %d, want dense text estimate", got)
	}
}

func TestEmbedTextsBatchesLargeRequests(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg:        config.Config{Embedding: config.AIProviderConfig{Dimensions: 3}},
		embeddings: provider,
	}
	inputs := make([]string, 20)
	for i := range inputs {
		inputs[i] = strings.Repeat("word ", 900)
	}
	response, err := service.embedTexts(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embedTexts error = %v", err)
	}
	if len(response.Embeddings) != len(inputs) {
		t.Fatalf("embeddings = %d, want %d", len(response.Embeddings), len(inputs))
	}
	if len(provider.callSizes) < 2 {
		t.Fatalf("expected batched calls, got sizes %#v", provider.callSizes)
	}
	for i, embedding := range response.Embeddings {
		if embedding.Index != i {
			t.Fatalf("embedding index %d = %d", i, embedding.Index)
		}
	}
}

func TestEmbedTextsHonorsConfiguredBatchLimits(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg: config.Config{
			Embedding:               config.AIProviderConfig{Dimensions: 3},
			EmbeddingBatchMaxItems:  3,
			EmbeddingBatchMaxTokens: 100000,
		},
		embeddings: provider,
	}
	inputs := make([]string, 7)
	for i := range inputs {
		inputs[i] = "small input"
	}
	response, err := service.embedTexts(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embedTexts error = %v", err)
	}
	if len(response.Embeddings) != len(inputs) {
		t.Fatalf("embeddings = %d, want %d", len(response.Embeddings), len(inputs))
	}
	if got, want := fmt.Sprint(provider.callSizes), fmt.Sprint([]int{3, 3, 1}); got != want {
		t.Fatalf("provider call sizes = %s, want %s", got, want)
	}
}

func TestEmbedTextsHonorsConfiguredBatchTokenLimit(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg: config.Config{
			Embedding:               config.AIProviderConfig{Dimensions: 3},
			EmbeddingBatchMaxItems:  100,
			EmbeddingBatchMaxTokens: 100,
		},
		embeddings: provider,
	}
	inputs := []string{
		strings.Repeat("word ", 90),
		strings.Repeat("word ", 90),
		strings.Repeat("word ", 90),
	}
	response, err := service.embedTexts(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embedTexts error = %v", err)
	}
	if len(response.Embeddings) != len(inputs) {
		t.Fatalf("embeddings = %d, want %d", len(response.Embeddings), len(inputs))
	}
	if got, want := fmt.Sprint(provider.callSizes), fmt.Sprint([]int{1, 1, 1}); got != want {
		t.Fatalf("provider call sizes = %s, want %s", got, want)
	}
}

func TestEmbedTextsSplitsContextOverflowBatch(t *testing.T) {
	provider := &contextOverflowEmbeddingProvider{failAtOrAbove: 4}
	service := Service{
		cfg: config.Config{
			Embedding:               config.AIProviderConfig{Provider: "local", Model: "qwen", Dimensions: 3},
			EmbeddingBatchMaxItems:  4,
			EmbeddingBatchMaxTokens: 100000,
		},
		embeddings: provider,
	}
	inputs := []string{"a", "b", "c", "d"}
	response, err := service.embedTexts(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embedTexts error = %v", err)
	}
	if got, want := fmt.Sprint(provider.callSizes), fmt.Sprint([]int{4, 2, 2}); got != want {
		t.Fatalf("provider call sizes = %s, want %s", got, want)
	}
	if len(response.Embeddings) != len(inputs) {
		t.Fatalf("embeddings = %d, want %d", len(response.Embeddings), len(inputs))
	}
	for i, embedding := range response.Embeddings {
		if embedding.Index != i || embedding.Vector[0] != float64(i) {
			t.Fatalf("embedding[%d] = index %d vector %#v", i, embedding.Index, embedding.Vector)
		}
	}
	if response.Usage == nil || response.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %#v, want successful split usage", response.Usage)
	}
}

func TestEmbedTextsSplitsProviderTimeoutBatch(t *testing.T) {
	provider := &providerTimeoutEmbeddingProvider{failOnceAtOrAbove: 4}
	service := Service{
		cfg: config.Config{
			Embedding:               config.AIProviderConfig{Provider: "local", Model: "qwen", Dimensions: 3},
			EmbeddingBatchMaxItems:  4,
			EmbeddingBatchMaxTokens: 100000,
		},
		embeddings: provider,
	}
	inputs := []string{"a", "b", "c", "d"}
	response, err := service.embedTexts(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embedTexts error = %v", err)
	}
	if got, want := fmt.Sprint(provider.callSizes), fmt.Sprint([]int{4, 2, 2}); got != want {
		t.Fatalf("provider call sizes = %s, want %s", got, want)
	}
	if len(response.Embeddings) != len(inputs) {
		t.Fatalf("embeddings = %d, want %d", len(response.Embeddings), len(inputs))
	}
	for i, embedding := range response.Embeddings {
		if embedding.Index != i || embedding.Vector[0] != float64(i) {
			t.Fatalf("embedding[%d] = index %d vector %#v", i, embedding.Index, embedding.Vector)
		}
	}
	if response.Usage == nil || response.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %#v, want successful split usage", response.Usage)
	}
}

func TestEmbedPreparedDocumentsBatchesChunksAcrossDocuments(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg:        config.Config{Embedding: config.AIProviderConfig{Dimensions: 3}},
		embeddings: provider,
	}
	inputs := []IngestDocumentInput{
		{
			SourceType: "local_repo",
			SourceURL:  "file:///repo/a.go",
			Title:      "a.go",
			Scope:      "repo:test",
			Content:    "package main\n\nfunc A() {}",
			Metadata:   map[string]any{"content_kind": "code", "git_path": "a.go"},
		},
		{
			SourceType: "local_repo",
			SourceURL:  "file:///repo/b.go",
			Title:      "b.go",
			Scope:      "repo:test",
			Content:    "package main\n\nfunc B() {}",
			Metadata:   map[string]any{"content_kind": "code", "git_path": "b.go"},
		},
	}
	prepared := make([]preparedIngestDocument, 0, len(inputs))
	for _, input := range inputs {
		doc, err := service.prepareIngestDocument(input)
		if err != nil {
			t.Fatalf("prepareIngestDocument error = %v", err)
		}
		prepared = append(prepared, doc)
	}

	embedded, err := service.embedPreparedDocuments(context.Background(), prepared)
	if err != nil {
		t.Fatalf("embedPreparedDocuments error = %v", err)
	}
	if got, want := provider.callSizes, []int{2}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("provider call sizes = %#v, want %#v", got, want)
	}
	if embedded[0].chunkEmbeddings[0].Vector[0] != 0 || embedded[1].chunkEmbeddings[0].Vector[0] != 1 {
		t.Fatalf("chunk embeddings were not mapped by document order: %#v %#v", embedded[0].chunkEmbeddings, embedded[1].chunkEmbeddings)
	}
	if len(embedded[0].claims) != 0 || len(embedded[1].claims) != 0 {
		t.Fatalf("code documents should not extract claims: %#v %#v", embedded[0].claims, embedded[1].claims)
	}
}

func TestEmbedPreparedDocumentsBatchesClaimsAcrossDocuments(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg:        config.Config{Embedding: config.AIProviderConfig{Dimensions: 3}},
		embeddings: provider,
	}
	inputs := []IngestDocumentInput{
		{
			SourceType: "markdown",
			SourceURL:  "file:///repo/a.md",
			Title:      "a.md",
			Scope:      "repo:test",
			Content:    "- Agents should use Abra memory before changing production code.",
		},
		{
			SourceType: "markdown",
			SourceURL:  "file:///repo/b.md",
			Title:      "b.md",
			Scope:      "repo:test",
			Content:    "- Release checks must pass before publishing an OSS build.",
		},
	}
	prepared := make([]preparedIngestDocument, 0, len(inputs))
	for _, input := range inputs {
		doc, err := service.prepareIngestDocument(input)
		if err != nil {
			t.Fatalf("prepareIngestDocument error = %v", err)
		}
		prepared = append(prepared, doc)
	}

	embedded, err := service.embedPreparedDocuments(context.Background(), prepared)
	if err != nil {
		t.Fatalf("embedPreparedDocuments error = %v", err)
	}
	if got, want := provider.callSizes, []int{2, 2}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("provider call sizes = %#v, want chunk batch then claim batch %#v", got, want)
	}
	if len(embedded[0].claimEmbeddings) != 1 || len(embedded[1].claimEmbeddings) != 1 {
		t.Fatalf("claim embeddings missing: %#v %#v", embedded[0].claimEmbeddings, embedded[1].claimEmbeddings)
	}
	if embedded[0].claimEmbeddings[0].Vector[0] != 2 || embedded[1].claimEmbeddings[0].Vector[0] != 3 {
		t.Fatalf("claim embeddings were not mapped by document order: %#v %#v", embedded[0].claimEmbeddings, embedded[1].claimEmbeddings)
	}
}

func TestPrepareIngestDocumentDefaultsDirectIngestToUnverified(t *testing.T) {
	service := Service{}
	doc, err := service.prepareIngestDocument(IngestDocumentInput{
		SourceType: "markdown",
		SourceURL:  "file:///repo/direct.md",
		Title:      "direct.md",
		Scope:      "repo:test",
		Content:    "- Direct ingest without explicit authority must remain unverified.",
	})
	if err != nil {
		t.Fatalf("prepareIngestDocument error = %v", err)
	}
	if doc.authority != "manual-unverified" || doc.authorityScore != 0.35 || doc.claimStatus != "unverified" {
		t.Fatalf("direct ingest authority = %q score %.2f status %q, want manual-unverified/0.35/unverified", doc.authority, doc.authorityScore, doc.claimStatus)
	}
}

func TestPrepareIngestDocumentHonorsExplicitDirectIngestAuthority(t *testing.T) {
	service := Service{}
	doc, err := service.prepareIngestDocument(IngestDocumentInput{
		SourceType:     "markdown",
		SourceURL:      "file:///repo/direct.md",
		Title:          "direct.md",
		Scope:          "repo:test",
		Content:        "- Explicit trusted direct ingest may create verified claims.",
		Authority:      "official-doc",
		AuthorityScore: 0.99,
	})
	if err != nil {
		t.Fatalf("prepareIngestDocument error = %v", err)
	}
	if doc.authority != "official-doc" || doc.authorityScore != 0.99 || doc.claimStatus != "verified" {
		t.Fatalf("explicit direct ingest authority = %q score %.2f status %q, want official-doc/0.99/verified", doc.authority, doc.authorityScore, doc.claimStatus)
	}
}

func TestPrepareIngestDocumentTrustsSourceConfigAuthority(t *testing.T) {
	service := Service{}
	doc, err := service.prepareIngestDocument(IngestDocumentInput{
		SourceType: "markdown",
		SourceURL:  "file:///repo/source.md",
		Title:      "source.md",
		Scope:      "repo:test",
		Content:    "- Source config ingest may create verified source-backed claims.",
		Metadata: map[string]any{
			"source_config_id": "source-1",
			"authority":        "team-convention",
			"authority_score":  0.7,
		},
	})
	if err != nil {
		t.Fatalf("prepareIngestDocument error = %v", err)
	}
	if doc.authority != "team-convention" || doc.authorityScore != 0.7 || doc.claimStatus != "verified" {
		t.Fatalf("source config authority = %q score %.2f status %q, want team-convention/0.7/verified", doc.authority, doc.authorityScore, doc.claimStatus)
	}
}

func TestIngestDocumentsValidatesBeforeEmbedding(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg:        config.Config{Embedding: config.AIProviderConfig{Dimensions: 3}},
		embeddings: provider,
	}
	_, err := service.IngestDocuments(context.Background(), []IngestDocumentInput{
		{
			SourceType: "markdown",
			SourceURL:  "file:///repo/a.md",
			Title:      "a.md",
			Scope:      "repo:test",
			Content:    "Agents should use Abra memory before changing production code.",
		},
		{
			SourceType: "markdown",
			SourceURL:  "file:///repo/b.md",
			Title:      "b.md",
			Scope:      "repo:test",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "document 1:") || !strings.Contains(err.Error(), "content are required") {
		t.Fatalf("IngestDocuments error = %v, want indexed validation error", err)
	}
	if len(provider.callSizes) != 0 {
		t.Fatalf("embedding provider was called before validation completed: %#v", provider.callSizes)
	}
}

func TestPreparedIngestSourceLocksDeduplicateAndSort(t *testing.T) {
	docs := []preparedIngestDocument{
		{input: IngestDocumentInput{Scope: "repo:b", SourceType: "markdown", SourceURL: "file://b.md"}},
		{input: IngestDocumentInput{Scope: "repo:a", SourceType: "markdown", SourceURL: "file://a.md"}},
		{input: IngestDocumentInput{Scope: "repo:b", SourceType: "markdown", SourceURL: "file://b.md"}},
		{input: IngestDocumentInput{Scope: " repo:a ", SourceType: " local_repo ", SourceURL: " file://a.md "}},
		{input: IngestDocumentInput{Scope: " repo:a ", SourceType: " local_repo ", SourceURL: " file://a.go "}},
	}

	locks := preparedIngestSourceLocks(docs)
	if got, want := len(locks), 3; got != want {
		t.Fatalf("locks = %d, want %d: %#v", got, want, locks)
	}
	got := []string{}
	for _, lock := range locks {
		got = append(got, lock.scope+"|"+lock.sourceURL)
	}
	want := []string{
		"repo:a|file://a.go",
		"repo:a|file://a.md",
		"repo:b|file://b.md",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("locks = %#v, want %#v", got, want)
	}
}

func TestProviderLimiterSerializesEmbeddingCalls(t *testing.T) {
	beforeMetrics := observability.AIProviderMetricsSnapshot()
	provider := &concurrentEmbeddingProvider{delay: 20 * time.Millisecond}
	service := Service{
		cfg:           config.Config{Embedding: config.AIProviderConfig{Provider: "local", Dimensions: 3}},
		embeddings:    provider,
		providerSlots: make(chan struct{}, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.embed(ctx, ai.EmbeddingRequest{Input: []string{"hello"}, Dimensions: 3})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("embed error = %v", err)
		}
	}
	if provider.maxConcurrent != 1 {
		t.Fatalf("max concurrent provider calls = %d, want 1", provider.maxConcurrent)
	}
	metrics := observability.AIProviderMetricsSnapshot()
	beforeCalls := aiProviderMetricValue(beforeMetrics, "embedding", "local", "ok", "calls")
	if got := aiProviderMetricValue(metrics, "embedding", "local", "ok", "calls") - beforeCalls; got != 4 {
		t.Fatalf("provider call metric = %d, want 4 in %#v", got, metrics)
	}
	beforeWaits := aiProviderMetricValue(beforeMetrics, "embedding", "local", "ok", "waits")
	if got := aiProviderMetricValue(metrics, "embedding", "local", "ok", "waits") - beforeWaits; got != 4 {
		t.Fatalf("provider wait metric = %d, want 4 in %#v", got, metrics)
	}
	if got := aiProviderMetricValue(metrics, "embedding", "local", "", "max_in_flight"); got < 1 {
		t.Fatalf("max in-flight metric = %d, want at least 1 in %#v", got, metrics)
	}
}

func TestRecallQueryEmbeddingCacheReusesAndCopiesVectors(t *testing.T) {
	provider := &recordingEmbeddingProvider{}
	service := Service{
		cfg:                 config.Config{Embedding: config.AIProviderConfig{Provider: "local", BaseURL: "http://example.test/v1", Model: "qwen3", Dimensions: 3}},
		embeddings:          provider,
		providerSlots:       make(chan struct{}, 1),
		queryEmbeddingCache: newEmbeddingCache(8),
	}

	first, ok, err := service.recallQueryEmbedding(context.Background(), "Source Scoped Recall")
	if err != nil {
		t.Fatalf("first embedding error = %v", err)
	}
	if !ok {
		t.Fatal("first embedding was not returned")
	}
	first[0] = 99

	second, ok, err := service.recallQueryEmbedding(context.Background(), "Source Scoped Recall")
	if err != nil {
		t.Fatalf("second embedding error = %v", err)
	}
	if !ok {
		t.Fatal("second embedding was not returned")
	}
	if len(provider.callSizes) != 1 {
		t.Fatalf("embedding provider calls = %d, want 1", len(provider.callSizes))
	}
	if second[0] == 99 {
		t.Fatalf("cached vector was mutated through caller-owned slice: %#v", second)
	}

	if _, ok, err := service.recallQueryEmbedding(context.Background(), "Working Memory Compose"); err != nil || !ok {
		t.Fatalf("different query embedding ok=%v err=%v", ok, err)
	}
	if len(provider.callSizes) != 2 {
		t.Fatalf("embedding provider calls after different query = %d, want 2", len(provider.callSizes))
	}
}

func TestRecallCandidateLimitExpandsOnlyWithReranker(t *testing.T) {
	if got := recallCandidateLimit(5, false); got != 5 {
		t.Fatalf("candidate limit without reranker = %d, want 5", got)
	}
	if got := recallCandidateLimit(5, true); got != 15 {
		t.Fatalf("candidate limit with reranker = %d, want 15", got)
	}
	if got := recallCandidateLimit(20, true); got != 20 {
		t.Fatalf("candidate limit should cap at store max = %d, want 20", got)
	}
	if got := recallCandidateLimit(0, true); got != 15 {
		t.Fatalf("invalid final limit should use default candidate pool = %d, want 15", got)
	}
}

func TestFinalizeRecallResultReranksBeforeFinalTrim(t *testing.T) {
	service := Service{
		cfg: config.Config{Reranker: config.AIProviderConfig{Provider: "local", Model: "rerank-model"}},
		reranker: &fakeRerankerProvider{
			responses: []ai.RerankResponse{
				{Results: []ai.RerankResult{{Index: 2, Score: 1}, {Index: 0, Score: 0}, {Index: 1, Score: 0}}},
				{Results: []ai.RerankResult{{Index: 2, Score: 1}, {Index: 0, Score: 0}, {Index: 1, Score: 0}}},
			},
		},
	}

	result := service.finalizeRecallResult(context.Background(), "base query", store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "claim-base-1", Claim: "base one", Rank: 0.70, TextScore: 0.3},
			{ID: "claim-base-2", Claim: "base two", Rank: 0.69, TextScore: 0.2},
			{ID: "claim-rerank", Claim: "rerank should rescue this", Rank: 0.68, TextScore: 0.1},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-base-1", Title: "base one", Content: "doc one", Rank: 0.70, TextScore: 0.3},
			{ID: "doc-base-2", Title: "base two", Content: "doc two", Rank: 0.69, TextScore: 0.2},
			{ID: "doc-rerank", Title: "rerank", Content: "doc three", Rank: 0.68, TextScore: 0.1},
		},
		GraphContext: []store.RelationResult{
			{ID: "relation-1", FromEntity: "A", ToEntity: "B", Type: "depends_on"},
			{ID: "relation-2", FromEntity: "B", ToEntity: "C", Type: "depends_on"},
			{ID: "relation-3", FromEntity: "C", ToEntity: "D", Type: "depends_on"},
		},
	}, 2)

	if len(result.Claims) != 2 || result.Claims[0].ID != "claim-rerank" || result.Claims[1].ID != "claim-base-1" {
		t.Fatalf("claims were not reranked before trim: %#v", result.Claims)
	}
	if len(result.SupportingDocuments) != 2 || result.SupportingDocuments[0].ID != "doc-rerank" || result.SupportingDocuments[1].ID != "doc-base-1" {
		t.Fatalf("documents were not reranked before trim: %#v", result.SupportingDocuments)
	}
	if len(result.GraphContext) != 2 {
		t.Fatalf("graph context length = %d, want final limit 2", len(result.GraphContext))
	}
	if result.RetrievalMode != "hybrid_reranked" {
		t.Fatalf("retrieval mode = %q, want hybrid_reranked", result.RetrievalMode)
	}
	if !hasBrainRetrievalReason(result.RetrievalReasons, "rerank", "hybrid_reranked", 4) {
		t.Fatalf("rerank reason should describe final result set: %#v", result.RetrievalReasons)
	}
}

func TestFinalizeRecallResultFallsBackToBaseRankingWhenRerankerFails(t *testing.T) {
	service := Service{
		cfg:      config.Config{Reranker: config.AIProviderConfig{Provider: "local", Model: "rerank-model"}},
		reranker: &fakeRerankerProvider{err: errors.New("rate limited")},
	}

	result := service.finalizeRecallResult(context.Background(), "base query", store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "claim-base-1", Claim: "base one", Rank: 0.70, TextScore: 0.3},
			{ID: "claim-base-2", Claim: "base two", Rank: 0.69, TextScore: 0.2},
			{ID: "claim-extra", Claim: "extra", Rank: 0.68, TextScore: 0.1},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-base-1", Title: "base one", Content: "doc one", Rank: 0.70, TextScore: 0.3},
			{ID: "doc-base-2", Title: "base two", Content: "doc two", Rank: 0.69, TextScore: 0.2},
			{ID: "doc-extra", Title: "extra", Content: "doc three", Rank: 0.68, TextScore: 0.1},
		},
		GraphContext: []store.RelationResult{
			{ID: "relation-1", FromEntity: "A", ToEntity: "B", Type: "depends_on"},
			{ID: "relation-2", FromEntity: "B", ToEntity: "C", Type: "depends_on"},
			{ID: "relation-3", FromEntity: "C", ToEntity: "D", Type: "depends_on"},
		},
	}, 2)

	if len(result.Claims) != 2 || result.Claims[0].ID != "claim-base-1" || result.Claims[1].ID != "claim-base-2" {
		t.Fatalf("failed rerank should preserve base claim order before trim: %#v", result.Claims)
	}
	if len(result.SupportingDocuments) != 2 || result.SupportingDocuments[0].ID != "doc-base-1" || result.SupportingDocuments[1].ID != "doc-base-2" {
		t.Fatalf("failed rerank should preserve base document order before trim: %#v", result.SupportingDocuments)
	}
	if result.RetrievalMode != "hybrid" {
		t.Fatalf("failed rerank mode = %q, want hybrid", result.RetrievalMode)
	}
	if hasBrainRetrievalReason(result.RetrievalReasons, "rerank", "hybrid_reranked", 4) {
		t.Fatalf("failed rerank should not add rerank reason: %#v", result.RetrievalReasons)
	}
	if len(result.RetrievalWarnings) != 2 {
		t.Fatalf("rerank warnings = %#v, want claim and document warnings", result.RetrievalWarnings)
	}
	if result.Claims[0].RerankApplied || result.SupportingDocuments[0].RerankApplied {
		t.Fatalf("failed rerank should not mark final results as reranked: claims=%#v docs=%#v", result.Claims, result.SupportingDocuments)
	}
}

func TestRerankRecallSurfacesRerankerFailure(t *testing.T) {
	service := Service{
		cfg:      config.Config{Reranker: config.AIProviderConfig{Provider: "local", Model: "rerank-model"}},
		reranker: &fakeRerankerProvider{err: errors.New("rate limited")},
	}
	result := service.rerankRecall(context.Background(), "base query", store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "claim-1", Claim: "base result", Rank: 0.8, TextScore: 0.4, VectorScore: 0.2},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-1", Title: "doc", Content: "supporting result", Rank: 0.7, TextScore: 0.3, VectorScore: 0.2},
		},
	})

	if len(result.RetrievalWarnings) != 2 {
		t.Fatalf("warnings = %#v, want claim and document rerank warnings", result.RetrievalWarnings)
	}
	if result.RetrievalWarnings[0].Stage != "retrieval" || result.RetrievalWarnings[0].Operation != "rerank_claims" || !strings.Contains(result.RetrievalWarnings[0].Message, "rate limited") {
		t.Fatalf("unexpected claim warning: %#v", result.RetrievalWarnings[0])
	}
	if result.RetrievalWarnings[0].Query != "" {
		t.Fatalf("rerank warning leaked query: %#v", result.RetrievalWarnings[0])
	}
	if result.RetrievalWarnings[1].Stage != "retrieval" || result.RetrievalWarnings[1].Operation != "rerank_documents" || !strings.Contains(result.RetrievalWarnings[1].Message, "rate limited") {
		t.Fatalf("unexpected document warning: %#v", result.RetrievalWarnings[1])
	}
	if result.RetrievalWarnings[1].Query != "" {
		t.Fatalf("rerank warning leaked query: %#v", result.RetrievalWarnings[1])
	}
	if result.RetrievalMode != "hybrid" || len(result.RetrievalReasons) != 0 {
		t.Fatalf("failed rerank should not mark mode/reasons as reranked: mode=%q reasons=%#v", result.RetrievalMode, result.RetrievalReasons)
	}
	if result.Claims[0].Rank != 0.8 || result.Claims[0].BaseRank != 0.8 || result.Claims[0].RerankApplied {
		t.Fatalf("base claim should be preserved without rerank metadata: %#v", result.Claims[0])
	}
}

func TestRerankRecallBoundsBoostAndStoresScores(t *testing.T) {
	service := Service{
		cfg: config.Config{Reranker: config.AIProviderConfig{Provider: "local", Model: "rerank-model"}},
		reranker: &fakeRerankerProvider{
			responses: []ai.RerankResponse{
				{Results: []ai.RerankResult{{Index: 1, Score: 99}, {Index: 0, Score: -3}}},
				{Results: []ai.RerankResult{{Index: 1, Score: 0.99}, {Index: 0, Score: 0.01}}},
			},
		},
	}

	result := service.rerankRecall(context.Background(), "base query", store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "base-best", Claim: "strong base", Rank: 0.9, TextScore: 0.5, VectorScore: 0.4},
			{ID: "rerank-favorite", Claim: "weak base", Rank: 0.2, TextScore: 0.1, VectorScore: 0.1},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-base-best", Title: "strong", Content: "strong doc", Rank: 0.9, TextScore: 0.5, VectorScore: 0.4},
			{ID: "doc-rerank-favorite", Title: "weak", Content: "weak doc", Rank: 0.2, TextScore: 0.1, VectorScore: 0.1},
		},
	})

	if result.RetrievalMode != "hybrid_reranked" {
		t.Fatalf("retrieval mode = %q, want hybrid_reranked", result.RetrievalMode)
	}
	if len(result.RetrievalWarnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", result.RetrievalWarnings)
	}
	if result.Claims[0].ID != "base-best" || result.Claims[1].ID != "rerank-favorite" {
		t.Fatalf("bounded rerank should not swamp base claim rank: %#v", result.Claims)
	}
	if result.Claims[0].BaseRank != 0.9 || result.Claims[0].RerankScore != 0 || !result.Claims[0].RerankApplied {
		t.Fatalf("base claim rerank metadata wrong: %#v", result.Claims[0])
	}
	if result.Claims[1].BaseRank != 0.2 || result.Claims[1].RerankScore != 1 || !result.Claims[1].RerankApplied || result.Claims[1].Rank != 0.4 {
		t.Fatalf("reranked claim metadata wrong: %#v", result.Claims[1])
	}
	if result.SupportingDocuments[0].ID != "doc-base-best" || result.SupportingDocuments[1].ID != "doc-rerank-favorite" {
		t.Fatalf("bounded rerank should not swamp base document rank: %#v", result.SupportingDocuments)
	}
	if result.SupportingDocuments[1].BaseRank != 0.2 || result.SupportingDocuments[1].RerankScore != 0.99 || !result.SupportingDocuments[1].RerankApplied {
		t.Fatalf("reranked document metadata wrong: %#v", result.SupportingDocuments[1])
	}
}

func TestRerankRecallIgnoresInvalidRerankIndexes(t *testing.T) {
	service := Service{
		cfg: config.Config{Reranker: config.AIProviderConfig{Provider: "local", Model: "rerank-model"}},
		reranker: &fakeRerankerProvider{
			responses: []ai.RerankResponse{
				{Results: []ai.RerankResult{{Index: 7, Score: 1}}},
			},
		},
	}
	result := service.rerankRecall(context.Background(), "base query", store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "claim-1", Claim: "base result", Rank: 0.8, TextScore: 0.4, VectorScore: 0.2},
		},
	})

	if result.RetrievalMode != "hybrid" || len(result.RetrievalReasons) != 0 {
		t.Fatalf("invalid rerank indexes should not mark result reranked: mode=%q reasons=%#v", result.RetrievalMode, result.RetrievalReasons)
	}
	if result.Claims[0].RerankApplied || result.Claims[0].RerankScore != 0 || result.Claims[0].Rank != 0.8 || result.Claims[0].BaseRank != 0.8 {
		t.Fatalf("invalid rerank indexes should not alter claim: %#v", result.Claims[0])
	}
}

func TestRerankMetadataJSONIncludesZeroScore(t *testing.T) {
	raw, err := json.Marshal(store.ClaimResult{
		ID:            "claim-1",
		Claim:         "base result",
		Rank:          0.9,
		BaseRank:      0.9,
		RerankApplied: true,
		RerankScore:   0,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"base_rank_score":0.9`, `"rerank_score":0`, `"rerank_applied":true`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json missing %s: %s", want, raw)
		}
	}
}

func aiProviderMetricValue(metrics []observability.AIProviderMetric, operation, provider, status, field string) int64 {
	var total int64
	for _, metric := range metrics {
		if metric.Operation != operation || metric.Provider != provider || metric.Status != status {
			continue
		}
		switch field {
		case "calls":
			total += metric.Calls
		case "waits":
			total += metric.Waits
		case "max_in_flight":
			total += metric.MaxInFlight
		}
	}
	return total
}

func hasBrainRetrievalReason(reasons []store.RetrievalReason, signal, mode string, count int) bool {
	for _, reason := range reasons {
		if reason.Signal == signal && reason.Mode == mode && reason.Count == count {
			return true
		}
	}
	return false
}

type fakeRerankerProvider struct {
	err       error
	responses []ai.RerankResponse
	calls     int
}

func (p *fakeRerankerProvider) Name() string {
	return "fake-reranker"
}

func (p *fakeRerankerProvider) Kind() ai.ProviderKind {
	return ai.ProviderOpenAICompatible
}

func (p *fakeRerankerProvider) Validate() error {
	return nil
}

func (p *fakeRerankerProvider) Rerank(_ context.Context, request ai.RerankRequest) (ai.RerankResponse, error) {
	p.calls++
	if p.err != nil {
		return ai.RerankResponse{}, p.err
	}
	if len(p.responses) >= p.calls {
		response := p.responses[p.calls-1]
		response.Provider = p.Name()
		if response.Model == "" {
			response.Model = request.Model
		}
		return response, nil
	}
	results := make([]ai.RerankResult, 0, len(request.Documents))
	for i := range request.Documents {
		results = append(results, ai.RerankResult{Index: i, Score: 0.5})
	}
	return ai.RerankResponse{Provider: p.Name(), Model: request.Model, Results: results}, nil
}

type recordingEmbeddingProvider struct {
	callSizes   []int
	totalInputs int
}

func (p *recordingEmbeddingProvider) Name() string {
	return "recording"
}

func (p *recordingEmbeddingProvider) Kind() ai.ProviderKind {
	return ai.ProviderOpenAICompatible
}

func (p *recordingEmbeddingProvider) Validate() error {
	return nil
}

func (p *recordingEmbeddingProvider) Embed(_ context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	p.callSizes = append(p.callSizes, len(request.Input))
	embeddings := make([]ai.Embedding, len(request.Input))
	for i := range request.Input {
		embeddings[i] = ai.Embedding{Index: i, Vector: []float64{float64(p.totalInputs + i), 0, 0}, Dimensions: 3}
	}
	p.totalInputs += len(request.Input)
	return ai.EmbeddingResponse{
		Provider:   p.Name(),
		Model:      "test-embedding",
		Embeddings: embeddings,
		Usage:      &ai.Usage{PromptTokens: len(request.Input), TotalTokens: len(request.Input)},
	}, nil
}

type contextOverflowEmbeddingProvider struct {
	callSizes     []int
	failAtOrAbove int
}

func (p *contextOverflowEmbeddingProvider) Name() string {
	return "overflow"
}

func (p *contextOverflowEmbeddingProvider) Kind() ai.ProviderKind {
	return ai.ProviderOpenAICompatible
}

func (p *contextOverflowEmbeddingProvider) Validate() error {
	return nil
}

func (p *contextOverflowEmbeddingProvider) Embed(_ context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	p.callSizes = append(p.callSizes, len(request.Input))
	if len(request.Input) >= p.failAtOrAbove {
		return ai.EmbeddingResponse{}, &ai.ProviderError{
			Operation:   "embedding",
			Provider:    "local",
			Model:       "qwen",
			Code:        "context_overflow",
			Status:      http.StatusBadRequest,
			Retryable:   false,
			BatchStart:  intFromMetadata(request.Metadata, "batch_start"),
			BatchEnd:    intFromMetadata(request.Metadata, "batch_end"),
			BatchSize:   len(request.Input),
			BatchTokens: intFromMetadata(request.Metadata, "batch_tokens"),
			Message:     "request exceeds available context size",
		}
	}
	start := intFromMetadata(request.Metadata, "batch_start")
	embeddings := make([]ai.Embedding, len(request.Input))
	for i := range request.Input {
		embeddings[i] = ai.Embedding{Index: i, Vector: []float64{float64(start + i), 0, 0}, Dimensions: 3}
	}
	return ai.EmbeddingResponse{
		Provider:   p.Name(),
		Model:      "test-embedding",
		Embeddings: embeddings,
		Usage:      &ai.Usage{PromptTokens: len(request.Input), TotalTokens: len(request.Input)},
	}, nil
}

type providerTimeoutEmbeddingProvider struct {
	callSizes         []int
	failOnceAtOrAbove int
	failed            bool
}

func (p *providerTimeoutEmbeddingProvider) Name() string {
	return "timeout"
}

func (p *providerTimeoutEmbeddingProvider) Kind() ai.ProviderKind {
	return ai.ProviderOpenAICompatible
}

func (p *providerTimeoutEmbeddingProvider) Validate() error {
	return nil
}

func (p *providerTimeoutEmbeddingProvider) Embed(_ context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	p.callSizes = append(p.callSizes, len(request.Input))
	if !p.failed && len(request.Input) >= p.failOnceAtOrAbove {
		p.failed = true
		return ai.EmbeddingResponse{}, &ai.ProviderError{
			Operation:   "embedding",
			Provider:    "local",
			Model:       "qwen",
			Code:        "provider_timeout",
			Status:      http.StatusGatewayTimeout,
			Retryable:   true,
			BatchStart:  intFromMetadata(request.Metadata, "batch_start"),
			BatchEnd:    intFromMetadata(request.Metadata, "batch_end"),
			BatchSize:   len(request.Input),
			BatchTokens: intFromMetadata(request.Metadata, "batch_tokens"),
			Message:     "provider timed out",
		}
	}
	start := intFromMetadata(request.Metadata, "batch_start")
	embeddings := make([]ai.Embedding, len(request.Input))
	for i := range request.Input {
		embeddings[i] = ai.Embedding{Index: i, Vector: []float64{float64(start + i), 0, 0}, Dimensions: 3}
	}
	return ai.EmbeddingResponse{
		Provider:   p.Name(),
		Model:      "test-embedding",
		Embeddings: embeddings,
		Usage:      &ai.Usage{PromptTokens: len(request.Input), TotalTokens: len(request.Input)},
	}, nil
}

func intFromMetadata(metadata map[string]any, key string) int {
	value, _ := metadata[key].(int)
	return value
}

type concurrentEmbeddingProvider struct {
	delay         time.Duration
	mu            sync.Mutex
	inFlight      int
	maxConcurrent int
}

func (p *concurrentEmbeddingProvider) Name() string {
	return "concurrent"
}

func (p *concurrentEmbeddingProvider) Kind() ai.ProviderKind {
	return ai.ProviderOpenAICompatible
}

func (p *concurrentEmbeddingProvider) Validate() error {
	return nil
}

func (p *concurrentEmbeddingProvider) Embed(ctx context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxConcurrent {
		p.maxConcurrent = p.inFlight
	}
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()

	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ai.EmbeddingResponse{}, ctx.Err()
	}

	embeddings := make([]ai.Embedding, len(request.Input))
	for i := range request.Input {
		embeddings[i] = ai.Embedding{Index: i, Vector: []float64{1, 0, 0}, Dimensions: 3}
	}
	return ai.EmbeddingResponse{Provider: p.Name(), Model: "test-embedding", Embeddings: embeddings}, nil
}

func TestRedactSecretContext(t *testing.T) {
	input := "Access to private registry - request the rotated REGISTRY_USER and REGISTRY_PASSWORD from Infra; stored as CI workspace variables."
	got := redact(input)
	if strings.Contains(got, "REGISTRY_USER") || strings.Contains(got, "REGISTRY_PASSWORD") {
		t.Fatalf("redaction leaked credential names: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "request the rotated") {
		t.Fatalf("redaction leaked credential instructions: %q", got)
	}
}

func TestRedactKeepsNonSecretDomainTokens(t *testing.T) {
	input := "Example Web App uses `Shared UI Tokens` for shared UI primitives."
	got := redact(input)
	if got != input {
		t.Fatalf("redaction changed non-secret domain text: %q", got)
	}
}

func TestRedactObservationValueRedactsNestedTranscriptContent(t *testing.T) {
	got := redactObservationValue(map[string]any{
		"content": "Contact me at person@example.com and use REGISTRY_PASSWORD from vault.",
		"nested": map[string]any{
			"turns": []any{
				"Call +6281234567890",
				map[string]any{"text": "credential TOKEN_SECRET is rotated"},
			},
		},
		"count": 2,
	})
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, leaked := range []string{"person@example.com", "+6281234567890", "REGISTRY_PASSWORD", "TOKEN_SECRET"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("redacted value leaked %q: %s", leaked, text)
		}
	}
	if got["count"] != 2 {
		t.Fatalf("non-string value changed: %#v", got)
	}
}

type storeLevelKeySummary struct {
	level   string
	key     string
	summary string
}

func levelKeySummaries(records []store.MemorySummaryRecord) []storeLevelKeySummary {
	out := make([]storeLevelKeySummary, 0, len(records))
	for _, record := range records {
		out = append(out, storeLevelKeySummary{level: record.Level, key: record.Key, summary: record.Summary})
	}
	return out
}

func assertSummary(t *testing.T, summaries []storeLevelKeySummary, level, key, contains string) {
	t.Helper()
	for _, summary := range summaries {
		if summary.level == level && summary.key == key {
			if !strings.Contains(summary.summary, contains) {
				t.Fatalf("%s/%s summary = %q, want substring %q", level, key, summary.summary, contains)
			}
			return
		}
	}
	t.Fatalf("missing summary level=%s key=%s in %#v", level, key, summaries)
}
