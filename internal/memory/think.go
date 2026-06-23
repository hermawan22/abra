package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type ThinkInput struct {
	Question          string                    `json:"question"`
	Scope             string                    `json:"scope"`
	Agent             string                    `json:"agent,omitempty"`
	Entity            string                    `json:"entity,omitempty"`
	Mode              RetrievalMode             `json:"mode,omitempty"`
	AsOf              string                    `json:"as_of,omitempty"`
	IncludeHistorical bool                      `json:"include_historical,omitempty"`
	Synthesize        bool                      `json:"synthesize,omitempty"`
	Limit             int                       `json:"limit,omitempty"`
	MaxQueries        int                       `json:"max_queries,omitempty"`
	TokenBudget       int                       `json:"token_budget,omitempty"`
	IncludeUnverified bool                      `json:"include_unverified,omitempty"`
	AgentProfile      *store.AgentProfileRecord `json:"-"`
}

type ThinkResult struct {
	Question              string                   `json:"question"`
	Scope                 string                   `json:"scope"`
	RetrievalMode         RetrievalMode            `json:"mode,omitempty"`
	Answer                string                   `json:"answer"`
	DeterministicAnswer   string                   `json:"deterministic_answer,omitempty"`
	Synthesis             SynthesisResult          `json:"synthesis,omitempty"`
	Citations             []Citation               `json:"citations"`
	EvidenceAnchors       []EvidenceAnchor         `json:"evidence_anchors,omitempty"`
	Gaps                  []ThinkGap               `json:"gaps"`
	GraphPaths            []ThinkGraphPath         `json:"graph_paths,omitempty"`
	EntityDossiers        []EntityDossier          `json:"entity_dossiers,omitempty"`
	TemporalContext       TemporalContext          `json:"temporal_context,omitempty"`
	WhyTrace              AnswerTrace              `json:"why_trace,omitempty"`
	Conflicts             []store.ConflictResult   `json:"conflicts,omitempty"`
	MemoryHealth          store.MemoryHealthResult `json:"memory_health"`
	Verification          VerificationReport       `json:"verification"`
	AgentDecision         AgentDecision            `json:"agent_decision"`
	NextActions           []string                 `json:"next_actions"`
	LearningSuggestions   []LearningSuggestion     `json:"learning_suggestions,omitempty"`
	RetrievalTrace        []RetrievalTraceItem     `json:"retrieval_trace"`
	RetrievalReasons      []store.RetrievalReason  `json:"retrieval_reasons,omitempty"`
	RetrievalWarnings     []RetrievalWarning       `json:"retrieval_warnings,omitempty"`
	GraphWarnings         []GraphWarning           `json:"graph_warnings,omitempty"`
	Stats                 ComposeStats             `json:"stats"`
	SupportingClaimIDs    []string                 `json:"supporting_claim_ids,omitempty"`
	SupportingDocumentIDs []string                 `json:"supporting_document_ids,omitempty"`
}

type ThinkCitation = Citation

type ThinkGap struct {
	Code            string `json:"code"`
	Severity        string `json:"severity"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

type ThinkGraphPath struct {
	RelationID  string  `json:"relation_id,omitempty"`
	From        string  `json:"from"`
	Type        string  `json:"type"`
	To          string  `json:"to"`
	Confidence  float64 `json:"confidence"`
	CitationRef string  `json:"citation_ref,omitempty"`
}

type AnswerTrace struct {
	TraceID          string                  `json:"trace_id"`
	RetrievalMode    RetrievalMode           `json:"mode,omitempty"`
	TemporalContext  TemporalContext         `json:"temporal_context,omitempty"`
	Claims           []AnswerTraceRef        `json:"claims,omitempty"`
	Documents        []AnswerTraceRef        `json:"documents,omitempty"`
	Relations        []AnswerTraceRef        `json:"relations,omitempty"`
	Anchors          []AnswerTraceRef        `json:"anchors,omitempty"`
	RetrievalReasons []store.RetrievalReason `json:"retrieval_reasons,omitempty"`
}

type AnswerTraceRef struct {
	ID          string  `json:"id,omitempty"`
	Kind        string  `json:"kind"`
	Ref         string  `json:"ref,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	Title       string  `json:"title,omitempty"`
	Summary     string  `json:"summary,omitempty"`
	Rank        float64 `json:"rank_score,omitempty"`
	Freshness   string  `json:"freshness,omitempty"`
	CitationRef string  `json:"citation_ref,omitempty"`
}

