-- Shared rate-limit buckets for replicated API deployments.
-- The API uses this table when a Store is available so limits are enforced
-- across pods instead of per process.

CREATE TABLE IF NOT EXISTS rate_limit_buckets (
  bucket_key TEXT PRIMARY KEY,
  reset_at TIMESTAMPTZ NOT NULL,
  count INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (count >= 0)
);

CREATE INDEX IF NOT EXISTS rate_limit_buckets_updated_idx
  ON rate_limit_buckets (updated_at);
