-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE OR REPLACE FUNCTION abra_set_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;

CREATE TABLE IF NOT EXISTS source_configs (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  source_type TEXT NOT NULL,
  name TEXT NOT NULL,
  base_url TEXT,
  connector_kind TEXT NOT NULL DEFAULT 'generic',
  status TEXT NOT NULL DEFAULT 'active',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  freshness_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  schedule_cron TEXT,
  priority INTEGER NOT NULL DEFAULT 100,
  credentials_ref TEXT,
  redact_pii BOOLEAN NOT NULL DEFAULT true,
  config JSONB NOT NULL DEFAULT '{}'::jsonb,
  last_success_at TIMESTAMPTZ,
  last_error_at TIMESTAMPTZ,
  last_error TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (scope, source_type, name),
  CHECK (status IN ('active', 'paused', 'disabled', 'error')),
  CHECK (authority_score >= 0 AND authority_score <= 1),
  CHECK (priority >= 0)
);

CREATE TABLE IF NOT EXISTS ingestion_jobs (
  id TEXT PRIMARY KEY,
  source_config_id TEXT REFERENCES source_configs(id) ON DELETE SET NULL,
  scope TEXT NOT NULL,
  source_type TEXT NOT NULL,
  source_url TEXT,
  trigger_type TEXT NOT NULL DEFAULT 'manual',
  status TEXT NOT NULL DEFAULT 'queued',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  lease_owner TEXT,
  heartbeat_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  documents_seen INTEGER NOT NULL DEFAULT 0,
  documents_changed INTEGER NOT NULL DEFAULT 0,
  chunks_written INTEGER NOT NULL DEFAULT 0,
  claims_written INTEGER NOT NULL DEFAULT 0,
  entities_written INTEGER NOT NULL DEFAULT 0,
  observations_written INTEGER NOT NULL DEFAULT 0,
  error_message TEXT,
  error_details JSONB NOT NULL DEFAULT '{}'::jsonb,
  watermarks JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  CHECK (trigger_type IN ('manual', 'schedule', 'webhook', 'backfill', 'revalidate')),
  CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'canceled', 'retry')),
  CHECK (attempts >= 0),
  CHECK (max_attempts > 0),
  CHECK (finished_at IS NULL OR started_at IS NULL OR finished_at >= started_at)
);

ALTER TABLE documents
  ADD COLUMN IF NOT EXISTS source_config_id TEXT,
  ADD COLUMN IF NOT EXISTS ingestion_job_id TEXT,
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active',
  ADD COLUMN IF NOT EXISTS authority TEXT NOT NULL DEFAULT 'manual-unverified',
  ADD COLUMN IF NOT EXISTS authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  ADD COLUMN IF NOT EXISTS freshness_status TEXT NOT NULL DEFAULT 'unknown',
  ADD COLUMN IF NOT EXISTS freshness_checked_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS last_verified_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE chunks
  ADD COLUMN IF NOT EXISTS scope TEXT,
  ADD COLUMN IF NOT EXISTS source_config_id TEXT,
  ADD COLUMN IF NOT EXISTS ingestion_job_id TEXT,
  ADD COLUMN IF NOT EXISTS embedding_provider TEXT,
  ADD COLUMN IF NOT EXISTS embedding_model TEXT,
  ADD COLUMN IF NOT EXISTS embedding_dimensions INTEGER NOT NULL DEFAULT 1536,
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE chunks ch
SET scope = d.scope
FROM documents d
WHERE ch.document_id = d.id
  AND ch.scope IS NULL;

