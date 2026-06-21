ALTER TABLE claims
  ADD COLUMN IF NOT EXISTS valid_from TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS supersedes_claim_id TEXT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_valid_window_check_v1') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_valid_window_check_v1
      CHECK (expires_at IS NULL OR valid_from IS NULL OR expires_at > valid_from);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_no_self_supersede_check_v1') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_no_self_supersede_check_v1
      CHECK (supersedes_claim_id IS NULL OR supersedes_claim_id <> id);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_supersedes_claim_id_fkey') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_supersedes_claim_id_fkey
      FOREIGN KEY (supersedes_claim_id) REFERENCES claims(id) ON DELETE SET NULL;
  END IF;
END;
$$;

UPDATE claims
SET status = 'expired',
    freshness_status = 'expired',
    freshness_checked_at = now(),
    updated_at = now()
WHERE expires_at IS NOT NULL
  AND expires_at <= now()
  AND status NOT IN ('deprecated', 'expired');

CREATE INDEX IF NOT EXISTS claims_temporal_lifecycle_idx
  ON claims (scope, status, valid_from, expires_at);

CREATE INDEX IF NOT EXISTS claims_supersedes_active_idx
  ON claims (supersedes_claim_id, scope, status, valid_from, expires_at)
  WHERE supersedes_claim_id IS NOT NULL;
