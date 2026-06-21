#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { createServer } from "node:http";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const datasetPath = process.env.ABRA_GOLDEN_DATASET || "examples/evals/golden.jsonl";
const defaultScopeSuffix =
  process.env.ABRA_GOLDEN_SCOPE_SUFFIX ||
  new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
const defaultLimit = Number(process.env.ABRA_GOLDEN_LIMIT || "5");
const memoryMaxMs = Number(process.env.ABRA_GOLDEN_MEMORY_MAX_MS || "3500");
const requestTimeoutMs = Number(process.env.ABRA_GOLDEN_REQUEST_TIMEOUT_MS || "30000");
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

function connectorFixtureHost(address) {
  if (process.env.ABRA_GOLDEN_MCP_HOST) {
    return process.env.ABRA_GOLDEN_MCP_HOST;
  }
  const apiHost = new URL(baseUrl).hostname;
  if (["127.0.0.1", "localhost", "::1", "[::1]"].includes(apiHost)) {
    return "host.docker.internal";
  }
  return address.address;
}

async function request(path, { method = "GET", body, expectStatus = 200 } = {}) {
	const response = await fetch(`${baseUrl}${path}`, {
		method,
		signal: AbortSignal.timeout(requestTimeoutMs),
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

function materializeValue(value) {
  if (typeof value === "string") {
    return materialize(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => materializeValue(item));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, materializeValue(item)]));
  }
  return value;
}

function scopedRecord(record) {
  return materializeValue(structuredClone(record));
}

function valuesOf(value) {
  if (value === undefined || value === null) {
    return [];
  }
  return Array.isArray(value) ? value : [value];
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
  for (const value of valuesOf(contains)) {
    assert(textOf(payload).includes(String(value).toLowerCase()), `${label} missing expected text ${JSON.stringify(value)}`);
  }
}

function forbidText(payload, contains, label) {
  for (const value of valuesOf(contains)) {
    assert(!textOf(payload).includes(String(value).toLowerCase()), `${label} included forbidden text ${JSON.stringify(value)}`);
  }
}

function citationsForSource(payload, sourceUrl) {
  return (Array.isArray(payload.citations) ? payload.citations : []).filter(
    (citation) => citation.source_url === sourceUrl || citation.url === sourceUrl
  );
}

function sourceWasCited(payload, sourceUrl) {
  return (
    (Array.isArray(payload.claims) && payload.claims.some((claim) => claim.source_url === sourceUrl)) ||
    (Array.isArray(payload.facts) && payload.facts.some((claim) => claim.source_url === sourceUrl)) ||
    (Array.isArray(payload.supporting_documents) && payload.supporting_documents.some((document) => document.source_url === sourceUrl)) ||
    (Array.isArray(payload.evidence) && payload.evidence.some((item) => item.source_url === sourceUrl)) ||
    citationsForSource(payload, sourceUrl).length > 0
  );
}

function citationRefsForSource(payload, sourceUrl) {
  return citationsForSource(payload, sourceUrl).map((citation) => String(citation.ref || "")).filter(Boolean);
}

function answerCitesRef(answer, ref) {
  return String(answer || "").includes(`[${ref}]`);
}

function gapCodes(payload) {
  return Array.isArray(payload.gaps) ? payload.gaps.map((gap) => String(gap.code || "")) : [];
}

function allowedDecisionValues(item, specificField) {
  return valuesOf(item[specificField] ?? item.expected_agent_decision).map(String);
}

const records = await readDataset(datasetPath);
const documents = records.filter((record) => (record.type || "case") === "document");
const cases = records.filter((record) => (record.type || "case") === "case");
const connectorCases = records.filter((record) => record.type === "connector");

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

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolve(server.address());
    });
  });
}

function closeServer(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

function readRequestJSON(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.setEncoding("utf8");
    req.on("data", (chunk) => {
      raw += chunk;
    });
    req.on("end", () => {
      try {
        resolve(raw.trim() === "" ? {} : JSON.parse(raw));
      } catch (error) {
        reject(error);
      }
    });
    req.on("error", reject);
  });
}

function createConnectorFixtureServer(connector) {
  const calls = [];
  const documents = valuesOf(connector.documents).map((document) => materializeValue(document));
  const server = createServer(async (req, res) => {
    try {
      const body = await readRequestJSON(req);
      calls.push(body);
      const tool = body && body.params && body.params.name;
      if (connector.expected_tool && tool !== connector.expected_tool) {
        res.writeHead(400, { "content-type": "application/json" });
        res.end(JSON.stringify({ jsonrpc: "2.0", id: body.id, error: { code: -32602, message: `unexpected tool ${tool}` } }));
        return;
      }
      res.writeHead(200, { "content-type": "application/json" });
      res.end(
        JSON.stringify({
          jsonrpc: "2.0",
          id: body.id,
          result: {
            structuredContent: { documents }
          }
        })
      );
    } catch (error) {
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: error instanceof Error ? error.message : String(error) }));
    }
  });
  return { server, calls };
}

