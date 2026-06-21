ALTER TABLE source_configs
  DROP CONSTRAINT IF EXISTS source_configs_status_check;

ALTER TABLE source_configs
  ADD CONSTRAINT source_configs_status_check
  CHECK (status IN ('active', 'paused', 'disabled', 'deleted', 'error'));
