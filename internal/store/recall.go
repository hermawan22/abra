package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) SearchClaims(ctx context.Context, query, scope, excludeID string, limit int) ([]ClaimResult, error) {
	query = strings.TrimSpace(query)
	scope = strings.TrimSpace(scope)
	excludeID = strings.TrimSpace(excludeID)
	if query == "" || scope == "" {
		return nil, nil
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.queryRunner().Query(ctx, searchClaimsSQL(), query, scope, excludeID, limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := []ClaimResult{}
	for rows.Next() {
		var claim ClaimResult
		if err := rows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func recordEffectiveSQL(alias string) string {
	return recordEffectiveAtSQL(alias, "now()")
}

func recordEffectiveAtSQL(alias, atExpr string) string {
	atExpr = strings.TrimSpace(atExpr)
	if atExpr == "" {
		atExpr = "now()"
	}
	return fmt.Sprintf(`(%[1]s.valid_from IS NULL OR %[1]s.valid_from <= %[2]s)
		  AND (%[1]s.expires_at IS NULL OR %[1]s.expires_at > %[2]s)`, alias, atExpr)
}

func claimEffectiveSQL(alias string) string {
	return claimEffectiveWithSupersedingStatusSQL(alias, "superseding_claim.status NOT IN ('deprecated', 'expired')")
}

func claimEffectiveSQLForRecallAt(alias string, includeUnverified bool, atExpr string) string {
	if includeUnverified {
		return claimEffectiveWithSupersedingStatusAtSQL(alias, "superseding_claim.status NOT IN ('deprecated', 'expired')", atExpr)
	}
	return claimEffectiveWithSupersedingStatusAtSQL(alias, "superseding_claim.status IN ('verified', 'inferred')", atExpr)
}

func claimEffectiveWithSupersedingStatusSQL(alias, supersedingStatusFilter string) string {
	return claimEffectiveWithSupersedingStatusAtSQL(alias, supersedingStatusFilter, "now()")
}

func claimEffectiveWithSupersedingStatusAtSQL(alias, supersedingStatusFilter, atExpr string) string {
	supersedingStatusFilter = strings.TrimSpace(supersedingStatusFilter)
	if supersedingStatusFilter == "" {
		supersedingStatusFilter = "superseding_claim.status NOT IN ('deprecated', 'expired')"
	}
	atExpr = strings.TrimSpace(atExpr)
	if atExpr == "" {
		atExpr = "now()"
	}
	return fmt.Sprintf(`(%[1]s.valid_from IS NULL OR %[1]s.valid_from <= %[3]s)
		  AND (%[1]s.expires_at IS NULL OR %[1]s.expires_at > %[3]s)
		  AND NOT EXISTS (
		    SELECT 1
		    FROM claims superseding_claim
		    WHERE superseding_claim.supersedes_claim_id = %[1]s.id
		      AND superseding_claim.scope = %[1]s.scope
		      AND %[2]s
		      AND (superseding_claim.valid_from IS NULL OR superseding_claim.valid_from <= %[3]s)
		      AND (superseding_claim.expires_at IS NULL OR superseding_claim.expires_at > %[3]s)
		  )`, alias, supersedingStatusFilter, atExpr)
}

func activeClaimStatusSQL(alias string, includeUnverified bool) string {
	if includeUnverified {
		return fmt.Sprintf("%s.status NOT IN ('deprecated', 'expired')", alias)
	}
	return fmt.Sprintf("%s.status IN ('verified', 'inferred')", alias)
}

func historicalClaimStatusSQL(alias string, includeUnverified bool) string {
	if includeUnverified {
		return fmt.Sprintf("%s.status NOT IN ('deprecated')", alias)
	}
	return fmt.Sprintf("%s.status IN ('verified', 'inferred', 'expired')", alias)
}

func recallEffectiveAt(options RecallOptions) time.Time {
	if !options.AsOf.IsZero() {
		return options.AsOf.UTC()
	}
	return time.Now().UTC()
}

func searchClaimsSQL() string {
	return fmt.Sprintf(`
		SELECT
			id,
			claim_text,
			scope,
			status,
			source_url,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $5)) * 0.65
			  ),
			  0.4
			) AS text_score,
			0::double precision AS vector_score,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $5)) * 0.65
			  ),
			  0.4
			)
			  + confidence
			  + CASE authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
			CASE
			  WHEN expires_at IS NOT NULL AND expires_at < now() THEN 'expired'
			  WHEN last_verified_at IS NULL THEN 'unknown'
			  WHEN last_verified_at < now() - interval '120 days' THEN 'stale'
			  ELSE 'fresh'
			END AS freshness
		FROM claims
		WHERE scope = $2
		  AND status NOT IN ('deprecated', 'expired')
		  AND %s
		  AND ($3 = '' OR id != $3)
		  AND (
		    search_vector @@ plainto_tsquery('simple', $1)
		    OR search_vector @@ to_tsquery('simple', $5)
		  )
		ORDER BY rank_score DESC
		LIMIT $4
	`, claimEffectiveSQL("claims"))
}

func (s *Store) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (RecallResult, error) {
	return s.RecallWithOptions(ctx, query, scope, limit, includeUnverified, RecallOptions{})
}

func (s *Store) RecallWithOptions(ctx context.Context, query, scope string, limit int, includeUnverified bool, options RecallOptions) (RecallResult, error) {
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	statusFilter := activeClaimStatusSQL("c", includeUnverified)
	if options.IncludeHistorical {
		statusFilter = historicalClaimStatusSQL("c", includeUnverified)
	}
	effectiveAt := recallEffectiveAt(options)
	lifecycleFilter := claimEffectiveSQLForRecallAt("c", includeUnverified, "$5")
	if options.IncludeHistorical {
		lifecycleFilter = "true"
	}

	claimsRows, err := s.pool.Query(ctx, fmt.Sprintf(`
		WITH ranked_claims AS (
		SELECT
			c.id,
			c.claim_text,
			c.scope,
			c.status,
			c.source_url,
			LEAST(
			  GREATEST(
			    ts_rank_cd(c.search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(c.search_vector, to_tsquery('simple', $4)) * 0.65
			  ),
			  0.4
			) AS text_score,
			0::double precision AS vector_score,
			LEAST(
			  GREATEST(
			    ts_rank_cd(c.search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(c.search_vector, to_tsquery('simple', $4)) * 0.65
			  ),
			  0.4
				)
				  + c.confidence
			  + CASE c.authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
				CASE
				  WHEN c.status = 'expired' OR (c.expires_at IS NOT NULL AND c.expires_at <= $5) OR c.freshness_status = 'expired' OR d.freshness_status = 'expired' THEN 'expired'
				  WHEN c.freshness_status = 'stale' OR d.freshness_status = 'stale' OR source_freshness.refresh_due THEN 'stale'
				  WHEN c.last_verified_at IS NULL THEN 'unknown'
				  WHEN c.last_verified_at < $5 - interval '120 days' THEN 'stale'
				  ELSE 'fresh'
				END AS freshness,
			c.updated_at AS sort_updated_at
		FROM claims c
		LEFT JOIN documents d
		  ON d.scope = c.scope
		 AND COALESCE(d.source_type, '') = COALESCE(c.source_type, '')
		 AND COALESCE(d.source_url, '') = COALESCE(c.source_url, '')
		LEFT JOIN source_configs sc ON sc.id = c.source_config_id
		LEFT JOIN source_configs d_sc ON d_sc.id = d.source_config_id
		LEFT JOIN LATERAL (
		  SELECT
		    sc.status = 'active'
		    AND (
		      (freshness.freshness_interval IS NOT NULL AND (
			        (sc.last_success_at IS NULL AND sc.created_at < $5 - freshness.freshness_interval)
			        OR sc.last_success_at < $5 - freshness.freshness_interval
			      ))
			      OR (freshness.schedule_interval IS NOT NULL AND (
			        (sc.last_success_at IS NULL AND sc.created_at < $5 - freshness.schedule_interval)
			        OR sc.last_success_at < $5 - freshness.schedule_interval
			      ))
		    ) AS refresh_due
		  FROM (
		    SELECT
		      CASE
		        WHEN sc.freshness_policy->>'max_age_seconds' ~ '^[1-9][0-9]*$'
		          THEN make_interval(secs => (sc.freshness_policy->>'max_age_seconds')::int)
		        WHEN sc.freshness_policy->>'max_age_minutes' ~ '^[1-9][0-9]*$'
		          THEN make_interval(mins => (sc.freshness_policy->>'max_age_minutes')::int)
		        WHEN sc.freshness_policy->>'max_age_hours' ~ '^[1-9][0-9]*$'
		          THEN make_interval(hours => (sc.freshness_policy->>'max_age_hours')::int)
		        WHEN sc.freshness_policy->>'max_age_days' ~ '^[1-9][0-9]*$'
		          THEN make_interval(days => (sc.freshness_policy->>'max_age_days')::int)
		        ELSE NULL
		      END AS freshness_interval,
		      CASE
		        WHEN sc.schedule_cron = '@hourly' THEN interval '1 hour'
		        WHEN sc.schedule_cron = '@daily' THEN interval '24 hours'
		        WHEN sc.schedule_cron ~ '^@every[[:space:]]+[1-9][0-9]*[smhd]$' THEN
		          CASE right(sc.schedule_cron, 1)
		            WHEN 's' THEN make_interval(secs => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		            WHEN 'm' THEN make_interval(mins => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		            WHEN 'h' THEN make_interval(hours => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		            WHEN 'd' THEN make_interval(days => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		          END
		        ELSE NULL
		      END AS schedule_interval
		  ) freshness
		) source_freshness ON true
		WHERE c.scope = $2
		  AND %s
		  AND %s
		  AND COALESCE(sc.status, 'active') <> 'deleted'
		  AND COALESCE(d_sc.status, 'active') <> 'deleted'
		  AND (
		    c.search_vector @@ plainto_tsquery('simple', $1)
		    OR c.search_vector @@ to_tsquery('simple', $4)
		  )
		)
		SELECT id, claim_text, scope, status, source_url, text_score, vector_score, rank_score, freshness
		FROM ranked_claims
		ORDER BY
		  CASE freshness
		    WHEN 'fresh' THEN 0
		    WHEN 'unknown' THEN 1
		    WHEN 'stale' THEN 2
		    WHEN 'expired' THEN 3
		    ELSE 1
		  END ASC,
		  rank_score DESC,
		  sort_updated_at DESC
		LIMIT $3
		`, statusFilter, lifecycleFilter), query, scope, limit, anyQuery, effectiveAt)
	if err != nil {
		return RecallResult{}, err
	}
	defer claimsRows.Close()

	result := RecallResult{
		Claims:              []ClaimResult{},
		SupportingDocuments: []DocumentResult{},
		GraphContext:        []RelationResult{},
		RetrievalMode:       "full_text",
	}
	for claimsRows.Next() {
		var claim ClaimResult
		if err := claimsRows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return RecallResult{}, err
		}
		result.Claims = append(result.Claims, claim)
	}
	if err := claimsRows.Err(); err != nil {
		return RecallResult{}, err
	}

	docRows, err := s.pool.Query(ctx, `
		SELECT d.id, d.title, d.source_url, ch.content,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS text_score,
		       0::double precision AS vector_score,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS rank_score
		FROM chunks ch
		JOIN documents d ON d.id = ch.document_id
		LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		WHERE ch.scope = $2
		  AND d.scope = $2
		  AND d.status NOT IN ('deprecated', 'deleted')
		  AND COALESCE(sc.status, 'active') <> 'deleted'
		  AND (
		    ch.search_vector @@ plainto_tsquery('simple', $1)
		    OR ch.search_vector @@ to_tsquery('simple', $4)
		  )
		ORDER BY rank_score DESC
		LIMIT $3
	`, query, scope, min(limit, 5), anyQuery)
	if err != nil {
		return RecallResult{}, err
	}
	defer docRows.Close()
	for docRows.Next() {
		var doc DocumentResult
		if err := docRows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.TextScore, &doc.VectorScore, &doc.Rank); err != nil {
			return RecallResult{}, err
		}
		result.SupportingDocuments = append(result.SupportingDocuments, doc)
	}
	if err := docRows.Err(); err != nil {
		return RecallResult{}, err
	}
	relations, err := s.RelatedGraphWithOptions(ctx, query, scope, min(limit, 8), options)
	if err != nil {
		return RecallResult{}, err
	}
	result.GraphContext = relations
	if err := s.appendGraphLinkedClaims(ctx, &result, scope, includeUnverified, options); err != nil {
		return RecallResult{}, err
	}
	applyBaseRankScores(&result)
	result.RetrievalReasons = recallRetrievalReasons(result)
	return result, nil
}

func (s *Store) RecallHybrid(ctx context.Context, query, scope string, limit int, includeUnverified bool, queryEmbedding []float64) (RecallResult, error) {
	return s.RecallHybridWithOptions(ctx, query, scope, limit, includeUnverified, queryEmbedding, RecallOptions{})
}

func (s *Store) RecallHybridWithOptions(ctx context.Context, query, scope string, limit int, includeUnverified bool, queryEmbedding []float64, options RecallOptions) (RecallResult, error) {
	if len(queryEmbedding) == 0 {
		return s.RecallWithOptions(ctx, query, scope, limit, includeUnverified, options)
	}
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	statusFilter := activeClaimStatusSQL("c", includeUnverified)
	if options.IncludeHistorical {
		statusFilter = historicalClaimStatusSQL("c", includeUnverified)
	}
	vector := vectorLiteral(queryEmbedding)

	dimensions := len(queryEmbedding)
	effectiveAt := recallEffectiveAt(options)
	claimsRows, err := s.pool.Query(ctx, hybridRecallClaimsSQL(statusFilter, dimensions, includeUnverified, options.IncludeHistorical), query, scope, limit, anyQuery, vector, dimensions, effectiveAt)
	if err != nil {
		return RecallResult{}, err
	}
	defer claimsRows.Close()

	result := RecallResult{
		Claims:              []ClaimResult{},
		SupportingDocuments: []DocumentResult{},
		GraphContext:        []RelationResult{},
		RetrievalMode:       "hybrid",
	}
	for claimsRows.Next() {
		var claim ClaimResult
		if err := claimsRows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return RecallResult{}, err
		}
		result.Claims = append(result.Claims, claim)
	}
	if err := claimsRows.Err(); err != nil {
		return RecallResult{}, err
	}

	docRows, err := s.pool.Query(ctx, hybridRecallDocumentsSQL(dimensions), query, scope, min(limit, 5), anyQuery, vector, dimensions)
	if err != nil {
		return RecallResult{}, err
	}
	defer docRows.Close()
	for docRows.Next() {
		var doc DocumentResult
		if err := docRows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.TextScore, &doc.VectorScore, &doc.Rank); err != nil {
			return RecallResult{}, err
		}
		result.SupportingDocuments = append(result.SupportingDocuments, doc)
	}
	if err := docRows.Err(); err != nil {
		return RecallResult{}, err
	}
	relations, err := s.RelatedGraphWithOptions(ctx, query, scope, min(limit, 8), options)
	if err != nil {
		return RecallResult{}, err
	}
	result.GraphContext = relations
	if err := s.appendGraphLinkedClaims(ctx, &result, scope, includeUnverified, options); err != nil {
		return RecallResult{}, err
	}
	applyBaseRankScores(&result)
	result.RetrievalReasons = recallRetrievalReasons(result)
	return result, nil
}

