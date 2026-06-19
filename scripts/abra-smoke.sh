#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${ABRA_BASE_URL:-http://127.0.0.1:18080}"
TOKEN="${ABRA_API_TOKEN:-dev-token}"
WEBHOOK_SECRET="${ABRA_WEBHOOK_SECRET:-dev-webhook-secret}"
STAMP="$(date -u +%Y%m%d%H%M%S)"
SCOPE="${ABRA_SMOKE_SCOPE:-team:smoke:${STAMP}}"
SOURCE_NAME="smoke-${STAMP}"
SOURCE_URL="file://abra-smoke-${STAMP}.md"
WEBHOOK_SOURCE_URL="https://jira.example.local/browse/ABRA-${STAMP}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

json_get() {
  local path="$1"
  curl -fsS -H "Authorization: Bearer ${TOKEN}" "${BASE_URL}${path}"
}

json_post() {
  local path="$1"
  local body="$2"
  curl -fsS \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "content-type: application/json" \
    -d "$body" \
    "${BASE_URL}${path}"
}

json_post_signed() {
  local path="$1"
  local body="$2"
  local signature
  signature="$(BODY="$body" WEBHOOK_SECRET="$WEBHOOK_SECRET" node -e 'const crypto=require("crypto"); process.stdout.write(crypto.createHmac("sha256", process.env.WEBHOOK_SECRET).update(process.env.BODY).digest("hex"))')"
  curl -fsS \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "content-type: application/json" \
    -H "x-abra-signature: sha256=${signature}" \
    -d "$body" \
    "${BASE_URL}${path}"
}

expect_get_status() {
  local expected="$1"
  local path="$2"
  local code
  code="$(curl -sS -o "${tmpdir}/status-body" -w '%{http_code}' "${BASE_URL}${path}")"
  if [[ "$code" != "$expected" ]]; then
    echo "expected ${path} to return ${expected}, got ${code}" >&2
    cat "${tmpdir}/status-body" >&2 || true
    exit 1
  fi
}

expect_post_status() {
  local expected="$1"
  local path="$2"
  local body="$3"
  local code
  code="$(curl -sS -o "${tmpdir}/status-body" -w '%{http_code}' -H "content-type: application/json" -d "$body" "${BASE_URL}${path}")"
  if [[ "$code" != "$expected" ]]; then
    echo "expected ${path} to return ${expected}, got ${code}" >&2
    cat "${tmpdir}/status-body" >&2 || true
    exit 1
  fi
}

expect_auth_post_status() {
  local expected="$1"
  local path="$2"
  local body="$3"
  local code
  code="$(curl -sS -o "${tmpdir}/status-body" -w '%{http_code}' -H "Authorization: Bearer ${TOKEN}" -H "content-type: application/json" -d "$body" "${BASE_URL}${path}")"
  if [[ "$code" != "$expected" ]]; then
    echo "expected authenticated ${path} to return ${expected}, got ${code}" >&2
    cat "${tmpdir}/status-body" >&2 || true
    exit 1
  fi
}

expect_post_status 401 "/recall" "{\"query\":\"auth\",\"scope\":\"${SCOPE}\"}"
expect_get_status 401 "/metrics"

json_get "/readyz" >"${tmpdir}/ready.json"
json_get "/metrics" >"${tmpdir}/metrics.txt"
grep -q "abra_http_requests_total" "${tmpdir}/metrics.txt"

json_post "/approvals" "{
  \"action\":\"source_authority_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"source_config\",
  \"target_id\":\"${SCOPE}/local_repo/${SOURCE_NAME}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify source authority approval before writing trusted source config\",
  \"payload\":{\"source_name\":\"${SOURCE_NAME}\",\"authority\":\"team-convention\",\"authority_score\":0.7}
}" >"${tmpdir}/source-approval.json"
source_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/source-approval.json")"
json_post "/approvals/${source_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke source authority approval\"
}" >"${tmpdir}/source-approval-decision.json"

expect_auth_post_status 400 "/sources/configs" "{
  \"name\":\"invalid-${SOURCE_NAME}\",
  \"source_type\":\"local_repo\",
  \"scope\":\"${SCOPE}\",
  \"base_url\":\"https://example.com/not-mounted\",
  \"connector_kind\":\"generic\",
  \"status\":\"active\",
  \"config\":{}
}"

json_post "/approvals" "{
  \"action\":\"acl_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"acl_policy\",
  \"target_id\":\"${SCOPE}/smoke-allow-recall\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify ACL policy approval before writing policy\",
  \"payload\":{\"principal\":\"agent:agent-alpha\",\"action\":\"recall\"}
}" >"${tmpdir}/acl-approval.json"
acl_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/acl-approval.json")"
json_post "/approvals/${acl_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke ACL approval\"
}" >"${tmpdir}/acl-approval-decision.json"

json_post "/approvals" "{
  \"action\":\"acl_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"acl_policy\",
  \"target_id\":\"${SCOPE}/mcp-allow-recall\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify MCP ACL policy approval before writing policy\",
  \"payload\":{\"principal\":\"agent:agent-alpha\",\"action\":\"recall\"}
}" >"${tmpdir}/mcp-acl-approval.json"
mcp_acl_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/mcp-acl-approval.json")"
json_post "/approvals/${mcp_acl_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke MCP ACL approval\"
}" >"${tmpdir}/mcp-acl-approval-decision.json"

json_post "/approvals" "{
  \"action\":\"acl_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"agent_policy\",
  \"target_id\":\"${SCOPE}/smoke-require-agent-review\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify agent action policy approval before writing policy\",
  \"payload\":{\"principal\":\"agent:agent-alpha\",\"action\":\"agent_write\"}
}" >"${tmpdir}/agent-policy-approval.json"
agent_policy_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/agent-policy-approval.json")"
json_post "/approvals/${agent_policy_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke agent action policy approval\"
}" >"${tmpdir}/agent-policy-approval-decision.json"

json_post "/approvals" "{
  \"action\":\"acl_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"agent_policy\",
  \"target_id\":\"${SCOPE}/mcp-require-agent-review\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify MCP agent action policy approval before writing policy\",
  \"payload\":{\"principal\":\"agent:agent-alpha\",\"action\":\"agent_write\"}
}" >"${tmpdir}/mcp-agent-policy-approval.json"
mcp_agent_policy_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/mcp-agent-policy-approval.json")"
json_post "/approvals/${mcp_agent_policy_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke MCP agent action policy approval\"
}" >"${tmpdir}/mcp-agent-policy-approval-decision.json"

json_post "/approvals" "{
  \"action\":\"acl_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"agent_profile\",
  \"target_id\":\"${SCOPE}/agent-alpha\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify agent profile approval before writing configurable agent runtime profile\",
  \"payload\":{\"profile_key\":\"agent-alpha\",\"principal_ref\":\"agent:agent-alpha\"}
}" >"${tmpdir}/agent-profile-approval.json"
agent_profile_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/agent-profile-approval.json")"
json_post "/approvals/${agent_profile_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke agent profile approval\"
}" >"${tmpdir}/agent-profile-approval-decision.json"

json_post "/approvals" "{
  \"action\":\"backfill\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"memory_summaries\",
  \"target_id\":\"${SCOPE}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify summary rebuild approval before scope backfill\",
  \"payload\":{\"limit\":10}
}" >"${tmpdir}/rebuild-approval.json"
rebuild_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/rebuild-approval.json")"
json_post "/approvals/${rebuild_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke summary rebuild approval\"
}" >"${tmpdir}/rebuild-approval-decision.json"

json_post "/acl/policies" "{
  \"scope\":\"${SCOPE}\",
  \"name\":\"smoke-allow-recall\",
  \"subject_type\":\"agent\",
  \"subject_id\":\"agent-alpha\",
  \"effect\":\"allow\",
  \"priority\":10,
  \"approval_id\":\"${acl_approval_id}\",
  \"rule\":{\"actions\":[\"recall\"],\"resource_types\":[\"claim\",\"document\"],\"resource_ids\":[\"*\"]},
  \"metadata\":{\"owner\":\"smoke\"}
}" >"${tmpdir}/acl-policy.json"
json_get "/audit/events?scope=${SCOPE}&event_type=acl_policy.upserted&target_type=acl_policy&limit=20" >"${tmpdir}/acl-policy-audit.json"

json_post "/acl/decision" "{
  \"principal_type\":\"agent\",
  \"principal_id\":\"agent-alpha\",
  \"action\":\"recall\",
  \"scope\":\"${SCOPE}\",
  \"resource_type\":\"claim\",
  \"resource_id\":\"smoke\"
}" >"${tmpdir}/acl-decision.json"

json_post "/acl/decision" "{
  \"principal_type\":\"agent\",
  \"principal_id\":\"unknown\",
  \"action\":\"recall\",
  \"scope\":\"${SCOPE}\",
  \"resource_type\":\"claim\",
  \"resource_id\":\"smoke\"
}" >"${tmpdir}/acl-deny.json"

json_post "/agent/policies" "{
  \"scope\":\"${SCOPE}\",
  \"name\":\"smoke-require-agent-review\",
  \"subject_type\":\"agent\",
  \"subject_id\":\"agent-alpha\",
  \"effect\":\"require_review\",
  \"priority\":20,
  \"approval_id\":\"${agent_policy_approval_id}\",
  \"rule\":{\"actions\":[\"agent_write\"],\"target_types\":[\"memory_write\"],\"target_ids\":[\"${SCOPE}\"]},
  \"metadata\":{\"owner\":\"smoke\"}
}" >"${tmpdir}/agent-policy.json"
json_get "/agent/policies?scope=${SCOPE}&limit=10" >"${tmpdir}/agent-policies.json"
json_get "/audit/events?scope=${SCOPE}&event_type=agent_policy.upserted&target_type=agent_policy&limit=20" >"${tmpdir}/agent-policy-audit.json"

json_post "/agent/policy/decision" "{
  \"principal_type\":\"agent\",
  \"principal_id\":\"agent-alpha\",
  \"action\":\"agent_write\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"memory_write\",
  \"target_id\":\"${SCOPE}\"
}" >"${tmpdir}/agent-policy-decision.json"

json_post "/agent/profiles" "{
  \"scope\":\"${SCOPE}\",
  \"profile_key\":\"agent-alpha\",
  \"display_name\":\"Agent Alpha\",
  \"agent_type\":\"frontend\",
  \"principal_ref\":\"agent:agent-alpha\",
  \"default_scope\":\"${SCOPE}\",
  \"allowed_scopes\":[\"${SCOPE}\",\"team:design-system:*\"],
  \"denied_scopes\":[\"team:design-system:secret\"],
  \"permissions\":{\"memory_read\":true,\"memory_write\":\"review\"},
  \"memory_preferences\":{\"token_budget\":900,\"max_queries\":6,\"include_unverified\":true},
  \"metadata\":{\"owner\":\"smoke\"},
  \"created_by\":\"smoke\",
  \"approval_id\":\"${agent_profile_approval_id}\"
}" >"${tmpdir}/agent-profile.json"
json_get "/agent/profiles?scope=${SCOPE}&status=active&limit=10" >"${tmpdir}/agent-profiles.json"
json_get "/audit/events?scope=${SCOPE}&event_type=agent_profile.upserted&target_type=agent_profile&limit=20" >"${tmpdir}/agent-profile-audit.json"

