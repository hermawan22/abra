package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type stringMapScanner struct {
	values map[string]string
	key    string
}

type memoryHealthAssessment struct {
	Score   int
	Status  string
	Reasons []string
	Signals []MemoryHealthSignal
}

type memoryHealthPenalizer func(points int, code, category, severity string, count int, reason, action string)

func memorySummaryLevelConstraintReady(definition string) bool {
	definition = strings.ToLower(definition)
	for _, level := range []string{"core", "agent_core", "shared"} {
		if !strings.Contains(definition, "'"+level+"'") {
			return false
		}
	}
	return true
}

func (s *Store) UpsertMemorySummary(ctx context.Context, summary MemorySummaryRecord) (string, error) {
	if summary.Scope == "" || summary.Level == "" || summary.Key == "" || summary.Title == "" || summary.Summary == "" {
		return "", fmt.Errorf("scope, level, key, title, and summary are required")
	}
	id := stableID("summary", summary.Scope, summary.Level, summary.Key)
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO memory_summaries (
		  id, scope, level, summary_key, title, summary, source_count,
		  relation_count, token_estimate, source_urls, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb)
		ON CONFLICT (scope, level, summary_key)
		DO UPDATE SET
		  title = EXCLUDED.title,
		  summary = EXCLUDED.summary,
		  source_count = EXCLUDED.source_count,
		  relation_count = EXCLUDED.relation_count,
		  token_estimate = EXCLUDED.token_estimate,
		  source_urls = EXCLUDED.source_urls,
		  metadata = memory_summaries.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, id, summary.Scope, summary.Level, summary.Key, summary.Title, summary.Summary, summary.SourceCount, summary.RelationCount, summary.TokenEstimate, jsonArray(summary.SourceURLs), jsonb(summary.Metadata))
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		SELECT id
		FROM memory_summaries
		WHERE scope = $1 AND level = $2 AND summary_key = $3
	`, summary.Scope, summary.Level, summary.Key).Scan(&id)
	return id, err
}

func (s *Store) ListMemorySummaries(ctx context.Context, query, scope string, limit int) ([]MemorySummaryResult, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, memorySummarySelectSQL(), strings.TrimSpace(query), strings.TrimSpace(scope), limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemorySummaryResults(rows)
}

func (s *Store) ListMemorySummariesByLevels(ctx context.Context, query, scope string, levels []string, limit int) ([]MemorySummaryResult, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	levels = normalizedSummaryLevels(levels)
	if len(levels) == 0 {
		return nil, nil
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, memorySummarySelectByLevelsSQL(), strings.TrimSpace(query), strings.TrimSpace(scope), limit, anyQuery, levels)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemorySummaryResults(rows)
}

func scanMemorySummaryResults(rows pgx.Rows) ([]MemorySummaryResult, error) {
	out := []MemorySummaryResult{}
	for rows.Next() {
		var item MemorySummaryResult
		var urlsRaw, metadataRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.Scope,
			&item.Level,
			&item.Key,
			&item.Title,
			&item.Summary,
			&item.SourceCount,
			&item.RelationCount,
			&item.TokenEstimate,
			&urlsRaw,
			&metadataRaw,
			&item.Rank,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.SourceURLs = decodeJSONStringSlice(urlsRaw)
		item.Metadata = decodeJSONMap(metadataRaw)
		out = append(out, item)
	}
	return out, rows.Err()
}

func normalizedSummaryLevels(levels []string) []string {
	allowed := map[string]struct{}{
		"source": {}, "repo": {}, "module": {}, "file": {}, "route": {}, "component": {}, "symbol": {}, "package": {}, "decision": {}, "core": {}, "agent_core": {}, "shared": {},
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, level := range levels {
		level = strings.ToLower(strings.TrimSpace(level))
		if _, ok := allowed[level]; !ok {
			continue
		}
		if _, ok := seen[level]; ok {
			continue
		}
		seen[level] = struct{}{}
		out = append(out, level)
	}
	return out
}

func (s *Store) MemoryHealth(ctx context.Context, scope string) (MemoryHealthResult, error) {
	scope = strings.TrimSpace(scope)
	result := MemoryHealthResult{
		Scope:       scope,
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		LastUpdated: map[string]string{},
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, 'active') = 'active')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'stale')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'deprecated')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'deleted')::int,
		  COALESCE(MAX(ingested_at)::text, '')
			FROM documents
			WHERE ($1 = '' OR scope = $1)
			  AND NOT EXISTS (
			    SELECT 1 FROM source_configs sc
			    WHERE sc.id = documents.source_config_id
			      AND sc.status = 'deleted'
			  )
		`, scope).Scan(
		&result.Documents.Total,
		&result.Documents.Active,
		&result.Documents.Stale,
		&result.Documents.Deprecated,
		&result.Documents.Deleted,
		stringMapTarget(result.LastUpdated, "documents"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(DISTINCT claims.id)::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'verified')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'inferred')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'unverified')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'challenged')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'deprecated')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'expired')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE freshness_status = 'stale' OR expires_at < now())::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE evidence.id IS NOT NULL)::int,
		  COALESCE(MAX(claims.updated_at)::text, '')
			FROM claims
			LEFT JOIN evidence ON evidence.claim_id = claims.id
			WHERE ($1 = '' OR claims.scope = $1)
			  AND NOT EXISTS (
			    SELECT 1 FROM source_configs sc
			    WHERE sc.id = claims.source_config_id
			      AND sc.status = 'deleted'
			  )
		`, scope).Scan(
		&result.Claims.Total,
		&result.Claims.Verified,
		&result.Claims.Inferred,
		&result.Claims.Unverified,
		&result.Claims.Challenged,
		&result.Claims.Deprecated,
		&result.Claims.Expired,
		&result.Claims.Stale,
		&result.Claims.WithEvidence,
		stringMapTarget(result.LastUpdated, "claims"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT claims.id)::int
		FROM claims
		JOIN documents
		  ON documents.scope = claims.scope
		 AND COALESCE(documents.source_url, '') = COALESCE(claims.source_url, '')
			WHERE ($1 = '' OR claims.scope = $1)
			  AND documents.metadata->>'content_kind' = 'code'
			  AND claims.status NOT IN ('deprecated', 'expired')
			  AND NOT EXISTS (
			    SELECT 1 FROM source_configs sc
			    WHERE sc.id = claims.source_config_id
			      AND sc.status = 'deleted'
			  )
			  AND NOT EXISTS (
			    SELECT 1 FROM source_configs sc
			    WHERE sc.id = documents.source_config_id
			      AND sc.status = 'deleted'
			  )
		`, scope).Scan(&result.Claims.TrustedFromCodeDocuments); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
			  (SELECT COUNT(*)::int FROM entities WHERE ($1 = '' OR scope = $1) AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = entities.source_config_id AND sc.status = 'deleted')),
			  (SELECT COUNT(*)::int FROM entities WHERE ($1 = '' OR scope = $1) AND status = 'active' AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = entities.source_config_id AND sc.status = 'deleted')),
			  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = relations.source_config_id AND sc.status = 'deleted')),
			  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND status = 'active' AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = relations.source_config_id AND sc.status = 'deleted')),
			  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND status = 'challenged' AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = relations.source_config_id AND sc.status = 'deleted')),
			  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND (freshness_status = 'stale' OR expires_at < now()) AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = relations.source_config_id AND sc.status = 'deleted')),
			  COALESCE((SELECT MAX(updated_at)::text FROM relations WHERE ($1 = '' OR scope = $1) AND NOT EXISTS (SELECT 1 FROM source_configs sc WHERE sc.id = relations.source_config_id AND sc.status = 'deleted')), '')
		`, scope).Scan(
		&result.Graph.Entities,
		&result.Graph.ActiveEntities,
		&result.Graph.Relations,
		&result.Graph.ActiveRelations,
		&result.Graph.ChallengedRelations,
		&result.Graph.StaleRelations,
		stringMapTarget(result.LastUpdated, "graph"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int, COALESCE(SUM(token_estimate), 0)::int, COALESCE(MAX(updated_at)::text, '')
		FROM memory_summaries
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(&result.Summaries.Total, &result.Summaries.TokenEstimate, stringMapTarget(result.LastUpdated, "summaries")); err != nil {
		return MemoryHealthResult{}, err
	}
	levels, err := s.memorySummaryLevels(ctx, scope)
	if err != nil {
		return MemoryHealthResult{}, err
	}
	result.Summaries.Levels = levels
	if err := s.pool.QueryRow(ctx, memoryHealthSourceCountsSQL(), scope).Scan(
		&result.Sources.Total,
		&result.Sources.Active,
		&result.Sources.Paused,
		&result.Sources.Disabled,
		&result.Sources.Error,
		&result.Sources.Due,
		&result.Sources.Overdue,
		stringMapTarget(result.LastUpdated, "sources"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	sourceHealth, err := s.memoryHealthSourceDetails(ctx, scope, maxMemoryHealthSourceDetails)
	if err != nil {
		return MemoryHealthResult{}, err
	}
	result.SourceHealth = sourceHealth
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM (
		  SELECT 1
		  FROM learning_proposals
		  WHERE ($1 = '' OR scope = $1)
		    AND status = 'pending'
		  GROUP BY
		    scope,
		    proposal_type,
		    title,
		    COALESCE(target_type, ''),
		    COALESCE(target_id, ''),
		    COALESCE(source_url, '')
		  HAVING COUNT(*) > 1
		) duplicates
	`, scope).Scan(&result.Learning.DuplicatePendingGroups); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE created_at >= now() - interval '24 hours')::int,
		  COUNT(*) FILTER (WHERE status = 'succeeded')::int,
		  COUNT(*) FILTER (
		    WHERE status = 'failed'
		      AND (
		        source_config_id IS NULL
		        OR NOT EXISTS (
		          SELECT 1
		          FROM ingestion_jobs newer
		          WHERE newer.source_config_id = ingestion_jobs.source_config_id
		            AND newer.status = 'succeeded'
		            AND newer.updated_at > ingestion_jobs.updated_at
		        )
		      )
		  )::int,
		  COUNT(*) FILTER (WHERE status = 'running')::int,
		  COUNT(*) FILTER (
		    WHERE status = 'running'
		      AND (heartbeat_at IS NULL OR heartbeat_at < now() - interval '10 minutes')
		  )::int,
		  COUNT(*) FILTER (WHERE status = 'queued')::int,
		  COUNT(*) FILTER (WHERE status = 'retry')::int,
		  COALESCE(SUM(documents_seen), 0)::int,
		  COALESCE(SUM(documents_changed), 0)::int,
		  COALESCE(SUM(chunks_written), 0)::int,
		  COALESCE(SUM(claims_written), 0)::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM ingestion_jobs
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Ingestion.TotalJobs,
		&result.Ingestion.RecentJobs,
		&result.Ingestion.SucceededJobs,
		&result.Ingestion.FailedJobs,
		&result.Ingestion.RunningJobs,
		&result.Ingestion.StaleRunningJobs,
		&result.Ingestion.QueuedJobs,
		&result.Ingestion.RetryJobs,
		&result.Ingestion.DocumentsSeen,
		&result.Ingestion.DocumentsChanged,
		&result.Ingestion.ChunksWritten,
		&result.Ingestion.ClaimsWritten,
		stringMapTarget(result.LastUpdated, "ingestion"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'open')::int,
		  COUNT(*) FILTER (WHERE status = 'reviewing')::int,
		  COUNT(*) FILTER (WHERE status IN ('open', 'reviewing') AND severity = 'blocking')::int,
		  COUNT(*) FILTER (WHERE status IN ('open', 'reviewing') AND severity = 'high')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM conflicts
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Conflicts.Total,
		&result.Conflicts.Open,
		&result.Conflicts.Reviewing,
		&result.Conflicts.Blocking,
		&result.Conflicts.High,
		stringMapTarget(result.LastUpdated, "conflicts"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'pending')::int,
		  COUNT(*) FILTER (WHERE status = 'accepted')::int,
		  COUNT(*) FILTER (WHERE status = 'applied')::int,
		  COUNT(*) FILTER (WHERE status = 'rejected')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM learning_proposals
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Learning.Total,
		&result.Learning.Pending,
		&result.Learning.Accepted,
		&result.Learning.Applied,
		&result.Learning.Rejected,
		stringMapTarget(result.LastUpdated, "learning"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'pending')::int,
		  COUNT(*) FILTER (WHERE status = 'approved')::int,
		  COUNT(*) FILTER (WHERE status = 'rejected')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM approval_requests
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Approvals.Total,
		&result.Approvals.Pending,
		&result.Approvals.Approved,
		&result.Approvals.Rejected,
		stringMapTarget(result.LastUpdated, "approvals"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	assessment := assessMemoryHealth(result)
	result.Score = assessment.Score
	result.Status = assessment.Status
	result.Reasons = assessment.Reasons
	result.Signals = assessment.Signals
	return result, nil
}

