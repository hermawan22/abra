package memory

import (
	"math"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type VerificationReport struct {
	Verdict               string                     `json:"verdict"`
	Score                 float64                    `json:"score"`
	ActionRequired        bool                       `json:"action_required"`
	Checks                []VerificationCheck        `json:"checks"`
	RetrievalCoverage     RetrievalCoverage          `json:"retrieval_coverage"`
	RetrievalQuality      RetrievalQuality           `json:"retrieval_quality"`
	EvidenceSources       int                        `json:"evidence_sources"`
	ClaimCoverage         float64                    `json:"claim_coverage"`
	VerifiedClaims        int                        `json:"verified_claims"`
	UnverifiedClaims      []string                   `json:"unverified_claims,omitempty"`
	StaleClaims           []string                   `json:"stale_claims,omitempty"`
	ChallengedClaims      []string                   `json:"challenged_claims,omitempty"`
	ConflictClaims        []string                   `json:"conflict_claims,omitempty"`
	ActiveConflicts       []store.ConflictResult     `json:"active_conflicts,omitempty"`
	RetrievalWarnings     []RetrievalWarning         `json:"retrieval_warnings,omitempty"`
	GraphWarnings         []GraphWarning             `json:"graph_warnings,omitempty"`
	MemoryHealthStatus    string                     `json:"memory_health_status,omitempty"`
	MemoryHealthSignals   []store.MemoryHealthSignal `json:"memory_health_signals,omitempty"`
	MissingEvidenceClaims []string                   `json:"missing_evidence_claims,omitempty"`
	RequiredActions       []string                   `json:"required_actions,omitempty"`
	Recommendations       []string                   `json:"recommendations"`
}

type VerificationCheck struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Score   float64 `json:"score"`
	Message string  `json:"message"`
}

type RetrievalQuality struct {
	ResultCount         int     `json:"result_count"`
	TopRankScore        float64 `json:"top_rank_score"`
	AverageRankScore    float64 `json:"average_rank_score"`
	TopTextScore        float64 `json:"top_text_score"`
	TopVectorScore      float64 `json:"top_vector_score"`
	LexicalHits         int     `json:"lexical_hits"`
	VectorHits          int     `json:"vector_hits"`
	ZeroScoreResults    int     `json:"zero_score_results"`
	UniqueSources       int     `json:"unique_sources"`
	DominantSourceShare float64 `json:"dominant_source_share"`
	LowConfidence       bool    `json:"low_confidence"`
	LowSourceDiversity  bool    `json:"low_source_diversity"`
}

type RetrievalCoverage struct {
	Targets  RetrievalCoverageTarget `json:"targets"`
	Actual   RetrievalCoverageTarget `json:"actual"`
	Complete bool                    `json:"complete"`
	Missing  []string                `json:"missing,omitempty"`
}

