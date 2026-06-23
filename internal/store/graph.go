package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertEntity(ctx context.Context, entity EntityRecord) (string, error) {
	id := stableID("entity", entity.Scope, entity.EntityType, strings.ToLower(entity.Name))
	if entity.Confidence == 0 {
		entity.Confidence = 0.5
	}
	embedding := any(nil)
	if len(entity.Embedding) > 0 {
		embedding = vectorLiteral(entity.Embedding)
	}
	if entity.SourceConfigID == "" {
		entity.SourceConfigID = metadataString(entity.Metadata, "source_config_id")
	}
	if entity.IngestionJobID == "" {
		entity.IngestionJobID = metadataString(entity.Metadata, "ingestion_job_id")
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO entities (
		  id, scope, entity_type, canonical_name, description, authority,
		  authority_score, confidence, source_url, source_type, embedding,
		  source_config_id, ingestion_job_id, metadata
		)
		VALUES (
		  $1, $2, $3, $4, NULLIF($5, ''), 'extracted', 0.55, $6,
		  NULLIF($7, ''), NULLIF($8, ''), $9::vector, NULLIF($10, ''), NULLIF($11, ''), $12::jsonb
		)
		ON CONFLICT (id)
		DO UPDATE SET
		  description = COALESCE(NULLIF(EXCLUDED.description, ''), entities.description),
		  confidence = GREATEST(entities.confidence, EXCLUDED.confidence),
		  source_url = COALESCE(EXCLUDED.source_url, entities.source_url),
		  source_type = COALESCE(EXCLUDED.source_type, entities.source_type),
		  source_config_id = COALESCE(EXCLUDED.source_config_id, entities.source_config_id),
		  ingestion_job_id = COALESCE(EXCLUDED.ingestion_job_id, entities.ingestion_job_id),
		  updated_at = now(),
		  metadata = entities.metadata || EXCLUDED.metadata
	`, id, entity.Scope, entity.EntityType, entity.Name, entity.Description, entity.Confidence, entity.SourceURL, entity.SourceType, embedding, entity.SourceConfigID, entity.IngestionJobID, jsonb(entity.Metadata))
	return id, err
}

func (s *Store) UpsertRelation(ctx context.Context, relation RelationRecord) (string, error) {
	id := stableID("relation", relation.Scope, relation.RelationType, relation.SourceEntityID, relation.TargetEntityID, relation.SourceURL)
	if relation.Confidence == 0 {
		relation.Confidence = 0.5
	}
	if relation.SourceConfigID == "" {
		relation.SourceConfigID = metadataString(relation.Metadata, "source_config_id")
	}
	if relation.IngestionJobID == "" {
		relation.IngestionJobID = metadataString(relation.Metadata, "ingestion_job_id")
	}
	err := s.queryRunner().QueryRow(ctx, `
		UPDATE relations
		SET claim_id = COALESCE(NULLIF($2, ''), claim_id),
			    status = CASE
			      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true') THEN 'active'
			      ELSE status
			    END,
		    confidence = CASE
			      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true') THEN GREATEST(confidence, $3)
			      ELSE GREATEST(confidence, $3)
			    END,
		    source_url = COALESCE(NULLIF($4, ''), source_url),
		    source_type = COALESCE(NULLIF($5, ''), source_type),
		    source_config_id = COALESCE(NULLIF($6, ''), source_config_id),
		    ingestion_job_id = COALESCE(NULLIF($7, ''), ingestion_job_id),
		    last_verified_at = now(),
		    updated_at = now(),
		    metadata = CASE
			      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true')
			        THEN (metadata - 'source_refresh_deprecated' - 'source_refresh_deprecated_at' - 'source_refresh_job_id' - 'source_sync_deleted' - 'source_sync_deleted_at') || $8::jsonb || jsonb_build_object('source_refresh_reactivated_at', now()::text)
			      ELSE metadata || $8::jsonb
			    END
			WHERE id = $1
			  AND status != 'expired'
			  AND (status != 'deprecated' OR metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true')
		RETURNING id
	`, id, relation.ClaimID, relation.Confidence, relation.SourceURL, relation.SourceType, relation.SourceConfigID, relation.IngestionJobID, jsonb(relation.Metadata)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		INSERT INTO relations (
		  id, scope, relation_type, source_entity_id, target_entity_id, claim_id,
		  authority, authority_score, confidence, source_url, source_type,
		  source_config_id, ingestion_job_id, metadata
		)
		VALUES (
		  $1, $2, $3, $4, $5, NULLIF($6, ''), 'extracted', 0.55,
		  $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), $12::jsonb
		)
		ON CONFLICT (scope, relation_type, source_entity_id, target_entity_id)
		WHERE status NOT IN ('deprecated', 'expired')
		DO UPDATE SET
		  claim_id = COALESCE(EXCLUDED.claim_id, relations.claim_id),
		  confidence = GREATEST(relations.confidence, EXCLUDED.confidence),
		  source_url = COALESCE(EXCLUDED.source_url, relations.source_url),
		  source_type = COALESCE(EXCLUDED.source_type, relations.source_type),
		  source_config_id = COALESCE(EXCLUDED.source_config_id, relations.source_config_id),
		  ingestion_job_id = COALESCE(EXCLUDED.ingestion_job_id, relations.ingestion_job_id),
		  last_verified_at = now(),
		  updated_at = now(),
		  metadata = relations.metadata || EXCLUDED.metadata
		RETURNING id
	`, id, relation.Scope, relation.RelationType, relation.SourceEntityID, relation.TargetEntityID, relation.ClaimID, relation.Confidence, relation.SourceURL, relation.SourceType, relation.SourceConfigID, relation.IngestionJobID, jsonb(relation.Metadata)).Scan(&id)
	return id, err
}

func (s *Store) ListGraphEntities(ctx context.Context, scope string, limit int) ([]GraphEntityResult, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, canonical_name, entity_type, status, confidence, source_url, updated_at::text
		FROM entities
		WHERE ($1 = '' OR scope = $1)
		ORDER BY updated_at DESC, confidence DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := []GraphEntityResult{}
	for rows.Next() {
		var entity GraphEntityResult
		if err := rows.Scan(&entity.ID, &entity.Name, &entity.Type, &entity.Status, &entity.Confidence, &entity.SourceURL, &entity.UpdatedAt); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

func (s *Store) ListGraphRelations(ctx context.Context, scope string, limit int) ([]GraphRelationResult, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  r.id,
		  src.id,
		  src.canonical_name,
		  dst.id,
		  dst.canonical_name,
		  r.relation_type,
		  r.status,
		  r.confidence,
		  r.source_url,
		  r.updated_at::text
		FROM relations r
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		WHERE ($1 = '' OR r.scope = $1)
		ORDER BY r.updated_at DESC, r.confidence DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []GraphRelationResult{}
	for rows.Next() {
		var relation GraphRelationResult
		if err := rows.Scan(&relation.ID, &relation.FromID, &relation.FromEntity, &relation.ToID, &relation.ToEntity, &relation.Type, &relation.Status, &relation.Confidence, &relation.SourceURL, &relation.UpdatedAt); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (s *Store) ListActiveRelationsFromEntity(ctx context.Context, scope, sourceEntityID string, limit int) ([]GraphRelationResult, error) {
	scope = strings.TrimSpace(scope)
	sourceEntityID = strings.TrimSpace(sourceEntityID)
	if scope == "" || sourceEntityID == "" {
		return nil, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.queryRunner().Query(ctx, listActiveRelationsFromEntitySQL(), scope, sourceEntityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []GraphRelationResult{}
	for rows.Next() {
		var relation GraphRelationResult
		if err := rows.Scan(&relation.ID, &relation.FromID, &relation.FromEntity, &relation.ToID, &relation.ToEntity, &relation.Type, &relation.Status, &relation.Confidence, &relation.SourceURL, &relation.UpdatedAt); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func listActiveRelationsFromEntitySQL() string {
	return fmt.Sprintf(`
		SELECT
		  r.id,
		  src.id,
		  src.canonical_name,
		  dst.id,
		  dst.canonical_name,
		  r.relation_type,
		  r.status,
		  r.confidence,
		  r.source_url,
		  r.updated_at::text
		FROM relations r
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		WHERE r.scope = $1
		  AND r.source_entity_id = $2
		  AND r.status = 'active'
		  AND src.status NOT IN ('deprecated', 'deleted')
		  AND dst.status NOT IN ('deprecated', 'deleted')
		  AND %s
		  AND %s
		  AND %s
		ORDER BY r.confidence DESC, r.updated_at DESC
		LIMIT $3
	`, recordEffectiveSQL("r"), recordEffectiveSQL("src"), recordEffectiveSQL("dst"))
}

func (s *Store) RelatedGraph(ctx context.Context, query, scope string, limit int) ([]RelationResult, error) {
	return s.RelatedGraphWithOptions(ctx, query, scope, limit, RecallOptions{})
}

func (s *Store) ResolveEntity(ctx context.Context, scope, name string) (EntityResolutionResult, error) {
	scope = strings.TrimSpace(scope)
	name = strings.TrimSpace(name)
	if scope == "" || name == "" {
		return EntityResolutionResult{}, nil
	}
	var result EntityResolutionResult
	err := s.pool.QueryRow(ctx, `
		SELECT e.id, e.canonical_name, e.entity_type, e.status, e.confidence, e.source_url, e.updated_at::text
		FROM entities e
		LEFT JOIN entity_aliases ea
		  ON ea.entity_id = e.id
		 AND ea.scope = e.scope
		 AND ea.status = 'active'
		LEFT JOIN source_configs e_sc ON e_sc.id = e.source_config_id
		WHERE e.scope = $1
		  AND e.status = 'active'
		  AND COALESCE(e_sc.status, 'active') <> 'deleted'
		  AND (
		    lower(e.canonical_name) = lower($2)
		    OR lower(ea.alias) = lower($2)
		  )
		GROUP BY e.id, e.canonical_name, e.entity_type, e.status, e.confidence, e.source_url, e.updated_at
		ORDER BY
		  CASE WHEN lower(e.canonical_name) = lower($2) THEN 0 ELSE 1 END,
		  e.confidence DESC,
		  e.updated_at DESC
		LIMIT 1
	`, scope, name).Scan(&result.ID, &result.Name, &result.Type, &result.Status, &result.Confidence, &result.SourceURL, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EntityResolutionResult{}, nil
	}
	if err != nil {
		return EntityResolutionResult{}, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT alias
		FROM entity_aliases
		WHERE entity_id = $1
		  AND scope = $2
		  AND status = 'active'
		ORDER BY confidence DESC, updated_at DESC, alias ASC
		LIMIT 12
	`, result.ID, scope)
	if err != nil {
		return EntityResolutionResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return EntityResolutionResult{}, err
		}
		if strings.TrimSpace(alias) != "" && !strings.EqualFold(alias, result.Name) {
			result.Aliases = append(result.Aliases, alias)
		}
	}
	return result, rows.Err()
}

func (s *Store) RelatedGraphWithOptions(ctx context.Context, query, scope string, limit int, options RecallOptions) ([]RelationResult, error) {
	if limit < 1 || limit > 50 {
		limit = 8
	}
	anyQuery := fullTextAnyQuery(query)
	effectiveAt := recallEffectiveAt(options)
	rows, err := s.pool.Query(ctx, relatedGraphSQL(options.IncludeHistorical), query, scope, limit, anyQuery, effectiveAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RelationResult
	for rows.Next() {
		var relation RelationResult
		if err := rows.Scan(&relation.ID, &relation.ClaimID, &relation.FromEntity, &relation.ToEntity, &relation.Type, &relation.Status, &relation.Freshness, &relation.Confidence, &relation.SourceURL); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func relatedGraphSQL(includeHistorical bool) string {
	relationStatusFilter := "r.status = 'active'"
	relationLifecycleFilter := recordEffectiveAtSQL("r", "$5")
	if includeHistorical {
		relationStatusFilter = "r.status NOT IN ('deprecated')"
		relationLifecycleFilter = "true"
	}
	return fmt.Sprintf(`
		WITH seed_entities AS (
		  SELECT e.id
		  FROM entities e
		  LEFT JOIN entity_aliases ea
		    ON ea.entity_id = e.id
		   AND ea.scope = e.scope
		   AND ea.status NOT IN ('deprecated', 'deleted')
		  LEFT JOIN source_configs e_sc ON e_sc.id = e.source_config_id
		  WHERE e.scope = $2
		    AND e.status NOT IN ('deprecated', 'deleted')
		    AND COALESCE(e_sc.status, 'active') <> 'deleted'
		    AND %s
		    AND (
		      e.search_vector @@ plainto_tsquery('simple', $1)
		      OR e.search_vector @@ to_tsquery('simple', $4)
		      OR e.canonical_name ILIKE '%%' || $1 || '%%'
		      OR ea.alias ILIKE '%%' || $1 || '%%'
		    )
		  GROUP BY e.id, e.confidence, e.updated_at
		  ORDER BY e.confidence DESC, e.updated_at DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		seed_edges AS (
		  SELECT r.id, 1 AS distance, 1.0::double precision AS seed_score
		  FROM relations r
		  JOIN entities src ON src.id = r.source_entity_id
		  JOIN entities dst ON dst.id = r.target_entity_id
		  LEFT JOIN source_configs r_sc ON r_sc.id = r.source_config_id
		  LEFT JOIN source_configs src_sc ON src_sc.id = src.source_config_id
		  LEFT JOIN source_configs dst_sc ON dst_sc.id = dst.source_config_id
			  WHERE r.scope = $2
			    AND %s
			    AND src.status NOT IN ('deprecated', 'deleted')
			    AND dst.status NOT IN ('deprecated', 'deleted')
			    AND COALESCE(r_sc.status, 'active') <> 'deleted'
			    AND COALESCE(src_sc.status, 'active') <> 'deleted'
				    AND COALESCE(dst_sc.status, 'active') <> 'deleted'
				    AND %s
				    AND %s
				    AND %s
				    AND (
		      r.source_entity_id IN (SELECT id FROM seed_entities)
		      OR r.target_entity_id IN (SELECT id FROM seed_entities)
		      OR src.search_vector @@ plainto_tsquery('simple', $1)
		      OR src.search_vector @@ to_tsquery('simple', $4)
		      OR dst.search_vector @@ plainto_tsquery('simple', $1)
		      OR dst.search_vector @@ to_tsquery('simple', $4)
		      OR r.relation_type ILIKE '%%' || $1 || '%%'
		    )
		  ORDER BY r.confidence DESC, r.updated_at DESC
		  LIMIT GREATEST($3 * 4, 16)
		),
		frontier_entities AS (
		  SELECT id FROM seed_entities
		  UNION
		  SELECT r.source_entity_id
		  FROM relations r
		  JOIN seed_edges se ON se.id = r.id
		  UNION
		  SELECT r.target_entity_id
		  FROM relations r
		  JOIN seed_edges se ON se.id = r.id
		),
		neighbor_edges AS (
		  SELECT r.id, 2 AS distance, 0.65::double precision AS seed_score
		  FROM relations r
		  JOIN entities src ON src.id = r.source_entity_id
		  JOIN entities dst ON dst.id = r.target_entity_id
		  LEFT JOIN source_configs r_sc ON r_sc.id = r.source_config_id
		  LEFT JOIN source_configs src_sc ON src_sc.id = src.source_config_id
		  LEFT JOIN source_configs dst_sc ON dst_sc.id = dst.source_config_id
			  WHERE r.scope = $2
			    AND %s
			    AND src.status NOT IN ('deprecated', 'deleted')
			    AND dst.status NOT IN ('deprecated', 'deleted')
			    AND COALESCE(r_sc.status, 'active') <> 'deleted'
			    AND COALESCE(src_sc.status, 'active') <> 'deleted'
				    AND COALESCE(dst_sc.status, 'active') <> 'deleted'
				    AND %s
				    AND %s
				    AND %s
				    AND (
		      r.source_entity_id IN (SELECT id FROM frontier_entities)
		      OR r.target_entity_id IN (SELECT id FROM frontier_entities)
		    )
		  ORDER BY r.confidence DESC, r.updated_at DESC
		  LIMIT GREATEST($3 * 5, 20)
		),
		ranked_edges AS (
		  SELECT id, MIN(distance) AS distance, MAX(seed_score) AS seed_score
		  FROM (
		    SELECT * FROM seed_edges
		    UNION ALL
		    SELECT * FROM neighbor_edges
		  ) edges
		  GROUP BY id
		)
			SELECT
			  r.id,
			  COALESCE(r.claim_id, ''),
			  src.canonical_name,
			  dst.canonical_name,
			  r.relation_type,
			  r.status,
			  CASE
			    WHEN r.status = 'expired' OR (r.expires_at IS NOT NULL AND r.expires_at <= $5) OR r.freshness_status = 'expired' THEN 'expired'
			    WHEN r.freshness_status = 'stale' THEN 'stale'
			    WHEN r.last_verified_at IS NULL THEN 'unknown'
			    WHEN r.last_verified_at < $5 - interval '120 days' THEN 'stale'
			    ELSE 'fresh'
			  END AS freshness,
			  r.confidence,
			  r.source_url
			FROM ranked_edges ranked
		JOIN relations r ON r.id = ranked.id
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		LEFT JOIN source_configs r_sc ON r_sc.id = r.source_config_id
		LEFT JOIN source_configs src_sc ON src_sc.id = src.source_config_id
		LEFT JOIN source_configs dst_sc ON dst_sc.id = dst.source_config_id
			WHERE %s
			  AND %s
			  AND %s
			  AND COALESCE(r_sc.status, 'active') <> 'deleted'
		  AND COALESCE(src_sc.status, 'active') <> 'deleted'
		  AND COALESCE(dst_sc.status, 'active') <> 'deleted'
		ORDER BY
		  ranked.distance ASC,
		  (r.confidence * ranked.seed_score) DESC,
		  r.updated_at DESC
		LIMIT $3
		`,
		recordEffectiveAtSQL("e", "$5"),
		relationStatusFilter,
		relationLifecycleFilter,
		recordEffectiveAtSQL("src", "$5"),
		recordEffectiveAtSQL("dst", "$5"),
		relationStatusFilter,
		relationLifecycleFilter,
		recordEffectiveAtSQL("src", "$5"),
		recordEffectiveAtSQL("dst", "$5"),
		relationLifecycleFilter,
		recordEffectiveAtSQL("src", "$5"),
		recordEffectiveAtSQL("dst", "$5"),
	)
}
