package memory

import (
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type impactAccumulator struct {
	kind      string
	name      string
	score     float64
	reasons   map[string]struct{}
	sources   map[string]struct{}
	relations int
	summaries int
	facts     int
}

func impactMap(input ComposeInput, summaries []store.MemorySummaryResult, facts []store.ClaimResult, docs []store.DocumentResult, graph []store.RelationResult, relevant []string) []ImpactItem {
	items := map[string]*impactAccumulator{}
	add := func(kind, name, reason string, score float64, sources []string) {
		kind = strings.TrimSpace(kind)
		name = strings.TrimSpace(name)
		reason = strings.TrimSpace(reason)
		if kind == "" || name == "" {
			return
		}
		key := strings.ToLower(kind + "\x00" + name)
		item := items[key]
		if item == nil {
			item = &impactAccumulator{
				kind:    kind,
				name:    name,
				reasons: map[string]struct{}{},
				sources: map[string]struct{}{},
			}
			items[key] = item
		}
		item.score += score
		if reason != "" {
			item.reasons[reason] = struct{}{}
		}
		for _, source := range sources {
			source = strings.TrimSpace(source)
			if source != "" {
				item.sources[source] = struct{}{}
			}
		}
	}
	incRelation := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.relations++
		}
	}
	incSummary := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.summaries++
		}
	}
	incFact := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.facts++
		}
	}

	for _, file := range append(input.Files, input.ChangedFiles...) {
		add("file", file, "provided as task file context", 0.45, nil)
	}
	for _, file := range relevant {
		add("file", file, "mentioned by recalled facts or supporting documents", 0.35, sourcesForName(file, facts, docs))
		incFact("file", file)
	}
	for _, summary := range summaries {
		kind := impactKind(summary.Level, summary.Key)
		score := 0.25 + minFloat(summary.Rank, 1)*0.35
		add(kind, summary.Key, "matched hierarchical summary", score, summary.SourceURLs)
		incSummary(kind, summary.Key)
	}
	for _, relation := range graph {
		source := pointerString(relation.SourceURL)
		fromKind := impactKind("", relation.FromEntity)
		toKind := impactKind("", relation.ToEntity)
		reason := "connected by graph relation " + relation.Type
		score := 0.3 + minFloat(relation.Confidence, 1)*0.45
		add(fromKind, relation.FromEntity, reason, score, []string{source})
		add(toKind, relation.ToEntity, reason, score, []string{source})
		incRelation(fromKind, relation.FromEntity)
		incRelation(toKind, relation.ToEntity)
	}
	for _, fact := range facts {
		source := pointerString(fact.Source)
		for _, match := range filePattern.FindAllStringSubmatch(fact.Claim, -1) {
			if len(match) > 1 && looksLikeRepoPath(match[1]) {
				add("file", match[1], "mentioned by source-backed fact", 0.35, []string{source})
				incFact("file", match[1])
			}
		}
	}

	out := make([]ImpactItem, 0, len(items))
	for _, item := range items {
		out = append(out, ImpactItem{
			Kind:            item.kind,
			Name:            item.name,
			Confidence:      impactConfidence(item),
			Reasons:         sortedSet(item.reasons, 4),
			EvidenceSources: sortedSet(item.sources, 5),
			RelationCount:   item.relations,
			SummaryCount:    item.summaries,
			FactCount:       item.facts,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			if out[i].Kind == out[j].Kind {
				return out[i].Name < out[j].Name
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].Confidence > out[j].Confidence
	})
	if len(out) > 20 {
		return out[:20]
	}
	return out
}

func impactKind(level, name string) string {
	level = strings.TrimSpace(level)
	switch level {
	case "file", "repo", "module", "route", "component", "symbol", "package", "source", "decision":
		return level
	}
	name = strings.TrimSpace(name)
	switch {
	case strings.HasPrefix(name, "/"):
		return "route"
	case looksLikeImpactFile(name):
		return "file"
	case strings.Contains(name, "/") && !strings.Contains(name, "."):
		return "module"
	default:
		return "entity"
	}
}

func looksLikeImpactFile(name string) bool {
	if !looksLikeRepoPath(name) {
		return false
	}
	if strings.Contains(name, "/") {
		return true
	}
	for _, exact := range []string{"package.json", "go.mod", "go.sum", "Dockerfile"} {
		if name == exact {
			return true
		}
	}
	return false
}

func impactConfidence(item *impactAccumulator) float64 {
	score := item.score
	score += float64(item.relations) * 0.08
	score += float64(item.summaries) * 0.07
	score += float64(item.facts) * 0.06
	if len(item.sources) > 0 {
		score += 0.08
	}
	return round2(minFloat(score, 1))
}

func sourcesForName(name string, facts []store.ClaimResult, docs []store.DocumentResult) []string {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return nil
	}
	sources := map[string]struct{}{}
	for _, fact := range facts {
		if strings.Contains(strings.ToLower(fact.Claim), needle) {
			if source := pointerString(fact.Source); source != "" {
				sources[source] = struct{}{}
			}
		}
	}
	for _, doc := range docs {
		if strings.Contains(strings.ToLower(doc.Source+" "+doc.Content), needle) && strings.TrimSpace(doc.Source) != "" {
			sources[doc.Source] = struct{}{}
		}
	}
	return sortedSet(sources, 5)
}

func sortedSet(values map[string]struct{}, limit int) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