func verifyPacket(summaries []store.MemorySummaryResult, facts []store.ClaimResult, docs []store.DocumentResult, graph []store.RelationResult, evidence []EvidenceItem, plan RetrievalPlan, conflicts []store.ConflictResult, retrievalWarnings []RetrievalWarning, graphWarnings []GraphWarning, healthInput ...store.MemoryHealthResult) VerificationReport {
	health := verificationMemoryHealth(healthInput)
	report := VerificationReport{
		Checks:              []VerificationCheck{},
		RetrievalCoverage:   retrievalCoverage(summaries, facts, docs, graph, evidence, plan.CoverageTargets),
		RetrievalQuality:    retrievalQuality(facts, docs),
		EvidenceSources:     len(evidence),
		Recommendations:     []string{},
		ActiveConflicts:     conflicts,
		RetrievalWarnings:   retrievalWarnings,
		GraphWarnings:       graphWarnings,
		MemoryHealthStatus:  strings.TrimSpace(health.Status),
		MemoryHealthSignals: append([]store.MemoryHealthSignal(nil), health.Signals...),
	}
	sourceSet := map[string]struct{}{}
	for _, item := range evidence {
		if strings.TrimSpace(item.SourceURL) != "" {
			sourceSet[item.SourceURL] = struct{}{}
		}
	}
	for _, doc := range docs {
		if strings.TrimSpace(doc.Source) != "" {
			sourceSet[doc.Source] = struct{}{}
		}
	}
	report.EvidenceSources = len(sourceSet)

	sourced := 0
	for _, fact := range facts {
		if fact.Status == "verified" || fact.Status == "inferred" {
			report.VerifiedClaims++
		}
		if fact.Source == nil || strings.TrimSpace(*fact.Source) == "" {
			report.MissingEvidenceClaims = append(report.MissingEvidenceClaims, fact.ID)
		} else {
			sourced++
		}
		switch fact.Status {
		case "unverified":
			report.UnverifiedClaims = append(report.UnverifiedClaims, fact.ID)
		case "challenged":
			report.ChallengedClaims = append(report.ChallengedClaims, fact.ID)
		}
		if fact.Freshness == "stale" || fact.Freshness == "expired" {
			report.StaleClaims = append(report.StaleClaims, fact.ID)
		}
	}
	if len(facts) > 0 {
		report.ClaimCoverage = round2(float64(sourced) / float64(len(facts)))
	}

	report.Checks = append(report.Checks,
		claimCoverageCheck(report.ClaimCoverage, len(facts), plan.CoverageTargets.Facts),
		countTargetCheck("summaries", len(summaries), plan.CoverageTargets.Summaries, "hierarchical summaries were retrieved"),
		countTargetCheck("supporting_documents", len(docs), plan.CoverageTargets.SupportingDocuments, "supporting source chunks were retrieved"),
		countTargetCheck("evidence_sources", report.EvidenceSources, plan.CoverageTargets.EvidenceSources, "distinct evidence sources are available"),
		countTargetCheck("graph_context", len(graph), plan.CoverageTargets.GraphRelations, "graph context is available for impact exploration"),
		retrievalCoverageCheck(report.RetrievalCoverage),
		retrievalQualityCheck(report.RetrievalQuality),
		retrievalSourceDiversityCheck(report.RetrievalQuality),
		memoryHealthCheck(health),
	)
	report.Checks = append(report.Checks, unsafeSignalCheck("unverified_claims", len(report.UnverifiedClaims), "unverified claims are present"))
	report.Checks = append(report.Checks, unsafeSignalCheck("stale_claims", len(report.StaleClaims), "stale or expired claims are present"))
	report.Checks = append(report.Checks, unsafeSignalCheck("challenged_claims", len(report.ChallengedClaims), "challenged claims are present"))
	report.ConflictClaims = conflictClaimIDs(conflicts)
	report.Checks = append(report.Checks, unsafeSignalCheck("active_conflicts", len(report.ActiveConflicts), "active memory conflicts are present"))
	report.Checks = append(report.Checks, retrievalCompletenessCheck(len(report.RetrievalWarnings)))
	report.Checks = append(report.Checks, graphConsistencyCheck(len(report.GraphWarnings)))
	report.Checks = append(report.Checks, unsafeSignalCheck("missing_evidence", len(report.MissingEvidenceClaims), "claims without source URLs are present"))

	score := 0.0
	for _, check := range report.Checks {
		score += check.Score
	}
	if len(report.Checks) > 0 {
		score = score / float64(len(report.Checks))
	}
	report.Score = round2(score)
	report.ActionRequired = len(report.ActiveConflicts) > 0 || len(report.ChallengedClaims) > 0 || len(report.StaleClaims) > 0 || len(report.RetrievalWarnings) > 0 || len(report.GraphWarnings) > 0 || len(report.MissingEvidenceClaims) > 0 || report.RetrievalQuality.LowConfidence || report.RetrievalQuality.LowSourceDiversity || !report.RetrievalCoverage.Complete || memoryHealthActionRequired(report.MemoryHealthStatus)
	report.Verdict = verificationVerdict(report)
	report.RequiredActions = verificationRequiredActions(report, len(facts), len(graph))
	report.Recommendations = verificationRecommendations(report, len(facts), len(graph))

	sort.Strings(report.UnverifiedClaims)
	sort.Strings(report.StaleClaims)
	sort.Strings(report.ChallengedClaims)
	sort.Strings(report.ConflictClaims)
	sort.Strings(report.MissingEvidenceClaims)
	return report
}