func (s *Store) memoryHealthSourceDetails(ctx context.Context, scope string, limit int) ([]MemoryHealthSourceDetail, error) {
	if limit <= 0 {
		limit = maxMemoryHealthSourceDetails
	}
	rows, err := s.pool.Query(ctx, memoryHealthSourceDetailsSQL(), scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemoryHealthSourceDetail{}
	for rows.Next() {
		var item MemoryHealthSourceDetail
		var lastSuccessAt, lastErrorAt, latestJobUpdated sql.NullString
		if err := rows.Scan(
			&item.ID,
			&item.Name,
			&item.Type,
			&item.Status,
			&lastSuccessAt,
			&lastErrorAt,
			&item.LastError,
			&item.Due,
			&item.Overdue,
			&item.RetryJobs,
			&item.FailedJobs,
			&item.RunningJobs,
			&item.QueuedJobs,
			&item.StaleRunningJobs,
			&item.LatestJobID,
			&item.LatestJobStatus,
			&latestJobUpdated,
		); err != nil {
			return nil, err
		}
		item.LastSuccessAt = stringPtrFromNull(lastSuccessAt)
		item.LastErrorAt = stringPtrFromNull(lastErrorAt)
		if latestJobUpdated.Valid && latestJobUpdated.String != "" {
			item.LatestJobUpdated = latestJobUpdated.String
		}
		item.RemediationHint = memoryHealthSourceRemediationHint(item)
		out = append(out, item)
	}
	return out, rows.Err()
}

func memoryHealthSourceCountsSQL() string {
	return sourceReadinessCTE() + `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'active')::int,
		  COUNT(*) FILTER (WHERE status = 'paused')::int,
		  COUNT(*) FILTER (WHERE status = 'disabled')::int,
		  COUNT(*) FILTER (WHERE status = 'error')::int,
		  COUNT(*) FILTER (WHERE refresh_due)::int,
		  COUNT(*) FILTER (WHERE refresh_overdue)::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM source_readiness
	`
}

func memoryHealthSourceDetailsSQL() string {
	return sourceReadinessCTE() + `,
		job_rollup AS (
		  SELECT
		    source_config_id,
		    COUNT(*) FILTER (WHERE status = 'retry')::int AS retry_jobs,
		    COUNT(*) FILTER (
		      WHERE status = 'failed'
		        AND NOT EXISTS (
		          SELECT 1
		          FROM ingestion_jobs newer
		          WHERE newer.source_config_id = ingestion_jobs.source_config_id
		            AND newer.status = 'succeeded'
		            AND newer.updated_at > ingestion_jobs.updated_at
		        )
		    )::int AS failed_jobs,
		    COUNT(*) FILTER (WHERE status = 'running')::int AS running_jobs,
		    COUNT(*) FILTER (WHERE status = 'queued')::int AS queued_jobs,
		    COUNT(*) FILTER (
		      WHERE status = 'running'
		        AND (heartbeat_at IS NULL OR heartbeat_at < now() - interval '10 minutes')
		    )::int AS stale_running_jobs
		  FROM ingestion_jobs
		  WHERE source_config_id IS NOT NULL
		    AND ($1 = '' OR scope = $1)
		  GROUP BY source_config_id
		),
		latest_job AS (
		  SELECT DISTINCT ON (source_config_id)
		    source_config_id,
		    id,
		    status,
		    updated_at::text AS updated_at
		  FROM ingestion_jobs
		  WHERE source_config_id IS NOT NULL
		    AND ($1 = '' OR scope = $1)
		  ORDER BY source_config_id, updated_at DESC, created_at DESC, id DESC
		)
		SELECT
		  sr.id,
		  sr.name,
		  sr.source_type,
		  sr.status,
		  sr.last_success_at::text,
		  sr.last_error_at::text,
		  COALESCE(sr.last_error, ''),
		  sr.refresh_due,
		  sr.refresh_overdue,
		  COALESCE(j.retry_jobs, 0)::int,
		  COALESCE(j.failed_jobs, 0)::int,
		  COALESCE(j.running_jobs, 0)::int,
		  COALESCE(j.queued_jobs, 0)::int,
		  COALESCE(j.stale_running_jobs, 0)::int,
		  COALESCE(lj.id, ''),
		  COALESCE(lj.status, ''),
		  lj.updated_at
		FROM source_readiness sr
		LEFT JOIN job_rollup j ON j.source_config_id = sr.id
		LEFT JOIN latest_job lj ON lj.source_config_id = sr.id
		WHERE sr.status = 'error'
		   OR sr.refresh_due
		   OR sr.refresh_overdue
		   OR COALESCE(j.failed_jobs, 0) > 0
		   OR COALESCE(j.retry_jobs, 0) > 0
		   OR COALESCE(j.stale_running_jobs, 0) > 0
		   OR COALESCE(j.running_jobs, 0) > 0
		   OR COALESCE(j.queued_jobs, 0) > 0
		ORDER BY
		  CASE
		    WHEN sr.status = 'error' THEN 0
		    WHEN sr.refresh_overdue THEN 1
		    WHEN COALESCE(j.failed_jobs, 0) > 0 THEN 2
		    WHEN COALESCE(j.stale_running_jobs, 0) > 0 THEN 3
		    WHEN COALESCE(j.retry_jobs, 0) > 0 THEN 4
		    WHEN sr.refresh_due THEN 5
		    WHEN COALESCE(j.running_jobs, 0) > 0 THEN 6
		    WHEN COALESCE(j.queued_jobs, 0) > 0 THEN 7
		    ELSE 8
		  END,
		  sr.updated_at ASC,
		  sr.id ASC
		LIMIT $2
	`
}

func sourceReadinessCTE() string {
	return `
		WITH source_intervals AS (
		  SELECT
		    sc.*,
		    freshness.freshness_interval,
		    freshness.schedule_interval
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
		  WHERE ($1 = '' OR sc.scope = $1)
		),
		source_readiness AS (
		  SELECT
		    source_due.*,
		    (
		      source_due.refresh_due
		      AND (
		        (COALESCE(source_due.freshness_interval, source_due.schedule_interval) IS NOT NULL
		          AND (
		            (source_due.last_success_at IS NULL AND source_due.created_at < now() - COALESCE(source_due.freshness_interval, source_due.schedule_interval))
		            OR source_due.last_success_at < now() - (COALESCE(source_due.freshness_interval, source_due.schedule_interval) * 2)
		          ))
		        OR (COALESCE(source_due.freshness_interval, source_due.schedule_interval) IS NULL
		          AND (
		            (source_due.last_success_at IS NULL AND source_due.created_at < now() - interval '24 hours')
		            OR (source_due.last_success_at IS NOT NULL AND source_due.updated_at > source_due.last_success_at AND source_due.updated_at < now() - interval '24 hours')
		          ))
		      )
		    ) AS refresh_overdue
		  FROM (
		    SELECT
		      source_intervals.*,
		      (
		        source_intervals.status = 'active'
		        AND source_intervals.source_type IN ('local_repo', 'markdown', 'git_repo', 'mcp')
		        AND NOT EXISTS (
		          SELECT 1
		          FROM ingestion_jobs ij
		          WHERE ij.source_config_id = source_intervals.id
		            AND ij.status IN ('queued', 'retry', 'running')
		        )
		        AND (
		          source_intervals.last_success_at IS NULL
		          OR source_intervals.updated_at > source_intervals.last_success_at
		          OR (source_intervals.freshness_interval IS NOT NULL AND source_intervals.last_success_at < now() - source_intervals.freshness_interval)
		          OR (source_intervals.schedule_interval IS NOT NULL AND source_intervals.last_success_at < now() - source_intervals.schedule_interval)
		        )
		      ) AS refresh_due
		    FROM source_intervals
		  ) source_due
		)
	`
}

func memoryHealthSourceRemediationHint(source MemoryHealthSourceDetail) string {
	switch {
	case source.Status == "error":
		return "fix source configuration or credentials, then retry ingestion"
	case source.FailedJobs > 0:
		return "inspect failed ingestion jobs, fix the source error, then retry"
	case source.StaleRunningJobs > 0:
		return "restart or cancel stale running jobs, then retry affected ingestion"
	case source.RetryJobs > 0:
		return "monitor retrying jobs or inspect repeated failures"
	case source.Overdue:
		return "refresh this source before relying on affected memory"
	case source.Due:
		return "enqueue a source refresh or confirm the source is intentionally unchanged"
	case source.RunningJobs > 0:
		return "check active ingestion worker progress and heartbeat"
	case source.QueuedJobs > 0:
		return "wait for ingestion workers or increase worker capacity if the queue is stuck"
	default:
		return "review source configuration and ingestion history"
	}
}

func stringPtrFromNull(value sql.NullString) *string {
	if !value.Valid || value.String == "" {
		return nil
	}
	text := value.String
	return &text
}

func (s *Store) memorySummaryLevels(ctx context.Context, scope string) (map[string]int, error) {
	rows, err := s.queryRunner().Query(ctx, `
		SELECT level, COUNT(*)::int
		FROM memory_summaries
		WHERE ($1 = '' OR scope = $1)
		GROUP BY level
		ORDER BY level
	`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	levels := map[string]int{}
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return nil, err
		}
		levels[level] = count
	}
	return levels, rows.Err()
}

