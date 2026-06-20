#!/usr/bin/env node

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const stamp = new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
const suffix = `${stamp}-${Math.random().toString(36).slice(2, 8)}`;
const scope = process.env.ABRA_TIER1_SCOPE || `team:eval-tier1-${suffix}`;
const isolatedScope = process.env.ABRA_TIER1_ISOLATED_SCOPE || `${scope}:isolated`;
const sourceUrl = `file://abra-tier1-${suffix}.md`;
const corroboratingSourceUrl = `file://abra-tier1-corroborating-${suffix}.md`;
const isolatedSourceUrl = `file://abra-tier1-isolated-${suffix}.md`;
const memoryMaxMs = Number(process.env.ABRA_TIER1_MEMORY_MAX_MS || "2500");

const startedAt = new Date().toISOString();
const checks = [];
const artifacts = {
  base_url: baseUrl,
  scope,
  isolated_scope: isolatedScope,
  source_url: sourceUrl,
  corroborating_source_url: corroboratingSourceUrl,
  isolated_source_url: isolatedSourceUrl
};

requireTokenForRemoteBaseURL(baseUrl);

function requireTokenForRemoteBaseURL(rawBaseUrl) {
  const url = new URL(rawBaseUrl);
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(url.hostname);
  if (!loopback && !process.env.ABRA_API_TOKEN && process.env.ABRA_ALLOW_DEV_TOKEN !== "1") {
    throw new Error("ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments.");
  }
}

async function request(path, { method = "GET", body, expectStatus = 200, text = false } = {}) {
  const response = await fetch(`${baseUrl}${path}`, {
    method,
    headers: {
      authorization: `Bearer ${token}`,
      ...(body === undefined ? {} : { "content-type": "application/json" })
    },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  const raw = await response.text();
  if (response.status !== expectStatus) {
    throw new Error(`${method} ${path} returned ${response.status}, expected ${expectStatus}: ${raw}`);
  }
  if (text) {
    return raw;
  }
  if (raw.trim() === "") {
    return {};
  }
  return JSON.parse(raw);
}

async function approvedApproval(body) {
  const approval = await request("/approvals", {
    method: "POST",
    expectStatus: 202,
    body
  });
  assert(approval.approval && approval.approval.id, "approval request did not return an id");
  const decision = await request(`/approvals/${encodeURIComponent(approval.approval.id)}/approve`, {
    method: "POST",
    body: {
      decided_by: "abra-tier1-eval",
      decision_reason: "Tier 1 deterministic eval"
    }
  });
  assert(decision.approval && decision.approval.status === "approved", "approval request was not approved");
  return approval.approval.id;
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function textOf(value) {
  return JSON.stringify(value).toLowerCase();
}

function retrievedMemoryText(packet) {
  return textOf({
    summaries: packet.summaries,
    facts: packet.facts,
    supporting_documents: packet.supporting_documents,
    graph_context: packet.graph_context,
    evidence: packet.evidence
  });
}

async function runCheck(name, fn) {
  const before = Date.now();
  try {
    const details = await fn();
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

let ingest;
let corroboratingIngest;
let isolatedIngest;
let recall;
let isolatedRecall;
let policy;
let entities;
let relations;
let memoryPacket;
let isolatedMemoryPacket;
let forgottenClaimID;
let afterForget;
let capturedObservation;

await runCheck("runtime_ready_with_local_embeddings", async () => {
  const ready = await request("/readyz");
  assert(ready.ok === true, "readyz did not report ok=true");
  assert(
    ready.embedding_provider === "local" || process.env.ABRA_TIER1_ALLOW_NONLOCAL === "1",
    `Tier 1 must run with EMBEDDING_PROVIDER=local for the default local neural recall profile, got ${ready.embedding_provider}`
  );
  artifacts.embedding_provider = ready.embedding_provider;
  return { embedding_provider: ready.embedding_provider, auth_required: ready.auth_required };
});

await runCheck("seed_fixture_documents", async () => {
  ingest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "markdown",
      source_url: sourceUrl,
      source_id: `abra-tier1-${suffix}`,
      title: "Abra Tier 1 Frontend Fixture",
      scope,
      content: [
        "- Example Web App should use `Playwright` for critical user journeys before release.",
        "- Example Web App uses `Shared UI Tokens` for shared UI primitives.",
        "- Review Service depends on `Postgres` for scoring decisions."
      ].join("\n"),
      metadata: {
        authority: "team-convention",
        authority_score: 0.8,
        eval_tier: "tier1"
      }
    }
  });
  isolatedIngest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "markdown",
      source_url: isolatedSourceUrl,
      source_id: `abra-tier1-isolated-${suffix}`,
      title: "Abra Tier 1 Isolated Fixture",
      scope: isolatedScope,
      content: "- Archive Service should use Spreadsheet Exports for release review evidence.",
      metadata: {
        authority: "team-convention",
        authority_score: 0.7,
        eval_tier: "tier1"
      }
    }
  });
  corroboratingIngest = await request("/ingest/documents", {
    method: "POST",
    body: {
      source_type: "markdown",
      source_url: corroboratingSourceUrl,
      source_id: `abra-tier1-corroborating-${suffix}`,
      title: "Abra Tier 1 Design System Fixture",
      scope,
      content: [
        "- Design System Handbook says Example Web App should use `Shared UI Tokens` for reusable UI primitives.",
        "- Release reviewers should inspect critical user journey evidence before approving frontend changes."
      ].join("\n"),
      metadata: {
        authority: "design-system-handbook",
        authority_score: 0.75,
        eval_tier: "tier1"
      }
    }
  });
  assert(ingest.document_id, "primary ingest did not return document_id");
  assert(corroboratingIngest.document_id, "corroborating ingest did not return document_id");
  assert(ingest.claims >= 3, `primary fixture produced too few claims: ${ingest.claims}`);
  assert(isolatedIngest.document_id, "isolated ingest did not return document_id");
  artifacts.document_id = ingest.document_id;
  artifacts.corroborating_document_id = corroboratingIngest.document_id;
  artifacts.isolated_document_id = isolatedIngest.document_id;
  return {
    document_id: ingest.document_id,
    claims: ingest.claims,
    entities: ingest.entities,
    relations: ingest.relations,
    corroborating_document_id: corroboratingIngest.document_id,
    isolated_document_id: isolatedIngest.document_id
  };
});

