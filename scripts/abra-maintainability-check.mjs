#!/usr/bin/env node

import { existsSync, readFileSync } from "node:fs";
import { readdirSync, statSync } from "node:fs";
import { dirname, join, normalize } from "node:path";

const lineBudgets = [
  {
    path: "cmd/abra/main.go",
    max: 700,
    reason: "CLI router/orchestration hotspot; move cohesive command families to sibling files before adding more surface."
  },
  {
    path: "cmd/abra/main_test.go",
    max: 700,
    reason: "CLI tests should stay split by command family so public behavior remains reviewable."
  },
  {
    path: "cmd/abra/ingest_cli.go",
    max: 700,
    reason: "direct ingest should stay focused; source, connector, and approval command families belong in sibling files."
  },
  {
    path: "cmd/abra/source_connect_cli.go",
    max: 500,
    reason: "source registration/sync onboarding should remain reviewable and avoid absorbing connector operations."
  },
  {
    path: "cmd/abra/connector_cli.go",
    max: 500,
    reason: "connector onboarding should stay separate from source lifecycle and direct ingest behavior."
  },
  {
    path: "cmd/abra/source_ops_cli.go",
    max: 450,
    reason: "source lifecycle operations should stay compact; split new job/status views before this becomes a shell UI."
  },
  {
    path: "cmd/abra/approvals_cli.go",
    max: 250,
    reason: "approval CLI must stay small because governance behavior lives in the server/store layer."
  },
  {
    path: "cmd/abra/runtime_assets.go",
    max: 1100,
    reason: "runtime asset/bootstrap helpers should not absorb CLI help or command behavior."
  },
  {
    path: "cmd/abra/runtime_help.go",
    max: 700,
    reason: "operator help text should remain centralized but small enough to review in one pass."
  },
  {
    path: "internal/store/store.go",
    max: 400,
    reason: "database bootstrap/query hotspot; put new aggregate-specific queries in focused store files."
  },
  {
    path: "internal/brain/service.go",
    max: 400,
    reason: "brain service bootstrap should stay thin; provider, ingest, graph, summary, and text logic belong in focused files."
  },
  {
    path: "internal/memory/composer.go",
    max: 700,
    reason: "brain composition hotspot; split new scoring, formatting, or policy logic into focused files."
  },
  {
    path: "internal/server/server.go",
    max: 300,
    reason: "HTTP/MCP server bootstrap hotspot; put new handlers or schemas in focused files."
  },
  {
    path: "internal/server/mcp_tool_calls.go",
    max: 1150,
    reason: "MCP dispatcher hotspot; put large new tool families in dedicated files."
  },
  {
    path: "internal/server/mcp_tool_schemas.go",
    max: 700,
    reason: "MCP schema hotspot; keep schema growth deliberate and covered by discoverability tests."
  },
  {
    path: "scripts/abra-smoke.sh",
    max: 1100,
    reason: "smoke orchestration should not embed large assertion programs; move assertions into scripts/lib."
  },
  {
    path: "scripts/lib/smoke-assertions.cjs",
    max: 800,
    reason: "smoke assertions should stay reusable and split again if more scenarios are added."
  },
  {
    path: "scripts/abra-tier23-eval.mjs",
    max: 1500,
    reason: "Tier 2/3 eval remains scenario-oriented, but pure helpers should live under scripts/lib."
  },
  {
    path: "scripts/lib/tier23-helpers.mjs",
    max: 120,
    reason: "Tier 2/3 helpers should remain generic; scenario logic belongs in the eval script."
  }
];

const requiredFiles = [
  "CONTRIBUTING.md",
  "CODE_OF_CONDUCT.md",
  "SECURITY.md",
  "LICENSE",
  "docs/ARCHITECTURE.md",
  "docs/BENCHMARKS.md",
  "docs/REPOSITORY_LAYOUT.md",
  "docs/EXTENSIONS.md",
  "docs/PLUGIN_AUTHORING.md",
  "docs/FEATURE_FREEZE.md",
  "docs/assets/abra-system.svg",
  "docs/assets/governed-learning-loop.svg",
  "docs/assets/cognitive-loop.svg",
  "docs/assets/token-efficiency.svg",
  "docs/assets/benchmark-dimensions.svg",
  "benchmarks/agent-memory-comparison.json",
  "migrations/README.md",
  "examples/connectors/README.md",
  "examples/connectors/mcp-knowledge-base.connector.json",
  ".github/pull_request_template.md",
  ".github/ISSUE_TEMPLATE/bug_report.yml",
  ".github/ISSUE_TEMPLATE/feature_request.yml"
];