ALTER TABLE claims
  ADD COLUMN IF NOT EXISTS claim_type TEXT NOT NULL DEFAULT 'fact',
  ADD COLUMN IF NOT EXISTS source_config_id TEXT,
  ADD COLUMN IF NOT EXISTS ingestion_job_id TEXT,
  ADD COLUMN IF NOT EXISTS authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  ADD COLUMN IF NOT EXISTS freshness_status TEXT NOT NULL DEFAULT 'unknown',
  ADD COLUMN IF NOT EXISTS freshness_checked_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS embedding_provider TEXT,
  ADD COLUMN IF NOT EXISTS embedding_model TEXT,
  ADD COLUMN IF NOT EXISTS embedding_dimensions INTEGER NOT NULL DEFAULT 1536,
  ADD COLUMN IF NOT EXISTS supersedes_claim_id TEXT;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'documents_source_config_id_fkey') THEN
    ALTER TABLE documents
      ADD CONSTRAINT documents_source_config_id_fkey
      FOREIGN KEY (source_config_id) REFERENCES source_configs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'documents_ingestion_job_id_fkey') THEN
    ALTER TABLE documents
      ADD CONSTRAINT documents_ingestion_job_id_fkey
      FOREIGN KEY (ingestion_job_id) REFERENCES ingestion_jobs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'documents_status_check_v1') THEN
    ALTER TABLE documents
      ADD CONSTRAINT documents_status_check_v1
      CHECK (status IN ('active', 'stale', 'deprecated', 'deleted'));
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'documents_authority_score_check_v1') THEN
    ALTER TABLE documents
      ADD CONSTRAINT documents_authority_score_check_v1
      CHECK (authority_score >= 0 AND authority_score <= 1);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'documents_freshness_status_check_v1') THEN
    ALTER TABLE documents
      ADD CONSTRAINT documents_freshness_status_check_v1
      CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown'));
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chunks_source_config_id_fkey') THEN
    ALTER TABLE chunks
      ADD CONSTRAINT chunks_source_config_id_fkey
      FOREIGN KEY (source_config_id) REFERENCES source_configs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chunks_ingestion_job_id_fkey') THEN
    ALTER TABLE chunks
      ADD CONSTRAINT chunks_ingestion_job_id_fkey
      FOREIGN KEY (ingestion_job_id) REFERENCES ingestion_jobs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chunks_embedding_dimensions_check_v1') THEN
    ALTER TABLE chunks
      ADD CONSTRAINT chunks_embedding_dimensions_check_v1
      CHECK (embedding_dimensions > 0);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_source_config_id_fkey') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_source_config_id_fkey
      FOREIGN KEY (source_config_id) REFERENCES source_configs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_ingestion_job_id_fkey') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_ingestion_job_id_fkey
      FOREIGN KEY (ingestion_job_id) REFERENCES ingestion_jobs(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_supersedes_claim_id_fkey') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_supersedes_claim_id_fkey
      FOREIGN KEY (supersedes_claim_id) REFERENCES claims(id) ON DELETE SET NULL;
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_authority_score_check_v1') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_authority_score_check_v1
      CHECK (authority_score >= 0 AND authority_score <= 1);
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_freshness_status_check_v1') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_freshness_status_check_v1
      CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown'));
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'claims_embedding_dimensions_check_v1') THEN
    ALTER TABLE claims
      ADD CONSTRAINT claims_embedding_dimensions_check_v1
      CHECK (embedding_dimensions > 0);
  END IF;
END;
$$;

CREATE TABLE IF NOT EXISTS entities (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  canonical_name TEXT NOT NULL,
  description TEXT,
  status TEXT NOT NULL DEFAULT 'active',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  freshness_status TEXT NOT NULL DEFAULT 'unknown',
  freshness_checked_at TIMESTAMPTZ,
  source_config_id TEXT REFERENCES source_configs(id) ON DELETE SET NULL,
  ingestion_job_id TEXT REFERENCES ingestion_jobs(id) ON DELETE SET NULL,
  source_url TEXT,
  source_type TEXT,
  source_id TEXT,
  source_updated_at TIMESTAMPTZ,
  valid_from TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_verified_at TIMESTAMPTZ,
  embedding vector,
  embedding_provider TEXT,
  embedding_model TEXT,
  embedding_dimensions INTEGER NOT NULL DEFAULT 1536,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', canonical_name || ' ' || COALESCE(description, ''))
  ) STORED,
  CHECK (status IN ('active', 'candidate', 'merged', 'deprecated', 'deleted')),
  CHECK (authority_score >= 0 AND authority_score <= 1),
  CHECK (confidence >= 0 AND confidence <= 1),
  CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown')),
  CHECK (embedding_dimensions > 0),
  CHECK (expires_at IS NULL OR valid_from IS NULL OR expires_at > valid_from)
);

