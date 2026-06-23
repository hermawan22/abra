package memory

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/store"
)

func TestBuildThinkResultIncludesCitationsGapsAndDecision(t *testing.T) {
	source := "file://docs/adr.md"
	packet := ComposeResult{
		Task:  "Which runtime should agents use?",
		Scope: "repo:example",
		Facts: []store.ClaimResult{
			{
				ID:        "claim-1",
				Claim:     "Agents must use Abra before autonomous code changes.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "fresh",
				Rank:      1.2,
			},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-1", Title: "ADR", Source: source, Content: "Use Abra first.", Rank: 0.9},
		},
		GraphContext: []store.RelationResult{
			{ID: "rel-1", FromEntity: "Agent", Type: "uses", ToEntity: "Abra", Confidence: 0.91, SourceURL: &source},
		},
		RetrievalReasons: []store.RetrievalReason{
			{Mode: "hybrid", Signal: "text", Message: "Full-text matches contributed.", Count: 1},
			{Mode: "hybrid", Signal: "vector", Message: "Semantic matches contributed.", Count: 1},
		},
		MemoryHealth: store.MemoryHealthResult{Status: "healthy"},
		Verification: VerificationReport{
			Verdict:           "strong",
			Score:             0.94,
			RetrievalCoverage: RetrievalCoverage{Complete: true},
			RetrievalQuality:  RetrievalQuality{ResultCount: 2, TopRankScore: 1.2},
			Recommendations:   []string{"keep citing source evidence"},
		},
		AgentDecision: AgentDecision{
			Decision:           "proceed",
			AutonomousAllowed:  true,
			AllowedNextActions: []string{"cite_evidence"},
		},
		Stats: ComposeStats{Facts: 1, SupportingDocuments: 1, GraphRelations: 1},
	}

	result := BuildThinkResult(packet)
	if !strings.Contains(result.Answer, "Abra's governed answer") {
		t.Fatalf("answer did not look synthesized:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "[C1]") {
		t.Fatalf("answer missing citation ref:\n%s", result.Answer)
	}
	if len(result.Citations) != 1 || result.Citations[0].SourceURL != source {
		t.Fatalf("citations = %#v", result.Citations)
	}
	if result.Citations[0].ClaimID != "claim-1" || result.Citations[0].DocumentID != "doc-1" || len(result.Citations[0].RelationIDs) != 1 {
		t.Fatalf("citation lineage = %#v", result.Citations[0])
	}
	if len(result.GraphPaths) != 1 || result.GraphPaths[0].CitationRef != "C1" {
		t.Fatalf("graph paths = %#v", result.GraphPaths)
	}
	if len(result.Gaps) != 0 {
		t.Fatalf("unexpected gaps = %#v", result.Gaps)
	}
	if result.AgentDecision.Decision != "proceed" {
		t.Fatalf("decision = %#v", result.AgentDecision)
	}
	if len(result.RetrievalReasons) != 2 || result.RetrievalReasons[0].Signal != "text" {
		t.Fatalf("retrieval reasons = %#v", result.RetrievalReasons)
	}
}

func TestBuildThinkResultIncludesEvidenceAnchors(t *testing.T) {
	source := "file://docs/runbook.md"
	packet := ComposeResult{
		Task:  "How should retries work?",
		Scope: "repo:example",
		Facts: []store.ClaimResult{
			{
				ID:        "claim-1",
				Claim:     "Retry callbacks must remain idempotent.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "fresh",
			},
		},
		SupportingDocuments: []store.DocumentResult{
			{
				ID:      "doc-1",
				Title:   "Runbook",
				Source:  source,
				Content: "Deployment note.\nRetry callbacks must remain idempotent. Operators should verify duplicate webhook delivery.",
			},
		},
		MemoryHealth:  store.MemoryHealthResult{Status: "healthy", Score: 100},
		AgentDecision: AgentDecision{Decision: "proceed", AutonomousAllowed: true},
	}
	citations, refs := buildCitations(packet)
	packet.Citations = citations
	packet.EvidenceAnchors = evidenceAnchors(packet.Facts, packet.SupportingDocuments, refs)
	packet.Citations = attachCitationAnchors(packet.Citations, packet.EvidenceAnchors)
	packet.Evidence = evidence(packet.Facts, packet.SupportingDocuments, refs, packet.EvidenceAnchors)
	packet.Verification = verifyPacket(packet.Summaries, packet.Facts, packet.SupportingDocuments, packet.GraphContext, packet.Evidence, packet.RetrievalPlan, nil, nil, nil, packet.MemoryHealth, packet.EvidenceAnchors)

	result := BuildThinkResult(packet)
	if len(result.EvidenceAnchors) == 0 {
		t.Fatalf("expected evidence anchors")
	}
	if result.EvidenceAnchors[0].Kind != "claim" || !strings.Contains(result.EvidenceAnchors[0].Quote, "Retry callbacks must remain idempotent") {
		t.Fatalf("claim anchor = %#v", result.EvidenceAnchors[0])
	}
	if len(result.Citations) != 1 || len(result.Citations[0].Anchors) == 0 {
		t.Fatalf("citation anchors = %#v", result.Citations)
	}
	if len(result.Verification.WeakEvidenceAnchors) != 0 {
		t.Fatalf("anchored claim should not be weak: %#v", result.Verification.WeakEvidenceAnchors)
	}
}

type fakeSynthesizer struct {
	result SynthesisResult
	err    error
}

func (f fakeSynthesizer) Synthesize(context.Context, SynthesisInput) (SynthesisResult, error) {
	return f.result, f.err
}

type fakeExtractorProvider struct {
	value any
}

func (f fakeExtractorProvider) Name() string { return "fake" }

func (f fakeExtractorProvider) Kind() ai.ProviderKind { return ai.ProviderOpenAICompatible }

func (f fakeExtractorProvider) Validate() error { return nil }

func (f fakeExtractorProvider) Extract(context.Context, ai.ExtractionRequest) (ai.ExtractionResponse, error) {
	return ai.ExtractionResponse{Provider: "fake", Model: "model", Value: f.value}, nil
}

func TestThinkUsesOptionalSynthesisWhenValid(t *testing.T) {
	store := &fakeStore{}
	composer := NewComposerWithOptions(store, ComposerOptions{
		Synthesizer: fakeSynthesizer{result: SynthesisResult{Enabled: true, Status: "ok", Provider: "fake", Model: "model", Answer: "Synthesized answer [C1].", CitationRefs: []string{"C1"}}},
	})
	result, err := composer.Think(context.Background(), ThinkInput{Question: "What runtime?", Scope: "repo:test", Synthesize: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "Synthesized answer [C1]." {
		t.Fatalf("answer = %q", result.Answer)
	}
	if result.DeterministicAnswer == "" || result.Synthesis.Status != "ok" {
		t.Fatalf("synthesis metadata = %#v deterministic=%q", result.Synthesis, result.DeterministicAnswer)
	}
}

func TestExtractorSynthesizerRejectsUncitedAnswer(t *testing.T) {
	synthesizer := NewExtractorSynthesizer(fakeExtractorProvider{value: map[string]any{
		"answer":        "Synthesized answer without citation.",
		"citation_refs": []any{},
	}}, "model", 100)
	result, err := synthesizer.Synthesize(context.Background(), SynthesisInput{
		Question:            "What runtime?",
		Scope:               "repo:test",
		DeterministicAnswer: "Use runtime [C1].",
		Citations:           []Citation{{Ref: "C1", SourceURL: "file://runbook.md"}},
		EvidenceAnchors:     []EvidenceAnchor{{Kind: "claim", Ref: "C1", Quote: "Use runtime."}},
		Verification:        VerificationReport{Verdict: "strong"},
		AgentDecision:       AgentDecision{Decision: "proceed", AutonomousAllowed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "rejected" || !strings.Contains(result.Warning, "citation ref") {
		t.Fatalf("synthesis = %#v", result)
	}
}

func TestThinkKeepsDeterministicAnswerWhenSynthesisUnavailable(t *testing.T) {
	composer := NewComposerWithOptions(&fakeStore{}, ComposerOptions{})
	result, err := composer.Think(context.Background(), ThinkInput{Question: "What runtime?", Scope: "repo:test", Synthesize: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Synthesis.Status != "disabled" {
		t.Fatalf("synthesis = %#v", result.Synthesis)
	}
	if result.Answer == "" || result.Answer != result.DeterministicAnswer {
		t.Fatalf("deterministic fallback answer=%q deterministic=%q", result.Answer, result.DeterministicAnswer)
	}
}

func TestEvaluateThinkResultChecksCitationAndAnchors(t *testing.T) {
	result := ThinkResult{
		Answer: "Retry callbacks stay idempotent [C1].",
		Citations: []Citation{
			{Ref: "C1", SourceURL: "file://runbook.md"},
		},
		EvidenceAnchors: []EvidenceAnchor{
			{Kind: "claim", Ref: "C1", Quote: "Retry callbacks stay idempotent."},
		},
		Gaps:            []ThinkGap{{Code: "stale_claims", Severity: "medium"}},
		Conflicts:       []store.ConflictResult{{ID: "conflict-1"}},
		TemporalContext: TemporalContext{HistoricalIncluded: true},
		EntityDossiers: []EntityDossier{
			{
				Entity: "Retry Service",
				Trust:  "anchored",
				Stats:  EntityDossierStats{ActiveClaims: 2},
			},
		},
		Synthesis:     SynthesisResult{Status: "blocked"},
		Verification:  VerificationReport{Verdict: "strong"},
		AgentDecision: AgentDecision{Decision: "proceed"},
		MemoryHealth:  store.MemoryHealthResult{Status: "healthy"},
		Stats:         ComposeStats{ContextTokens: 240, ContextDroppedBlocks: 1},
	}
	report := EvaluateThinkResult(BrainEvalCase{
		Name:                    "retry",
		MinVerdict:              "partial",
		RequireDecision:         "proceed",
		RequireCitationRefs:     []string{"C1"},
		RequireAnswerText:       []string{"idempotent"},
		ForbidAnswerText:        []string{"always disable retries"},
		RequireAnchoredClaim:    true,
		RequireGapCodes:         []string{"stale_claims"},
		RequireConflict:         true,
		RequireHistorical:       true,
		RequireSynthesis:        "blocked",
		RequireEntityDossier:    "Retry Service",
		RequireEntityTrust:      "anchored",
		RequireMemoryHealth:     "healthy",
		MinCitations:            1,
		MinEvidenceAnchors:      1,
		MinEntityActiveClaims:   1,
		MaxContextTokens:        300,
		MinContextDroppedBlocks: 1,
	}, result)
	if !report.Passed || report.Score != 1 {
		t.Fatalf("report = %#v", report)
	}
}

func TestEvaluateThinkResultTreatsEmptySynthesisAsDisabled(t *testing.T) {
	report := EvaluateThinkResult(BrainEvalCase{RequireSynthesis: "disabled"}, ThinkResult{})
	if !report.Passed {
		t.Fatalf("report = %#v", report)
	}
}

func TestCanonicalBrainEvalPackCoversBrainGaps(t *testing.T) {
	raw, err := os.ReadFile("../../examples/evals/brain/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []BrainEvalCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, tc := range suite.Cases {
		if tc.RequireEntityDossier != "" || tc.RequireEntityTrust != "" || tc.MinEntityActiveClaims > 0 {
			covered["entity_dossier"] = true
		}
		if tc.RequireHistorical {
			covered["historical"] = true
		}
		if len(tc.RequireGapCodes) > 0 || len(tc.ForbidAnswerText) > 0 {
			covered["stale_gap"] = true
		}
		if tc.RequireConflict || strings.EqualFold(tc.RequireDecision, "blocked") {
			covered["conflict_blocking"] = true
		}
		if tc.RequireSynthesis != "" {
			covered["synthesis"] = true
		}
		if tc.RequireMemoryHealth != "" {
			covered["memory_health"] = true
		}
		if tc.MinCitations > 0 || tc.MinEvidenceAnchors > 0 || tc.RequireAnchoredClaim {
			covered["evidence"] = true
		}
		if len(tc.RequireAnswerText) > 0 || tc.RequireDecision != "" {
			covered["answer_gate"] = true
		}
		if tc.MaxContextTokens > 0 || tc.MinContextDroppedBlocks > 0 {
			covered["token_budget"] = true
		}
	}
	for _, want := range []string{"entity_dossier", "evidence", "answer_gate", "memory_health", "synthesis", "token_budget"} {
		if !covered[want] {
			t.Fatalf("canonical brain eval pack does not cover %s", want)
		}
	}
}

func TestBuildThinkResultSurfacesGovernanceGaps(t *testing.T) {
	packet := ComposeResult{
		Task:         "What changed?",
		Scope:        "repo:example",
		MemoryHealth: store.MemoryHealthResult{Status: "needs_review"},
		Verification: VerificationReport{
			Verdict: "weak",
			RetrievalCoverage: RetrievalCoverage{
				Complete: false,
				Missing:  []string{"facts", "evidence_sources"},
			},
			RetrievalQuality: RetrievalQuality{LowConfidence: true, LowSourceDiversity: true},
			UnverifiedClaims: []string{"claim-unverified"},
		},
		AgentDecision: AgentDecision{
			Decision:        "needs_review",
			ReviewRequired:  true,
			RequiredActions: []string{"add_evidence"},
		},
	}

	result := BuildThinkResult(packet)
	if !strings.Contains(result.Answer, "cannot answer this with source-backed memory") {
		t.Fatalf("answer should refuse unsupported certainty:\n%s", result.Answer)
	}
	codes := map[string]bool{}
	for _, gap := range result.Gaps {
		codes[gap.Code] = true
	}
	for _, want := range []string{"no_source_backed_facts", "coverage_facts", "coverage_evidence_sources", "low_confidence_retrieval", "low_source_diversity", "unverified_claims", "memory_health_needs_review"} {
		if !codes[want] {
			t.Fatalf("gap %q missing from %#v", want, result.Gaps)
		}
	}
	if result.AgentDecision.Decision != "needs_review" {
		t.Fatalf("decision = %#v", result.AgentDecision)
	}
}

func TestBuildThinkResultUsesSummaryOnlyContext(t *testing.T) {
	source := "file://README.md"
	packet := ComposeResult{
		Task:  "What is this repo?",
		Scope: "repo:example",
		Summaries: []store.MemorySummaryResult{{
			ID:         "summary-1",
			Title:      "README.md",
			Summary:    "Abra is an MCP-first governed brain layer for AI agents.",
			SourceURLs: []string{source},
		}},
		MemoryHealth: store.MemoryHealthResult{Status: "healthy"},
		Verification: VerificationReport{
			Verdict:           "strong",
			Score:             1,
			RetrievalCoverage: RetrievalCoverage{Complete: true},
		},
		AgentDecision: AgentDecision{Decision: "proceed", AutonomousAllowed: true},
		Stats:         ComposeStats{Summaries: 1},
	}

	result := BuildThinkResult(packet)
	if strings.Contains(result.Answer, "cannot answer this with source-backed memory") {
		t.Fatalf("summary-only answer should not refuse:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "README.md: Abra is an MCP-first governed brain layer") ||
		!strings.Contains(result.Answer, "[C1]") {
		t.Fatalf("summary answer missing content or citation:\n%s", result.Answer)
	}
	if len(result.Citations) != 1 || result.Citations[0].Kind != "summary" || result.Citations[0].SourceURL != source {
		t.Fatalf("citations = %#v", result.Citations)
	}
}

func TestBuildThinkResultPreservesComposeCitations(t *testing.T) {
	source := "file://docs/source.md"
	packet := ComposeResult{
		Task:  "What should be cited?",
		Scope: "repo:example",
		Facts: []store.ClaimResult{
			{ID: "claim-1", Claim: "Use the compose packet citation refs.", Scope: "repo:example", Status: "verified", Source: &source},
		},
		Citations: []Citation{
			{Ref: "C7", Kind: "claim", SourceURL: source, ClaimID: "claim-1", ClaimIDs: []string{"claim-1"}},
		},
		MemoryHealth: store.MemoryHealthResult{Status: "healthy"},
		Verification: VerificationReport{
			Verdict:           "strong",
			RetrievalCoverage: RetrievalCoverage{Complete: true},
		},
		AgentDecision: AgentDecision{Decision: "proceed", AutonomousAllowed: true},
	}

	result := BuildThinkResult(packet)
	if len(result.Citations) != 1 || result.Citations[0].Ref != "C7" {
		t.Fatalf("citations = %#v", result.Citations)
	}
	if !strings.Contains(result.Answer, "[C7]") {
		t.Fatalf("answer did not preserve compose citation ref:\n%s", result.Answer)
	}
}

func TestBuildThinkResultOmitsNonFreshFactsWhenFreshFactsExist(t *testing.T) {
	source := "file://example-policy.md"
	packet := ComposeResult{
		Task:  "What is the current retry process?",
		Scope: "repo:example",
		Facts: []store.ClaimResult{
			{
				ID:        "claim-stale",
				Claim:     "Legacy Operations Notebook is the retry source of truth.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "stale",
				Rank:      9.9,
			},
			{
				ID:        "claim-fresh",
				Claim:     "Retry now uses the live event replay workflow.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "fresh",
				Rank:      0.4,
			},
		},
		MemoryHealth: store.MemoryHealthResult{Status: "healthy"},
		Verification: VerificationReport{
			Verdict:           "partial",
			RetrievalCoverage: RetrievalCoverage{Complete: true},
			StaleClaims:       []string{"claim-stale"},
		},
		AgentDecision: AgentDecision{Decision: "caution", ReviewRequired: true},
	}

	result := BuildThinkResult(packet)
	if strings.Contains(result.Answer, "Legacy Operations Notebook") {
		t.Fatalf("answer included stale historical claim:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "Retry now uses the live event replay workflow") {
		t.Fatalf("answer missing fresh claim:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "non-fresh claim") {
		t.Fatalf("answer should disclose omitted non-fresh claims:\n%s", result.Answer)
	}
}

func TestBuildThinkResultDeepModeLabelsHistoricalFacts(t *testing.T) {
	source := "file://retry-policy.md"
	packet := ComposeResult{
		Task:          "What is the current retry process?",
		Scope:         "repo:example",
		RetrievalMode: RetrievalModeDeep,
		Facts: []store.ClaimResult{
			{
				ID:        "claim-stale",
				Claim:     "Legacy Ops Notebook used to be the retry source of truth.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "stale",
			},
			{
				ID:        "claim-fresh",
				Claim:     "Retry now uses the live event replay workflow.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "fresh",
			},
		},
		MemoryHealth:  store.MemoryHealthResult{Status: "healthy"},
		Verification:  VerificationReport{Verdict: "partial", RetrievalCoverage: RetrievalCoverage{Complete: true}},
		AgentDecision: AgentDecision{Decision: "caution", ReviewRequired: true},
	}

	result := BuildThinkResult(packet)
	if !strings.Contains(result.Answer, "Retry now uses the live event replay workflow") {
		t.Fatalf("answer missing fresh claim:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "Historical context (stale or superseded; do not use autonomously):") ||
		!strings.Contains(result.Answer, "Legacy Ops Notebook used to be the retry source of truth") {
		t.Fatalf("deep answer missing historical context:\n%s", result.Answer)
	}
	if result.RetrievalMode != RetrievalModeDeep {
		t.Fatalf("mode = %q", result.RetrievalMode)
	}
}