func (c *Composer) Think(ctx context.Context, input ThinkInput) (ThinkResult, error) {
	question := strings.TrimSpace(input.Question)
	scope := strings.TrimSpace(input.Scope)
	if question == "" || scope == "" {
		return ThinkResult{}, fmt.Errorf("question and scope are required")
	}
	packet, err := c.Compose(ctx, ComposeInput{
		Task:              question,
		Scope:             scope,
		Hook:              "before_task",
		Agent:             input.Agent,
		Entity:            input.Entity,
		Mode:              input.Mode,
		AsOf:              input.AsOf,
		IncludeHistorical: input.IncludeHistorical,
		Limit:             input.Limit,
		MaxQueries:        input.MaxQueries,
		TokenBudget:       input.TokenBudget,
		IncludeUnverified: input.IncludeUnverified,
		AgentProfile:      input.AgentProfile,
	})
	if err != nil {
		return ThinkResult{}, err
	}
	result := BuildThinkResult(packet)
	if input.Synthesize {
		result = c.synthesizeThink(ctx, input, result)
	}
	if err := c.persistThinkTrace(ctx, input, result); err != nil {
		return ThinkResult{}, err
	}
	return result, nil
}

type brainTraceStore interface {
	UpsertBrainTrace(ctx context.Context, record store.BrainTraceRecord) error
}

func (c *Composer) persistThinkTrace(ctx context.Context, input ThinkInput, result ThinkResult) error {
	persistor, ok := c.store.(brainTraceStore)
	if !ok || strings.TrimSpace(result.WhyTrace.TraceID) == "" {
		return nil
	}
	return persistor.UpsertBrainTrace(ctx, store.BrainTraceRecord{
		TraceID:  result.WhyTrace.TraceID,
		Scope:    result.Scope,
		Question: result.Question,
		Mode:     string(result.RetrievalMode),
		Answer:   result.Answer,
		Trace:    jsonMap(result.WhyTrace),
		Result:   jsonMap(result),
	})
}

func jsonMap(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}

func BuildThinkResult(packet ComposeResult) ThinkResult {
	citations := packet.Citations
	citationRefs := citationRefMap(citations)
	if len(citations) == 0 {
		citations, citationRefs = buildCitations(packet)
	}
	graphPaths := thinkGraphPaths(packet.GraphContext, citationRefs)
	result := ThinkResult{
		Question:              packet.Task,
		Scope:                 packet.Scope,
		RetrievalMode:         packet.RetrievalMode,
		Citations:             citations,
		EvidenceAnchors:       packet.EvidenceAnchors,
		Gaps:                  thinkGaps(packet),
		GraphPaths:            graphPaths,
		EntityDossiers:        packet.EntityDossiers,
		TemporalContext:       packet.TemporalContext,
		WhyTrace:              answerTrace(packet, citations),
		Conflicts:             packet.Conflicts,
		MemoryHealth:          packet.MemoryHealth,
		Verification:          packet.Verification,
		AgentDecision:         packet.AgentDecision,
		NextActions:           thinkNextActions(packet),
		LearningSuggestions:   packet.LearningSuggestions,
		RetrievalTrace:        packet.RetrievalTrace,
		RetrievalReasons:      packet.RetrievalReasons,
		RetrievalWarnings:     packet.RetrievalWarnings,
		GraphWarnings:         packet.GraphWarnings,
		Stats:                 packet.Stats,
		SupportingClaimIDs:    claimIDs(packet.Facts),
		SupportingDocumentIDs: documentIDs(packet.SupportingDocuments),
	}
	result.Answer = thinkAnswer(packet, citationRefs)
	result.DeterministicAnswer = result.Answer
	return result
}

func thinkAnswer(packet ComposeResult, citationRefs map[string]string) string {
	lines := []string{}
	if len(packet.Facts) == 0 && len(packet.SupportingDocuments) == 0 && len(packet.Summaries) == 0 && len(packet.GraphContext) == 0 {
		lines = append(lines, "Abra cannot answer this with source-backed memory yet.")
	} else {
		lines = append(lines, "Abra's governed answer:")
		lines = append(lines, thinkFactLines(packet, citationRefs)...)
		lines = append(lines, thinkFallbackLines(packet, citationRefs)...)
		lines = append(lines, thinkGraphLines(packet.GraphContext, citationRefs)...)
		lines = append(lines, thinkEntityDossierLines(packet.EntityDossiers)...)
	}
	lines = append(lines, thinkGateLines(packet)...)
	return strings.Join(lines, "\n")
}

