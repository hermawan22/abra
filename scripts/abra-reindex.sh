#!/usr/bin/env bash
set -euo pipefail

DATABASE_URL="${DATABASE_URL:-${ABRA_DATABASE_URL:-}}"
DRY_RUN="${ABRA_DRY_RUN:-1}"
CONCURRENTLY="${ABRA_REINDEX_CONCURRENTLY:-1}"
STRICT="${ABRA_REINDEX_STRICT:-0}"
PSQL_BIN="${PSQL_BIN:-psql}"

DEFAULT_INDEXES=(
  chunks_embedding_idx
  claims_embedding_idx
  chunks_search_idx
  claims_search_idx
  claims_scope_status_idx
  claims_dedupe_idx
  documents_scope_source_idx
  audit_events_created_idx
  audit_events_target_idx
  entities_scope_type_name_active_idx
  entities_scope_type_status_idx
  entities_name_trgm_idx
  entities_search_idx
  entities_embedding_idx
  entities_metadata_gin_idx
  entities_freshness_idx
  entity_aliases_entity_alias_idx
  entity_aliases_scope_alias_idx
  entity_aliases_alias_trgm_idx
  entity_aliases_metadata_gin_idx
  relations_active_edge_idx
  relations_source_traversal_idx
  relations_target_traversal_idx
  relations_claim_idx
  relations_metadata_gin_idx
  relations_freshness_idx
  observations_scope_type_status_idx
  observations_subject_idx
  observations_object_idx
  observations_relation_idx
  observations_claim_idx
  observations_document_chunk_idx
  observations_job_idx
  observations_search_idx
  observations_value_gin_idx
  observations_metadata_gin_idx
  conflicts_scope_status_severity_idx
  conflicts_claim_pair_idx
  conflicts_relation_pair_idx
  conflicts_entity_idx
  conflicts_metadata_gin_idx
  source_configs_scope_type_status_idx
  source_configs_name_trgm_idx
  source_configs_config_gin_idx
  source_configs_freshness_policy_gin_idx
  source_configs_metadata_gin_idx
  ingestion_jobs_status_created_idx
  ingestion_jobs_scope_status_idx
  ingestion_jobs_source_config_idx
  ingestion_jobs_lease_idx
  ingestion_jobs_error_details_gin_idx
  ingestion_jobs_watermarks_gin_idx
  ingestion_jobs_metadata_gin_idx
  agent_profiles_scope_status_idx
  agent_profiles_principal_idx
  agent_profiles_allowed_scopes_gin_idx
  agent_profiles_permissions_gin_idx
  agent_profiles_metadata_gin_idx
  policies_scope_type_status_idx
  policies_agent_priority_idx
  policies_subject_idx
  policies_effect_idx
  policies_rule_gin_idx
  policies_metadata_gin_idx
  policies_acl_scope_subject_priority_idx
  policies_agent_action_scope_priority_idx
  policies_agent_action_scope_subject_priority_idx
  documents_source_config_idx
  documents_ingestion_job_idx
  documents_scope_status_freshness_idx
  documents_metadata_gin_idx
  chunks_scope_idx
  chunks_source_config_idx
  chunks_ingestion_job_idx
  chunks_metadata_gin_idx
  claims_source_config_idx
  claims_ingestion_job_idx
  claims_type_status_idx
  claims_freshness_idx
  claims_metadata_gin_idx
)

fail() {
  echo "abra-reindex: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

is_dry_run() {
  [[ "$DRY_RUN" == "1" || "$DRY_RUN" == "true" ]]
}

read -r -a REQUESTED_INDEXES <<<"${ABRA_REINDEX_INDEXES:-}"
if [[ ${#REQUESTED_INDEXES[@]} -gt 0 ]]; then
  INDEXES=("${REQUESTED_INDEXES[@]}")
else
  INDEXES=("${DEFAULT_INDEXES[@]}")
fi

CLAUSE=""
if [[ "$CONCURRENTLY" == "1" || "$CONCURRENTLY" == "true" ]]; then
  CLAUSE=" CONCURRENTLY"
fi

if is_dry_run; then
  echo "abra-reindex: dry-run; planned commands:"
  for index_name in "${INDEXES[@]}"; do
    echo "REINDEX INDEX${CLAUSE} ${index_name};"
  done
  echo "abra-reindex: set ABRA_DRY_RUN=0 to execute"
  exit 0
fi

if [[ -z "$DATABASE_URL" ]]; then
  fail "set DATABASE_URL or ABRA_DATABASE_URL"
fi

require_command "$PSQL_BIN"

for index_name in "${INDEXES[@]}"; do
  exists="$("$PSQL_BIN" "$DATABASE_URL" -AtX -v ON_ERROR_STOP=1 -c "SELECT to_regclass('${index_name}') IS NOT NULL")"
  if [[ "$exists" != "t" ]]; then
    if [[ "$STRICT" == "1" || "$STRICT" == "true" ]]; then
      fail "index does not exist: ${index_name}"
    fi
    echo "abra-reindex: skipping missing index ${index_name}"
    continue
  fi

  echo "abra-reindex: reindexing ${index_name}"
  "$PSQL_BIN" "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -c "REINDEX INDEX${CLAUSE} ${index_name};"
done

echo "abra-reindex: complete"
