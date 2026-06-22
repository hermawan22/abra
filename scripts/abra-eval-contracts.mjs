#!/usr/bin/env node

import { readFile } from "node:fs/promises";

import { compatibleHybridRetrievalModes, isHybridRetrievalMode } from "./lib/eval-contracts.mjs";

const scanTargets = [
  "scripts/abra-tier1-eval.mjs",
  "scripts/abra-tier23-eval.mjs",
  "scripts/abra-golden-eval.mjs",
  "scripts/abra-provider-benchmark.mjs",
  "scripts/abra-perf-eval.mjs",
  "scripts/abra-smoke.sh"
];

function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

assert(isHybridRetrievalMode("hybrid"), "hybrid mode must remain compatible");
assert(isHybridRetrievalMode("hybrid_reranked"), "hybrid_reranked mode must remain compatible");
assert(!isHybridRetrievalMode("lexical"), "lexical fallback must not satisfy hybrid-mode evals");

const brittlePatterns = [
  /\bretrieval_mode\s*={2,3}\s*["']hybrid["']/,
  /\bretrieval_mode\s*!={1,2}\s*["']hybrid["']/
];

const violations = [];
for (const target of scanTargets) {
  const source = await readFile(target, "utf8");
  for (const [index, line] of source.split(/\r?\n/).entries()) {
    if (brittlePatterns.some((pattern) => pattern.test(line))) {
      violations.push(`${target}:${index + 1}: ${line.trim()}`);
    }
  }
}

if (violations.length > 0) {
  console.error("Eval scripts must accept all compatible hybrid retrieval modes:");
  console.error(`allowed: ${compatibleHybridRetrievalModes.join(", ")}`);
  for (const violation of violations) {
    console.error(`- ${violation}`);
  }
  process.exit(1);
}

console.log(`ok eval retrieval mode contract: ${compatibleHybridRetrievalModes.join(", ")}`);
