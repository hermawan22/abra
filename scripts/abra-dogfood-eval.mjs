import fs from "node:fs";
import path from "node:path";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const repoPath = path.resolve(process.env.ABRA_DOGFOOD_REPO_PATH || process.cwd());
const sourceRoot = process.env.ABRA_DOGFOOD_SOURCE_ROOT || repoPath;
const scope = process.env.ABRA_DOGFOOD_SCOPE || "repo:abra";
const sourceName = process.env.ABRA_DOGFOOD_SOURCE_NAME || "abra-self";
const keepSourceActive = process.env.ABRA_DOGFOOD_KEEP_SOURCE_ACTIVE === "1";
const timeoutMs = Number(process.env.ABRA_DOGFOOD_TIMEOUT_MS || 180000);
const pollMs = Number(process.env.ABRA_DOGFOOD_POLL_MS || 2000);

requireTokenForRemoteBaseURL(baseUrl);

function requireTokenForRemoteBaseURL(rawBaseUrl) {
  const url = new URL(rawBaseUrl);
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(url.hostname);
  if (!loopback && !process.env.ABRA_API_TOKEN && process.env.ABRA_ALLOW_DEV_TOKEN !== "1") {
    throw new Error("ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments.");
  }
}

function assert(condition, message, details = undefined) {
  if (!condition) {
    const error = new Error(message);
    error.details = details;
    throw error;
  }
}

function requireRepoFile(relPath) {
  const fullPath = path.join(repoPath, relPath);
  assert(fs.existsSync(fullPath), `dogfood repo path is missing ${relPath}`, { repoPath, fullPath });
}

async function request(route, { method = "GET", body } = {}) {
  const headers = {
    authorization: `Bearer ${token}`,
  };
  const options = { method, headers };
  if (body !== undefined) {
    headers["content-type"] = "application/json";
    options.body = JSON.stringify(body);
  }
  const response = await fetch(`${baseUrl}${route}`, options);
  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { raw: text };
    }
  }
  if (!response.ok) {
    const error = new Error(`${method} ${route} failed with ${response.status}`);
    error.details = data;
    throw error;
  }
  return data;
}

async function approvedRequest({ action, targetType, targetId, reason, payload }) {
  const created = await request("/approvals", {
    method: "POST",
    body: {
      action,
      scope,
      target_type: targetType,
      target_id: targetId,
      requested_by: "dogfood-eval",
      reason,
      payload,
      metadata: { eval: "dogfood" },
    },
  });
  const approvalId = created?.approval?.id;
  assert(approvalId, "approval creation did not return approval.id", created);
  await request(`/approvals/${approvalId}/approve`, {
    method: "POST",
    body: {
      decided_by: "dogfood-eval",
      decision_reason: reason,
      metadata: { eval: "dogfood" },
    },
  });
  return approvalId;
}

async function waitForJob(jobId) {
  const started = Date.now();
  while (Date.now() - started < timeoutMs) {
    const listed = await request(`/ingestion/jobs?scope=${encodeURIComponent(scope)}&limit=25`);
    const job = (listed.ingestion_jobs || []).find((item) => item.id === jobId);
    if (job?.status === "succeeded") {
      return job;
    }
    if (job && ["failed", "canceled"].includes(job.status)) {
      throw Object.assign(new Error(`dogfood ingestion job ${jobId} ended as ${job.status}`), { details: job });
    }
    await new Promise((resolve) => setTimeout(resolve, pollMs));
  }
  throw new Error(`timed out waiting for dogfood ingestion job ${jobId}`);
}

function sourceConfigBody(approvalId, { id = undefined, status = "active" } = {}) {
  return {
    ...(id ? { id } : {}),
    name: sourceName,
    source_type: "local_repo",
    scope,
    base_url: `file://${sourceRoot}`,
    connector_kind: "generic",
    status,
    authority: "source-code",
    authority_score: 0.82,
    approval_id: approvalId,
    config: {
      root: sourceRoot,
      include: [
        "README.md",
        "PRODUCTION.md",
        "RELEASE.md",
        "SECURITY.md",
        "docs/**/*.md",
        "deploy/**/*.md",
        "examples/**/*.md",
      ],
      exclude: [
        ".git/**",
        "node_modules/**",
        "backups/**",
        "coverage/**",
        "dist/**",
        "build/**",
        ".next/**",
      ],
      include_code: true,
      code_include: [
        "cmd/**/*.go",
        "internal/**/*.go",
        "package.json",
        "go.mod",
      ],
      code_exclude: ["**/*_test.go"],
      git_provider: "local",
      git_project_path: "abra",
    },
    metadata: {
      repo: "abra",
      dogfood: "true",
      source_root: sourceRoot,
    },
    created_by: "dogfood-eval",
  };
}

