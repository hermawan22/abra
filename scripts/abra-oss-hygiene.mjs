#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

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

const findings = trackedFiles().flatMap(scanFile);

if (findings.length > 0) {
  console.error("OSS hygiene check failed. Remove private context or secrets before publishing:");
  for (const finding of findings) {
    console.error(`${finding.file}:${finding.line}: ${finding.rule}: ${finding.message}`);
  }
  process.exit(1);
}

console.log("OSS hygiene check passed.");
