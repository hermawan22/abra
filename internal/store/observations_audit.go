package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) InsertAuditEvent(ctx context.Context, eventType, targetType, targetID, scope, sourceURL string, metadata map[string]any) error {
	id := stableID("audit", eventType, targetType, targetID, fmt.Sprint(metadata))
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO audit_events (id, event_type, target_type, target_id, scope, source_url, metadata)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)
		ON CONFLICT DO NOTHING
	`, id, eventType, targetType, targetID, scope, sourceURL, jsonb(metadata))
	return err
}

func (s *Store) InsertObservation(ctx context.Context, record ObservationRecord) (ObservationResult, error) {
	record = normalizeObservation(record)
	if record.Scope == "" {
		return ObservationResult{}, fmt.Errorf("scope is required")
	}
	if record.ObservationText == "" {
		return ObservationResult{}, fmt.Errorf("observation_text is required")
	}
	if record.ID == "" {
		record.ID = stableID("observation", record.Scope, record.SourceURL, record.SourceID, record.ObservationType, record.ObservedAt, record.ObservationText)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO observations (
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, subject_entity_id, object_entity_id, relation_id,
		  claim_id, document_id, chunk_id, source_config_id, ingestion_job_id,
		  source_url, source_type, source_id, observed_at, valid_from, expires_at,
		  created_by, value, metadata
		)
		VALUES (
		  $1, $2, $3, $4, $5, $6, $7,
		  $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
		  NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''), NULLIF($16, ''), NULLIF($17, ''),
		  NULLIF($18, ''), NULLIF($19, ''), NULLIF($20, ''), $21::timestamptz, NULLIF($22, '')::timestamptz, NULLIF($23, '')::timestamptz,
		  NULLIF($24, ''), $25::jsonb, $26::jsonb
		)
		ON CONFLICT (id) DO UPDATE SET
		  observation_text = EXCLUDED.observation_text,
		  status = EXCLUDED.status,
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  confidence = EXCLUDED.confidence,
		  freshness_status = EXCLUDED.freshness_status,
		  value = observations.value || EXCLUDED.value,
		  metadata = observations.metadata || EXCLUDED.metadata,
		  updated_at = now()
		RETURNING
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
	`, record.ID, record.Scope, record.ObservationType, record.ObservationText, record.Status, record.Authority, record.AuthorityScore,
		record.Confidence, record.FreshnessStatus, record.SubjectEntityID, record.ObjectEntityID, record.RelationID,
		record.ClaimID, record.DocumentID, record.ChunkID, record.SourceConfigID, record.IngestionJobID,
		record.SourceURL, record.SourceType, record.SourceID, record.ObservedAt, record.ValidFrom, record.ExpiresAt,
		record.CreatedBy, jsonb(record.Value), jsonb(record.Metadata))
	return scanObservation(row)
}

func (s *Store) GetObservation(ctx context.Context, id string) (ObservationResult, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
		FROM observations
		WHERE id = $1
	`, strings.TrimSpace(id))
	observation, err := scanObservation(row)
	if err == pgx.ErrNoRows {
		return ObservationResult{}, fmt.Errorf("observation %q not found", strings.TrimSpace(id))
	}
	return observation, err
}

func (s *Store) LinkObservationProposal(ctx context.Context, observationID, proposalID, createdBy string) (ObservationResult, error) {
	observationID = strings.TrimSpace(observationID)
	proposalID = strings.TrimSpace(proposalID)
	if observationID == "" || proposalID == "" {
		return ObservationResult{}, fmt.Errorf("observation_id and proposal_id are required")
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE observations
		SET status = CASE
		      WHEN status IN ('raw', 'challenged') THEN 'proposed'
		      ELSE status
		    END,
		    metadata = metadata || jsonb_build_object(
		      'learning_proposal_id', $2::text,
		      'learning_proposed_at', now()::text,
		      'learning_proposed_by', NULLIF($3, '')
		    ),
		    updated_at = now()
		WHERE id = $1
		  AND status NOT IN ('rejected', 'deprecated', 'expired')
		RETURNING
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
	`, observationID, proposalID, strings.TrimSpace(createdBy))
	observation, err := scanObservation(row)
	if err == pgx.ErrNoRows {
		return ObservationResult{}, fmt.Errorf("observation %q not found or cannot be proposed", observationID)
	}
	return observation, err
}