await runCheck("raw_observation_is_searchable_but_not_trusted_recall", async () => {
  const observationText = `Observation-only release sentinel ${suffix} should stay outside trusted recall.`;
  const approvalID = await approvedApproval({
    action: "agent_write",
    scope,
    target_type: "memory_write",
    target_id: scope,
    reason: "Tier 1 deterministic eval records one raw observation."
  });
  capturedObservation = await request("/observations", {
    method: "POST",
    body: {
      scope,
      observation_text: observationText,
      observation_type: "episode",
      status: "raw",
      source_url: `file://abra-tier1-observation-${suffix}.md`,
      created_by: "abra-tier1-eval",
      approval_id: approvalID,
      metadata: {
        eval_tier: "tier1"
      }
    }
  });
  assert(capturedObservation.observation && capturedObservation.observation.id, "observation capture did not return an id");
  assert(capturedObservation.observation.status === "raw", `observation status = ${capturedObservation.observation.status}`);
  const listed = await request(`/observations?scope=${encodeURIComponent(scope)}&query=${encodeURIComponent("release sentinel")}&type=episode&status=raw&limit=10`);
  const observations = Array.isArray(listed.observations) ? listed.observations : [];
  assert(observations.some((item) => item.id === capturedObservation.observation.id), "captured observation was not listable by scope/query/type/status");
  const observationRecall = await request("/recall", {
    method: "POST",
    body: {
      query: `release sentinel ${suffix}`,
      scope,
      limit: 10
    }
  });
  assert(!textOf(observationRecall).includes(observationText), "raw observation leaked into trusted recall output");
  const proposed = await request("/learning/proposals", {
    method: "POST",
    expectStatus: 202,
    body: {
      scope,
      proposal_type: "claim",
      title: `Promote observation ${capturedObservation.observation.id}`,
      rationale: "Tier 1 verifies observation review without trusted auto-promotion.",
      target_type: "observation",
      target_id: capturedObservation.observation.id,
      source_url: `file://abra-tier1-observation-${suffix}.md`,
      confidence: 0.7,
      payload: {
        observation_id: capturedObservation.observation.id,
        claim: observationText,
        promotion_flow: "observation_to_claim"
      },
      created_by: "abra-tier1-eval"
    }
  });
  assert(proposed.learning_proposal && proposed.learning_proposal.id, "observation proposal did not return a learning proposal");
  assert(proposed.learning_proposal.target_type === "observation", `proposal target_type = ${proposed.learning_proposal.target_type}`);
  const repeatedProposal = await request("/learning/proposals", {
    method: "POST",
    expectStatus: 202,
    body: {
      scope,
      proposal_type: "claim",
      title: `Promote observation ${capturedObservation.observation.id}`,
      rationale: "Tier 1 verifies observation review without trusted auto-promotion.",
      target_type: "observation",
      target_id: capturedObservation.observation.id,
      source_url: `file://abra-tier1-observation-${suffix}.md`,
      confidence: 0.7,
      payload: {
        observation_id: capturedObservation.observation.id,
        claim: observationText,
        promotion_flow: "observation_to_claim"
      },
      created_by: "abra-tier1-eval"
    }
  });
  assert(
    repeatedProposal.learning_proposal && repeatedProposal.learning_proposal.id === proposed.learning_proposal.id,
    "repeated observation proposal did not reuse the pending proposal"
  );
  assert(repeatedProposal.created === false, "repeated observation proposal should not create a duplicate proposal");
  const proposedObservations = await request(`/observations?scope=${encodeURIComponent(scope)}&query=${encodeURIComponent("release sentinel")}&type=episode&status=proposed&limit=10`);
  assert(
    Array.isArray(proposedObservations.observations) && proposedObservations.observations.some((item) => item.id === capturedObservation.observation.id),
    "observation was not marked proposed after learning proposal creation"
  );
  const decided = await request(`/learning/proposals/${encodeURIComponent(proposed.learning_proposal.id)}/decide`, {
    method: "POST",
    body: {
      status: "accepted",
      reviewed_by: "abra-tier1-eval",
      review_reason: "Tier 1 verifies apply-plan handoff only."
    }
  });
  assert(
    decided.apply_plan &&
      decided.apply_plan.ready === true &&
      decided.apply_plan.proposal_type === "claim" &&
      decided.apply_plan.action === "review_claim_promotion" &&
      decided.apply_plan.endpoint === "/claims" &&
      decided.apply_plan.target_type === "memory_write" &&
      decided.apply_plan.target_id === scope,
    `unexpected apply_plan = ${JSON.stringify(decided.apply_plan)}`
  );
  const afterProposalRecall = await request("/recall", {
    method: "POST",
    body: {
      query: `release sentinel ${suffix}`,
      scope,
      limit: 10
    }
  });
  assert(!textOf(afterProposalRecall).includes(observationText), "accepted observation proposal auto-promoted into trusted recall");
  return { observation_id: capturedObservation.observation.id, proposal_id: proposed.learning_proposal.id, listed: observations.length };
});