func applyBaseRankScores(result *RecallResult) {
	if result == nil {
		return
	}
	for i := range result.Claims {
		if result.Claims[i].BaseRank == 0 && result.Claims[i].Rank != 0 {
			result.Claims[i].BaseRank = result.Claims[i].Rank
		}
	}
	for i := range result.SupportingDocuments {
		if result.SupportingDocuments[i].BaseRank == 0 && result.SupportingDocuments[i].Rank != 0 {
			result.SupportingDocuments[i].BaseRank = result.SupportingDocuments[i].Rank
		}
	}
}

func (s *Store) appendGraphLinkedClaims(ctx context.Context, result *RecallResult, scope string, includeUnverified bool, options RecallOptions) error {
	ids := graphLinkedClaimIDs(result.Claims, result.GraphContext)
	if len(ids) == 0 {
		return nil
	}
	statusFilter := activeClaimStatusSQL("c", includeUnverified)
	if options.IncludeHistorical {
		statusFilter = historicalClaimStatusSQL("c", includeUnverified)
	}
	lifecycleFilter := claimEffectiveSQLForRecallAt("c", includeUnverified, "$3")
	if options.IncludeHistorical {
		lifecycleFilter = "true"
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT
		  c.id,
		  c.claim_text,
		  c.scope,
		  c.status,
		  c.source_url,
		  0::double precision AS text_score,
		  0::double precision AS vector_score,
		  c.confidence + COALESCE(MAX(r.confidence), 0) * 0.3 AS rank_score,
		  CASE
		    WHEN c.status = 'expired' OR (c.expires_at IS NOT NULL AND c.expires_at <= $3) OR c.freshness_status = 'expired' THEN 'expired'
		    WHEN c.freshness_status = 'stale' THEN 'stale'
		    WHEN c.last_verified_at IS NULL THEN 'unknown'
		    WHEN c.last_verified_at < $3 - interval '120 days' THEN 'stale'
		    ELSE 'fresh'
		  END AS freshness
		FROM claims c
		LEFT JOIN relations r ON r.claim_id = c.id AND r.scope = c.scope
		WHERE c.id = ANY($1)
		  AND c.scope = $2
		  AND %s
		  AND %s
		GROUP BY c.id, c.claim_text, c.scope, c.status, c.source_url, c.confidence, c.expires_at, c.freshness_status, c.last_verified_at
		ORDER BY rank_score DESC, c.updated_at DESC
	`, statusFilter, lifecycleFilter), ids, strings.TrimSpace(scope), recallEffectiveAt(options))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var claim ClaimResult
		if err := rows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return err
		}
		result.Claims = append(result.Claims, claim)
	}
	return rows.Err()
}

func graphLinkedClaimIDs(claims []ClaimResult, relations []RelationResult) []string {
	seenClaims := map[string]struct{}{}
	for _, claim := range claims {
		if strings.TrimSpace(claim.ID) != "" {
			seenClaims[claim.ID] = struct{}{}
		}
	}
	seenIDs := map[string]struct{}{}
	ids := []string{}
	for _, relation := range relations {
		id := strings.TrimSpace(relation.ClaimID)
		if id == "" {
			continue
		}
		if _, ok := seenClaims[id]; ok {
			continue
		}
		if _, ok := seenIDs[id]; ok {
			continue
		}
		seenIDs[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func RecallRetrievalReasons(result RecallResult) []RetrievalReason {
	return recallRetrievalReasons(result)
}

func recallRetrievalReasons(result RecallResult) []RetrievalReason {
	reasons := []RetrievalReason{}
	textCount := 0
	vectorCount := 0
	for _, claim := range result.Claims {
		if claim.TextScore > 0 {
			textCount++
		}
		if claim.VectorScore > 0 {
			vectorCount++
		}
	}
	for _, doc := range result.SupportingDocuments {
		if doc.TextScore > 0 {
			textCount++
		}
		if doc.VectorScore > 0 {
			vectorCount++
		}
	}
	if textCount > 0 {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "text",
			Message: "Full-text/BM25-style matches contributed to recalled claims or documents.",
			Count:   textCount,
		})
	}
	if vectorCount > 0 {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "vector",
			Message: "Semantic vector similarity contributed to recalled claims or documents.",
			Count:   vectorCount,
		})
	}
	if len(result.GraphContext) > 0 {
		graphLinkedClaims := 0
		for _, claim := range result.Claims {
			if claim.TextScore == 0 && claim.VectorScore == 0 {
				graphLinkedClaims++
			}
		}
		if graphLinkedClaims > 0 {
			reasons = append(reasons, RetrievalReason{
				Mode:    "entity_graph",
				Signal:  "graph_claim",
				Message: "Claims linked from graph relations contributed even without lexical or vector match.",
				Count:   graphLinkedClaims,
			})
		}
		reasons = append(reasons, RetrievalReason{
			Mode:    "entity_local",
			Signal:  "graph",
			Message: "Entity-neighborhood graph relations expanded the packet beyond lexical matches.",
			Count:   len(result.GraphContext),
		})
		reasons = append(reasons, RetrievalReason{
			Mode:    "entity_boost",
			Signal:  "entity",
			Message: "Entity-linked graph context boosted relationship-aware recall without an additional LLM call.",
			Count:   len(result.GraphContext),
		})
	}
	if len(reasons) == 0 && (len(result.Claims) > 0 || len(result.SupportingDocuments) > 0) {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "rank",
			Message: "Ranked recall returned context without exposed text/vector sub-scores.",
			Count:   len(result.Claims) + len(result.SupportingDocuments),
		})
	}
	return reasons
}

func hybridRecallClaimsSQL(statusFilter string, dimensions int, includeUnverified bool, includeHistorical bool) string {
	embeddingExpr, queryExpr := vectorComparisonExpr("embedding", "$5", dimensions)
	lifecycleFilter := claimEffectiveSQLForRecallAt("c", includeUnverified, "$7")
	if includeHistorical {
		lifecycleFilter = "true"
	}
	return fmt.Sprintf(`
		WITH text_matches AS (
		  SELECT
		    c.id,
		    LEAST(
		      GREATEST(
		        ts_rank_cd(c.search_vector, plainto_tsquery('simple', $1)),
		        ts_rank_cd(c.search_vector, to_tsquery('simple', $4)) * 0.65
		      ),
		      0.4
		    ) AS text_score
		  FROM claims c
		  WHERE c.scope = $2
		    AND %s
		    AND %s
		    AND (
		      c.search_vector @@ plainto_tsquery('simple', $1)
		      OR c.search_vector @@ to_tsquery('simple', $4)
		    )
		  ORDER BY text_score DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		vector_matches AS (
		  SELECT
		    c.id,
		    GREATEST(0, 1 - (%s <=> %s)) AS vector_score
		  FROM claims c
		  WHERE c.scope = $2
		    AND %s
		    AND %s
		    AND c.embedding_dimensions = $6
		  ORDER BY %s <=> %s
		  LIMIT GREATEST($3 * 3, 12)
		),
		candidates AS (
		  SELECT id FROM text_matches
		  UNION
		  SELECT id FROM vector_matches
		),
		ranked_claims AS (
		  SELECT
			c.id,
			c.claim_text,
			c.scope,
			c.status,
			c.source_url,
			COALESCE(tm.text_score, 0) AS text_score,
			COALESCE(vm.vector_score, 0) AS vector_score,
			COALESCE(tm.text_score, 0)
			  + COALESCE(vm.vector_score, 0) * 0.45
			  + c.confidence
			  + CASE c.authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
				CASE
				  WHEN c.status = 'expired' OR (c.expires_at IS NOT NULL AND c.expires_at <= $7) OR c.freshness_status = 'expired' OR d.freshness_status = 'expired' THEN 'expired'
				  WHEN c.freshness_status = 'stale' OR d.freshness_status = 'stale' OR source_freshness.refresh_due THEN 'stale'
				  WHEN c.last_verified_at IS NULL THEN 'unknown'
				  WHEN c.last_verified_at < $7 - interval '120 days' THEN 'stale'
				  ELSE 'fresh'
				END AS freshness,
			c.updated_at AS sort_updated_at
		  FROM candidates candidate
		  JOIN claims c ON c.id = candidate.id
		  LEFT JOIN text_matches tm ON tm.id = c.id
		  LEFT JOIN vector_matches vm ON vm.id = c.id
		  LEFT JOIN documents d
		    ON d.scope = c.scope
		   AND COALESCE(d.source_type, '') = COALESCE(c.source_type, '')
		   AND COALESCE(d.source_url, '') = COALESCE(c.source_url, '')
		  LEFT JOIN source_configs sc ON sc.id = c.source_config_id
		  LEFT JOIN source_configs d_sc ON d_sc.id = d.source_config_id
		  LEFT JOIN LATERAL (
		    SELECT
		      sc.status = 'active'
		      AND (
		        (freshness.freshness_interval IS NOT NULL AND (
			          (sc.last_success_at IS NULL AND sc.created_at < $7 - freshness.freshness_interval)
			          OR sc.last_success_at < $7 - freshness.freshness_interval
			        ))
			        OR (freshness.schedule_interval IS NOT NULL AND (
			          (sc.last_success_at IS NULL AND sc.created_at < $7 - freshness.schedule_interval)
			          OR sc.last_success_at < $7 - freshness.schedule_interval
			        ))
		      ) AS refresh_due
		    FROM (
		      SELECT
		        CASE
		          WHEN sc.freshness_policy->>'max_age_seconds' ~ '^[1-9][0-9]*$'
		            THEN make_interval(secs => (sc.freshness_policy->>'max_age_seconds')::int)
		          WHEN sc.freshness_policy->>'max_age_minutes' ~ '^[1-9][0-9]*$'
		            THEN make_interval(mins => (sc.freshness_policy->>'max_age_minutes')::int)
		          WHEN sc.freshness_policy->>'max_age_hours' ~ '^[1-9][0-9]*$'
		            THEN make_interval(hours => (sc.freshness_policy->>'max_age_hours')::int)
		          WHEN sc.freshness_policy->>'max_age_days' ~ '^[1-9][0-9]*$'
		            THEN make_interval(days => (sc.freshness_policy->>'max_age_days')::int)
		          ELSE NULL
		        END AS freshness_interval,
		        CASE
		          WHEN sc.schedule_cron = '@hourly' THEN interval '1 hour'
		          WHEN sc.schedule_cron = '@daily' THEN interval '24 hours'
		          WHEN sc.schedule_cron ~ '^@every[[:space:]]+[1-9][0-9]*[smhd]$' THEN
		            CASE right(sc.schedule_cron, 1)
		              WHEN 's' THEN make_interval(secs => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		              WHEN 'm' THEN make_interval(mins => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		              WHEN 'h' THEN make_interval(hours => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		              WHEN 'd' THEN make_interval(days => regexp_replace(sc.schedule_cron, '[^0-9]', '', 'g')::int)
		            END
		          ELSE NULL
		        END AS schedule_interval
		    ) freshness
		  ) source_freshness ON true
		  WHERE COALESCE(sc.status, 'active') <> 'deleted'
		    AND COALESCE(d_sc.status, 'active') <> 'deleted'
		)
		SELECT id, claim_text, scope, status, source_url, text_score, vector_score, rank_score, freshness
		FROM ranked_claims
		ORDER BY
		  CASE freshness
		    WHEN 'fresh' THEN 0
		    WHEN 'unknown' THEN 1
		    WHEN 'stale' THEN 2
		    WHEN 'expired' THEN 3
		    ELSE 1
		  END ASC,
		  rank_score DESC,
		  sort_updated_at DESC
		LIMIT $3
	`, statusFilter, lifecycleFilter, embeddingExpr, queryExpr, statusFilter, lifecycleFilter, embeddingExpr, queryExpr)
}

func hybridRecallDocumentsSQL(dimensions int) string {
	embeddingExpr, queryExpr := vectorComparisonExpr("ch.embedding", "$5", dimensions)
	return fmt.Sprintf(`
		WITH text_matches AS (
		  SELECT
		    ch.id,
		    LEAST(
		      GREATEST(
		        ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		        ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		      ),
		      0.4
		    ) AS text_score
		  FROM chunks ch
		  JOIN documents d ON d.id = ch.document_id
		  LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		  WHERE ch.scope = $2
		    AND d.scope = $2
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND COALESCE(sc.status, 'active') <> 'deleted'
		    AND (
		      ch.search_vector @@ plainto_tsquery('simple', $1)
		      OR ch.search_vector @@ to_tsquery('simple', $4)
		    )
		  ORDER BY text_score DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		vector_matches AS (
		  SELECT
		    ch.id,
		    GREATEST(0, 1 - (%s <=> %s)) AS vector_score
		  FROM chunks ch
		  JOIN documents d ON d.id = ch.document_id
		  LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		  WHERE ch.scope = $2
		    AND d.scope = $2
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND COALESCE(sc.status, 'active') <> 'deleted'
		    AND ch.embedding_dimensions = $6
		  ORDER BY %s <=> %s
		  LIMIT GREATEST($3 * 3, 12)
		),
		candidates AS (
		  SELECT id FROM text_matches
		  UNION
		  SELECT id FROM vector_matches
		)
		SELECT d.id, d.title, d.source_url, ch.content,
		       COALESCE(tm.text_score, 0) AS text_score,
		       COALESCE(vm.vector_score, 0) AS vector_score,
		       COALESCE(tm.text_score, 0)
		         + COALESCE(vm.vector_score, 0) * 0.45 AS rank_score
		FROM candidates candidate
		JOIN chunks ch ON ch.id = candidate.id
		JOIN documents d ON d.id = ch.document_id
		LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		LEFT JOIN text_matches tm ON tm.id = ch.id
		LEFT JOIN vector_matches vm ON vm.id = ch.id
		WHERE COALESCE(sc.status, 'active') <> 'deleted'
		ORDER BY rank_score DESC, d.ingested_at DESC, ch.chunk_index ASC
		LIMIT $3
	`, embeddingExpr, queryExpr, embeddingExpr, queryExpr)
}

func vectorComparisonExpr(column, parameter string, dimensions int) (string, string) {
	switch dimensions {
	case 768, 1024, 1280, 1536:
		cast := fmt.Sprintf("vector(%d)", dimensions)
		return fmt.Sprintf("%s::%s", column, cast), fmt.Sprintf("%s::%s", parameter, cast)
	default:
		return column, parameter + "::vector"
	}
}

func (s *Store) Sources(ctx context.Context, query, scope string, limit int) ([]DocumentResult, error) {
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.title, d.source_url, ch.content,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS rank_score
		FROM chunks ch
		JOIN documents d ON d.id = ch.document_id
		LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		WHERE ch.scope = $2
		  AND d.scope = $2
		  AND d.status NOT IN ('deprecated', 'deleted')
		  AND COALESCE(sc.status, 'active') <> 'deleted'
		  AND (
		    ch.search_vector @@ plainto_tsquery('simple', $1)
		    OR ch.search_vector @@ to_tsquery('simple', $4)
		  )
		ORDER BY rank_score DESC
		LIMIT $3
	`, query, scope, limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []DocumentResult
	for rows.Next() {
		var doc DocumentResult
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.Rank); err != nil {
			return nil, err
		}
		doc.TextScore = doc.Rank
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func fullTextAnyQuery(query string) string {
	seen := map[string]struct{}{}
	terms := []string{}
	for _, term := range fullTextTermPattern.FindAllString(strings.ToLower(query), -1) {
		if len(term) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
		if len(terms) >= 16 {
			break
		}
	}
	if len(terms) == 0 {
		return "__abra_no_match__"
	}
	return strings.Join(terms, " | ")
}