func (s *Store) ListObservations(ctx context.Context, filter ObservationFilter) ([]ObservationResult, error) {
	filter.Scope = strings.TrimSpace(filter.Scope)
	if filter.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	filter.ObservationType = strings.TrimSpace(filter.ObservationType)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Since = strings.TrimSpace(filter.Since)
	filter.Until = strings.TrimSpace(filter.Until)
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	anyQuery := ""
	if strings.TrimSpace(filter.Query) != "" {
		anyQuery = fullTextAnyQuery(filter.Query)
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
		FROM observations
		WHERE scope = $1
		  AND ($2 = '' OR observation_type = $2)
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR observed_at >= $4::timestamptz)
		  AND ($5 = '' OR observed_at <= $5::timestamptz)
		  AND ($6 = '' OR search_vector @@ to_tsquery('simple', NULLIF($6, '')))
		ORDER BY
		  CASE WHEN $6 = '' THEN 0 ELSE ts_rank_cd(search_vector, to_tsquery('simple', $6)) END DESC,
		  observed_at DESC,
		  updated_at DESC
		LIMIT $7
	`, filter.Scope, filter.ObservationType, filter.Status, filter.Since, filter.Until, anyQuery, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := []ObservationResult{}
	for rows.Next() {
		observation, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func (s *Store) ListAuditEvents(ctx context.Context, filter AuditEventFilter) ([]AuditEventRecord, error) {
	query, args := auditEventsQuery(filter)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []AuditEventRecord{}
	for rows.Next() {
		var event AuditEventRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&event.ID,
			&event.EventType,
			&event.Actor,
			&event.TargetType,
			&event.TargetID,
			&event.Scope,
			&event.SourceURL,
			&metadataRaw,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.Metadata = decodeJSONMap(metadataRaw)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) GetIntegrationCursor(ctx context.Context, id string) (IntegrationCursorRecord, bool, error) {
	var record IntegrationCursorRecord
	var cursorTime sql.NullTime
	var metadataRaw []byte
	err := s.queryRunner().QueryRow(ctx, `
		SELECT
		  id,
		  integration_type,
		  target,
		  COALESCE(cursor_value, ''),
		  cursor_time,
		  metadata,
		  created_at::text,
		  updated_at::text
		FROM integration_cursors
		WHERE id = $1
	`, strings.TrimSpace(id)).Scan(
		&record.ID,
		&record.IntegrationType,
		&record.Target,
		&record.CursorValue,
		&cursorTime,
		&metadataRaw,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return IntegrationCursorRecord{}, false, nil
	}
	if err != nil {
		return IntegrationCursorRecord{}, false, err
	}
	if cursorTime.Valid {
		record.CursorTime = cursorTime.Time
	}
	record.Metadata = decodeJSONMap(metadataRaw)
	return record, true, nil
}

func (s *Store) ListAuditEventsForDelivery(ctx context.Context, scope string, cursor IntegrationCursorRecord, limit int) ([]AuditEventRecord, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	var cursorTime any
	if !cursor.CursorTime.IsZero() {
		cursorTime = cursor.CursorTime.UTC()
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  id,
		  event_type,
		  COALESCE(actor, ''),
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  COALESCE(scope, ''),
		  COALESCE(source_url, ''),
		  metadata,
		  created_at::text
		FROM audit_events
		WHERE ($1 = '' OR scope = $1)
		  AND (
		    $2::timestamptz IS NULL
		    OR created_at > $2
		    OR (created_at = $2 AND id > $3)
		  )
		ORDER BY created_at ASC, id ASC
		LIMIT $4
	`, strings.TrimSpace(scope), cursorTime, strings.TrimSpace(cursor.CursorValue), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []AuditEventRecord{}
	for rows.Next() {
		var event AuditEventRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&event.ID,
			&event.EventType,
			&event.Actor,
			&event.TargetType,
			&event.TargetID,
			&event.Scope,
			&event.SourceURL,
			&metadataRaw,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.Metadata = decodeJSONMap(metadataRaw)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) UpsertIntegrationCursorFromAuditEvent(ctx context.Context, record IntegrationCursorRecord, auditEventID string) error {
	record.ID = strings.TrimSpace(record.ID)
	record.IntegrationType = strings.TrimSpace(record.IntegrationType)
	record.Target = strings.TrimSpace(record.Target)
	auditEventID = strings.TrimSpace(auditEventID)
	if record.ID == "" || record.IntegrationType == "" || record.Target == "" || auditEventID == "" {
		return fmt.Errorf("cursor id, integration_type, target, and audit event id are required")
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO integration_cursors (
		  id, integration_type, target, cursor_value, cursor_time, metadata
		)
		SELECT $1, $2, $3, audit_events.id, audit_events.created_at, $5::jsonb
		FROM audit_events
		WHERE audit_events.id = $4
		ON CONFLICT (id)
		DO UPDATE SET
		  integration_type = EXCLUDED.integration_type,
		  target = EXCLUDED.target,
		  cursor_value = EXCLUDED.cursor_value,
		  cursor_time = EXCLUDED.cursor_time,
		  metadata = integration_cursors.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, record.ID, record.IntegrationType, record.Target, auditEventID, jsonb(record.Metadata))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("audit event %q not found", auditEventID)
	}
	return nil
}

