#!/usr/bin/env node

import { readFile } from "node:fs/promises";

import { assertHybridRetrievalMode } from "./lib/eval-contracts.mjs";
import { createMCPToolCaller } from "./lib/mcp.mjs";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const datasetPath = process.env.ABRA_PROVIDER_DATASET || process.env.ABRA_GOLDEN_DATASET || "examples/evals/golden.jsonl";
const runID =
  process.env.ABRA_PROVIDER_RUN_ID ||
  new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
const defaultLimit = numberEnv("ABRA_PROVIDER_LIMIT", 5);
const recallP95MaxMs = numberEnv("ABRA_PROVIDER_RECALL_P95_MS", 750);
const memoryP95MaxMs = numberEnv("ABRA_PROVIDER_MEMORY_P95_MS", 3500);
const minHitRateAt3 = decimalEnv("ABRA_PROVIDER_MIN_HIT_RATE_AT_3", 1);
const minCitationCoverage = decimalEnv("ABRA_PROVIDER_MIN_CITATION_COVERAGE", 1);
const maxLeakageCount = numberEnv("ABRA_PROVIDER_MAX_LEAKAGE_COUNT", 0, true);
const expectedProvider = (process.env.ABRA_PROVIDER_EXPECT || "").trim();
const startedAt = new Date().toISOString();
const checks = [];
const recallLatencies = [];
const memoryLatencies = [];
const recallRanks = [];
const caseResults = [];
const memoryVerdicts = {};
const agentDecisions = {};
let ready = {};
let expectedCitationCases = 0;
let citationHits = 0;
let leakageCount = 0;
let verifiedWithoutCitation = 0;
const mcpTool = createMCPToolCaller({ baseUrl, token });

requireTokenForRemoteBaseURL(baseUrl);

function requireTokenForRemoteBaseURL(rawBaseUrl) {
  const url = new URL(rawBaseUrl);
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(url.hostname);
  if (!loopback && !process.env.ABRA_API_TOKEN && process.env.ABRA_ALLOW_DEV_TOKEN !== "1") {
    throw new Error("ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments.");
  }
}

function numberEnv(name, fallback, allowZero = false) {
  const value = Number(process.env[name] ?? fallback);
  return Number.isFinite(value) && (allowZero ? value >= 0 : value > 0) ? value : fallback;
}

function decimalEnv(name, fallback) {
  const value = Number(process.env[name] ?? fallback);
  return Number.isFinite(value) && value >= 0 ? value : fallback;
}

async function request(path, { method = "GET", body, expectStatus = 200 } = {}) {
  if (method === "POST" && path === "working_memory_compose") {
    return mcpTool("working_memory_compose", body || {});
  }
  if (method === "POST" && path === "brain_think") {
    return mcpTool("brain_think", body || {});
  }
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

function materialize(value) {
  return String(value || "").replaceAll("{{run_id}}", runID);
}

function scopedRecord(record) {
  const out = structuredClone(record);
  for (const key of ["scope", "source_url", "source_id", "expected_source_url", "title", "query", "memory_task"]) {
    if (out[key] !== undefined) {
      out[key] = materialize(out[key]);
    }
  }
  return out;
}

async function readDataset(path) {
  const raw = await readFile(path, "utf8");
  const records = [];
  for (const [index, line] of raw.split(/\r?\n/).entries()) {
    const trimmed = line.trim();
    if (trimmed === "" || trimmed.startsWith("#")) {
      continue;
    }
    try {
      records.push(scopedRecord(JSON.parse(trimmed)));
    } catch (error) {
      throw new Error(`${path}:${index + 1} invalid JSONL record: ${error.message}`);
    }
  }
  return records;
}

function textOf(value) {
  return JSON.stringify(value).toLowerCase();
}

function findRank(items, contains, sourceUrl) {
  const needle = String(contains || "").toLowerCase();
  const index = items.findIndex((item) => {
    const haystack = String(item.claim_text || item.content || item.title || "").toLowerCase();
    return haystack.includes(needle) && (!sourceUrl || item.source_url === sourceUrl);
  });
  return index === -1 ? null : index + 1;
}

function percentile(values, pct) {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil((pct / 100) * sorted.length) - 1));
  return sorted[index];
}

function latencyStats(values) {
  if (values.length === 0) {
    return { count: 0, p50_ms: 0, p95_ms: 0, p99_ms: 0, avg_ms: 0, max_ms: 0 };
  }
  return {
    count: values.length,
    p50_ms: percentile(values, 50),
    p95_ms: percentile(values, 95),
    p99_ms: percentile(values, 99),
    avg_ms: Math.round(values.reduce((sum, value) => sum + value, 0) / values.length),
    max_ms: Math.max(...values)
  };
}

