package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type ThinkInput struct {
	Question          string                    `json:"question"`
	Scope             string                    `json:"scope"`
	Agent             string                    `json:"agent,omitempty"`
	Limit             int                       `json:"limit,omitempty"`
	MaxQueries        int                       `json:"max_queries,omitempty"`
	TokenBudget       int                       `json:"token_budget,omitempty"`
	IncludeUnverified bool                      `json:"include_unverified,omitempty"`
	AgentProfile      *store.AgentProfileRecord `json:"-"`
}

type ThinkResult struct {
	Question              string                   `json:"question"`
	Scope                 string                   `json:"scope"`
	Answer                string                   `json:"answer"`
	Citations             []ThinkCitation          `json:"citations"`
	Gaps                  []ThinkGap               `json:"gaps"`
	GraphPaths            []ThinkGraphPath         `json:"graph_paths,omitempty"`
	Conflicts             []store.ConflictResult   `json:"conflicts,omitempty"`
	MemoryHealth          store.MemoryHealthResult `json:"memory_health"`
	Verification          VerificationReport       `json:"verification"`
	AgentDecision         AgentDecision            `json:"agent_decision"`
	NextActions           []string                 `json:"next_actions"`
	RetrievalTrace        []RetrievalTraceItem     `json:"retrieval_trace"`
	RetrievalReasons      []store.RetrievalReason  `json:"retrieval_reasons,omitempty"`
	RetrievalWarnings     []RetrievalWarning       `json:"retrieval_warnings,omitempty"`
	GraphWarnings         []GraphWarning           `json:"graph_warnings,omitempty"`
	Stats                 ComposeStats             `json:"stats"`
	SupportingClaimIDs    []string                 `json:"supporting_claim_ids,omitempty"`
	SupportingDocumentIDs []string                 `json:"supporting_document_ids,omitempty"`
}

type ThinkCitation struct {
	Ref        string `json:"ref"`
	Kind       string `json:"kind"`
	SourceURL  string `json:"source_url"`
	Title      string `json:"title,omitempty"`
	ClaimID    string `json:"claim_id,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
}

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
		Limit:             input.Limit,
		MaxQueries:        input.MaxQueries,
		TokenBudget:       input.TokenBudget,
		IncludeUnverified: input.IncludeUnverified,
		AgentProfile:      input.AgentProfile,
	})
	if err != nil {
		return ThinkResult{}, err
	}
	return BuildThinkResult(packet), nil
}

func BuildThinkResult(packet ComposeResult) ThinkResult {
	citations, citationRefs := thinkCitations(packet)
	graphPaths := thinkGraphPaths(packet.GraphContext, citationRefs)
	result := ThinkResult{
		Question:              packet.Task,
		Scope:                 packet.Scope,
		Citations:             citations,
		Gaps:                  thinkGaps(packet),
		GraphPaths:            graphPaths,
		Conflicts:             packet.Conflicts,
		MemoryHealth:          packet.MemoryHealth,
		Verification:          packet.Verification,
		AgentDecision:         packet.AgentDecision,
		NextActions:           thinkNextActions(packet),
		RetrievalTrace:        packet.RetrievalTrace,
		RetrievalReasons:      packet.RetrievalReasons,
		RetrievalWarnings:     packet.RetrievalWarnings,
		GraphWarnings:         packet.GraphWarnings,
		Stats:                 packet.Stats,
		SupportingClaimIDs:    claimIDs(packet.Facts),
		SupportingDocumentIDs: documentIDs(packet.SupportingDocuments),
	}
	result.Answer = thinkAnswer(packet, citationRefs)
	return result
}

func thinkAnswer(packet ComposeResult, citationRefs map[string]string) string {
	lines := []string{}
	if len(packet.Facts) == 0 && len(packet.SupportingDocuments) == 0 && len(packet.GraphContext) == 0 {
		lines = append(lines, "Abra cannot answer this with source-backed memory yet.")
	} else {
		lines = append(lines, "Abra's governed answer:")
		for i, fact := range packet.Facts {
			if i >= 8 {
				break
			}
			ref := ""
			if fact.Source != nil {
				ref = citationRefs[*fact.Source]
			}
			status := fact.Status
			if fact.Freshness != "" && fact.Freshness != "fresh" {
				status += ", " + fact.Freshness
			}
			lines = append(lines, "- "+fact.Claim+formatThinkRef(ref)+" ("+status+").")
		}
		if len(packet.Facts) == 0 && len(packet.SupportingDocuments) > 0 {
			for i, doc := range packet.SupportingDocuments {
				if i >= 3 {
					break
				}
				lines = append(lines, "- Supporting source chunk: "+doc.Title+formatThinkRef(citationRefs[doc.Source])+".")
			}
		}
		if len(packet.GraphContext) > 0 {
			lines = append(lines, "Graph context:")
			for i, relation := range packet.GraphContext {
				if i >= 5 {
					break
				}
				ref := ""
				if relation.SourceURL != nil {
					ref = citationRefs[*relation.SourceURL]
				}
				lines = append(lines, fmt.Sprintf("- %s --%s--> %s%s.", relation.FromEntity, relation.Type, relation.ToEntity, formatThinkRef(ref)))
			}
		}
	}

	if packet.AgentDecision.Decision != "" {
		lines = append(lines, "Decision gate: "+packet.AgentDecision.Decision+".")
	}
	if packet.AgentDecision.ReviewRequired {
		lines = append(lines, "Review is required before autonomous use.")
	}
	if len(packet.Verification.Recommendations) > 0 {
		lines = append(lines, "Caveats: "+strings.Join(packet.Verification.Recommendations, "; ")+".")
	}
	return strings.Join(lines, "\n")
}

func formatThinkRef(ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	return " [" + ref + "]"
}

func thinkCitations(packet ComposeResult) ([]ThinkCitation, map[string]string) {
	out := []ThinkCitation{}
	refs := map[string]string{}
	add := func(kind, sourceURL, title, claimID, documentID string) {
		sourceURL = strings.TrimSpace(sourceURL)
		if sourceURL == "" {
			return
		}
		if _, ok := refs[sourceURL]; ok {
			return
		}
		ref := fmt.Sprintf("C%d", len(out)+1)
		refs[sourceURL] = ref
		out = append(out, ThinkCitation{
			Ref:        ref,
			Kind:       kind,
			SourceURL:  sourceURL,
			Title:      strings.TrimSpace(title),
			ClaimID:    strings.TrimSpace(claimID),
			DocumentID: strings.TrimSpace(documentID),
		})
	}
	for _, fact := range packet.Facts {
		if fact.Source != nil {
			add("claim", *fact.Source, "", fact.ID, "")
		}
	}
	for _, doc := range packet.SupportingDocuments {
		add("document", doc.Source, doc.Title, "", doc.ID)
	}
	for _, relation := range packet.GraphContext {
		if relation.SourceURL != nil {
			add("graph_relation", *relation.SourceURL, "", "", "")
		}
	}
	return out, refs
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

func thinkNextActions(packet ComposeResult) []string {
	actions := []string{}
	actions = appendUnique(actions, packet.AgentDecision.RequiredActions...)
	actions = appendUnique(actions, packet.AgentDecision.AllowedNextActions...)
	for _, recommendation := range packet.Verification.Recommendations {
		actions = appendUnique(actions, recommendation)
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