await runCheck("recall_returns_expected_claim_and_citation", async () => {
  recall = await request("/recall", {
    method: "POST",
    body: {
      query: "Playwright critical user journeys",
      scope,
      limit: 5,
      include_unverified: false
    }
  });
  const claims = Array.isArray(recall.claims) ? recall.claims : [];
  const supportingDocuments = Array.isArray(recall.supporting_documents) ? recall.supporting_documents : [];
  assert(recall.retrieval_mode === "hybrid", `recall mode = ${recall.retrieval_mode}, want hybrid`);
  const playwrightClaim = claims.find((claim) => String(claim.claim_text || "").includes("Playwright"));
  assert(playwrightClaim, "recall did not return the expected Playwright claim");
  assert(Number.isFinite(Number(playwrightClaim.text_score)), "Playwright claim did not include text_score");
  assert(Number.isFinite(Number(playwrightClaim.vector_score)), "Playwright claim did not include vector_score");
  assert(playwrightClaim.status === "verified", `expected verified Playwright claim, got ${playwrightClaim.status}`);
  assert(playwrightClaim.source_url === sourceUrl, `expected source_url ${sourceUrl}, got ${playwrightClaim.source_url}`);
  assert(
    supportingDocuments.every((document) => Number.isFinite(Number(document.text_score)) && Number.isFinite(Number(document.vector_score))),
    "supporting documents did not include text/vector scores"
  );
  assert(
    supportingDocuments.some((document) => document.source_url === sourceUrl),
    "recall did not return the seeded source as a supporting document"
  );
  forgottenClaimID = playwrightClaim.id;
  artifacts.playwright_claim_id = forgottenClaimID;
  return {
    retrieval_mode: recall.retrieval_mode,
    claims: claims.length,
    supporting_documents: supportingDocuments.length,
    playwright_claim_id: forgottenClaimID
  };
});