func auditEventsQuery(filter AuditEventFilter) (string, []any) {
	limit := filter.Limit
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	conditions := []string{"TRUE"}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if strings.TrimSpace(filter.Scope) != "" {
		add("scope = $%d", strings.TrimSpace(filter.Scope))
	}
	if strings.TrimSpace(filter.EventType) != "" {
		add("event_type = $%d", strings.TrimSpace(filter.EventType))
	}
	if strings.TrimSpace(filter.TargetType) != "" {
		add("target_type = $%d", strings.TrimSpace(filter.TargetType))
	}
	if !filter.Since.IsZero() {
		add("created_at >= $%d", filter.Since.UTC())
	}
	if !filter.Until.IsZero() {
		add("created_at <= $%d", filter.Until.UTC())
	}
	args = append(args, limit)
	return fmt.Sprintf(`
		SELECT
		  id,
		  event_type,
		  COALESCE(actor, ''),
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  COALESCE(scope, ''),
		  COALESCE(source_url, ''),
		  metadata,
		  created_at::text
		FROM audit_events
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d
	`, strings.Join(conditions, " AND "), len(args)), args
}

func normalizeObservation(record ObservationRecord) ObservationRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.Scope = strings.TrimSpace(record.Scope)
	record.ObservationType = strings.TrimSpace(record.ObservationType)
	if record.ObservationType == "" {
		record.ObservationType = "episode"
	}
	record.ObservationText = strings.TrimSpace(record.ObservationText)
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = "raw"
	}
	record.Authority = strings.TrimSpace(record.Authority)
	if record.Authority == "" {
		record.Authority = "manual-unverified"
	}
	if record.AuthorityScore <= 0 {
		record.AuthorityScore = 0.35
	}
	if record.Confidence <= 0 {
		record.Confidence = 0.35
	}
	record.FreshnessStatus = strings.TrimSpace(record.FreshnessStatus)
	if record.FreshnessStatus == "" {
		record.FreshnessStatus = "unknown"
	}
	record.SubjectEntityID = strings.TrimSpace(record.SubjectEntityID)
	record.ObjectEntityID = strings.TrimSpace(record.ObjectEntityID)
	record.RelationID = strings.TrimSpace(record.RelationID)
	record.ClaimID = strings.TrimSpace(record.ClaimID)
	record.DocumentID = strings.TrimSpace(record.DocumentID)
	record.ChunkID = strings.TrimSpace(record.ChunkID)
	record.SourceConfigID = strings.TrimSpace(record.SourceConfigID)
	record.IngestionJobID = strings.TrimSpace(record.IngestionJobID)
	record.SourceURL = strings.TrimSpace(record.SourceURL)
	record.SourceType = strings.TrimSpace(record.SourceType)
	record.SourceID = strings.TrimSpace(record.SourceID)
	record.ObservedAt = strings.TrimSpace(record.ObservedAt)
	if record.ObservedAt == "" {
		record.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	record.ValidFrom = strings.TrimSpace(record.ValidFrom)
	record.ExpiresAt = strings.TrimSpace(record.ExpiresAt)
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	return record
}

func scanObservation(row rowScanner) (ObservationResult, error) {
	var observation ObservationResult
	var valueRaw, metadataRaw []byte
	if err := row.Scan(
		&observation.ID,
		&observation.Scope,
		&observation.ObservationType,
		&observation.ObservationText,
		&observation.Status,
		&observation.Authority,
		&observation.AuthorityScore,
		&observation.Confidence,
		&observation.FreshnessStatus,
		&observation.SubjectEntityID,
		&observation.ObjectEntityID,
		&observation.RelationID,
		&observation.ClaimID,
		&observation.DocumentID,
		&observation.ChunkID,
		&observation.SourceConfigID,
		&observation.IngestionJobID,
		&observation.SourceURL,
		&observation.SourceType,
		&observation.SourceID,
		&observation.ObservedAt,
		&observation.ValidFrom,
		&observation.ExpiresAt,
		&observation.LastVerifiedAt,
		&observation.CreatedBy,
		&observation.CreatedAt,
		&observation.UpdatedAt,
		&valueRaw,
		&metadataRaw,
	); err != nil {
		return ObservationResult{}, err
	}
	observation.Value = decodeJSONMap(valueRaw)
	observation.Metadata = decodeJSONMap(metadataRaw)
	return observation, nil
}
