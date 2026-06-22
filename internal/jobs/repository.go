package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/ingest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

const (
	defaultRetryBackoffBaseSeconds = 30
	defaultRetryBackoffMaxSeconds  = 15 * 60
)

func OpenRepository(ctx context.Context, databaseURL string) (*Repository, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse worker postgres config: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open worker postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping worker postgres: %w", err)
	}
	return &Repository{pool: pool}, nil
}

func (r *Repository) Close() {
	r.pool.Close()
}

func (r *Repository) ListEnabledLocalMarkdownSources(ctx context.Context, limit int) ([]SourceConfig, error) {
	if limit <= 0 {
		limit = DefaultMaxSourcesPerRun
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, scope, source_type, name, COALESCE(base_url, ''), connector_kind, authority, authority_score, config, metadata
		FROM source_configs
		WHERE status = 'active'
		  AND source_type IN ('local_repo', 'markdown', 'git_repo', 'mcp')
		ORDER BY priority ASC, updated_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []SourceConfig
	for rows.Next() {
		var source SourceConfig
		var configRaw, metadataRaw []byte
		if err := rows.Scan(
			&source.ID,
			&source.Scope,
			&source.SourceType,
			&source.Name,
			&source.BaseURL,
			&source.ConnectorKind,
			&source.Authority,
			&source.AuthorityScore,
			&configRaw,
			&metadataRaw,
		); err != nil {
			return nil, err
		}
		source.Config = decodeJSONMap(configRaw)
		source.Metadata = decodeJSONMap(metadataRaw)
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (r *Repository) RecoverStaleIngestionJobs(ctx context.Context, leaseTimeout time.Duration) (int64, error) {
	if leaseTimeout <= 0 {
		leaseTimeout = DefaultLeaseTimeout
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'retry' END,
		    lease_owner = NULL,
		    heartbeat_at = NULL,
		    finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END,
		    next_attempt_at = CASE
		      WHEN attempts >= max_attempts THEN NULL
		      ELSE now() + make_interval(secs => LEAST($4::int, $3::int * power(2, GREATEST(attempts - 1, 0))::int))
		    END,
		    error_message = COALESCE(error_message, 'lease expired'),
		    metadata = metadata || $2::jsonb,
		    updated_at = now()
		WHERE status = 'running'
		  AND (heartbeat_at IS NULL OR heartbeat_at < now() - ($1::text)::interval)
	`, fmt.Sprintf("%d seconds", int(leaseTimeout.Seconds())), jsonb(map[string]any{
		"recovered_at": time.Now().UTC().Format(time.RFC3339Nano),
		"reason":       "lease_expired",
	}), defaultRetryBackoffBaseSeconds, defaultRetryBackoffMaxSeconds)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repository) EnqueueScheduledSources(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = DefaultMaxSourcesPerRun
	}
	tag, err := r.pool.Exec(ctx, `
		WITH candidates AS (
		  SELECT
		    sc.id,
		    sc.scope,
		    sc.source_type,
		    sc.base_url,
		    sc.authority,
		    sc.name,
		    sc.connector_kind
		  FROM source_configs sc
		  CROSS JOIN LATERAL (
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
		  WHERE sc.status = 'active'
		    AND sc.source_type IN ('local_repo', 'markdown', 'git_repo', 'mcp')
		    AND NOT EXISTS (
		      SELECT 1
		      FROM ingestion_jobs ij
		      WHERE ij.source_config_id = sc.id
		        AND ij.status IN ('queued', 'retry', 'running')
		    )
		    AND (
		      sc.last_success_at IS NULL
		      OR sc.updated_at > sc.last_success_at
		      OR (freshness.freshness_interval IS NOT NULL AND sc.last_success_at < now() - freshness.freshness_interval)
		      OR (freshness.schedule_interval IS NOT NULL AND sc.last_success_at < now() - freshness.schedule_interval)
		    )
		  ORDER BY sc.priority ASC, sc.updated_at ASC, sc.id ASC
		  LIMIT $1
		  FOR UPDATE OF sc SKIP LOCKED
		)
		INSERT INTO ingestion_jobs (
		  id, source_config_id, scope, source_type, source_url, trigger_type,
		  status, authority, max_attempts, metadata
		)
		SELECT
		  md5('job:' || id || ':' || clock_timestamp()::text || ':' || random()::text),
		  id,
		  scope,
		  source_type,
		  base_url,
		  'schedule',
		  'queued',
		  authority,
		  3,
		  jsonb_build_object(
		    'source_config_name', name,
		    'connector_kind', connector_kind,
		    'queued_by', 'abra-worker'
		  )
		FROM candidates
	`, limit)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func (r *Repository) ClaimQueuedIngestionJobs(ctx context.Context, limit int, leaseOwner string) ([]QueuedIngestionJob, error) {
	if limit <= 0 {
		limit = DefaultMaxSourcesPerRun
	}
	if leaseOwner == "" {
		leaseOwner = DefaultLeaseOwner
	}
	rows, err := r.pool.Query(ctx, `
		WITH next_jobs AS (
		  SELECT ij.id
		  FROM ingestion_jobs ij
		  LEFT JOIN source_configs sc ON sc.id = ij.source_config_id
		  WHERE ij.status IN ('queued', 'retry')
		    AND ij.attempts < ij.max_attempts
		    AND (ij.next_attempt_at IS NULL OR ij.next_attempt_at <= now())
		    AND (
		      (sc.status IN ('active', 'error') AND sc.source_type IN ('local_repo', 'markdown', 'git_repo', 'mcp'))
		      OR (ij.trigger_type = 'webhook' AND EXISTS (
		        SELECT 1 FROM ingestion_job_documents payload WHERE payload.job_id = ij.id
		      ))
		    )
		  ORDER BY CASE WHEN ij.trigger_type = 'webhook' THEN -1 ELSE COALESCE(sc.priority, 1000) END ASC,
		           ij.created_at ASC,
		           ij.id ASC
		  LIMIT $2
		  FOR UPDATE OF ij SKIP LOCKED
		),
		claimed AS (
		  UPDATE ingestion_jobs ij
		  SET status = 'running',
		      lease_owner = $1,
		      heartbeat_at = now(),
		      started_at = now(),
		      finished_at = NULL,
		      next_attempt_at = NULL,
		      attempts = attempts + 1,
		      updated_at = now()
		  FROM next_jobs
		  WHERE ij.id = next_jobs.id
		  RETURNING
		    ij.id,
		    ij.trigger_type,
		    ij.attempts,
		    ij.max_attempts,
		    COALESCE(ij.source_config_id, '') AS source_config_id,
		    ij.scope,
		    ij.source_type,
		    ij.authority
		)
		SELECT
		  claimed.id,
		  claimed.trigger_type,
		  claimed.attempts,
		  claimed.max_attempts,
		  COALESCE(sc.id, ''),
		  COALESCE(sc.scope, claimed.scope),
		  COALESCE(sc.source_type, claimed.source_type),
		  COALESCE(sc.name, claimed.trigger_type || ':' || claimed.id),
		  COALESCE(sc.base_url, ''),
		  COALESCE(sc.connector_kind, ''),
		  COALESCE(sc.authority, claimed.authority),
		  COALESCE(sc.authority_score, 0),
		  COALESCE(sc.config, '{}'::jsonb),
		  COALESCE(sc.metadata, '{}'::jsonb)
		FROM claimed
		LEFT JOIN source_configs sc ON sc.id = claimed.source_config_id
		ORDER BY CASE WHEN claimed.trigger_type = 'webhook' THEN -1 ELSE COALESCE(sc.priority, 1000) END ASC,
		         claimed.id ASC
	`, leaseOwner, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []QueuedIngestionJob{}
	for rows.Next() {
		var job QueuedIngestionJob
		var configRaw, metadataRaw []byte
		if err := rows.Scan(
			&job.ID,
			&job.TriggerType,
			&job.Attempts,
			&job.MaxAttempts,
			&job.Source.ID,
			&job.Source.Scope,
			&job.Source.SourceType,
			&job.Source.Name,
			&job.Source.BaseURL,
			&job.Source.ConnectorKind,
			&job.Source.Authority,
			&job.Source.AuthorityScore,
			&configRaw,
			&metadataRaw,
		); err != nil {
			return nil, err
		}
		job.Source.Config = decodeJSONMap(configRaw)
		job.Source.Metadata = decodeJSONMap(metadataRaw)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (r *Repository) StartIngestionJob(ctx context.Context, source SourceConfig, triggerType string) (string, error) {
	if triggerType == "" {
		triggerType = "schedule"
	}
	jobID := ingestionJobID(source.ID, time.Now().UTC())
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ingestion_jobs (
		  id, source_config_id, scope, source_type, source_url, trigger_type,
		  status, authority, lease_owner, heartbeat_at, started_at, attempts,
		  metadata
		)
		VALUES (
		  $1, $2, $3, $4, NULLIF($5, ''), $6,
		  'running', $7, 'abra-worker', now(), now(), 1,
		  $8::jsonb
		)
	`, jobID, source.ID, source.Scope, string(source.SourceType), source.BaseURL, triggerType, source.Authority, jsonb(map[string]any{
		"source_config_name": source.Name,
		"connector_kind":     source.Metadata["connector_kind"],
	}))
	if err != nil {
		return "", err
	}
	return jobID, nil
}

func (r *Repository) HeartbeatIngestionJob(ctx context.Context, jobID string, leaseOwner string) error {
	if leaseOwner == "" {
		leaseOwner = DefaultLeaseOwner
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET heartbeat_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = $2
	`, jobID, leaseOwner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		status, statusErr := r.jobStatus(ctx, jobID)
		if statusErr != nil {
			return statusErr
		}
		return fmt.Errorf("job %q is no longer running for lease owner %q (status %q)", jobID, leaseOwner, status)
	}
	return nil
}

func (r *Repository) GetWebhookDocument(ctx context.Context, jobID string) (IngestDocumentInput, error) {
	var doc IngestDocumentInput
	var metadataRaw []byte
	err := r.pool.QueryRow(ctx, `
		SELECT
		  scope,
		  source_type,
		  source_url,
		  COALESCE(source_id, ''),
		  title,
		  content,
		  COALESCE(source_updated_at, ''),
		  metadata
		FROM ingestion_job_documents
		WHERE job_id = $1
		ORDER BY document_index ASC
		LIMIT 1
	`, jobID).Scan(
		&doc.Scope,
		&doc.SourceType,
		&doc.SourceURL,
		&doc.SourceID,
		&doc.Title,
		&doc.Content,
		&doc.SourceUpdatedAt,
		&metadataRaw,
	)
	if err == pgx.ErrNoRows {
		return IngestDocumentInput{}, fmt.Errorf("webhook payload for job %q not found", jobID)
	}
	if err != nil {
		return IngestDocumentInput{}, err
	}
	doc.Metadata = decodeJSONMap(metadataRaw)
	return doc, nil
}

func (r *Repository) FinishIngestionJob(ctx context.Context, jobID string, leaseOwner string, stats SourceStats, runErr error) (string, error) {
	if leaseOwner == "" {
		leaseOwner = DefaultLeaseOwner
	}
	if runErr != nil {
		var status string
		retryable := shouldRetryIngestionJob(runErr)
		metadata := map[string]any{
			"documents_skipped":       stats.DocumentsSkipped,
			"documents_deferred":      stats.DocumentsDeferred,
			"files_skipped_large":     stats.FilesSkippedLarge,
			"files_skipped_binary":    stats.FilesSkippedBinary,
			"files_skipped_generated": stats.FilesSkippedGenerated,
		}
		for key, value := range providerFailureMetadata(runErr) {
			metadata[key] = value
		}
		err := r.pool.QueryRow(ctx, `
			UPDATE ingestion_jobs
			SET status = CASE WHEN attempts >= max_attempts OR NOT $11::boolean THEN 'failed' ELSE 'retry' END,
			    lease_owner = NULL,
			    heartbeat_at = NULL,
			    finished_at = CASE WHEN attempts >= max_attempts OR NOT $11::boolean THEN now() ELSE NULL END,
			    next_attempt_at = CASE
			      WHEN attempts >= max_attempts OR NOT $11::boolean THEN NULL
			      ELSE now() + make_interval(secs => LEAST($10::int, $9::int * power(2, GREATEST(attempts - 1, 0))::int))
			    END,
			    documents_seen = $2,
			    documents_changed = $3,
			    chunks_written = $4,
			    claims_written = $5,
			    error_message = $6,
			    metadata = metadata || $7::jsonb,
			    updated_at = now()
			WHERE id = $1
			  AND status = 'running'
			  AND lease_owner = $8
			RETURNING status
		`, jobID, stats.DocumentsSeen, stats.DocumentsChanged, stats.ChunksWritten, stats.ClaimsWritten, runErr.Error(), jsonb(metadata), leaseOwner, defaultRetryBackoffBaseSeconds, defaultRetryBackoffMaxSeconds, retryable).Scan(&status)
		if err == pgx.ErrNoRows {
			return r.jobStatus(ctx, jobID)
		}
		return status, err
	}
	var status string
	err := r.pool.QueryRow(ctx, `
		UPDATE ingestion_jobs
		SET status = 'succeeded',
		    lease_owner = NULL,
		    heartbeat_at = NULL,
		    finished_at = now(),
		    next_attempt_at = NULL,
		    documents_seen = $2,
		    documents_changed = $3,
		    chunks_written = $4,
		    claims_written = $5,
		    error_message = NULL,
		    metadata = metadata || $6::jsonb,
		    updated_at = now()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = $7
		RETURNING status
	`, jobID, stats.DocumentsSeen, stats.DocumentsChanged, stats.ChunksWritten, stats.ClaimsWritten, jsonb(map[string]any{
		"documents_skipped":       stats.DocumentsSkipped,
		"documents_deferred":      stats.DocumentsDeferred,
		"files_skipped_large":     stats.FilesSkippedLarge,
		"files_skipped_binary":    stats.FilesSkippedBinary,
		"files_skipped_generated": stats.FilesSkippedGenerated,
	}), leaseOwner).Scan(&status)
	if err == pgx.ErrNoRows {
		return r.jobStatus(ctx, jobID)
	}
	return status, err
}

func shouldRetryIngestionJob(err error) bool {
	if providerErr, ok := ai.ProviderErrorInfo(err); ok {
		return providerErr.Retryable
	}
	return true
}

func providerFailureMetadata(err error) map[string]any {
	providerErr, ok := ai.ProviderErrorInfo(err)
	if !ok {
		return nil
	}
	metadata := map[string]any{
		"error_component":    "ai_provider",
		"error_class":        providerErr.Code,
		"provider_operation": providerErr.Operation,
		"provider_retryable": providerErr.Retryable,
		"provider_attempts":  providerErr.Attempts,
	}
	if providerErr.Provider != "" {
		metadata["provider_name"] = providerErr.Provider
	}
	if providerErr.Model != "" {
		metadata["provider_model"] = providerErr.Model
	}
	if providerErr.Status > 0 {
		metadata["provider_status"] = providerErr.Status
	}
	if providerErr.BatchSize > 0 {
		metadata["provider_batch_size"] = providerErr.BatchSize
		metadata["provider_batch_start"] = providerErr.BatchStart
		metadata["provider_batch_end"] = providerErr.BatchEnd
	}
	if providerErr.BatchTokens > 0 {
		metadata["provider_batch_tokens"] = providerErr.BatchTokens
	}
	return metadata
}

func (r *Repository) jobStatus(ctx context.Context, jobID string) (string, error) {
	var status string
	err := r.pool.QueryRow(ctx, `SELECT status FROM ingestion_jobs WHERE id = $1`, jobID).Scan(&status)
	return status, err
}

func (r *Repository) DocumentState(ctx context.Context, doc ingest.Document) (DocumentState, error) {
	var state DocumentState
	err := r.pool.QueryRow(ctx, `
		SELECT content_checksum,
		       COALESCE(metadata->>'ingest_checksum', ''),
		       COALESCE(metadata->>'ingest_fingerprint', ''),
		       COALESCE(metadata->>'ingest_complete' = 'true', false)
		FROM documents
		WHERE source_type = $1
		  AND source_url = $2
		  AND scope = $3
		LIMIT 1
	`, string(doc.SourceType), doc.SourceURL, doc.Scope).Scan(
		&state.ContentChecksum,
		&state.IngestChecksum,
		&state.IngestFingerprint,
		&state.IngestComplete,
	)
	if err == pgx.ErrNoRows {
		return DocumentState{}, nil
	}
	if err != nil {
		return DocumentState{}, err
	}
	state.Found = true
	return state, nil
}

func (r *Repository) DocumentStates(ctx context.Context, docs []ingest.Document) (map[string]DocumentState, error) {
	states := make(map[string]DocumentState, len(docs))
	if len(docs) == 0 {
		return states, nil
	}
	sourceTypes := make([]string, 0, len(docs))
	sourceURLs := make([]string, 0, len(docs))
	scopes := make([]string, 0, len(docs))
	for _, doc := range docs {
		sourceTypes = append(sourceTypes, string(doc.SourceType))
		sourceURLs = append(sourceURLs, doc.SourceURL)
		scopes = append(scopes, doc.Scope)
	}
	rows, err := r.pool.Query(ctx, `
		WITH requested AS (
		  SELECT source_type, source_url, scope
		  FROM unnest($1::text[], $2::text[], $3::text[]) AS input(source_type, source_url, scope)
		)
		SELECT requested.source_type,
		       requested.source_url,
		       requested.scope,
		       d.content_checksum,
		       COALESCE(d.metadata->>'ingest_checksum', ''),
		       COALESCE(d.metadata->>'ingest_fingerprint', ''),
		       COALESCE(d.metadata->>'ingest_complete' = 'true', false)
		FROM requested
		JOIN LATERAL (
		  SELECT content_checksum, metadata
		  FROM documents
		  WHERE source_type = requested.source_type
		    AND source_url = requested.source_url
		    AND scope = requested.scope
		  LIMIT 1
		) d ON true
	`, sourceTypes, sourceURLs, scopes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceType, sourceURL, scope string
		var state DocumentState
		if err := rows.Scan(
			&sourceType,
			&sourceURL,
			&scope,
			&state.ContentChecksum,
			&state.IngestChecksum,
			&state.IngestFingerprint,
			&state.IngestComplete,
		); err != nil {
			return nil, err
		}
		state.Found = true
		doc := ingest.Document{SourceType: ingest.SourceType(sourceType), SourceURL: sourceURL, Scope: scope}
		states[documentStateKey(doc)] = state
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return states, nil
}

func (r *Repository) MarkSourceSuccess(ctx context.Context, sourceID string, stats SourceStats) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, `
		UPDATE source_configs
		SET last_success_at = CASE WHEN $3::boolean THEN now() ELSE last_success_at END,
		    last_error_at = NULL,
		    last_error = NULL,
		    status = CASE WHEN status = 'error' THEN 'active' ELSE status END,
		    metadata = metadata || $2::jsonb
		WHERE id = $1
	`, sourceID, jsonb(map[string]any{
		"last_worker_documents_seen":          stats.DocumentsSeen,
		"last_worker_documents_changed":       stats.DocumentsChanged,
		"last_worker_documents_skipped":       stats.DocumentsSkipped,
		"last_worker_documents_deferred":      stats.DocumentsDeferred,
		"last_worker_files_skipped_large":     stats.FilesSkippedLarge,
		"last_worker_files_skipped_binary":    stats.FilesSkippedBinary,
		"last_worker_files_skipped_generated": stats.FilesSkippedGenerated,
		"last_worker_chunks_written":          stats.ChunksWritten,
		"last_worker_claims_written":          stats.ClaimsWritten,
	}), sourceFullyDrained(stats)); err != nil {
		return err
	}
	if err := retireMissingSourceDocuments(ctx, tx, sourceID, stats.SourceDocuments); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retireMissingSourceDocuments(ctx context.Context, tx pgx.Tx, sourceID string, refs []SourceDocumentRef) error {
	if sourceID == "" || len(refs) == 0 {
		return nil
	}
	sourceTypes := make([]string, 0, len(refs))
	sourceURLs := make([]string, 0, len(refs))
	scopes := make([]string, 0, len(refs))
	for _, ref := range refs {
		sourceTypes = append(sourceTypes, ref.SourceType)
		sourceURLs = append(sourceURLs, ref.SourceURL)
		scopes = append(scopes, ref.Scope)
	}
	_, err := tx.Exec(ctx, retireMissingSourceDocumentsSQL(), sourceID, sourceTypes, sourceURLs, scopes)
	return err
}

func retireMissingSourceDocumentsSQL() string {
	return `
		WITH seen AS (
		  SELECT source_type, source_url, scope
		  FROM unnest($2::text[], $3::text[], $4::text[]) AS input(source_type, source_url, scope)
		),
		retired_docs AS (
		  UPDATE documents d
		  SET status = 'deleted',
		      freshness_status = 'expired',
		      freshness_checked_at = now(),
		      updated_at = now(),
		      metadata = metadata || jsonb_build_object(
		        'source_sync_deleted', true,
		        'source_sync_deleted_at', now()::text
		      )
		  WHERE d.source_config_id = $1
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND NOT EXISTS (
		      SELECT 1
		      FROM seen
		      WHERE seen.source_type = d.source_type
		        AND seen.source_url = d.source_url
		        AND seen.scope = d.scope
		    )
		  RETURNING d.scope, d.source_type, d.source_url
		),
		retired_claims AS (
		  UPDATE claims c
		  SET status = 'deprecated',
		      confidence = 0,
		      updated_at = now(),
		      metadata = metadata || jsonb_build_object(
		        'source_sync_deleted', true,
		        'source_sync_deleted_at', now()::text
		      )
		  WHERE c.source_config_id = $1
		    AND c.status NOT IN ('deprecated', 'expired')
		    AND EXISTS (
		      SELECT 1
		      FROM retired_docs d
		      WHERE d.scope = c.scope
		        AND COALESCE(d.source_type, '') = COALESCE(c.source_type, '')
		        AND COALESCE(d.source_url, '') = COALESCE(c.source_url, '')
		    )
		  RETURNING c.id
		),
		retired_relations AS (
		  UPDATE relations r
		  SET status = 'deprecated',
		      confidence = 0,
		      updated_at = now(),
		      metadata = metadata || jsonb_build_object(
		        'source_sync_deleted', true,
		        'source_sync_deleted_at', now()::text
		      )
		  WHERE r.source_config_id = $1
		    AND r.status NOT IN ('deprecated', 'expired')
		    AND EXISTS (
		      SELECT 1
		      FROM retired_docs d
		      WHERE d.scope = r.scope
		        AND COALESCE(d.source_url, '') = COALESCE(r.source_url, '')
		    )
		  RETURNING r.id
		)
		DELETE FROM memory_summaries ms
		WHERE EXISTS (
		  SELECT 1
		  FROM retired_docs d
		  WHERE d.scope = ms.scope
		    AND ms.source_urls @> jsonb_build_array(d.source_url)
		)
	`
}

func sourceFullyDrained(stats SourceStats) bool {
	return stats.DocumentsDeferred == 0
}

func (r *Repository) MarkSourceError(ctx context.Context, sourceID string, sourceErr error) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE source_configs
		SET last_error_at = now(),
		    last_error = $2,
		    status = 'error'
		WHERE id = $1
	`, sourceID, sourceErr.Error())
	return err
}

func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func jsonb(value map[string]any) string {
	if value == nil {
		value = map[string]any{}
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func ingestionJobID(sourceID string, now time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("job:%s:%d", sourceID, now.UnixNano())))
	return hex.EncodeToString(sum[:])
}