await runCheck("connector_control_plane", async () => {
  if (connectorCases.length === 0) {
    return { skipped: true, reason: "no connector records" };
  }
  const results = [];
  for (const connector of connectorCases) {
    assert(connector.id, "connector record missing id");
    assert(connector.scope, `${connector.id} missing scope`);
    assert(connector.expected_tool, `${connector.id} missing expected_tool`);
    assert(connector.documents, `${connector.id} missing documents`);
    const { server, calls } = createConnectorFixtureServer(connector);
    const address = await listen(server);
    const mcpUrl = `http://${connectorFixtureHost(address)}:${address.port}/mcp`;
    try {
      const sourceConfig = {
        id: connector.source_config_id || `${connector.id}-source`,
        scope: connector.scope,
        source_type: "mcp",
        name: connector.name || connector.id,
        base_url: mcpUrl,
        connector_kind: connector.connector_kind || "mcp",
        status: connector.status || "paused",
        authority: connector.authority || "connector-fixture",
        authority_score: Number(connector.authority_score ?? 0.7),
        schedule_cron: connector.schedule_cron || "",
        config: {
          tool: connector.expected_tool,
          arguments: connector.arguments || {},
          document_source_type: connector.document_source_type || connector.connector_kind || "mcp"
        },
        metadata: {
          eval_dataset: "golden",
          connector_model: "user_owned_mcp"
        },
        created_by: "golden-eval"
      };
      const validated = await request("/sources/configs/validate", {
        method: "POST",
        body: sourceConfig
      });
      assert(validated.status === "ok", `${connector.id} connector validation status = ${validated.status}`);
      assert(validated.count === valuesOf(connector.documents).length, `${connector.id} connector validation count mismatch`);
      requireText(validated, connector.expected_validate_contains, `${connector.id} connector validation`);
      assert(calls.length >= 1, `${connector.id} did not call MCP fixture`);

      const registered = await request("/sources/configs", {
        method: "POST",
        body: sourceConfig
      });
      assert(registered.source_config_id === sourceConfig.id, `${connector.id} registered source id mismatch`);
      assert(registered.status === "upserted", `${connector.id} register status = ${registered.status}`);

      const listed = await request(`/sources/configs?scope=${encodeURIComponent(connector.scope)}&limit=20`);
      const configs = Array.isArray(listed.source_configs) ? listed.source_configs : [];
      const stored = configs.find((item) => item.id === sourceConfig.id);
      assert(stored, `${connector.id} registered connector source was not listable`);
      assert(stored.status === sourceConfig.status, `${connector.id} source status = ${stored.status}`);
      assert(stored.source_type === "mcp", `${connector.id} source type = ${stored.source_type}`);
      assert(stored.connector_kind === sourceConfig.connector_kind, `${connector.id} connector kind mismatch`);
      results.push({
        id: connector.id,
        source_config_id: sourceConfig.id,
        status: stored.status,
        validated_documents: validated.count,
        mcp_calls: calls.length
      });
    } finally {
      await closeServer(server);
    }
  }
  return { connectors: results };
});

