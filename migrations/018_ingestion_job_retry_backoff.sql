ALTER TABLE ingestion_jobs
  ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS ingestion_jobs_webhook_due_idx
  ON ingestion_jobs (status, trigger_type, next_attempt_at ASC, created_at ASC, id ASC)
  WHERE status IN ('queued', 'retry')
    AND trigger_type = 'webhook';

CREATE INDEX IF NOT EXISTS ingestion_jobs_source_due_idx
  ON ingestion_jobs (status, source_config_id, next_attempt_at ASC, created_at ASC, id ASC)
  WHERE status IN ('queued', 'retry')
    AND source_config_id IS NOT NULL;