json_post "/approvals" "{
  \"action\":\"agent_write\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"memory_write\",
  \"target_id\":\"${SCOPE}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Create first conflict fixture claim\",
  \"payload\":{\"claim\":\"SmokeConflict frontend e2e framework must use Playwright\"}
}" >"${tmpdir}/conflict-write-approval-a.json"
conflict_write_approval_a_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/conflict-write-approval-a.json")"
json_post "/approvals/${conflict_write_approval_a_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke conflict fixture write approval\"
}" >"${tmpdir}/conflict-write-approval-a-decision.json"

json_post "/claims" "{
  \"claim\":\"SmokeConflict frontend e2e framework must use Playwright for browser tests.\",
  \"scope\":\"${SCOPE}\",
  \"source_url\":\"file://smoke-conflict-playwright-${STAMP}.md\",
  \"source_type\":\"markdown\",
  \"authority\":\"team-convention\",
  \"created_by\":\"agent-alpha\",
  \"approval_id\":\"${conflict_write_approval_a_id}\",
  \"metadata\":{\"fixture\":\"conflict-a\",\"stamp\":\"${STAMP}\"}
}" >"${tmpdir}/conflict-claim-a.json"
conflict_claim_a_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).claim_id))' <"${tmpdir}/conflict-claim-a.json")"

json_post "/approvals" "{
  \"action\":\"agent_write\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"memory_write\",
  \"target_id\":\"${SCOPE}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Create second conflict fixture claim\",
  \"payload\":{\"claim\":\"SmokeConflict frontend e2e framework must use Cypress\"}
}" >"${tmpdir}/conflict-write-approval-b.json"
conflict_write_approval_b_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/conflict-write-approval-b.json")"
json_post "/approvals/${conflict_write_approval_b_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke conflict fixture write approval\"
}" >"${tmpdir}/conflict-write-approval-b-decision.json"

json_post "/claims" "{
  \"claim\":\"SmokeConflict frontend e2e framework must use Cypress for browser tests.\",
  \"scope\":\"${SCOPE}\",
  \"source_url\":\"file://smoke-conflict-cypress-${STAMP}.md\",
  \"source_type\":\"markdown\",
  \"authority\":\"team-convention\",
  \"created_by\":\"agent-alpha\",
  \"approval_id\":\"${conflict_write_approval_b_id}\",
  \"metadata\":{\"fixture\":\"conflict-b\",\"stamp\":\"${STAMP}\"}
}" >"${tmpdir}/conflict-claim-b.json"
conflict_claim_b_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).claim_id))' <"${tmpdir}/conflict-claim-b.json")"

json_post "/approvals" "{
  \"action\":\"challenge_claim\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"claim\",
  \"target_id\":\"${conflict_claim_a_id}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Record active contradiction between two fixture claims\",
  \"payload\":{\"conflicting_claim_id\":\"${conflict_claim_b_id}\",\"verdict\":\"conflict\"}
}" >"${tmpdir}/conflict-challenge-approval.json"
conflict_challenge_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/conflict-challenge-approval.json")"
json_post "/approvals/${conflict_challenge_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke conflict challenge approval\"
}" >"${tmpdir}/conflict-challenge-approval-decision.json"

json_post "/claims/${conflict_claim_a_id}/challenge" "{
  \"reason\":\"Fixture claims intentionally disagree about the frontend e2e framework\",
  \"verdict\":\"conflict\",
  \"conflicting_claim_id\":\"${conflict_claim_b_id}\",
  \"severity\":\"blocking\",
  \"created_by\":\"agent-alpha\",
  \"approval_id\":\"${conflict_challenge_approval_id}\",
  \"metadata\":{\"fixture\":\"active-conflict\",\"stamp\":\"${STAMP}\"}
}" >"${tmpdir}/conflict-challenge.json"
conflict_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).conflict_id))' <"${tmpdir}/conflict-challenge.json")"

json_post "/memory/compose" "{
  \"task\":\"SmokeConflict frontend e2e framework browser tests\",
  \"scope\":\"${SCOPE}\",
  \"hook\":\"before_task\",
  \"agent\":\"agent-alpha\",
  \"limit\":6,
  \"max_queries\":6,
  \"token_budget\":800,
  \"include_unverified\":true
}" >"${tmpdir}/conflict-memory.json"

json_get "/conflicts?scope=${SCOPE}&status=open&limit=10" >"${tmpdir}/conflicts-open.json"

json_post "/conflicts/${conflict_id}/resolve" "{
  \"status\":\"resolved\",
  \"resolved_by\":\"smoke\",
  \"resolution\":\"Playwright fixture claim wins for the smoke test\",
  \"metadata\":{\"fixture\":\"resolved-conflict\",\"stamp\":\"${STAMP}\"}
}" >"${tmpdir}/conflict-resolved.json"

json_post "/memory/compose" "{
  \"task\":\"SmokeConflict frontend e2e framework browser tests\",
  \"scope\":\"${SCOPE}\",
  \"hook\":\"before_task\",
  \"agent\":\"agent-alpha\",
  \"limit\":6,
  \"max_queries\":6,
  \"token_budget\":800,
  \"include_unverified\":true
}" >"${tmpdir}/conflict-memory-resolved.json"

json_post "/sources/configs" "{
  \"name\":\"${SOURCE_NAME}\",
  \"source_type\":\"local_repo\",
  \"scope\":\"${SCOPE}\",
  \"base_url\":\"file:///app/examples\",
  \"connector_kind\":\"generic\",
  \"status\":\"active\",
  \"authority\":\"team-convention\",
  \"authority_score\":0.7,
  \"approval_id\":\"${source_approval_id}\",
  \"config\":{\"root\":\"/app/examples\",\"include\":[\"**/*.md\"]},
  \"metadata\":{\"owner\":\"smoke\"}
}" >"${tmpdir}/source.json"
source_config_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).source_config_id))' <"${tmpdir}/source.json")"

json_post "/approvals" "{
  \"action\":\"source_authority_change\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"source_config\",
  \"target_id\":\"${source_config_id}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify source pause and resume audit lifecycle\",
  \"payload\":{\"source_config_id\":\"${source_config_id}\",\"status\":\"paused\"}
}" >"${tmpdir}/source-status-approval.json"
source_status_approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/source-status-approval.json")"
json_post "/approvals/${source_status_approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke source status approval\"
}" >"${tmpdir}/source-status-approval-decision.json"

json_post "/sources/configs" "{
  \"id\":\"${source_config_id}\",
  \"name\":\"${SOURCE_NAME}\",
  \"source_type\":\"local_repo\",
  \"scope\":\"${SCOPE}\",
  \"base_url\":\"file:///app/examples\",
  \"connector_kind\":\"generic\",
  \"status\":\"paused\",
  \"authority\":\"team-convention\",
  \"authority_score\":0.7,
  \"approval_id\":\"${source_status_approval_id}\",
  \"config\":{\"root\":\"/app/examples\",\"include\":[\"**/*.md\"]},
  \"metadata\":{\"owner\":\"smoke\",\"status_change\":\"pause\"}
}" >"${tmpdir}/source-paused.json"

json_post "/sources/configs" "{
  \"id\":\"${source_config_id}\",
  \"name\":\"${SOURCE_NAME}\",
  \"source_type\":\"local_repo\",
  \"scope\":\"${SCOPE}\",
  \"base_url\":\"file:///app/examples\",
  \"connector_kind\":\"generic\",
  \"status\":\"active\",
  \"authority\":\"team-convention\",
  \"authority_score\":0.7,
  \"approval_id\":\"${source_status_approval_id}\",
  \"config\":{\"root\":\"/app/examples\",\"include\":[\"**/*.md\"]},
  \"metadata\":{\"owner\":\"smoke\",\"status_change\":\"resume\"}
}" >"${tmpdir}/source-resumed.json"

json_get "/audit/events?scope=${SCOPE}&event_type=source_config.upserted&target_type=source_config&limit=20" >"${tmpdir}/source-audit-events.json"

json_post "/ingest/documents" "{
  \"source_type\":\"markdown\",
  \"source_url\":\"${SOURCE_URL}\",
  \"source_id\":\"abra-smoke-${STAMP}\",
  \"title\":\"Abra smoke ${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"Abra smoke verifies metrics ingestion recall policy MCP and job history.\",
  \"metadata\":{\"authority\":\"team-convention\",\"authority_score\":0.7}
}" >"${tmpdir}/ingest.json"

REFRESH_SOURCE_URL="file://abra-refresh-${STAMP}.md"
json_post "/ingest/documents" "{
  \"source_type\":\"markdown\",
  \"source_url\":\"${REFRESH_SOURCE_URL}\",
  \"source_id\":\"abra-refresh-${STAMP}\",
  \"title\":\"Abra refresh ${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"- RefreshLifecycle frontend cache must use React Query for server state.\\n- RefreshLifecycle obsolete cache must use localStorage for server state.\",
  \"metadata\":{\"authority\":\"team-convention\",\"authority_score\":0.7}
}" >"${tmpdir}/refresh-initial.json"

json_post "/ingest/documents" "{
  \"source_type\":\"markdown\",
  \"source_url\":\"${REFRESH_SOURCE_URL}\",
  \"source_id\":\"abra-refresh-${STAMP}\",
  \"title\":\"Abra refresh ${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"- RefreshLifecycle frontend cache must use React Query for server state.\",
  \"metadata\":{\"authority\":\"team-convention\",\"authority_score\":0.7}
}" >"${tmpdir}/refresh-updated.json"

json_post "/recall" "{
  \"query\":\"RefreshLifecycle obsolete localStorage server state\",
  \"scope\":\"${SCOPE}\",
  \"limit\":5,
  \"include_unverified\":true
}" >"${tmpdir}/refresh-obsolete-recall.json"

json_post "/recall" "{
  \"query\":\"RefreshLifecycle React Query server state\",
  \"scope\":\"${SCOPE}\",
  \"limit\":5,
  \"include_unverified\":true
}" >"${tmpdir}/refresh-current-recall.json"

GRAPH_WARNING_SOURCE_URL="file://abra-graph-warning-${STAMP}.md"
json_post "/ingest/documents" "{
  \"source_type\":\"markdown\",
  \"source_url\":\"${GRAPH_WARNING_SOURCE_URL}\",
  \"source_id\":\"abra-graph-warning-${STAMP}\",
  \"title\":\"Abra graph warning ${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"SmokeGraph Frontend App should use Playwright. SmokeGraph Frontend App should use Cypress.\",
  \"metadata\":{\"authority\":\"team-convention\",\"authority_score\":0.7}
}" >"${tmpdir}/graph-warning-ingest.json"

