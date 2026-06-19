#!/usr/bin/env node

import { spawn } from "node:child_process";

const startedAt = new Date().toISOString();
const runId = startedAt.replace(/[^0-9A-Za-z]/g, "").slice(0, 18);
const profile = (process.env.ABRA_RELEASE_PROFILE || "full").trim();
const baseUrl = process.env.ABRA_BASE_URL || "http://127.0.0.1:18080";
const token = process.env.ABRA_API_TOKEN || "dev-token";
const quick = profile === "quick";
const commandTimeoutMs = numberEnv("ABRA_RELEASE_COMMAND_TIMEOUT_MS", quick ? 120_000 : 600_000);
const outputLimit = numberEnv("ABRA_RELEASE_OUTPUT_LIMIT", 12_000);
const manageStack = boolEnv("ABRA_RELEASE_MANAGE_STACK");
const prepareDogfoodSource = !quick && boolEnv("ABRA_RELEASE_PREPARE_DOGFOOD_SOURCE", manageStack);
const approvalEnforcementGate = !quick && boolEnv("ABRA_RELEASE_APPROVAL_ENFORCEMENT_GATE", manageStack);
const dogfoodContainerSourceRoot = process.env.ABRA_RELEASE_DOGFOOD_SOURCE_ROOT || "/tmp/abra-src";
const checks = [];
const managedStackEnv = {
  ABRA_API_KEYS: process.env.ABRA_API_KEYS || token,
  ABRA_WEBHOOK_SECRETS: process.env.ABRA_WEBHOOK_SECRETS || "release-gate-webhook-secret",
  ABRA_APPROVAL_MODE: process.env.ABRA_APPROVAL_MODE || "advisory",
  ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION: process.env.ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION || "true",
  EMBEDDING_PROVIDER: process.env.EMBEDDING_PROVIDER || "local",
  EMBEDDING_BASE_URL: process.env.EMBEDDING_BASE_URL || "http://127.0.0.1/unused-local-embeddings",
  EMBEDDING_API_KEY: process.env.EMBEDDING_API_KEY || "unused-local-embedding-key",
  RATE_LIMIT_MAX: process.env.RATE_LIMIT_MAX || "1000",
  WORKER_INTERVAL: process.env.WORKER_INTERVAL || "30s"
};

function numberEnv(name, fallback) {
  const value = Number(process.env[name] || fallback);
  return Number.isFinite(value) && value > 0 ? value : fallback;
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
    ABRA_PERF_SOAK_SECONDS: process.env.ABRA_PERF_SOAK_SECONDS || "0"
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
  await runCommand("eval_tier1", "npm", ["run", "eval:tier1"]);
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

await main();

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
    docker_build_included: !quick || boolEnv("ABRA_RELEASE_DOCKER_BUILD"),
    dogfood_source_prepared: prepareDogfoodSource,
    approval_enforcement_gate_included: approvalEnforcementGate
  }
};

console.log(JSON.stringify(summary, null, 2));

if (failed.length > 0) {
  process.exitCode = 1;
}
