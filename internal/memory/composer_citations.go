package memory

import (
	"sort"
	"strconv"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func risks(facts []store.ClaimResult, graph []store.RelationResult, conflicts []store.ConflictResult, retrievalWarnings []RetrievalWarning, graphWarnings []GraphWarning) []string {
	out := []string{}
	stale := 0
	challenged := 0
	unverified := 0
	for _, fact := range facts {
		if fact.Freshness == "stale" || fact.Freshness == "expired" {
			stale++
		}
		if fact.Status == "challenged" {
			challenged++
		}
		if fact.Status == "unverified" {
			unverified++
		}
	}
	if stale > 0 {
		out = append(out, "Some recalled facts are stale or expired; verify source freshness before acting.")
	}
	if challenged > 0 {
		out = append(out, "Some recalled facts are challenged; do not treat them as authoritative without source review.")
	}
	if unverified > 0 {
		out = append(out, "Unverified claims were included; use them only as leads, not proof.")
	}
	if len(conflicts) > 0 {
		out = append(out, "Active memory conflicts surfaced; treat contradictory claims or graph relations as unsafe until resolved.")
	}
	if len(retrievalWarnings) > 0 {
		out = append(out, "Some retrieval branches failed; treat the packet as degraded and rerun retrieval before autonomous work.")
	}
	if len(graphWarnings) > 0 {
		out = append(out, "Graph warnings surfaced competing or opposing relations; review graph evidence before autonomous work.")
	}
	if len(graph) == 0 {
		out = append(out, "No graph relations matched the task; cross-file impact may be underexplored.")
	}
	if len(out) == 0 {
		out = append(out, "No stale, challenged, or unverified memory surfaced in this packet.")
	}
	return out
}

func unavailableMemoryHealth(scope string, err error) store.MemoryHealthResult {
	message := "memory health could not be checked"
	if err != nil {
		message = "memory health could not be checked: " + compactError(err)
	}
	return store.MemoryHealthResult{
		Scope:   scope,
		Status:  "critical",
		Score:   0,
		Reasons: []string{message},
		Signals: []store.MemoryHealthSignal{
			{
				Code:        "memory_health_unavailable",
				Category:    "readiness",
				Severity:    "critical",
				Count:       1,
				ScoreImpact: 100,
				Message:     message,
				Action:      "check_memory_health_endpoint_and_storage",
			},
		},
	}
}

func applyMemoryHealthRisks(risks []string, health store.MemoryHealthResult) []string {
	status := strings.TrimSpace(health.Status)
	if status == "" || status == "healthy" {
		return risks
	}
	cleaned := risks[:0]
	for _, risk := range risks {
		if risk == "No stale, challenged, or unverified memory surfaced in this packet." {
			continue
		}
		cleaned = append(cleaned, risk)
	}
	for _, signal := range health.Signals {
		if signal.Severity == "critical" || signal.Severity == "warning" {
			cleaned = append(cleaned, "Memory health "+signal.Severity+" signal "+signal.Code+": "+signal.Action+".")
		}
	}
	if len(cleaned) == 0 {
		cleaned = append(cleaned, "Memory health is "+status+"; review health signals before autonomous work.")
	}
	return appendUnique(cleaned)
}

func buildCitations(packet ComposeResult) ([]Citation, map[string]string) {
	out := []Citation{}
	refs := map[string]string{}
	indexes := map[string]int{}
	add := func(kind, sourceURL, title, claimID, documentID, summaryID, relationID string) {
		sourceURL = strings.TrimSpace(sourceURL)
		if sourceURL == "" {
			return
		}
		idx, ok := indexes[sourceURL]
		if !ok {
			ref := "C" + strconv.Itoa(len(out)+1)
			refs[sourceURL] = ref
			out = append(out, Citation{
				Ref:       ref,
				Kind:      kind,
				SourceURL: sourceURL,
				Title:     strings.TrimSpace(title),
			})
			idx = len(out) - 1
			indexes[sourceURL] = idx
		}
		citation := out[idx]
		if citation.Kind == "" {
			citation.Kind = kind
		}
		if citation.Title == "" {
			citation.Title = strings.TrimSpace(title)
		}
		if citation.ClaimID == "" {
			citation.ClaimID = strings.TrimSpace(claimID)
		}
		if citation.DocumentID == "" {
			citation.DocumentID = strings.TrimSpace(documentID)
		}
		citation.ClaimIDs = appendUnique(citation.ClaimIDs, claimID)
		citation.DocumentIDs = appendUnique(citation.DocumentIDs, documentID)
		citation.SummaryIDs = appendUnique(citation.SummaryIDs, summaryID)
		citation.RelationIDs = appendUnique(citation.RelationIDs, relationID)
		out[idx] = citation
	}
	for _, fact := range packet.Facts {
		if fact.Source != nil {
			add("claim", *fact.Source, "", fact.ID, "", "", "")
		}
	}
	for _, doc := range packet.SupportingDocuments {
		add("document", doc.Source, doc.Title, "", doc.ID, "", "")
	}
	for _, summary := range packet.Summaries {
		for _, sourceURL := range summary.SourceURLs {
			add("summary", sourceURL, summary.Title, "", "", summary.ID, "")
		}
	}
	for _, relation := range packet.GraphContext {
		if relation.SourceURL != nil {
			add("graph_relation", *relation.SourceURL, "", "", "", "", relation.ID)
		}
	}
	return out, refs
}

func evidence(facts []store.ClaimResult, docs []store.DocumentResult, citationRefs map[string]string, anchors []EvidenceAnchor) []EvidenceItem {
	bySource := map[string]EvidenceItem{}
	for _, fact := range facts {
		if fact.Source == nil || strings.TrimSpace(*fact.Source) == "" {
			continue
		}
		item := bySource[*fact.Source]
		item.SourceURL = *fact.Source
		item.Ref = citationRefs[*fact.Source]
		item.Count++
		bySource[*fact.Source] = item
	}
	for _, doc := range docs {
		if strings.TrimSpace(doc.Source) == "" {
			continue
		}
		item := bySource[doc.Source]
		item.SourceURL = doc.Source
		item.Ref = citationRefs[doc.Source]
		item.Title = doc.Title
		item.Count++
		bySource[doc.Source] = item
	}
	for _, anchor := range anchors {
		if strings.TrimSpace(anchor.SourceURL) == "" {
			continue
		}
		item := bySource[anchor.SourceURL]
		item.SourceURL = anchor.SourceURL
		if item.Ref == "" {
			item.Ref = anchor.Ref
		}
		if item.Title == "" {
			item.Title = anchor.Title
		}
		item.Anchors++
		bySource[anchor.SourceURL] = item
	}
	out := make([]EvidenceItem, 0, len(bySource))
	for _, item := range bySource {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].SourceURL < out[j].SourceURL
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > 20 {
		return out[:20]
	}
	return out
}