func stringMapTarget(values map[string]string, key string) *stringMapScanner {
	return &stringMapScanner{values: values, key: key}
}

func (s *stringMapScanner) Scan(value any) error {
	if s == nil || s.values == nil || s.key == "" {
		return nil
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if typed != "" {
			s.values[s.key] = typed
		}
	case []byte:
		if len(typed) > 0 {
			s.values[s.key] = string(typed)
		}
	case time.Time:
		if !typed.IsZero() {
			s.values[s.key] = typed.UTC().Format(time.RFC3339)
		}
	default:
		text := fmt.Sprint(typed)
		if text != "" {
			s.values[s.key] = text
		}
	}
	return nil
}

func assessMemoryHealth(result MemoryHealthResult) memoryHealthAssessment {
	score := 100
	reasons := []string{}
	signals := []MemoryHealthSignal{}
	penalize := memoryHealthPenalizer(func(points int, code, category, severity string, count int, reason, action string) {
		score -= points
		reasons = append(reasons, reason)
		signals = append(signals, MemoryHealthSignal{
			Code:        code,
			Category:    category,
			Severity:    severity,
			Count:       count,
			ScoreImpact: points,
			Message:     reason,
			Action:      action,
		})
	})
	assessMemoryContentHealth(result, penalize)
	assessMemoryConflictHealth(result, penalize)
	assessMemorySourceHealth(result, penalize)
	assessMemoryIngestionHealth(result, penalize)
	assessMemoryReviewQueueHealth(result, penalize)
	if score < 0 {
		score = 0
	}
	status := memoryHealthStatus(result, score)
	if len(reasons) == 0 {
		reasons = append(reasons, "memory is source-backed and ready")
		signals = append(signals, MemoryHealthSignal{
			Code:        "memory_ready",
			Category:    "readiness",
			Severity:    "info",
			Count:       0,
			ScoreImpact: 0,
			Message:     "memory is source-backed and ready",
			Action:      "proceed",
		})
	}
	return memoryHealthAssessment{Score: score, Status: status, Reasons: reasons, Signals: signals}
}

