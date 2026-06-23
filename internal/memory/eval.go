package memory

import (
	"strconv"
	"strings"
)

type BrainEvalCase struct {
	Name                    string   `json:"name"`
	Question                string   `json:"question"`
	Scope                   string   `json:"scope"`
	MinVerdict              string   `json:"min_verdict,omitempty"`
	RequireDecision         string   `json:"require_decision,omitempty"`
	RequireCitationRefs     []string `json:"require_citation_refs,omitempty"`
	RequireAnswerText       []string `json:"require_answer_text,omitempty"`
	ForbidAnswerText        []string `json:"forbid_answer_text,omitempty"`
	RequireAnchoredClaim    bool     `json:"require_anchored_claim,omitempty"`
	RequireGapCodes         []string `json:"require_gap_codes,omitempty"`
	RequireConflict         bool     `json:"require_conflict,omitempty"`
	RequireHistorical       bool     `json:"require_historical,omitempty"`
	RequireSynthesis        string   `json:"require_synthesis,omitempty"`
	RequireEntityDossier    string   `json:"require_entity_dossier,omitempty"`
	RequireEntityTrust      string   `json:"require_entity_trust,omitempty"`
	RequireMemoryHealth     string   `json:"require_memory_health,omitempty"`
	MinCitations            int      `json:"min_citations,omitempty"`
	MinEvidenceAnchors      int      `json:"min_evidence_anchors,omitempty"`
	MinEntityActiveClaims   int      `json:"min_entity_active_claims,omitempty"`
	MaxContextTokens        int      `json:"max_context_tokens,omitempty"`
	MinContextDroppedBlocks int      `json:"min_context_dropped_blocks,omitempty"`
}

type BrainEvalReport struct {
	Name    string           `json:"name"`
	Passed  bool             `json:"passed"`
	Score   float64          `json:"score"`
	Checks  []BrainEvalCheck `json:"checks"`
	Details map[string]any   `json:"details,omitempty"`
}

type BrainEvalCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

