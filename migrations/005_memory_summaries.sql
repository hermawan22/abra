CREATE TABLE IF NOT EXISTS memory_summaries (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  level TEXT NOT NULL,
  summary_key TEXT NOT NULL,
  title TEXT NOT NULL,
  summary TEXT NOT NULL,
  source_count INTEGER NOT NULL DEFAULT 0,
  relation_count INTEGER NOT NULL DEFAULT 0,
  token_estimate INTEGER NOT NULL DEFAULT 0,
  source_urls JSONB NOT NULL DEFAULT '[]'::jsonb,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', title || ' ' || summary || ' ' || summary_key)
  ) STORED,
  UNIQUE (scope, level, summary_key),
  CHECK (level IN ('source', 'repo', 'module', 'file', 'route', 'component', 'symbol', 'package', 'decision')),
  CHECK (source_count >= 0),
  CHECK (relation_count >= 0),
  CHECK (token_estimate >= 0)
);

CREATE INDEX IF NOT EXISTS memory_summaries_scope_level_idx ON memory_summaries (scope, level, updated_at DESC);
CREATE INDEX IF NOT EXISTS memory_summaries_search_idx ON memory_summaries USING gin (search_vector);
