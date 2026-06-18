package memory

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestGraphWarningsDetectCompetingAlternatives(t *testing.T) {
	warnings := graphWarnings([]store.RelationResult{
		{ID: "relation-playwright", FromEntity: "Frontend App", ToEntity: "Playwright", Type: "should_use", Confidence: 0.9},
		{ID: "relation-cypress", FromEntity: "Frontend App", ToEntity: "Cypress", Type: "should_use", Confidence: 0.8},
		{FromEntity: "Frontend App", ToEntity: "Shared UI Tokens", Type: "should_use", Confidence: 0.7},
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one competing alternative warning", warnings)
	}
	if warnings[0].WarningType != "competing_graph_alternatives" || warnings[0].Severity != "high" {
		t.Fatalf("warning = %#v, want high competing alternative", warnings[0])
	}
	if len(warnings[0].Relations) != 2 || warnings[0].Relations[0].ID == "" || warnings[0].Relations[1].ID == "" {
		t.Fatalf("warning relations should preserve relation ids: %#v", warnings[0].Relations)
	}
}

func TestGraphWarningsDetectOpposingPolicy(t *testing.T) {
	warnings := graphWarnings([]store.RelationResult{
		{FromEntity: "Frontend App", ToEntity: "Cypress", Type: "should_not_use", Confidence: 0.9},
		{FromEntity: "Frontend App", ToEntity: "Cypress", Type: "uses", Confidence: 0.8},
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want exactly one opposing policy warning", warnings)
	}
	if warnings[0].WarningType != "opposing_graph_policy" || warnings[0].Severity != "high" {
		t.Fatalf("warning = %#v, want high opposing policy", warnings[0])
	}
}

func TestGraphWarningsIgnoresNonExclusiveRelations(t *testing.T) {
	warnings := graphWarnings([]store.RelationResult{
		{FromEntity: "Frontend App", ToEntity: "Playwright", Type: "should_use", Confidence: 0.9},
		{FromEntity: "Frontend App", ToEntity: "Shared UI Tokens", Type: "should_use", Confidence: 0.8},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none for non-exclusive relations", warnings)
	}
}