const requiredDocPhrases = [
  {
    path: "README.md",
    phrases: [
      "MCP-first for agents",
      "CLI for operators",
      "HTTP as transport"
    ]
  },
  {
    path: "docs/ARCHITECTURE.md",
    phrases: [
      "MCP is the canonical agent interface",
      "Core owns governance",
      "Plugins do not write trusted memory directly"
    ]
  },
  {
    path: "docs/BENCHMARKS.md",
    phrases: [
      "Token efficiency",
      "No-LLM default query path",
      "Benchmark Protocol"
    ]
  },
  {
    path: "docs/REPOSITORY_LAYOUT.md",
    phrases: [
      "cmd/",
      "internal/",
      "migrations/"
    ]
  },
  {
    path: "docs/PLUGIN_AUTHORING.md",
    phrases: [
      "normalized document",
      "MCP exporter",
      "signed webhook",
      "must not include secrets"
    ]
  },
  {
    path: "migrations/README.md",
    phrases: [
      "append-only",
      "schema_migrations",
      "NNN_short_descriptive_slug.sql"
    ]
  },
  {
    path: "CONTRIBUTING.md",
    phrases: [
      "Architecture map",
      "Plugin contributions",
      "Run the fast checks"
    ]
  }
];

const markdownForbiddenTerms = [
  "Slaude",
  "gbrain",
  "kalah",
  "belom",
  "next possible",
  "Remaining Gaps",
  "Priority Roadmap",
  "competitor audit",
  "CLI-only"
];

const failures = [];

function markdownFiles(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    if (name === ".git" || name === "node_modules" || name === ".tmp") {
      continue;
    }
    const path = join(dir, name);
    const stat = statSync(path);
    if (stat.isDirectory()) {
      out.push(...markdownFiles(path));
    } else if (name.endsWith(".md")) {
      out.push(path.replace(/^\.\//, ""));
    }
  }
  return out;
}

function stripAnchor(target) {
  const hashIndex = target.indexOf("#");
  if (hashIndex < 0) {
    return target;
  }
  return target.slice(0, hashIndex);
}

function isExternalLink(target) {
  return /^[a-z][a-z0-9+.-]*:/i.test(target) || target.startsWith("mailto:");
}

function localMarkdownLinkFindings(file, text) {
  const findings = [];
  const linkPattern = /!?\[[^\]\n]*\]\(([^)\n]+)\)/g;
  for (const match of text.matchAll(linkPattern)) {
    const rawTarget = match[1].trim();
    if (!rawTarget || rawTarget.startsWith("#") || isExternalLink(rawTarget)) {
      continue;
    }
    const withoutTitle = rawTarget.match(/^<([^>]+)>$/)?.[1] || rawTarget.split(/\s+["'][^"']*["']\s*$/)[0];
    const targetPath = stripAnchor(withoutTitle);
    if (!targetPath || targetPath.startsWith("#")) {
      continue;
    }
    const resolved = normalize(join(dirname(file), decodeURIComponent(targetPath)));
    if (resolved.startsWith("..")) {
      findings.push(`${file}: local markdown link escapes repository: ${rawTarget}`);
      continue;
    }
    if (!existsSync(resolved)) {
      findings.push(`${file}: local markdown link target is missing: ${rawTarget}`);
    }
  }
  return findings;
}

for (const file of requiredFiles) {
  if (!existsSync(file)) {
    failures.push(`${file}: required contributor/OSS file is missing`);
  }
}

for (const budget of lineBudgets) {
  if (!existsSync(budget.path)) {
    failures.push(`${budget.path}: tracked hotspot is missing`);
    continue;
  }
  const lines = readFileSync(budget.path, "utf8").split(/\r?\n/).length;
  if (lines > budget.max) {
    failures.push(`${budget.path}: ${lines} lines exceeds budget ${budget.max}. ${budget.reason}`);
  }
}

for (const doc of requiredDocPhrases) {
  if (!existsSync(doc.path)) {
    continue;
  }
  const text = readFileSync(doc.path, "utf8");
  for (const phrase of doc.phrases) {
    if (!text.includes(phrase)) {
      failures.push(`${doc.path}: missing required phrase ${JSON.stringify(phrase)}`);
    }
  }
}

for (const file of markdownFiles(".")) {
  const text = readFileSync(file, "utf8");
  failures.push(...localMarkdownLinkFindings(file, text));
  for (const term of markdownForbiddenTerms) {
    if (text.includes(term)) {
      failures.push(`${file}: public markdown contains internal or outdated positioning term ${JSON.stringify(term)}`);
    }
  }
  for (const match of text.matchAll(/```text\n([\s\S]*?)```/g)) {
    if (match[1].includes("->")) {
      failures.push(`${file}: text diagram with arrows should be an image asset, not a fenced text block`);
    }
  }
}

if (failures.length > 0) {
  console.error("Maintainability check failed:");
  for (const failure of failures) {
    console.error(`- ${failure}`);
  }
  process.exit(1);
}

console.log("Maintainability check passed.");
