package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type EnqueueIngestionJobInput struct {
	SourceConfigID string         `json:"source_config_id"`
	TriggerType    string         `json:"trigger_type"`
	CreatedBy      string         `json:"created_by"`
	MaxAttempts    int            `json:"max_attempts"`
	Metadata       map[string]any `json:"metadata"`
}

type RetryIngestionJobInput struct {
	CreatedBy   string         `json:"created_by"`
	MaxAttempts int            `json:"max_attempts"`
	Metadata    map[string]any `json:"metadata"`
}

type CancelIngestionJobInput struct {
	Reason    string         `json:"reason"`
	CreatedBy string         `json:"created_by"`
	Metadata  map[string]any `json:"metadata"`
}

type StartWebhookIngestionJobInput struct {
	Scope         string         `json:"scope"`
	SourceType    string         `json:"source_type"`
	SourceURL     string         `json:"source_url"`
	Authority     string         `json:"authority"`
	ConnectorKind string         `json:"connector_kind"`
	EventType     string         `json:"event_type"`
	DeliveryID    string         `json:"delivery_id"`
	DocumentIndex int            `json:"document_index"`
	CreatedBy     string         `json:"created_by"`
	Metadata      map[string]any `json:"metadata"`
}

type FinishWebhookIngestionJobInput struct {
	DocumentsSeen    int            `json:"documents_seen"`
	DocumentsChanged int            `json:"documents_changed"`
	ChunksWritten    int            `json:"chunks_written"`
	ClaimsWritten    int            `json:"claims_written"`
	Error            error          `json:"-"`
	Metadata         map[string]any `json:"metadata"`
}

func (s *Store) EnqueueIngestionJob(ctx context.Context, input EnqueueIngestionJobInput) (IngestionJobRecord, error) {
	input.SourceConfigID = strings.TrimSpace(input.SourceConfigID)
	if input.SourceConfigID == "" {
		return IngestionJobRecord{}, fmt.Errorf("source_config_id is required")
	}
	triggerType := normalizedTriggerType(input.TriggerType)
	if triggerType == "" {
		return IngestionJobRecord{}, fmt.Errorf("unsupported trigger_type %q", input.TriggerType)
	}
	if input.MaxAttempts <= 0 {
		input.MaxAttempts = 3
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"queued_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	jobID := stableID("job", input.SourceConfigID, triggerType, time.Now().UTC().Format(time.RFC3339Nano))
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO ingestion_jobs (
		  id, source_config_id, scope, source_type, source_url, trigger_type,
		  status, authority, max_attempts, created_by, metadata
		)
		SELECT
		  $1, id, scope, source_type, base_url, $2,
		  'queued', authority, $3, NULLIF($4, ''), $5::jsonb
		FROM source_configs
		WHERE id = $6
		  AND status IN ('active', 'error')
	`, jobID, triggerType, input.MaxAttempts, strings.TrimSpace(input.CreatedBy), jsonb(metadata), input.SourceConfigID)
	if err != nil {
		return IngestionJobRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		return IngestionJobRecord{}, fmt.Errorf("source_config_id %q is not active or does not exist", input.SourceConfigID)
	}
	return s.GetIngestionJob(ctx, jobID)
}

func (s *Store) StartWebhookIngestionJob(ctx context.Context, input StartWebhookIngestionJobInput) (IngestionJobRecord, bool, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	if input.Scope == "" || input.SourceType == "" || input.SourceURL == "" {
		return IngestionJobRecord{}, false, fmt.Errorf("scope, source_type, and source_url are required")
	}
	authority := strings.TrimSpace(input.Authority)
	if authority == "" {
		authority = "webhook"
	}
	jobID := webhookIngestionJobID(input)
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"connector_kind":      strings.TrimSpace(input.ConnectorKind),
		"webhook_event_type":  strings.TrimSpace(input.EventType),
		"webhook_delivery_id": strings.TrimSpace(input.DeliveryID),
		"webhook_doc_index":   input.DocumentIndex,
		"started_at":          time.Now().UTC().Format(time.RFC3339Nano),
	})
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO ingestion_jobs (
		  id, source_config_id, scope, source_type, source_url, trigger_type,
		  status, authority, lease_owner, heartbeat_at, started_at, attempts,
		  max_attempts, created_by, metadata
		)
		VALUES (
		  $1, NULL, $2, $3, $4, 'webhook',
		  'running', $5, 'abra-api', now(), now(), 1,
		  3, NULLIF($6, ''), $7::jsonb
		)
		ON CONFLICT DO NOTHING
	`, jobID, input.Scope, input.SourceType, input.SourceURL, authority, strings.TrimSpace(input.CreatedBy), jsonb(metadata))
	if err != nil {
		return IngestionJobRecord{}, false, err
	}
	if tag.RowsAffected() > 0 {
		job, getErr := s.GetIngestionJob(ctx, jobID)
		return job, false, getErr
	}
	job, err := s.restartRetryableWebhookJob(ctx, jobID, metadata)
	if err != nil {
		return IngestionJobRecord{}, false, err
	}
	return job, job.Status != "running" || job.Attempts == 1, nil
}

