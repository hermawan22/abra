package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/store"
)

const maxEntityDossiers = 3

type TemporalContext struct {
	EffectiveAt              string   `json:"effective_at,omitempty"`
	AsOf                     string   `json:"as_of,omitempty"`
	AsOfAppliedToRecall      bool     `json:"as_of_applied_to_recall"`
	IncludeHistorical        bool     `json:"include_historical"`
	HistoricalIncluded       bool     `json:"historical_included"`
	DefaultHidesHistorical   bool     `json:"default_hides_historical"`
	CurrentFilter            string   `json:"current_filter,omitempty"`
	HistoricalUseRequirement string   `json:"historical_use_requirement,omitempty"`
	Warnings                 []string `json:"warnings,omitempty"`
}

type EntityDossier struct {
	Entity              string                  `json:"entity"`
	EntityKey           string                  `json:"entity_key"`
	Aliases             []string                `json:"aliases,omitempty"`
	Trust               string                  `json:"trust"`
	NextAction          string                  `json:"next_action,omitempty"`
	ActiveClaims        []EntityDossierClaim    `json:"active_claims,omitempty"`
	HistoricalClaims    []EntityDossierClaim    `json:"historical_claims,omitempty"`
	ActiveRelations     []EntityDossierRelation `json:"active_relations,omitempty"`
	HistoricalRelations []EntityDossierRelation `json:"historical_relations,omitempty"`
	EvidenceAnchors     []EvidenceAnchor        `json:"evidence_anchors,omitempty"`
	Conflicts           []store.ConflictResult  `json:"conflicts,omitempty"`
	RetrievalReasons    []store.RetrievalReason `json:"retrieval_reasons,omitempty"`
	Stats               EntityDossierStats      `json:"stats"`
}

