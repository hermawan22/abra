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
