#!/usr/bin/env node

import { assertHybridRetrievalMode } from "./lib/eval-contracts.mjs";
import {
  approvalGateBlocked,
  assert,
  countHitAt,
  rankClaim,
  requireTokenForRemoteBaseURL,
  retrievedMemoryText,
  skipped,
  textOf
} from "./lib/tier23-helpers.mjs";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const startedAt = new Date().toISOString();
const stamp = (process.env.ABRA_TIER23_RUN_ID || startedAt).replace(/[^0-9A-Za-z]/g, "").slice(0, 24);
const scope = process.env.ABRA_TIER23_SCOPE || `team:eval-tier23-${stamp}`;
const isolatedScope = process.env.ABRA_TIER23_ISOLATED_SCOPE || `${scope}:isolated`;
const policyScope = process.env.ABRA_TIER23_POLICY_SCOPE || `${scope}:agent-policy`;
const sourceUrl = `file://abra-tier23-${stamp}.md`;
const isolatedSourceUrl = `file://abra-tier23-isolated-${stamp}.md`;
const allowNonLocal = process.env.ABRA_TIER23_ALLOW_NONLOCAL === "1";
const memoryMaxMs = Number(process.env.ABRA_TIER23_MEMORY_MAX_MS || "3500");

const checks = [];
const artifacts = {
  base_url: baseUrl,
  scope,
  isolated_scope: isolatedScope,
  policy_scope: policyScope,
  source_url: sourceUrl,
  isolated_source_url: isolatedSourceUrl
};

requireTokenForRemoteBaseURL(baseUrl);

let ready;
let primaryIngest;
let isolatedIngest;
let codeIngest;
let baselineClaimID;
let approvalEnforcementExpected = process.env.ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT === "1";
let baselineMemoryPacket;

async function request(path, { method = "GET", body, expectStatus = 200, text = false } = {}) {
  if (method === "POST" && path === "working_memory_compose") {
    return mcpTool("working_memory_compose", body || {});
  }
  if (method === "POST" && path === "brain_think") {
    return mcpTool("brain_think", body || {});
  }
  const response = await rawRequest(path, { method, body });
  const expected = Array.isArray(expectStatus) ? expectStatus : [expectStatus];
  if (!expected.includes(response.status)) {
    throw new Error(`${method} ${path} returned ${response.status}, expected ${expected.join("|")}: ${response.raw}`);
  }
  if (text) {
    return response.raw;
  }
  return response.json;
}

async function rawRequest(path, { method = "GET", body } = {}) {
  const response = await fetch(`${baseUrl}${path}`, {
    method,
    headers: {
      authorization: `Bearer ${token}`,
      ...(body === undefined ? {} : { "content-type": "application/json" })
    },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  const raw = await response.text();
  let json = {};
  if (raw.trim() !== "") {
    try {
      json = JSON.parse(raw);
    } catch {
      json = { raw };
    }
  }
  return { status: response.status, raw, json, headers: response.headers };
}

async function mcpTool(name, args) {
  const response = await request("/mcp", {
    method: "POST",
    body: {
      jsonrpc: "2.0",
      id: `tier23-${name}-${Date.now()}`,
      method: "tools/call",
      params: {
        name,
        arguments: args
      }
    }
  });
  assert(!response.error, `mcp ${name} returned error: ${JSON.stringify(response.error)}`);
  assert(
    response.result &&
      Array.isArray(response.result.content) &&
      response.result.content[0] &&
      typeof response.result.content[0].text === "string",
    `mcp ${name} did not return text content`
  );
  const payload = JSON.parse(response.result.content[0].text);
  if (payload && typeof payload === "object") {
    if (name === "capture_observation") {
      return payload.observation || payload;
    }
    if (name === "propose_learning") {
      return payload.learning_proposal || payload;
    }
  }
  return payload;
}

async function runCheck(name, fn) {
  const before = Date.now();
  try {
    const details = await fn();
    if (details && details.skipped) {
      checks.push({
        name,
        ok: true,
        skipped: true,
        duration_ms: Date.now() - before,
        reason: details.reason,
        details: details.details || {}
      });
      return;
    }
    checks.push({ name, ok: true, duration_ms: Date.now() - before, details: details || {} });
  } catch (error) {
    checks.push({
      name,
      ok: false,
      duration_ms: Date.now() - before,
      error: error instanceof Error ? error.message : String(error)
    });
  }
}

function assertApprovalGate(response, operationName) {
  if (approvalGateBlocked(response)) {
    return { enforced: true, status: response.status, response: response.json };
  }
  if (approvalEnforcementExpected) {
    throw new Error(
      `${operationName} was accepted without approval; expected an approval gate, got ${response.status}: ${response.raw}`
    );
  }
  return skipped(`${operationName} approval enforcement is not active on this deployment yet`, {
    status: response.status,
    note: "Set ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT=1 when risky-action approval gates are implemented."
  });
}

async function approvedRequest({ action, scope, targetType, targetId, requestedBy = "abra-tier23-eval", reason, payload = {}, metadata = {} }) {
  const created = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action,
      scope,
      target_type: targetType,
      target_id: targetId,
      requested_by: requestedBy,
      reason,
      payload,
      metadata: {
        eval_suite: "tier23",
        ...metadata
      }
    }
  });
  const approvalId = created?.approval?.id;
  assert(approvalId, "approval creation did not return approval.id");
  const approved = await request(`/approvals/${encodeURIComponent(approvalId)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: reason
    }
  });
  assert(approved.approval && approved.approval.status === "approved", "approval request was not approved");
  return approvalId;
}

async function memoryWriteApproval(scopeValue, reason, payload = {}, metadata = {}) {
  if (!approvalEnforcementExpected) {
    return "";
  }
  return approvedRequest({
    action: "agent_write",
    scope: scopeValue,
    targetType: "memory_write",
    targetId: scopeValue,
    reason,
    payload,
    metadata
  });
}

await runCheck("runtime_ready_with_local_embeddings", async () => {
  ready = await request("/readyz");
  assert(ready.ok === true, "readyz did not report ok=true");
  assert(
    ready.embedding_provider === "local" || allowNonLocal,
    `Tier 2/3 eval must run with EMBEDDING_PROVIDER=local for the default local neural recall profile, got ${ready.embedding_provider}`
  );
  approvalEnforcementExpected =
    approvalEnforcementExpected ||
    ready.approval_enforcement === true ||
    ready.approval_enforcement_required === true;
  artifacts.embedding_provider = ready.embedding_provider;
  artifacts.approval_enforcement_expected = approvalEnforcementExpected;
  return {
    embedding_provider: ready.embedding_provider,
    auth_required: ready.auth_required,
    approval_enforcement_expected: approvalEnforcementExpected
  };
});

await runCheck("seed_tier23_fixture_documents", async () => {
  const primaryApprovalId = await memoryWriteApproval(scope, "Approve Tier 2/3 primary fixture ingest for enforced approval eval.", {
    source_url: sourceUrl
  });
  const isolatedApprovalId = await memoryWriteApproval(isolatedScope, "Approve Tier 2/3 isolated fixture ingest for enforced approval eval.", {
    source_url: isolatedSourceUrl
  });
  primaryIngest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "markdown",
      source_url: sourceUrl,
      source_id: `abra-tier23-${stamp}`,
      title: "Abra Tier 2 and Tier 3 Eval Fixture",
      scope,
      content: [
        "- Provider Migration should compare External Embedding Pilot against the Local Embedding Baseline before production rollout.",
        "- Sensitive Report Export requires operator approval before broad-scope memory writes.",
        "- Agent Workflow must run policy planning before code changes and cite source-backed recall results.",
        "- Source Authority Changes require operator approval before the source config is trusted."
      ].join("\n"),
      metadata: {
        authority: "eval-fixture",
        authority_score: 0.82,
        eval_tier: "tier23"
      },
      approval_id: primaryApprovalId || undefined
    }
  });
  isolatedIngest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "markdown",
      source_url: isolatedSourceUrl,
      source_id: `abra-tier23-isolated-${stamp}`,
      title: "Abra Tier 2 and Tier 3 Isolated Fixture",
      scope: isolatedScope,
      content: "- Archive Service should retain release export evidence for audit review.",
      metadata: {
        authority: "eval-fixture",
        authority_score: 0.7,
        eval_tier: "tier23"
      },
      approval_id: isolatedApprovalId || undefined
    }
  });
  assert(primaryIngest.document_id, "primary ingest did not return document_id");
  assert(primaryIngest.claims >= 4, `primary fixture produced too few claims: ${primaryIngest.claims}`);
  assert(isolatedIngest.document_id, "isolated ingest did not return document_id");
  artifacts.document_id = primaryIngest.document_id;
  artifacts.isolated_document_id = isolatedIngest.document_id;
  return {
    document_id: primaryIngest.document_id,
    claims: primaryIngest.claims,
    entities: primaryIngest.entities,
    relations: primaryIngest.relations,
    isolated_document_id: isolatedIngest.document_id
  };
});

await runCheck("tier2_code_documents_do_not_become_claims", async () => {
  const approvalId = await memoryWriteApproval(scope, "Approve Tier 2/3 code fixture ingest for enforced approval eval.", {
    source_url: `file://abra-tier23-code-${stamp}.go`
  });
  codeIngest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "local_repo",
      source_url: `file://abra-tier23-code-${stamp}.go`,
      source_id: `abra-tier23-code-${stamp}`,
      title: "internal/example/policy.go",
      scope,
      content: [
        "package example",
        "",
        "func Example() {",
        "  // - Code comments must not become trusted memory.",
        "  return",
        "}",
        "",
        "- Frontend apps must use FakeRunner for browser tests."
      ].join("\n"),
      metadata: {
        authority: "eval-fixture",
        authority_score: 0.82,
        content_kind: "code",
        git_path: "internal/example/policy.go",
        eval_tier: "tier23"
      },
      approval_id: approvalId || undefined
    }
  });
  assert(codeIngest.document_id, "code ingest did not return document_id");
  assert(codeIngest.chunks >= 1, `code fixture produced no chunks: ${JSON.stringify(codeIngest)}`);
  assert(codeIngest.claims === 0, `code fixture produced trusted claims: ${JSON.stringify(codeIngest)}`);
  return {
    document_id: codeIngest.document_id,
    chunks: codeIngest.chunks,
    claims: codeIngest.claims,
    entities: codeIngest.entities,
    relations: codeIngest.relations
  };
});

