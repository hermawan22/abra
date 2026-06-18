-- Performance indexes for v1 hot paths.
-- These are intentionally idempotent and non-invasive so existing self-hosted
-- deployments can apply them during the normal migration flow.

CREATE INDEX IF NOT EXISTS documents_scope_status_ingested_idx
  ON documents (scope, status, ingested_at DESC, id);

CREATE INDEX IF NOT EXISTS chunks_scope_document_chunk_idx
  ON chunks (scope, document_id, chunk_index);

CREATE INDEX IF NOT EXISTS relations_scope_source_status_idx
  ON relations (scope, source_url, status);

CREATE INDEX IF NOT EXISTS relations_scope_updated_idx
  ON relations (scope, updated_at DESC, confidence DESC);

CREATE INDEX IF NOT EXISTS entities_scope_updated_idx
  ON entities (scope, updated_at DESC, confidence DESC);

CREATE INDEX IF NOT EXISTS memory_summaries_scope_updated_idx
  ON memory_summaries (scope, updated_at DESC);

CREATE INDEX IF NOT EXISTS source_configs_status_type_priority_idx
  ON source_configs (status, source_type, priority ASC, updated_at ASC, id ASC);

CREATE INDEX IF NOT EXISTS source_configs_scope_priority_idx
  ON source_configs (scope, priority ASC, updated_at DESC);

CREATE INDEX IF NOT EXISTS ingestion_jobs_scope_created_idx
  ON ingestion_jobs (scope, created_at DESC);

CREATE INDEX IF NOT EXISTS ingestion_jobs_queue_created_idx
  ON ingestion_jobs (status, created_at ASC, id ASC)
  WHERE status IN ('queued', 'retry');

CREATE INDEX IF NOT EXISTS ingestion_jobs_source_pending_idx
  ON ingestion_jobs (source_config_id, status)
  WHERE status IN ('queued', 'retry', 'running');

CREATE INDEX IF NOT EXISTS policies_acl_scope_subject_priority_idx
  ON policies (scope, subject_type, subject_id, priority ASC, created_at DESC)
  WHERE policy_type = 'acl';