func retrievalCoverage(summaries []store.MemorySummaryResult, facts []store.ClaimResult, docs []store.DocumentResult, graph []store.RelationResult, evidence []EvidenceItem, targets RetrievalCoverageTarget) RetrievalCoverage {
	coverage := RetrievalCoverage{
		Targets: targets,
		Actual: RetrievalCoverageTarget{
			Summaries:           len(summaries),
			Facts:               len(facts),
			SupportingDocuments: len(docs),
			GraphRelations:      len(graph),
			EvidenceSources:     len(evidence),
		},
		Complete: true,
	}
	check := func(name string, actual, target int) {
		if actual >= target {
			return
		}
		coverage.Complete = false
		coverage.Missing = append(coverage.Missing, name)
	}
	check("summaries", coverage.Actual.Summaries, targets.Summaries)
	check("facts", coverage.Actual.Facts, targets.Facts)
	check("supporting_documents", coverage.Actual.SupportingDocuments, targets.SupportingDocuments)
	check("graph_relations", coverage.Actual.GraphRelations, targets.GraphRelations)
	check("evidence_sources", coverage.Actual.EvidenceSources, targets.EvidenceSources)
	sort.Strings(coverage.Missing)
	return coverage
}

func retrievalQuality(facts []store.ClaimResult, docs []store.DocumentResult) RetrievalQuality {
	quality := RetrievalQuality{ResultCount: len(facts) + len(docs)}
	totalRank := 0.0
	sourceCounts := map[string]int{}
	observe := func(rank, text, vector float64, source string) {
		totalRank += rank
		quality.TopRankScore = maxFloat(quality.TopRankScore, rank)
		quality.TopTextScore = maxFloat(quality.TopTextScore, text)
		quality.TopVectorScore = maxFloat(quality.TopVectorScore, vector)
		if text > 0.01 {
			quality.LexicalHits++
		}
		if vector > 0.01 {
			quality.VectorHits++
		}
		if rank <= 0 && text <= 0 && vector <= 0 {
			quality.ZeroScoreResults++
		}
		source = strings.TrimSpace(source)
		if source != "" {
			sourceCounts[source]++
		}
	}
	for _, fact := range facts {
		observe(fact.Rank, fact.TextScore, fact.VectorScore, pointerString(fact.Source))
	}
	for _, doc := range docs {
		observe(doc.Rank, doc.TextScore, doc.VectorScore, doc.Source)
	}
	if quality.ResultCount > 0 {
		quality.AverageRankScore = round2(totalRank / float64(quality.ResultCount))
	}
	maxSourceCount := 0
	for _, count := range sourceCounts {
		maxSourceCount = maxInt(maxSourceCount, count)
	}
	quality.UniqueSources = len(sourceCounts)
	if quality.ResultCount > 0 && maxSourceCount > 0 {
		quality.DominantSourceShare = round2(float64(maxSourceCount) / float64(quality.ResultCount))
	}
	quality.TopRankScore = round2(quality.TopRankScore)
	quality.TopTextScore = round2(quality.TopTextScore)
	quality.TopVectorScore = round2(quality.TopVectorScore)
	noLexicalSemanticSignal := quality.TopTextScore < 0.1 && quality.TopVectorScore < 0.1
	weakRankSignal := quality.TopRankScore < 0.35
	boostedWithoutRawSignal := quality.TopTextScore == 0 && quality.TopVectorScore == 0 && quality.TopRankScore >= 1
	quality.LowConfidence = quality.ResultCount > 0 && noLexicalSemanticSignal && (weakRankSignal || boostedWithoutRawSignal)
	quality.LowSourceDiversity = quality.ResultCount >= 4 && quality.UniqueSources <= 1
	return quality
}

func retrievalCoverageCheck(coverage RetrievalCoverage) VerificationCheck {
	if coverage.Complete {
		return VerificationCheck{Name: "retrieval_coverage", Status: "pass", Score: 1, Message: "retrieval met the intent-specific coverage contract"}
	}
	ratio := coverageRatio(coverage)
	score := round2(0.15 + ratio*0.55)
	return VerificationCheck{Name: "retrieval_coverage", Status: "review", Score: score, Message: "retrieval missed required coverage: " + strings.Join(coverage.Missing, ", ")}
}

