#!/usr/bin/env node

import { readFileSync } from "node:fs";

const matrixPath = process.env.ABRA_BRAIN_BENCHMARK_MATRIX || "benchmarks/agent-memory-comparison.json";
const matrix = JSON.parse(readFileSync(matrixPath, "utf8"));
const values = matrix.score_values || { strong: 1, partial: 0.5, unknown: 0 };

function capabilityScore(system) {
  let earned = 0;
  let possible = 0;
  for (const dimension of matrix.dimensions) {
    const weight = Number(dimension.weight || 1);
    possible += weight;
    earned += weight * Number(values[system.scores?.[dimension.id]] ?? 0);
  }
  return possible === 0 ? 0 : earned / possible;
}

function assertShape() {
  const dimensionIDs = new Set(matrix.dimensions.map((dimension) => dimension.id));
  if (dimensionIDs.size !== matrix.dimensions.length) {
    throw new Error("benchmark dimensions must have unique ids");
  }
  for (const system of matrix.systems) {
    for (const id of dimensionIDs) {
      if (!(id in system.scores)) {
        throw new Error(`${system.id} missing score for ${id}`);
      }
      if (!(system.scores[id] in values)) {
        throw new Error(`${system.id}.${id} has unknown rating ${system.scores[id]}`);
      }
    }
  }
}

assertShape();

const systems = matrix.systems
  .map((system) => ({ ...system, capability_score: Number(capabilityScore(system).toFixed(3)) }))
  .sort((left, right) => right.capability_score - left.capability_score);

const report = {
  suite: "abra-brain-benchmark",
  status: "passed",
  score_kind: "normalized_capability_score",
  score_scale: "0.000 to 1.000",
  score_note: "Capability scores compare documented brain features. They are not latency, throughput, token count, or accuracy measurements.",
  matrix: matrixPath,
  updated: matrix.updated,
  reporting_rule: matrix.reporting_rule,
  systems: systems.map((system) => ({
    id: system.id,
    name: system.name,
    capability_score: system.capability_score,
    source: system.source
  })),
  dimensions: matrix.dimensions.map((dimension) => ({
    id: dimension.id,
    label: dimension.label,
    weight: dimension.weight
  }))
};

console.log(JSON.stringify(report, null, 2));
