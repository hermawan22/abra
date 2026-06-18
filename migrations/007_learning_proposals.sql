CREATE TABLE IF NOT EXISTS learning_proposals (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  proposal_type TEXT NOT NULL,
  title TEXT NOT NULL,
  rationale TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  target_type TEXT,
  target_id TEXT,
  source_url TEXT,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT,
  reviewed_by TEXT,
  review_reason TEXT,
  approval_id TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reviewed_at TIMESTAMPTZ,
  CHECK (proposal_type IN ('claim', 'challenge', 'source_refresh', 'summary_rebuild', 'ingestion', 'policy', 'graph', 'other')),
  CHECK (status IN ('pending', 'accepted', 'rejected', 'applied', 'canceled')),
  CHECK (confidence >= 0 AND confidence <= 1)
);

CREATE INDEX IF NOT EXISTS learning_proposals_scope_status_idx
  ON learning_proposals (scope, status, created_at DESC);

CREATE INDEX IF NOT EXISTS learning_proposals_target_idx
  ON learning_proposals (target_type, target_id, created_at DESC);

CREATE INDEX IF NOT EXISTS learning_proposals_payload_gin_idx
  ON learning_proposals USING gin (payload);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'learning_proposals_set_updated_at') THEN
    CREATE TRIGGER learning_proposals_set_updated_at
      BEFORE UPDATE ON learning_proposals
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;
END $$;