await runCheck("tier2_recall_baseline_metrics", async () => {
  const cases = [
    {
      id: "provider-migration",
      query: "compare external embedding pilot local embedding baseline",
      expected_claim_contains: "Provider Migration",
      expected_source_url: sourceUrl,
      min_rank: 5
    },
    {
      id: "approval-required",
      query: "sensitive data export broad-scope memory writes approval",
      expected_claim_contains: "operator approval",
      expected_source_url: sourceUrl,
      min_rank: 5
    }
  ];
  const ranks = [];
  const caseResults = [];
  let verifiedWithoutCitation = 0;
  const before = Date.now();
  for (const item of cases) {
    const recall = await request("/recall", {
      method: "POST",
      body: {
        query: item.query,
        scope,
        limit: 5,
        include_unverified: false
      }
    });
    const claims = Array.isArray(recall.claims) ? recall.claims : [];
    assertHybridRetrievalMode(recall.retrieval_mode, `${item.id} recall`);
    assert(
      claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score))),
      `${item.id} recall claims missing text/vector score components`
    );
    const rank = rankClaim(claims, {
      contains: item.expected_claim_contains,
      source: item.expected_source_url
    });
    ranks.push(rank);
    if (rank === null || rank > item.min_rank) {
      throw new Error(`${item.id} expected ${item.expected_claim_contains} by rank ${item.min_rank}, got ${rank}`);
    }
    verifiedWithoutCitation += claims.filter((claim) => claim.status === "verified" && !claim.source_url).length;
    if (!baselineClaimID && rank !== null) {
      baselineClaimID = claims[rank - 1].id;
    }
    caseResults.push({ id: item.id, rank, claims: claims.length, retrieval_mode: recall.retrieval_mode });
  }
  assert(verifiedWithoutCitation === 0, `${verifiedWithoutCitation} verified claims were missing source_url`);
  artifacts.baseline_claim_id = baselineClaimID;
  return {
    cases: caseResults,
    hit_rate_at_1: countHitAt(ranks, 1) / cases.length,
    hit_rate_at_3: countHitAt(ranks, 3) / cases.length,
    hit_rate_at_5: countHitAt(ranks, 5) / cases.length,
    verified_without_citation: verifiedWithoutCitation,
    recall_latency_ms_total: Date.now() - before
  };
});

await runCheck("tier2_scope_leakage_guard", async () => {
  const recall = await request("/recall", {
    method: "POST",
    body: {
      query: "compare external embedding pilot local embedding baseline",
      scope: isolatedScope,
      limit: 5,
      include_unverified: true
    }
  });
  const forbidden = ["provider migration", "local embedding baseline", "sensitive data export"];
  for (const item of forbidden) {
    assert(!textOf(recall).includes(item), `isolated scope leaked ${item}`);
  }
  return {
    claims: Array.isArray(recall.claims) ? recall.claims.length : 0,
    supporting_documents: Array.isArray(recall.supporting_documents) ? recall.supporting_documents.length : 0
  };
});

