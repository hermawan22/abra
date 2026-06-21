#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const ignoredPathPatterns = [
  /^\.git\//,
  /^node_modules\//,
  /^dist\//,
  /^\.tmp\//,
  /^coverage\//
];

const forbiddenPatterns = [
  {
    id: "private_key",
    pattern: /-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/,
    message: "private key material must never be committed"
  },
  {
    id: "aws_access_key",
    pattern: /\bAKIA[0-9A-Z]{16}\b/,
    message: "AWS access key pattern detected"
  },
  {
    id: "github_token",
    pattern: /\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{22,}\b/,
    message: "GitHub token pattern detected"
  },
  {
    id: "slack_token",
    pattern: /\bxox[baprs]-[A-Za-z0-9-]{20,}\b/,
    message: "Slack token pattern detected"
  },
  {
    id: "google_api_key",
    pattern: /\bAIza[0-9A-Za-z_-]{35}\b/,
    message: "Google API key pattern detected"
  },
  {
    id: "openai_key",
    pattern: /\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}\b/,
    message: "OpenAI-style API key pattern detected"
  },
  {
    id: "local_user_path",
    pattern: /\/Users\/[A-Za-z0-9._-]+\/(?:WORKS|work|workspace|Projects)\b/,
    message: "developer-local absolute workspace path detected"
  },
  {
    id: "private_registry_credentials",
    pattern: /\bNEXUS_(?:USER|PASSWORD|TOKEN)\b/,
    message: "private registry credential name detected"
  }
];

function extraForbiddenPatterns() {
  const raw = process.env.ABRA_OSS_PRIVATE_CONTEXT_PATTERNS || "";
  return raw
    .split(/\r?\n|,/)
    .map((pattern) => pattern.trim())
    .filter(Boolean)
    .map((pattern, index) => ({
      id: `private_context_${index + 1}`,
      pattern: new RegExp(pattern, "i"),
      message: "private context pattern detected"
    }));
}

function trackedFiles() {
  const output = execFileSync("git", ["ls-files", "-z"], { encoding: "utf8" });
  return output.split("\0").filter(Boolean).filter((file) => {
    return !ignoredPathPatterns.some((pattern) => pattern.test(file));
  });
}

function lineNumberFor(content, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) {
    if (content.charCodeAt(i) === 10) {
      line += 1;
    }
  }
  return line;
}

function scanFile(file) {
  let content;
  try {
    content = readFileSync(file, "utf8");
  } catch {
    return [];
  }
  if (content.includes("\u0000")) {
    return [];
  }
  const findings = [];
  for (const rule of [...forbiddenPatterns, ...extraForbiddenPatterns()]) {
    rule.pattern.lastIndex = 0;
    let match;
    while ((match = rule.pattern.exec(content)) !== null) {
      findings.push({
        file,
        line: lineNumberFor(content, match.index),
        rule: rule.id,
        message: rule.message
      });
      if (!rule.pattern.global) {
        break;
      }
    }
  }
  return findings;
}

function workflowActionRefFindings(files) {
  const workflowFiles = files.filter((file) => /^\.github\/workflows\/[^/]+\.ya?ml$/.test(file));
  const findings = [];
  for (const file of workflowFiles) {
    let content;
    try {
      content = readFileSync(file, "utf8");
    } catch {
      continue;
    }
    const lines = content.split(/\r?\n/);
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index];
      const match = line.match(/^\s*(?:-\s*)?uses:\s*['"]?([^'"\s#]+)['"]?/);
      if (!match) {
        continue;
      }
      const spec = match[1];
      if (spec.startsWith("./") || spec.startsWith("../")) {
        continue;
      }
      const at = spec.lastIndexOf("@");
      if (at < 0) {
        findings.push({
          file,
          line: index + 1,
          rule: "unpinned_github_action",
          message: `external action ${spec} must be pinned to a full commit SHA`
        });
        continue;
      }
      const ref = spec.slice(at + 1);
      if (!/^[0-9a-f]{40}$/i.test(ref)) {
        findings.push({
          file,
          line: index + 1,
          rule: "unpinned_github_action",
          message: `external action ${spec} must use a 40-character commit SHA, not a mutable tag or branch`
        });
      }
    }
  }
  return findings;
}

