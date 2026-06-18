#!/usr/bin/env node

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const startedAt = new Date().toISOString();
const stamp = startedAt.replace(/[^0-9A-Za-z]/g, "").slice(0, 24);
const scope = process.env.ABRA_PERF_SCOPE || `team:perf-${stamp}`;
const documentCount = numberEnv("ABRA_PERF_DOCS", 40);
const iterations = numberEnv("ABRA_PERF_ITERATIONS", 30);
const concurrency = numberEnv("ABRA_PERF_CONCURRENCY", 4);
const capacityIterations = numberEnv("ABRA_PERF_CAPACITY_ITERATIONS", Math.max(iterations * 2, 60));
const capacityConcurrency = numberEnv("ABRA_PERF_CAPACITY_CONCURRENCY", Math.max(concurrency * 2, 8));
const soakSeconds = nonNegativeNumberEnv("ABRA_PERF_SOAK_SECONDS", 0);
const soakConcurrency = numberEnv("ABRA_PERF_SOAK_CONCURRENCY", capacityConcurrency);
const recallP95MaxMs = numberEnv("ABRA_PERF_RECALL_P95_MS", 750);
const memoryP95MaxMs = numberEnv("ABRA_PERF_MEMORY_P95_MS", 2500);
const memoryCapacityP95MaxMs = numberEnv("ABRA_PERF_MEMORY_CAPACITY_P95_MS", memoryP95MaxMs * 2);
const memorySoakP95MaxMs = numberEnv("ABRA_PERF_MEMORY_SOAK_P95_MS", memoryCapacityP95MaxMs);
const maxFailureRate = decimalEnv("ABRA_PERF_MAX_FAILURE_RATE", 0);

const checks = [];

function numberEnv(name, fallback) {
  const value = Number(process.env[name] || fallback);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

function nonNegativeNumberEnv(name, fallback) {
  const value = Number(process.env[name] || fallback);
  return Number.isFinite(value) && value >= 0 ? value : fallback;
}

function decimalEnv(name, fallback) {
  const value = Number(process.env[name] || fallback);
  return Number.isFinite(value) && value >= 0 ? value : fallback;
}

async function request(path, { method = "GET", body, expectStatus = 200 } = {}) {
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
  return raw.trim() === "" ? {} : JSON.parse(raw);
}

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
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

function percentile(values, pct) {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil((pct / 100) * sorted.length) - 1));
  return sorted[index];
}

function stats(values) {
  if (values.length === 0) {
    return {
      count: 0,
      min_ms: 0,
      p50_ms: 0,
      p95_ms: 0,
      p99_ms: 0,
      max_ms: 0,
      avg_ms: 0
    };
  }
  return {
    count: values.length,
    min_ms: Math.min(...values),
    p50_ms: percentile(values, 50),
    p95_ms: percentile(values, 95),
    p99_ms: percentile(values, 99),
    max_ms: Math.max(...values),
    avg_ms: Math.round(values.reduce((sum, value) => sum + value, 0) / Math.max(1, values.length))
  };
}

async function measure(fn) {
  const before = performance.now();
  const result = await fn();
  return { duration_ms: Math.round(performance.now() - before), result };
}

async function runConcurrent(total, workers, fn) {
  const durations = [];
  const failures = [];
  const outputs = [];
  let next = 0;
  async function worker() {
    while (true) {
      const index = next++;
      if (index >= total) {
        return;
      }
      try {
        const measured = await measure(() => fn(index));
        durations.push(measured.duration_ms);
        outputs.push(measured.result);
      } catch (error) {
        failures.push(error instanceof Error ? error.message : String(error));
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(workers, total) }, () => worker()));
  return {
    attempted: total,
    durations,
    outputs,
    failures,
    failure_rate: failures.length / Math.max(1, total)
  };
}