func assessMemoryContentHealth(result MemoryHealthResult, penalize memoryHealthPenalizer) {
	if result.Documents.Total == 0 {
		penalize(35, "documents_empty", "documents", "critical", 0, "no documents ingested", "ingest at least one source for this scope")
	}
	if result.Claims.Verified+result.Claims.Inferred == 0 {
		penalize(18, "trusted_claims_empty", "claims", "warning", 0, "no trusted claims available", "ingest authoritative knowledge documents or approve inferred claims")
	}
	if result.Claims.Total > 0 && result.Claims.WithEvidence == 0 {
		penalize(15, "claims_missing_evidence", "claims", "warning", result.Claims.Total, "claims have no evidence links", "re-ingest sources or attach evidence before agents rely on these claims")
	}
	if result.Claims.TrustedFromCodeDocuments > 0 {
		penalize(30, "trusted_claims_from_code_documents", "trust_guard", "critical", result.Claims.TrustedFromCodeDocuments, "trusted claims from code documents need cleanup", "deprecate polluted claims and re-ingest code as graph-only knowledge")
	}
	if result.Summaries.Total == 0 {
		penalize(12, "summaries_empty", "summaries", "warning", 0, "no hierarchical summaries", "run summary rebuild or re-ingest sources with summary generation enabled")
	}
	if result.Graph.ActiveRelations == 0 {
		penalize(8, "graph_relations_empty", "graph", "warning", 0, "no active graph relations", "ingest sources that expose relationships or run graph extraction")
	}
	if result.Claims.Challenged+result.Claims.Stale+result.Claims.Expired > 0 {
		penalize(8, "claims_need_review", "claims", "warning", result.Claims.Challenged+result.Claims.Stale+result.Claims.Expired, "claims need freshness or challenge review", "review challenged, stale, or expired claims before reuse")
	}
	if result.Graph.ChallengedRelations+result.Graph.StaleRelations > 0 {
		penalize(6, "graph_relations_need_review", "graph", "warning", result.Graph.ChallengedRelations+result.Graph.StaleRelations, "graph relations need review", "review challenged or stale graph relations")
	}
}