func thinkFactLines(packet ComposeResult, citationRefs map[string]string) []string {
	lines := []string{}
	answerFacts, omittedNonFreshFacts := primaryThinkFacts(packet.Facts)
	for i, fact := range answerFacts {
		if i >= 8 {
			break
		}
		lines = append(lines, thinkFactLine(fact, citationRefs, false))
	}
	if packet.RetrievalMode == RetrievalModeDeep && omittedNonFreshFacts > 0 {
		lines = append(lines, thinkHistoricalFactLines(packet.Facts, citationRefs)...)
	}
	if omittedNonFreshFacts > 0 {
		lines = append(lines, fmt.Sprintf("- %d non-fresh claim(s) were omitted from the primary answer; inspect gaps before reuse.", omittedNonFreshFacts))
	}
	return lines
}

func thinkHistoricalFactLines(facts []store.ClaimResult, citationRefs map[string]string) []string {
	historical := nonFreshThinkFacts(facts)
	if len(historical) == 0 {
		return nil
	}
	lines := []string{"Historical context (stale or superseded; do not use autonomously):"}
	for i, fact := range historical {
		if i >= 5 {
			break
		}
		lines = append(lines, thinkFactLine(fact, citationRefs, true))
	}
	return lines
}

func thinkFactLine(fact store.ClaimResult, citationRefs map[string]string, includeFreshness bool) string {
	ref := ""
	if fact.Source != nil {
		ref = citationRefs[*fact.Source]
	}
	status := fact.Status
	if fact.Freshness != "" && (includeFreshness || fact.Freshness != "fresh") {
		status += ", " + fact.Freshness
	}
	return "- " + fact.Claim + formatThinkRef(ref) + " (" + status + ")."
}

func thinkFallbackLines(packet ComposeResult, citationRefs map[string]string) []string {
	if len(packet.Facts) == 0 && len(packet.SupportingDocuments) > 0 {
		return thinkSupportingDocumentLines(packet.SupportingDocuments, citationRefs)
	}
	if len(packet.Facts) == 0 && len(packet.SupportingDocuments) == 0 && len(packet.Summaries) > 0 {
		return thinkSummaryLines(packet.Summaries, citationRefs)
	}
	return nil
}

func thinkSupportingDocumentLines(docs []store.DocumentResult, citationRefs map[string]string) []string {
	lines := []string{}
	for i, doc := range docs {
		if i >= 3 {
			break
		}
		lines = append(lines, "- Supporting source chunk: "+doc.Title+formatThinkRef(citationRefs[doc.Source])+".")
	}
	return lines
}

func thinkSummaryLines(summaries []store.MemorySummaryResult, citationRefs map[string]string) []string {
	lines := []string{}
	for i, summary := range summaries {
		if i >= 3 {
			break
		}
		ref := ""
		if len(summary.SourceURLs) > 0 {
			ref = citationRefs[summary.SourceURLs[0]]
		}
		lines = append(lines, "- "+summary.Title+": "+summary.Summary+formatThinkRef(ref)+".")
	}
	return lines
}

func thinkGraphLines(relations []store.RelationResult, citationRefs map[string]string) []string {
	if len(relations) == 0 {
		return nil
	}
	lines := []string{"Graph context:"}
	for i, relation := range relations {
		if i >= 5 {
			break
		}
		ref := ""
		if relation.SourceURL != nil {
			ref = citationRefs[*relation.SourceURL]
		}
		lines = append(lines, fmt.Sprintf("- %s --%s--> %s%s.", relation.FromEntity, relation.Type, relation.ToEntity, formatThinkRef(ref)))
	}
	return lines
}

func thinkEntityDossierLines(dossiers []EntityDossier) []string {
	if len(dossiers) == 0 {
		return nil
	}
	lines := []string{"Entity dossier:"}
	for i, dossier := range dossiers {
		if i >= 2 {
			break
		}
		lines = append(lines, fmt.Sprintf("- %s: trust=%s active_claims=%d active_relations=%d anchors=%d next=%s.", dossier.Entity, dossier.Trust, dossier.Stats.ActiveClaims, dossier.Stats.ActiveRelations, dossier.Stats.Anchors, dossier.NextAction))
	}
	return lines
}

func thinkGateLines(packet ComposeResult) []string {
	lines := []string{}
	if packet.AgentDecision.Decision != "" {
		lines = append(lines, "Decision gate: "+packet.AgentDecision.Decision+".")
	}
	if packet.AgentDecision.ReviewRequired {
		lines = append(lines, "Review is required before autonomous use.")
	}
	if len(packet.Verification.Recommendations) > 0 {
		lines = append(lines, "Caveats: "+strings.Join(packet.Verification.Recommendations, "; ")+".")
	}
	return lines
}

