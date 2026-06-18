CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS documents (
  id TEXT PRIMARY KEY,
  source_type TEXT NOT NULL,
  source_url TEXT NOT NULL,
  source_id TEXT,
  title TEXT NOT NULL,
  scope TEXT NOT NULL,
  content_checksum TEXT NOT NULL,
  source_updated_at TIMESTAMPTZ,
  ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (source_type, source_url, scope)
);

CREATE TABLE IF NOT EXISTS chunks (
  id TEXT PRIMARY KEY,
  document_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
  chunk_index INTEGER NOT NULL,
  content TEXT NOT NULL,
  embedding vector NOT NULL,
  embedding_provider TEXT,
  embedding_model TEXT,
  embedding_dimensions INTEGER NOT NULL DEFAULT 1536,
  search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (embedding_dimensions > 0),
  UNIQUE (document_id, chunk_index)
);

CREATE TABLE IF NOT EXISTS claims (
  id TEXT PRIMARY KEY,
  claim_text TEXT NOT NULL,
  scope TEXT NOT NULL,
  source_url TEXT,
  source_type TEXT,
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  status TEXT NOT NULL DEFAULT 'unverified',
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  embedding vector NOT NULL,
  embedding_provider TEXT,
  embedding_model TEXT,
  embedding_dimensions INTEGER NOT NULL DEFAULT 1536,
  valid_from TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_verified_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', claim_text)) STORED,
  CHECK (embedding_dimensions > 0),
  CHECK (status IN ('verified', 'unverified', 'inferred', 'challenged', 'deprecated', 'expired'))
);

CREATE TABLE IF NOT EXISTS evidence (
  id TEXT PRIMARY KEY,
  claim_id TEXT NOT NULL REFERENCES claims(id) ON DELETE CASCADE,
  document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
  quote TEXT,
  source_url TEXT NOT NULL,
  source_type TEXT,
  source_updated_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS feedback (
  id TEXT PRIMARY KEY,
  claim_id TEXT NOT NULL REFERENCES claims(id) ON DELETE CASCADE,
  verdict TEXT NOT NULL,
  reason TEXT,
  source_url TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (verdict IN ('correct', 'incorrect', 'stale', 'conflict', 'useful', 'not_useful'))
);

CREATE TABLE IF NOT EXISTS audit_events (
  id TEXT PRIMARY KEY,
  event_type TEXT NOT NULL,
  actor TEXT,
  target_type TEXT,
  target_id TEXT,
  scope TEXT,
  source_url TEXT,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS chunks_embedding_idx ON chunks USING hnsw ((embedding::vector(1536)) vector_cosine_ops) WHERE embedding_dimensions = 1536;
CREATE INDEX IF NOT EXISTS claims_embedding_idx ON claims USING hnsw ((embedding::vector(1536)) vector_cosine_ops) WHERE embedding_dimensions = 1536;
CREATE INDEX IF NOT EXISTS chunks_search_idx ON chunks USING gin (search_vector);
CREATE INDEX IF NOT EXISTS claims_search_idx ON claims USING gin (search_vector);
CREATE INDEX IF NOT EXISTS claims_scope_status_idx ON claims (scope, status);
CREATE UNIQUE INDEX IF NOT EXISTS claims_dedupe_idx ON claims (scope, COALESCE(source_url, ''), claim_text);
CREATE INDEX IF NOT EXISTS documents_scope_source_idx ON documents (scope, source_type);
CREATE INDEX IF NOT EXISTS audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX IF NOT EXISTS audit_events_target_idx ON audit_events (target_type, target_id);
