package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Store) UpsertSourceConfig(ctx context.Context, source SourceConfigRecord) (string, error) {
	if source.ID == "" {
		source.ID = stableID("source", source.Scope, source.SourceType, source.Name)
	}
	if source.ConnectorKind == "" {
		source.ConnectorKind = "generic"
	}
	if source.Status == "" {
		source.Status = "active"
	}
	if source.Authority == "" {
		source.Authority = "manual-unverified"
	}
	if source.AuthorityScore == 0 {
		source.AuthorityScore = 0.35
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO source_configs (
		  id, scope, source_type, name, base_url, connector_kind, status,
		  authority, authority_score, freshness_policy, schedule_cron, config, metadata, created_by
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10::jsonb, NULLIF($11, ''), $12::jsonb, $13::jsonb, NULLIF($14, ''))
		ON CONFLICT (scope, source_type, name)
		DO UPDATE SET
		  base_url = EXCLUDED.base_url,
		  connector_kind = EXCLUDED.connector_kind,
		  status = EXCLUDED.status,
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  freshness_policy = EXCLUDED.freshness_policy,
		  schedule_cron = EXCLUDED.schedule_cron,
		  config = EXCLUDED.config,
		  metadata = source_configs.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, source.ID, source.Scope, source.SourceType, source.Name, source.BaseURL, source.ConnectorKind, source.Status, source.Authority, source.AuthorityScore, jsonb(source.FreshnessPolicy), source.ScheduleCron, jsonb(source.Config), jsonb(source.Metadata), source.CreatedBy)
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		SELECT id
		FROM source_configs
		WHERE scope = $1 AND source_type = $2 AND name = $3
	`, source.Scope, source.SourceType, source.Name).Scan(&source.ID)
	return source.ID, err
}

func (s *Store) UpdateSourceConfigStatus(ctx context.Context, id, status string, metadata map[string]any) (SourceConfigRecord, error) {
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if id == "" || status == "" {
		return SourceConfigRecord{}, fmt.Errorf("source_config_id and status are required")
	}
	switch status {
	case "active", "paused", "disabled", "deleted", "error":
	default:
		return SourceConfigRecord{}, fmt.Errorf("source status must be active, paused, disabled, deleted, or error")
	}
	tag, err := s.queryRunner().Exec(ctx, `
		UPDATE source_configs
		SET status = $2,
		    metadata = metadata || $3::jsonb,
		    updated_at = now()
		WHERE id = $1
	`, id, status, jsonb(metadata))
	if err != nil {
		return SourceConfigRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		return SourceConfigRecord{}, fmt.Errorf("source_config_id %q not found", id)
	}
	return s.GetSourceConfig(ctx, id)
}

func (s *Store) ListSourceConfigs(ctx context.Context, scope string, limit int) ([]SourceConfigRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id, scope, source_type, name, COALESCE(base_url, ''), connector_kind,
		  status, authority, authority_score, freshness_policy, COALESCE(schedule_cron, ''), config, metadata,
		  last_success_at::text, last_error_at::text, last_error, COALESCE(created_by, '')
		FROM source_configs
		WHERE ($1 = '' OR scope = $1)
		ORDER BY priority ASC, updated_at DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []SourceConfigRecord{}
	for rows.Next() {
		var source SourceConfigRecord
		var freshnessPolicyRaw, configRaw, metadataRaw []byte
		if err := rows.Scan(
			&source.ID,
			&source.Scope,
			&source.SourceType,
			&source.Name,
			&source.BaseURL,
			&source.ConnectorKind,
			&source.Status,
			&source.Authority,
			&source.AuthorityScore,
			&freshnessPolicyRaw,
			&source.ScheduleCron,
			&configRaw,
			&metadataRaw,
			&source.LastSuccessAt,
			&source.LastErrorAt,
			&source.LastError,
			&source.CreatedBy,
		); err != nil {
			return nil, err
		}
		source.FreshnessPolicy = decodeJSONMap(freshnessPolicyRaw)
		source.Config = decodeJSONMap(configRaw)
		source.Metadata = decodeJSONMap(metadataRaw)
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *Store) ListScopes(ctx context.Context, limit int) ([]ScopeSummary, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > maxListScopesLimit {
		limit = maxListScopesLimit
	}
	rows, err := s.pool.Query(ctx, `
		WITH known_scopes AS (
		  SELECT scope FROM documents
		  UNION
		  SELECT scope FROM claims
		  UNION
		  SELECT scope FROM observations
		  UNION
		  SELECT scope FROM memory_summaries
		  UNION
		  SELECT scope FROM entities
		  UNION
		  SELECT scope FROM relations
		  UNION
		  SELECT scope FROM conflicts
		  UNION
		  SELECT scope FROM source_configs
		  UNION
		  SELECT scope FROM ingestion_jobs
		)
		SELECT
		  known_scopes.scope,
		  (SELECT COUNT(*) FROM documents WHERE documents.scope = known_scopes.scope) AS documents,
		  (SELECT COUNT(*) FROM claims WHERE claims.scope = known_scopes.scope) AS claims,
		  (SELECT COUNT(*) FROM observations WHERE observations.scope = known_scopes.scope) AS observations,
		  (SELECT COUNT(*) FROM memory_summaries WHERE memory_summaries.scope = known_scopes.scope) AS summaries,
		  (SELECT COUNT(*) FROM entities WHERE entities.scope = known_scopes.scope) AS entities,
		  (SELECT COUNT(*) FROM relations WHERE relations.scope = known_scopes.scope) AS relations,
		  (SELECT COUNT(*) FROM conflicts WHERE conflicts.scope = known_scopes.scope) AS conflicts,
		  (SELECT COUNT(*) FROM source_configs WHERE source_configs.scope = known_scopes.scope) AS sources,
		  (SELECT COUNT(*) FROM ingestion_jobs WHERE ingestion_jobs.scope = known_scopes.scope) AS jobs
		FROM known_scopes
		WHERE TRIM(known_scopes.scope) <> ''
		ORDER BY documents DESC, claims DESC, observations DESC, summaries DESC, relations DESC, entities DESC, conflicts DESC, sources DESC, jobs DESC, known_scopes.scope ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := []ScopeSummary{}
	for rows.Next() {
		var scope ScopeSummary
		if err := rows.Scan(&scope.Scope, &scope.Documents, &scope.Claims, &scope.Observations, &scope.Summaries, &scope.Entities, &scope.Relations, &scope.Conflicts, &scope.Sources, &scope.Jobs); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

func (s *Store) GetSourceConfig(ctx context.Context, id string) (SourceConfigRecord, error) {
	var source SourceConfigRecord
	var freshnessPolicyRaw, configRaw, metadataRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT
		  id, scope, source_type, name, COALESCE(base_url, ''), connector_kind,
		  status, authority, authority_score, freshness_policy, COALESCE(schedule_cron, ''), config, metadata,
		  last_success_at::text, last_error_at::text, last_error, COALESCE(created_by, '')
		FROM source_configs
		WHERE id = $1
	`, strings.TrimSpace(id)).Scan(
		&source.ID,
		&source.Scope,
		&source.SourceType,
		&source.Name,
		&source.BaseURL,
		&source.ConnectorKind,
		&source.Status,
		&source.Authority,
		&source.AuthorityScore,
		&freshnessPolicyRaw,
		&source.ScheduleCron,
		&configRaw,
		&metadataRaw,
		&source.LastSuccessAt,
		&source.LastErrorAt,
		&source.LastError,
		&source.CreatedBy,
	)
	if err == pgx.ErrNoRows {
		return SourceConfigRecord{}, fmt.Errorf("source_config_id %q not found", strings.TrimSpace(id))
	}
	if err != nil {
		return SourceConfigRecord{}, err
	}
	source.FreshnessPolicy = decodeJSONMap(freshnessPolicyRaw)
	source.Config = decodeJSONMap(configRaw)
	source.Metadata = decodeJSONMap(metadataRaw)
	return source, nil
}

func (s *Store) ListIngestionJobs(ctx context.Context, scope, sourceConfigID string, limit int) ([]IngestionJobRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id,
		  COALESCE(source_config_id, ''),
		  scope,
		  source_type,
		  COALESCE(source_url, ''),
		  trigger_type,
		  status,
		  authority,
		  COALESCE(lease_owner, ''),
		  heartbeat_at::text,
		  started_at::text,
		  finished_at::text,
		  attempts,
		  max_attempts,
		  documents_seen,
		  documents_changed,
		  chunks_written,
		  claims_written,
		  error_message,
		  COALESCE(created_by, ''),
		  created_at::text,
		  updated_at::text,
		  metadata
		FROM ingestion_jobs
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR source_config_id = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, scope, sourceConfigID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []IngestionJobRecord{}
	for rows.Next() {
		var job IngestionJobRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&job.ID,
			&job.SourceConfigID,
			&job.Scope,
			&job.SourceType,
			&job.SourceURL,
			&job.TriggerType,
			&job.Status,
			&job.Authority,
			&job.LeaseOwner,
			&job.HeartbeatAt,
			&job.StartedAt,
			&job.FinishedAt,
			&job.Attempts,
			&job.MaxAttempts,
			&job.DocumentsSeen,
			&job.DocumentsChanged,
			&job.ChunksWritten,
			&job.ClaimsWritten,
			&job.ErrorMessage,
			&job.CreatedBy,
			&job.CreatedAt,
			&job.UpdatedAt,
			&metadataRaw,
		); err != nil {
			return nil, err
		}
		job.Metadata = decodeJSONMap(metadataRaw)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}
