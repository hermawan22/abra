package brain

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/store"
)

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

type recordingEmbeddingProvider struct {
	callSizes []int
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
		embeddings[i] = ai.Embedding{Index: i, Vector: []float64{1, 0, 0}, Dimensions: 3}
	}
	return ai.EmbeddingResponse{
		Provider:   p.Name(),
		Model:      "test-embedding",
		Embeddings: embeddings,
		Usage:      &ai.Usage{PromptTokens: len(request.Input), TotalTokens: len(request.Input)},
	}, nil
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