CREATE TABLE IF NOT EXISTS entity_aliases (
  id TEXT PRIMARY KEY,
  entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
  scope TEXT NOT NULL,
  alias TEXT NOT NULL,
  alias_type TEXT NOT NULL DEFAULT 'synonym',
  status TEXT NOT NULL DEFAULT 'active',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  source_url TEXT,
  source_type TEXT,
  source_id TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  CHECK (status IN ('active', 'candidate', 'deprecated', 'deleted')),
  CHECK (confidence >= 0 AND confidence <= 1)
);

CREATE TABLE IF NOT EXISTS relations (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  relation_type TEXT NOT NULL,
  source_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
  target_entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
  claim_id TEXT REFERENCES claims(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'active',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  freshness_status TEXT NOT NULL DEFAULT 'unknown',
  freshness_checked_at TIMESTAMPTZ,
  source_config_id TEXT REFERENCES source_configs(id) ON DELETE SET NULL,
  ingestion_job_id TEXT REFERENCES ingestion_jobs(id) ON DELETE SET NULL,
  source_url TEXT,
  source_type TEXT,
  source_id TEXT,
  valid_from TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_verified_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  CHECK (source_entity_id <> target_entity_id),
  CHECK (status IN ('active', 'candidate', 'challenged', 'deprecated', 'expired')),
  CHECK (authority_score >= 0 AND authority_score <= 1),
  CHECK (confidence >= 0 AND confidence <= 1),
  CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown')),
  CHECK (expires_at IS NULL OR valid_from IS NULL OR expires_at > valid_from)
);

CREATE TABLE IF NOT EXISTS observations (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  observation_type TEXT NOT NULL,
  observation_text TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'proposed',
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  authority_score DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.35,
  freshness_status TEXT NOT NULL DEFAULT 'unknown',
  freshness_checked_at TIMESTAMPTZ,
  subject_entity_id TEXT REFERENCES entities(id) ON DELETE SET NULL,
  object_entity_id TEXT REFERENCES entities(id) ON DELETE SET NULL,
  relation_id TEXT REFERENCES relations(id) ON DELETE SET NULL,
  claim_id TEXT REFERENCES claims(id) ON DELETE SET NULL,
  document_id TEXT REFERENCES documents(id) ON DELETE SET NULL,
  chunk_id TEXT REFERENCES chunks(id) ON DELETE SET NULL,
  source_config_id TEXT REFERENCES source_configs(id) ON DELETE SET NULL,
  ingestion_job_id TEXT REFERENCES ingestion_jobs(id) ON DELETE SET NULL,
  source_url TEXT,
  source_type TEXT,
  source_id TEXT,
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  valid_from TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_verified_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  value JSONB NOT NULL DEFAULT '{}'::jsonb,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', observation_text)) STORED,
  CHECK (status IN ('raw', 'proposed', 'accepted', 'rejected', 'challenged', 'deprecated', 'expired')),
  CHECK (authority_score >= 0 AND authority_score <= 1),
  CHECK (confidence >= 0 AND confidence <= 1),
  CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown')),
  CHECK (expires_at IS NULL OR valid_from IS NULL OR expires_at > valid_from)
);

CREATE TABLE IF NOT EXISTS conflicts (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  conflict_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'open',
  severity TEXT NOT NULL DEFAULT 'medium',
  primary_claim_id TEXT REFERENCES claims(id) ON DELETE CASCADE,
  conflicting_claim_id TEXT REFERENCES claims(id) ON DELETE CASCADE,
  primary_relation_id TEXT REFERENCES relations(id) ON DELETE CASCADE,
  conflicting_relation_id TEXT REFERENCES relations(id) ON DELETE CASCADE,
  entity_id TEXT REFERENCES entities(id) ON DELETE SET NULL,
  detected_by TEXT,
  detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  resolved_at TIMESTAMPTZ,
  resolved_by TEXT,
  resolution TEXT,
  authority TEXT NOT NULL DEFAULT 'system-detected',
  freshness_status TEXT NOT NULL DEFAULT 'unknown',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  CHECK (status IN ('open', 'reviewing', 'resolved', 'suppressed')),
  CHECK (severity IN ('low', 'medium', 'high', 'blocking')),
  CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown')),
  CHECK (
    (primary_claim_id IS NOT NULL AND conflicting_claim_id IS NOT NULL)
    OR (primary_relation_id IS NOT NULL AND conflicting_relation_id IS NOT NULL)
  )
);

CREATE TABLE IF NOT EXISTS agent_profiles (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  profile_key TEXT NOT NULL,
  display_name TEXT NOT NULL,
  agent_type TEXT NOT NULL DEFAULT 'agent',
  status TEXT NOT NULL DEFAULT 'active',
  principal_ref TEXT,
  default_scope TEXT,
  allowed_scopes TEXT[] NOT NULL DEFAULT '{}'::text[],
  denied_scopes TEXT[] NOT NULL DEFAULT '{}'::text[],
  permissions JSONB NOT NULL DEFAULT '{}'::jsonb,
  memory_preferences JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (scope, profile_key),
  CHECK (status IN ('active', 'disabled', 'deleted'))
);

CREATE TABLE IF NOT EXISTS policies (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  policy_type TEXT NOT NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  priority INTEGER NOT NULL DEFAULT 100,
  agent_profile_id TEXT REFERENCES agent_profiles(id) ON DELETE CASCADE,
  subject_type TEXT,
  subject_id TEXT,
  effect TEXT NOT NULL,
  authority TEXT NOT NULL DEFAULT 'manual-unverified',
  freshness_status TEXT NOT NULL DEFAULT 'unknown',
  rule JSONB NOT NULL DEFAULT '{}'::jsonb,
  valid_from TIMESTAMPTZ,
  expires_at TIMESTAMPTZ,
  last_evaluated_at TIMESTAMPTZ,
  created_by TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE (scope, policy_type, name),
  CHECK (status IN ('active', 'disabled', 'draft', 'deleted')),
  CHECK (priority >= 0),
  CHECK (effect IN ('allow', 'deny', 'require_review', 'redact', 'rank_modifier')),
  CHECK (freshness_status IN ('fresh', 'stale', 'expired', 'unknown')),
  CHECK (expires_at IS NULL OR valid_from IS NULL OR expires_at > valid_from)
);

CREATE UNIQUE INDEX IF NOT EXISTS entities_scope_type_name_active_idx
  ON entities (scope, entity_type, lower(canonical_name))
  WHERE status NOT IN ('deprecated', 'deleted');
CREATE INDEX IF NOT EXISTS entities_scope_type_status_idx ON entities (scope, entity_type, status);
CREATE INDEX IF NOT EXISTS entities_name_trgm_idx ON entities USING gin (canonical_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS entities_search_idx ON entities USING gin (search_vector);
CREATE INDEX IF NOT EXISTS entities_embedding_1536_idx
  ON entities USING hnsw ((embedding::vector(1536)) vector_cosine_ops)
  WHERE embedding IS NOT NULL AND embedding_dimensions = 1536;
CREATE INDEX IF NOT EXISTS entities_metadata_gin_idx ON entities USING gin (metadata);
CREATE INDEX IF NOT EXISTS entities_freshness_idx ON entities (scope, freshness_status, last_verified_at DESC NULLS LAST);

CREATE UNIQUE INDEX IF NOT EXISTS entity_aliases_entity_alias_idx
  ON entity_aliases (entity_id, lower(alias))
  WHERE status NOT IN ('deprecated', 'deleted');
CREATE INDEX IF NOT EXISTS entity_aliases_scope_alias_idx ON entity_aliases (scope, lower(alias));
CREATE INDEX IF NOT EXISTS entity_aliases_alias_trgm_idx ON entity_aliases USING gin (alias gin_trgm_ops);
CREATE INDEX IF NOT EXISTS entity_aliases_metadata_gin_idx ON entity_aliases USING gin (metadata);

CREATE UNIQUE INDEX IF NOT EXISTS relations_active_edge_idx
  ON relations (scope, relation_type, source_entity_id, target_entity_id)
  WHERE status NOT IN ('deprecated', 'expired');
CREATE INDEX IF NOT EXISTS relations_source_traversal_idx ON relations (scope, source_entity_id, relation_type, status);
CREATE INDEX IF NOT EXISTS relations_target_traversal_idx ON relations (scope, target_entity_id, relation_type, status);
CREATE INDEX IF NOT EXISTS relations_claim_idx ON relations (claim_id);
CREATE INDEX IF NOT EXISTS relations_metadata_gin_idx ON relations USING gin (metadata);
CREATE INDEX IF NOT EXISTS relations_freshness_idx ON relations (scope, freshness_status, last_verified_at DESC NULLS LAST);

CREATE INDEX IF NOT EXISTS observations_scope_type_status_idx ON observations (scope, observation_type, status);
CREATE INDEX IF NOT EXISTS observations_subject_idx ON observations (scope, subject_entity_id, observation_type);
CREATE INDEX IF NOT EXISTS observations_object_idx ON observations (scope, object_entity_id, observation_type);
CREATE INDEX IF NOT EXISTS observations_relation_idx ON observations (relation_id);
CREATE INDEX IF NOT EXISTS observations_claim_idx ON observations (claim_id);
CREATE INDEX IF NOT EXISTS observations_document_chunk_idx ON observations (document_id, chunk_id);
CREATE INDEX IF NOT EXISTS observations_job_idx ON observations (ingestion_job_id, created_at DESC);
CREATE INDEX IF NOT EXISTS observations_search_idx ON observations USING gin (search_vector);
CREATE INDEX IF NOT EXISTS observations_value_gin_idx ON observations USING gin (value);
CREATE INDEX IF NOT EXISTS observations_metadata_gin_idx ON observations USING gin (metadata);

CREATE INDEX IF NOT EXISTS conflicts_scope_status_severity_idx ON conflicts (scope, status, severity);
CREATE INDEX IF NOT EXISTS conflicts_claim_pair_idx ON conflicts (primary_claim_id, conflicting_claim_id);
CREATE INDEX IF NOT EXISTS conflicts_relation_pair_idx ON conflicts (primary_relation_id, conflicting_relation_id);
CREATE INDEX IF NOT EXISTS conflicts_entity_idx ON conflicts (entity_id, status);
CREATE INDEX IF NOT EXISTS conflicts_metadata_gin_idx ON conflicts USING gin (metadata);

CREATE INDEX IF NOT EXISTS source_configs_scope_type_status_idx ON source_configs (scope, source_type, status);
CREATE INDEX IF NOT EXISTS source_configs_name_trgm_idx ON source_configs USING gin (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS source_configs_config_gin_idx ON source_configs USING gin (config);
CREATE INDEX IF NOT EXISTS source_configs_freshness_policy_gin_idx ON source_configs USING gin (freshness_policy);
CREATE INDEX IF NOT EXISTS source_configs_metadata_gin_idx ON source_configs USING gin (metadata);

CREATE INDEX IF NOT EXISTS ingestion_jobs_status_created_idx ON ingestion_jobs (status, created_at);
CREATE INDEX IF NOT EXISTS ingestion_jobs_scope_status_idx ON ingestion_jobs (scope, status, created_at DESC);
CREATE INDEX IF NOT EXISTS ingestion_jobs_source_config_idx ON ingestion_jobs (source_config_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ingestion_jobs_lease_idx ON ingestion_jobs (status, heartbeat_at) WHERE status IN ('queued', 'running', 'retry');
CREATE INDEX IF NOT EXISTS ingestion_jobs_error_details_gin_idx ON ingestion_jobs USING gin (error_details);
CREATE INDEX IF NOT EXISTS ingestion_jobs_watermarks_gin_idx ON ingestion_jobs USING gin (watermarks);
CREATE INDEX IF NOT EXISTS ingestion_jobs_metadata_gin_idx ON ingestion_jobs USING gin (metadata);

CREATE INDEX IF NOT EXISTS agent_profiles_scope_status_idx ON agent_profiles (scope, status);
CREATE INDEX IF NOT EXISTS agent_profiles_principal_idx ON agent_profiles (principal_ref) WHERE principal_ref IS NOT NULL;
CREATE INDEX IF NOT EXISTS agent_profiles_allowed_scopes_gin_idx ON agent_profiles USING gin (allowed_scopes);
CREATE INDEX IF NOT EXISTS agent_profiles_permissions_gin_idx ON agent_profiles USING gin (permissions);
CREATE INDEX IF NOT EXISTS agent_profiles_metadata_gin_idx ON agent_profiles USING gin (metadata);

CREATE INDEX IF NOT EXISTS policies_scope_type_status_idx ON policies (scope, policy_type, status);
CREATE INDEX IF NOT EXISTS policies_agent_priority_idx ON policies (agent_profile_id, priority, status);
CREATE INDEX IF NOT EXISTS policies_subject_idx ON policies (subject_type, subject_id, status);
CREATE INDEX IF NOT EXISTS policies_effect_idx ON policies (effect, status);
CREATE INDEX IF NOT EXISTS policies_rule_gin_idx ON policies USING gin (rule);
CREATE INDEX IF NOT EXISTS policies_metadata_gin_idx ON policies USING gin (metadata);

CREATE INDEX IF NOT EXISTS documents_source_config_idx ON documents (source_config_id);
CREATE INDEX IF NOT EXISTS documents_ingestion_job_idx ON documents (ingestion_job_id);
CREATE INDEX IF NOT EXISTS documents_scope_status_freshness_idx ON documents (scope, status, freshness_status);
CREATE INDEX IF NOT EXISTS documents_metadata_gin_idx ON documents USING gin (metadata);

CREATE INDEX IF NOT EXISTS chunks_scope_idx ON chunks (scope);
CREATE INDEX IF NOT EXISTS chunks_source_config_idx ON chunks (source_config_id);
CREATE INDEX IF NOT EXISTS chunks_ingestion_job_idx ON chunks (ingestion_job_id);
CREATE INDEX IF NOT EXISTS chunks_metadata_gin_idx ON chunks USING gin (metadata);

CREATE INDEX IF NOT EXISTS claims_source_config_idx ON claims (source_config_id);
CREATE INDEX IF NOT EXISTS claims_ingestion_job_idx ON claims (ingestion_job_id);
CREATE INDEX IF NOT EXISTS claims_type_status_idx ON claims (scope, claim_type, status);
CREATE INDEX IF NOT EXISTS claims_freshness_idx ON claims (scope, freshness_status, last_verified_at DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS claims_metadata_gin_idx ON claims USING gin (metadata);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'documents_set_updated_at') THEN
    CREATE TRIGGER documents_set_updated_at
      BEFORE UPDATE ON documents
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'chunks_set_updated_at') THEN
    CREATE TRIGGER chunks_set_updated_at
      BEFORE UPDATE ON chunks
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'claims_set_updated_at') THEN
    CREATE TRIGGER claims_set_updated_at
      BEFORE UPDATE ON claims
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'source_configs_set_updated_at') THEN
    CREATE TRIGGER source_configs_set_updated_at
      BEFORE UPDATE ON source_configs
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'ingestion_jobs_set_updated_at') THEN
    CREATE TRIGGER ingestion_jobs_set_updated_at
      BEFORE UPDATE ON ingestion_jobs
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'entities_set_updated_at') THEN
    CREATE TRIGGER entities_set_updated_at
      BEFORE UPDATE ON entities
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'entity_aliases_set_updated_at') THEN
    CREATE TRIGGER entity_aliases_set_updated_at
      BEFORE UPDATE ON entity_aliases
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'relations_set_updated_at') THEN
    CREATE TRIGGER relations_set_updated_at
      BEFORE UPDATE ON relations
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'observations_set_updated_at') THEN
    CREATE TRIGGER observations_set_updated_at
      BEFORE UPDATE ON observations
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'conflicts_set_updated_at') THEN
    CREATE TRIGGER conflicts_set_updated_at
      BEFORE UPDATE ON conflicts
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'agent_profiles_set_updated_at') THEN
    CREATE TRIGGER agent_profiles_set_updated_at
      BEFORE UPDATE ON agent_profiles
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;

  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'policies_set_updated_at') THEN
    CREATE TRIGGER policies_set_updated_at
      BEFORE UPDATE ON policies
      FOR EACH ROW EXECUTE FUNCTION abra_set_updated_at();
  END IF;
END;
$$;