func primaryThinkFacts(facts []store.ClaimResult) ([]store.ClaimResult, int) {
	hasFresh := false
	for _, fact := range facts {
		if strings.EqualFold(strings.TrimSpace(fact.Freshness), "fresh") {
			hasFresh = true
			break
		}
	}
	if !hasFresh {
		return facts, 0
	}
	out := make([]store.ClaimResult, 0, len(facts))
	omitted := 0
	for _, fact := range facts {
		switch strings.ToLower(strings.TrimSpace(fact.Freshness)) {
		case "fresh":
			out = append(out, fact)
		case "stale", "expired":
			omitted++
		default:
			omitted++
		}
	}
	return out, omitted
}

func nonFreshThinkFacts(facts []store.ClaimResult) []store.ClaimResult {
	out := make([]store.ClaimResult, 0, len(facts))
	for _, fact := range facts {
		if !strings.EqualFold(strings.TrimSpace(fact.Freshness), "fresh") {
			out = append(out, fact)
		}
	}
	return out
}

func formatThinkRef(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	return " [" + ref + "]"
}

func thinkGaps(packet ComposeResult) []ThinkGap {
	gaps := []ThinkGap{}
	add := func(code, severity, message, action string) {
		gaps = append(gaps, ThinkGap{Code: code, Severity: severity, Message: message, SuggestedAction: action})
	}
	if !hasUsableEvidence(packet) || (packet.Verification.RetrievalCoverage.Targets.Facts > 0 && len(packet.Facts) == 0) {
		add("no_source_backed_facts", "high", "No source-backed claims were retrieved for the question.", "ingest relevant sources or narrow the question")
	}
	for _, missing := range packet.Verification.RetrievalCoverage.Missing {
		add("coverage_"+missing, "medium", "The retrieval contract missed "+missing+".", "rerun with a narrower question or rebuild summaries/embeddings")
	}
	if packet.Verification.RetrievalQuality.LowConfidence {
		add("low_confidence_retrieval", "medium", "Retrieved evidence had low lexical/vector confidence.", "rerun with a more specific query")
	}
	if packet.Verification.RetrievalQuality.LowSourceDiversity {
		add("low_source_diversity", "medium", "Retrieved evidence is concentrated in one source.", "corroborate with another source before treating it as settled")
	}
	if len(packet.Verification.UnverifiedClaims) > 0 {
		add("unverified_claims", "medium", "Unverified claims are present.", "add evidence or request review before trusting them")
	}
	if len(packet.Verification.StaleClaims) > 0 {
		add("stale_claims", "medium", "Stale or expired claims are present.", "refresh sources or challenge stale claims")
	}
	if len(packet.Verification.ChallengedClaims) > 0 {
		add("challenged_claims", "high", "Challenged claims are present.", "resolve challenges before autonomous use")
	}
	if len(packet.Conflicts) > 0 {
		add("active_conflicts", "high", "Active claim or graph conflicts block safe use.", "resolve or suppress conflicts through conflict review")
	}
	if len(packet.GraphWarnings) > 0 {
		add("graph_warnings", "medium", "Graph context has competing or opposing relations.", "inspect graph warnings and related conflicts")
	}
	if packet.MemoryHealth.Status != "" && packet.MemoryHealth.Status != "healthy" {
		add("memory_health_"+packet.MemoryHealth.Status, "high", "Scoped memory health is "+packet.MemoryHealth.Status+".", "inspect memory health signals")
	}
	if len(packet.RetrievalWarnings) > 0 {
		add("retrieval_degraded", "medium", "One or more retrieval branches degraded.", "inspect retrieval_trace before trusting the answer")
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		return gapWeight(gaps[i].Severity) > gapWeight(gaps[j].Severity)
	})
	return gaps
}