await runCheck("tier3_policy_plan_before_and_after_work", async () => {
  const beforePlan = await request("/policy/plan", {
    method: "POST",
    body: {
      hook: "before_code",
      task: "change source authority and write company-wide memory from an agent workflow",
      scope,
      files: ["scripts/abra-tier23-eval.mjs"],
      changed_files: ["scripts/abra-tier23-eval.mjs"],
      language: "javascript",
      agent: "tier23-eval"
    }
  });
  const afterPlan = await request("/policy/plan", {
    method: "POST",
    body: {
      hook: "after_task",
      task: "change source authority and write company-wide memory from an agent workflow",
      scope,
      changed_files: ["scripts/abra-tier23-eval.mjs"],
      language: "javascript",
      agent: "tier23-eval"
    }
  });
  for (const plan of [beforePlan, afterPlan]) {
    assert(plan.required === true, `policy plan for ${plan.hook} did not require recall`);
    assert(Array.isArray(plan.queries) && plan.queries.length >= 1, `policy plan for ${plan.hook} returned no queries`);
    assert(plan.queries.every((query) => query.scope === scope), `policy plan for ${plan.hook} used an unexpected scope`);
  }
  return {
    before_code_queries: beforePlan.queries.length,
    after_task_queries: afterPlan.queries.length
  };
});

await runCheck("tier3_working_memory_agent_packet_baseline", async () => {
  const cases = [
    {
      id: "agent-workflow",
      task: "build agent workflow that changes source authority and writes scoped memory",
      expected: ["Agent Workflow", "Source Authority Changes"],
      min_facts: 2,
      min_summaries: 1
    },
    {
      id: "provider-migration",
      task: "plan provider migration from local embedding baseline to external embedding pilot",
      expected: ["Provider Migration", "Local Embedding Baseline"],
      min_facts: 1,
      min_summaries: 1
    }
  ];
  const results = [];
  const beforeAll = Date.now();
  let totalFacts = 0;
  let totalSummaries = 0;
  let totalEvidence = 0;
  let totalGraph = 0;
  for (const item of cases) {
    const before = Date.now();
    const packet = await request("working_memory_compose", {
      method: "POST",
      body: {
        task: item.task,
        scope,
        hook: "before_task",
        files: ["scripts/abra-tier23-eval.mjs"],
        changed_files: ["scripts/abra-tier23-eval.mjs"],
        language: "javascript",
        agent: "abra-tier23-eval",
        limit: 6,
        max_queries: 6,
        token_budget: 900,
        include_unverified: false
      }
    });
    const latency = Date.now() - before;
    const packetText = textOf(packet);
    for (const expected of item.expected) {
      assert(packetText.includes(expected.toLowerCase()), `${item.id} packet did not include ${expected}`);
    }
    assert(packet.plan && packet.plan.required === true, `${item.id} packet did not include a required policy plan`);
    assert(Array.isArray(packet.plan.queries) && packet.plan.queries.every((query) => query.scope === scope), `${item.id} packet included an out-of-scope query`);
    assert(packet.retrieval_plan && packet.retrieval_plan.mode, `${item.id} packet did not include retrieval planner output`);
    assert(Array.isArray(packet.retrieval_plan.stages) && packet.retrieval_plan.stages.length >= 4, `${item.id} retrieval plan had too few stages`);
    assert(packet.retrieval_plan.budget && packet.retrieval_plan.budget.context_tokens === 900, `${item.id} retrieval plan did not preserve context token budget`);
    assert(packet.retrieval_plan.coverage_targets && packet.retrieval_plan.coverage_targets.summaries >= 1, `${item.id} retrieval plan missing coverage targets`);
    assert(Array.isArray(packet.summaries) && packet.summaries.length >= item.min_summaries, `${item.id} packet had too few summaries`);
    assert(Array.isArray(packet.facts) && packet.facts.length >= item.min_facts, `${item.id} packet had too few facts`);
    assert(Array.isArray(packet.supporting_documents) && packet.supporting_documents.some((document) => document.source_url === sourceUrl), `${item.id} packet did not cite the fixture source`);
    assert(Array.isArray(packet.evidence) && packet.evidence.some((item) => item.source_url === sourceUrl), `${item.id} packet had no grouped evidence for the fixture source`);
    assert(packet.verification && (packet.verification.verdict === "strong" || packet.verification.verdict === "partial"), `${item.id} packet did not include usable verification`);
    assert(packet.memory_health && packet.memory_health.status && Array.isArray(packet.memory_health.signals) && packet.memory_health.signals.length >= 1, `${item.id} packet did not include memory health signals`);
    assert(packet.verification.claim_coverage === 1, `${item.id} verification did not report full claim coverage`);
    assert(Array.isArray(packet.verification.checks) && packet.verification.checks.length >= 4, `${item.id} verification checks were missing`);
    assert(packet.verification.retrieval_coverage && packet.verification.retrieval_coverage.complete === true, `${item.id} verification did not satisfy retrieval coverage`);
    assert(packet.verification.checks.some((check) => check.name === "retrieval_coverage"), `${item.id} verification missing retrieval coverage check`);
    assert(packet.verification.retrieval_quality && packet.verification.retrieval_quality.result_count >= 1, `${item.id} verification missing retrieval quality`);
    assert(packet.verification.checks.some((check) => check.name === "retrieval_quality"), `${item.id} verification missing retrieval quality check`);
    assert(Array.isArray(packet.agent_policy_decisions) && packet.agent_policy_decisions.length >= 4, `${item.id} packet did not include agent policy decisions`);
    assert(packet.agent_policy_decisions.some((item) => item.action === "agent_write"), `${item.id} packet did not include agent-write policy decision`);
    assert(packet.agent_decision && ["proceed", "caution"].includes(packet.agent_decision.decision), `${item.id} packet did not include a usable agent decision`);
    assert(Array.isArray(packet.agent_decision.allowed_next_actions) && packet.agent_decision.allowed_next_actions.length >= 1, `${item.id} agent decision did not include allowed next actions`);
    assert(Array.isArray(packet.impact_map) && packet.impact_map.length >= 1, `${item.id} packet missing impact map`);
    assert(Array.isArray(packet.validation_plan) && packet.validation_plan.length >= 1, `${item.id} packet missing validation plan`);
    assert(packet.context_window && Array.isArray(packet.context_window.blocks) && packet.context_window.blocks.length >= 1, `${item.id} packet missing budgeted context window`);
    assert(packet.context_window.prompt && packet.context_window.estimated_tokens > 0 && packet.context_window.estimated_tokens <= packet.context_window.max_tokens, `${item.id} context window was not prompt-ready or budgeted`);
    assert(packet.context_window.blocks.some((block) => block.type === "task"), `${item.id} context window did not preserve task gate`);
    assert(Array.isArray(packet.learning_suggestions), `${item.id} packet did not include learning suggestions`);
    assert(Array.isArray(packet.risks) && packet.risks.length >= 1, `${item.id} packet had no risk guidance`);
    assert(Array.isArray(packet.suggested_steps) && packet.suggested_steps.length >= 2, `${item.id} packet had too few suggested steps`);
    assert(packet.stats && packet.stats.queries_run >= 2, `${item.id} packet did not report enough queries`);
    assert(packet.stats.graph_queries >= 1, `${item.id} packet did not report graph query stats`);
    assert(packet.stats.impact_items === packet.impact_map.length, `${item.id} packet did not report impact map stats`);
    assert(packet.stats.validation_steps === packet.validation_plan.length, `${item.id} packet did not report validation plan stats`);
    assert(packet.stats.context_blocks === packet.context_window.blocks.length, `${item.id} packet did not report context block stats`);
    assert(packet.stats.context_tokens === packet.context_window.estimated_tokens, `${item.id} packet did not report context token stats`);
    assert(packet.stats.health_signals === packet.memory_health.signals.length, `${item.id} packet did not report health signal stats`);
    totalFacts += packet.facts.length;
    totalSummaries += packet.summaries.length;
    totalEvidence += packet.evidence.length;
    totalGraph += Array.isArray(packet.graph_context) ? packet.graph_context.length : 0;
    if (!baselineMemoryPacket) {
      baselineMemoryPacket = packet;
    }
    results.push({
      id: item.id,
      latency_ms: latency,
      summaries: packet.summaries.length,
      facts: packet.facts.length,
      supporting_documents: packet.supporting_documents.length,
      graph_relations: Array.isArray(packet.graph_context) ? packet.graph_context.length : 0,
      impact_items: Array.isArray(packet.impact_map) ? packet.impact_map.length : 0,
      validation_steps: Array.isArray(packet.validation_plan) ? packet.validation_plan.length : 0,
      context_tokens: packet.context_window?.estimated_tokens || 0,
      health_status: packet.memory_health.status,
      verification: packet.verification.verdict,
      agent_decision: packet.agent_decision.decision,
      learning_suggestions: packet.learning_suggestions.length,
      evidence: packet.evidence.length
    });
  }
  const totalLatency = Date.now() - beforeAll;
  assert(totalLatency <= memoryMaxMs * cases.length, `working memory total latency ${totalLatency}ms exceeded ${memoryMaxMs * cases.length}ms`);
  return {
    cases: results,
    avg_latency_ms: Math.round(totalLatency / cases.length),
    max_latency_ms_per_case: memoryMaxMs,
    total_summaries: totalSummaries,
    total_facts: totalFacts,
    total_graph_relations: totalGraph,
    total_evidence: totalEvidence
  };
});

