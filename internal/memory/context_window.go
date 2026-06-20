package memory

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type ContextWindow struct {
	Strategy        string                `json:"strategy"`
	MaxTokens       int                   `json:"max_tokens"`
	EstimatedTokens int                   `json:"estimated_tokens"`
	Blocks          []ContextBlock        `json:"blocks"`
	DroppedBlocks   []DroppedContextBlock `json:"dropped_blocks,omitempty"`
	Warnings        []string              `json:"warnings,omitempty"`
	Prompt          string                `json:"prompt"`
}

type ContextBlock struct {
	Type         string   `json:"type"`
	ID           string   `json:"id,omitempty"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	Tokens       int      `json:"tokens"`
	Priority     float64  `json:"priority"`
	SourceURLs   []string `json:"source_urls,omitempty"`
	CitationRefs []string `json:"citation_refs,omitempty"`
	ClaimIDs     []string `json:"claim_ids,omitempty"`
	RelationIDs  []string `json:"relation_ids,omitempty"`
}

type DroppedContextBlock struct {
	Type     string  `json:"type"`
	ID       string  `json:"id,omitempty"`
	Title    string  `json:"title"`
	Tokens   int     `json:"tokens"`
	Priority float64 `json:"priority"`
	Reason   string  `json:"reason"`
}

type contextCandidate struct {
	block ContextBlock
	order int
}

func buildContextWindow(input ComposeInput, result ComposeResult) ContextWindow {
	maxTokens := input.TokenBudget
	candidates := contextCandidates(input, result)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].block.Priority == candidates[j].block.Priority {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].block.Priority > candidates[j].block.Priority
	})

	window := ContextWindow{
		Strategy:  "priority-packed source context for bounded model windows",
		MaxTokens: maxTokens,
		Blocks:    []ContextBlock{},
	}
	for _, candidate := range candidates {
		block := normalizeContextBlock(candidate.block)
		if block.Tokens <= 0 {
			continue
		}
		remaining := maxTokens - window.EstimatedTokens
		if remaining <= 0 {
			window.DroppedBlocks = append(window.DroppedBlocks, droppedContextBlock(block, "token budget exhausted"))
			continue
		}
		if block.Tokens > remaining {
			if isCriticalContextBlock(block) && remaining >= 40 {
				block.Content = truncateForTokens(block.Content, remaining)
				block.Tokens = estimateTokens(block.Content)
			} else {
				window.DroppedBlocks = append(window.DroppedBlocks, droppedContextBlock(block, "does not fit remaining token budget"))
				continue
			}
		}
		window.Blocks = append(window.Blocks, block)
		window.EstimatedTokens += block.Tokens
	}
	window.Warnings = contextWarnings(window, result)
	window.Prompt = renderContextPrompt(window)
	return window
}

func contextCandidates(input ComposeInput, result ComposeResult) []contextCandidate {
	out := []contextCandidate{}
	order := 0
	citationRefs := citationRefMap(result.Citations)
	add := func(block ContextBlock) {
		block.CitationRefs = contextCitationRefs(block.SourceURLs, citationRefs)
		out = append(out, contextCandidate{block: block, order: order})
		order++
	}

	add(ContextBlock{
		Type:     "task",
		Title:    "Task and Memory Gate",
		Priority: 1,
		Content: fmt.Sprintf(
			"Task: %s\nScope: %s\nIntent: %s\nMemory health: %s (score %d; signals: %s)\nVerification: %s (score %.2f)\nRetrieval quality: results=%d sources=%d dominant_source_share=%.2f low_confidence=%t low_source_diversity=%t\nRequired actions: %s\nAgent decision: %s; autonomous_allowed=%t",
			result.Task,
			result.Scope,
			result.Intent,
			textOrDefault(result.MemoryHealth.Status, "unknown"),
			result.MemoryHealth.Score,
			healthSignalCodes(result.MemoryHealth.Signals),
			result.Verification.Verdict,
			result.Verification.Score,
			result.Verification.RetrievalQuality.ResultCount,
			result.Verification.RetrievalQuality.UniqueSources,
			result.Verification.RetrievalQuality.DominantSourceShare,
			result.Verification.RetrievalQuality.LowConfidence,
			result.Verification.RetrievalQuality.LowSourceDiversity,
			actionListOrDefault(result.Verification.RequiredActions),
			result.AgentDecision.Decision,
			result.AgentDecision.AutonomousAllowed,
		),
	})
	if len(result.Risks) > 0 {
		add(ContextBlock{
			Type:     "risk",
			Title:    "Risks",
			Priority: 0.99,
			Content:  bulletList(result.Risks, 6),
		})
	}
	if len(result.ValidationPlan) > 0 {
		add(ContextBlock{
			Type:     "validation",
			Title:    "Validation Plan",
			Priority: 0.98,
			Content:  validationContext(result.ValidationPlan),
		})
	}
	if len(result.RetrievalReasons) > 0 {
		add(ContextBlock{
			Type:     "retrieval",
			Title:    "Retrieval Reasons",
			Priority: 0.97,
			Content:  retrievalReasonsContext(result.RetrievalReasons),
		})
	}
	for _, summary := range result.Summaries {
		add(ContextBlock{
			Type:       "summary",
			ID:         summary.ID,
			Title:      summary.Level + ": " + summary.Title,
			Priority:   0.58 + minFloat(summary.Rank, 1)*0.14 + summaryLevelBoost(summary.Level),
			Content:    summary.Summary,
			SourceURLs: summary.SourceURLs,
		})
	}
	for _, fact := range result.Facts {
		add(ContextBlock{
			Type:       "fact",
			ID:         fact.ID,
			Title:      fact.Status + " claim",
			Priority:   factPriority(fact),
			Content:    factContext(fact),
			SourceURLs: compactList([]string{pointerString(fact.Source)}),
			ClaimIDs:   []string{fact.ID},
		})
	}
	for _, relation := range result.GraphContext {
		add(ContextBlock{
			Type:        "graph",
			ID:          relation.ID,
			Title:       relation.FromEntity + " -> " + relation.ToEntity,
			Priority:    0.48 + minFloat(relation.Confidence, 1)*0.16,
			Content:     relationContext(relation),
			SourceURLs:  compactList([]string{pointerString(relation.SourceURL)}),
			RelationIDs: compactList([]string{relation.ID}),
		})
	}
	for _, item := range result.ImpactMap {
		add(ContextBlock{
			Type:       "impact",
			Title:      item.Kind + ": " + item.Name,
			Priority:   0.44 + minFloat(item.Confidence, 1)*0.12,
			Content:    impactContext(item),
			SourceURLs: item.EvidenceSources,
		})
	}
	for _, doc := range result.SupportingDocuments {
		add(ContextBlock{
			Type:       "source",
			ID:         doc.ID,
			Title:      doc.Title,
			Priority:   0.38 + minFloat(doc.Rank, 1)*0.12,
			Content:    sourceContext(doc),
			SourceURLs: []string{doc.Source},
		})
	}
	_ = input
	return out
}

func healthSignalCodes(signals []store.MemoryHealthSignal) string {
	if len(signals) == 0 {
		return "none"
	}
	codes := make([]string, 0, minInt(len(signals), 5))
	for _, signal := range signals {
		if strings.TrimSpace(signal.Code) != "" {
			codes = append(codes, signal.Code)
		}
		if len(codes) >= 5 {
			break
		}
	}
	if len(codes) == 0 {
		return "none"
	}
	if len(signals) > len(codes) {
		codes = append(codes, fmt.Sprintf("+%d more", len(signals)-len(codes)))
	}
	return strings.Join(codes, ", ")
}

func actionListOrDefault(actions []string) string {
	if len(actions) == 0 {
		return "none"
	}
	limit := minInt(len(actions), 6)
	out := make([]string, 0, limit+1)
	for _, action := range actions[:limit] {
		if strings.TrimSpace(action) != "" {
			out = append(out, action)
		}
	}
	if len(out) == 0 {
		return "none"
	}
	if len(actions) > limit {
		out = append(out, fmt.Sprintf("+%d more", len(actions)-limit))
	}
	return strings.Join(out, ", ")
}

func textOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeContextBlock(block ContextBlock) ContextBlock {
	block.Type = strings.TrimSpace(block.Type)
	block.ID = strings.TrimSpace(block.ID)
	block.Title = strings.Join(strings.Fields(block.Title), " ")
	block.Content = strings.TrimSpace(block.Content)
	block.SourceURLs = compactList(block.SourceURLs)
	block.CitationRefs = compactList(block.CitationRefs)
	block.ClaimIDs = compactList(block.ClaimIDs)
	block.RelationIDs = compactList(block.RelationIDs)
	block.Priority = round2(block.Priority)
	block.Tokens = estimateTokens(block.Title + "\n" + block.Content)
	return block
}

func citationRefMap(citations []Citation) map[string]string {
	out := map[string]string{}
	for _, citation := range citations {
		sourceURL := strings.TrimSpace(citation.SourceURL)
		ref := strings.TrimSpace(citation.Ref)
		if sourceURL != "" && ref != "" {
			out[sourceURL] = ref
		}
	}
	return out
}

func contextCitationRefs(sourceURLs []string, citationRefs map[string]string) []string {
	out := []string{}
	for _, sourceURL := range sourceURLs {
		if ref := citationRefs[strings.TrimSpace(sourceURL)]; ref != "" {
			out = appendUnique(out, ref)
		}
	}
	return out
}

func isCriticalContextBlock(block ContextBlock) bool {
	switch block.Type {
	case "task", "risk", "validation":
		return true
	default:
		return false
	}
}

func droppedContextBlock(block ContextBlock, reason string) DroppedContextBlock {
	return DroppedContextBlock{
		Type:     block.Type,
		ID:       block.ID,
		Title:    block.Title,
		Tokens:   block.Tokens,
		Priority: block.Priority,
		Reason:   reason,
	}
}

func contextWarnings(window ContextWindow, result ComposeResult) []string {
	warnings := []string{}
	if len(window.Blocks) == 0 {
		warnings = append(warnings, "No context blocks fit the requested budget.")
	}
	if len(window.DroppedBlocks) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d lower-priority context block(s) were dropped to stay within budget.", len(window.DroppedBlocks)))
	}
	if result.Verification.ActionRequired {
		warnings = append(warnings, "Verification requires review before autonomous use.")
	}
	if !result.AgentDecision.AutonomousAllowed {
		warnings = append(warnings, "Agent decision does not allow autonomous action.")
	}
	return warnings
}

func renderContextPrompt(window ContextWindow) string {
	parts := []string{}
	for _, block := range window.Blocks {
		header := "[" + strings.ToUpper(block.Type) + "] " + block.Title
		if len(block.CitationRefs) > 0 {
			header += " [" + strings.Join(block.CitationRefs, ", ") + "]"
		}
		parts = append(parts, header+"\n"+block.Content)
	}
	return strings.Join(parts, "\n\n")
}

func summaryLevelBoost(level string) float64 {
	switch level {
	case "repo", "source":
		return 0.08
	case "module", "package":
		return 0.05
	case "file", "component", "route", "symbol":
		return 0.03
	default:
		return 0
	}
}

func factPriority(fact store.ClaimResult) float64 {
	score := 0.50 + minFloat(fact.Rank, 1)*0.16
	switch fact.Status {
	case "verified":
		score += 0.08
	case "inferred":
		score += 0.04
	case "unverified", "challenged", "expired":
		score -= 0.08
	}
	if pointerString(fact.Source) != "" {
		score += 0.04
	}
	return score
}

func factContext(fact store.ClaimResult) string {
	parts := []string{fact.Claim}
	if fact.Status != "" || fact.Freshness != "" {
		parts = append(parts, fmt.Sprintf("status=%s freshness=%s", fact.Status, fact.Freshness))
	}
	if source := pointerString(fact.Source); source != "" {
		parts = append(parts, "source="+source)
	}
	return strings.Join(parts, "\n")
}

func relationContext(relation store.RelationResult) string {
	parts := []string{
		fmt.Sprintf("%s --%s--> %s", relation.FromEntity, relation.Type, relation.ToEntity),
		fmt.Sprintf("confidence=%.2f", relation.Confidence),
	}
	if relation.ID != "" {
		parts = append(parts, "relation_id="+relation.ID)
	}
	if source := pointerString(relation.SourceURL); source != "" {
		parts = append(parts, "source="+source)
	}
	return strings.Join(parts, "\n")
}

func impactContext(item ImpactItem) string {
	parts := []string{
		fmt.Sprintf("confidence=%.2f relation_count=%d summary_count=%d fact_count=%d", item.Confidence, item.RelationCount, item.SummaryCount, item.FactCount),
	}
	if len(item.Reasons) > 0 {
		parts = append(parts, "reasons: "+strings.Join(item.Reasons, "; "))
	}
	return strings.Join(parts, "\n")
}

func sourceContext(doc store.DocumentResult) string {
	content := strings.Join(strings.Fields(doc.Content), " ")
	if estimateTokens(content) > 120 {
		content = truncateForTokens(content, 120)
	}
	return "source=" + doc.Source + "\n" + content
}

func retrievalReasonsContext(reasons []store.RetrievalReason) string {
	lines := []string{}
	for _, reason := range reasons {
		signal := strings.TrimSpace(reason.Signal)
		if signal == "" {
			signal = "retrieval"
		}
		mode := strings.TrimSpace(reason.Mode)
		if mode == "" {
			mode = "unknown"
		}
		message := strings.Join(strings.Fields(reason.Message), " ")
		if message == "" {
			message = "Retrieval contributed context for this packet."
		}
		count := ""
		if reason.Count > 0 {
			count = fmt.Sprintf(", n=%d", reason.Count)
		}
		lines = append(lines, fmt.Sprintf("%s (%s%s): %s", signal, mode, count, message))
	}
	return bulletList(lines, 6)
}

func validationContext(steps []ValidationStep) string {
	lines := []string{}
	for _, step := range steps {
		line := step.Name + " (" + step.Type + ")"
		if step.Command != "" {
			line += ": " + step.Command
		}
		line += " - " + step.Reason
		lines = append(lines, line)
		if len(lines) >= 6 {
			break
		}
	}
	return bulletList(lines, 6)
}

func bulletList(values []string, limit int) string {
	lines := []string{}
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			continue
		}
		lines = append(lines, "- "+value)
		if limit > 0 && len(lines) >= limit {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func estimateTokens(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	byChars := (len(text) + 3) / 4
	byWords := len(strings.Fields(text))
	if byChars > byWords {
		return byChars
	}
	return byWords
}

func truncateForTokens(text string, tokens int) string {
	text = strings.TrimSpace(text)
	if tokens <= 0 || text == "" {
		return ""
	}
	limit := tokens * 4
	if limit >= len(text) {
		return text
	}
	if limit < 12 {
		limit = 12
	}
	return strings.TrimSpace(text[:limit]) + "..."
}
