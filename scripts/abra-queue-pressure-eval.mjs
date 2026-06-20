#!/usr/bin/env node

import { createHmac, randomBytes } from "node:crypto";

const baseUrl = (process.env.ABRA_BASE_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const token = process.env.ABRA_API_TOKEN || "dev-token";
const webhookSecret = process.env.ABRA_WEBHOOK_SECRET || "dev-webhook-secret";
const runId = process.env.ABRA_QUEUE_PRESSURE_RUN_ID || randomBytes(6).toString("hex");
const scope = process.env.ABRA_QUEUE_PRESSURE_SCOPE || `repo:abra-queue-pressure-${runId}`;
const batches = boundedInt("ABRA_QUEUE_PRESSURE_BATCHES", 2, 1, 10);
const docsPerBatch = boundedInt("ABRA_QUEUE_PRESSURE_DOCS_PER_BATCH", 4, 1, 50);
const timeoutMs = boundedInt("ABRA_QUEUE_PRESSURE_TIMEOUT_MS", 180_000, 10_000, 1_800_000);
const pollMs = boundedInt("ABRA_QUEUE_PRESSURE_POLL_MS", 1_000, 250, 30_000);
const maxDrainMs = boundedInt("ABRA_QUEUE_PRESSURE_MAX_DRAIN_MS", 180_000, 1_000, 1_800_000);
const maxQueueWaitMs = boundedInt("ABRA_QUEUE_PRESSURE_MAX_QUEUE_WAIT_MS", 90_000, 1_000, 1_800_000);
const totalDocs = batches * docsPerBatch;

requireTokenForRemoteBaseURL(baseUrl);

function boundedInt(name, fallback, min, max) {
  const value = Number(process.env[name] || fallback);
  if (!Number.isFinite(value) || value < min || value > max) {
    return fallback;
  }
  return Math.trunc(value);
}

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

function signature(body) {
  return `sha256=${createHmac("sha256", webhookSecret).update(body).digest("hex")}`;
}

async function request(route, { method = "GET", body, signed = false } = {}) {
  const headers = {
    authorization: `Bearer ${token}`,
  };
  const options = { method, headers };
  if (body !== undefined) {
    const raw = JSON.stringify(body);
    headers["content-type"] = "application/json";
    if (signed) {
      headers["x-abra-signature"] = signature(raw);
    }
    options.body = raw;
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

function buildDocuments(batchIndex) {
  return Array.from({ length: docsPerBatch }, (_, index) => {
    const globalIndex = batchIndex * docsPerBatch + index;
    return {
      scope,
      source_type: "queue_pressure",
      source_url: `https://queue-pressure.example.invalid/${runId}/doc-${globalIndex}.md`,
      source_id: `queue-pressure-${runId}-${globalIndex}`,
      title: `Queue pressure fixture ${globalIndex}`,
      content: [
        `# Queue pressure fixture ${globalIndex}`,
        "",
        `Run ${runId} validates Abra worker backlog drain under signed webhook ingestion.`,
        `The unique pressure marker is abra-queue-pressure-${runId}-${globalIndex}.`,
        "This document is intentionally short so the gate measures queue drain and indexing behavior, not large-file chunk splitting."
      ].join("\n"),
      authority: "eval-fixture",
      authority_score: 0.55,
      metadata: {
        eval: "queue_pressure",
        run_id: runId,
        batch: batchIndex,
        index: globalIndex
      }
    };
  });
}

async function submitBatch(batchIndex) {
  const body = {
    connector_kind: "queue_pressure",
    event_type: "batch.created",
    delivery_id: `queue-pressure-${runId}-${batchIndex}`,
    documents: buildDocuments(batchIndex),
    metadata: {
      eval: "queue_pressure",
      run_id: runId,
      batch: batchIndex
    }
  };
  const response = await request("/ingest/webhooks", { method: "POST", body, signed: true });
  assert(response.accepted === docsPerBatch, "webhook batch did not accept every document", response);
  const jobIds = (response.documents || []).map((document) => document.ingestion_job_id).filter(Boolean);
  assert(jobIds.length === docsPerBatch, "webhook batch did not return every ingestion job id", response);
  return jobIds;
}

function parseTime(value) {
  const parsed = Date.parse(value || "");
  return Number.isFinite(parsed) ? parsed : 0;
}

function percentile(values, quantile) {
  if (values.length === 0) {
    return 0;
  }
  const sorted = [...values].sort((a, b) => a - b);
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * quantile) - 1));
  return sorted[index];
}

function summarizeHealthSample(health) {
  const ingestion = health.ingestion || {};
  return {
    queued_jobs: ingestion.queued_jobs || 0,
    retry_jobs: ingestion.retry_jobs || 0,
    running_jobs: ingestion.running_jobs || 0,
    failed_jobs: ingestion.failed_jobs || 0,
    stale_running_jobs: ingestion.stale_running_jobs || 0
  };
}

async function listJobs() {
  const listed = await request(`/ingestion/jobs?scope=${encodeURIComponent(scope)}&limit=100`);
  return listed.ingestion_jobs || [];
}