function hitRate(ranks, cutoff) {
  if (ranks.length === 0) {
    return 0;
  }
  return ranks.filter((rank) => rank !== null && rank <= cutoff).length / ranks.length;
}

function sourceWasCited(recall, sourceURL) {
  return (
    (Array.isArray(recall.claims) && recall.claims.some((claim) => claim.source_url === sourceURL)) ||
    (Array.isArray(recall.supporting_documents) && recall.supporting_documents.some((document) => document.source_url === sourceURL))
  );
}

const records = await readDataset(datasetPath);
const documents = records.filter((record) => (record.type || "case") === "document");
const cases = records.filter((record) => (record.type || "case") === "case");

await runCheck("runtime_ready", async () => {
  ready = await request("/readyz");
  assert(ready.ok === true, "readyz did not report ok=true");
  if (expectedProvider !== "") {
    assert(ready.embedding_provider === expectedProvider, `embedding provider = ${ready.embedding_provider}, want ${expectedProvider}`);
  }
  return {
    embedding_provider: ready.embedding_provider,
    expected_provider: expectedProvider || undefined,
    auth_required: ready.auth_required === true
  };
});

await runCheck("dataset_loaded", async () => {
  assert(cases.length > 0, "provider benchmark dataset must contain at least one case");
  return {
    dataset: datasetPath,
    run_id: runID,
    records: records.length,
    documents: documents.length,
    cases: cases.length
  };
});

await runCheck("seed_provider_benchmark_documents", async () => {
  const seeded = [];
  for (const document of documents) {
    for (const field of ["scope", "source_type", "source_url", "title", "content"]) {
      assert(document[field], `document ${document.id || document.source_url || "<unknown>"} missing ${field}`);
    }
    const started = Date.now();
    const ingest = await request("/ingest/documents", {
      method: "POST",
      body: {
        source_type: document.source_type,
        source_url: document.source_url,
        source_id: document.source_id || document.id || document.source_url,
        title: document.title,
        scope: document.scope,
        content: document.content,
        metadata: document.metadata || { authority: "provider-benchmark", authority_score: 0.8 }
      }
    });
    seeded.push({
      id: document.id || document.source_url,
      document_id: ingest.document_id,
      claims: ingest.claims || 0,
      chunks: ingest.chunks || 0,
      relations: ingest.relations || 0,
      latency_ms: Date.now() - started
    });
  }
  return { seeded: seeded.length, documents: seeded };
});

await runCheck("provider_quality_cases", async () => {
  for (const item of cases) {
    const limit = Number(item.limit || defaultLimit);
    const recallStarted = Date.now();
    const recall = await request("/recall", {
      method: "POST",
      body: {
        query: item.query,
        scope: item.scope,
        limit,
        include_unverified: item.include_unverified === true
      }
    });
    const recallLatency = Date.now() - recallStarted;
    recallLatencies.push(recallLatency);
    const claims = Array.isArray(recall.claims) ? recall.claims : [];
    assertHybridRetrievalMode(recall.retrieval_mode, `${item.id} recall`);
    assert(
      claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score))),
      `${item.id} recall claims missing text/vector score components`
    );
    verifiedWithoutCitation += claims.filter((claim) => claim.status === "verified" && !claim.source_url).length;
    let rank = null;
    if (item.expected_claim_contains) {
      rank = findRank(claims, item.expected_claim_contains, item.expected_source_url);
      recallRanks.push(rank);
    }
    if (item.expected_source_url) {
      expectedCitationCases++;
      if (sourceWasCited(recall, item.expected_source_url)) {
        citationHits++;
      }
    }
    if (item.must_not_contain && textOf(recall).includes(String(item.must_not_contain).toLowerCase())) {
      leakageCount++;
    }

    let memoryLatency = null;
    let memoryVerdict = null;
    let agentDecision = null;
    if (item.memory_task) {
      const memoryStarted = Date.now();
      const memory = await request("working_memory_compose", {
        method: "POST",
        body: {
          task: item.memory_task,
          scope: item.scope,
          hook: item.hook || "before_task",
          agent: item.agent || "provider-benchmark",
          files: item.files || [],
          changed_files: item.changed_files || [],
          language: item.language || "",
          limit,
          max_queries: Number(item.max_queries || 6),
          token_budget: Number(item.token_budget || 900),
          include_unverified: item.include_unverified === true
        }
      });
      memoryLatency = Date.now() - memoryStarted;
      memoryLatencies.push(memoryLatency);
      assert(memory.verification && memory.verification.verdict, `${item.id} memory packet missing verification`);
      assert(memory.retrieval_plan && memory.retrieval_plan.coverage_targets, `${item.id} memory packet missing retrieval coverage targets`);
      assert(memory.verification.retrieval_coverage && memory.verification.retrieval_coverage.complete === true, `${item.id} memory packet did not satisfy retrieval coverage`);
      assert(memory.agent_decision && memory.agent_decision.decision, `${item.id} memory packet missing agent decision`);
      assert(memory.context_window && memory.context_window.estimated_tokens <= memory.context_window.max_tokens, `${item.id} memory context exceeded budget`);
      memoryVerdict = memory.verification.verdict;
      agentDecision = memory.agent_decision.decision;
      memoryVerdicts[memoryVerdict] = (memoryVerdicts[memoryVerdict] || 0) + 1;
      agentDecisions[agentDecision] = (agentDecisions[agentDecision] || 0) + 1;
    }

    caseResults.push({
      id: item.id,
      rank,
      recall_latency_ms: recallLatency,
      claims: claims.length,
      supporting_documents: Array.isArray(recall.supporting_documents) ? recall.supporting_documents.length : 0,
      source_cited: item.expected_source_url ? sourceWasCited(recall, item.expected_source_url) : undefined,
      memory_latency_ms: memoryLatency,
      memory_verdict: memoryVerdict,
      agent_decision: agentDecision
    });
  }
  return { cases: caseResults };
});

