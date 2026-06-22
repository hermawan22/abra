#!/usr/bin/env node

import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

const startedAt = new Date().toISOString();
const runId = startedAt.replace(/[^0-9A-Za-z]/g, "").slice(0, 18);
const profile = (process.env.ABRA_RELEASE_PROFILE || "full").trim();
const quick = profile === "quick";
const dryRun = boolEnv("ABRA_RELEASE_DRY_RUN");
const manageStack = boolEnv("ABRA_RELEASE_MANAGE_STACK", !quick);
const managedHTTPPort = process.env.ABRA_RELEASE_ABRA_PORT || "18081";
const baseUrl = process.env.ABRA_BASE_URL || (manageStack ? `http://127.0.0.1:${managedHTTPPort}` : "http://127.0.0.1:18080");
const releaseSecretSuffix = `${runId.toLowerCase()}-${randomBytes(12).toString("hex")}`;
const defaultReleaseToken = `release-gate-${releaseSecretSuffix}`;
const token = manageStack && placeholderSecret(process.env.ABRA_API_TOKEN) ? defaultReleaseToken : process.env.ABRA_API_TOKEN || "dev-token";
const defaultWebhookSecret = `release-gate-webhook-${releaseSecretSuffix}`;
const webhookSecret = manageStack && placeholderSecret(process.env.ABRA_WEBHOOK_SECRET) ? defaultWebhookSecret : process.env.ABRA_WEBHOOK_SECRET || "dev-webhook-secret";
const defaultPostgresPassword = `release-gate-db-${releaseSecretSuffix}`;
const postgresUser = process.env.POSTGRES_USER || "abra";
const postgresDb = process.env.POSTGRES_DB || "abra";
const postgresPassword = manageStack && placeholderSecret(process.env.POSTGRES_PASSWORD) ? defaultPostgresPassword : process.env.POSTGRES_PASSWORD || "dev-only-postgres-password";
const databaseUrl = process.env.ABRA_DATABASE_URL || `postgres://${postgresUser}:${postgresPassword}@postgres:5432/${postgresDb}`;
const commandTimeoutMs = numberEnv("ABRA_RELEASE_COMMAND_TIMEOUT_MS", quick ? 120_000 : 900_000);
const readyTimeoutMs = numberEnv("ABRA_RELEASE_READY_TIMEOUT_MS", 120_000);
const outputLimit = numberEnv("ABRA_RELEASE_OUTPUT_LIMIT", 12_000);
const prepareDogfoodSource = !quick && boolEnv("ABRA_RELEASE_PREPARE_DOGFOOD_SOURCE", manageStack);
const approvalEnforcementGate = !quick && boolEnv("ABRA_RELEASE_APPROVAL_ENFORCEMENT_GATE", manageStack);
const cleanupManagedStack = manageStack && boolEnv("ABRA_RELEASE_CLEANUP_STACK", true);
const managedComposeProject = process.env.ABRA_RELEASE_COMPOSE_PROJECT_NAME || `abra-release-gate-${runId.toLowerCase()}`;
const managedComposeHTTPPort = process.env.ABRA_RELEASE_ABRA_PORT || urlPort(baseUrl, managedHTTPPort);
const managedComposePostgresPort = process.env.ABRA_RELEASE_POSTGRES_PORT || "55433";
const managedAbraImage = process.env.ABRA_RELEASE_IMAGE || "abra:release-gate";
const dogfoodContainerSourceRoot = process.env.ABRA_RELEASE_DOGFOOD_SOURCE_ROOT || "/tmp/abra-src";
const defaultManagedLocalEmbeddingModel = "Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0";
const managedEnvFile = `.tmp/release-gate-${runId.toLowerCase()}.env`;
const checks = [];
const managedApiKeys = placeholderSecret(process.env.ABRA_API_KEYS) ? token : process.env.ABRA_API_KEYS;
const managedWebhookSecrets = placeholderSecret(process.env.ABRA_WEBHOOK_SECRETS) ? webhookSecret : process.env.ABRA_WEBHOOK_SECRETS;
const managedStackEnv = {
  ...(manageStack ? {
    COMPOSE_PROJECT_NAME: managedComposeProject,
    ABRA_IMAGE: managedAbraImage,
    POSTGRES_IMAGE: process.env.POSTGRES_IMAGE || "pgvector/pgvector:pg16",
    ABRA_PUBLISH_ADDR: "127.0.0.1",
    ABRA_PORT: managedComposeHTTPPort,
    POSTGRES_BIND_ADDR: "127.0.0.1",
    POSTGRES_PORT: managedComposePostgresPort
  } : {}),
  ...(!manageStack ? {
    ABRA_IMAGE: process.env.ABRA_IMAGE || "ghcr.io/hermawan22/abra@sha256:0000000000000000000000000000000000000000000000000000000000000000",
    POSTGRES_IMAGE: process.env.POSTGRES_IMAGE || "pgvector/pgvector@sha256:0000000000000000000000000000000000000000000000000000000000000000"
  } : {}),
  ABRA_API_KEYS: managedApiKeys,
  ABRA_WEBHOOK_SECRETS: managedWebhookSecrets,
  POSTGRES_USER: postgresUser,
  POSTGRES_PASSWORD: postgresPassword,
  POSTGRES_DB: postgresDb,
  ABRA_DATABASE_URL: databaseUrl,
  ABRA_APPROVAL_MODE: process.env.ABRA_APPROVAL_MODE || "advisory",
  ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION: process.env.ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION || "true",
  EMBEDDING_PROVIDER: process.env.EMBEDDING_PROVIDER || "local",
  EMBEDDING_BASE_URL: process.env.EMBEDDING_BASE_URL || "http://host.docker.internal:8080/v1",
  EMBEDDING_API_KEY: process.env.EMBEDDING_API_KEY || "unused-local-embedding-key",
  EMBEDDING_MODEL: process.env.EMBEDDING_MODEL || defaultManagedLocalEmbeddingModel,
  EMBEDDING_DIMENSIONS: process.env.EMBEDDING_DIMENSIONS || "1024",
  RATE_LIMIT_MAX: process.env.RATE_LIMIT_MAX || "1000",
  WORKER_INTERVAL: process.env.WORKER_INTERVAL || "30s",
  WORKER_SOURCE_TIMEOUT: process.env.WORKER_SOURCE_TIMEOUT || "10m",
  WORKER_LEASE_TIMEOUT: process.env.WORKER_LEASE_TIMEOUT || "15m"
};