await runCheck("tier3_agent_workflow_trace", async () => {
  const task = "implement an agent workflow that changes source authority and writes scoped memory safely";
  const files = ["internal/memory/composer.go", "internal/server/server.go"];
  const trace = [];
  const beforePlan = await request("/policy/plan", {
    method: "POST",
    body: {
      hook: "before_code",
      task,
      scope,
      files,
      changed_files: files,
      language: "go",
      agent: "workflow-trace-agent"
    }
  });
  assert(beforePlan.required === true, "agent workflow before_code plan did not require recall");
  assert(Array.isArray(beforePlan.queries) && beforePlan.queries.length >= 3, "agent workflow before_code plan returned too few queries");
  assert(beforePlan.queries.every((query) => query.scope === scope), "agent workflow before_code plan used an unexpected scope");
  trace.push({
    step: "policy_plan_before_code",
    status: "ok",
    queries: beforePlan.queries.length
  });

  let recalledClaims = 0;
  let recalledDocuments = 0;
  for (const planned of beforePlan.queries.slice(0, 3)) {
    const recall = await request("/recall", {
      method: "POST",
      body: {
        query: planned.query,
        scope: planned.scope,
        limit: planned.limit,
        include_unverified: planned.include_unverified
      }
    });
    assertHybridRetrievalMode(recall.retrieval_mode, "agent workflow recall");
    recalledClaims += Array.isArray(recall.claims) ? recall.claims.length : 0;
    recalledDocuments += Array.isArray(recall.supporting_documents) ? recall.supporting_documents.length : 0;
  }
  assert(recalledClaims + recalledDocuments > 0, "agent workflow planned recall returned no evidence");
  trace.push({
    step: "execute_planned_recall",
    status: "ok",
    claims: recalledClaims,
    supporting_documents: recalledDocuments
  });

  const packet = await request("working_memory_compose", {
    method: "POST",
    body: {
      task,
      scope,
      hook: "before_code",
      files,
      changed_files: files,
      language: "go",
      agent: "workflow-trace-agent",
      limit: 8,
      max_queries: 6,
      token_budget: 1000,
      include_unverified: false
    }
  });
  assert(packet.plan && packet.plan.required === true, "agent workflow packet did not preserve policy plan");
  assert(Array.isArray(packet.retrieval_trace) && packet.retrieval_trace.length >= 6, "agent workflow packet did not include retrieval trace");
  assert(packet.retrieval_trace.some((item) => item.stage === "health" && item.operation === "memory_health_lookup" && item.cache_status), "agent workflow packet did not trace health cache status");
  assert(packet.memory_health && ["healthy", "needs_review"].includes(packet.memory_health.status), `agent workflow memory health not usable: ${packet.memory_health && packet.memory_health.status}`);
  assert(packet.verification && ["strong", "partial"].includes(packet.verification.verdict), `agent workflow verification not usable: ${packet.verification && packet.verification.verdict}`);
  assert(packet.verification.retrieval_coverage && packet.verification.retrieval_coverage.complete === true, "agent workflow verification did not satisfy retrieval coverage");
  assert(packet.agent_decision && ["proceed", "caution", "needs_review"].includes(packet.agent_decision.decision), `agent workflow decision not usable: ${packet.agent_decision && packet.agent_decision.decision}`);
  assert(packet.context_window && packet.context_window.prompt && packet.context_window.estimated_tokens <= packet.context_window.max_tokens, "agent workflow context window was not prompt-ready");
  assert(Array.isArray(packet.validation_plan) && packet.validation_plan.length >= 1, "agent workflow packet returned no validation plan");
  trace.push({
    step: "working_memory_compose",
    status: "ok",
    decision: packet.agent_decision.decision,
    autonomous_allowed: packet.agent_decision.autonomous_allowed === true,
    verification: packet.verification.verdict,
    health_status: packet.memory_health.status,
    context_tokens: packet.context_window.estimated_tokens,
    validation_steps: packet.validation_plan.length
  });

  const afterPlan = await request("/policy/plan", {
    method: "POST",
    body: {
      hook: "after_task",
      task,
      scope,
      changed_files: files,
      language: "go",
      agent: "workflow-trace-agent"
    }
  });
  assert(afterPlan.required === true, "agent workflow after_task plan did not require recall");
  assert(Array.isArray(afterPlan.queries) && afterPlan.queries.length >= 2, "agent workflow after_task plan returned too few queries");
  assert(afterPlan.queries.every((query) => query.scope === scope), "agent workflow after_task plan used an unexpected scope");
  trace.push({
    step: "policy_plan_after_task",
    status: "ok",
    queries: afterPlan.queries.length
  });

  const expectedSteps = ["policy_plan_before_code", "execute_planned_recall", "working_memory_compose", "policy_plan_after_task"];
  assert(trace.map((item) => item.step).join(" > ") === expectedSteps.join(" > "), "agent workflow trace order changed");
  return {
    trace,
    recalled_claims: recalledClaims,
    recalled_documents: recalledDocuments,
    agent_decision: packet.agent_decision.decision,
    autonomous_allowed: packet.agent_decision.autonomous_allowed === true
  };
});