func EvaluateThinkResult(tc BrainEvalCase, result ThinkResult) BrainEvalReport {
	report := BrainEvalReport{Name: strings.TrimSpace(tc.Name), Passed: true, Details: map[string]any{}}
	if report.Name == "" {
		report.Name = strings.TrimSpace(tc.Question)
	}
	add := func(name string, passed bool, message string) {
		report.Checks = append(report.Checks, BrainEvalCheck{Name: name, Passed: passed, Message: message})
		if !passed {
			report.Passed = false
		}
	}
	if tc.MinVerdict != "" {
		add("min_verdict", verdictAtLeast(result.Verification.Verdict, tc.MinVerdict), "got "+result.Verification.Verdict+", want at least "+tc.MinVerdict)
	}
	if tc.RequireDecision != "" {
		add("agent_decision", strings.EqualFold(result.AgentDecision.Decision, tc.RequireDecision), "got "+result.AgentDecision.Decision+", want "+tc.RequireDecision)
	}
	answer := strings.ToLower(result.Answer)
	for _, required := range tc.RequireAnswerText {
		add("answer_contains", strings.Contains(answer, strings.ToLower(strings.TrimSpace(required))), "missing "+required)
	}
	for _, forbidden := range tc.ForbidAnswerText {
		add("answer_forbids", !strings.Contains(answer, strings.ToLower(strings.TrimSpace(forbidden))), "forbidden "+forbidden)
	}
	refs := map[string]struct{}{}
	for _, citation := range result.Citations {
		refs[strings.TrimSpace(citation.Ref)] = struct{}{}
	}
	for _, required := range tc.RequireCitationRefs {
		_, ok := refs[strings.TrimSpace(required)]
		add("citation_ref", ok, "missing "+required)
	}
	if tc.RequireAnchoredClaim {
		add("anchored_claim", hasClaimAnchor(result.EvidenceAnchors), "missing claim evidence anchor")
	}
	gapCodes := map[string]struct{}{}
	for _, gap := range result.Gaps {
		gapCodes[strings.TrimSpace(gap.Code)] = struct{}{}
	}
	for _, required := range tc.RequireGapCodes {
		_, ok := gapCodes[strings.TrimSpace(required)]
		add("gap_code", ok, "missing "+required)
	}
	if tc.RequireConflict {
		add("conflict", len(result.Conflicts) > 0 || strings.EqualFold(result.AgentDecision.Decision, "blocked"), "missing conflict or blocked decision")
	}
	if tc.RequireHistorical {
		add("historical_context", result.TemporalContext.HistoricalIncluded || strings.Contains(answer, "historical context"), "missing historical context")
	}
	if tc.RequireSynthesis != "" {
		got := synthesisStatus(result.Synthesis.Status)
		add("synthesis_status", strings.EqualFold(got, tc.RequireSynthesis), "got "+got+", want "+tc.RequireSynthesis)
	}
	if tc.RequireMemoryHealth != "" {
		add("memory_health", strings.EqualFold(result.MemoryHealth.Status, tc.RequireMemoryHealth), "got "+result.MemoryHealth.Status+", want "+tc.RequireMemoryHealth)
	}
	if tc.MinCitations > 0 {
		add("min_citations", len(result.Citations) >= tc.MinCitations, "got "+strconv.Itoa(len(result.Citations))+", want at least "+strconv.Itoa(tc.MinCitations))
	}
	if tc.MinEvidenceAnchors > 0 {
		add("min_evidence_anchors", len(result.EvidenceAnchors) >= tc.MinEvidenceAnchors, "got "+strconv.Itoa(len(result.EvidenceAnchors))+", want at least "+strconv.Itoa(tc.MinEvidenceAnchors))
	}
	var dossier EntityDossier
	if tc.RequireEntityDossier != "" || tc.RequireEntityTrust != "" || tc.MinEntityActiveClaims > 0 {
		dossier = matchingEntityDossier(result.EntityDossiers, tc.RequireEntityDossier)
	}
	if tc.RequireEntityDossier != "" {
		add("entity_dossier", dossier.Entity != "", "missing "+tc.RequireEntityDossier)
	}
	if tc.RequireEntityTrust != "" {
		add("entity_trust", strings.EqualFold(dossier.Trust, tc.RequireEntityTrust), "got "+dossier.Trust+", want "+tc.RequireEntityTrust)
	}
	if tc.MinEntityActiveClaims > 0 {
		add("entity_active_claims", dossier.Stats.ActiveClaims >= tc.MinEntityActiveClaims, "got "+strconv.Itoa(dossier.Stats.ActiveClaims)+", want at least "+strconv.Itoa(tc.MinEntityActiveClaims))
	}
	if tc.MaxContextTokens > 0 {
		add("context_tokens", result.Stats.ContextTokens <= tc.MaxContextTokens, "got "+strconv.Itoa(result.Stats.ContextTokens)+", want at most "+strconv.Itoa(tc.MaxContextTokens))
	}
	if tc.MinContextDroppedBlocks > 0 {
		add("context_dropped_blocks", result.Stats.ContextDroppedBlocks >= tc.MinContextDroppedBlocks, "got "+strconv.Itoa(result.Stats.ContextDroppedBlocks)+", want at least "+strconv.Itoa(tc.MinContextDroppedBlocks))
	}
	passed := 0
	for _, check := range report.Checks {
		if check.Passed {
			passed++
		}
	}
	if len(report.Checks) == 0 {
		report.Score = 1
	} else {
		report.Score = round2(float64(passed) / float64(len(report.Checks)))
	}
	report.Details["verdict"] = result.Verification.Verdict
	report.Details["decision"] = result.AgentDecision.Decision
	report.Details["citations"] = len(result.Citations)
	report.Details["anchors"] = len(result.EvidenceAnchors)
	report.Details["gaps"] = len(result.Gaps)
	report.Details["conflicts"] = len(result.Conflicts)
	report.Details["synthesis_status"] = synthesisStatus(result.Synthesis.Status)
	report.Details["entity_dossiers"] = len(result.EntityDossiers)
	report.Details["context_tokens"] = result.Stats.ContextTokens
	report.Details["context_dropped_blocks"] = result.Stats.ContextDroppedBlocks
	return report
}

func hasClaimAnchor(anchors []EvidenceAnchor) bool {
	for _, anchor := range anchors {
		if anchor.Kind == "claim" && strings.TrimSpace(anchor.Quote) != "" {
			return true
		}
	}
	return false
}

func verdictAtLeast(got, want string) bool {
	order := map[string]int{
		"":        0,
		"unsafe":  0,
		"weak":    1,
		"partial": 2,
		"strong":  3,
	}
	return order[strings.ToLower(strings.TrimSpace(got))] >= order[strings.ToLower(strings.TrimSpace(want))]
}

func synthesisStatus(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "disabled"
	}
	return value
}

func matchingEntityDossier(dossiers []EntityDossier, entity string) EntityDossier {
	entity = strings.ToLower(strings.TrimSpace(entity))
	if entity == "" {
		if len(dossiers) > 0 {
			return dossiers[0]
		}
		return EntityDossier{}
	}
	for _, dossier := range dossiers {
		if strings.EqualFold(dossier.Entity, entity) || strings.EqualFold(dossier.EntityKey, entity) {
			return dossier
		}
		for _, alias := range dossier.Aliases {
			if strings.EqualFold(alias, entity) {
				return dossier
			}
		}
	}
	return EntityDossier{}
}
