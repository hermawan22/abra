-- +goose Up
CREATE TABLE IF NOT EXISTS approval_requests (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  scope TEXT NOT NULL,
  target_type TEXT,
  target_id TEXT,
  status TEXT NOT NULL DEFAULT 'pending',
  requested_by TEXT,
  decided_by TEXT,
  reason TEXT,
  decision_reason TEXT,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  expires_at TIMESTAMPTZ,
  decided_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (status IN ('pending', 'approved', 'rejected', 'canceled', 'expired')),
  CHECK (action IN (
    'agent_write',
    'forget_claim',
    'challenge_claim',
    'source_authority_change',
    'scope_expansion',
    'backfill',
    'connector_enable',
    'acl_change',
    'other'
  ))
);

CREATE INDEX IF NOT EXISTS approval_requests_scope_status_idx ON approval_requests (scope, status, created_at DESC);
CREATE INDEX IF NOT EXISTS approval_requests_target_idx ON approval_requests (target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS approval_requests_payload_gin_idx ON approval_requests USING gin (payload);
CREATE INDEX IF NOT EXISTS approval_requests_metadata_gin_idx ON approval_requests USING gin (metadata);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'approval_requests_set_updated_at') THEN
    CREATE TRIGGER approval_requests_set_updated_at
      BEFORE UPDATE ON approval_requests
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;
END $$;