const productionComposeEnv = {
  ...managedStackEnv,
  ABRA_IMAGE: process.env.ABRA_IMAGE || "ghcr.io/hermawan22/abra@sha256:0000000000000000000000000000000000000000000000000000000000000000",
  POSTGRES_IMAGE: process.env.POSTGRES_IMAGE || "pgvector/pgvector@sha256:0000000000000000000000000000000000000000000000000000000000000000"
};

const composeDevFiles = ["-f", "docker-compose.yml", "-f", "docker-compose.dev.yml"];

function placeholderSecret(value) {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized) {
    return true;
  }
  const primary = normalized.split(/[|,;]/, 1)[0].trim();
  return [
    "dev-token",
    "demo-only-dev-token",
    "dev-only-postgres-password",
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

function imageRefIsDigestPinned(value) {
  return /@sha256:[0-9a-f]{64}$/i.test(String(value || "").trim());
}

function validateProductionComposeImages() {
  const invalid = [];
  for (const name of ["ABRA_IMAGE", "POSTGRES_IMAGE"]) {
    if (process.env[name] && !imageRefIsDigestPinned(process.env[name])) {
      invalid.push(`${name} must be digest-pinned with @sha256:<64 hex>`);
    }
  }
  checks.push({
    name: "production_compose_image_refs",
    command: "validate ABRA_IMAGE and POSTGRES_IMAGE",
    ok: invalid.length === 0,
    exit_code: invalid.length === 0 ? 0 : 1,
    duration_ms: 0,
    stdout: invalid.length === 0 ? "production Compose images are digest-pinned or using digest-shaped sentinels" : "",
    stderr: invalid.join("; ")
  });
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
  const commandText = redact([command, ...args].map(displayArg).join(" "));
  const env = {
    ...process.env,
    ABRA_BASE_URL: baseUrl,
    ABRA_API_TOKEN: token,
    ABRA_WEBHOOK_SECRET: webhookSecret,
    ...options.env
  };
  for (const [key, value] of Object.entries(options.env || {})) {
    if (value === null) {
      delete env[key];
    }
  }
  if (dryRun) {
    checks.push({
      name,
      command: commandText,
      ok: true,
      exit_code: 0,
      duration_ms: 0,
      dry_run: true,
      skipped: true,
      stdout: "dry run: command was not executed",
      stderr: ""
    });
    return;
  }
  const result = await new Promise((resolve) => {
    let stdout = "";
    let stderr = "";
    let timedOut = false;
    let exited = false;
    const child = spawn(command, args, {
      cwd: options.cwd || process.cwd(),
      env,
      shell: false,
      detached: true
    });
    const timer = setTimeout(() => {
      timedOut = true;
      killProcessTree(child, "SIGTERM");
      stderr += `\ncommand timed out after ${commandTimeoutMs}ms`;
      setTimeout(() => {
        if (!exited) {
          killProcessTree(child, "SIGKILL");
        }
      }, 5000).unref();
    }, commandTimeoutMs);
    child.stdout.on("data", (chunk) => {
      stdout += chunk.toString();
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk.toString();
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      resolve({ code: 127, stdout, stderr: `${stderr}${error.message}`, timedOut });
    });
    child.on("close", (code, signal) => {
      exited = true;
      clearTimeout(timer);
      resolve({ code: timedOut ? 124 : signal ? 128 : code ?? 1, stdout, stderr, signal, timedOut });
    });
  });
  checks.push({
    name,
    command: commandText,
    ok: result.code === 0 && !result.timedOut,
    exit_code: result.code,
    duration_ms: Date.now() - started,
    ...(result.signal ? { signal: result.signal } : {}),
    ...(result.timedOut ? { timed_out: true } : {}),
    stdout: truncate(result.stdout.trim()),
    stderr: truncate(result.stderr.trim())
  });
}

function killProcessTree(child, signal) {
  if (!child.pid) {
    return;
  }
  try {
    process.kill(-child.pid, signal);
  } catch {
    try {
      child.kill(signal);
    } catch {
      // Ignore best-effort timeout cleanup failures; the command result still fails.
    }
  }
}

function displayArg(value) {
  const text = String(value);
  if (/^[A-Za-z0-9_./:=@+-]+$/.test(text)) {
    return text;
  }
  return shellArg(text);
}

function shellArg(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function writeManagedEnvFile() {
  mkdirSync(dirname(managedEnvFile), { recursive: true });
  const lines = Object.entries(managedStackEnv)
    .filter(([, value]) => value !== undefined && value !== null)
    .map(([key, value]) => `${key}=${String(value).replace(/\n/g, "")}`);
  writeFileSync(managedEnvFile, `${lines.join("\n")}\n`, { mode: 0o600 });
}

async function waitForReady(name) {
  const started = Date.now();
  let lastError = "";
  while (Date.now() - started < readyTimeoutMs) {
    try {
      const response = await fetch(`${baseUrl}/readyz`, { signal: AbortSignal.timeout(5000) });
      const text = await response.text();
      if (response.ok) {
        let payload = {};
        try {
          payload = text ? JSON.parse(text) : {};
        } catch {
          payload = { raw: text };
        }
        if (payload.ok === true) {
          checks.push({
            name,
            command: `GET ${baseUrl}/readyz`,
            ok: true,
            exit_code: 0,
            duration_ms: Date.now() - started,
            stdout: truncate(JSON.stringify(payload)),
            stderr: ""
          });
          return;
        }
        lastError = `readyz returned ok=${payload.ok}`;
      } else {
        lastError = `readyz returned HTTP ${response.status}: ${text}`;
      }
    } catch (error) {
      lastError = error.message;
    }
    await new Promise((resolve) => setTimeout(resolve, 2000));
  }
  checks.push({
    name,
    command: `GET ${baseUrl}/readyz`,
    ok: false,
    exit_code: 1,
    duration_ms: Date.now() - started,
    stdout: "",
    stderr: truncate(`Abra did not become ready within ${readyTimeoutMs}ms: ${lastError}`)
  });
}

function quickPerfEnv() {
  if (!quick) {
    return {};
  }
  if (usesManagedLocalQwenEmbeddings()) {
    return managedLocalPerfEnv({ quickProfile: true });
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

function usesManagedLocalQwenEmbeddings() {
  const provider = String(managedStackEnv.EMBEDDING_PROVIDER || "").trim().toLowerCase();
  const model = String(managedStackEnv.EMBEDDING_MODEL || "").trim().toLowerCase();
  return manageStack && provider === "local" && model.includes("qwen/qwen3-embedding-0.6b-gguf");
}

function managedLocalPerfEnv({ quickProfile = false } = {}) {
  return {
    ABRA_PERF_DOCS: process.env.ABRA_PERF_DOCS || (quickProfile ? "12" : "16"),
    ABRA_PERF_ITERATIONS: process.env.ABRA_PERF_ITERATIONS || "8",
    ABRA_PERF_CONCURRENCY: process.env.ABRA_PERF_CONCURRENCY || "1",
    ABRA_PERF_CAPACITY_ITERATIONS: process.env.ABRA_PERF_CAPACITY_ITERATIONS || "16",
    ABRA_PERF_CAPACITY_CONCURRENCY: process.env.ABRA_PERF_CAPACITY_CONCURRENCY || "8",
    ABRA_PERF_PROFILE_NAME: process.env.ABRA_PERF_PROFILE_NAME || (quickProfile ? "managed-local-qwen-quick-release-gate" : "managed-local-qwen-release-gate"),
    ABRA_PERF_RECALL_P95_MS: process.env.ABRA_PERF_RECALL_P95_MS || "6000",
    ABRA_PERF_MEMORY_P95_MS: process.env.ABRA_PERF_MEMORY_P95_MS || "30000",
    ABRA_PERF_MEMORY_CAPACITY_P95_MS: process.env.ABRA_PERF_MEMORY_CAPACITY_P95_MS || "90000",
    ABRA_PERF_SOAK_SECONDS: process.env.ABRA_PERF_SOAK_SECONDS || "0"
  };
}

function fullPerfEnv() {
  if (quick) {
    return quickPerfEnv();
  }
  if (usesManagedLocalQwenEmbeddings()) {
    return managedLocalPerfEnv();
  }
  return {
    ABRA_PERF_MEMORY_P95_MS: process.env.ABRA_PERF_MEMORY_P95_MS || "3000",
    ABRA_PERF_MEMORY_CAPACITY_P95_MS: process.env.ABRA_PERF_MEMORY_CAPACITY_P95_MS || "6000"
  };
}

function tier1Env() {
	if (!quick && !usesManagedLocalQwenEmbeddings()) {
		return {};
	}
	return {
		ABRA_TIER1_MEMORY_MAX_MS: process.env.ABRA_TIER1_MEMORY_MAX_MS || (quick ? "5000" : "10000")
	};
}

function goldenEnv() {
	if (!quick && !usesManagedLocalQwenEmbeddings()) {
		return {};
	}
	return {
		ABRA_GOLDEN_MEMORY_MAX_MS: process.env.ABRA_GOLDEN_MEMORY_MAX_MS || (quick ? "5000" : "10000")
	};
}

function providerEvalEnv() {
	if (!quick && !usesManagedLocalQwenEmbeddings()) {
		return {};
	}
	return {
		ABRA_PROVIDER_RECALL_P95_MS: process.env.ABRA_PROVIDER_RECALL_P95_MS || (quick ? "1000" : "2000"),
		ABRA_PROVIDER_MEMORY_P95_MS: process.env.ABRA_PROVIDER_MEMORY_P95_MS || (quick ? "5000" : "10000")
	};
}

function tier23Env(extra = {}) {
	if (!quick && !usesManagedLocalQwenEmbeddings()) {
		return { ...extra };
	}
	return {
		ABRA_TIER23_MEMORY_MAX_MS: process.env.ABRA_TIER23_MEMORY_MAX_MS || (quick ? "5000" : "10000"),
		...extra
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
  validateProductionComposeImages();

  await runCommand("agent_context_files", "go", ["run", "./cmd/abra", "agents", "verify", ".", "--scope", "repo:abra", "--files-only", "--strict"]);
  await runCommand("script_checks", "npm", ["run", "check:scripts"]);
  await runCommand("eval_contracts", "npm", ["run", "test:eval-contracts"]);
  await runCommand("installer_fail_closed", "npm", ["run", "test:installer"]);
  await runCommand("npm_pack_allowlist", "npm", ["run", "test:npm-pack"]);
  await runCommand("oss_hygiene", "npm", ["run", "check:oss"]);
  await runCommand("go_tests", "go", ["test", "./..."], {
    env: {
      ABRA_BASE_URL: null,
      ABRA_API_TOKEN: null,
      ABRA_WEBHOOK_SECRET: null
    }
  });
  await runCommand("docker_compose_config", "docker", ["compose", "-f", "docker-compose.yml", "config"], {
    env: productionComposeEnv
  });
  await runCommand("helm_lint", "helm", ["lint", "./deploy/helm"]);
  await runCommand("helm_template", "helm", ["template", "abra", "./deploy/helm"]);

  if (!quick || boolEnv("ABRA_RELEASE_DOCKER_BUILD")) {
    await runCommand("docker_build", "docker", ["build", "-t", process.env.ABRA_RELEASE_IMAGE || "abra:release-gate", "."]);
  }

  if (manageStack) {
    const stackCheckStart = checks.length;
    if (usesManagedLocalQwenEmbeddings()) {
      writeManagedEnvFile();
      await runCommand("managed_local_models_up", "go", ["run", "./cmd/abra", "models", "up", "--env-file", managedEnvFile, "--startup-timeout", process.env.ABRA_RELEASE_MODEL_STARTUP_TIMEOUT || "10m"], {
        env: managedStackEnv
      });
    }
    await runCommand("docker_compose_build_stack", "docker", ["compose", ...composeDevFiles, "build", "api", "worker", "migrate"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_postgres_up", "docker", ["compose", ...composeDevFiles, "up", "-d", "postgres"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_migrate", "docker", ["compose", ...composeDevFiles, "run", "--rm", "migrate"], {
      env: managedStackEnv
    });
    await runCommand("docker_compose_up", "docker", ["compose", ...composeDevFiles, "up", "-d", "api", "worker"], {
      env: managedStackEnv
    });
    await waitForReady("managed_stack_ready");
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
	await runCommand("eval_tier1", "npm", ["run", "eval:tier1"], { env: tier1Env() });
	if (!quick) {
		await runCommand("eval_golden", "npm", ["run", "eval:golden"], { env: goldenEnv() });
		await runCommand("eval_provider", "npm", ["run", "eval:provider"], { env: providerEvalEnv() });
		await runCommand("eval_tier23", "npm", ["run", "eval:tier23"], { env: tier23Env() });
    if (approvalEnforcementGate) {
      if (manageStack) {
        await runCommand("docker_compose_enforce_up", "docker", ["compose", ...composeDevFiles, "up", "-d", "--force-recreate", "api", "worker"], {
          env: {
            ...managedStackEnv,
            ABRA_APPROVAL_MODE: "enforce",
          }
        });
        await waitForReady("managed_stack_ready_enforced");
      }
			await runCommand("eval_tier23_enforced", "npm", ["run", "eval:tier23"], {
				env: tier23Env({ ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT: "1" })
			});
      if (manageStack) {
        await runCommand("docker_compose_advisory_up", "docker", ["compose", ...composeDevFiles, "up", "-d", "--force-recreate", "api", "worker"], {
          env: managedStackEnv
        });
        await waitForReady("managed_stack_ready_advisory");
      }
    }
    if (prepareDogfoodSource) {
      const quotedDogfoodRoot = shellArg(dogfoodContainerSourceRoot);
      await runCommand("prepare_dogfood_source_dir", "docker", ["compose", ...composeDevFiles, "exec", "-T", "worker", "sh", "-lc", `rm -rf -- ${quotedDogfoodRoot} && mkdir -p -- ${quotedDogfoodRoot}`], {
        env: managedStackEnv
      });
      await runCommand("prepare_dogfood_source_copy", "bash", [
        "-lc",
        `COPYFILE_DISABLE=1 tar --exclude .tmp --exclude node_modules --exclude .git --exclude '._*' --no-xattrs -cf - . | docker compose -f docker-compose.yml -f docker-compose.dev.yml exec -T worker tar -C ${quotedDogfoodRoot} -xf -`
      ], {
        env: managedStackEnv
      });
      await runCommand("prepare_dogfood_source_clean", "docker", ["compose", ...composeDevFiles, "exec", "-T", "worker", "find", dogfoodContainerSourceRoot, "-name", "._*", "-delete"], {
        env: managedStackEnv
      });
    }
    await runCommand("eval_dogfood", "npm", ["run", "eval:dogfood"], {
      env: {
        ABRA_DOGFOOD_SCOPE: process.env.ABRA_DOGFOOD_SCOPE || `repo:abra-release-${runId}`,
        ABRA_DOGFOOD_SOURCE_NAME: process.env.ABRA_DOGFOOD_SOURCE_NAME || `abra-self-${runId}`,
        ABRA_DOGFOOD_TIMEOUT_MS: process.env.ABRA_DOGFOOD_TIMEOUT_MS || "600000",
        ...(prepareDogfoodSource ? { ABRA_DOGFOOD_SOURCE_ROOT: dogfoodContainerSourceRoot } : {})
      }
    });
  }
  await runCommand("perf_local", "npm", ["run", "perf:local"], { env: fullPerfEnv() });
}

async function cleanup() {
  if (!cleanupManagedStack) {
    return;
  }
  await runCommand("docker_compose_down_managed_stack", "docker", ["compose", ...composeDevFiles, "down", "--volumes"], {
    env: managedStackEnv
  });
  try {
    rmSync(managedEnvFile, { force: true });
  } catch {
    // Best-effort cleanup; the release summary records stack cleanup separately.
  }
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
  dry_run: dryRun,
  failed_checks: failed.map((check) => ({
    name: check.name,
    exit_code: check.exit_code,
    error: check.stderr || check.stdout || ""
  })),
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
    managed_stack_cleaned: cleanupManagedStack,
    dry_run: dryRun
  }
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