await runCheck("recall_does_not_leak_across_scope", async () => {
  isolatedRecall = await request("/recall", {
    method: "POST",
    body: {
      query: "Playwright critical user journeys",
      scope: isolatedScope,
      limit: 5,
      include_unverified: true
    }
  });
  assert(!textOf(isolatedRecall).includes("playwright"), "isolated scope leaked Playwright recall result");
  return {
    claims: Array.isArray(isolatedRecall.claims) ? isolatedRecall.claims.length : 0,
    supporting_documents: Array.isArray(isolatedRecall.supporting_documents)
      ? isolatedRecall.supporting_documents.length
      : 0
  };
});

await runCheck("policy_plan_requires_scoped_recall_queries", async () => {
  policy = await request("/policy/plan", {
    method: "POST",
    body: {
      hook: "before_code",
      task: "implement frontend end-to-end test coverage",
      scope,
      files: ["cmd/abra/main.go"],
      changed_files: ["cmd/abra/main.go"],
      language: "javascript",
      agent: "agent-alpha"
    }
  });
  assert(policy.required === true, "policy plan did not require recall");
  assert(Array.isArray(policy.queries) && policy.queries.length >= 2, "policy plan did not return enough queries");
  assert(policy.queries.every((query) => query.scope === scope), "policy plan returned a query outside eval scope");
  assert(textOf(policy.queries).includes("coding conventions"), "policy plan did not include coding convention recall");
  return { queries: policy.queries.length, hook: policy.hook };
});

await runCheck("graph_contains_expected_entities_and_relation", async () => {
  entities = await request(`/graph/entities?scope=${encodeURIComponent(scope)}&limit=50`);
  relations = await request(`/graph/relations?scope=${encodeURIComponent(scope)}&limit=50`);
  const entityList = Array.isArray(entities.entities) ? entities.entities : [];
  const relationList = Array.isArray(relations.relations) ? relations.relations : [];
  const entityNames = new Set(entityList.map((entity) => entity.name));
  assert(entityNames.has("Example Web App"), "graph entities did not include Example Web App");
  assert(entityNames.has("Playwright"), "graph entities did not include Playwright");
  assert(
    relationList.some(
      (relation) =>
        relation.from_entity === "Example Web App" &&
        relation.to_entity === "Playwright" &&
        relation.relation_type === "should_use"
    ),
    "graph relations did not include Example Web App should_use Playwright"
  );
  return { entities: entityList.length, relations: relationList.length };
});

