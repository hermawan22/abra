package memory

import (
	"context"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

const maxEvidenceAnchorQuoteRunes = 240

type storedEvidenceAnchorStore interface {
	EvidenceAnchorsForClaims(ctx context.Context, scope string, claimIDs []string) ([]store.EvidenceAnchorResult, error)
}

func evidenceAnchors(facts []store.ClaimResult, docs []store.DocumentResult, citationRefs map[string]string) []EvidenceAnchor {
	anchors := []EvidenceAnchor{}
	docsBySource := map[string][]store.DocumentResult{}
	for _, doc := range docs {
		source := canonicalSourceID(doc.Source)
		if source == "" {
			continue
		}
		docsBySource[source] = append(docsBySource[source], doc)
	}

	for _, fact := range facts {
		if fact.Source == nil || strings.TrimSpace(*fact.Source) == "" || strings.TrimSpace(fact.Claim) == "" {
			continue
		}
		source := canonicalSourceID(*fact.Source)
		if source == "" {
			continue
		}
		if anchor, ok := bestClaimAnchor(fact, docsBySource[source], citationRefs[*fact.Source]); ok {
			anchors = append(anchors, anchor)
		}
	}

	for _, doc := range docs {
		if strings.TrimSpace(doc.Source) == "" || strings.TrimSpace(doc.Content) == "" {
			continue
		}
		quote, start, end := leadingAnchorQuote(doc.Content)
		if quote == "" {
			continue
		}
		anchors = append(anchors, EvidenceAnchor{
			Ref:        citationRefs[doc.Source],
			Kind:       "document",
			SourceURL:  doc.Source,
			Title:      doc.Title,
			DocumentID: doc.ID,
			Quote:      quote,
			StartChar:  start,
			EndChar:    end,
			Score:      1,
		})
	}

	sort.SliceStable(anchors, func(i, j int) bool {
		if anchors[i].Ref == anchors[j].Ref {
			if anchors[i].Kind == anchors[j].Kind {
				return anchors[i].Quote < anchors[j].Quote
			}
			return anchors[i].Kind < anchors[j].Kind
		}
		return anchors[i].Ref < anchors[j].Ref
	})
	if len(anchors) > 40 {
		return anchors[:40]
	}
	return anchors
}

func (c *Composer) storedEvidenceAnchors(ctx context.Context, scope string, facts []store.ClaimResult, citationRefs map[string]string) ([]EvidenceAnchor, error) {
	anchorStore, ok := c.store.(storedEvidenceAnchorStore)
	if !ok {
		return nil, nil
	}
	claimByID := map[string]store.ClaimResult{}
	claimIDs := make([]string, 0, len(facts))
	for _, fact := range facts {
		if strings.TrimSpace(fact.ID) == "" || fact.Source == nil || strings.TrimSpace(*fact.Source) == "" {
			continue
		}
		claimByID[fact.ID] = fact
		claimIDs = append(claimIDs, fact.ID)
	}
	if len(claimIDs) == 0 {
		return nil, nil
	}
	results, err := anchorStore.EvidenceAnchorsForClaims(ctx, scope, claimIDs)
	if err != nil {
		return nil, err
	}
	anchors := make([]EvidenceAnchor, 0, len(results))
	for _, item := range results {
		quote := trimAnchorQuote(item.Quote)
		if quote == "" {
			continue
		}
		sourceURL := strings.TrimSpace(item.SourceURL)
		if sourceURL == "" {
			if fact, ok := claimByID[item.ClaimID]; ok && fact.Source != nil {
				sourceURL = strings.TrimSpace(*fact.Source)
			}
		}
		if sourceURL == "" {
			continue
		}
		anchors = append(anchors, EvidenceAnchor{
			Ref:        citationRefs[sourceURL],
			Kind:       "claim",
			SourceURL:  sourceURL,
			Title:      item.DocumentTitle,
			ClaimID:    item.ClaimID,
			DocumentID: item.DocumentID,
			Quote:      quote,
			StartChar:  item.StartChar,
			EndChar:    item.EndChar,
			Score:      1,
		})
	}
	return anchors, nil
}

func mergeEvidenceAnchors(primary, secondary []EvidenceAnchor) []EvidenceAnchor {
	if len(primary) == 0 {
		return capEvidenceAnchors(secondary)
	}
	if len(secondary) == 0 {
		return capEvidenceAnchors(primary)
	}
	merged := make([]EvidenceAnchor, 0, len(primary)+len(secondary))
	seen := map[string]struct{}{}
	add := func(anchor EvidenceAnchor) {
		if strings.TrimSpace(anchor.Quote) == "" || strings.TrimSpace(anchor.SourceURL) == "" {
			return
		}
		key := evidenceAnchorKey(anchor)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		merged = append(merged, anchor)
	}
	for _, anchor := range primary {
		add(anchor)
	}
	for _, anchor := range secondary {
		add(anchor)
	}
	return capEvidenceAnchors(merged)
}

func capEvidenceAnchors(anchors []EvidenceAnchor) []EvidenceAnchor {
	if len(anchors) > 60 {
		return anchors[:60]
	}
	return anchors
}

func evidenceAnchorKey(anchor EvidenceAnchor) string {
	return strings.Join([]string{
		anchor.Kind,
		anchor.ClaimID,
		anchor.DocumentID,
		canonicalSourceID(anchor.SourceURL),
		strings.ToLower(strings.TrimSpace(anchor.Quote)),
	}, "\x00")
}

func bestClaimAnchor(fact store.ClaimResult, docs []store.DocumentResult, ref string) (EvidenceAnchor, bool) {
	best := EvidenceAnchor{}
	bestScore := 0.0
	for _, doc := range docs {
		quote, start, end, score := anchorQuoteForClaim(fact.Claim, doc.Content)
		if quote == "" || score <= bestScore {
			continue
		}
		bestScore = score
		best = EvidenceAnchor{
			Ref:        ref,
			Kind:       "claim",
			SourceURL:  pointerString(fact.Source),
			Title:      doc.Title,
			ClaimID:    fact.ID,
			DocumentID: doc.ID,
			Quote:      quote,
			StartChar:  start,
			EndChar:    end,
			Score:      round2(score),
		}
	}
	return best, bestScore >= 0.65
}

func anchorQuoteForClaim(claim, content string) (string, int, int, float64) {
	claim = strings.TrimSpace(claim)
	content = strings.TrimSpace(content)
	if claim == "" || content == "" {
		return "", 0, 0, 0
	}
	lowerContent := strings.ToLower(content)
	lowerClaim := strings.ToLower(claim)
	if idx := strings.Index(lowerContent, lowerClaim); idx >= 0 {
		end := idx + len(claim)
		return trimAnchorQuote(content[idx:end]), idx, end, 1
	}

	claimTokens := evidenceTokens(normalizeEvidenceText(claim))
	if len(claimTokens) == 0 {
		return "", 0, 0, 0
	}
	bestQuote := ""
	bestStart := 0
	bestEnd := 0
	bestScore := 0.0
	for _, candidate := range anchorCandidates(content) {
		score := tokenOverlapScore(claimTokens, evidenceTokens(normalizeEvidenceText(candidate.Text)))
		if score > bestScore {
			bestQuote = candidate.Text
			bestStart = candidate.Start
			bestEnd = candidate.End
			bestScore = score
		}
	}
	if bestScore < 0.65 {
		return "", 0, 0, bestScore
	}
	return trimAnchorQuote(bestQuote), bestStart, bestEnd, bestScore
}

type anchorCandidate struct {
	Text  string
	Start int
	End   int
}

func anchorCandidates(content string) []anchorCandidate {
	out := []anchorCandidate{}
	start := 0
	for i, r := range content {
		if r != '\n' && r != '.' && r != ';' && r != ':' {
			continue
		}
		addAnchorCandidate(&out, content, start, i)
		start = i + len(string(r))
	}
	addAnchorCandidate(&out, content, start, len(content))
	return out
}

func addAnchorCandidate(out *[]anchorCandidate, content string, start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(content) {
		end = len(content)
	}
	text := strings.TrimSpace(content[start:end])
	if text == "" {
		return
	}
	*out = append(*out, anchorCandidate{Text: text, Start: start, End: end})
}

func tokenOverlapScore(needles, haystack []string) float64 {
	if len(needles) == 0 || len(haystack) == 0 {
		return 0
	}
	seen := map[string]struct{}{}
	for _, token := range haystack {
		seen[token] = struct{}{}
	}
	matched := 0
	for _, token := range needles {
		if _, ok := seen[token]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(needles))
}

func leadingAnchorQuote(content string) (string, int, int) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", 0, 0
	}
	end := minInt(len(content), maxEvidenceAnchorQuoteRunes)
	if end < len(content) {
		if cut := strings.LastIndexAny(content[:end], ".\n;"); cut > 40 {
			end = cut + 1
		}
	}
	return trimAnchorQuote(content[:end]), 0, end
}

func trimAnchorQuote(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len([]rune(value)) <= maxEvidenceAnchorQuoteRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxEvidenceAnchorQuoteRunes-1])) + "..."
}

func attachCitationAnchors(citations []Citation, anchors []EvidenceAnchor) []Citation {
	if len(citations) == 0 || len(anchors) == 0 {
		return citations
	}
	byRef := map[string][]EvidenceAnchor{}
	for _, anchor := range anchors {
		if strings.TrimSpace(anchor.Ref) == "" {
			continue
		}
		byRef[anchor.Ref] = append(byRef[anchor.Ref], anchor)
	}
	for i := range citations {
		citationAnchors := byRef[citations[i].Ref]
		if len(citationAnchors) > 5 {
			citationAnchors = citationAnchors[:5]
		}
		citations[i].Anchors = citationAnchors
	}
	return citations
}