json_post "/memory/compose" "{
  \"task\":\"SmokeGraph Frontend App browser test runner decision\",
  \"scope\":\"${SCOPE}\",
  \"hook\":\"before_task\",
  \"agent\":\"agent-alpha\",
  \"limit\":6,
  \"max_queries\":6,
  \"token_budget\":800,
  \"include_unverified\":true
}" >"${tmpdir}/graph-warning-memory.json"

json_get "/conflicts?scope=${SCOPE}&status=open&limit=20" >"${tmpdir}/graph-warning-conflicts.json"
graph_warning_relation_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const conflicts=JSON.parse(d).conflicts||[];const c=conflicts.find(x=>x.primary_relation_id&&x.conflicting_relation_id&&x.detected_by==="auto-graph-detector"); if(!c){process.exit(2)} console.log(c.primary_relation_id)})' <"${tmpdir}/graph-warning-conflicts.json")"
json_get "/conflicts?scope=${SCOPE}&status=open&relation_id=${graph_warning_relation_id}&limit=20" >"${tmpdir}/graph-warning-conflicts-relation-filter.json"

json_post "/ingest/documents" "{
  \"source_type\":\"local_repo\",
  \"source_url\":\"file://src/pages/smoke-${STAMP}/index.tsx\",
  \"source_id\":\"abra-smoke-code-${STAMP}\",
  \"title\":\"src/pages/smoke-${STAMP}/index.tsx\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"import { Button } from 'example-ui-kit';\\nexport default function SmokePage() { return <Button />; }\\n\",
  \"metadata\":{\"authority\":\"code-structure\",\"authority_score\":0.72,\"git_path\":\"src/pages/smoke-${STAMP}/index.tsx\",\"content_kind\":\"code\"}
}" >"${tmpdir}/code-ingest.json"

REFRESH_CODE_SOURCE_URL="file://src/pages/refresh-${STAMP}/index.tsx"
json_post "/ingest/documents" "{
  \"source_type\":\"local_repo\",
  \"source_url\":\"${REFRESH_CODE_SOURCE_URL}\",
  \"source_id\":\"abra-refresh-code-${STAMP}\",
  \"title\":\"src/pages/refresh-${STAMP}/index.tsx\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"import LegacyWidget from 'legacy-ui';\\nexport default function RefreshPage() { return <LegacyWidget />; }\\n\",
  \"metadata\":{\"authority\":\"code-structure\",\"authority_score\":0.72,\"git_path\":\"src/pages/refresh-${STAMP}/index.tsx\",\"content_kind\":\"code\"}
}" >"${tmpdir}/code-refresh-initial.json"

json_post "/memory/summaries" "{
  \"query\":\"legacy-ui refresh page\",
  \"scope\":\"${SCOPE}\",
  \"limit\":10
}" >"${tmpdir}/code-refresh-legacy-summaries-initial.json"

json_get "/graph/relations?scope=${SCOPE}&limit=100" >"${tmpdir}/code-refresh-relations-initial.json"

json_post "/ingest/documents" "{
  \"source_type\":\"local_repo\",
  \"source_url\":\"${REFRESH_CODE_SOURCE_URL}\",
  \"source_id\":\"abra-refresh-code-${STAMP}\",
  \"title\":\"src/pages/refresh-${STAMP}/index.tsx\",
  \"scope\":\"${SCOPE}\",
  \"content\":\"import { Button } from 'example-ui-kit';\\nexport default function RefreshPage() { return <Button />; }\\n\",
  \"metadata\":{\"authority\":\"code-structure\",\"authority_score\":0.72,\"git_path\":\"src/pages/refresh-${STAMP}/index.tsx\",\"content_kind\":\"code\"}
}" >"${tmpdir}/code-refresh-updated.json"

json_post "/memory/summaries" "{
  \"query\":\"legacy-ui refresh page\",
  \"scope\":\"${SCOPE}\",
  \"limit\":10
}" >"${tmpdir}/code-refresh-legacy-summaries-updated.json"

json_post "/memory/summaries" "{
  \"query\":\"example-ui-kit refresh page\",
  \"scope\":\"${SCOPE}\",
  \"limit\":10
}" >"${tmpdir}/code-refresh-replacement-summaries-updated.json"

json_get "/graph/relations?scope=${SCOPE}&limit=100" >"${tmpdir}/code-refresh-relations-updated.json"

json_post_signed "/ingest/webhooks" "{
  \"connector_kind\":\"jira\",
  \"event_type\":\"issue.updated\",
  \"delivery_id\":\"smoke-${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"source_type\":\"jira\",
  \"source_url\":\"${WEBHOOK_SOURCE_URL}\",
  \"source_id\":\"ABRA-${STAMP}\",
  \"title\":\"ABRA-${STAMP} signed webhook\",
  \"content\":\"Signed webhook ingestion should create source-cited connector memory for Abra smoke.\",
  \"authority\":\"jira-project\",
  \"authority_score\":0.75,
  \"metadata\":{\"owner\":\"smoke\",\"connector\":\"jira\"}
}" >"${tmpdir}/webhook.json"

json_post_signed "/ingest/webhooks" "{
  \"connector_kind\":\"jira\",
  \"event_type\":\"issue.updated\",
  \"delivery_id\":\"smoke-${STAMP}\",
  \"scope\":\"${SCOPE}\",
  \"source_type\":\"jira\",
  \"source_url\":\"${WEBHOOK_SOURCE_URL}\",
  \"source_id\":\"ABRA-${STAMP}\",
  \"title\":\"ABRA-${STAMP} signed webhook\",
  \"content\":\"Signed webhook ingestion should create source-cited connector memory for Abra smoke.\",
  \"authority\":\"jira-project\",
  \"authority_score\":0.75,
  \"metadata\":{\"owner\":\"smoke\",\"connector\":\"jira\"}
}" >"${tmpdir}/webhook-duplicate.json"

json_post "/recall" "{
  \"query\":\"source-cited connector memory\",
  \"scope\":\"${SCOPE}\",
  \"limit\":5,
  \"include_unverified\":true
}" >"${tmpdir}/recall.json"

json_post "/policy/plan" "{
  \"hook\":\"before_code\",
  \"task\":\"ship Abra v1 ops\",
  \"scope\":\"${SCOPE}\",
  \"files\":[\"cmd/abra/main.go\"],
  \"changed_files\":[\"internal/server/server.go\"],
  \"language\":\"go\",
  \"agent\":\"agent-alpha\"
}" >"${tmpdir}/policy.json"

json_post "/memory/compose" "{
  \"task\":\"smoke page imports example-ui-kit\",
  \"scope\":\"${SCOPE}\",
  \"hook\":\"before_code\",
  \"files\":[\"src/pages/smoke-${STAMP}/index.tsx\"],
  \"changed_files\":[\"src/pages/smoke-${STAMP}/index.tsx\"],
  \"language\":\"typescriptreact\",
  \"agent\":\"agent-alpha\",
  \"limit\":5,
  \"max_queries\":6,
  \"token_budget\":900,
  \"include_unverified\":true
}" >"${tmpdir}/memory.json"

json_post "/memory/compose" "{
  \"task\":\"smoke page imports example-ui-kit\",
  \"scope\":\"${SCOPE}\",
  \"hook\":\"before_code\",
  \"files\":[\"src/pages/smoke-${STAMP}/index.tsx\"],
  \"changed_files\":[\"src/pages/smoke-${STAMP}/index.tsx\"],
  \"language\":\"typescriptreact\",
  \"agent\":\"agent-alpha\",
  \"limit\":5
}" >"${tmpdir}/profile-memory.json"

json_post "/memory/summaries" "{
  \"query\":\"smoke page example-ui-kit\",
  \"scope\":\"${SCOPE}\",
  \"limit\":10
}" >"${tmpdir}/summaries.json"

json_post "/memory/summaries/rebuild" "{
  \"scope\":\"${SCOPE}\",
  \"limit\":10,
  \"approval_id\":\"${rebuild_approval_id}\"
}" >"${tmpdir}/rebuild-summaries.json"

json_get "/memory/health?scope=${SCOPE}" >"${tmpdir}/memory-health.json"

json_post "/learning/proposals" "{
  \"scope\":\"${SCOPE}\",
  \"proposal_type\":\"summary_rebuild\",
  \"title\":\"Smoke learning proposal ${STAMP}\",
  \"rationale\":\"Verify learning proposals are reviewable without becoming trusted memory\",
  \"target_type\":\"scope\",
  \"target_id\":\"${SCOPE}\",
  \"confidence\":0.7,
  \"payload\":{\"source\":\"smoke\",\"stamp\":\"${STAMP}\"},
  \"created_by\":\"smoke\"
}" >"${tmpdir}/learning-proposal.json"
learning_proposal_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).learning_proposal.id))' <"${tmpdir}/learning-proposal.json")"
json_get "/learning/proposals?scope=${SCOPE}&status=pending&limit=10" >"${tmpdir}/learning-proposals.json"
json_post "/learning/proposals/${learning_proposal_id}/decide" "{
  \"status\":\"accepted\",
  \"reviewed_by\":\"smoke\",
  \"review_reason\":\"Smoke proposal lifecycle\"
}" >"${tmpdir}/learning-proposal-decision.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":100,
	  \"method\":\"initialize\",
	  \"params\":{}
	}" >"${tmpdir}/mcp-init.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":1,
	  \"method\":\"tools/call\",
  \"params\":{\"name\":\"policy_plan\",\"arguments\":{\"hook\":\"before_code\",\"task\":\"ship Abra v1 ops\",\"scope\":\"${SCOPE}\"}}
}" >"${tmpdir}/mcp.json"

