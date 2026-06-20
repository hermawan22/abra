#!/usr/bin/env node

import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";

const startedAt = new Date().toISOString();
const runId = startedAt.replace(/[^0-9A-Za-z]/g, "").slice(0, 18);
const profile = (process.env.ABRA_RELEASE_PROFILE || "full").trim();
const quick = profile === "quick";
const manageStack = boolEnv("ABRA_RELEASE_MANAGE_STACK");
const managedHTTPPort = process.env.ABRA_RELEASE_ABRA_PORT || "18081";
const baseUrl = process.env.ABRA_BASE_URL || (manageStack ? `http://127.0.0.1:${managedHTTPPort}` : "http://127.0.0.1:18080");
const releaseSecretSuffix = `${runId.toLowerCase()}-${randomBytes(12).toString("hex")}`;
const defaultReleaseToken = `release-gate-${releaseSecretSuffix}`;
const token = manageStack && placeholderSecret(process.env.ABRA_API_TOKEN) ? defaultReleaseToken : process.env.ABRA_API_TOKEN || "dev-token";
const defaultWebhookSecret = `release-gate-webhook-${releaseSecretSuffix}`;
const webhookSecret = manageStack && placeholderSecret(process.env.ABRA_WEBHOOK_SECRET) ? defaultWebhookSecret : process.env.ABRA_WEBHOOK_SECRET || "dev-webhook-secret";
const commandTimeoutMs = numberEnv("ABRA_RELEASE_COMMAND_TIMEOUT_MS", quick ? 120_000 : 600_000);
const outputLimit = numberEnv("ABRA_RELEASE_OUTPUT_LIMIT", 12_000);
const prepareDogfoodSource = !quick && boolEnv("ABRA_RELEASE_PREPARE_DOGFOOD_SOURCE", manageStack);
const approvalEnforcementGate = !quick && boolEnv("ABRA_RELEASE_APPROVAL_ENFORCEMENT_GATE", manageStack);
const cleanupManagedStack = manageStack && boolEnv("ABRA_RELEASE_CLEANUP_STACK", true);
const managedComposeProject = process.env.ABRA_RELEASE_COMPOSE_PROJECT_NAME || `abra-release-gate-${runId.toLowerCase()}`;
const managedComposeHTTPPort = process.env.ABRA_RELEASE_ABRA_PORT || urlPort(baseUrl, managedHTTPPort);
const managedComposePostgresPort = process.env.ABRA_RELEASE_POSTGRES_PORT || "55433";
const dogfoodContainerSourceRoot = process.env.ABRA_RELEASE_DOGFOOD_SOURCE_ROOT || "/tmp/abra-src";
const checks = [];
const managedApiKeys = placeholderSecret(process.env.ABRA_API_KEYS) ? token : process.env.ABRA_API_KEYS;
const managedWebhookSecrets = placeholderSecret(process.env.ABRA_WEBHOOK_SECRETS) ? webhookSecret : process.env.ABRA_WEBHOOK_SECRETS;
const managedStackEnv = {
  ...(manageStack ? {
    COMPOSE_PROJECT_NAME: managedComposeProject,
    ABRA_PUBLISH_ADDR: "127.0.0.1",
    ABRA_PORT: managedComposeHTTPPort,
    POSTGRES_BIND_ADDR: "127.0.0.1",
    POSTGRES_PORT: managedComposePostgresPort
  } : {}),
  ABRA_API_KEYS: managedApiKeys,
  ABRA_WEBHOOK_SECRETS: managedWebhookSecrets,
  ABRA_APPROVAL_MODE: process.env.ABRA_APPROVAL_MODE || "advisory",
  ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION: process.env.ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION || "true",
  EMBEDDING_PROVIDER: process.env.EMBEDDING_PROVIDER || "local",
  EMBEDDING_BASE_URL: process.env.EMBEDDING_BASE_URL || "http://host.docker.internal:8080/v1",
  EMBEDDING_API_KEY: process.env.EMBEDDING_API_KEY || "unused-local-embedding-key",
  RATE_LIMIT_MAX: process.env.RATE_LIMIT_MAX || "1000",
  WORKER_INTERVAL: process.env.WORKER_INTERVAL || "30s"
};

function placeholderSecret(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) {
    return true;
  }
  const primary = normalized.split(/[|,;]/, 1)[0].trim();
  return [
    "dev-token",
    "dev-webhook-secret",
    "changeme",
    "change-me",
    "replace-me",
    "replace-with-token",
    "placeholder"
  ].includes(primary) || primary.includes("placeholder") || primary.includes("example");
}