func coverageRatio(coverage RetrievalCoverage) float64 {
	target := coverage.Targets.Summaries + coverage.Targets.Facts + coverage.Targets.SupportingDocuments + coverage.Targets.GraphRelations + coverage.Targets.EvidenceSources
	if target == 0 {
		return 1
	}
	actual := minInt(coverage.Actual.Summaries, coverage.Targets.Summaries) +
		minInt(coverage.Actual.Facts, coverage.Targets.Facts) +
		minInt(coverage.Actual.SupportingDocuments, coverage.Targets.SupportingDocuments) +
		minInt(coverage.Actual.GraphRelations, coverage.Targets.GraphRelations) +
		minInt(coverage.Actual.EvidenceSources, coverage.Targets.EvidenceSources)
	return float64(actual) / float64(target)
}

func retrievalQualityCheck(quality RetrievalQuality) VerificationCheck {
	if quality.ResultCount == 0 {
		return VerificationCheck{Name: "retrieval_quality", Status: "missing", Score: 0, Message: "no retrieval results were available for ranking quality"}
	}
	if quality.LowConfidence {
		return VerificationCheck{Name: "retrieval_quality", Status: "review", Score: 0.2, Message: "retrieval results have very low lexical and semantic relevance signal"}
	}
	if quality.TopTextScore > 0.01 && quality.TopVectorScore > 0.01 {
		return VerificationCheck{Name: "retrieval_quality", Status: "pass", Score: 1, Message: "retrieval includes lexical and semantic ranking signal"}
	}
	if quality.TopRankScore >= 0.35 {
		return VerificationCheck{Name: "retrieval_quality", Status: "pass", Score: 0.9, Message: "retrieval ranking signal is strong enough for verification"}
	}
	return VerificationCheck{Name: "retrieval_quality", Status: "partial", Score: 0.65, Message: "retrieval ranking signal is present but narrow"}
}

func retrievalSourceDiversityCheck(quality RetrievalQuality) VerificationCheck {
	if quality.ResultCount == 0 {
		return VerificationCheck{Name: "retrieval_source_diversity", Status: "missing", Score: 0.35, Message: "no retrieval results were available for source diversity"}
	}
	if quality.UniqueSources >= 2 {
		return VerificationCheck{Name: "retrieval_source_diversity", Status: "pass", Score: 1, Message: "retrieval includes corroborating source diversity"}
	}
	if quality.LowSourceDiversity {
		return VerificationCheck{Name: "retrieval_source_diversity", Status: "review", Score: 0.45, Message: "retrieval is concentrated in one source despite several results"}
	}
	return VerificationCheck{Name: "retrieval_source_diversity", Status: "partial", Score: 0.75, Message: "retrieval has one source; acceptable for narrow packets but not corroborated"}
}

func claimCoverageCheck(coverage float64, total int, target int) VerificationCheck {
	if total == 0 && target == 0 {
		return VerificationCheck{Name: "source_coverage", Status: "pass", Score: 1, Message: "no claims required by the retrieval contract"}
	}
	return coverageCheck("source_coverage", coverage, total, "claims include source URLs")
}

func retrievalCompletenessCheck(warnings int) VerificationCheck {
	if warnings == 0 {
		return VerificationCheck{Name: "retrieval_completeness", Status: "pass", Score: 1, Message: "all retrieval branches completed"}
	}
	return VerificationCheck{Name: "retrieval_completeness", Status: "review", Score: 0.3, Message: "some retrieval branches failed and the packet is degraded"}
}

func graphConsistencyCheck(warnings int) VerificationCheck {
	if warnings == 0 {
		return VerificationCheck{Name: "graph_consistency", Status: "pass", Score: 1, Message: "no graph contradictions detected"}
	}
	return VerificationCheck{Name: "graph_consistency", Status: "review", Score: 0.25, Message: "graph context contains competing or opposing relations"}
}

func memoryHealthCheck(health store.MemoryHealthResult) VerificationCheck {
	status := strings.TrimSpace(health.Status)
	switch status {
	case "", "healthy":
		return VerificationCheck{Name: "memory_health", Status: "pass", Score: 1, Message: "scoped memory health is healthy"}
	case "needs_review":
		return VerificationCheck{Name: "memory_health", Status: "review", Score: 0.45, Message: "scoped memory health needs review"}
	case "critical":
		return VerificationCheck{Name: "memory_health", Status: "fail", Score: 0.1, Message: "scoped memory health is critical"}
	default:
		return VerificationCheck{Name: "memory_health", Status: "review", Score: 0.35, Message: "scoped memory health status is " + status}
	}
}