json_post "/approvals" "{
  \"action\":\"agent_write\",
  \"scope\":\"${SCOPE}\",
  \"target_type\":\"smoke\",
  \"target_id\":\"${STAMP}\",
  \"requested_by\":\"smoke\",
  \"reason\":\"Verify approval workflow during smoke\",
  \"payload\":{\"source_url\":\"${SOURCE_URL}\"}
}" >"${tmpdir}/approval.json"
approval_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).approval.id))' <"${tmpdir}/approval.json")"
json_post "/approvals/${approval_id}/approve" "{
  \"decided_by\":\"smoke\",
  \"decision_reason\":\"Smoke approval\"
}" >"${tmpdir}/approval-decision.json"
json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":2,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"brain_summaries\",\"arguments\":{\"query\":\"smoke page example-ui-kit\",\"scope\":\"${SCOPE}\",\"limit\":5}}
}" >"${tmpdir}/mcp-summaries.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":3,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"working_memory_compose\",\"arguments\":{\"task\":\"ship Abra v1 ops\",\"scope\":\"${SCOPE}\",\"hook\":\"before_task\",\"agent\":\"agent-alpha\",\"limit\":5,\"max_queries\":5,\"token_budget\":900}}
}" >"${tmpdir}/mcp-memory.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":31,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"memory_health\",\"arguments\":{\"scope\":\"${SCOPE}\"}}
}" >"${tmpdir}/mcp-memory-health.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":4,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"request_approval\",\"arguments\":{\"action\":\"agent_write\",\"scope\":\"${SCOPE}\",\"reason\":\"Verify MCP approval workflow\",\"requested_by\":\"smoke\"}}
}" >"${tmpdir}/mcp-approval.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":5,
	  \"method\":\"tools/list\",
	  \"params\":{}
	}" >"${tmpdir}/mcp-tools.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":6,
	  \"method\":\"resources/list\",
	  \"params\":{}
	}" >"${tmpdir}/mcp-resources.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":7,
	  \"method\":\"resources/templates/list\",
	  \"params\":{}
	}" >"${tmpdir}/mcp-resource-templates.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":8,
	  \"method\":\"resources/read\",
	  \"params\":{\"uri\":\"abra://guide/agent-workflow\"}
	}" >"${tmpdir}/mcp-resource-guide.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":9,
	  \"method\":\"resources/read\",
	  \"params\":{\"uri\":\"abra://memory/health/${SCOPE}\"}
	}" >"${tmpdir}/mcp-resource-health.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":10,
	  \"method\":\"prompts/list\",
	  \"params\":{}
	}" >"${tmpdir}/mcp-prompts.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
	  \"id\":11,
	  \"method\":\"prompts/get\",
	  \"params\":{\"name\":\"abra-before-code\",\"arguments\":{\"task\":\"ship Abra v1 ops\",\"scope\":\"${SCOPE}\",\"agent\":\"agent-alpha\"}}
	}" >"${tmpdir}/mcp-prompt-before-code.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":12,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"ingest_document\",\"arguments\":{\"source_type\":\"markdown\",\"source_url\":\"file://mcp-ingest-single-${STAMP}.md\",\"source_id\":\"mcp-ingest-single-${STAMP}\",\"title\":\"MCP ingest single ${STAMP}\",\"scope\":\"${SCOPE}\",\"content\":\"- MCPIngest single document must preserve source-backed agent memory.\",\"authority\":\"team-convention\",\"authority_score\":0.7,\"metadata\":{\"fixture\":\"mcp-ingest-single\"}}}
}" >"${tmpdir}/mcp-ingest-document.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":13,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"ingest_documents\",\"arguments\":{\"scope\":\"${SCOPE}\",\"source_type\":\"markdown\",\"authority\":\"team-convention\",\"authority_score\":0.7,\"metadata\":{\"fixture\":\"mcp-ingest-batch\"},\"documents\":[{\"source_url\":\"file://mcp-ingest-batch-a-${STAMP}.md\",\"source_id\":\"mcp-ingest-batch-a-${STAMP}\",\"title\":\"MCP ingest batch A ${STAMP}\",\"content\":\"- MCPIngest batch document A must support connector batch memory.\"},{\"source_url\":\"file://mcp-ingest-batch-b-${STAMP}.md\",\"source_id\":\"mcp-ingest-batch-b-${STAMP}\",\"title\":\"MCP ingest batch B ${STAMP}\",\"content\":\"- MCPIngest batch document B must support connector batch memory.\"}]}}
}" >"${tmpdir}/mcp-ingest-documents.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":14,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"upsert_source_config\",\"arguments\":{\"id\":\"${source_config_id}\",\"name\":\"${SOURCE_NAME}\",\"source_type\":\"local_repo\",\"scope\":\"${SCOPE}\",\"base_url\":\"file:///app/examples\",\"connector_kind\":\"generic\",\"status\":\"active\",\"authority\":\"team-convention\",\"authority_score\":0.7,\"approval_id\":\"${source_approval_id}\",\"config\":{\"root\":\"/app/examples\",\"include\":[\"**/*.md\"]},\"metadata\":{\"owner\":\"smoke-mcp\"},\"created_by\":\"smoke-mcp\"}}
}" >"${tmpdir}/mcp-source-config.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":15,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_source_configs\",\"arguments\":{\"scope\":\"${SCOPE}\",\"limit\":10}}
}" >"${tmpdir}/mcp-source-configs.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":16,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"enqueue_ingestion_job\",\"arguments\":{\"source_config_id\":\"${source_config_id}\",\"trigger_type\":\"manual\",\"created_by\":\"smoke-mcp\",\"metadata\":{\"fixture\":\"mcp-enqueue\"}}}
}" >"${tmpdir}/mcp-ingestion-job.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":17,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_ingestion_jobs\",\"arguments\":{\"scope\":\"${SCOPE}\",\"source_config_id\":\"${source_config_id}\",\"limit\":20}}
}" >"${tmpdir}/mcp-ingestion-jobs.json"

json_get "/audit/events?scope=${SCOPE}&event_type=source_config.upserted&target_type=source_config&limit=50" >"${tmpdir}/mcp-source-config-audit.json"

	json_post "/mcp" "{
	  \"jsonrpc\":\"2.0\",
  \"id\":30,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"upsert_acl_policy\",\"arguments\":{\"scope\":\"${SCOPE}\",\"name\":\"mcp-allow-recall\",\"subject_type\":\"agent\",\"subject_id\":\"agent-alpha\",\"effect\":\"allow\",\"priority\":15,\"approval_id\":\"${mcp_acl_approval_id}\",\"rule\":{\"actions\":[\"recall\"],\"resource_types\":[\"claim\"],\"resource_ids\":[\"*\"]},\"metadata\":{\"owner\":\"smoke-mcp\"},\"created_by\":\"smoke-mcp\"}}
}" >"${tmpdir}/mcp-acl-policy.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":31,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_acl_policies\",\"arguments\":{\"scope\":\"${SCOPE}\",\"subject_type\":\"agent\",\"subject_id\":\"agent-alpha\",\"limit\":10}}
}" >"${tmpdir}/mcp-acl-policies.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":32,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"acl_decision\",\"arguments\":{\"scope\":\"${SCOPE}\",\"principal_type\":\"agent\",\"principal_id\":\"agent-alpha\",\"action\":\"recall\",\"resource_type\":\"claim\",\"resource_id\":\"smoke\",\"context\":{\"fixture\":\"mcp-acl-decision\"}}}
}" >"${tmpdir}/mcp-acl-decision.json"
json_get "/audit/events?scope=${SCOPE}&event_type=acl_policy.upserted&target_type=acl_policy&limit=50" >"${tmpdir}/mcp-acl-policy-audit.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":33,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"upsert_agent_policy\",\"arguments\":{\"scope\":\"${SCOPE}\",\"name\":\"mcp-require-agent-review\",\"subject_type\":\"agent\",\"subject_id\":\"agent-alpha\",\"effect\":\"require_review\",\"priority\":25,\"approval_id\":\"${mcp_agent_policy_approval_id}\",\"rule\":{\"actions\":[\"agent_write\"],\"target_types\":[\"memory_write\"],\"target_ids\":[\"${SCOPE}\"]},\"metadata\":{\"owner\":\"smoke-mcp\"},\"created_by\":\"smoke-mcp\"}}
}" >"${tmpdir}/mcp-agent-policy.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":34,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_agent_policies\",\"arguments\":{\"scope\":\"${SCOPE}\",\"limit\":10}}
}" >"${tmpdir}/mcp-agent-policies.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":35,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"agent_policy_decision\",\"arguments\":{\"scope\":\"${SCOPE}\",\"principal_type\":\"agent\",\"principal_id\":\"agent-alpha\",\"action\":\"agent_write\",\"target_type\":\"memory_write\",\"target_id\":\"${SCOPE}\",\"context\":{\"fixture\":\"mcp-agent-policy-decision\"}}}
}" >"${tmpdir}/mcp-agent-policy-decision.json"
json_get "/audit/events?scope=${SCOPE}&event_type=agent_policy.upserted&target_type=agent_policy&limit=50" >"${tmpdir}/mcp-agent-policy-audit.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":36,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_agent_profiles\",\"arguments\":{\"scope\":\"${SCOPE}\",\"status\":\"active\",\"limit\":10}}
}" >"${tmpdir}/mcp-agent-profiles.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":6,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"propose_learning\",\"arguments\":{\"scope\":\"${SCOPE}\",\"proposal_type\":\"graph\",\"title\":\"MCP learning proposal ${STAMP}\",\"rationale\":\"Verify MCP learning proposal workflow\",\"target_type\":\"scope\",\"target_id\":\"${SCOPE}\",\"created_by\":\"smoke\"}}
}" >"${tmpdir}/mcp-learning.json"
mcp_learning_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const r=JSON.parse(d); const p=JSON.parse(r.result.content[0].text); console.log(p.id);})' <"${tmpdir}/mcp-learning.json")"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":32,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"decide_learning_proposal\",\"arguments\":{\"proposal_id\":\"${mcp_learning_id}\",\"status\":\"accepted\",\"reviewed_by\":\"smoke-mcp\",\"review_reason\":\"Verify MCP learning decision workflow\"}}
}" >"${tmpdir}/mcp-learning-decision.json"

json_get "/audit/events?scope=${SCOPE}&event_type=learning.proposed&target_type=learning_proposal&limit=50" >"${tmpdir}/mcp-learning-proposed-audit.json"
json_get "/audit/events?scope=${SCOPE}&event_type=learning.decided&target_type=learning_proposal&limit=50" >"${tmpdir}/mcp-learning-decided-audit.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":7,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_conflicts\",\"arguments\":{\"scope\":\"${SCOPE}\",\"status\":\"resolved\",\"limit\":10}}
}" >"${tmpdir}/mcp-conflicts.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":8,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"resolve_conflict\",\"arguments\":{\"conflict_id\":\"${conflict_id}\",\"status\":\"suppressed\",\"resolved_by\":\"smoke-mcp\",\"resolution\":\"Suppressed through MCP smoke lifecycle\",\"metadata\":{\"fixture\":\"mcp-conflict-resolution\"}}}
}" >"${tmpdir}/mcp-conflict-resolve.json"

json_get "/ingestion/jobs?scope=${SCOPE}&limit=10" >"${tmpdir}/jobs.json"
json_get "/metrics" >"${tmpdir}/metrics-after.txt"

