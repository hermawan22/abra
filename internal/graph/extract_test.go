package graph

import (
	"reflect"
	"testing"
)

func TestExtractTextFindsBacktickedAndCapitalizedEntities(t *testing.T) {
	got := ExtractText("The Platform Team owns `ledger-service`. Billing Service uses Kafka. Risk API depends on Postgres.")

	want := []EntityCandidate{
		{Name: "Billing Service", Type: "service", Mentions: 2},
		{Name: "Kafka", Type: "technology", Mentions: 2},
		{Name: "ledger-service", Type: "service", Mentions: 2},
		{Name: "Platform Team", Type: "team", Mentions: 2},
		{Name: "Postgres", Type: "technology", Mentions: 2},
		{Name: "Risk API", Type: "service", Mentions: 2},
	}
	if !reflect.DeepEqual(got.Entities, want) {
		t.Fatalf("entities = %#v, want %#v", got.Entities, want)
	}
}

func TestExtractTextFindsRelationPatterns(t *testing.T) {
	got := ExtractText("Payments Service uses Kafka, Postgres, and Redis. Platform Team owns Payments Service. Risk API should use Ledger API. Billing API depends on Risk API. Frontend App must not use Cypress.")

	want := []RelationCandidate{
		{From: "Billing API", To: "Risk API", Type: "depends_on", Evidence: "Billing API depends on Risk API", Confidence: 0.7},
		{From: "Frontend App", To: "Cypress", Type: "should_not_use", Evidence: "Frontend App must not use Cypress", Confidence: 0.7},
		{From: "Payments Service", To: "Kafka", Type: "uses", Evidence: "Payments Service uses Kafka, Postgres, and Redis", Confidence: 0.7},
		{From: "Payments Service", To: "Postgres", Type: "uses", Evidence: "Payments Service uses Kafka, Postgres, and Redis", Confidence: 0.7},
		{From: "Payments Service", To: "Redis", Type: "uses", Evidence: "Payments Service uses Kafka, Postgres, and Redis", Confidence: 0.7},
		{From: "Platform Team", To: "Payments Service", Type: "owns", Evidence: "Platform Team owns Payments Service", Confidence: 0.7},
		{From: "Risk API", To: "Ledger API", Type: "should_use", Evidence: "Risk API should use Ledger API", Confidence: 0.7},
	}
	if !reflect.DeepEqual(got.Relations, want) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, want)
	}
}

func TestExtractTextKeepsDottedTechNamesInRelations(t *testing.T) {
	got := ExtractText("Frontend App uses Next.js. Web Team owns Frontend App.")

	want := []RelationCandidate{
		{From: "Frontend App", To: "Next.js", Type: "uses", Evidence: "Frontend App uses Next.js", Confidence: 0.7},
		{From: "Web Team", To: "Frontend App", Type: "owns", Evidence: "Web Team owns Frontend App", Confidence: 0.7},
	}
	if !reflect.DeepEqual(got.Relations, want) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, want)
	}
}

func TestExtractTextFindsLifecycleRelationPatterns(t *testing.T) {
	got := ExtractText("Checkout API replaces Legacy Checkout API. Payment Job duplicates Billing Job. Risk Model derives from Fraud Model.")

	want := []RelationCandidate{
		{From: "Checkout API", To: "Legacy Checkout API", Type: "supersedes", Evidence: "Checkout API replaces Legacy Checkout API", Confidence: 0.7},
		{From: "Payment Job", To: "Billing Job", Type: "duplicates", Evidence: "Payment Job duplicates Billing Job", Confidence: 0.7},
		{From: "Risk Model", To: "Fraud Model", Type: "derives_from", Evidence: "Risk Model derives from Fraud Model", Confidence: 0.7},
	}
	if !reflect.DeepEqual(got.Relations, want) {
		t.Fatalf("relations = %#v, want %#v", got.Relations, want)
	}
}

func TestExtractTextSkipsGenericLifecyclePhrases(t *testing.T) {
	got := ExtractText("This derives from the Work. This duplicates Previous Guidance. Current replaces Previous.")
	if len(got.Relations) != 0 {
		t.Fatalf("relations = %#v, want none", got.Relations)
	}
}

func TestExtractFromDocumentsCarriesSourceMetadataAndDedupes(t *testing.T) {
	got := ExtractFromDocuments([]Document{
		{
			ID:        "doc-2",
			Title:     "Architecture",
			Content:   "Risk API uses Kafka.",
			SourceURL: "file://risk.md",
		},
		{
			ID:        "doc-1",
			Title:     "Architecture",
			Content:   "Risk API uses Kafka. Risk API uses Kafka.",
			SourceURL: "file://risk-copy.md",
		},
	})

	if len(got.Relations) != 1 {
		t.Fatalf("relations = %#v, want one deduped relation", got.Relations)
	}
	want := RelationCandidate{
		From:       "Risk API",
		To:         "Kafka",
		Type:       "uses",
		Evidence:   "Risk API uses Kafka",
		SourceID:   "doc-2",
		SourceURL:  "file://risk.md",
		Confidence: 0.7,
	}
	if !reflect.DeepEqual(got.Relations[0], want) {
		t.Fatalf("relation = %#v, want %#v", got.Relations[0], want)
	}
}

func TestExtractFromClaimsIsDeterministic(t *testing.T) {
	claims := []string{
		"Risk API should use Ledger API.",
		"`ledger-service` depends on Postgres.",
		"Platform Team owns Risk API.",
	}

	first := ExtractFromClaims(claims)
	second := ExtractFromClaims(claims)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("extraction is not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
}
