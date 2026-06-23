#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${ABRA_BASE_URL:-http://127.0.0.1:18080}"
TOKEN="${ABRA_API_TOKEN:-dev-token}"
WEBHOOK_SECRET="${ABRA_WEBHOOK_SECRET:-dev-webhook-secret}"
STAMP="$(date -u +%Y%m%d%H%M%S)"
SCOPE="${ABRA_SMOKE_SCOPE:-team:smoke:${STAMP}}"
SOURCE_NAME="smoke-${STAMP}"
SOURCE_URL="file://abra-smoke-${STAMP}.md"
WEBHOOK_SOURCE_URL="https://jira.example.invalid/browse/ABRA-${STAMP}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

case "$BASE_URL" in
  http://127.0.0.1:*|http://localhost:*|http://[::1]:*)
    ;;
  *)
    if [[ -z "${ABRA_API_TOKEN:-}" && "${ABRA_ALLOW_DEV_TOKEN:-}" != "1" ]]; then
      echo "ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments." >&2
      exit 1
    fi
    ;;
esac

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

json_mcp_tool() {
  local tool="$1"
  local arguments="$2"
  TOOL="$tool" ARGS="$arguments" node -e 'const payload={jsonrpc:"2.0",id:1,method:"tools/call",params:{name:process.env.TOOL,arguments:JSON.parse(process.env.ARGS)}}; process.stdout.write(JSON.stringify(payload));' \
    | curl -fsS \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "content-type: application/json" \
      -d @- \
      "${BASE_URL}/mcp" \
    | node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const envelope=JSON.parse(d); if(envelope.error){console.error(JSON.stringify(envelope.error)); process.exit(1);} const result=envelope.result||{}; if(result.structuredContent!==undefined){process.stdout.write(JSON.stringify(result.structuredContent)); return;} const content=Array.isArray(result.content)?result.content:[]; const text=content.find((item)=>item&&item.type==="text"&&typeof item.text==="string"); process.stdout.write(text?text.text:JSON.stringify(result));})'
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

wait_ingestion_job() {
  local job_id="$1"
  local output="$2"
  local timeout="${ABRA_SMOKE_JOB_TIMEOUT:-120}"
  local deadline=$((SECONDS + timeout))
  while true; do
    json_get "/ingestion/jobs?scope=${SCOPE}&limit=100" >"${output}"
    local status
    status="$(JOB_ID="$job_id" node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const jobs=(JSON.parse(d).ingestion_jobs)||[]; const job=jobs.find((item)=>item.id===process.env.JOB_ID); if(!job){process.stdout.write("missing"); return;} process.stdout.write(String(job.status||""));})' <"${output}")"
    case "$status" in
      succeeded)
        return 0
        ;;
      failed|canceled)
        echo "ingestion job ${job_id} ended with status ${status}" >&2
        cat "${output}" >&2 || true
        exit 1
        ;;
    esac
    if (( SECONDS >= deadline )); then
      echo "ingestion job ${job_id} did not finish within ${timeout}s; last status: ${status}" >&2
      cat "${output}" >&2 || true
      exit 1
    fi
    sleep 1
  done
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

json_mcp_tool "working_memory_compose" "{
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

json_mcp_tool "working_memory_compose" "{
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

json_mcp_tool "working_memory_compose" "{
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

webhook_job_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const r=JSON.parse(d); console.log(r.documents[0].ingestion_job_id);})' <"${tmpdir}/webhook.json")"
wait_ingestion_job "$webhook_job_id" "${tmpdir}/webhook-job.json"

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

json_mcp_tool "working_memory_compose" "{
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

json_mcp_tool "working_memory_compose" "{
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