const recallRanks = [];
let verifiedWithoutCitation = 0;
let leakageCount = 0;
let memoryCases = 0;
let sourceBackedMemoryCases = 0;
let trackedStaleMemoryMentions = 0;
let thinkCases = 0;
let thinkCitationHits = 0;
let thinkForbiddenLeaks = 0;
const decisions = {};
const thinkDecisions = {};

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
    let staleMemoryMentioned = false;
    let think = null;
    let thinkLatency = null;
    let thinkSourceCited = false;
    let thinkHasCitationRef = false;
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
        requireText(memory, item.expected_memory_contains, item.id);
      }
      forbidText(memory, item.forbidden_memory_contains, item.id);
      if (item.expected_memory_source_url) {
        sourceBackedMemoryCases++;
        assert(
          sourceWasCited(memory, item.expected_memory_source_url),
          `${item.id} memory packet did not cite expected source ${item.expected_memory_source_url}`
        );
      }
      staleMemoryMentioned =
        valuesOf(item.tracked_stale_memory_contains).some((value) => textOf(memory).includes(String(value).toLowerCase()));
      if (staleMemoryMentioned) {
        trackedStaleMemoryMentions++;
      }
      {
        const allowed = allowedDecisionValues(item, "expected_memory_agent_decision");
        if (allowed.length > 0) {
          assert(
            allowed.includes(memory.agent_decision.decision),
            `${item.id} expected memory agent decision ${allowed.join("|")}, got ${memory.agent_decision.decision}`
          );
        }
      }
    }
    if (
      item.expected_think_contains ||
      item.expected_think_source_url ||
      item.forbidden_think_contains ||
      item.expected_think_gap_codes ||
      item.expected_think_agent_decision ||
      item.expected_think_answer_decision_text
    ) {
      thinkCases++;
      const before = Date.now();
      think = await request("/brain/think", {
        method: "POST",
        body: {
          question: item.think_question || item.query,
          scope: item.scope,
          agent: item.agent || "golden-eval",
          limit,
          max_queries: Number(item.max_queries || 6),
          token_budget: Number(item.token_budget || 900),
          include_unverified: item.include_unverified === true
        }
      });
      thinkLatency = Date.now() - before;
      assert(thinkLatency <= memoryMaxMs, `${item.id} brain_think latency ${thinkLatency}ms exceeded ${memoryMaxMs}ms`);
      assert(think.answer && typeof think.answer === "string", `${item.id} brain_think missing answer`);
      assert(Array.isArray(think.citations), `${item.id} brain_think missing citations array`);
      assert(Array.isArray(think.gaps), `${item.id} brain_think missing gaps array`);
      assert(think.agent_decision && think.agent_decision.decision, `${item.id} brain_think missing agent decision`);
      assert(think.verification && think.verification.verdict, `${item.id} brain_think missing verification`);
      thinkDecisions[think.agent_decision.decision] = (thinkDecisions[think.agent_decision.decision] || 0) + 1;
      requireText(think.answer, item.expected_think_contains, `${item.id} brain_think answer`);
      try {
        forbidText(think.answer, item.forbidden_think_contains, `${item.id} brain_think answer`);
      } catch (error) {
        thinkForbiddenLeaks++;
        throw error;
      }
      if (item.expected_think_source_url) {
        thinkSourceCited = sourceWasCited(think, item.expected_think_source_url);
        assert(thinkSourceCited, `${item.id} brain_think did not cite expected source ${item.expected_think_source_url}`);
        const citedRefs = citationRefsForSource(think, item.expected_think_source_url);
        assert(
          citedRefs.some((ref) => answerCitesRef(think.answer, ref)),
          `${item.id} brain_think answer did not include citation ref for ${item.expected_think_source_url}`
        );
        thinkCitationHits++;
      }
      thinkHasCitationRef = /\[C\d+\]/.test(think.answer);
      assert(!item.expected_think_source_url || thinkHasCitationRef, `${item.id} brain_think answer did not include citation refs`);
      if (item.expected_think_gap_codes) {
        const actualGapCodes = gapCodes(think);
        for (const expected of valuesOf(item.expected_think_gap_codes)) {
          assert(actualGapCodes.includes(expected), `${item.id} brain_think missing expected gap code ${expected}`);
        }
      }
      {
        const allowed = allowedDecisionValues(item, "expected_think_agent_decision");
        if (allowed.length > 0) {
          assert(
            allowed.includes(think.agent_decision.decision),
            `${item.id} brain_think expected agent decision ${allowed.join("|")}, got ${think.agent_decision.decision}`
          );
        }
      }
      if (item.expected_think_answer_decision_text) {
        assert(
          String(think.answer).includes(`Decision gate: ${think.agent_decision.decision}.`),
          `${item.id} brain_think answer missing decision gate text for ${think.agent_decision.decision}`
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
      agent_decision: memory ? memory.agent_decision.decision : undefined,
      memory_source_cited: item.expected_memory_source_url && memory ? sourceWasCited(memory, item.expected_memory_source_url) : undefined,
      tracked_stale_memory_mentioned: staleMemoryMentioned || undefined,
      think_latency_ms: thinkLatency,
      think_agent_decision: think ? think.agent_decision.decision : undefined,
      think_source_cited: item.expected_think_source_url && think ? thinkSourceCited : undefined,
      think_answer_citation_ref: think ? thinkHasCitationRef : undefined
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
    source_backed_memory_cases: sourceBackedMemoryCases,
    tracked_stale_memory_mentions: trackedStaleMemoryMentions,
    think_cases: thinkCases,
    think_citation_hits: thinkCitationHits,
    think_forbidden_leaks: thinkForbiddenLeaks,
    agent_decisions: decisions,
    think_agent_decisions: thinkDecisions
  },
  artifacts
};

console.log(JSON.stringify(summary, null, 2));
if (failed.length > 0) {
  process.exitCode = 1;
}
