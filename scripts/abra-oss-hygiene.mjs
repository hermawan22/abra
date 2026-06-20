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
  /^coverage\//,
  /^scripts\/abra-oss-hygiene\.mjs$/,
  /(^|\/)package-lock\.json$/
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
    id: "private_org_name",
    pattern: /\bAmartha\b|creafilo\.com|support@creafilo\.com|next-insurance|bitbucket\.org\/Amartha/i,
    message: "private organization context detected"
  },
  {
    id: "private_registry_credentials",
    pattern: /\bNEXUS_(?:USER|PASSWORD|TOKEN)\b/,
    message: "private registry credential name detected"
  }
];

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
  for (const rule of forbiddenPatterns) {
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
  ...workflowActionRefFindings(files)
];

if (findings.length > 0) {
  console.error("OSS hygiene check failed. Remove private context or secrets before publishing:");
  for (const finding of findings) {
    console.error(`${finding.file}:${finding.line}: ${finding.rule}: ${finding.message}`);
  }
  process.exit(1);
}

console.log("OSS hygiene check passed.");
