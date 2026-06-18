package brain

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestExtractPolicyAssertion(t *testing.T) {
	assertion, ok := extractPolicyAssertion("Frontend apps must use Playwright for browser tests.")
	if !ok {
		t.Fatal("expected policy assertion")
	}
	if assertion.Subject != "frontend apps" {
		t.Fatalf("subject = %q", assertion.Subject)
	}
	if assertion.Value != "playwright" {
		t.Fatalf("value = %q", assertion.Value)
	}
	if assertion.Purpose != "browser tests" {
		t.Fatalf("purpose = %q", assertion.Purpose)
	}
	if assertion.Key != "frontend apps :: browser tests" {
		t.Fatalf("key = %q", assertion.Key)
	}
	if assertion.Negated {
		t.Fatal("expected positive assertion")
	}
}

func TestPolicyAssertionsConflictOnDifferentPositiveValues(t *testing.T) {
	left, _ := extractPolicyAssertion("Frontend apps must use Playwright for browser tests.")
	right, _ := extractPolicyAssertion("Frontend apps should use Cypress for browser tests.")
	if !policyAssertionsConflict(left, right) {
		t.Fatal("expected different positive policy values to conflict")
	}
	if got := policyConflictSeverity(left, right); got != "high" {
		t.Fatalf("severity = %q, want high", got)
	}
}

func TestPolicyAssertionsConflictOnPositiveAndNegativeSameValue(t *testing.T) {
	left, _ := extractPolicyAssertion("Frontend apps must use Cypress for browser tests.")
	right, _ := extractPolicyAssertion("Frontend apps should not use Cypress for browser tests.")
	if !policyAssertionsConflict(left, right) {
		t.Fatal("expected positive and negative same value to conflict")
	}
}

func TestPolicyAssertionsIgnoreDifferentPolicyKeys(t *testing.T) {
	left, _ := extractPolicyAssertion("Frontend apps must use Playwright for browser tests.")
	right, _ := extractPolicyAssertion("Backend services must use Cypress for browser tests.")
	if policyAssertionsConflict(left, right) {
		t.Fatal("different subjects should not conflict")
	}
}

func TestPolicyAssertionsIgnoreDifferentNegativeValues(t *testing.T) {
	left, _ := extractPolicyAssertion("Frontend apps should not use Cypress for browser tests.")
	right, _ := extractPolicyAssertion("Frontend apps should not use Selenium for browser tests.")
	if policyAssertionsConflict(left, right) {
		t.Fatal("different negative values should not conflict")
	}
}

func TestGraphRelationConflictDetectsCompetingBrowserTestRunner(t *testing.T) {
	conflictType, severity, reason, ok := graphRelationConflict(
		graphRelationConflictCandidate{TargetEntity: "Cypress", RelationType: "should_use"},
		store.GraphRelationResult{ToEntity: "Playwright", Type: "should_use"},
	)
	if !ok {
		t.Fatal("expected competing browser test runner relation conflict")
	}
	if conflictType != "competes_with" || severity != "high" || reason == "" {
		t.Fatalf("unexpected conflict result: type=%q severity=%q reason=%q", conflictType, severity, reason)
	}
}

func TestGraphRelationConflictDetectsOpposingUsePolicy(t *testing.T) {
	conflictType, severity, reason, ok := graphRelationConflict(
		graphRelationConflictCandidate{TargetEntity: "Cypress", RelationType: "should_not_use"},
		store.GraphRelationResult{ToEntity: "Cypress", Type: "uses"},
	)
	if !ok {
		t.Fatal("expected opposing graph use policy conflict")
	}
	if conflictType != "contradicts" || severity != "high" || reason == "" {
		t.Fatalf("unexpected conflict result: type=%q severity=%q reason=%q", conflictType, severity, reason)
	}
}

func TestGraphRelationConflictIgnoresNonExclusiveTargets(t *testing.T) {
	_, _, _, ok := graphRelationConflict(
		graphRelationConflictCandidate{TargetEntity: "Shared UI Tokens", RelationType: "should_use"},
		store.GraphRelationResult{ToEntity: "Playwright", Type: "should_use"},
	)
	if ok {
		t.Fatal("did not expect non-exclusive target relation conflict")
	}
}