STAMP="$STAMP" SCOPE="$SCOPE" node - "${tmpdir}" <<'NODE'
const fs = require("fs");
const path = require("path");
const dir = process.argv[2];
function read(name) {
  return JSON.parse(fs.readFileSync(path.join(dir, name), "utf8"));
}
function mcpTextPayload(response) {
  if (!response.result || !Array.isArray(response.result.content) || !response.result.content[0] || typeof response.result.content[0].text !== "string") {
    throw new Error("mcp tool response did not include text content");
  }
  return JSON.parse(response.result.content[0].text);
}
const ready = read("ready.json");
const ingest = read("ingest.json");
const refreshInitial = read("refresh-initial.json");
const refreshUpdated = read("refresh-updated.json");
const refreshObsoleteRecall = read("refresh-obsolete-recall.json");
const refreshCurrentRecall = read("refresh-current-recall.json");
const graphWarningIngest = read("graph-warning-ingest.json");
const graphWarningMemory = read("graph-warning-memory.json");
const graphWarningConflicts = read("graph-warning-conflicts.json");
const graphWarningRelationFilterConflicts = read("graph-warning-conflicts-relation-filter.json");
const codeIngest = read("code-ingest.json");
const codeRefreshInitial = read("code-refresh-initial.json");
const codeRefreshUpdated = read("code-refresh-updated.json");
const codeRefreshLegacySummariesInitial = read("code-refresh-legacy-summaries-initial.json");
const codeRefreshLegacySummariesUpdated = read("code-refresh-legacy-summaries-updated.json");
const codeRefreshReplacementSummariesUpdated = read("code-refresh-replacement-summaries-updated.json");
const codeRefreshRelationsInitial = read("code-refresh-relations-initial.json");
const codeRefreshRelationsUpdated = read("code-refresh-relations-updated.json");
const webhook = read("webhook.json");
const webhookDuplicate = read("webhook-duplicate.json");
const recall = read("recall.json");
const policy = read("policy.json");
const memory = read("memory.json");
const profileMemory = read("profile-memory.json");
const memoryHealth = read("memory-health.json");
const summaries = read("summaries.json");
const rebuildSummaries = read("rebuild-summaries.json");
const learningProposal = read("learning-proposal.json");
const learningProposals = read("learning-proposals.json");
const learningProposalDecision = read("learning-proposal-decision.json");
const sourceApproval = read("source-approval.json");
const sourceApprovalDecision = read("source-approval-decision.json");
const sourceStatusApproval = read("source-status-approval.json");
const sourceStatusApprovalDecision = read("source-status-approval-decision.json");
const aclApproval = read("acl-approval.json");
const aclApprovalDecision = read("acl-approval-decision.json");
const mcpAclApproval = read("mcp-acl-approval.json");
const mcpAclApprovalDecision = read("mcp-acl-approval-decision.json");
const agentPolicyApproval = read("agent-policy-approval.json");
const agentPolicyApprovalDecision = read("agent-policy-approval-decision.json");
const mcpAgentPolicyApproval = read("mcp-agent-policy-approval.json");
const mcpAgentPolicyApprovalDecision = read("mcp-agent-policy-approval-decision.json");
const agentProfileApproval = read("agent-profile-approval.json");
const agentProfileApprovalDecision = read("agent-profile-approval-decision.json");
const rebuildApproval = read("rebuild-approval.json");
const rebuildApprovalDecision = read("rebuild-approval-decision.json");
const aclPolicy = read("acl-policy.json");
const aclPolicyAudit = read("acl-policy-audit.json");
const aclDecision = read("acl-decision.json");
const aclDeny = read("acl-deny.json");
const agentPolicy = read("agent-policy.json");
const agentPolicies = read("agent-policies.json");
const agentPolicyAudit = read("agent-policy-audit.json");
const agentPolicyDecision = read("agent-policy-decision.json");
const agentProfile = read("agent-profile.json");
const agentProfiles = read("agent-profiles.json");
const agentProfileAudit = read("agent-profile-audit.json");
const conflictClaimA = read("conflict-claim-a.json");
const conflictClaimB = read("conflict-claim-b.json");
const conflictChallenge = read("conflict-challenge.json");
const conflictMemory = read("conflict-memory.json");
const conflictsOpen = read("conflicts-open.json");
const conflictResolved = read("conflict-resolved.json");
const conflictMemoryResolved = read("conflict-memory-resolved.json");
const source = read("source.json");
const sourcePaused = read("source-paused.json");
const sourceResumed = read("source-resumed.json");
	const sourceAuditEvents = read("source-audit-events.json");
	const mcpInit = read("mcp-init.json");
	const mcp = read("mcp.json");
	const mcpSummaries = read("mcp-summaries.json");
	const mcpMemory = read("mcp-memory.json");
	const mcpMemoryHealth = read("mcp-memory-health.json");
	const approval = read("approval.json");
	const approvalDecision = read("approval-decision.json");
	const mcpApproval = read("mcp-approval.json");
	const mcpTools = read("mcp-tools.json");
	const mcpResources = read("mcp-resources.json");
	const mcpResourceTemplates = read("mcp-resource-templates.json");
	const mcpResourceGuide = read("mcp-resource-guide.json");
	const mcpResourceHealth = read("mcp-resource-health.json");
	const mcpPrompts = read("mcp-prompts.json");
	const mcpPromptBeforeCode = read("mcp-prompt-before-code.json");
const mcpIngestDocument = read("mcp-ingest-document.json");
const mcpIngestDocuments = read("mcp-ingest-documents.json");
const mcpSourceConfig = read("mcp-source-config.json");
const mcpSourceConfigs = read("mcp-source-configs.json");
const mcpIngestionJob = read("mcp-ingestion-job.json");
const mcpIngestionJobs = read("mcp-ingestion-jobs.json");
const mcpSourceConfigAudit = read("mcp-source-config-audit.json");
	const mcpAclPolicy = read("mcp-acl-policy.json");
