export function requireTokenForRemoteBaseURL(rawBaseUrl) {
  const url = new URL(rawBaseUrl);
  const loopback = ["127.0.0.1", "localhost", "::1", "[::1]"].includes(url.hostname);
  if (!loopback && !process.env.ABRA_API_TOKEN && process.env.ABRA_ALLOW_DEV_TOKEN !== "1") {
    throw new Error("ABRA_API_TOKEN is required when ABRA_BASE_URL is not loopback. Set ABRA_ALLOW_DEV_TOKEN=1 only for isolated test environments.");
  }
}

export function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

export function textOf(value) {
  return JSON.stringify(value).toLowerCase();
}

export function retrievedMemoryText(packet) {
  return textOf({
    summaries: packet.summaries,
    facts: packet.facts,
    supporting_documents: packet.supporting_documents,
    graph_context: packet.graph_context,
    evidence: packet.evidence
  });
}

export function skipped(reason, details = {}) {
  return { skipped: true, reason, details };
}

export function rankClaim(claims, { contains, source }) {
  const needle = contains.toLowerCase();
  const index = claims.findIndex((claim) => {
    const claimText = String(claim.claim_text || "").toLowerCase();
    return claimText.includes(needle) && (!source || claim.source_url === source);
  });
  return index === -1 ? null : index + 1;
}

export function countHitAt(ranks, cutoff) {
  return ranks.filter((rank) => rank !== null && rank <= cutoff).length;
}

export function approvalGateBlocked(response) {
  if ([202, 401, 403, 409, 412, 423, 425, 428].includes(response.status)) {
    return true;
  }
  return response.status >= 400 && /\bapproval|review|pending|required\b/i.test(response.raw);
}
