CREATE TABLE IF NOT EXISTS ingestion_job_documents (
  job_id TEXT NOT NULL REFERENCES ingestion_jobs(id) ON DELETE CASCADE,
  document_index INTEGER NOT NULL DEFAULT 0,
  scope TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_id TEXT,
  title TEXT NOT NULL,
  content TEXT NOT NULL,
  source_updated_at TEXT,
  content_checksum TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (job_id, document_index)
);

CREATE INDEX IF NOT EXISTS ingestion_job_documents_scope_idx
  ON ingestion_job_documents (scope, source_type, source_url);