const mcpAclPolicies = read("mcp-acl-policies.json");
const mcpAclDecision = read("mcp-acl-decision.json");
const mcpAclPolicyAudit = read("mcp-acl-policy-audit.json");
const mcpAgentPolicy = read("mcp-agent-policy.json");
const mcpAgentPolicies = read("mcp-agent-policies.json");
const mcpAgentPolicyDecision = read("mcp-agent-policy-decision.json");
const mcpAgentPolicyAudit = read("mcp-agent-policy-audit.json");
const mcpAgentProfiles = read("mcp-agent-profiles.json");
const mcpLearning = read("mcp-learning.json");
const mcpLearningDecision = read("mcp-learning-decision.json");
const mcpLearningProposedAudit = read("mcp-learning-proposed-audit.json");
const mcpLearningDecidedAudit = read("mcp-learning-decided-audit.json");
const mcpConflicts = read("mcp-conflicts.json");
const mcpConflictResolve = read("mcp-conflict-resolve.json");
const jobs = read("jobs.json");
const metricsAfter = fs.readFileSync(path.join(dir, "metrics-after.txt"), "utf8");
if (!ready.ok) throw new Error("readyz did not report ok");
if (typeof ready.tracing_enabled !== "boolean") throw new Error("readyz did not expose tracing_enabled");
if (!ingest.document_id || ingest.chunks < 1) throw new Error("ingest did not write a document chunk");
if (!refreshInitial.document_id || refreshInitial.claims < 2) {
  throw new Error("refresh fixture initial ingest did not write enough claims");
}
if (!refreshUpdated.document_id || refreshUpdated.deprecated_claims < 2) {
  throw new Error("refresh fixture update did not deprecate previous source claims");
}
if (Array.isArray(refreshObsoleteRecall.claims) && refreshObsoleteRecall.claims.some((claim) => String(claim.claim_text || "").includes("obsolete cache"))) {
  throw new Error("source refresh did not remove obsolete claim from trusted recall");
}
if (!Array.isArray(refreshCurrentRecall.claims) || !refreshCurrentRecall.claims.some((claim) => String(claim.claim_text || "").includes("React Query") && claim.status === "verified")) {
  throw new Error("source refresh did not reactivate the still-present claim");
}
if (!graphWarningIngest.document_id || graphWarningIngest.relations < 2) {
  throw new Error("graph warning fixture did not ingest competing graph relations");
}
if (!Array.isArray(graphWarningMemory.graph_warnings) || !graphWarningMemory.graph_warnings.some((warning) => warning.warning_type === "competing_graph_alternatives" && warning.severity === "high")) {
  throw new Error("working memory did not surface competing graph alternatives");
}
if (!graphWarningMemory.graph_warnings.some((warning) => Array.isArray(warning.relations) && warning.relations.some((relation) => relation.id))) {
  throw new Error("working memory graph warnings did not preserve relation ids");
}
if (!graphWarningMemory.stats || graphWarningMemory.stats.graph_warnings !== graphWarningMemory.graph_warnings.length) {
  throw new Error("working memory graph warning stats did not match graph warnings");
}
if (!graphWarningMemory.verification || !["partial", "unsafe"].includes(graphWarningMemory.verification.verdict) || !Array.isArray(graphWarningMemory.verification.graph_warnings) || graphWarningMemory.verification.graph_warnings.length < 1) {
  throw new Error("working memory verifier did not require graph warning review");
}
if (!Array.isArray(graphWarningMemory.conflicts) || !graphWarningMemory.conflicts.some((conflict) => conflict.primary_relation_id && conflict.conflicting_relation_id)) {
  throw new Error("working memory did not surface active graph relation conflicts");
}
if (!graphWarningMemory.agent_decision || graphWarningMemory.agent_decision.autonomous_allowed !== false || !["caution", "needs_review", "blocked"].includes(graphWarningMemory.agent_decision.decision) || !Array.isArray(graphWarningMemory.agent_decision.required_actions) || !graphWarningMemory.agent_decision.required_actions.includes("review_graph_warnings")) {
  throw new Error("working memory agent gate did not require graph warning review");
}
if (!graphWarningMemory.agent_decision.required_actions.includes("review_relation_conflicts") || !Array.isArray(graphWarningMemory.agent_decision.allowed_next_actions) || !graphWarningMemory.agent_decision.allowed_next_actions.includes("list_conflicts") || graphWarningMemory.agent_decision.allowed_next_actions.includes("request_approval")) {
  throw new Error("working memory agent gate did not route relation conflicts to conflict review");
}
if (!Array.isArray(graphWarningConflicts.conflicts) || !graphWarningConflicts.conflicts.some((conflict) => conflict.primary_relation_id && conflict.conflicting_relation_id && conflict.detected_by === "auto-graph-detector")) {
  throw new Error("graph warning fixture did not persist a relation conflict");
}
if (!Array.isArray(graphWarningRelationFilterConflicts.conflicts) || graphWarningRelationFilterConflicts.conflicts.length < 1 || !graphWarningRelationFilterConflicts.conflicts.every((conflict) => conflict.primary_relation_id || conflict.conflicting_relation_id)) {
  throw new Error("relation_id conflict filter did not return relation conflicts");
}
if (!codeIngest.document_id || codeIngest.entities < 1 || codeIngest.relations < 1) {
  throw new Error("code ingest did not write structural graph entities and relations");
}
if (!codeRefreshInitial.document_id || codeRefreshInitial.entities < 1 || codeRefreshInitial.relations < 1) {
  throw new Error("code refresh fixture initial ingest did not write graph intelligence");
}
if (!Array.isArray(codeRefreshLegacySummariesInitial.summaries) || !codeRefreshLegacySummariesInitial.summaries.some((summary) => summary.key === "legacy-ui")) {
  throw new Error("code refresh fixture did not create the initial legacy-ui package summary");
}
if (!Array.isArray(codeRefreshRelationsInitial.relations) || !codeRefreshRelationsInitial.relations.some((relation) => relation.to_entity === "legacy-ui" && relation.status === "active")) {
  throw new Error("code refresh fixture did not create the initial legacy-ui active relation");
}
if (!codeRefreshUpdated.document_id || codeRefreshUpdated.deprecated_relations < 1 || codeRefreshUpdated.deleted_summaries < 1) {
  throw new Error("code source refresh did not reconcile previous graph relations and summaries");
}
if (Array.isArray(codeRefreshLegacySummariesUpdated.summaries) && codeRefreshLegacySummariesUpdated.summaries.some((summary) => summary.key === "legacy-ui")) {
  throw new Error("code source refresh left the obsolete legacy-ui package summary active");
}
if (!Array.isArray(codeRefreshReplacementSummariesUpdated.summaries) || !codeRefreshReplacementSummariesUpdated.summaries.some((summary) => summary.key === "example-ui-kit")) {
  throw new Error("code source refresh did not create the replacement example-ui-kit package summary");
}
if (Array.isArray(codeRefreshRelationsUpdated.relations) && codeRefreshRelationsUpdated.relations.some((relation) => relation.to_entity === "legacy-ui" && relation.status === "active")) {
  throw new Error("code source refresh left the obsolete legacy-ui relation active");
}
if (!Array.isArray(codeRefreshRelationsUpdated.relations) || !codeRefreshRelationsUpdated.relations.some((relation) => relation.to_entity === "example-ui-kit" && relation.status === "active")) {
  throw new Error("code source refresh did not create the replacement example-ui-kit active relation");
}
if (webhook.accepted !== 1 || !Array.isArray(webhook.documents) || webhook.documents[0].source_url !== "https://jira.example.local/browse/ABRA-" + process.env.STAMP) {
  throw new Error("signed webhook did not ingest exactly one connector document");
}
if (!webhook.documents[0].ingestion_job_id || webhook.documents[0].job_status !== "succeeded") {
  throw new Error("signed webhook did not return a succeeded ingestion job");
}
if (webhookDuplicate.accepted !== 1 || !Array.isArray(webhookDuplicate.documents) || webhookDuplicate.documents[0].duplicate !== true || webhookDuplicate.documents[0].ingestion_job_id !== webhook.documents[0].ingestion_job_id) {
  throw new Error("signed webhook redelivery was not treated as an idempotent duplicate");
}
if (!Array.isArray(recall.supporting_documents) || recall.supporting_documents.length < 1) {
  throw new Error("recall did not return supporting documents");
}
if (recall.retrieval_mode !== "hybrid") {
  throw new Error("recall did not use hybrid retrieval");
}
if (!recall.claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score)))) {
  throw new Error("recall claims did not include text/vector score components");
}
if (!policy.required || !Array.isArray(policy.queries) || policy.queries.length < 1) {
  throw new Error("policy plan did not require recall queries");
}
if (!memory.intent || !memory.strategy || !memory.retrieval_plan || !memory.retrieval_plan.mode || !memory.retrieval_plan.budget || memory.retrieval_plan.budget.context_tokens < 300 || !Array.isArray(memory.retrieval_trace) || memory.retrieval_trace.length < 1 || !memory.memory_health || !memory.memory_health.status || !Array.isArray(memory.memory_health.signals) || memory.memory_health.signals.length < 1 || !memory.verification || !memory.verification.verdict || !Array.isArray(memory.agent_policy_decisions) || memory.agent_policy_decisions.length < 1 || !memory.agent_decision || !memory.agent_decision.decision || !Array.isArray(memory.agent_decision.allowed_next_actions) || !memory.context_window || !Array.isArray(memory.context_window.blocks) || memory.context_window.blocks.length < 1 || !memory.context_window.prompt || memory.context_window.estimated_tokens < 1 || memory.context_window.estimated_tokens > memory.context_window.max_tokens || !Array.isArray(memory.learning_suggestions) || memory.learning_suggestions.length < 1 || !Array.isArray(memory.summaries) || memory.summaries.length < 1 || !Array.isArray(memory.facts) || !Array.isArray(memory.supporting_documents) || !Array.isArray(memory.impact_map) || memory.impact_map.length < 1 || !Array.isArray(memory.validation_plan) || memory.validation_plan.length < 1 || !memory.stats || memory.stats.queries_run < 1 || memory.stats.graph_relations < 1 || memory.stats.graph_queries < 1 || memory.stats.health_signals !== memory.memory_health.signals.length || memory.stats.graph_warnings !== (Array.isArray(memory.graph_warnings) ? memory.graph_warnings.length : 0) || memory.stats.impact_items !== memory.impact_map.length || memory.stats.validation_steps !== memory.validation_plan.length || memory.stats.context_blocks !== memory.context_window.blocks.length || memory.stats.context_tokens !== memory.context_window.estimated_tokens || memory.stats.context_dropped_blocks !== (Array.isArray(memory.context_window.dropped_blocks) ? memory.context_window.dropped_blocks.length : 0) || memory.stats.retrieval_trace_items !== memory.retrieval_trace.length || memory.stats.retrieval_warnings !== (Array.isArray(memory.retrieval_warnings) ? memory.retrieval_warnings.length : 0) || memory.stats.total_duration_ms < 0 || memory.stats.parallel_queries < 1 || memory.stats.parallel_graph_queries < 1) {
  throw new Error("memory compose did not return an agent-ready packet");
}
if (!memory.verification.retrieval_quality || memory.verification.retrieval_quality.result_count < 1 || !memory.verification.checks.some((check) => check.name === "retrieval_quality")) {
  throw new Error("memory compose did not return retrieval quality verification");
}
if (!memory.context_window.blocks.some((item) => item.type === "task")) {
  throw new Error("memory context window did not preserve task gate context");
}
if (!profileMemory.agent_profile || profileMemory.agent_profile.profile_key !== "agent-alpha" || !profileMemory.context_window || profileMemory.context_window.max_tokens !== 900 || !profileMemory.plan || !Array.isArray(profileMemory.plan.queries) || profileMemory.plan.queries.length < 1 || profileMemory.plan.queries.length > 6) {
  throw new Error("agent profile preferences were not applied to working-memory compose");
}
if (!memory.context_window.prompt.includes("Memory health:")) {
  throw new Error("memory context window did not include memory health gate");
}
if (!memory.retrieval_trace.every((item) => ["ok", "degraded"].includes(item.status))) {
  throw new Error("memory compose did not mark every retrieval trace stage with a status");
}
if (!memory.retrieval_trace.some((item) => item.stage === "retrieval" && item.operation === "planned_summary_and_recall" && item.parallel === true)) {
  throw new Error("memory compose did not trace parallel planned recall");
}
if (!memory.retrieval_trace.some((item) => item.stage === "graph" && item.operation === "seed_graph_expansion" && item.parallel === true)) {
  throw new Error("memory compose did not trace parallel graph expansion");
}
if (!memory.impact_map.some((item) => item.kind === "file" && item.name === "src/pages/smoke-" + process.env.STAMP + "/index.tsx")) {
  throw new Error("memory compose did not include the touched file in the impact map");
}
if (!memory.validation_plan.some((item) => item.command === "npm test" && item.required === true)) {
  throw new Error("memory compose did not include the expected package validation plan");
}
const memoryAgentWritePolicy = memory.agent_policy_decisions.find((item) => item.action === "agent_write");
if (!memoryAgentWritePolicy || memoryAgentWritePolicy.decision !== "require_review") {
  throw new Error("memory compose did not surface stored agent-write review policy");
}
if (!["needs_review", "blocked"].includes(memory.agent_decision.decision) || memory.agent_decision.autonomous_allowed !== false) {
  throw new Error("memory compose did not apply stored agent policy to the agent decision");
}
if (!Array.isArray(memory.agent_decision.required_actions) || !memory.agent_decision.required_actions.includes("request_approval_for_agent_write")) {
  throw new Error("memory compose did not require approval for stored agent-write policy");
}
if (!memoryHealth || memoryHealth.scope !== process.env.SCOPE || typeof memoryHealth.score !== "number" || !memoryHealth.status || !Array.isArray(memoryHealth.reasons) || !Array.isArray(memoryHealth.signals) || memoryHealth.signals.length < 1 || !memoryHealth.documents || memoryHealth.documents.total < 1 || !memoryHealth.claims || memoryHealth.claims.verified < 1 || !memoryHealth.graph || memoryHealth.graph.active_relations < 1 || !memoryHealth.summaries || memoryHealth.summaries.total < 1 || !memoryHealth.sources || memoryHealth.sources.total < 1 || !memoryHealth.ingestion || memoryHealth.ingestion.total_jobs < 0 || !memoryHealth.conflicts || typeof memoryHealth.conflicts.open !== "number" || !memoryHealth.learning || typeof memoryHealth.learning.pending !== "number" || !memoryHealth.approvals || typeof memoryHealth.approvals.pending !== "number") {
  throw new Error("memory health did not return scoped aggregate quality signals");
}
for (const signal of memoryHealth.signals) {
  if (!signal.code || !signal.category || !signal.severity || !signal.message || !signal.action || typeof signal.count !== "number" || typeof signal.score_impact !== "number") {
    throw new Error("memory health returned an invalid structured signal");
  }
}
if (!Array.isArray(summaries.summaries) || summaries.summaries.length < 1) {
  throw new Error("memory summaries endpoint did not return hierarchical summaries");
}
const summaryLevels = new Set(summaries.summaries.map((summary) => summary.level));
for (const level of ["repo", "route", "component", "symbol", "package"]) {
  if (!summaryLevels.has(level)) {
    throw new Error(`memory summaries endpoint did not return ${level} code-intelligence summaries`);
  }
}
if (!rebuildSummaries.scope || rebuildSummaries.documents < 1 || rebuildSummaries.summaries < 1) {
  throw new Error("summary rebuild did not process existing documents");
}
if (!learningProposal.learning_proposal || learningProposal.learning_proposal.status !== "pending") {
  throw new Error("learning proposal was not created as pending");
}
if (!Array.isArray(learningProposals.learning_proposals) || !learningProposals.learning_proposals.some((item) => item.id === learningProposal.learning_proposal.id)) {
  throw new Error("learning proposal list did not include pending proposal");
}
if (!learningProposalDecision.learning_proposal || learningProposalDecision.learning_proposal.status !== "accepted") {
  throw new Error("learning proposal decision did not accept proposal");
}
if (!learningProposalDecision.apply_plan || learningProposalDecision.apply_plan.ready !== true || learningProposalDecision.apply_plan.action !== "rebuild_summaries") {
  throw new Error("learning proposal decision did not return a summary rebuild apply plan");
}
if (!mcp.result && !mcp.error) throw new Error("mcp response was not JSON-RPC shaped");
if (!mcpSummaries.result && !mcpSummaries.error) throw new Error("mcp summaries response was not JSON-RPC shaped");
if (!mcpMemory.result && !mcpMemory.error) throw new Error("mcp working memory response was not JSON-RPC shaped");
if (!sourceApproval.approval || sourceApproval.approval.status !== "pending") {
  throw new Error("source approval request was not pending");
}
if (!sourceApprovalDecision.approval || sourceApprovalDecision.approval.status !== "approved") {
  throw new Error("source approval decision did not approve request");
}
if (!sourceStatusApproval.approval || sourceStatusApproval.approval.status !== "pending") {
  throw new Error("source status approval request was not pending");
}
if (!sourceStatusApprovalDecision.approval || sourceStatusApprovalDecision.approval.status !== "approved") {
  throw new Error("source status approval decision did not approve request");
}
if (!source.source_config_id || sourcePaused.source_config_id !== source.source_config_id || sourceResumed.source_config_id !== source.source_config_id) {
  throw new Error("source pause/resume did not preserve source_config_id");
}
if (!Array.isArray(sourceAuditEvents.audit_events) || !sourceAuditEvents.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.status === "paused") || !sourceAuditEvents.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.status === "active")) {
  throw new Error("source config pause/resume audit events were not recorded");
}
if (!aclApproval.approval || aclApproval.approval.status !== "pending") {
  throw new Error("acl approval request was not pending");
}
if (!aclApprovalDecision.approval || aclApprovalDecision.approval.status !== "approved") {
  throw new Error("acl approval decision did not approve request");
}
if (!mcpAclApproval.approval || mcpAclApproval.approval.status !== "pending") {
  throw new Error("mcp acl approval request was not pending");
}
if (!mcpAclApprovalDecision.approval || mcpAclApprovalDecision.approval.status !== "approved") {
  throw new Error("mcp acl approval decision did not approve request");
}
if (!agentPolicyApproval.approval || agentPolicyApproval.approval.status !== "pending") {
  throw new Error("agent policy approval request was not pending");
}
if (!agentPolicyApprovalDecision.approval || agentPolicyApprovalDecision.approval.status !== "approved") {
  throw new Error("agent policy approval decision did not approve request");
}
if (!mcpAgentPolicyApproval.approval || mcpAgentPolicyApproval.approval.status !== "pending") {
  throw new Error("mcp agent policy approval request was not pending");
}
if (!mcpAgentPolicyApprovalDecision.approval || mcpAgentPolicyApprovalDecision.approval.status !== "approved") {
  throw new Error("mcp agent policy approval decision did not approve request");
}
if (!agentProfileApproval.approval || agentProfileApproval.approval.status !== "pending") {
  throw new Error("agent profile approval request was not pending");
}
if (!agentProfileApprovalDecision.approval || agentProfileApprovalDecision.approval.status !== "approved") {
  throw new Error("agent profile approval decision did not approve request");
}
if (!rebuildApproval.approval || rebuildApproval.approval.status !== "pending") {
  throw new Error("summary rebuild approval request was not pending");
}
if (!rebuildApprovalDecision.approval || rebuildApprovalDecision.approval.status !== "approved") {
  throw new Error("summary rebuild approval decision did not approve request");
}
if (!aclPolicy.acl_policy || aclPolicy.acl_policy.effect !== "allow") {
  throw new Error("acl policy was not written");
}
if (!Array.isArray(aclPolicyAudit.audit_events) || !aclPolicyAudit.audit_events.some((event) => event.target_id === aclPolicy.acl_policy.id && event.metadata && event.metadata.channel === "http" && event.metadata.name === "smoke-allow-recall")) {
  throw new Error("acl policy audit event was not recorded");
}
if (aclDecision.allowed !== true || aclDecision.decision !== "allow") {
  throw new Error("acl decision did not allow expected principal");
}
if (aclDeny.allowed !== false || aclDeny.decision !== "deny") {
  throw new Error("acl decision did not deny unknown principal");
}
if (!agentPolicy.agent_policy || agentPolicy.agent_policy.effect !== "require_review") {
  throw new Error("agent action policy was not written");
}
if (!Array.isArray(agentPolicies.agent_policies) || !agentPolicies.agent_policies.some((item) => item.id === agentPolicy.agent_policy.id && item.effect === "require_review")) {
  throw new Error("agent action policy list did not return the configured policy");
}
if (!Array.isArray(agentPolicyAudit.audit_events) || !agentPolicyAudit.audit_events.some((event) => event.target_id === agentPolicy.agent_policy.id && event.metadata && event.metadata.name === "smoke-require-agent-review")) {
  throw new Error("agent action policy audit event was not recorded");
}
if (agentPolicyDecision.allowed !== false || agentPolicyDecision.decision !== "require_review") {
  throw new Error("agent action policy did not require review for expected principal");
}
if (!agentProfile.agent_profile || agentProfile.agent_profile.profile_key !== "agent-alpha" || agentProfile.agent_profile.default_scope !== process.env.SCOPE || !Array.isArray(agentProfile.agent_profile.allowed_scopes) || !agentProfile.agent_profile.allowed_scopes.includes(process.env.SCOPE) || !agentProfile.agent_profile.denied_scopes.includes("team:design-system:secret")) {
  throw new Error("agent profile was not written with configurable scope preferences");
}
if (!Array.isArray(agentProfiles.agent_profiles) || !agentProfiles.agent_profiles.some((item) => item.id === agentProfile.agent_profile.id && item.principal_ref === "agent:agent-alpha")) {
  throw new Error("agent profile list did not return the configured profile");
}
if (!Array.isArray(agentProfileAudit.audit_events) || !agentProfileAudit.audit_events.some((event) => event.target_id === agentProfile.agent_profile.id && event.metadata && event.metadata.profile_key === "agent-alpha")) {
  throw new Error("agent profile audit event was not recorded");
}
if (!conflictClaimA.claim_id || !conflictClaimB.claim_id || conflictClaimA.claim_id === conflictClaimB.claim_id) {
  throw new Error("conflict fixture claims were not created correctly");
}
if (conflictClaimB.conflicts < 1) {
  throw new Error("automatic conflict detection did not flag the second contradictory claim");
}
if (!conflictChallenge.conflict_id || !conflictChallenge.feedback_id) {
  throw new Error("conflict challenge did not write feedback and conflict records");
}
if (!Array.isArray(conflictMemory.conflicts) || conflictMemory.conflicts.length < 1) {
  throw new Error("conflict memory packet did not surface active conflicts");
}
if (!conflictMemory.verification || conflictMemory.verification.verdict !== "unsafe" || !Array.isArray(conflictMemory.verification.active_conflicts) || conflictMemory.verification.active_conflicts.length < 1) {
  throw new Error("conflict memory packet did not mark active conflicts as unsafe");
}
if (!conflictMemory.agent_decision || conflictMemory.agent_decision.decision !== "blocked" || conflictMemory.agent_decision.autonomous_allowed !== false) {
  throw new Error("conflict memory packet did not block autonomous agent action");
}
if (!Array.isArray(conflictMemory.agent_decision.required_actions) || !conflictMemory.agent_decision.required_actions.includes("resolve_active_conflicts") || !Array.isArray(conflictMemory.agent_decision.allowed_next_actions) || !conflictMemory.agent_decision.allowed_next_actions.includes("list_conflicts") || conflictMemory.agent_decision.allowed_next_actions.includes("request_approval")) {
  throw new Error("conflict memory packet did not route active conflicts to conflict review");
}
if (!Array.isArray(conflictMemory.learning_suggestions) || !conflictMemory.learning_suggestions.some((item) => item.title === "Resolve active claim conflict")) {
  throw new Error("conflict memory packet did not propose a conflict-resolution learning action");
}
if (!Array.isArray(conflictsOpen.conflicts) || !conflictsOpen.conflicts.some((item) => item.id === conflictChallenge.conflict_id && item.status === "open")) {
  throw new Error("conflict list did not return the open conflict");
}
if (!conflictResolved.conflict || conflictResolved.conflict.id !== conflictChallenge.conflict_id || conflictResolved.conflict.status !== "resolved" || !conflictResolved.conflict.resolved_at) {
  throw new Error("conflict resolve endpoint did not close the conflict");
}
if (Array.isArray(conflictMemoryResolved.conflicts) && conflictMemoryResolved.conflicts.some((item) => item.id === conflictChallenge.conflict_id)) {
  throw new Error("resolved conflict was still surfaced as an active memory conflict");
}
if (conflictMemoryResolved.verification && Array.isArray(conflictMemoryResolved.verification.active_conflicts) && conflictMemoryResolved.verification.active_conflicts.some((item) => item.id === conflictChallenge.conflict_id)) {
  throw new Error("resolved conflict still appeared in active verifier conflicts");
}
if (!approval.approval || approval.approval.status !== "pending") throw new Error("approval request was not pending");
if (!approvalDecision.approval || approvalDecision.approval.status !== "approved") {
  throw new Error("approval decision did not approve request");
}
	if (!mcpApproval.result && !mcpApproval.error) throw new Error("mcp approval response was not JSON-RPC shaped");
	if (!mcpInit.result || !mcpInit.result.capabilities || !mcpInit.result.capabilities.tools || !mcpInit.result.capabilities.resources || !mcpInit.result.capabilities.prompts) {
	  throw new Error("mcp initialize did not declare tools, resources, and prompts capabilities");
	}
	if (!mcpTools.result || !Array.isArray(mcpTools.result.tools) || !mcpTools.result.tools.some((tool) => tool.name === "propose_learning")) {
	  throw new Error("mcp tools list did not expose learning tools");
	}
	if (!mcpResources.result || !Array.isArray(mcpResources.result.resources) || !mcpResources.result.resources.some((resource) => resource.uri === "abra://guide/agent-workflow")) {
	  throw new Error("mcp resources list did not expose the agent workflow guide");
	}