async function runTimed(seconds, workers, fn) {
  const durations = [];
  const failures = [];
  const outputs = [];
  let next = 0;
  const started = performance.now();
  const deadline = started + seconds * 1000;
  async function worker() {
    while (performance.now() < deadline) {
      const index = next++;
      try {
        const measured = await measure(() => fn(index));
        durations.push(measured.duration_ms);
        outputs.push(measured.result);
      } catch (error) {
        failures.push(error instanceof Error ? error.message : String(error));
      }
    }
  }
  await Promise.all(Array.from({ length: workers }, () => worker()));
  const elapsedSeconds = Math.max(0.001, (performance.now() - started) / 1000);
  const attempted = durations.length + failures.length;
  return {
    attempted,
    durations,
    outputs,
    failures,
    failure_rate: failures.length / Math.max(1, attempted),
    elapsed_seconds: Number(elapsedSeconds.toFixed(3)),
    throughput_rps: Number((attempted / elapsedSeconds).toFixed(2))
  };
}

function assertFailureRate(result, label) {
  assert(
    result.failure_rate <= maxFailureRate,
    `${label} failure rate ${result.failure_rate.toFixed(4)} exceeded ${maxFailureRate}: ${result.failures.slice(0, 3).join("; ")}`
  );
}

function healthTrace(response) {
  return Array.isArray(response.retrieval_trace)
    ? response.retrieval_trace.find((item) => item.stage === "health" && item.operation === "memory_health_lookup")
    : undefined;
}

function cacheStatusSummary(responses) {
  const summary = {};
  for (const response of responses) {
    const status = healthTrace(response)?.cache_status || "missing";
    summary[status] = (summary[status] || 0) + 1;
  }
  return summary;
}

function perfDocument(index) {
  const module = `module-${index % 8}`;
  const component = `SharedComponent${index % 12}`;
  return {
    source_type: "markdown",
    source_url: `file://abra-perf-${stamp}-${index}.md`,
    source_id: `abra-perf-${stamp}-${index}`,
    title: `Abra Perf Fixture ${index}`,
    scope,
    content: [
      `- \`${module}\` should use Source Scoped Recall for bounded agent memory packet ${index}.`,
      `- Working Memory Compose must return Summary Evidence Graph Context Risk Guidance for \`${module}\`.`,
      `- Agent Workflow requires Approval Enforcement before broad memory writes in \`${module}\`.`,
      `- \`${module}\` uses \`${component}\` and validates release behavior with deterministic eval checks.`
    ].join("\n"),
    metadata: {
      authority: "perf-fixture",
      authority_score: 0.75,
      eval_tier: "performance",
      module
    }
  };
}

const queries = [
  "Source Scoped Recall bounded agent memory",
  "Working Memory Compose Summary Evidence Graph Context Risk Guidance",
  "Agent Workflow Approval Enforcement broad memory writes",
  "deterministic eval checks release behavior"
];

await runCheck("runtime_ready", async () => {
  const ready = await request("/readyz");
  assert(ready.ok === true, "readyz did not report ok=true");
  return {
    embedding_provider: ready.embedding_provider,
    approval_enforcement: ready.approval_enforcement === true,
    auth_required: ready.auth_required === true
  };
});

await runCheck("seed_performance_fixture_documents", async () => {
  const before = Date.now();
  let claims = 0;
  let chunks = 0;
  for (let index = 0; index < documentCount; index++) {
    const result = await request("/ingest/documents", {
      method: "POST",
      body: perfDocument(index)
    });
    assert(result.document_id, `document ${index} did not return document_id`);
    claims += Number(result.claims || 0);
    chunks += Number(result.chunks || 0);
  }
  assert(claims >= documentCount * 3, `fixture produced too few claims: ${claims}`);
  return {
    documents: documentCount,
    claims,
    chunks,
    seed_latency_ms: Date.now() - before
  };
});