await runCheck("tier3_working_memory_persist_learning_opt_in", async () => {
  const approvalId = approvalEnforcementExpected
    ? await approvedRequest({
        action: "agent_write",
        scope,
        targetType: "memory_write",
        targetId: scope,
        requestedBy: "tier23-eval",
        reason: "Approve Tier 2/3 unverified learning fixture write before testing proposal deduplication.",
        payload: {
          claim: `Tier23 Unsourced Memory ${stamp} should use Review Queue for learning checks.`
        },
        metadata: {
          purpose: "learning_proposal_fixture"
        }
      })
    : "";
  const remembered = await request("/claims", {
    method: "POST",
    body: {
      claim: `Tier23 Unsourced Memory ${stamp} should use Review Queue for learning checks.`,
      scope,
      created_by: "tier23-eval",
      approval_id: approvalId
    }
  });
  const claimId = remembered.claim_id;
  assert(claimId, "unverified learning fixture claim did not return claim_id");

  const composeBody = {
    task: `review tier23 unsourced memory ${stamp}`,
    scope,
    hook: "before_task",
    agent: "abra-tier23-eval",
    limit: 6,
    max_queries: 6,
    token_budget: 900,
    include_unverified: true,
    persist_learning: true
  };
  const packet = await request("working_memory_compose", {
    method: "POST",
    body: composeBody
  });
  assert(
    Array.isArray(packet.facts) && packet.facts.some((claim) => claim.id === claimId),
    "working memory did not retrieve the unverified fixture claim"
  );
  const proposalSuggestion = (packet.learning_suggestions || []).find((suggestion) => {
    return suggestion.title === "Promote or reject unverified memory" && suggestion.persisted === true && suggestion.proposal_id;
  });
  assert(proposalSuggestion, "working memory did not persist the unverified-memory learning suggestion when persist_learning=true");

  const repeated = await request("working_memory_compose", {
    method: "POST",
    body: composeBody
  });
  const repeatedSuggestion = (repeated.learning_suggestions || []).find((suggestion) => {
    return suggestion.title === "Promote or reject unverified memory";
  });
  assert(repeatedSuggestion?.proposal_id === proposalSuggestion.proposal_id, "repeated compose did not reuse the pending learning proposal");
  assert(repeatedSuggestion.persisted_new === false, "repeated compose should not create a duplicate learning proposal");

  const concurrent = await Promise.all([
    request("working_memory_compose", { method: "POST", body: composeBody }),
    request("working_memory_compose", { method: "POST", body: composeBody }),
    request("working_memory_compose", { method: "POST", body: composeBody })
  ]);
  const concurrentIds = new Set(
    concurrent.map((concurrentPacket) => {
      return (concurrentPacket.learning_suggestions || []).find((suggestion) => {
        return suggestion.title === "Promote or reject unverified memory";
      })?.proposal_id;
    })
  );
  assert(
    concurrentIds.size === 1 && concurrentIds.has(proposalSuggestion.proposal_id),
    "concurrent compose did not reuse the same pending learning proposal"
  );

  const listed = await request(`/learning/proposals?scope=${encodeURIComponent(scope)}&status=pending&limit=25`);
  const matchingPending = (listed.learning_proposals || []).filter((proposal) => {
    return proposal.title === "Promote or reject unverified memory" && proposal.proposal_type === "claim";
  });
  assert(
    Array.isArray(listed.learning_proposals) &&
      listed.learning_proposals.some((proposal) => proposal.id === proposalSuggestion.proposal_id),
    "pending learning proposal list did not include the persisted proposal"
  );
  assert(matchingPending.length === 1, `expected one matching pending learning proposal, got ${matchingPending.length}`);
  const decided = await request(`/learning/proposals/${encodeURIComponent(proposalSuggestion.proposal_id)}/decide`, {
    method: "POST",
    body: {
      status: "accepted",
      reviewed_by: "abra-tier23-eval",
      review_reason: "Tier 3 validates accepted learning proposals return an apply plan.",
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  assert(decided.learning_proposal && decided.learning_proposal.status === "accepted", "learning proposal was not accepted");
  assert(decided.apply_plan && decided.apply_plan.ready === true, "accepted learning proposal did not return a ready apply plan");
  assert(decided.apply_plan.proposal_type === "claim" && decided.apply_plan.action === "review_claim_promotion", `unexpected learning apply plan: ${JSON.stringify(decided.apply_plan)}`);
  await request(`/learning/proposals/${encodeURIComponent(proposalSuggestion.proposal_id)}/decide`, {
    method: "POST",
    expectStatus: 400,
    body: {
      status: "applied",
      reviewed_by: "abra-tier23-eval",
      review_reason: "Accepted proposals should not be decided twice by this lifecycle check."
    }
  });
  return {
    claim_id: claimId,
    approval_id: approvalId || undefined,
    proposal_id: proposalSuggestion.proposal_id,
    apply_action: decided.apply_plan.action,
    persisted_new: proposalSuggestion.persisted_new,
    repeated_persisted_new: repeatedSuggestion.persisted_new,
    concurrent_reused: true,
    pending_proposals: listed.learning_proposals.length
  };
});

await runCheck("tier3_observation_learning_proposal_requires_review_and_dedupes", async () => {
  const observationText = `ABRA Tier 3 observation sentinel ${stamp} must stay outside trusted recall until explicit promotion.`;
  const approvalId = await memoryWriteApproval(scope, "Approve Tier 2/3 raw observation fixture capture for enforced approval eval.", {
    observation_text: observationText
  });
  const captured = await request("/observations", {
    method: "POST",
    body: {
      scope,
      observation_text: observationText,
      observation_type: "episode",
      status: "raw",
      source_url: `file://abra-tier23-observation-${stamp}.md`,
      source_type: "eval",
      created_by: "abra-tier23-eval",
      approval_id: approvalId || undefined,
      metadata: {
        eval_suite: "tier23",
        fixture: "observation_learning_proposal"
      }
    }
  });
  const observation = captured.observation;
  assert(observation && observation.id && observation.status === "raw", "observation capture did not return a raw observation");

  await request("/learning/proposals", {
    method: "POST",
    expectStatus: 400,
    body: {
      scope: isolatedScope,
      proposal_type: "claim",
      target_type: "observation",
      target_id: observation.id,
      created_by: "abra-tier23-eval"
    }
  });

  const proposalBody = {
    scope,
    proposal_type: "claim",
    target_type: "observation",
    target_id: observation.id,
    created_by: "abra-tier23-eval"
  };
  const firstProposal = await request("/learning/proposals", {
    method: "POST",
    expectStatus: [200, 202],
    body: proposalBody
  });
  assert(firstProposal.learning_proposal && firstProposal.learning_proposal.id, "observation proposal did not return a learning proposal");
  assert(firstProposal.created === true, "first observation proposal was not reported as newly created");
  assert(firstProposal.learning_proposal.status === "pending", "observation proposal was not pending");
  assert(firstProposal.learning_proposal.target_type === "observation", "observation proposal target_type was not observation");
  assert(firstProposal.learning_proposal.target_id === observation.id, "observation proposal target_id did not match observation");
  assert(
    firstProposal.learning_proposal.payload &&
      firstProposal.learning_proposal.payload.observation_id === observation.id &&
      firstProposal.learning_proposal.payload.observation_text === observationText &&
      firstProposal.learning_proposal.payload.promotion_flow === "observation_to_claim",
    "observation proposal payload did not preserve promotion context"
  );

  const secondProposal = await request("/learning/proposals", {
    method: "POST",
    expectStatus: [200, 202],
    body: proposalBody
  });
  assert(secondProposal.learning_proposal && secondProposal.learning_proposal.id === firstProposal.learning_proposal.id, "duplicate observation proposal did not reuse the pending proposal");
  assert(secondProposal.created === false, "duplicate observation proposal was reported as newly created");

  const proposedObservations = await request(
    `/observations?scope=${encodeURIComponent(scope)}&query=${encodeURIComponent("Tier 3 observation sentinel")}&type=episode&status=proposed&limit=10`
  );
  assert(
    Array.isArray(proposedObservations.observations) &&
      proposedObservations.observations.some((item) => item.id === observation.id && item.status === "proposed"),
    "observation was not marked proposed after learning proposal creation"
  );

  const decided = await request(`/learning/proposals/${encodeURIComponent(firstProposal.learning_proposal.id)}/decide`, {
    method: "POST",
    body: {
      status: "accepted",
      reviewed_by: "abra-tier23-eval",
      review_reason: "Tier 3 validates observation proposal review handoff.",
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  assert(decided.learning_proposal && decided.learning_proposal.status === "accepted", "observation proposal was not accepted");
  assert(
    decided.apply_plan &&
      decided.apply_plan.ready === true &&
      decided.apply_plan.proposal_type === "claim" &&
      decided.apply_plan.action === "review_claim_promotion" &&
      decided.apply_plan.endpoint === "/claims" &&
      decided.apply_plan.target_type === "memory_write" &&
      decided.apply_plan.target_id === scope,
    `unexpected observation proposal apply plan: ${JSON.stringify(decided.apply_plan)}`
  );
  assert(
    decided.apply_plan.requires_approval === approvalEnforcementExpected,
    `observation proposal approval requirement = ${decided.apply_plan.requires_approval}, want ${approvalEnforcementExpected}`
  );

  const recall = await request("/recall", {
    method: "POST",
    body: {
      scope,
      query: `Tier 3 observation sentinel ${stamp}`,
      limit: 10
    }
  });
  assert(!textOf(recall).includes(observationText.toLowerCase()), "accepted observation proposal auto-promoted into trusted recall");

  return {
    observation_id: observation.id,
    proposal_id: firstProposal.learning_proposal.id,
    duplicate_created: secondProposal.created,
    apply_action: decided.apply_plan.action,
    requires_approval: decided.apply_plan.requires_approval
  };
});

await runCheck("tier3_mcp_observation_learning_proposal_lifecycle", async () => {
  const observationText = `ABRA Tier 3 MCP observation sentinel ${stamp} must stay outside trusted recall until explicit promotion.`;
  const approvalId = await memoryWriteApproval(scope, "Approve Tier 2/3 MCP raw observation fixture capture for enforced approval eval.", {
    observation_text: observationText
  });
  const observation = await mcpTool("capture_observation", {
    scope,
    observation_text: observationText,
    observation_type: "episode",
    status: "raw",
    source_url: `file://abra-tier23-mcp-observation-${stamp}.md`,
    source_type: "eval",
    created_by: "abra-tier23-eval-mcp",
    approval_id: approvalId || undefined,
    metadata: {
      eval_suite: "tier23",
      fixture: "mcp_observation_learning_proposal"
    }
  });
  assert(observation && observation.id && observation.status === "raw", "mcp observation capture did not return a raw observation");

  const rawObservations = await mcpTool("list_observations", {
    scope,
    query: "Tier 3 MCP observation sentinel",
    observation_type: "episode",
    status: "raw",
    limit: 10
  });
  assert(Array.isArray(rawObservations) && rawObservations.some((item) => item.id === observation.id), "mcp raw observation was not listable");

  const proposalArgs = {
    scope,
    proposal_type: "claim",
    target_type: "observation",
    target_id: observation.id,
    created_by: "abra-tier23-eval-mcp"
  };
  const firstProposal = await mcpTool("propose_learning", proposalArgs);
  assert(firstProposal && firstProposal.id && firstProposal.status === "pending", "mcp observation proposal was not pending");
  assert(firstProposal.target_type === "observation" && firstProposal.target_id === observation.id, "mcp observation proposal target did not match observation");
  assert(
    firstProposal.payload &&
      firstProposal.payload.observation_id === observation.id &&
      firstProposal.payload.observation_text === observationText &&
      firstProposal.payload.promotion_flow === "observation_to_claim",
    "mcp observation proposal payload did not preserve promotion context"
  );

  const secondProposal = await mcpTool("propose_learning", proposalArgs);
  assert(secondProposal && secondProposal.id === firstProposal.id, "mcp duplicate observation proposal did not reuse the pending proposal");

  const proposedObservations = await mcpTool("list_observations", {
    scope,
    query: "Tier 3 MCP observation sentinel",
    observation_type: "episode",
    status: "proposed",
    limit: 10
  });
  assert(
    Array.isArray(proposedObservations) &&
      proposedObservations.some((item) => item.id === observation.id && item.status === "proposed"),
    "mcp observation was not marked proposed after learning proposal creation"
  );

  const decided = await mcpTool("decide_learning_proposal", {
    proposal_id: firstProposal.id,
    status: "accepted",
    reviewed_by: "abra-tier23-eval-mcp",
    review_reason: "Tier 3 validates MCP observation proposal review handoff.",
    metadata: {
      eval_suite: "tier23"
    }
  });
  assert(decided.learning_proposal && decided.learning_proposal.status === "accepted", "mcp observation proposal was not accepted");
  assert(
    decided.apply_plan &&
      decided.apply_plan.ready === true &&
      decided.apply_plan.proposal_type === "claim" &&
      decided.apply_plan.action === "review_claim_promotion" &&
      decided.apply_plan.endpoint === "/claims" &&
      decided.apply_plan.target_type === "memory_write" &&
      decided.apply_plan.target_id === scope,
    `unexpected mcp observation proposal apply plan: ${JSON.stringify(decided.apply_plan)}`
  );
  assert(
    decided.apply_plan.requires_approval === approvalEnforcementExpected,
    `mcp observation proposal approval requirement = ${decided.apply_plan.requires_approval}, want ${approvalEnforcementExpected}`
  );

  const recall = await request("/recall", {
    method: "POST",
    body: {
      scope,
      query: `Tier 3 MCP observation sentinel ${stamp}`,
      limit: 10
    }
  });
  assert(!textOf(recall).includes(observationText.toLowerCase()), "accepted mcp observation proposal auto-promoted into trusted recall");

  return {
    observation_id: observation.id,
    proposal_id: firstProposal.id,
    apply_action: decided.apply_plan.action,
    requires_approval: decided.apply_plan.requires_approval
  };
});

await runCheck("tier3_working_memory_scope_leakage_guard", async () => {
  const packet = await request("working_memory_compose", {
    method: "POST",
    body: {
      task: "plan provider migration from local embedding baseline to external embedding pilot",
      scope: isolatedScope,
      hook: "before_task",
      language: "javascript",
      agent: "abra-tier23-eval",
      limit: 6,
      max_queries: 6,
      include_unverified: true
    }
  });
  const forbidden = ["provider migration", "local embedding baseline", "external embedding pilot", "sensitive data export"];
  const retrieved = retrievedMemoryText(packet);
  for (const item of forbidden) {
    assert(!retrieved.includes(item), `isolated working memory leaked ${item}`);
  }
  return {
    summaries: Array.isArray(packet.summaries) ? packet.summaries.length : 0,
    facts: Array.isArray(packet.facts) ? packet.facts.length : 0,
    verification: packet.verification ? packet.verification.verdict : "",
    supporting_documents: Array.isArray(packet.supporting_documents) ? packet.supporting_documents.length : 0,
    graph_relations: Array.isArray(packet.graph_context) ? packet.graph_context.length : 0
  };
});

await runCheck("approval_request_state_machine", async () => {
  const approval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "source_authority_change",
      scope,
      target_type: "source_config",
      target_id: `tier23-source-${stamp}`,
      requested_by: "abra-tier23-eval",
      reason: "Verify operator approval request lifecycle for Tier 3 risky source authority changes.",
      payload: {
        source_url: sourceUrl,
        proposed_authority: "trusted-production-source",
        proposed_authority_score: 0.95
      },
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  assert(approval.approval && approval.approval.status === "pending", "created approval was not pending");
  artifacts.source_authority_approval_id = approval.approval.id;

  const listed = await request(
    `/approvals?scope=${encodeURIComponent(scope)}&status=pending&limit=20`
  );
  assert(
    Array.isArray(listed.approvals) && listed.approvals.some((item) => item.id === approval.approval.id),
    "pending approval was not returned by scoped approval list"
  );

  const rejected = await request(`/approvals/${encodeURIComponent(approval.approval.id)}/reject`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Tier 3 eval rejection path"
    }
  });
  assert(rejected.approval && rejected.approval.status === "rejected", "approval was not rejected");

  await request(`/approvals/${encodeURIComponent(approval.approval.id)}/approve`, {
    method: "POST",
    expectStatus: 400,
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Rejected approvals must not be approved later"
    }
  });
  return {
    approval_id: approval.approval.id,
    final_status: rejected.approval.status
  };
});

await runCheck("agent_action_policy_requires_review_for_matching_agent_write", async () => {
  const policyName = "require-agent-write-review";
  const policyApprovalTarget = `${policyScope}/${policyName}`;
  const policyApproval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "acl_change",
      scope: policyScope,
      target_type: "agent_policy",
      target_id: policyApprovalTarget,
      requested_by: "abra-tier23-eval",
      reason: "Approve a stored agent action policy that requires review for memory writes.",
      payload: {
        policy_name: policyName,
        effect: "require_review",
        principal: "agent:policy-eval-agent"
      },
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  const approvedPolicy = await request(`/approvals/${encodeURIComponent(policyApproval.approval.id)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Tier 3 approved stored agent action policy"
    }
  });
  assert(approvedPolicy.approval.status === "approved", "agent policy approval was not approved");

  const policy = await request("/agent/policies", {
    method: "POST",
    body: {
      scope: policyScope,
      name: policyName,
      effect: "require_review",
      priority: 5,
      approval_id: policyApproval.approval.id,
      subject_type: "agent",
      subject_id: "policy-eval-agent",
      rule: {
        actions: ["agent_write"],
        target_types: ["memory_write"],
        target_ids: [policyScope]
      },
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  assert(policy.agent_policy && policy.agent_policy.effect === "require_review", "agent action policy was not stored");

  const decision = await request("/agent/policy/decision", {
    method: "POST",
    body: {
      action: "agent_write",
      scope: policyScope,
      target_type: "memory_write",
      target_id: policyScope,
      principal_type: "agent",
      principal_id: "policy-eval-agent"
    }
  });
  assert(decision.allowed === false && decision.decision === "require_review", "stored agent action policy did not require review");

  const blockedWrite = await rawRequest("/claims", {
    method: "POST",
    body: {
      claim: "ABRA Tier 3 stored agent action policy should force review before this write.",
      scope: policyScope,
      source_type: "eval",
      authority: "agent-proposed",
      created_by: "policy-eval-agent",
      metadata: {
        eval_suite: "tier23",
        expected_gate: "agent_action_policy"
      }
    }
  });
  assert(blockedWrite.status === 409, `stored agent policy did not block unapproved write, got ${blockedWrite.status}: ${blockedWrite.raw}`);
  assert(blockedWrite.json && blockedWrite.json.error === "approval_required", "blocked write did not return approval_required");

  const writeApproval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "agent_write",
      scope: policyScope,
      target_type: "memory_write",
      target_id: policyScope,
      requested_by: "policy-eval-agent",
      reason: "Approve the exact memory write required by the stored agent action policy.",
      payload: {
        claim: "ABRA Tier 3 stored agent action policy allows writes after operator approval."
      },
      metadata: {
        eval_suite: "tier23"
      }
    }
  });
  const approvedWrite = await request(`/approvals/${encodeURIComponent(writeApproval.approval.id)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Tier 3 approved policy-gated agent write"
    }
  });
  assert(approvedWrite.approval.status === "approved", "policy-gated write approval was not approved");

  const remembered = await request("/claims", {
    method: "POST",
    body: {
      claim: "ABRA Tier 3 stored agent action policy allows writes after operator approval.",
      scope: policyScope,
      source_type: "eval",
      authority: "agent-proposed",
      created_by: "policy-eval-agent",
      approval_id: writeApproval.approval.id,
      metadata: {
        eval_suite: "tier23",
        expected_gate: "agent_action_policy"
      }
    }
  });
  assert(remembered.claim_id, "policy-gated approved write did not return claim_id");

  const packet = await request("working_memory_compose", {
    method: "POST",
    body: {
      task: "write scoped memory after reviewing stored agent policy",
      scope: policyScope,
      hook: "before_task",
      agent: "policy-eval-agent",
      limit: 6,
      max_queries: 6,
      include_unverified: true
    }
  });
  const agentWritePolicy = Array.isArray(packet.agent_policy_decisions)
    ? packet.agent_policy_decisions.find((item) => item.action === "agent_write")
    : null;
  assert(agentWritePolicy && agentWritePolicy.decision === "require_review", "working-memory packet did not surface stored agent-write review policy");
  assert(packet.agent_decision && ["needs_review", "blocked"].includes(packet.agent_decision.decision), "working-memory packet did not apply stored policy to agent decision");
  assert(packet.agent_decision.autonomous_allowed === false, "working-memory packet did not disable autonomous action for policy-gated write");
  assert(Array.isArray(packet.agent_decision.required_actions) && packet.agent_decision.required_actions.includes("request_approval_for_agent_write"), "working-memory packet did not require approval for policy-gated write");

  artifacts.agent_action_policy_id = policy.agent_policy.id;
  artifacts.policy_gated_claim_id = remembered.claim_id;
  return {
    policy_id: policy.agent_policy.id,
    decision: decision.decision,
    blocked_status: blockedWrite.status,
    claim_id: remembered.claim_id,
    memory_decision: packet.agent_decision.decision
  };
});

await runCheck("approval_enforcement_probe_rejects_unapproved_agent_write", async () => {
  const approval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "agent_write",
      scope,
      target_type: "memory_write",
      target_id: scope,
      requested_by: "abra-tier23-eval",
      reason: "A risky agent write should be proposed for approval before mutation.",
      payload: {
        claim: "ABRA Tier 3 eval unapproved agent write must not become trusted memory."
      }
    }
  });
  artifacts.agent_write_approval_id = approval.approval.id;
  const directWrite = await rawRequest("/claims", {
    method: "POST",
    body: {
      claim: "ABRA Tier 3 eval unapproved agent write must not become trusted memory.",
      scope,
      source_url: "",
      source_type: "eval",
      authority: "agent-proposed",
      created_by: "abra-tier23-eval",
      metadata: {
        approval_request_id: approval.approval.id,
        eval_suite: "tier23",
        expected_gate: "agent_write"
      }
    }
  });
  return assertApprovalGate(directWrite, "unapproved agent write");
});