func verificationMemoryHealth(values []store.MemoryHealthResult) store.MemoryHealthResult {
	if len(values) == 0 {
		return store.MemoryHealthResult{Status: "healthy", Score: 100}
	}
	return values[0]
}

func memoryHealthActionRequired(status string) bool {
	status = strings.TrimSpace(status)
	return status != "" && status != "healthy"
}

func coverageCheck(name string, coverage float64, total int, message string) VerificationCheck {
	if total == 0 {
		return VerificationCheck{Name: name, Status: "missing", Score: 0, Message: "no claims were retrieved"}
	}
	switch {
	case coverage >= 0.95:
		return VerificationCheck{Name: name, Status: "pass", Score: 1, Message: message}
	case coverage >= 0.7:
		return VerificationCheck{Name: name, Status: "partial", Score: 0.65, Message: message}
	default:
		return VerificationCheck{Name: name, Status: "fail", Score: 0.2, Message: message}
	}
}

func countCheck(name string, count int, message string) VerificationCheck {
	if count > 0 {
		return VerificationCheck{Name: name, Status: "pass", Score: 1, Message: message}
	}
	return VerificationCheck{Name: name, Status: "missing", Score: 0.35, Message: message}
}

func countTargetCheck(name string, count int, target int, message string) VerificationCheck {
	if target <= 0 {
		return VerificationCheck{Name: name, Status: "pass", Score: 1, Message: "not required by the retrieval contract"}
	}
	if count >= target {
		return VerificationCheck{Name: name, Status: "pass", Score: 1, Message: message}
	}
	if count > 0 {
		return VerificationCheck{Name: name, Status: "partial", Score: 0.65, Message: message}
	}
	return VerificationCheck{Name: name, Status: "missing", Score: 0.25, Message: message}
}

func unsafeSignalCheck(name string, count int, message string) VerificationCheck {
	if count == 0 {
		return VerificationCheck{Name: name, Status: "pass", Score: 1, Message: "no " + strings.ReplaceAll(name, "_", " ")}
	}
	return VerificationCheck{Name: name, Status: "review", Score: 0.25, Message: message}
}

func conflictClaimIDs(conflicts []store.ConflictResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, conflict := range conflicts {
		add(conflict.PrimaryClaimID)
		add(conflict.ConflictingClaimID)
	}
	return out
}

func verificationVerdict(report VerificationReport) string {
	switch {
	case len(report.ActiveConflicts) > 0:
		return "unsafe"
	case report.MemoryHealthStatus == "critical":
		return "unsafe"
	case len(report.ChallengedClaims) > 0 || len(report.MissingEvidenceClaims) > 0:
		return "unsafe"
	case len(report.RetrievalWarnings) > 0:
		return "partial"
	case memoryHealthActionRequired(report.MemoryHealthStatus):
		return "partial"
	case len(report.GraphWarnings) > 0:
		return "partial"
	case !report.RetrievalCoverage.Complete:
		return "weak"
	case report.RetrievalQuality.LowConfidence:
		return "weak"
	case report.RetrievalQuality.LowSourceDiversity:
		return "partial"
	case len(report.StaleClaims) > 0 || len(report.UnverifiedClaims) > 0:
		return "partial"
	case report.Score >= 0.85 && (report.ClaimCoverage >= 0.95 || report.RetrievalCoverage.Targets.Facts == 0) && report.EvidenceSources > 0:
		return "strong"
	case report.Score >= 0.6:
		return "partial"
	default:
		return "weak"
	}
}

