#!/usr/bin/env node

import { readFile } from "node:fs/promises";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const datasetPath = process.env.ABRA_GOLDEN_DATASET || "examples/evals/golden.jsonl";
const defaultScopeSuffix =
  process.env.ABRA_GOLDEN_SCOPE_SUFFIX ||
  new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
const defaultLimit = Number(process.env.ABRA_GOLDEN_LIMIT || "5");
const memoryMaxMs = Number(process.env.ABRA_GOLDEN_MEMORY_MAX_MS || "3500");
const startedAt = new Date().toISOString();
const checks = [];
const artifacts = { base_url: baseUrl, dataset: datasetPath };

requireTokenForRemoteBaseURL(baseUrl);

function requireTokenForRemoteBaseURL(rawBaseUrl) {
  const url = new URL(rawBaseUrl);
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(url.hostname);
  if (!loopback && !process.env.ABRA_API_TOKEN && process.env.ABRA_ALLOW_DEV_TOKEN !== "1") {
    throw new Error("ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments.");
  }
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

function materialize(value) {
  return String(value || "").replaceAll("{{run_id}}", defaultScopeSuffix);
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

function hitRate(ranks, cutoff) {
  if (ranks.length === 0) {
    return 0;
  }
  return ranks.filter((rank) => rank !== null && rank <= cutoff).length / ranks.length;
}

function requireText(payload, contains, label) {
  for (const value of contains || []) {
    assert(textOf(payload).includes(String(value).toLowerCase()), `${label} missing expected text ${JSON.stringify(value)}`);
  }
}

function forbidText(payload, contains, label) {
  for (const value of contains || []) {
    assert(!textOf(payload).includes(String(value).toLowerCase()), `${label} included forbidden text ${JSON.stringify(value)}`);
  }
}

const records = await readDataset(datasetPath);
const documents = records.filter((record) => (record.type || "case") === "document");
const cases = records.filter((record) => (record.type || "case") === "case");

await runCheck("runtime_ready", async () => {
  const ready = await request("/readyz");
  assert(ready.ok === true, "readyz did not report ok=true");
  artifacts.embedding_provider = ready.embedding_provider;
  return {
    embedding_provider: ready.embedding_provider,
    auth_required: ready.auth_required,
    approval_enforcement: ready.approval_enforcement
  };
});

await runCheck("dataset_loaded", async () => {
  assert(cases.length > 0, "golden dataset must contain at least one case record");
  return { records: records.length, documents: documents.length, cases: cases.length, run_id: defaultScopeSuffix };
});

await runCheck("seed_dataset_documents", async () => {
  const results = [];
  for (const document of documents) {
    for (const field of ["scope", "source_type", "source_url", "title", "content"]) {
      assert(document[field], `document ${document.id || document.source_url || "<unknown>"} missing ${field}`);
    }
    const ingest = await request("/ingest/documents", {
      method: "POST",
      body: {
        source_type: document.source_type,
        source_url: document.source_url,
        source_id: document.source_id || document.id || document.source_url,
        title: document.title,
        scope: document.scope,
        content: document.content,
        metadata: document.metadata || { authority: "eval-fixture", authority_score: 0.8 }
      }
    });
    results.push({
      id: document.id || document.source_url,
      document_id: ingest.document_id,
      claims: ingest.claims,
      relations: ingest.relations
    });
  }
  return { seeded: results.length, documents: results };
});

const recallRanks = [];
let verifiedWithoutCitation = 0;
let leakageCount = 0;
let memoryCases = 0;
const decisions = {};

await runCheck("golden_cases", async () => {
  const results = [];
  for (const item of cases) {
    assert(item.id, "case record missing id");
    assert(item.scope, `${item.id} missing scope`);
    assert(item.query, `${item.id} missing query`);
    const limit = Number(item.limit || defaultLimit);
    const recall = await request("/recall", {
      method: "POST",
      body: {
        query: item.query,
        scope: item.scope,
        limit,
        include_unverified: item.include_unverified === true
      }
    });
    const claims = Array.isArray(recall.claims) ? recall.claims : [];
    assert(recall.retrieval_mode === "hybrid", `${item.id} recall mode = ${recall.retrieval_mode}, want hybrid`);
    assert(
      claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score))),
      `${item.id} recall claims missing text/vector score components`
    );
    verifiedWithoutCitation += claims.filter((claim) => claim.status === "verified" && !claim.source_url).length;
    if (item.expected_claim_contains) {
      const rank = findRank(claims, item.expected_claim_contains, item.expected_source_url);
      recallRanks.push(rank);
      assert(rank !== null, `${item.id} did not retrieve expected claim ${JSON.stringify(item.expected_claim_contains)}`);
      assert(rank <= Number(item.min_rank || limit), `${item.id} expected rank <= ${item.min_rank || limit}, got ${rank}`);
    }
    if (item.expected_source_url) {
      assert(
        claims.some((claim) => claim.source_url === item.expected_source_url) ||
          (Array.isArray(recall.supporting_documents) &&
            recall.supporting_documents.some((document) => document.source_url === item.expected_source_url)),
        `${item.id} did not cite expected source ${item.expected_source_url}`
      );
    }
    if (item.must_not_contain && textOf(recall).includes(String(item.must_not_contain).toLowerCase())) {
      leakageCount++;
      throw new Error(`${item.id} leaked forbidden text ${JSON.stringify(item.must_not_contain)}`);
    }
    requireText(recall, item.expected_recall_contains, item.id);
    forbidText(recall, item.forbidden_recall_contains, item.id);

    let memory = null;
    let memoryLatency = null;
    if (item.memory_task) {
      memoryCases++;
      const before = Date.now();
      memory = await request("/memory/compose", {
        method: "POST",
        body: {
          task: item.memory_task,
          scope: item.scope,
          hook: item.hook || "before_task",
          agent: item.agent || "golden-eval",
          files: item.files || [],
          changed_files: item.changed_files || [],
          language: item.language || "",
          limit,
          max_queries: Number(item.max_queries || 6),
          token_budget: Number(item.token_budget || 900),
          include_unverified: item.include_unverified === true
        }
      });
      memoryLatency = Date.now() - before;
      assert(memoryLatency <= memoryMaxMs, `${item.id} memory latency ${memoryLatency}ms exceeded ${memoryMaxMs}ms`);
      assert(memory.verification && memory.verification.verdict, `${item.id} memory packet missing verification`);
      assert(memory.retrieval_plan && memory.retrieval_plan.coverage_targets, `${item.id} memory packet missing retrieval coverage targets`);
      assert(memory.verification.retrieval_coverage && memory.verification.retrieval_coverage.complete === true, `${item.id} memory packet did not satisfy retrieval coverage`);
      assert(memory.verification.retrieval_quality && memory.verification.retrieval_quality.result_count >= 1, `${item.id} memory packet missing retrieval quality`);
      assert(Array.isArray(memory.agent_policy_decisions), `${item.id} memory packet missing agent_policy_decisions`);
      assert(memory.agent_policy_decisions.some((decision) => decision.action === "agent_write"), `${item.id} memory packet missing agent-write policy decision`);
      assert(memory.agent_decision && memory.agent_decision.decision, `${item.id} memory packet missing agent_decision`);
      assert(Array.isArray(memory.impact_map) && memory.impact_map.length >= 1, `${item.id} memory packet missing impact_map`);
      assert(Array.isArray(memory.validation_plan) && memory.validation_plan.length >= 1, `${item.id} memory packet missing validation_plan`);
      assert(memory.context_window && Array.isArray(memory.context_window.blocks) && memory.context_window.blocks.length >= 1, `${item.id} memory packet missing context_window`);
      assert(memory.context_window.prompt && memory.context_window.estimated_tokens > 0 && memory.context_window.estimated_tokens <= memory.context_window.max_tokens, `${item.id} memory context_window was not prompt-ready or budgeted`);
      assert(memory.stats && memory.stats.impact_items === memory.impact_map.length, `${item.id} memory packet missing impact stats`);
      assert(memory.stats.validation_steps === memory.validation_plan.length, `${item.id} memory packet missing validation stats`);
      assert(memory.stats.context_blocks === memory.context_window.blocks.length, `${item.id} memory packet missing context block stats`);
      assert(memory.stats.context_tokens === memory.context_window.estimated_tokens, `${item.id} memory packet missing context token stats`);
      decisions[memory.agent_decision.decision] = (decisions[memory.agent_decision.decision] || 0) + 1;
      if (item.expected_memory_contains) {
        requireText(memory, Array.isArray(item.expected_memory_contains) ? item.expected_memory_contains : [item.expected_memory_contains], item.id);
      }
      if (item.expected_agent_decision) {
        const allowed = Array.isArray(item.expected_agent_decision)
          ? item.expected_agent_decision
          : [item.expected_agent_decision];
        assert(
          allowed.includes(memory.agent_decision.decision),
          `${item.id} expected agent decision ${allowed.join("|")}, got ${memory.agent_decision.decision}`
        );
      }
    }
    results.push({
      id: item.id,
      claims: claims.length,
      retrieval_mode: recall.retrieval_mode,
      supporting_documents: Array.isArray(recall.supporting_documents) ? recall.supporting_documents.length : 0,
      graph_relations: Array.isArray(recall.graph_context) ? recall.graph_context.length : 0,
      memory_latency_ms: memoryLatency,
      agent_decision: memory ? memory.agent_decision.decision : undefined
    });
  }
  assert(verifiedWithoutCitation === 0, `${verifiedWithoutCitation} verified claims were missing source_url`);
  return { cases: results };
});

const failed = checks.filter((check) => !check.ok);
const summary = {
  suite: "abra-golden-eval",
  status: failed.length === 0 ? "passed" : "failed",
  started_at: startedAt,
  finished_at: new Date().toISOString(),
  checks,
  totals: {
    passed: checks.filter((check) => check.ok).length,
    failed: failed.length,
    total: checks.length
  },
  metrics: {
    recall_cases_with_expected_claim: recallRanks.length,
    hit_rate_at_1: hitRate(recallRanks, 1),
    hit_rate_at_3: hitRate(recallRanks, 3),
    hit_rate_at_5: hitRate(recallRanks, 5),
    verified_without_citation: verifiedWithoutCitation,
    leakage_count: leakageCount,
    memory_cases: memoryCases,
    agent_decisions: decisions
  },
  artifacts
};

console.log(JSON.stringify(summary, null, 2));
if (failed.length > 0) {
  process.exitCode = 1;
}