await runCheck("warm_hot_paths", async () => {
  for (let index = 0; index < Math.min(4, queries.length); index++) {
    await request("/recall", {
      method: "POST",
      body: {
        query: queries[index],
        scope,
        limit: 8,
        include_unverified: false
      }
    });
    await request("/memory/compose", {
      method: "POST",
      body: {
        task: `implement ${queries[index]} for module-${index}`,
        scope,
        hook: "before_task",
        language: "typescript",
        agent: "abra-perf-eval",
        limit: 6,
        max_queries: 6,
        token_budget: 900,
        include_unverified: false
      }
    });
  }
  return { warmed_queries: Math.min(4, queries.length) };
});

await runCheck("recall_latency_gate", async () => {
  const result = await runConcurrent(iterations, concurrency, async (index) => {
    const response = await request("/recall", {
      method: "POST",
      body: {
        query: queries[index % queries.length],
        scope,
        limit: 8,
        include_unverified: false
      }
    });
    assert(response.retrieval_mode === "hybrid", `recall mode = ${response.retrieval_mode}, want hybrid`);
    assert(Array.isArray(response.claims) && response.claims.length >= 1, "recall returned no claims");
    assert(Array.isArray(response.supporting_documents) && response.supporting_documents.length >= 1, "recall returned no supporting documents");
    assert(
      response.claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score))),
      "recall claims missing text/vector score components"
    );
  });
  assertFailureRate(result, "recall");
  const latency = stats(result.durations);
  assert(latency.count > 0, "recall produced no successful latency samples");
  assert(latency.p95_ms <= recallP95MaxMs, `recall p95 ${latency.p95_ms}ms exceeded ${recallP95MaxMs}ms`);
  return {
    ...latency,
    p95_threshold_ms: recallP95MaxMs,
    attempted: result.attempted,
    concurrency,
    failures: result.failures.length,
    failure_rate: result.failure_rate
  };
});