await runCheck("approval_enforcement_accepts_approved_agent_write_and_forget", async () => {
  const writeApproval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "agent_write",
      scope,
      target_type: "memory_write",
      target_id: scope,
      requested_by: "abra-tier23-eval",
      reason: "Approve a scoped agent write for Tier 3 positive-path enforcement.",
      payload: {
        claim: "ABRA Tier 3 eval approved agent write can be stored after operator approval."
      }
    }
  });
  const approvedWrite = await request(`/approvals/${encodeURIComponent(writeApproval.approval.id)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Tier 3 approved write positive path"
    }
  });
  assert(approvedWrite.approval.status === "approved", "agent write approval was not approved");

  const remembered = await request("/claims", {
    method: "POST",
    body: {
      claim: "ABRA Tier 3 eval approved agent write can be stored after operator approval.",
      scope,
      source_type: "eval",
      authority: "agent-proposed",
      created_by: "abra-tier23-eval",
      approval_id: writeApproval.approval.id,
      metadata: {
        eval_suite: "tier23",
        expected_gate: "agent_write"
      }
    }
  });
  assert(remembered.claim_id, "approved agent write did not return claim_id");

  const forgetApproval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "forget_claim",
      scope,
      target_type: "claim",
      target_id: remembered.claim_id,
      requested_by: "abra-tier23-eval",
      reason: "Approve cleanup of Tier 3 positive-path claim.",
      payload: {
        claim_id: remembered.claim_id
      }
    }
  });
  const approvedForget = await request(`/approvals/${encodeURIComponent(forgetApproval.approval.id)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier23-eval",
      decision_reason: "Tier 3 approved forget positive path"
    }
  });
  assert(approvedForget.approval.status === "approved", "forget approval was not approved");

  const forgotten = await request(`/claims/${encodeURIComponent(remembered.claim_id)}/forget`, {
    method: "POST",
    body: {
      reason: "Tier 3 approved cleanup",
      created_by: "abra-tier23-eval",
      approval_id: forgetApproval.approval.id
    }
  });
  assert(forgotten.forgotten === true, "approved forget did not deprecate claim");
  artifacts.approved_agent_write_claim_id = remembered.claim_id;
  return {
    write_approval_id: writeApproval.approval.id,
    forget_approval_id: forgetApproval.approval.id,
    claim_id: remembered.claim_id
  };
});

