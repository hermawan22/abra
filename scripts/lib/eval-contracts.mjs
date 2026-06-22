export const compatibleHybridRetrievalModes = Object.freeze(["hybrid", "hybrid_reranked"]);

export function isHybridRetrievalMode(mode) {
  return compatibleHybridRetrievalModes.includes(String(mode || ""));
}

export function assertHybridRetrievalMode(mode, label = "recall") {
  if (!isHybridRetrievalMode(mode)) {
    throw new Error(
      `${label} retrieval mode = ${mode || "<missing>"}, want one of ${compatibleHybridRetrievalModes.join(", ")}`
    );
  }
}