observation_text="Smoke observation review sentinel ${STAMP} must not become trusted recall automatically."
json_post "/observations" "{
  \"scope\":\"${SCOPE}\",
  \"observation_text\":\"${observation_text}\",
  \"observation_type\":\"episode\",
  \"status\":\"raw\",
  \"source_url\":\"file://abra-smoke-observation-${STAMP}.md\",
  \"source_type\":\"smoke\",
  \"created_by\":\"smoke\",
  \"metadata\":{\"fixture\":\"http-observation-review\",\"stamp\":\"${STAMP}\"}
}" >"${tmpdir}/observation.json"
observation_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).observation.id))' <"${tmpdir}/observation.json")"
json_get "/observations?scope=${SCOPE}&query=observation%20review%20sentinel&type=episode&status=raw&limit=10" >"${tmpdir}/observations-raw.json"
json_post "/learning/proposals" "{
  \"scope\":\"${SCOPE}\",
  \"proposal_type\":\"claim\",
  \"target_type\":\"observation\",
  \"target_id\":\"${observation_id}\",
  \"created_by\":\"smoke\"
}" >"${tmpdir}/observation-learning-proposal.json"
observation_learning_proposal_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>console.log(JSON.parse(d).learning_proposal.id))' <"${tmpdir}/observation-learning-proposal.json")"
json_get "/observations?scope=${SCOPE}&query=observation%20review%20sentinel&type=episode&status=proposed&limit=10" >"${tmpdir}/observations-proposed.json"
json_post "/learning/proposals/${observation_learning_proposal_id}/decide" "{
  \"status\":\"accepted\",
  \"reviewed_by\":\"smoke\",
  \"review_reason\":\"Smoke observation proposal lifecycle\"
}" >"${tmpdir}/observation-learning-proposal-decision.json"
json_post "/recall" "{
  \"scope\":\"${SCOPE}\",
  \"query\":\"Smoke observation review sentinel ${STAMP}\",
  \"limit\":10
}" >"${tmpdir}/observation-recall-after-proposal.json"
json_get "/audit/events?scope=${SCOPE}&event_type=observation.proposed&target_type=observation&limit=50" >"${tmpdir}/observation-proposed-audit.json"

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
  \"id\":37,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"capture_observation\",\"arguments\":{\"scope\":\"${SCOPE}\",\"observation_text\":\"MCP observation review sentinel ${STAMP} must remain outside trusted recall.\",\"observation_type\":\"episode\",\"status\":\"raw\",\"source_url\":\"file://abra-smoke-mcp-observation-${STAMP}.md\",\"source_type\":\"smoke\",\"created_by\":\"smoke-mcp\",\"metadata\":{\"fixture\":\"mcp-observation-review\",\"stamp\":\"${STAMP}\"}}}
}" >"${tmpdir}/mcp-observation.json"
mcp_observation_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const r=JSON.parse(d); const p=JSON.parse(r.result.content[0].text); console.log((p.observation||p).id);})' <"${tmpdir}/mcp-observation.json")"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":38,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"propose_learning\",\"arguments\":{\"scope\":\"${SCOPE}\",\"proposal_type\":\"claim\",\"target_type\":\"observation\",\"target_id\":\"${mcp_observation_id}\",\"created_by\":\"smoke-mcp\"}}
}" >"${tmpdir}/mcp-observation-learning.json"
mcp_observation_learning_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const r=JSON.parse(d); const p=JSON.parse(r.result.content[0].text); console.log((p.learning_proposal||p).id);})' <"${tmpdir}/mcp-observation-learning.json")"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":39,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"list_observations\",\"arguments\":{\"scope\":\"${SCOPE}\",\"query\":\"MCP observation review sentinel\",\"observation_type\":\"episode\",\"status\":\"proposed\",\"limit\":10}}
}" >"${tmpdir}/mcp-observations-proposed.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":40,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"decide_learning_proposal\",\"arguments\":{\"proposal_id\":\"${mcp_observation_learning_id}\",\"status\":\"accepted\",\"reviewed_by\":\"smoke-mcp\",\"review_reason\":\"Verify MCP observation proposal lifecycle\"}}
}" >"${tmpdir}/mcp-observation-learning-decision.json"
json_post "/recall" "{
  \"scope\":\"${SCOPE}\",
  \"query\":\"MCP observation review sentinel ${STAMP}\",
  \"limit\":10
}" >"${tmpdir}/mcp-observation-recall-after-proposal.json"
json_get "/audit/events?scope=${SCOPE}&event_type=observation.proposed&target_type=observation&limit=50" >"${tmpdir}/mcp-observation-proposed-audit.json"

json_post "/mcp" "{
  \"jsonrpc\":\"2.0\",
  \"id\":6,
  \"method\":\"tools/call\",
  \"params\":{\"name\":\"propose_learning\",\"arguments\":{\"scope\":\"${SCOPE}\",\"proposal_type\":\"graph\",\"title\":\"MCP learning proposal ${STAMP}\",\"rationale\":\"Verify MCP learning proposal workflow\",\"target_type\":\"scope\",\"target_id\":\"${SCOPE}\",\"created_by\":\"smoke\"}}
}" >"${tmpdir}/mcp-learning.json"
mcp_learning_id="$(node -e 'let d="";process.stdin.on("data",x=>d+=x);process.stdin.on("end",()=>{const r=JSON.parse(d); const p=JSON.parse(r.result.content[0].text); console.log((p.learning_proposal||p).id);})' <"${tmpdir}/mcp-learning.json")"

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

STAMP="$STAMP" SCOPE="$SCOPE" node scripts/lib/smoke-assertions.cjs "${tmpdir}"