func assessMemoryConflictHealth(result MemoryHealthResult, penalize memoryHealthPenalizer) {
	if result.Conflicts.Blocking > 0 {
		penalize(25, "blocking_conflicts", "conflicts", "critical", result.Conflicts.Blocking, "blocking conflicts need review", "resolve blocking conflicts before allowing autonomous agent work")
	} else if result.Conflicts.Open+result.Conflicts.Reviewing > 0 {
		penalize(15, "active_conflicts", "conflicts", "warning", result.Conflicts.Open+result.Conflicts.Reviewing, "active conflicts need review", "review or resolve open memory conflicts")
	}
}

func assessMemorySourceHealth(result MemoryHealthResult, penalize memoryHealthPenalizer) {
	if result.Sources.Error > 0 {
		penalize(15, "source_configs_error", "sources", "critical", result.Sources.Error, "source configs are in error", "fix source configuration or credentials and retry ingestion")
	}
	if result.Sources.Overdue > 0 {
		penalize(20, "source_refresh_overdue", "sources", "critical", result.Sources.Overdue, "source refresh is overdue", "refresh stale sources before relying on affected memory")
	} else if result.Sources.Due > 0 {
		penalize(5, "source_refresh_due", "sources", "warning", result.Sources.Due, "source refresh is due", "refresh stale sources or confirm the source is intentionally unchanged")
	}
}

