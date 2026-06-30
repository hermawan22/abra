-- Migration 002: add GIN index for memory summary source URL containment

CREATE INDEX IF NOT EXISTS memory_summaries_source_urls_gin_idx
  ON memory_summaries USING gin (source_urls);