await runCheck("provider_quality_thresholds", async () => {
  const recall = latencyStats(recallLatencies);
  const memory = latencyStats(memoryLatencies);
  const hitRateAt3 = hitRate(recallRanks, 3);
  const citationCoverage = expectedCitationCases === 0 ? 1 : citationHits / expectedCitationCases;
  assert(hitRateAt3 >= minHitRateAt3, `hit_rate_at_3 ${hitRateAt3} below ${minHitRateAt3}`);
  assert(citationCoverage >= minCitationCoverage, `citation coverage ${citationCoverage} below ${minCitationCoverage}`);
  assert(leakageCount <= maxLeakageCount, `leakage count ${leakageCount} exceeded ${maxLeakageCount}`);
  assert(verifiedWithoutCitation === 0, `${verifiedWithoutCitation} verified claims were missing citation source_url`);
  assert(recall.p95_ms <= recallP95MaxMs, `recall p95 ${recall.p95_ms}ms exceeded ${recallP95MaxMs}ms`);
  assert(memory.count === 0 || memory.p95_ms <= memoryP95MaxMs, `memory p95 ${memory.p95_ms}ms exceeded ${memoryP95MaxMs}ms`);
  return {
    hit_rate_at_3: hitRateAt3,
    min_hit_rate_at_3: minHitRateAt3,
    citation_coverage: citationCoverage,
    min_citation_coverage: minCitationCoverage,
    leakage_count: leakageCount,
    max_leakage_count: maxLeakageCount,
    verified_without_citation: verifiedWithoutCitation,
    recall_p95_ms: recall.p95_ms,
    recall_p95_threshold_ms: recallP95MaxMs,
    memory_p95_ms: memory.p95_ms,
    memory_p95_threshold_ms: memoryP95MaxMs
  };
});

const failed = checks.filter((check) => !check.ok);
const summary = {
  suite: "abra-provider-benchmark",
  status: failed.length === 0 ? "passed" : "failed",
  started_at: startedAt,
  finished_at: new Date().toISOString(),
  checks,
  totals: {
    passed: checks.length - failed.length,
    failed: failed.length,
    total: checks.length
  },
  metrics: {
    embedding_provider: ready.embedding_provider,
    dataset: datasetPath,
    run_id: runID,
    cases: caseResults.length,
    expected_claim_cases: recallRanks.length,
    hit_rate_at_1: hitRate(recallRanks, 1),
    hit_rate_at_3: hitRate(recallRanks, 3),
    hit_rate_at_5: hitRate(recallRanks, 5),
    citation_coverage: expectedCitationCases === 0 ? 1 : citationHits / expectedCitationCases,
    leakage_count: leakageCount,
    verified_without_citation: verifiedWithoutCitation,
    recall_latency: latencyStats(recallLatencies),
    memory_latency: latencyStats(memoryLatencies),
    memory_verdicts: memoryVerdicts,
    agent_decisions: agentDecisions
  },
  artifacts: {
    base_url: baseUrl,
    dataset: datasetPath,
    run_id: runID,
    expected_provider: expectedProvider || undefined
  }
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