func assessMemoryIngestionHealth(result MemoryHealthResult, penalize memoryHealthPenalizer) {
	if result.Ingestion.FailedJobs > 0 {
		penalize(12, "ingestion_jobs_failed", "ingestion", "critical", result.Ingestion.FailedJobs, "ingestion jobs failed", "inspect failed jobs and retry after fixing the source error")
	}
	if result.Ingestion.StaleRunningJobs > 0 {
		penalize(25, "ingestion_jobs_stale_running", "ingestion", "critical", result.Ingestion.StaleRunningJobs, "ingestion jobs are stale while running", "restart or cancel stale workers, then retry affected ingestion jobs")
	}
	if result.Ingestion.RetryJobs > 0 {
		penalize(8, "ingestion_jobs_retrying", "ingestion", "warning", result.Ingestion.RetryJobs, "ingestion jobs are waiting to retry", "monitor retrying jobs or inspect repeated failures")
	}
}

func assessMemoryReviewQueueHealth(result MemoryHealthResult, penalize memoryHealthPenalizer) {
	if result.Learning.Pending > 0 {
		penalize(4, "learning_proposals_pending", "learning", "info", result.Learning.Pending, "learning proposals are pending", "accept, reject, or apply queued learning proposals")
	}
	if result.Learning.DuplicatePendingGroups > 0 {
		penalize(25, "learning_duplicate_pending_groups", "trust_guard", "critical", result.Learning.DuplicatePendingGroups, "duplicate pending learning proposals need cleanup", "deduplicate the pending learning queue before operators review it")
	}
	if result.Approvals.Pending > 0 {
		penalize(4, "approval_requests_pending", "approvals", "info", result.Approvals.Pending, "approval requests are pending", "approve or reject pending agent action requests")
	}
}