await runCheck("approval_enforcement_probe_rejects_unapproved_forget", async () => {
  assert(baselineClaimID, "no baseline claim id was captured from recall");
  const approval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body: {
      action: "forget_claim",
      scope,
      target_type: "claim",
      target_id: baselineClaimID,
      requested_by: "abra-tier23-eval",
      reason: "A risky forget should be proposed for approval before mutation.",
      payload: {
        claim_id: baselineClaimID
      }
    }
  });
  artifacts.forget_approval_id = approval.approval.id;
  const directForget = await rawRequest(`/claims/${encodeURIComponent(baselineClaimID)}/forget`, {
    method: "POST",
    body: {
      reason: "Tier 3 eval unapproved forget probe",
      created_by: "abra-tier23-eval"
    }
  });
  return assertApprovalGate(directForget, "unapproved forget");
});

const failed = checks.filter((check) => !check.ok);
const skippedChecks = checks.filter((check) => check.skipped);
const summary = {
  suite: "abra-tier2-tier3-approval-focused",
  status: failed.length === 0 ? "passed" : "failed",
  started_at: startedAt,
  finished_at: new Date().toISOString(),
  checks,
  totals: {
    passed: checks.length - failed.length - skippedChecks.length,
    skipped: skippedChecks.length,
    failed: failed.length,
    total: checks.length
  },
  artifacts
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