if (!mcpResourceTemplates.result || !Array.isArray(mcpResourceTemplates.result.resourceTemplates) || !mcpResourceTemplates.result.resourceTemplates.some((template) => template.uriTemplate === "abra://memory/health/{scope}") || !mcpResourceTemplates.result.resourceTemplates.some((template) => template.uriTemplate === "abra://working-memory?scope={scope}&task={task}")) {
	  throw new Error("mcp resource templates did not expose scoped health and working-memory resources");
	}
	if (!mcpResourceGuide.result || !Array.isArray(mcpResourceGuide.result.contents) || !String(mcpResourceGuide.result.contents[0] && mcpResourceGuide.result.contents[0].text).includes("working_memory_compose")) {
	  throw new Error("mcp guide resource did not return agent workflow text");
	}
	if (!mcpResourceHealth.result || !Array.isArray(mcpResourceHealth.result.contents)) {
	  throw new Error("mcp memory-health resource did not return contents");
	}
	const mcpResourceHealthPayload = JSON.parse(mcpResourceHealth.result.contents[0].text);
	if (mcpResourceHealthPayload.scope !== process.env.SCOPE || typeof mcpResourceHealthPayload.score !== "number" || !mcpResourceHealthPayload.status) {
	  throw new Error("mcp memory-health resource did not return scoped health JSON");
	}
	if (!mcpPrompts.result || !Array.isArray(mcpPrompts.result.prompts) || !mcpPrompts.result.prompts.some((prompt) => prompt.name === "abra-before-code")) {
	  throw new Error("mcp prompts list did not expose abra-before-code");
	}
	if (!mcpPromptBeforeCode.result || !Array.isArray(mcpPromptBeforeCode.result.messages) || !String(mcpPromptBeforeCode.result.messages[0] && mcpPromptBeforeCode.result.messages[0].content && mcpPromptBeforeCode.result.messages[0].content.text).includes("working_memory_compose")) {
	  throw new Error("mcp prompt get did not return a working-memory instruction");
	}