function rawInstallerURLFindings(files) {
  const findings = [];
  const rawInstallerPattern = /https:\/\/raw\.githubusercontent\.com\/[^\s'"`<>]+\/scripts\/install\.sh/g;
  for (const file of files) {
    let content;
    try {
      content = readFileSync(file, "utf8");
    } catch {
      continue;
    }
    rawInstallerPattern.lastIndex = 0;
    let match;
    while ((match = rawInstallerPattern.exec(content)) !== null) {
      findings.push({
        file,
        line: lineNumberFor(content, match.index),
        rule: "raw_branch_installer_url",
        message: "official install and upgrade paths must use GitHub Release installer URLs, not raw branch install.sh URLs"
      });
    }
  }
  return findings;
}

function publicPlaceholderDigestFindings(files) {
  const publicFiles = files.filter(
    (file) =>
      file.startsWith("deploy/") ||
      file.startsWith("examples/") ||
      file.startsWith("docs/") ||
      ["README.md", "PRODUCTION.md", "SECURITY.md"].includes(file)
  );
  const findings = [];
  const placeholderPattern = /sha256:0{64}/g;
  for (const file of publicFiles) {
    let content;
    try {
      content = readFileSync(file, "utf8");
    } catch {
      continue;
    }
    placeholderPattern.lastIndex = 0;
    let match;
    while ((match = placeholderPattern.exec(content)) !== null) {
      findings.push({
        file,
        line: lineNumberFor(content, match.index),
        rule: "public_placeholder_digest",
        message: "public deployment docs/examples must use explicit replacement placeholders, not all-zero sha256 digests"
      });
    }
  }
  return findings;
}

function helmImageDigestFindings(files) {
  const file = "deploy/helm/values.yaml";
  if (!files.includes(file)) {
    return [];
  }
  let content;
  try {
    content = readFileSync(file, "utf8");
  } catch {
    return [];
  }
  const findings = [];
  const emptyDigest = /^\s*digest:\s*["']?["']?\s*$/m.exec(content);
  if (emptyDigest) {
    findings.push({
      file,
      line: lineNumberFor(content, emptyDigest.index),
      rule: "helm_image_digest_required",
      message: "Helm defaults must render digest-pinned images instead of mutable version tags"
    });
  }
  const placeholderDigest = /^\s*digest:\s*["']?sha256:0{64}["']?\s*$/m.exec(content);
  if (placeholderDigest) {
    findings.push({
      file,
      line: lineNumberFor(content, placeholderDigest.index),
      rule: "helm_image_digest_placeholder",
      message: "Helm image digest must be a real release digest, not the all-zero placeholder"
    });
  }
  return findings;
}

function composeProductionImageFindings(files) {
  const composeFiles = files.filter((file) => {
    const name = file.toLowerCase();
    if (!/(^|\/)(docker-compose|compose)[^/]*\.ya?ml$/.test(name)) {
      return false;
    }
    if (/(^|[.-])(dev|demo|local|test)([.-]|$)/.test(name)) {
      return false;
    }
    return /(^|\/)(docker-compose\.ya?ml|compose\.ya?ml)$/.test(name) || /(prod|production)/.test(name);
  });
  const findings = [];
  for (const file of composeFiles) {
    let content;
    try {
      content = readFileSync(file, "utf8");
    } catch {
      continue;
    }
    const lines = content.split(/\r?\n/);
    let inServices = false;
    let service = "";
    let inBuild = false;
    for (let index = 0; index < lines.length; index += 1) {
      const line = lines[index];
      if (/^services:\s*$/.test(line)) {
        inServices = true;
        service = "";
        inBuild = false;
        continue;
      }
      if (!inServices) {
        continue;
      }
      const serviceMatch = line.match(/^  ([A-Za-z0-9_.-]+):\s*$/);
      if (serviceMatch) {
        service = serviceMatch[1];
        inBuild = false;
        continue;
      }
      if (!service || /^\S/.test(line)) {
        inServices = false;
        service = "";
        inBuild = false;
        continue;
      }
      const buildMatch = line.match(/^    build:\s*(.*)$/);
      if (buildMatch) {
        inBuild = true;
        const value = stripInlineComment(buildMatch[1]).trim();
        if (value === "." || value === "" || value.startsWith("{")) {
          findings.push({
            file,
            line: index + 1,
            rule: "compose_production_build_context",
            message: `production-facing Compose service ${service} must not build from the local checkout`
          });
        }
        continue;
      }
      if (inBuild && line.match(/^      context:\s*\.\s*(?:#.*)?$/)) {
        findings.push({
          file,
          line: index + 1,
          rule: "compose_production_build_context",
          message: `production-facing Compose service ${service} must not build from the local checkout`
        });
        continue;
      }
      if (!/^      /.test(line)) {
        inBuild = false;
      }
      const imageMatch = line.match(/^    image:\s*(.+)$/);
      if (!imageMatch) {
        continue;
      }
      const ref = imageFallbackRef(imageMatch[1]);
      if (ref == null) {
        continue;
      }
      if (isLocalImageRef(ref)) {
        findings.push({
          file,
          line: index + 1,
          rule: "compose_production_local_image_fallback",
          message: `production-facing Compose service ${service} must not default to local image ${ref}`
        });
        continue;
      }
      if (!hasSha256Digest(ref)) {
        findings.push({
          file,
          line: index + 1,
          rule: "compose_production_mutable_image",
          message: `production-facing Compose service ${service} image ${ref} must be digest-pinned`
        });
      } else if (hasAllZeroSha256Digest(ref)) {
        findings.push({
          file,
          line: index + 1,
          rule: "compose_production_placeholder_digest",
          message: `production-facing Compose service ${service} image ${ref} must use a real release digest, not the all-zero placeholder`
        });
      }
    }
  }
  return findings;
}

function stripInlineComment(value) {
  return String(value || "").replace(/\s+#.*$/, "");
}

function imageFallbackRef(value) {
  const trimmed = stripInlineComment(value).trim().replace(/^['"]|['"]$/g, "");
  const fallback = trimmed.match(/^\$\{[^}:]+(?::-|-)([^}]+)\}$/);
  if (fallback) {
    return fallback[1].trim();
  }
  if (/^\$\{[^}:]+:\?/.test(trimmed)) {
    return null;
  }
  return trimmed;
}

function hasSha256Digest(ref) {
  return /@sha256:[0-9a-f]{64}$/i.test(ref);
}

function hasAllZeroSha256Digest(ref) {
  return /@sha256:0{64}$/i.test(ref);
}

function isLocalImageRef(ref) {
  return /(^|\/)abra:local$/.test(ref) || /:local$/.test(ref);
}

function assertSelfTest(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function runSelfTest() {
  const originalCwd = process.cwd();
  const root = mkdtempSync(join(tmpdir(), "abra-oss-hygiene-test-"));
  try {
    process.chdir(root);
    mkdirSync(".github/workflows", { recursive: true });
    writeFileSync(
      ".github/workflows/bad.yml",
      [
        "jobs:",
        "  bad:",
        "    steps:",
        "      - uses: actions/checkout@v4",
        "      - uses: docker/login-action@main",
        "      - uses: owner/action-without-ref",
        ""
      ].join("\n")
    );
    writeFileSync(
      ".github/workflows/good.yml",
      [
        "jobs:",
        "  good:",
        "    steps:",
        "      - uses: ./github/actions/local",
        "      - uses: actions/checkout@34e114876b0b11c390a56381ad16ebd13914f8d5",
        "      - uses: 'actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020'",
        ""
      ].join("\n")
    );
    writeFileSync(
      "bad-install.md",
      [
        "# Bad installer docs",
        "curl -fsSL https://raw.githubusercontent.com/" + "example/abra/main/scripts/install.sh | sh",
        ""
      ].join("\n")
    );
    writeFileSync(
      "good-install.md",
      [
        "# Good installer docs",
        "curl -fsSL https://github.com/example/abra/releases/latest/download/install.sh | sh",
        "curl -fsSL https://github.com/example/abra/releases/download/v1.2.3/install.sh | sh",
        ""
      ].join("\n")
    );
    mkdirSync("deploy/kubernetes", { recursive: true });
    writeFileSync(
      "deploy/kubernetes/bad.yaml",
      "image: ghcr.io/example/abra@sha256:" + "0".repeat(64) + "\n"
    );
    mkdirSync("deploy/helm", { recursive: true });
    writeFileSync("deploy/helm/values.yaml", "image:\n  repository: ghcr.io/example/abra\n  digest: \"\"\n");
    writeFileSync(
      "docker-compose.yml",
      [
        "services:",
        "  db:",
        "    image: pgvector/pgvector:pg16",
        "  api:",
        "    build: .",
        "    image: ${ABRA_IMAGE:-abra:local}",
        "    environment:",
        "      NODE_ENV: ${NODE_ENV:-production}",
        "  required:",
        "    image: ${ABRA_IMAGE:?set ABRA_IMAGE}",
        "  pinned:",
        "    image: ghcr.io/example/abra@sha256:" + "0".repeat(64),
        ""
      ].join("\n")
    );
    writeFileSync(
      "docker-compose.dev.yml",
      [
        "services:",
        "  api:",
        "    build: .",
        "    image: abra:local",
        "    environment:",
        "      NODE_ENV: development",
        ""
      ].join("\n")
    );

    const findings = workflowActionRefFindings([
      ".github/workflows/bad.yml",
      ".github/workflows/good.yml"
    ]);
    assertSelfTest(findings.length === 3, `expected 3 workflow ref findings, got ${findings.length}`);
    assertSelfTest(
      findings.every((finding) => finding.file === ".github/workflows/bad.yml"),
      "expected only bad workflow findings"
    );
    assertSelfTest(
      findings.some((finding) => finding.message.includes("actions/checkout@v4")),
      "expected mutable major tag finding"
    );
    assertSelfTest(
      findings.some((finding) => finding.message.includes("docker/login-action@main")),
      "expected mutable branch finding"
    );
    assertSelfTest(
      findings.some((finding) => finding.message.includes("owner/action-without-ref")),
      "expected missing ref finding"
    );
    const installFindings = rawInstallerURLFindings(["bad-install.md", "good-install.md"]);
    assertSelfTest(installFindings.length === 1, `expected 1 raw installer URL finding, got ${installFindings.length}`);
    assertSelfTest(installFindings[0].file === "bad-install.md", "expected bad installer docs finding");
    assertSelfTest(installFindings[0].rule === "raw_branch_installer_url", "expected raw installer URL rule");
    const publicDigestFindings = publicPlaceholderDigestFindings(["deploy/kubernetes/bad.yaml", "scripts/internal.js"]);
    assertSelfTest(publicDigestFindings.length === 1, `expected 1 public placeholder digest finding, got ${publicDigestFindings.length}`);
    assertSelfTest(publicDigestFindings[0].rule === "public_placeholder_digest", "expected public placeholder digest rule");
    const helmFindings = helmImageDigestFindings(["deploy/helm/values.yaml"]);
    assertSelfTest(helmFindings.length === 1, `expected 1 Helm image digest finding, got ${helmFindings.length}`);
    assertSelfTest(helmFindings[0].rule === "helm_image_digest_required", "expected Helm image digest rule");
    const composeFindings = composeProductionImageFindings(["docker-compose.yml", "docker-compose.dev.yml"]);
    assertSelfTest(composeFindings.length === 4, `expected 4 Compose image findings, got ${composeFindings.length}`);
    assertSelfTest(
      composeFindings.some((finding) => finding.rule === "compose_production_mutable_image" && finding.message.includes("db")),
      "expected mutable production Compose image finding"
    );
    assertSelfTest(
      composeFindings.some((finding) => finding.rule === "compose_production_build_context" && finding.message.includes("api")),
      "expected production Compose local build finding"
    );
    assertSelfTest(
      composeFindings.some((finding) => finding.rule === "compose_production_local_image_fallback" && finding.message.includes("api")),
      "expected production Compose local image fallback finding"
    );
    assertSelfTest(
      composeFindings.some((finding) => finding.rule === "compose_production_placeholder_digest" && finding.message.includes("pinned")),
      "expected production Compose all-zero placeholder digest finding"
    );
    assertSelfTest(
      !composeFindings.some((finding) => finding.message.includes("service required ") || finding.file.includes("dev")),
      "expected required env image and dev override to be ignored"
    );
  } finally {
    process.chdir(originalCwd);
    rmSync(root, { recursive: true, force: true });
  }
  console.log("OSS hygiene self-test passed.");
}

if (process.argv.includes("--self-test")) {
  runSelfTest();
  process.exit(0);
}

const files = trackedFiles();
const findings = [
  ...files.flatMap(scanFile),
  ...workflowActionRefFindings(files),
  ...rawInstallerURLFindings(files),
  ...publicPlaceholderDigestFindings(files),
  ...helmImageDigestFindings(files),
  ...composeProductionImageFindings(files)
];

if (findings.length > 0) {
  console.error("OSS hygiene check failed. Remove private context or secrets before publishing:");
  for (const finding of findings) {
    console.error(`${finding.file}:${finding.line}: ${finding.rule}: ${finding.message}`);
  }
  process.exit(1);
}

console.log("OSS hygiene check passed.");