async function waitForDrain(jobIds) {
  const submitted = new Set(jobIds);
  const startedAt = Date.now();
  const samples = [];
  let latestJobs = [];
  let latestHealth = {};

  while (Date.now() - startedAt < timeoutMs) {
    latestJobs = (await listJobs()).filter((job) => submitted.has(job.id));
    latestHealth = await request(`/memory/health?scope=${encodeURIComponent(scope)}`);
    samples.push(summarizeHealthSample(latestHealth));

    const terminalFailure = latestJobs.find((job) => ["failed", "canceled"].includes(job.status));
    if (terminalFailure) {
      throw Object.assign(new Error(`queue pressure job ${terminalFailure.id} ended as ${terminalFailure.status}`), { details: terminalFailure });
    }

    const succeeded = latestJobs.filter((job) => job.status === "succeeded");
    if (latestJobs.length === submitted.size && succeeded.length === submitted.size) {
      return { jobs: latestJobs, health: latestHealth, samples, elapsed_ms: Date.now() - startedAt };
    }

    await new Promise((resolve) => setTimeout(resolve, pollMs));
  }

  const pending = latestJobs.filter((job) => !["succeeded", "failed", "canceled"].includes(job.status));
  throw Object.assign(new Error(`timed out waiting for ${submitted.size} queue pressure jobs to drain`), {
    details: {
      scope,
      seen_jobs: latestJobs.length,
      pending_jobs: pending.map((job) => ({ id: job.id, status: job.status, attempts: job.attempts })),
      health: summarizeHealthSample(latestHealth)
    }
  });
}

async function assertRecallFindsFixture() {
  const recall = await request("/recall", {
    method: "POST",
    body: {
      scope,
      query: `abra-queue-pressure-${runId}`,
      limit: 5
    }
  });
  const evidence = [...(recall.claims || []), ...(recall.supporting_documents || [])];
  const found = evidence.some((item) => String(item.source_url || "").includes(`/queue-pressure.example.invalid/${runId}/`) || String(item.content || item.claim_text || "").includes(`abra-queue-pressure-${runId}`));
  assert(found, "recall did not return queue pressure fixture evidence after drain", recall);
  return {
    retrieval_mode: recall.retrieval_mode,
    claims: recall.claims?.length || 0,
    supporting_documents: recall.supporting_documents?.length || 0
  };
}

async function main() {
  const ready = await request("/readyz");
  assert(ready.ok === true, "Abra is not ready", ready);

  const submittedJobIds = [];
  for (let batch = 0; batch < batches; batch += 1) {
    submittedJobIds.push(...await submitBatch(batch));
  }

  assert(submittedJobIds.length === totalDocs, "queue pressure submitted job count mismatch", submittedJobIds);
  const drained = await waitForDrain(submittedJobIds);
  const finalHealth = summarizeHealthSample(drained.health);
  assert(finalHealth.queued_jobs === 0, "memory health still reports queued jobs after drain", finalHealth);
  assert(finalHealth.running_jobs === 0, "memory health still reports running jobs after drain", finalHealth);
  assert(finalHealth.retry_jobs === 0, "memory health still reports retry jobs after drain", finalHealth);
  assert(finalHealth.failed_jobs === 0, "memory health still reports failed jobs after drain", finalHealth);
  assert(finalHealth.stale_running_jobs === 0, "memory health still reports stale running jobs after drain", finalHealth);

  const queueWaits = drained.jobs
    .map((job) => parseTime(job.started_at) - parseTime(job.created_at))
    .filter((value) => value >= 0);
  const drainTimes = drained.jobs
    .map((job) => parseTime(job.finished_at) - parseTime(job.created_at))
    .filter((value) => value >= 0);
  const queueWaitP95 = percentile(queueWaits, 0.95);
  const drainP95 = percentile(drainTimes, 0.95);
  assert(queueWaitP95 <= maxQueueWaitMs, "queue wait p95 exceeded threshold", { queue_wait_p95_ms: queueWaitP95, max_queue_wait_ms: maxQueueWaitMs });
  assert(drainP95 <= maxDrainMs, "drain p95 exceeded threshold", { drain_p95_ms: drainP95, max_drain_ms: maxDrainMs });

  const recall = await assertRecallFindsFixture();
  const output = {
    ok: true,
    suite: "abra-queue-pressure-eval",
    scope,
    run_id: runId,
    batches,
    docs_per_batch: docsPerBatch,
    documents: totalDocs,
    elapsed_ms: drained.elapsed_ms,
    queue_wait_p95_ms: queueWaitP95,
    drain_p95_ms: drainP95,
    max_queued_jobs: Math.max(...drained.samples.map((sample) => sample.queued_jobs), 0),
    max_running_jobs: Math.max(...drained.samples.map((sample) => sample.running_jobs), 0),
    max_retry_jobs: Math.max(...drained.samples.map((sample) => sample.retry_jobs), 0),
    max_failed_jobs: Math.max(...drained.samples.map((sample) => sample.failed_jobs), 0),
    health: finalHealth,
    recall,
    jobs: {
      succeeded: drained.jobs.filter((job) => job.status === "succeeded").length,
      failed: drained.jobs.filter((job) => job.status === "failed").length,
      canceled: drained.jobs.filter((job) => job.status === "canceled").length
    }
  };
  console.log(JSON.stringify(output, null, 2));
}

main().catch((error) => {
  console.error(JSON.stringify({
    ok: false,
    suite: "abra-queue-pressure-eval",
    error: error.message,
    details: error.details
  }, null, 2));
  process.exit(1);
});