for (const tool of ["memory_health", "list_conflicts", "resolve_conflict"]) {
	  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
	    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["ingest_document", "ingest_documents"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_agent_profile", "list_agent_profiles"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_source_config", "list_source_configs", "enqueue_ingestion_job", "list_ingestion_jobs", "retry_ingestion_job", "cancel_ingestion_job"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_acl_policy", "list_acl_policies", "acl_decision"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_agent_policy", "list_agent_policies", "agent_policy_decision"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
if (!mcpAclPolicy.result || mcpAclPolicy.error) throw new Error("mcp acl policy upsert response failed");
const mcpAclPolicyPayload = mcpTextPayload(mcpAclPolicy);
if (!mcpAclPolicyPayload.id || mcpAclPolicyPayload.name !== "mcp-allow-recall" || mcpAclPolicyPayload.effect !== "allow") {
  throw new Error("mcp upsert_acl_policy did not return the configured policy");
}
if (!mcpAclPolicies.result || mcpAclPolicies.error) throw new Error("mcp acl policy list response failed");
if (!mcpTextPayload(mcpAclPolicies).some((item) => item.id === mcpAclPolicyPayload.id && item.effect === "allow")) {
  throw new Error("mcp list_acl_policies did not return the configured policy");
}
if (!mcpAclDecision.result || mcpAclDecision.error) throw new Error("mcp acl decision response failed");
const mcpAclDecisionPayload = mcpTextPayload(mcpAclDecision);
if (mcpAclDecisionPayload.allowed !== true || mcpAclDecisionPayload.decision !== "allow") {
  throw new Error("mcp acl_decision did not allow expected principal");
}
if (!Array.isArray(mcpAclPolicyAudit.audit_events) || !mcpAclPolicyAudit.audit_events.some((event) => event.target_id === mcpAclPolicyPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.name === "mcp-allow-recall")) {
  throw new Error("mcp acl policy audit event was not recorded");
}
if (!mcpAgentPolicy.result || mcpAgentPolicy.error) throw new Error("mcp agent policy upsert response failed");
const mcpAgentPolicyPayload = mcpTextPayload(mcpAgentPolicy);
if (!mcpAgentPolicyPayload.id || mcpAgentPolicyPayload.name !== "mcp-require-agent-review" || mcpAgentPolicyPayload.effect !== "require_review") {
  throw new Error("mcp upsert_agent_policy did not return the configured policy");
}
if (!mcpAgentPolicies.result || mcpAgentPolicies.error) throw new Error("mcp agent policy list response failed");
if (!mcpTextPayload(mcpAgentPolicies).some((item) => item.id === mcpAgentPolicyPayload.id && item.effect === "require_review")) {
  throw new Error("mcp list_agent_policies did not return the configured policy");
}
if (!mcpAgentPolicyDecision.result || mcpAgentPolicyDecision.error) throw new Error("mcp agent policy decision response failed");
const mcpAgentPolicyDecisionPayload = mcpTextPayload(mcpAgentPolicyDecision);
if (mcpAgentPolicyDecisionPayload.allowed !== false || mcpAgentPolicyDecisionPayload.decision !== "require_review") {
  throw new Error("mcp agent_policy_decision did not require review for expected principal");
}
if (!Array.isArray(mcpAgentPolicyAudit.audit_events) || !mcpAgentPolicyAudit.audit_events.some((event) => event.target_id === mcpAgentPolicyPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.name === "mcp-require-agent-review")) {
  throw new Error("mcp agent policy audit event was not recorded");
}
if (!mcpAgentProfiles.result || mcpAgentProfiles.error) throw new Error("mcp agent profile list response failed");
if (!mcpTextPayload(mcpAgentProfiles).some((item) => item.id === agentProfile.agent_profile.id && item.profile_key === "agent-alpha")) {
  throw new Error("mcp list_agent_profiles did not return the configured profile");
}
if (!mcpIngestDocument.result || mcpIngestDocument.error) throw new Error("mcp ingest_document response failed");
const mcpIngestDocumentPayload = mcpTextPayload(mcpIngestDocument);
if (!mcpIngestDocumentPayload.document_id || mcpIngestDocumentPayload.chunks < 1 || mcpIngestDocumentPayload.claims < 1) {
  throw new Error("mcp ingest_document did not write source-backed memory");
}
if (!mcpIngestDocuments.result || mcpIngestDocuments.error) throw new Error("mcp ingest_documents response failed");
const mcpIngestDocumentsPayload = mcpTextPayload(mcpIngestDocuments);
if (mcpIngestDocumentsPayload.accepted !== 2 || !Array.isArray(mcpIngestDocumentsPayload.documents) || !mcpIngestDocumentsPayload.documents.every((item) => item.document_id && item.chunks >= 1 && item.claims >= 1 && item.scope === process.env.SCOPE)) {
  throw new Error("mcp ingest_documents did not write the expected batch memory");
}
if (!mcpSourceConfig.result || mcpSourceConfig.error) throw new Error("mcp source config upsert response failed");
const mcpSourceConfigPayload = mcpTextPayload(mcpSourceConfig);
if (mcpSourceConfigPayload.source_config_id !== source.source_config_id || mcpSourceConfigPayload.status !== "upserted") {
  throw new Error("mcp upsert_source_config did not return the configured source");
}
if (!mcpSourceConfigs.result || mcpSourceConfigs.error) throw new Error("mcp source config list response failed");
if (!mcpTextPayload(mcpSourceConfigs).some((item) => item.id === source.source_config_id && item.metadata && item.metadata.owner === "smoke-mcp")) {
  throw new Error("mcp list_source_configs did not return the configured source");
}
if (!mcpIngestionJob.result || mcpIngestionJob.error) throw new Error("mcp ingestion job enqueue response failed");
const mcpIngestionJobPayload = mcpTextPayload(mcpIngestionJob);
if (!mcpIngestionJobPayload.id || mcpIngestionJobPayload.source_config_id !== source.source_config_id || mcpIngestionJobPayload.trigger_type !== "manual") {
  throw new Error("mcp enqueue_ingestion_job did not return the queued manual job");
}
if (!mcpIngestionJobs.result || mcpIngestionJobs.error) throw new Error("mcp ingestion job list response failed");
if (!mcpTextPayload(mcpIngestionJobs).some((item) => item.id === mcpIngestionJobPayload.id)) {
  throw new Error("mcp list_ingestion_jobs did not return the queued manual job");
}
if (!Array.isArray(mcpSourceConfigAudit.audit_events) || !mcpSourceConfigAudit.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.channel === "mcp" && event.metadata.source_type === "local_repo")) {
  throw new Error("mcp source config audit event was not recorded");
}
if (!mcpMemoryHealth.result || mcpMemoryHealth.error) throw new Error("mcp memory_health response failed");
const mcpHealth = mcpTextPayload(mcpMemoryHealth);
if (mcpHealth.scope !== process.env.SCOPE || typeof mcpHealth.score !== "number" || !mcpHealth.status || !Array.isArray(mcpHealth.signals) || mcpHealth.signals.length < 1) {
  throw new Error("mcp memory_health did not return a scoped health score");
}
if (!mcpLearning.result && !mcpLearning.error) throw new Error("mcp learning response was not JSON-RPC shaped");
const mcpLearningPayload = mcpTextPayload(mcpLearning);
if (!mcpLearningPayload.id || mcpLearningPayload.status !== "pending") {
  throw new Error("mcp learning proposal did not return a pending proposal");
}
if (!mcpLearningDecision.result || mcpLearningDecision.error) throw new Error("mcp learning decision response failed");
const mcpLearningDecisionPayload = mcpTextPayload(mcpLearningDecision);
if (!mcpLearningDecisionPayload.learning_proposal || mcpLearningDecisionPayload.learning_proposal.status !== "accepted" || !mcpLearningDecisionPayload.apply_plan || mcpLearningDecisionPayload.apply_plan.action !== "review_graph_update") {
  throw new Error("mcp learning decision did not return an accepted graph apply plan");
}
if (!Array.isArray(mcpLearningProposedAudit.audit_events) || !mcpLearningProposedAudit.audit_events.some((event) => event.target_id === mcpLearningPayload.id && event.metadata && event.metadata.channel === "mcp")) {
  throw new Error("mcp learning proposal audit event was not recorded");
}
if (!Array.isArray(mcpLearningDecidedAudit.audit_events) || !mcpLearningDecidedAudit.audit_events.some((event) => event.target_id === mcpLearningPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.status === "accepted")) {
  throw new Error("mcp learning decision audit event was not recorded");
}
if (!mcpConflicts.result || mcpConflicts.error) throw new Error("mcp conflicts response failed");
if (!mcpTextPayload(mcpConflicts).some((item) => item.id === conflictChallenge.conflict_id && item.status === "resolved")) {
  throw new Error("mcp list_conflicts did not return the resolved conflict");
}
if (!mcpConflictResolve.result || mcpConflictResolve.error) throw new Error("mcp conflict resolve response failed");
if (mcpTextPayload(mcpConflictResolve).status !== "suppressed") {
  throw new Error("mcp resolve_conflict did not suppress the conflict");
}
if (!Array.isArray(jobs.ingestion_jobs)) throw new Error("ingestion jobs response was not shaped correctly");
for (const metric of [
  "abra_smart_path_requests_total",
  "operation=\"recall\"",
  "operation=\"working_memory\"",
  "abra_smart_path_graph_relations_returned_sum",
  "abra_smart_path_autonomous_allowed_total",
  "abra_working_memory_retrieval_quality_total",
  "abra_working_memory_retrieval_top_rank_score_sum",
  "abra_working_memory_retrieval_last_result_count",
  "abra_working_memory_health_status_total",
  "abra_working_memory_health_signals_returned_sum",
  "abra_working_memory_health_signal_total",
  "health_status=\"",
  "abra_agent_policy_decisions_total",
  "operation=\"working_memory\",action=\"agent_write\",decision=\"require_review\"",
  "operation=\"decision_api\",action=\"agent_write\",decision=\"require_review\""
]) {
  if (!metricsAfter.includes(metric)) {
    throw new Error(`metrics did not expose ${metric}`);
  }
}
console.log("Abra smoke passed", JSON.stringify({
  document_id: ingest.document_id,
  webhook_accepted: webhook.accepted,
  acl_decision: aclDecision.decision,
  agent_policy_decision: agentPolicyDecision.decision,
  conflict_decision: conflictMemory.agent_decision.decision,
  health_status: memoryHealth.status,
  health_score: memoryHealth.score,
  supporting_documents: recall.supporting_documents.length,
  policy_queries: policy.queries.length,
  approval_status: approvalDecision.approval.status,
  ingestion_jobs: jobs.ingestion_jobs.length
}));
NODE