await runCheck("working_memory_latency_gate", async () => {
  const result = await runConcurrent(iterations, concurrency, async (index) => {
    const response = await request("/memory/compose", {
      method: "POST",
      body: {
        task: `implement ${queries[index % queries.length]} for module-${index % 8}`,
        scope,
        hook: "before_task",
        language: "typescript",
        agent: "abra-perf-eval",
        limit: 6,
        max_queries: 6,
        include_unverified: false
      }
    });
    assert(response.intent, "working memory returned no intent");
    assert(response.retrieval_plan && response.retrieval_plan.mode, "working memory returned no retrieval plan");
    assert(Array.isArray(response.retrieval_trace) && response.retrieval_trace.length >= 1, "working memory returned no retrieval trace");
    assert(Array.isArray(response.summaries) && response.summaries.length >= 1, "working memory returned no summaries");
    assert(Array.isArray(response.facts) && response.facts.length >= 1, "working memory returned no facts");
    assert(Array.isArray(response.supporting_documents) && response.supporting_documents.length >= 1, "working memory returned no supporting documents");
    assert(Array.isArray(response.evidence) && response.evidence.length >= 1, "working memory returned no evidence");
    assert(response.verification && response.verification.verdict, "working memory returned no verification report");
    assert(response.retrieval_plan && response.retrieval_plan.coverage_targets, "working memory returned no retrieval coverage targets");
    assert(response.verification.retrieval_coverage && response.verification.retrieval_coverage.complete === true, "working memory did not satisfy retrieval coverage");
    assert(response.verification.retrieval_quality && response.verification.retrieval_quality.result_count >= 1, "working memory returned no retrieval quality report");
    assert(Array.isArray(response.agent_policy_decisions) && response.agent_policy_decisions.some((decision) => decision.action === "agent_write"), "working memory returned no agent policy decisions");
    assert(response.agent_decision && response.agent_decision.decision, "working memory returned no agent decision");
    assert(Array.isArray(response.impact_map) && response.impact_map.length >= 1, "working memory returned no impact map");
    assert(Array.isArray(response.validation_plan) && response.validation_plan.length >= 1, "working memory returned no validation plan");
    assert(response.context_window && Array.isArray(response.context_window.blocks) && response.context_window.blocks.length >= 1, "working memory returned no context window");
    assert(response.context_window.prompt && response.context_window.estimated_tokens > 0 && response.context_window.estimated_tokens <= response.context_window.max_tokens, "working memory returned invalid context budget");
    assert(Array.isArray(response.learning_suggestions) && response.learning_suggestions.length >= 1, "working memory returned no learning suggestions");
    assert(response.stats && response.stats.queries_run >= 1, "working memory returned no query stats");
    assert(response.stats.graph_queries >= 1, "working memory returned no graph query stats");
    assert(response.stats.graph_warnings === (Array.isArray(response.graph_warnings) ? response.graph_warnings.length : 0), "working memory returned inconsistent graph warning stats");
    assert(response.stats.impact_items === response.impact_map.length, "working memory returned inconsistent impact map stats");
    assert(response.stats.validation_steps === response.validation_plan.length, "working memory returned inconsistent validation plan stats");
    assert(response.stats.context_blocks === response.context_window.blocks.length, "working memory returned inconsistent context block stats");
    assert(response.stats.context_tokens === response.context_window.estimated_tokens, "working memory returned inconsistent context token stats");
    assert(response.stats.retrieval_trace_items === response.retrieval_trace.length, "working memory returned inconsistent retrieval trace stats");
    assert(response.stats.retrieval_warnings === (Array.isArray(response.retrieval_warnings) ? response.retrieval_warnings.length : 0), "working memory returned inconsistent retrieval warning stats");
    assert(response.stats.total_duration_ms >= 0, "working memory returned invalid duration stats");
    assert(response.retrieval_trace.every((item) => ["ok", "degraded"].includes(item.status)), "working memory returned trace stages without status");
    assert(response.retrieval_trace.some((item) => item.operation === "planned_summary_and_recall" && item.parallel === true), "working memory did not trace parallel recall");
    assert(response.retrieval_trace.some((item) => item.operation === "seed_graph_expansion" && item.parallel === true), "working memory did not trace parallel graph expansion");
    assert(["fresh", "cache_hit", "coalesced", "disabled"].includes(healthTrace(response)?.cache_status), "working memory did not trace memory-health cache status");
    return response;
  });
  assertFailureRate(result, "working-memory");
  const latency = stats(result.durations);
  assert(latency.count > 0, "working-memory produced no successful latency samples");
  assert(latency.p95_ms <= memoryP95MaxMs, `working-memory p95 ${latency.p95_ms}ms exceeded ${memoryP95MaxMs}ms`);
  return {
    ...latency,
    p95_threshold_ms: memoryP95MaxMs,
    attempted: result.attempted,
    concurrency,
    failures: result.failures.length,
    failure_rate: result.failure_rate,
    health_cache_statuses: cacheStatusSummary(result.outputs)
  };
});

