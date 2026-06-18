-- +goose Up
CREATE TABLE IF NOT EXISTS integration_cursors (
  id TEXT PRIMARY KEY,
  integration_type TEXT NOT NULL,
  target TEXT NOT NULL,
  cursor_value TEXT,
  cursor_time TIMESTAMPTZ,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS integration_cursors_type_target_idx
  ON integration_cursors (integration_type, target);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'integration_cursors_set_updated_at') THEN
    CREATE TRIGGER integration_cursors_set_updated_at
      BEFORE UPDATE ON integration_cursors
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;
END $$;