function summarizeMemory(memory) {
  return {
    decision: memory.agent_decision?.decision,
    verdict: memory.verification?.verdict,
    summaries: memory.summaries?.length || 0,
    facts: memory.facts?.length || 0,
    documents: memory.supporting_documents?.length || 0,
    graph_relations: memory.graph_context?.length || 0,
    impact_items: memory.impact_map?.length || 0,
    validation_steps: memory.validation_plan?.length || 0,
    context_blocks: memory.context_window?.blocks?.length || 0,
    context_tokens: memory.context_window?.estimated_tokens || 0,
    risks: memory.risks?.length || 0,
    health_status: memory.memory_health?.status,
    health_signals: memory.stats?.health_signals || 0,
    duration_ms: memory.stats?.total_duration_ms,
  };
}

function isCodeSourceURL(sourceURL = "") {
  return /\.(go|js|jsx|ts|tsx|json)(\?|#|$)/i.test(sourceURL);
}

async function main() {
  requireRepoFile("README.md");
  requireRepoFile("PRODUCTION.md");
  requireRepoFile("go.mod");
  requireRepoFile("internal/memory/composer.go");

  const ready = await request("/readyz");
  assert(ready.ok === true, "Abra is not ready", ready);

  const sourceTarget = `${scope}/local_repo/${sourceName}`;
  const sourceApprovalId = await approvedRequest({
    action: "source_authority_change",
    targetType: "source_config",
    targetId: sourceTarget,
    reason: "Dogfood Abra by ingesting its own repository as source-backed memory.",
    payload: { sourceName, sourceRoot, authority: "source-code", authority_score: 0.82 },
  });
  const source = await request("/sources/configs", {
    method: "POST",
    body: sourceConfigBody(sourceApprovalId),
  });
  const sourceConfigId = source.source_config_id;
  assert(sourceConfigId, "source config upsert did not return source_config_id", source);

  const queued = await request("/ingestion/jobs", {
    method: "POST",
    body: {
      source_config_id: sourceConfigId,
      trigger_type: "backfill",
      created_by: "dogfood-eval",
      max_attempts: 1,
      metadata: { eval: "dogfood" },
    },
  });
  const jobId = queued?.ingestion_job?.id;
  assert(jobId, "ingestion job enqueue did not return ingestion_job.id", queued);
  const job = await waitForJob(jobId);
  assert(job.documents_seen > 0, "dogfood ingestion saw no documents", job);
  assert(job.chunks_written > 0 || job.documents_changed === 0, "dogfood ingestion wrote no chunks for changed documents", job);

  const rebuildApprovalId = await approvedRequest({
    action: "backfill",
    targetType: "memory_summaries",
    targetId: scope,
    reason: "Dogfood summary rebuild after self-ingestion.",
    payload: { scope, limit: 80 },
  });
  const rebuild = await request("/memory/summaries/rebuild", {
    method: "POST",
    body: { scope, limit: 80, approval_id: rebuildApprovalId },
  });
  assert(rebuild.documents > 0, "summary rebuild saw no documents", rebuild);
  assert(rebuild.summaries > 0, "summary rebuild wrote no summaries", rebuild);

  const memory = await request("/memory/compose", {
    method: "POST",
    body: {
      task: "explain Abra architecture, ingestion, graph, policy planner, working-memory composer, and production readiness",
      scope,
      agent: "dogfood-eval",
      hook: "before_task",
      language: "go",
      files: ["README.md", "PRODUCTION.md", "internal/memory/composer.go", "internal/brain/service.go"],
      limit: 10,
      max_queries: 8,
      token_budget: 1200,
    },
  });
  const memorySummary = summarizeMemory(memory);
  assert(memorySummary.summaries > 0, "working-memory dogfood returned no summaries", memorySummary);
  assert(memorySummary.documents > 0 || memorySummary.facts > 0, "working-memory dogfood returned no facts or source documents", memorySummary);
  assert(memorySummary.graph_relations > 0, "working-memory dogfood returned no graph relations", memorySummary);
  assert(memorySummary.impact_items > 0, "working-memory dogfood returned no impact map", memorySummary);
  assert(memorySummary.validation_steps > 0, "working-memory dogfood returned no validation plan", memorySummary);
  assert(memorySummary.context_blocks > 0, "working-memory dogfood returned no budgeted context window", memorySummary);
  assert(memory.memory_health?.status === "healthy", "working-memory dogfood did not include healthy memory health", memory.memory_health);
  assert(memory.stats?.health_signals >= 1, "working-memory dogfood did not report health signal stats", memory.stats);
  assert(memory.context_window?.prompt?.includes("Memory health: healthy"), "working-memory dogfood context window did not include memory health gate", memory.context_window);
  assert(memory.context_window?.prompt && memory.context_window.estimated_tokens <= memory.context_window.max_tokens, "working-memory dogfood context window was not prompt-ready or budgeted", memory.context_window);
  const codeBackedFacts = (memory.facts || []).filter((fact) => isCodeSourceURL(fact.source_url));
  assert(codeBackedFacts.length === 0, "working-memory dogfood returned trusted facts extracted from code documents", {
    facts: codeBackedFacts.map((fact) => ({ id: fact.id, source_url: fact.source_url, claim_text: fact.claim_text })),
  });
  const health = await request(`/memory/health?scope=${encodeURIComponent(scope)}`);
  assert(Array.isArray(health.signals) && health.signals.length >= 1, "memory health did not return structured signals", health);
  for (const signal of health.signals) {
    assert(signal.code && signal.category && signal.severity && signal.message && signal.action, "memory health returned an incomplete structured signal", signal);
    assert(typeof signal.count === "number" && typeof signal.score_impact === "number", "memory health signal did not include numeric count and score impact", signal);
  }
  assert(
    health.claims?.trusted_from_code_documents === 0,
    "memory health detected trusted claims from code documents",
    health.claims
  );
  assert(
    health.learning?.duplicate_pending_groups === 0,
    "memory health detected duplicate pending learning proposals",
    health.learning
  );
  assert(health.ingestion?.stale_running_jobs === 0, "memory health detected stale running ingestion jobs", health.ingestion);
  assert(health.ingestion?.retry_jobs === 0, "memory health detected retrying ingestion jobs", health.ingestion);

  const graph = await request(`/graph/relations?scope=${encodeURIComponent(scope)}&limit=50`);
  const hasGoRelation = (graph.relations || []).some((relation) => {
    return relation.from_entity?.includes(".go") || relation.to_entity?.includes("go:") || relation.relation_type === "declares_package";
  });
  assert(hasGoRelation, "dogfood graph relation listing did not include Go code intelligence", graph);

  let finalSourceStatus = "active";
  if (!keepSourceActive) {
    const pauseApprovalId = await approvedRequest({
      action: "source_authority_change",
      targetType: "source_config",
      targetId: sourceConfigId,
      reason: "Pause dogfood source after eval so workers in other filesystem layouts do not keep retrying it.",
      payload: { source_config_id: sourceConfigId, status: "paused" },
    });
    await request("/sources/configs", {
      method: "POST",
      body: sourceConfigBody(pauseApprovalId, { id: sourceConfigId, status: "paused" }),
    });
    finalSourceStatus = "paused";
  }

  const output = {
    ok: true,
    ready,
    source_config_id: sourceConfigId,
    source_status: finalSourceStatus,
    ingestion_job: {
      id: job.id,
      status: job.status,
      documents_seen: job.documents_seen,
      documents_changed: job.documents_changed,
      documents_skipped: job.documents_skipped,
      chunks_written: job.chunks_written,
      claims_written: job.claims_written,
    },
    rebuild,
    health: {
      status: health.status,
      score: health.score,
      signals: health.signals?.length || 0,
      trusted_from_code_documents: health.claims?.trusted_from_code_documents,
      duplicate_pending_groups: health.learning?.duplicate_pending_groups,
      stale_running_jobs: health.ingestion?.stale_running_jobs,
      retry_jobs: health.ingestion?.retry_jobs,
    },
    memory: memorySummary,
  };
  console.log(JSON.stringify(output, null, 2));
}

main().catch((error) => {
  console.error(JSON.stringify({
    ok: false,
    error: error.message,
    details: error.details,
  }, null, 2));
  process.exit(1);
});
