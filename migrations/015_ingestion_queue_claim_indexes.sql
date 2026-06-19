-- Queue claim indexes for worker-backed ingestion.
-- Webhook jobs are prioritized ahead of scheduled source jobs, so keep a
-- narrow partial index for event-driven jobs and one for source-config jobs.

CREATE INDEX IF NOT EXISTS ingestion_jobs_webhook_queue_created_idx
  ON ingestion_jobs (status, trigger_type, created_at ASC, id ASC)
  WHERE status IN ('queued', 'retry')
    AND trigger_type = 'webhook';

CREATE INDEX IF NOT EXISTS ingestion_jobs_source_queue_created_idx
  ON ingestion_jobs (status, source_config_id, created_at ASC, id ASC)
  WHERE status IN ('queued', 'retry')
    AND source_config_id IS NOT NULL;