func verificationRequiredActions(report VerificationReport, facts, graph int) []string {
	actions := []string{}
	if facts == 0 {
		if report.RetrievalCoverage.Targets.Facts > 0 {
			actions = appendUnique(actions, "ingest_source_backed_memory", "rerun_working_memory_compose")
		} else {
			actions = appendUnique(actions, "cite_source_chunks_and_graph")
		}
	}
	if !report.RetrievalCoverage.Complete {
		actions = appendUnique(actions, "fill_missing_retrieval_layers")
		for _, layer := range report.RetrievalCoverage.Missing {
			actions = appendUnique(actions, "retrieve_"+safeActionName(layer))
		}
	}
	if len(report.MissingEvidenceClaims) > 0 {
		actions = appendUnique(actions, "attach_missing_evidence")
	}
	if len(report.ChallengedClaims) > 0 {
		actions = appendUnique(actions, "resolve_challenged_claims")
	}
	if len(report.ActiveConflicts) > 0 {
		actions = appendUnique(actions, "resolve_active_conflicts", "review_conflict_evidence")
	}
	if len(report.RetrievalWarnings) > 0 {
		actions = appendUnique(actions, "rerun_degraded_retrieval")
	}
	if report.RetrievalQuality.LowConfidence {
		actions = appendUnique(actions, "rerun_with_more_specific_query", "check_embeddings_or_reindex")
	}
	if report.RetrievalQuality.LowSourceDiversity {
		actions = appendUnique(actions, "corroborate_with_additional_source")
	}
	if len(report.GraphWarnings) > 0 {
		actions = appendUnique(actions, "review_graph_warnings")
	}
	if memoryHealthActionRequired(report.MemoryHealthStatus) {
		actions = appendUnique(actions, healthRequiredActions(store.MemoryHealthResult{
			Status:  report.MemoryHealthStatus,
			Signals: report.MemoryHealthSignals,
		})...)
	}
	if len(report.StaleClaims) > 0 {
		actions = appendUnique(actions, "refresh_stale_sources")
	}
	if len(report.UnverifiedClaims) > 0 {
		actions = appendUnique(actions, "verify_unverified_claims")
	}
	if graph == 0 && report.RetrievalCoverage.Targets.GraphRelations > 0 {
		actions = appendUnique(actions, "expand_graph_context")
	}
	if len(actions) == 0 {
		actions = append(actions, "cite_evidence")
	}
	return actions
}

func safeActionName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func verificationRecommendations(report VerificationReport, facts, graph int) []string {
	out := []string{}
	if facts == 0 {
		if report.RetrievalCoverage.Targets.Facts > 0 {
			out = append(out, "Do not answer as fact; ingest or recall source-backed memory first.")
		} else {
			out = append(out, "This packet has no claim facts by design; cite source chunks, summaries, and graph context instead.")
		}
	}
	if !report.RetrievalCoverage.Complete {
		out = append(out, "Rerun retrieval with broader task context or ingest missing layers before autonomous work: "+strings.Join(report.RetrievalCoverage.Missing, ", ")+".")
	}
	if len(report.MissingEvidenceClaims) > 0 {
		out = append(out, "Resolve claims without source URLs before treating them as trusted memory.")
	}
	if len(report.ChallengedClaims) > 0 {
		out = append(out, "Review challenged claims and their source evidence before acting.")
	}
	if len(report.ActiveConflicts) > 0 {
		out = append(out, "Resolve active memory conflicts before using contradictory claims or graph relations for autonomous work.")
	}
	if len(report.RetrievalWarnings) > 0 {
		out = append(out, "Rerun degraded retrieval branches before autonomous work; use current results only as partial context.")
	}
	if report.RetrievalQuality.LowConfidence {
		out = append(out, "Rerun retrieval with a more specific query or rebuild embeddings before using low-signal results.")
	}
	if report.RetrievalQuality.LowSourceDiversity {
		out = append(out, "Corroborate this packet with another source before treating one-source retrieval as settled.")
	}
	if len(report.GraphWarnings) > 0 {
		out = append(out, "Review graph warnings before treating dependency or tool choices as settled.")
	}
	if memoryHealthActionRequired(report.MemoryHealthStatus) {
		out = append(out, "Inspect memory health signals before treating this packet as safe for autonomous work.")
	}
	if len(report.StaleClaims) > 0 {
		out = append(out, "Refresh stale or expired sources before using the affected claims.")
	}
	if len(report.UnverifiedClaims) > 0 {
		out = append(out, "Use unverified claims only as leads unless a source-backed claim confirms them.")
	}
	if graph == 0 {
		out = append(out, "Expand retrieval through files or entities if cross-file impact matters.")
	}
	if len(out) == 0 {
		out = append(out, "Memory packet is source-backed; cite the evidence sources when answering or changing code.")
	}
	return out
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