func gapWeight(severity string) int {
	switch severity {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func thinkGraphPaths(relations []store.RelationResult, citationRefs map[string]string) []ThinkGraphPath {
	out := []ThinkGraphPath{}
	for _, relation := range relations {
		ref := ""
		if relation.SourceURL != nil {
			ref = citationRefs[*relation.SourceURL]
		}
		out = append(out, ThinkGraphPath{
			RelationID:  relation.ID,
			From:        relation.FromEntity,
			Type:        relation.Type,
			To:          relation.ToEntity,
			Confidence:  relation.Confidence,
			CitationRef: ref,
		})
	}
	return out
}

func answerTrace(packet ComposeResult, citations []Citation) AnswerTrace {
	citationRefs := citationRefMap(citations)
	trace := AnswerTrace{
		RetrievalMode:    packet.RetrievalMode,
		TemporalContext:  packet.TemporalContext,
		RetrievalReasons: append([]store.RetrievalReason(nil), packet.RetrievalReasons...),
	}
	hash := sha256.New()
	hash.Write([]byte(packet.Scope))
	hash.Write([]byte{0})
	hash.Write([]byte(packet.Task))
	for _, claim := range packet.Facts {
		ref := ""
		source := pointerString(claim.Source)
		if source != "" {
			ref = citationRefs[source]
		}
		trace.Claims = append(trace.Claims, AnswerTraceRef{
			ID:          claim.ID,
			Kind:        "claim",
			Ref:         ref,
			SourceURL:   source,
			Summary:     traceFirstLine(claim.Claim),
			Rank:        round2(claim.Rank),
			Freshness:   claim.Freshness,
			CitationRef: ref,
		})
		hash.Write([]byte(claim.ID))
	}
	for _, doc := range packet.SupportingDocuments {
		ref := citationRefs[doc.Source]
		trace.Documents = append(trace.Documents, AnswerTraceRef{
			ID:          doc.ID,
			Kind:        "document",
			Ref:         ref,
			SourceURL:   doc.Source,
			Title:       doc.Title,
			Summary:     traceFirstLine(doc.Content),
			Rank:        round2(doc.Rank),
			CitationRef: ref,
		})
		hash.Write([]byte(doc.ID))
	}
	for _, relation := range packet.GraphContext {
		source := pointerString(relation.SourceURL)
		ref := ""
		if source != "" {
			ref = citationRefs[source]
		}
		trace.Relations = append(trace.Relations, AnswerTraceRef{
			ID:          relation.ID,
			Kind:        "relation",
			Ref:         ref,
			SourceURL:   source,
			Summary:     relation.FromEntity + " " + relation.Type + " " + relation.ToEntity,
			Rank:        round2(relation.Confidence),
			CitationRef: ref,
		})
		hash.Write([]byte(relation.ID))
	}
	for _, anchor := range packet.EvidenceAnchors {
		trace.Anchors = append(trace.Anchors, AnswerTraceRef{
			ID:          traceFirstNonEmpty(anchor.ClaimID, anchor.DocumentID),
			Kind:        anchor.Kind + "_anchor",
			Ref:         anchor.Ref,
			SourceURL:   anchor.SourceURL,
			Title:       anchor.Title,
			Summary:     traceFirstLine(anchor.Quote),
			Rank:        round2(anchor.Score),
			CitationRef: anchor.Ref,
		})
		hash.Write([]byte(anchor.Ref + anchor.ClaimID + anchor.DocumentID + anchor.Quote))
	}
	trace.TraceID = "trace-" + hex.EncodeToString(hash.Sum(nil))[:16]
	return trace
}

func traceFirstLine(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, "\n"); ok {
		value = strings.TrimSpace(before)
	}
	if len([]rune(value)) > 160 {
		runes := []rune(value)
		value = strings.TrimSpace(string(runes[:159])) + "..."
	}
	return value
}

func traceFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func thinkNextActions(packet ComposeResult) []string {
	actions := []string{}
	actions = appendUnique(actions, packet.Verification.RequiredActions...)
	actions = appendUnique(actions, packet.AgentDecision.RequiredActions...)
	actions = appendUnique(actions, packet.AgentDecision.AllowedNextActions...)
	for _, recommendation := range packet.Verification.Recommendations {
		actions = appendUnique(actions, recommendation)
	}
	if len(packet.Verification.WeakEvidenceAnchors) > 0 {
		actions = appendUnique(actions, "propose text-span evidence anchors before synthesis")
	}
	if len(packet.LearningSuggestions) > 0 {
		actions = appendUnique(actions, "review learning_suggestions and persist only with --persist-learning or propose_learning")
	}
	if len(actions) == 0 {
		actions = append(actions, "cite_evidence")
	}
	return actions
}

func documentIDs(docs []store.DocumentResult) []string {
	out := []string{}
	for _, doc := range docs {
		if strings.TrimSpace(doc.ID) != "" {
			out = append(out, doc.ID)
		}
	}
	sort.Strings(out)
	return out
}