type EntityDossierClaim struct {
	ID          string  `json:"id"`
	Claim       string  `json:"claim_text"`
	Status      string  `json:"status"`
	Freshness   string  `json:"freshness,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	Rank        float64 `json:"rank_score,omitempty"`
	CitationRef string  `json:"citation_ref,omitempty"`
}

type EntityDossierRelation struct {
	ID          string  `json:"id,omitempty"`
	From        string  `json:"from_entity"`
	Type        string  `json:"relation_type"`
	To          string  `json:"to_entity"`
	Confidence  float64 `json:"confidence,omitempty"`
	SourceURL   string  `json:"source_url,omitempty"`
	CitationRef string  `json:"citation_ref,omitempty"`
}

type EntityDossierStats struct {
	Claims              int `json:"claims"`
	ActiveClaims        int `json:"active_claims"`
	HistoricalClaims    int `json:"historical_claims"`
	Relations           int `json:"relations"`
	ActiveRelations     int `json:"active_relations"`
	HistoricalRelations int `json:"historical_relations"`
	Anchors             int `json:"anchors"`
	Conflicts           int `json:"conflicts"`
}

type entityResolverStore interface {
	ResolveEntity(ctx context.Context, scope, name string) (store.EntityResolutionResult, error)
}

type EntityDossierInput struct {
	Entity            string                    `json:"entity"`
	Scope             string                    `json:"scope"`
	Agent             string                    `json:"agent,omitempty"`
	Mode              RetrievalMode             `json:"mode,omitempty"`
	AsOf              string                    `json:"as_of,omitempty"`
	IncludeHistorical bool                      `json:"include_historical,omitempty"`
	IncludeUnverified bool                      `json:"include_unverified,omitempty"`
	Limit             int                       `json:"limit,omitempty"`
	TokenBudget       int                       `json:"token_budget,omitempty"`
	AgentProfile      *store.AgentProfileRecord `json:"-"`
}

type EntityDossierResult struct {
	Entity            string                   `json:"entity"`
	Scope             string                   `json:"scope"`
	RetrievalMode     RetrievalMode            `json:"mode,omitempty"`
	Dossier           EntityDossier            `json:"dossier"`
	TemporalContext   TemporalContext          `json:"temporal_context,omitempty"`
	Citations         []Citation               `json:"citations,omitempty"`
	EvidenceAnchors   []EvidenceAnchor         `json:"evidence_anchors,omitempty"`
	Conflicts         []store.ConflictResult   `json:"conflicts,omitempty"`
	MemoryHealth      store.MemoryHealthResult `json:"memory_health"`
	Verification      VerificationReport       `json:"verification"`
	AgentDecision     AgentDecision            `json:"agent_decision"`
	RetrievalTrace    []RetrievalTraceItem     `json:"retrieval_trace,omitempty"`
	RetrievalReasons  []store.RetrievalReason  `json:"retrieval_reasons,omitempty"`
	RetrievalWarnings []RetrievalWarning       `json:"retrieval_warnings,omitempty"`
	GraphWarnings     []GraphWarning           `json:"graph_warnings,omitempty"`
	Stats             ComposeStats             `json:"stats"`
}

func (c *Composer) EntityDossier(ctx context.Context, input EntityDossierInput) (EntityDossierResult, error) {
	entity := strings.Join(strings.Fields(input.Entity), " ")
	scope := strings.TrimSpace(input.Scope)
	if entity == "" || scope == "" {
		return EntityDossierResult{}, fmt.Errorf("entity and scope are required")
	}
	mode := NormalizeRetrievalMode(string(input.Mode))
	if mode == "" {
		mode = RetrievalModeBalanced
	}
	resolved, err := c.resolveEntity(ctx, scope, entity)
	if err != nil {
		return EntityDossierResult{}, err
	}
	if strings.TrimSpace(resolved.Name) != "" {
		entity = resolved.Name
	}
	packet, err := c.Compose(ctx, ComposeInput{
		Task:              "Entity dossier: " + entity,
		Scope:             scope,
		Hook:              "before_task",
		Agent:             input.Agent,
		Entity:            entity,
		Mode:              mode,
		AsOf:              input.AsOf,
		IncludeHistorical: input.IncludeHistorical,
		Limit:             input.Limit,
		TokenBudget:       input.TokenBudget,
		IncludeUnverified: input.IncludeUnverified,
		Diagnostic:        true,
		AgentProfile:      input.AgentProfile,
	})
	if err != nil {
		return EntityDossierResult{}, err
	}
	dossier := EntityDossier{Entity: entity, EntityKey: entityKey(entity), Trust: "no_evidence", NextAction: "ingest or connect source-backed memory for this entity"}
	if len(packet.EntityDossiers) > 0 {
		dossier = packet.EntityDossiers[0]
	}
	if strings.TrimSpace(resolved.ID) != "" {
		dossier.Entity = resolved.Name
		dossier.EntityKey = resolved.ID
		dossier.Aliases = mergeAliases(resolved.Aliases, dossier.Aliases)
	}
	return EntityDossierResult{
		Entity:            entity,
		Scope:             scope,
		RetrievalMode:     packet.RetrievalMode,
		Dossier:           dossier,
		TemporalContext:   packet.TemporalContext,
		Citations:         packet.Citations,
		EvidenceAnchors:   packet.EvidenceAnchors,
		Conflicts:         packet.Conflicts,
		MemoryHealth:      packet.MemoryHealth,
		Verification:      packet.Verification,
		AgentDecision:     packet.AgentDecision,
		RetrievalTrace:    packet.RetrievalTrace,
		RetrievalReasons:  packet.RetrievalReasons,
		RetrievalWarnings: packet.RetrievalWarnings,
		GraphWarnings:     packet.GraphWarnings,
		Stats:             packet.Stats,
	}, nil
}

func (c *Composer) resolveEntity(ctx context.Context, scope, entity string) (store.EntityResolutionResult, error) {
	resolver, ok := c.store.(entityResolverStore)
	if !ok {
		return store.EntityResolutionResult{}, nil
	}
	return resolver.ResolveEntity(ctx, scope, entity)
}

func buildTemporalContext(input ComposeInput, result ComposeResult, asOfApplied bool, asOfWarning string) TemporalContext {
	includeHistorical := input.IncludeHistorical || input.Mode == RetrievalModeDeep
	ctx := TemporalContext{
		EffectiveAt:              time.Now().UTC().Format(time.RFC3339),
		AsOf:                     strings.TrimSpace(input.AsOf),
		AsOfAppliedToRecall:      asOfApplied,
		IncludeHistorical:        includeHistorical,
		DefaultHidesHistorical:   true,
		CurrentFilter:            "current verified/inferred claims and active graph relations; expired and superseded records are hidden by default",
		HistoricalUseRequirement: "historical claims and relations are context only and require explicit review before autonomous use",
	}
	if ctx.AsOf != "" {
		if asOfWarning != "" {
			ctx.Warnings = append(ctx.Warnings, asOfWarning)
		}
		if asOfApplied {
			ctx.CurrentFilter = "point-in-time verified/inferred claims and graph relations effective at as_of; expired and superseded records are hidden unless historical context is requested"
		} else if asOfWarning == "" {
			if _, err := time.Parse(time.RFC3339, ctx.AsOf); err != nil {
				ctx.Warnings = append(ctx.Warnings, "as_of must be RFC3339; temporal recall used current-time lifecycle filters")
			}
		}
	}
	for _, fact := range result.Facts {
		if !freshnessActive(fact.Freshness) {
			ctx.HistoricalIncluded = true
			break
		}
	}
	if !ctx.HistoricalIncluded {
		for _, relation := range result.GraphContext {
			if !relationActive(relation) {
				ctx.HistoricalIncluded = true
				break
			}
		}
	}
	return ctx
}

func buildEntityDossiers(input ComposeInput, result ComposeResult) []EntityDossier {
	focus := entityFocusNames(input, result)
	if len(focus) == 0 {
		return nil
	}
	citationRefs := citationRefMap(result.Citations)
	out := make([]EntityDossier, 0, len(focus))
	for _, name := range focus {
		dossier := buildEntityDossier(name, input, result, citationRefs)
		if dossier.Stats.Claims+dossier.Stats.Relations+dossier.Stats.Anchors+dossier.Stats.Conflicts == 0 {
			continue
		}
		out = append(out, dossier)
		if len(out) >= maxEntityDossiers {
			break
		}
	}
	return out
}

func buildEntityDossier(entity string, input ComposeInput, result ComposeResult, citationRefs map[string]string) EntityDossier {
	includeHistorical := input.IncludeHistorical || input.Mode == RetrievalModeDeep
	claimIDs := map[string]struct{}{}
	relationIDs := map[string]struct{}{}
	sourceURLs := map[string]struct{}{}
	dossier := EntityDossier{
		Entity:           entity,
		EntityKey:        entityKey(entity),
		Aliases:          entityAliases(entity, result.GraphContext),
		RetrievalReasons: append([]store.RetrievalReason(nil), result.RetrievalReasons...),
	}
	for _, claim := range result.Facts {
		if !entityMatchesText(entity, claim.Claim) {
			continue
		}
		item := entityDossierClaim(claim, citationRefs)
		claimIDs[claim.ID] = struct{}{}
		if source := pointerString(claim.Source); source != "" {
			sourceURLs[source] = struct{}{}
		}
		if freshnessActive(claim.Freshness) {
			dossier.ActiveClaims = append(dossier.ActiveClaims, item)
		} else if includeHistorical {
			dossier.HistoricalClaims = append(dossier.HistoricalClaims, item)
		}
	}
	for _, relation := range result.GraphContext {
		if !entityMatchesRelation(entity, relation) {
			continue
		}
		item := entityDossierRelation(relation, citationRefs)
		if relation.ID != "" {
			relationIDs[relation.ID] = struct{}{}
		}
		if relation.ClaimID != "" {
			claimIDs[relation.ClaimID] = struct{}{}
		}
		if source := pointerString(relation.SourceURL); source != "" {
			sourceURLs[source] = struct{}{}
		}
		if relationActive(relation) {
			dossier.ActiveRelations = append(dossier.ActiveRelations, item)
		} else if includeHistorical {
			dossier.HistoricalRelations = append(dossier.HistoricalRelations, item)
		}
	}
	for _, anchor := range result.EvidenceAnchors {
		if _, ok := claimIDs[anchor.ClaimID]; ok {
			dossier.EvidenceAnchors = append(dossier.EvidenceAnchors, anchor)
			continue
		}
		if _, ok := sourceURLs[anchor.SourceURL]; ok && entityMatchesText(entity, anchor.Quote) {
			dossier.EvidenceAnchors = append(dossier.EvidenceAnchors, anchor)
		}
	}
	for _, conflict := range result.Conflicts {
		if conflictTouches(conflict, claimIDs, relationIDs) {
			dossier.Conflicts = append(dossier.Conflicts, conflict)
		}
	}
	sortEntityDossier(&dossier)
	dossier.Stats = EntityDossierStats{
		Claims:              len(dossier.ActiveClaims) + len(dossier.HistoricalClaims),
		ActiveClaims:        len(dossier.ActiveClaims),
		HistoricalClaims:    len(dossier.HistoricalClaims),
		Relations:           len(dossier.ActiveRelations) + len(dossier.HistoricalRelations),
		ActiveRelations:     len(dossier.ActiveRelations),
		HistoricalRelations: len(dossier.HistoricalRelations),
		Anchors:             len(dossier.EvidenceAnchors),
		Conflicts:           len(dossier.Conflicts),
	}
	dossier.Trust = entityDossierTrust(dossier)
	dossier.NextAction = entityDossierNextAction(dossier)
	return dossier
}

func entityFocusNames(input ComposeInput, result ComposeResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			return
		}
		key := entityKey(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	add(input.Entity)
	for _, relation := range result.GraphContext {
		if entityMatchesText(relation.FromEntity, input.Task) {
			add(relation.FromEntity)
		}
		if entityMatchesText(relation.ToEntity, input.Task) {
			add(relation.ToEntity)
		}
	}
	if len(out) == 0 && len(result.GraphContext) > 0 {
		add(result.GraphContext[0].FromEntity)
	}
	if len(out) == 0 {
		for _, fact := range result.Facts {
			if candidate := leadingEntityCandidate(fact.Claim); candidate != "" {
				add(candidate)
				break
			}
		}
	}
	if len(out) > maxEntityDossiers {
		return out[:maxEntityDossiers]
	}
	return out
}

func entityDossierClaim(claim store.ClaimResult, citationRefs map[string]string) EntityDossierClaim {
	source := pointerString(claim.Source)
	return EntityDossierClaim{
		ID:          claim.ID,
		Claim:       claim.Claim,
		Status:      claim.Status,
		Freshness:   claim.Freshness,
		SourceURL:   source,
		Rank:        round2(claim.Rank),
		CitationRef: citationRefs[source],
	}
}

func entityDossierRelation(relation store.RelationResult, citationRefs map[string]string) EntityDossierRelation {
	source := pointerString(relation.SourceURL)
	return EntityDossierRelation{
		ID:          relation.ID,
		From:        relation.FromEntity,
		Type:        relation.Type,
		To:          relation.ToEntity,
		Confidence:  round2(relation.Confidence),
		SourceURL:   source,
		CitationRef: citationRefs[source],
	}
}

func entityDossierTrust(dossier EntityDossier) string {
	switch {
	case len(dossier.Conflicts) > 0:
		return "blocked_by_conflict"
	case len(dossier.ActiveClaims) > 0 && len(dossier.EvidenceAnchors) > 0:
		return "anchored"
	case len(dossier.ActiveClaims) > 0 || len(dossier.ActiveRelations) > 0:
		return "source_cited"
	case len(dossier.HistoricalClaims) > 0 || len(dossier.HistoricalRelations) > 0:
		return "historical_only"
	default:
		return "no_evidence"
	}
}

func entityDossierNextAction(dossier EntityDossier) string {
	switch dossier.Trust {
	case "blocked_by_conflict":
		return "resolve conflicts before autonomous use"
	case "anchored":
		return "safe to cite with evidence anchors"
	case "source_cited":
		return "cite sources; add evidence anchors before synthesis"
	case "historical_only":
		return "treat as historical context; refresh sources before use"
	default:
		return "ingest or connect source-backed memory for this entity"
	}
}

func entityAliases(entity string, relations []store.RelationResult) []string {
	aliases := []string{}
	add := func(value string) {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" || strings.EqualFold(value, entity) {
			return
		}
		aliases = append(aliases, value)
	}
	for _, relation := range relations {
		if strings.EqualFold(relation.FromEntity, entity) {
			add(relation.ToEntity)
		}
		if strings.EqualFold(relation.ToEntity, entity) {
			add(relation.FromEntity)
		}
	}
	return compactList(aliases)
}

func mergeAliases(first, second []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.Join(strings.Fields(value), " ")
		if value == "" {
			return
		}
		key := entityKey(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	for _, value := range first {
		add(value)
	}
	for _, value := range second {
		add(value)
	}
	return out
}

func sortEntityDossier(dossier *EntityDossier) {
	sort.SliceStable(dossier.ActiveClaims, func(i, j int) bool { return dossier.ActiveClaims[i].Rank > dossier.ActiveClaims[j].Rank })
	sort.SliceStable(dossier.HistoricalClaims, func(i, j int) bool { return dossier.HistoricalClaims[i].Rank > dossier.HistoricalClaims[j].Rank })
	sort.SliceStable(dossier.ActiveRelations, func(i, j int) bool {
		return dossier.ActiveRelations[i].Confidence > dossier.ActiveRelations[j].Confidence
	})
	sort.SliceStable(dossier.HistoricalRelations, func(i, j int) bool {
		return dossier.HistoricalRelations[i].Confidence > dossier.HistoricalRelations[j].Confidence
	})
	if len(dossier.ActiveClaims) > 8 {
		dossier.ActiveClaims = dossier.ActiveClaims[:8]
	}
	if len(dossier.HistoricalClaims) > 5 {
		dossier.HistoricalClaims = dossier.HistoricalClaims[:5]
	}
	if len(dossier.ActiveRelations) > 8 {
		dossier.ActiveRelations = dossier.ActiveRelations[:8]
	}
	if len(dossier.HistoricalRelations) > 5 {
		dossier.HistoricalRelations = dossier.HistoricalRelations[:5]
	}
	if len(dossier.EvidenceAnchors) > 8 {
		dossier.EvidenceAnchors = dossier.EvidenceAnchors[:8]
	}
}

func freshnessActive(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "fresh", "unknown":
		return true
	default:
		return false
	}
}

func relationActive(relation store.RelationResult) bool {
	status := strings.ToLower(strings.TrimSpace(relation.Status))
	if status == "expired" || status == "deprecated" || status == "challenged" {
		return false
	}
	return freshnessActive(relation.Freshness)
}

func entityMatchesRelation(entity string, relation store.RelationResult) bool {
	return entityMatchesText(entity, relation.FromEntity) || entityMatchesText(entity, relation.ToEntity)
}

func entityMatchesText(entity, text string) bool {
	entity = strings.ToLower(strings.TrimSpace(entity))
	text = strings.ToLower(strings.TrimSpace(text))
	if entity == "" || text == "" {
		return false
	}
	if strings.Contains(text, entity) {
		return true
	}
	parts := strings.FieldsFunc(entity, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '.' || r == ':' || r == ' '
	})
	matches := 0
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		if strings.Contains(text, part) {
			matches++
		}
	}
	return matches > 0 && matches >= minInt(2, len(parts))
}

func entityKey(value string) string {
	value = strings.ToLower(strings.Join(strings.Fields(value), " "))
	sum := sha256.Sum256([]byte(value))
	return "ent-" + hex.EncodeToString(sum[:])[:16]
}

func leadingEntityCandidate(claim string) string {
	claim = strings.TrimSpace(claim)
	if claim == "" {
		return ""
	}
	cut := claim
	for _, sep := range []string{" is ", " are ", " uses ", " should ", " must ", " has ", " have "} {
		if before, _, ok := strings.Cut(strings.ToLower(claim), sep); ok {
			runes := []rune(claim)
			cut = strings.TrimSpace(string(runes[:len([]rune(before))]))
			break
		}
	}
	if len([]rune(cut)) > 80 {
		return ""
	}
	return cut
}

func conflictTouches(conflict store.ConflictResult, claimIDs, relationIDs map[string]struct{}) bool {
	for _, id := range []string{conflict.PrimaryClaimID, conflict.ConflictingClaimID} {
		if _, ok := claimIDs[id]; ok && id != "" {
			return true
		}
	}
	for _, id := range []string{conflict.PrimaryRelationID, conflict.ConflictingRelationID} {
		if _, ok := relationIDs[id]; ok && id != "" {
			return true
		}
	}
	return false
}