func memoryHealthStatus(result MemoryHealthResult, score int) string {
	switch {
	case result.Conflicts.Blocking > 0 || result.Sources.Error > 0 || result.Sources.Overdue > 0 || result.Ingestion.FailedJobs > 0 || result.Ingestion.StaleRunningJobs > 0 || result.Claims.TrustedFromCodeDocuments > 0 || result.Learning.DuplicatePendingGroups > 0 || score < 55:
		return "critical"
	case score < 80 || result.Conflicts.Open+result.Conflicts.Reviewing > 0 || result.Sources.Due+result.Sources.Overdue > 0 || result.Ingestion.RetryJobs > 0 || result.Learning.Pending > 0 || result.Approvals.Pending > 0:
		return "needs_review"
	default:
		return "healthy"
	}
}

func memorySummarySelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  level,
		  summary_key,
		  title,
		  summary,
		  source_count,
		  relation_count,
		  token_estimate,
		  source_urls,
		  metadata,
		  LEAST(
		    GREATEST(
		      ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			      ts_rank_cd(search_vector, to_tsquery('simple', $4)) * 0.65
			    ),
			    0.4
			  )
			  + CASE level
			      WHEN 'agent_core' THEN 0.48
			      WHEN 'shared' THEN 0.46
			      WHEN 'core' THEN 0.45
			      WHEN 'repo' THEN 0.35
		      WHEN 'module' THEN 0.3
		      WHEN 'route' THEN 0.34
		      WHEN 'component' THEN 0.34
		      WHEN 'symbol' THEN 0.34
		      WHEN 'package' THEN 0.34
		      WHEN 'file' THEN 0.2
		      ELSE 0.1
		    END AS rank_score,
		  updated_at::text
		FROM memory_summaries
		WHERE scope = $2
		  AND NOT EXISTS (
		    SELECT 1
		    FROM documents d
		    JOIN source_configs sc ON sc.id = d.source_config_id
		    WHERE d.scope = memory_summaries.scope
		      AND sc.status = 'deleted'
		      AND memory_summaries.source_urls @> jsonb_build_array(d.source_url)
		  )
		  AND (
		    search_vector @@ plainto_tsquery('simple', $1)
		    OR search_vector @@ to_tsquery('simple', $4)
		    OR $1 = ''
		  )
		ORDER BY rank_score DESC, updated_at DESC
		LIMIT $3
		`
}

func memorySummarySelectByLevelsSQL() string {
	base := memorySummarySelectSQL()
	return strings.Replace(base, "WHERE scope = $2", "WHERE scope = $2\n\t\t\t  AND level = ANY($5)", 1)
}

func (s *Store) ListDocumentsForSummary(ctx context.Context, scope string, limit int) ([]SummaryDocumentRecord, error) {
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  d.id,
		  d.source_type,
		  d.source_url,
		  COALESCE(d.source_id, ''),
		  d.title,
		  d.scope,
		  COALESCE(ch.content, ''),
		  d.metadata,
		  COALESCE(rel.relation_count, 0),
		  COALESCE(ch.chunk_count, 0),
		  d.ingested_at::text
		FROM documents d
		LEFT JOIN source_configs sc ON sc.id = d.source_config_id
		LEFT JOIN LATERAL (
		  SELECT
		    string_agg(content, E'\n\n' ORDER BY chunk_index) AS content,
		    count(*) AS chunk_count
		  FROM chunks
		  WHERE document_id = d.id
		    AND chunk_index < 4
		) ch ON TRUE
		LEFT JOIN LATERAL (
		  SELECT count(*) AS relation_count
		  FROM relations
		  WHERE scope = d.scope
		    AND source_url = d.source_url
		    AND status NOT IN ('deprecated', 'expired')
		) rel ON TRUE
		WHERE d.scope = $1
		  AND d.status NOT IN ('deprecated', 'deleted')
		  AND COALESCE(sc.status, 'active') <> 'deleted'
		ORDER BY d.ingested_at DESC, d.id ASC
		LIMIT $2
	`, strings.TrimSpace(scope), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SummaryDocumentRecord{}
	for rows.Next() {
		var record SummaryDocumentRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&record.DocumentID,
			&record.SourceType,
			&record.SourceURL,
			&record.SourceID,
			&record.Title,
			&record.Scope,
			&record.Content,
			&metadataRaw,
			&record.Relations,
			&record.Chunks,
			&record.IngestedAt,
		); err != nil {
			return nil, err
		}
		record.Metadata = decodeJSONMap(metadataRaw)
		out = append(out, record)
	}
	return out, rows.Err()
}