func (s *Store) restartRetryableWebhookJob(ctx context.Context, jobID string, metadata map[string]any) (IngestionJobRecord, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET status = 'running',
		    lease_owner = 'abra-api',
		    heartbeat_at = now(),
		    started_at = now(),
		    finished_at = NULL,
		    attempts = attempts + 1,
		    error_message = NULL,
		    metadata = metadata || $2::jsonb,
		    updated_at = now()
		WHERE id = $1
		  AND trigger_type = 'webhook'
		  AND status IN ('failed', 'canceled', 'retry')
		  AND attempts < max_attempts
	`, jobID, jsonb(mergeMetadata(metadata, map[string]any{
		"redelivered_at": time.Now().UTC().Format(time.RFC3339Nano),
	})))
	if err != nil {
		return IngestionJobRecord{}, err
	}
	job, getErr := s.GetIngestionJob(ctx, jobID)
	if getErr != nil {
		return IngestionJobRecord{}, getErr
	}
	if tag.RowsAffected() > 0 {
		return job, nil
	}
	return job, nil
}

func (s *Store) FinishWebhookIngestionJob(ctx context.Context, jobID string, input FinishWebhookIngestionJobInput) (IngestionJobRecord, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return IngestionJobRecord{}, fmt.Errorf("job_id is required")
	}
	status := "succeeded"
	errorMessage := ""
	if input.Error != nil {
		status = "failed"
		errorMessage = input.Error.Error()
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"finished_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET status = $2,
		    lease_owner = NULL,
		    heartbeat_at = NULL,
		    finished_at = now(),
		    documents_seen = $3,
		    documents_changed = $4,
		    chunks_written = $5,
		    claims_written = $6,
		    error_message = NULLIF($7, ''),
		    metadata = metadata || $8::jsonb,
		    updated_at = now()
		WHERE id = $1
		  AND status = 'running'
		  AND lease_owner = 'abra-api'
	`, jobID, status, input.DocumentsSeen, input.DocumentsChanged, input.ChunksWritten, input.ClaimsWritten, errorMessage, jsonb(metadata))
	if err != nil {
		return IngestionJobRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		return s.GetIngestionJob(ctx, jobID)
	}
	return s.GetIngestionJob(ctx, jobID)
}

