package brain

import (
	"strings"
	"testing"

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

func TestRedactSecretContext(t *testing.T) {
	input := "Access to Nexus - request the rotated NEXUS_USER and NEXUS_PASSWORD from Infra; stored as Bitbucket workspace variables."
	got := redact(input)
	if strings.Contains(got, "NEXUS_USER") || strings.Contains(got, "NEXUS_PASSWORD") {
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