await runCheck("graph_retrieval_expands_shared_entity_neighbors", async () => {
  const graphRecall = await request("/recall", {
    method: "POST",
    body: {
      query: "Playwright",
      scope,
      limit: 8,
      include_unverified: false
    }
  });
  const graphContext = Array.isArray(graphRecall.graph_context) ? graphRecall.graph_context : [];
  assert(
    graphContext.some(
      (relation) =>
        relation.from_entity === "Example Web App" &&
        relation.to_entity === "Playwright" &&
        relation.relation_type === "should_use"
    ),
    "graph retrieval did not include direct Playwright edge"
  );
  assert(
    graphContext.some(
      (relation) =>
        relation.from_entity === "Example Web App" &&
        relation.to_entity === "Shared UI Tokens" &&
        relation.relation_type === "uses"
    ),
    "graph retrieval did not expand to shared Example Web App neighbor edge"
  );
  return {
    graph_relations: graphContext.length,
    direct_edge: "Example Web App -> Playwright",
    expanded_edge: "Example Web App -> Shared UI Tokens"
  };
});

await runCheck("working_memory_packet_is_agent_ready", async () => {
  const before = Date.now();
  memoryPacket = await request("/memory/compose", {
    method: "POST",
    body: {
      task: "implement frontend e2e coverage using Playwright and shared design system tokens",
      scope,
      hook: "before_code",
      files: ["cmd/abra/main.go"],
      changed_files: ["cmd/abra/main.go"],
      language: "javascript",
      agent: "abra-tier1-eval",
      limit: 6,
      max_queries: 6,
      include_unverified: false
    }
  });
  const latency = Date.now() - before;
  assert(latency <= memoryMaxMs, `working memory latency ${latency}ms exceeded ${memoryMaxMs}ms`);
  assert(memoryPacket.intent === "implementation", `expected implementation intent, got ${memoryPacket.intent}`);
  assert(memoryPacket.strategy && memoryPacket.strategy.includes("implementation packet"), "memory strategy was not implementation-aware");
  assert(memoryPacket.plan && memoryPacket.plan.required === true, "memory packet did not include a required policy plan");
  assert(Array.isArray(memoryPacket.plan.queries) && memoryPacket.plan.queries.length >= 2, "memory packet had too few planned queries");
  assert(memoryPacket.plan.queries.every((query) => query.scope === scope), "memory packet included a query outside eval scope");
  assert(memoryPacket.retrieval_plan && memoryPacket.retrieval_plan.mode, "memory packet did not include retrieval planner output");
  assert(Array.isArray(memoryPacket.retrieval_plan.stages) && memoryPacket.retrieval_plan.stages.length >= 4, "retrieval plan had too few stages");
  assert(memoryPacket.retrieval_plan.coverage_targets && memoryPacket.retrieval_plan.coverage_targets.summaries >= 1, "retrieval plan did not include coverage targets");
  assert(Array.isArray(memoryPacket.summaries) && memoryPacket.summaries.length >= 1, "memory packet did not include summaries");
  assert(Array.isArray(memoryPacket.facts) && memoryPacket.facts.some((claim) => String(claim.claim_text || "").includes("Playwright")), "memory packet did not include the Playwright fact");
  assert(Array.isArray(memoryPacket.supporting_documents) && memoryPacket.supporting_documents.some((document) => document.source_url === sourceUrl), "memory packet did not include the seeded supporting document");
  assert(Array.isArray(memoryPacket.graph_context) && memoryPacket.graph_context.some((relation) => relation.from_entity === "Example Web App" && relation.to_entity === "Playwright"), "memory packet did not include expected graph context");
  assert(Array.isArray(memoryPacket.impact_map) && memoryPacket.impact_map.some((item) => item.kind === "entity" && item.name === "Example Web App"), "memory packet did not include expected impact map entity");
  assert(Array.isArray(memoryPacket.evidence) && memoryPacket.evidence.some((item) => item.source_url === sourceUrl), "memory packet did not include grouped evidence");
  assert(memoryPacket.verification && memoryPacket.verification.verdict === "strong", `memory packet verification was not strong: ${memoryPacket.verification && memoryPacket.verification.verdict}`);
  assert(memoryPacket.verification.claim_coverage === 1, "memory packet verification did not report full claim coverage");
  assert(Array.isArray(memoryPacket.verification.checks) && memoryPacket.verification.checks.length >= 4, "memory packet verification checks were missing");
  assert(memoryPacket.verification.retrieval_coverage && memoryPacket.verification.retrieval_coverage.complete === true, "memory packet did not satisfy retrieval coverage");
  assert(memoryPacket.verification.checks.some((check) => check.name === "retrieval_coverage"), "memory packet verification did not include retrieval coverage check");
  assert(memoryPacket.verification.retrieval_quality && memoryPacket.verification.retrieval_quality.result_count >= 1, "memory packet verification did not include retrieval quality");
  assert(memoryPacket.verification.checks.some((check) => check.name === "retrieval_quality"), "memory packet verification did not include retrieval quality check");
  assert(Array.isArray(memoryPacket.agent_policy_decisions) && memoryPacket.agent_policy_decisions.length >= 4, "memory packet did not include agent policy decisions");
  assert(memoryPacket.agent_policy_decisions.some((item) => item.action === "agent_write"), "memory packet did not include agent-write policy decision");
  assert(memoryPacket.agent_decision && memoryPacket.agent_decision.decision === "proceed", `memory packet did not produce a proceed decision: ${memoryPacket.agent_decision && memoryPacket.agent_decision.decision}`);
  assert(memoryPacket.agent_decision.autonomous_allowed === true, "memory packet did not allow autonomous action for strong verified memory");
  assert(Array.isArray(memoryPacket.learning_suggestions) && memoryPacket.learning_suggestions.length >= 1, "memory packet did not include learning suggestions");
  assert(Array.isArray(memoryPacket.validation_plan) && memoryPacket.validation_plan.some((item) => item.command === "npm test" && item.required === true), "memory packet did not include package validation plan");
  assert(Array.isArray(memoryPacket.risks) && memoryPacket.risks.length >= 1, "memory packet did not include risk guidance");
  assert(Array.isArray(memoryPacket.suggested_steps) && memoryPacket.suggested_steps.length >= 2, "memory packet did not include suggested next steps");
  assert(memoryPacket.stats && memoryPacket.stats.queries_run >= 2, "memory packet did not report query stats");
  assert(memoryPacket.stats.summaries >= 1, "memory packet did not report summary stats");
  assert(memoryPacket.stats.graph_relations >= 1, "memory packet did not report graph stats");
  assert(memoryPacket.stats.graph_queries >= 1, "memory packet did not report graph query stats");
  assert(memoryPacket.stats.impact_items === memoryPacket.impact_map.length, "memory packet did not report impact map stats");
  assert(memoryPacket.stats.validation_steps === memoryPacket.validation_plan.length, "memory packet did not report validation plan stats");
  return {
    latency_ms: latency,
    max_latency_ms: memoryMaxMs,
    summaries: memoryPacket.summaries.length,
    facts: memoryPacket.facts.length,
    supporting_documents: memoryPacket.supporting_documents.length,
    graph_relations: memoryPacket.graph_context.length,
    impact_items: memoryPacket.impact_map.length,
    evidence: memoryPacket.evidence.length,
    verification: memoryPacket.verification.verdict,
    agent_decision: memoryPacket.agent_decision.decision,
    validation_steps: memoryPacket.validation_plan.length,
    learning_suggestions: memoryPacket.learning_suggestions.length,
    suggested_steps: memoryPacket.suggested_steps.length
  };
});