function numberEnv(name, fallback) {
  const value = Number(process.env[name] || fallback);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

function urlPort(raw, fallback) {
  try {
    const parsed = new URL(raw);
    if (parsed.port) {
      return parsed.port;
    }
    if (parsed.protocol === "https:") {
      return "443";
    }
    if (parsed.protocol === "http:") {
      return "80";
    }
  } catch {
    return fallback;
  }
  return fallback;
}

function boolEnv(name, fallback = false) {
  const value = String(process.env[name] || "").trim().toLowerCase();
  if (["1", "true", "yes", "on"].includes(value)) {
    return true;
  }
  if (["0", "false", "no", "off"].includes(value)) {
    return false;
  }
  return fallback;
}

function truncate(value) {
  value = redact(value);
  if (value.length <= outputLimit) {
    return value;
  }
  return `${value.slice(0, outputLimit)}\n...<truncated ${value.length - outputLimit} chars>`;
}

function redact(value) {
  let redacted = value;
  const secrets = new Set([
    token,
    process.env.ABRA_API_KEYS,
    process.env.ABRA_API_TOKEN,
    process.env.ABRA_WEBHOOK_SECRETS,
    process.env.ABRA_WEBHOOK_SECRET,
    process.env.EMBEDDING_API_KEY,
    process.env.ABRA_AUDIT_SINK_TOKEN,
    process.env.ABRA_AUDIT_SINK_SECRET
  ]);
  for (const secret of secrets) {
    if (secret && String(secret).length >= 4) {
      redacted = redacted.split(String(secret)).join("[redacted]");
    }
  }
  return redacted;
}

async function runCommand(name, command, args = [], options = {}) {
  const started = Date.now();
  const env = {
    ...process.env,
    ABRA_BASE_URL: baseUrl,
    ABRA_API_TOKEN: token,
    ABRA_WEBHOOK_SECRET: webhookSecret,
    ...options.env
  };
  const result = await new Promise((resolve) => {
    let stdout = "";
    let stderr = "";
    const child = spawn(command, args, {
      cwd: options.cwd || process.cwd(),
      env,
      shell: false
    });
    const timer = setTimeout(() => {
      child.kill("SIGTERM");
      stderr += `\ncommand timed out after ${commandTimeoutMs}ms`;
    }, commandTimeoutMs);
    child.stdout.on("data", (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk.toString();
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      resolve({ code: 127, stdout, stderr: `${stderr}${error.message}` });
    });
    child.on("close", (code, signal) => {
      clearTimeout(timer);
      resolve({ code: signal ? 128 : code ?? 1, stdout, stderr, signal });
    });
  });
  checks.push({
    name,
    command: [command, ...args].join(" "),
    ok: result.code === 0,
    exit_code: result.code,
    duration_ms: Date.now() - started,
    ...(result.signal ? { signal: result.signal } : {}),
    stdout: truncate(result.stdout.trim()),
    stderr: truncate(result.stderr.trim())
  });
}

function quickPerfEnv() {
  if (!quick) {
    return {};
  }
  return {
    ABRA_PERF_DOCS: process.env.ABRA_PERF_DOCS || "12",
    ABRA_PERF_ITERATIONS: process.env.ABRA_PERF_ITERATIONS || "8",
    ABRA_PERF_CAPACITY_ITERATIONS: process.env.ABRA_PERF_CAPACITY_ITERATIONS || "16",
    ABRA_PERF_RECALL_P95_MS: process.env.ABRA_PERF_RECALL_P95_MS || "1000",
    ABRA_PERF_MEMORY_P95_MS: process.env.ABRA_PERF_MEMORY_P95_MS || "5000",
    ABRA_PERF_SOAK_SECONDS: process.env.ABRA_PERF_SOAK_SECONDS || "0"
  };
}

function quickTier1Env() {
  if (!quick) {
    return {};
  }
  return {
    ABRA_TIER1_MEMORY_MAX_MS: process.env.ABRA_TIER1_MEMORY_MAX_MS || "5000"
  };
}

function quickQueuePressureEnv() {
  if (!quick) {
    return {};
  }
  return {
    ABRA_QUEUE_PRESSURE_BATCHES: process.env.ABRA_QUEUE_PRESSURE_BATCHES || "1",
    ABRA_QUEUE_PRESSURE_DOCS_PER_BATCH: process.env.ABRA_QUEUE_PRESSURE_DOCS_PER_BATCH || "2",
    ABRA_QUEUE_PRESSURE_TIMEOUT_MS: process.env.ABRA_QUEUE_PRESSURE_TIMEOUT_MS || "120000",
    ABRA_QUEUE_PRESSURE_MAX_DRAIN_MS: process.env.ABRA_QUEUE_PRESSURE_MAX_DRAIN_MS || "120000"
  };
}

async function main() {
  if (!["quick", "full"].includes(profile)) {
    checks.push({
      name: "validate_profile",
      ok: false,
      exit_code: 1,
      duration_ms: 0,
      stdout: "",
      stderr: `ABRA_RELEASE_PROFILE must be quick or full, got ${profile}`
    });
  } else {
    checks.push({
      name: "validate_profile",
      ok: true,
      exit_code: 0,
      duration_ms: 0,
      stdout: `profile=${profile}`,
      stderr: ""
    });
  }

  await runCommand("agent_context_files", "go", ["run", "./cmd/abra", "agents", "verify", ".", "--scope", "repo:abra", "--files-only", "--strict"]);
  await runCommand("script_checks", "npm", ["test"]);
  await runCommand("go_tests", "go", ["test", "./..."]);
  await runCommand("docker_compose_config", "docker", ["compose", "config"], {
    env: managedStackEnv
  });
  await runCommand("helm_lint", "helm", ["lint", "./deploy/helm"]);
  await runCommand("helm_template", "helm", ["template", "abra", "./deploy/helm"]);

  if (!quick || boolEnv("ABRA_RELEASE_DOCKER_BUILD")) {
    await runCommand("docker_build", "docker", ["build", "-t", process.env.ABRA_RELEASE_IMAGE || "abra:release-gate", "."]);
  }

  if (manageStack) {
    const stackCheckStart = checks.length;
    await runCommand("docker_compose_build_stack", "docker", ["compose", "build", "api", "worker", "migrate"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_postgres_up", "docker", ["compose", "up", "-d", "postgres"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_migrate", "docker", ["compose", "run", "--rm", "migrate"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_up", "docker", ["compose", "up", "-d", "api", "worker"], {
      env: managedStackEnv
    });
    if (checks.slice(stackCheckStart).some((check) => !check.ok)) {
      checks.push({
        name: "live_checks_skipped",
        command: "managed stack bootstrap",
        ok: false,
        exit_code: 1,
        duration_ms: 0,
        stdout: "",
        stderr: "managed Docker Compose stack did not start; skipping smoke/eval/perf checks that require ABRA_BASE_URL"
      });
      return;
    }
  }

  await runCommand("smoke_selfhost", "npm", ["run", "smoke:selfhost"]);
  if (!quick || boolEnv("ABRA_RELEASE_QUEUE_PRESSURE_GATE")) {
    await runCommand("eval_queue_pressure", "npm", ["run", "eval:queue-pressure"], { env: quickQueuePressureEnv() });
  }
  await runCommand("eval_tier1", "npm", ["run", "eval:tier1"], { env: quickTier1Env() });
  if (!quick) {
    await runCommand("eval_golden", "npm", ["run", "eval:golden"]);
    await runCommand("eval_provider", "npm", ["run", "eval:provider"]);
    await runCommand("eval_tier23", "npm", ["run", "eval:tier23"]);
    if (approvalEnforcementGate) {
      if (manageStack) {
        await runCommand("docker_compose_enforce_up", "docker", ["compose", "up", "-d", "--force-recreate", "api", "worker"], {
          env: {
            ...managedStackEnv,
            ABRA_APPROVAL_MODE: "enforce",
          }
        });
      }
      await runCommand("eval_tier23_enforced", "npm", ["run", "eval:tier23"], {
        env: { ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT: "1" }
      });
    }
    if (prepareDogfoodSource) {
      await runCommand("prepare_dogfood_source_dir", "docker", ["compose", "exec", "-T", "worker", "sh", "-lc", `rm -rf ${dogfoodContainerSourceRoot} && mkdir -p ${dogfoodContainerSourceRoot}`], {
        env: manageStack ? managedStackEnv : {}
      });
      await runCommand("prepare_dogfood_source_copy", "bash", [
        "-lc",
        `COPYFILE_DISABLE=1 tar --exclude .tmp --exclude node_modules --exclude .git --exclude '._*' --no-xattrs -cf - . | docker compose exec -T worker tar -C ${dogfoodContainerSourceRoot} -xf -`
      ], {
        env: manageStack ? managedStackEnv : {}
      });
      await runCommand("prepare_dogfood_source_clean", "docker", ["compose", "exec", "-T", "worker", "find", dogfoodContainerSourceRoot, "-name", "._*", "-delete"], {
        env: manageStack ? managedStackEnv : {}
      });
    }
    await runCommand("eval_dogfood", "npm", ["run", "eval:dogfood"], {
      env: {
        ABRA_DOGFOOD_SCOPE: process.env.ABRA_DOGFOOD_SCOPE || `repo:abra-release-${runId}`,
        ABRA_DOGFOOD_SOURCE_NAME: process.env.ABRA_DOGFOOD_SOURCE_NAME || `abra-self-${runId}`,
        ...(prepareDogfoodSource ? { ABRA_DOGFOOD_SOURCE_ROOT: dogfoodContainerSourceRoot } : {})
      }
    });
  }
  await runCommand("perf_local", "npm", ["run", "perf:local"], { env: quickPerfEnv() });
}

async function cleanup() {
  if (!cleanupManagedStack) {
    return;
  }
  await runCommand("docker_compose_down_managed_stack", "docker", ["compose", "down", "--volumes"], {
    env: managedStackEnv
  });
}

try {
  await main();
} finally {
  await cleanup();
}

const failed = checks.filter((check) => !check.ok);
const summary = {
  suite: "abra-release-gate",
  status: failed.length === 0 ? "passed" : "failed",
  profile,
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
    run_id: runId,
    dogfood_included: !quick,
    queue_pressure_included: !quick || boolEnv("ABRA_RELEASE_QUEUE_PRESSURE_GATE"),
    docker_build_included: !quick || boolEnv("ABRA_RELEASE_DOCKER_BUILD"),
    dogfood_source_prepared: prepareDogfoodSource,
    approval_enforcement_gate_included: approvalEnforcementGate,
    managed_stack_project: manageStack ? managedComposeProject : "",
    managed_stack_cleaned: cleanupManagedStack
  }
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