await runCheck("working_memory_capacity_probe", async () => {
  const result = await runConcurrent(capacityIterations, capacityConcurrency, async (index) => {
    const response = await request("/memory/compose", {
      method: "POST",
      body: {
        task: `capacity probe ${queries[index % queries.length]} for module-${index % 8}`,
        scope,
        hook: "before_code",
        language: "typescript",
        agent: "abra-perf-capacity",
        limit: 6,
        max_queries: 6,
        token_budget: 900,
        include_unverified: false
      }
    });
    assert(response.agent_decision && response.agent_decision.decision, "capacity probe returned no agent decision");
    assert(response.stats && response.stats.total_duration_ms >= 0, "capacity probe returned no duration stats");
    assert(["fresh", "cache_hit", "coalesced", "disabled"].includes(healthTrace(response)?.cache_status), "capacity probe did not trace memory-health cache status");
    return response;
  });
  assertFailureRate(result, "working-memory capacity");
  const latency = stats(result.durations);
  assert(latency.count > 0, "working-memory capacity produced no successful latency samples");
  assert(latency.p95_ms <= memoryCapacityP95MaxMs, `working-memory capacity p95 ${latency.p95_ms}ms exceeded ${memoryCapacityP95MaxMs}ms`);
  const cacheStatuses = cacheStatusSummary(result.outputs);
  assert(
    (cacheStatuses.cache_hit || 0) + (cacheStatuses.coalesced || 0) + (cacheStatuses.disabled || 0) > 0,
    `capacity probe did not exercise cache reuse, coalescing, or disabled mode: ${JSON.stringify(cacheStatuses)}`
  );
  return {
    ...latency,
    p95_threshold_ms: memoryCapacityP95MaxMs,
    attempted: result.attempted,
    concurrency: capacityConcurrency,
    failures: result.failures.length,
    failure_rate: result.failure_rate,
    max_failure_rate: maxFailureRate,
    health_cache_statuses: cacheStatuses
  };
});

if (soakSeconds > 0) {
  await runCheck("working_memory_soak_probe", async () => {
    const result = await runTimed(soakSeconds, soakConcurrency, async (index) => {
      const response = await request("/memory/compose", {
        method: "POST",
        body: {
          task: `soak probe ${queries[index % queries.length]} for module-${index % 8}`,
          scope,
          hook: "before_code",
          language: "typescript",
          agent: "abra-perf-soak",
          limit: 6,
          max_queries: 6,
          token_budget: 900,
          include_unverified: false
        }
      });
      assert(response.agent_decision && response.agent_decision.decision, "soak probe returned no agent decision");
      assert(response.stats && response.stats.total_duration_ms >= 0, "soak probe returned no duration stats");
      assert(["fresh", "cache_hit", "coalesced", "disabled"].includes(healthTrace(response)?.cache_status), "soak probe did not trace memory-health cache status");
      return response;
    });
    assert(result.attempted > 0, "soak probe produced no samples");
    assertFailureRate(result, "working-memory soak");
    const latency = stats(result.durations);
    assert(latency.count > 0, "working-memory soak produced no successful latency samples");
    assert(latency.p95_ms <= memorySoakP95MaxMs, `working-memory soak p95 ${latency.p95_ms}ms exceeded ${memorySoakP95MaxMs}ms`);
    return {
      ...latency,
      p95_threshold_ms: memorySoakP95MaxMs,
      attempted: result.attempted,
      concurrency: soakConcurrency,
      duration_seconds: soakSeconds,
      elapsed_seconds: result.elapsed_seconds,
      throughput_rps: result.throughput_rps,
      failures: result.failures.length,
      failure_rate: result.failure_rate,
      max_failure_rate: maxFailureRate,
      health_cache_statuses: cacheStatusSummary(result.outputs)
    };
  });
}

const failed = checks.filter((check) => !check.ok);
const summary = {
  suite: "abra-local-performance",
  status: failed.length === 0 ? "passed" : "failed",
  started_at: startedAt,
  finished_at: new Date().toISOString(),
  checks,
  totals: {
    passed: checks.length - failed.length,
    failed: failed.length,
    total: checks.length
  },
  artifacts: {
    base_url: baseUrl,
    scope,
    document_count: documentCount,
    iterations,
    concurrency,
    capacity_iterations: capacityIterations,
    capacity_concurrency: capacityConcurrency,
    soak_seconds: soakSeconds,
    soak_concurrency: soakConcurrency,
    recall_p95_threshold_ms: recallP95MaxMs,
    memory_p95_threshold_ms: memoryP95MaxMs,
    memory_capacity_p95_threshold_ms: memoryCapacityP95MaxMs,
    memory_soak_p95_threshold_ms: memorySoakP95MaxMs,
    max_failure_rate: maxFailureRate
  }
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