await runCheck("working_memory_does_not_leak_across_scope", async () => {
  isolatedMemoryPacket = await request("/memory/compose", {
    method: "POST",
    body: {
      task: "implement frontend e2e coverage using Playwright and shared design system tokens",
      scope: isolatedScope,
      hook: "before_code",
      files: ["cmd/abra/main.go"],
      changed_files: ["cmd/abra/main.go"],
      language: "javascript",
      agent: "abra-tier1-eval",
      limit: 6,
      max_queries: 6,
      include_unverified: true
    }
  });
  const retrieved = retrievedMemoryText(isolatedMemoryPacket);
  assert(!retrieved.includes("playwright"), "isolated working memory leaked Playwright context");
  assert(!retrieved.includes("design system tokens"), "isolated working memory leaked design-system context");
  return {
    summaries: Array.isArray(isolatedMemoryPacket.summaries) ? isolatedMemoryPacket.summaries.length : 0,
    facts: Array.isArray(isolatedMemoryPacket.facts) ? isolatedMemoryPacket.facts.length : 0,
    verification: isolatedMemoryPacket.verification ? isolatedMemoryPacket.verification.verdict : "",
    supporting_documents: Array.isArray(isolatedMemoryPacket.supporting_documents)
      ? isolatedMemoryPacket.supporting_documents.length
      : 0,
    graph_relations: Array.isArray(isolatedMemoryPacket.graph_context) ? isolatedMemoryPacket.graph_context.length : 0
  };
});

