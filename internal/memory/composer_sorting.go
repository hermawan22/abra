package memory

import (
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func sortClaims(in map[string]store.ClaimResult) []store.ClaimResult {
	out := make([]store.ClaimResult, 0, len(in))
	for _, claim := range in {
		out = append(out, claim)
	}
	sort.SliceStable(out, func(i, j int) bool {
		leftFreshness := freshnessPriority(out[i].Freshness)
		rightFreshness := freshnessPriority(out[j].Freshness)
		if leftFreshness != rightFreshness {
			return leftFreshness < rightFreshness
		}
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func claimPreferred(candidate, existing store.ClaimResult) bool {
	candidateFreshness := freshnessPriority(candidate.Freshness)
	existingFreshness := freshnessPriority(existing.Freshness)
	if candidateFreshness != existingFreshness {
		return candidateFreshness < existingFreshness
	}
	return candidate.Rank > existing.Rank
}

func freshnessPriority(freshness string) int {
	switch strings.ToLower(strings.TrimSpace(freshness)) {
	case "fresh":
		return 0
	case "", "unknown":
		return 1
	case "stale":
		return 2
	case "expired":
		return 3
	default:
		return 1
	}
}

func claimIDs(claims []store.ClaimResult) []string {
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		if strings.TrimSpace(claim.ID) != "" {
			out = append(out, claim.ID)
		}
	}
	return compactList(out)
}

func relationIDs(relations []store.RelationResult) []string {
	out := make([]string, 0, len(relations))
	for _, relation := range relations {
		if strings.TrimSpace(relation.ID) != "" {
			out = append(out, relation.ID)
		}
	}
	return compactList(out)
}

func sortDocuments(in map[string]store.DocumentResult) []store.DocumentResult {
	out := make([]store.DocumentResult, 0, len(in))
	for _, doc := range in {
		out = append(out, doc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func sortSummaries(in map[string]store.MemorySummaryResult) []store.MemorySummaryResult {
	out := make([]store.MemorySummaryResult, 0, len(in))
	for _, summary := range in {
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func sortRelations(in map[string]store.RelationResult) []store.RelationResult {
	out := make([]store.RelationResult, 0, len(in))
	for _, relation := range in {
		out = append(out, relation)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return relationKey(out[i]) < relationKey(out[j])
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

func mergeRetrievalReasons(target map[string]store.RetrievalReason, reasons []store.RetrievalReason) {
	for _, reason := range reasons {
		key := strings.ToLower(strings.TrimSpace(reason.Mode) + "\x00" + strings.TrimSpace(reason.Signal) + "\x00" + strings.TrimSpace(reason.Message))
		if key == "\x00\x00" {
			continue
		}
		if existing, ok := target[key]; ok {
			existing.Count += reason.Count
			target[key] = existing
			continue
		}
		target[key] = reason
	}
}

func sortRetrievalReasons(in map[string]store.RetrievalReason) []store.RetrievalReason {
	out := make([]store.RetrievalReason, 0, len(in))
	for _, reason := range in {
		out = append(out, reason)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			if out[i].Signal == out[j].Signal {
				return out[i].Mode < out[j].Mode
			}
			return out[i].Signal < out[j].Signal
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func relationKey(relation store.RelationResult) string {
	return strings.ToLower(relation.FromEntity + "\x00" + relation.Type + "\x00" + relation.ToEntity)
}

func graphExpansionSeeds(input ComposeInput, facts map[string]store.ClaimResult, docs map[string]store.DocumentResult, summaries map[string]store.MemorySummaryResult, graph map[string]store.RelationResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if value == "" || strings.EqualFold(value, input.Task) {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}

	for _, value := range append(input.Files, input.ChangedFiles...) {
		add(value)
	}
	for _, value := range relevantFiles(sortClaims(facts), sortDocuments(docs), nil, nil) {
		add(value)
	}
	for _, relation := range sortRelations(graph) {
		add(relation.FromEntity)
		add(relation.ToEntity)
	}
	for _, summary := range sortSummaries(summaries) {
		add(summary.Key)
		add(summary.Title)
	}
	if len(out) > 4 {
		return out[:4]
	}
	return out
}

func relevantFiles(facts []store.ClaimResult, docs []store.DocumentResult, files []string, changedFiles []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.Trim(value, " .,;:)]}\"'")
		if value == "" || strings.Contains(value, "://") || strings.Contains(value, "../") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
			return
		}
		if !looksLikeRepoPath(value) {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range append(files, changedFiles...) {
		add(value)
	}
	for _, claim := range facts {
		for _, match := range filePattern.FindAllStringSubmatch(claim.Claim, -1) {
			add(match[1])
		}
	}
	for _, doc := range docs {
		for _, match := range filePattern.FindAllStringSubmatch(doc.Source+" "+doc.Content, -1) {
			add(match[1])
		}
	}
	sort.Strings(out)
	if len(out) > 30 {
		return out[:30]
	}
	return out
}

func looksLikeRepoPath(value string) bool {
	if fileExtPattern.MatchString(value) {
		return true
	}
	for _, prefix := range []string{"src/", "internal/", "cmd/", "frontend/", "migrations/", "scripts/", "deploy/", "examples/", "docs/", "test/", "tests/"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
