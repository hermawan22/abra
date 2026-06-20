CREATE INDEX IF NOT EXISTS observations_scope_observed_idx ON observations (scope, observed_at DESC);
CREATE INDEX IF NOT EXISTS observations_scope_status_observed_idx ON observations (scope, status, observed_at DESC);