await runCheck("summary_rebuild_backfills_existing_documents", async () => {
  const approvalID = await approvedApproval({
    action: "backfill",
    scope,
    target_type: "memory_summaries",
    target_id: scope,
    requested_by: "abra-tier1-eval",
    reason: "Tier 1 deterministic eval verifies summary rebuild for existing documents.",
    payload: {
      limit: 10
    }
  });
  const rebuild = await request("/memory/summaries/rebuild", {
    method: "POST",
    body: {
      scope,
      limit: 10,
      approval_id: approvalID
    }
  });
  assert(rebuild.scope === scope, `summary rebuild returned unexpected scope ${rebuild.scope}`);
  assert(rebuild.documents >= 1, `summary rebuild processed too few documents: ${rebuild.documents}`);
  assert(rebuild.summaries >= 1, `summary rebuild wrote too few summaries: ${rebuild.summaries}`);
  const summaries = await request("/memory/summaries", {
    method: "POST",
    body: {
      query: "Example Web App Playwright design system tokens",
      scope,
      limit: 10
    }
  });
  assert(Array.isArray(summaries.summaries) && summaries.summaries.length >= 1, "summary query returned no summaries after rebuild");
  return {
    approval_id: approvalID,
    documents: rebuild.documents,
    summaries: rebuild.summaries,
    query_summaries: summaries.summaries.length
  };
});

await runCheck("forgotten_claim_is_not_returned_as_trusted_memory", async () => {
  assert(forgottenClaimID, "no Playwright claim id was captured from recall");
  const approvalID = await approvedApproval({
    action: "forget_claim",
    scope,
    target_type: "claim",
    target_id: forgottenClaimID,
    requested_by: "abra-tier1-eval",
    reason: "Tier 1 deterministic eval needs to verify forgotten claims disappear from recall.",
    payload: {
      claim_id: forgottenClaimID
    }
  });
  const forget = await request(`/claims/${encodeURIComponent(forgottenClaimID)}/forget`, {
    method: "POST",
    body: { reason: "tier1 deterministic eval", created_by: "abra-tier1-eval", approval_id: approvalID }
  });
  assert(forget.forgotten === true, "forget endpoint did not mark claim as forgotten");
  afterForget = await request("/recall", {
    method: "POST",
    body: {
      query: "Playwright critical user journeys",
      scope,
      limit: 10,
      include_unverified: true
    }
  });
  const claims = Array.isArray(afterForget.claims) ? afterForget.claims : [];
  assert(!claims.some((claim) => claim.id === forgottenClaimID), "forgotten claim id was returned by recall");
  return { remaining_claims: claims.length, forgotten_claim_id: forgottenClaimID };
});

const failed = checks.filter((check) => !check.ok);
const summary = {
  suite: "abra-tier1-deterministic",
  status: failed.length === 0 ? "passed" : "failed",
  started_at: startedAt,
  finished_at: new Date().toISOString(),
  checks,
  totals: {
    passed: checks.length - failed.length,
    failed: failed.length,
    total: checks.length
  },
  artifacts
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