func (s *Store) RetryIngestionJob(ctx context.Context, jobID string, input RetryIngestionJobInput) (IngestionJobRecord, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return IngestionJobRecord{}, fmt.Errorf("job_id is required")
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"retry_requested_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if strings.TrimSpace(input.CreatedBy) != "" {
		metadata["retry_requested_by"] = strings.TrimSpace(input.CreatedBy)
	}
	maxAttempts := input.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET status = 'retry',
		    lease_owner = NULL,
		    heartbeat_at = NULL,
		    started_at = NULL,
		    finished_at = NULL,
		    error_message = NULL,
		    max_attempts = GREATEST(max_attempts, attempts + 1, $2),
		    metadata = metadata || $3::jsonb,
		    updated_at = now()
		WHERE id = $1
		  AND status IN ('failed', 'canceled')
	`, jobID, maxAttempts, jsonb(metadata))
	if err != nil {
		return IngestionJobRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.GetIngestionJob(ctx, jobID)
		if getErr != nil {
			return IngestionJobRecord{}, getErr
		}
		return IngestionJobRecord{}, fmt.Errorf("job %q with status %q cannot be retried", jobID, current.Status)
	}
	return s.GetIngestionJob(ctx, jobID)
}

func (s *Store) CancelIngestionJob(ctx context.Context, jobID string, input CancelIngestionJobInput) (IngestionJobRecord, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return IngestionJobRecord{}, fmt.Errorf("job_id is required")
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"cancel_requested_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if reason := strings.TrimSpace(input.Reason); reason != "" {
		metadata["cancel_reason"] = reason
	}
	if createdBy := strings.TrimSpace(input.CreatedBy); createdBy != "" {
		metadata["cancel_requested_by"] = createdBy
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE ingestion_jobs
		SET status = 'canceled',
		    lease_owner = NULL,
		    heartbeat_at = NULL,
		    finished_at = COALESCE(finished_at, now()),
		    error_message = COALESCE(NULLIF($2, ''), error_message),
		    metadata = metadata || $3::jsonb,
		    updated_at = now()
		WHERE id = $1
		  AND status IN ('queued', 'retry')
	`, jobID, strings.TrimSpace(input.Reason), jsonb(metadata))
	if err != nil {
		return IngestionJobRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.GetIngestionJob(ctx, jobID)
		if getErr != nil {
			return IngestionJobRecord{}, getErr
		}
		return IngestionJobRecord{}, fmt.Errorf("job %q with status %q cannot be canceled", jobID, current.Status)
	}
	return s.GetIngestionJob(ctx, jobID)
}

func (s *Store) GetIngestionJob(ctx context.Context, jobID string) (IngestionJobRecord, error) {
	var job IngestionJobRecord
	var metadataRaw []byte
	err := s.pool.QueryRow(ctx, ingestionJobSelectSQL()+`
		WHERE id = $1
	`, strings.TrimSpace(jobID)).Scan(ingestionJobScanArgs(&job, &metadataRaw)...)
	if err == pgx.ErrNoRows {
		return IngestionJobRecord{}, fmt.Errorf("job %q not found", jobID)
	}
	if err != nil {
		return IngestionJobRecord{}, err
	}
	job.Metadata = decodeJSONMap(metadataRaw)
	return job, nil
}

func ingestionJobSelectSQL() string {
	return `
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
	`
}

func ingestionJobScanArgs(job *IngestionJobRecord, metadataRaw *[]byte) []any {
	return []any{
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
		metadataRaw,
	}
}

func normalizedTriggerType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "manual"
	}
	switch value {
	case "manual", "schedule", "webhook", "backfill", "revalidate":
		return value
	default:
		return ""
	}
}

func webhookIngestionJobID(input StartWebhookIngestionJobInput) string {
	deliveryID := strings.TrimSpace(input.DeliveryID)
	scope := strings.TrimSpace(input.Scope)
	sourceType := strings.TrimSpace(input.SourceType)
	sourceURL := strings.TrimSpace(input.SourceURL)
	documentIndex := fmt.Sprint(input.DocumentIndex)
	if deliveryID == "" {
		return stableID("webhook-job", scope, sourceType, sourceURL, documentIndex, time.Now().UTC().Format(time.RFC3339Nano))
	}
	return stableID("webhook-job", strings.TrimSpace(input.ConnectorKind), strings.TrimSpace(input.EventType), deliveryID, scope, sourceType, sourceURL, documentIndex)
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
