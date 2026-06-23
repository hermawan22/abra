const fs = require("fs");
const path = require("path");
const dir = process.argv[2];
function read(name) {
  return JSON.parse(fs.readFileSync(path.join(dir, name), "utf8"));
}
function mcpTextPayload(response) {
  if (!response.result || !Array.isArray(response.result.content) || !response.result.content[0] || typeof response.result.content[0].text !== "string") {
    throw new Error("mcp tool response did not include text content");
  }
  return JSON.parse(response.result.content[0].text);
}
const ready = read("ready.json");
const ingest = read("ingest.json");
const refreshInitial = read("refresh-initial.json");
const refreshUpdated = read("refresh-updated.json");
const refreshObsoleteRecall = read("refresh-obsolete-recall.json");
const refreshCurrentRecall = read("refresh-current-recall.json");
const graphWarningIngest = read("graph-warning-ingest.json");
const graphWarningMemory = read("graph-warning-memory.json");
const graphWarningConflicts = read("graph-warning-conflicts.json");
const graphWarningRelationFilterConflicts = read("graph-warning-conflicts-relation-filter.json");
const codeIngest = read("code-ingest.json");
const codeRefreshInitial = read("code-refresh-initial.json");
const codeRefreshUpdated = read("code-refresh-updated.json");
const codeRefreshLegacySummariesInitial = read("code-refresh-legacy-summaries-initial.json");
const codeRefreshLegacySummariesUpdated = read("code-refresh-legacy-summaries-updated.json");
const codeRefreshReplacementSummariesUpdated = read("code-refresh-replacement-summaries-updated.json");
const codeRefreshRelationsInitial = read("code-refresh-relations-initial.json");
const codeRefreshRelationsUpdated = read("code-refresh-relations-updated.json");
const webhook = read("webhook.json");
const webhookDuplicate = read("webhook-duplicate.json");
const webhookJob = read("webhook-job.json");
const recall = read("recall.json");
const policy = read("policy.json");
const memory = read("memory.json");
const profileMemory = read("profile-memory.json");
const memoryHealth = read("memory-health.json");
const summaries = read("summaries.json");
const rebuildSummaries = read("rebuild-summaries.json");
const learningProposal = read("learning-proposal.json");
const learningProposals = read("learning-proposals.json");
const learningProposalDecision = read("learning-proposal-decision.json");
const observation = read("observation.json");
const observationsRaw = read("observations-raw.json");
const observationLearningProposal = read("observation-learning-proposal.json");
const observationsProposed = read("observations-proposed.json");
const observationLearningProposalDecision = read("observation-learning-proposal-decision.json");
const observationRecallAfterProposal = read("observation-recall-after-proposal.json");
const observationProposedAudit = read("observation-proposed-audit.json");
const sourceApproval = read("source-approval.json");
const sourceApprovalDecision = read("source-approval-decision.json");
const sourceStatusApproval = read("source-status-approval.json");
const sourceStatusApprovalDecision = read("source-status-approval-decision.json");
const aclApproval = read("acl-approval.json");
const aclApprovalDecision = read("acl-approval-decision.json");
const mcpAclApproval = read("mcp-acl-approval.json");
const mcpAclApprovalDecision = read("mcp-acl-approval-decision.json");
const agentPolicyApproval = read("agent-policy-approval.json");
const agentPolicyApprovalDecision = read("agent-policy-approval-decision.json");
const mcpAgentPolicyApproval = read("mcp-agent-policy-approval.json");
const mcpAgentPolicyApprovalDecision = read("mcp-agent-policy-approval-decision.json");
const agentProfileApproval = read("agent-profile-approval.json");
const agentProfileApprovalDecision = read("agent-profile-approval-decision.json");
const rebuildApproval = read("rebuild-approval.json");
const rebuildApprovalDecision = read("rebuild-approval-decision.json");
const aclPolicy = read("acl-policy.json");
const aclPolicyAudit = read("acl-policy-audit.json");
const aclDecision = read("acl-decision.json");
const aclDeny = read("acl-deny.json");
const agentPolicy = read("agent-policy.json");
const agentPolicies = read("agent-policies.json");
const agentPolicyAudit = read("agent-policy-audit.json");
const agentPolicyDecision = read("agent-policy-decision.json");
const agentProfile = read("agent-profile.json");
const agentProfiles = read("agent-profiles.json");
const agentProfileAudit = read("agent-profile-audit.json");
const conflictClaimA = read("conflict-claim-a.json");
const conflictClaimB = read("conflict-claim-b.json");
const conflictChallenge = read("conflict-challenge.json");
const conflictMemory = read("conflict-memory.json");
const conflictsOpen = read("conflicts-open.json");
const conflictResolved = read("conflict-resolved.json");
const conflictMemoryResolved = read("conflict-memory-resolved.json");
const source = read("source.json");
const sourcePaused = read("source-paused.json");
const sourceResumed = read("source-resumed.json");
	const sourceAuditEvents = read("source-audit-events.json");
	const mcpInit = read("mcp-init.json");
	const mcp = read("mcp.json");
	const mcpSummaries = read("mcp-summaries.json");
	const mcpMemory = read("mcp-memory.json");
	const mcpMemoryHealth = read("mcp-memory-health.json");
	const approval = read("approval.json");
	const approvalDecision = read("approval-decision.json");
	const mcpApproval = read("mcp-approval.json");
	const mcpTools = read("mcp-tools.json");
	const mcpResources = read("mcp-resources.json");
	const mcpResourceTemplates = read("mcp-resource-templates.json");
	const mcpResourceGuide = read("mcp-resource-guide.json");
	const mcpResourceHealth = read("mcp-resource-health.json");
	const mcpPrompts = read("mcp-prompts.json");
	const mcpPromptBeforeCode = read("mcp-prompt-before-code.json");
const mcpIngestDocument = read("mcp-ingest-document.json");
const mcpIngestDocuments = read("mcp-ingest-documents.json");
const mcpSourceConfig = read("mcp-source-config.json");
const mcpSourceConfigs = read("mcp-source-configs.json");
const mcpIngestionJob = read("mcp-ingestion-job.json");
const mcpIngestionJobs = read("mcp-ingestion-jobs.json");
const mcpSourceConfigAudit = read("mcp-source-config-audit.json");
	const mcpAclPolicy = read("mcp-acl-policy.json");
const mcpAclPolicies = read("mcp-acl-policies.json");
const mcpAclDecision = read("mcp-acl-decision.json");
const mcpAclPolicyAudit = read("mcp-acl-policy-audit.json");
const mcpAgentPolicy = read("mcp-agent-policy.json");
const mcpAgentPolicies = read("mcp-agent-policies.json");
const mcpAgentPolicyDecision = read("mcp-agent-policy-decision.json");
const mcpAgentPolicyAudit = read("mcp-agent-policy-audit.json");
const mcpAgentProfiles = read("mcp-agent-profiles.json");
const mcpObservation = read("mcp-observation.json");
const mcpObservationLearning = read("mcp-observation-learning.json");
const mcpObservationsProposed = read("mcp-observations-proposed.json");
const mcpObservationLearningDecision = read("mcp-observation-learning-decision.json");
const mcpObservationRecallAfterProposal = read("mcp-observation-recall-after-proposal.json");
const mcpObservationProposedAudit = read("mcp-observation-proposed-audit.json");
const mcpLearning = read("mcp-learning.json");
const mcpLearningDecision = read("mcp-learning-decision.json");
const mcpLearningProposedAudit = read("mcp-learning-proposed-audit.json");
const mcpLearningDecidedAudit = read("mcp-learning-decided-audit.json");
const mcpConflicts = read("mcp-conflicts.json");
const mcpConflictResolve = read("mcp-conflict-resolve.json");
const jobs = read("jobs.json");
const metricsAfter = fs.readFileSync(path.join(dir, "metrics-after.txt"), "utf8");
if (!ready.ok) throw new Error("readyz did not report ok");
if (typeof ready.tracing_enabled !== "boolean") throw new Error("readyz did not expose tracing_enabled");
if (!ingest.document_id || ingest.chunks < 1) throw new Error("ingest did not write a document chunk");
if (!refreshInitial.document_id || refreshInitial.claims < 2) {
  throw new Error("refresh fixture initial ingest did not write enough claims");
}
if (!refreshUpdated.document_id || refreshUpdated.deprecated_claims < 2) {
  throw new Error("refresh fixture update did not deprecate previous source claims");
}
if (Array.isArray(refreshObsoleteRecall.claims) && refreshObsoleteRecall.claims.some((claim) => String(claim.claim_text || "").includes("obsolete cache"))) {
  throw new Error("source refresh did not remove obsolete claim from trusted recall");
}
if (!Array.isArray(refreshCurrentRecall.claims) || !refreshCurrentRecall.claims.some((claim) => String(claim.claim_text || "").includes("React Query") && claim.status === "verified")) {
  throw new Error("source refresh did not reactivate the still-present claim");
}
if (!graphWarningIngest.document_id || graphWarningIngest.relations < 2) {
  throw new Error("graph warning fixture did not ingest competing graph relations");
}
const graphWarnings = Array.isArray(graphWarningMemory.graph_warnings) ? graphWarningMemory.graph_warnings : [];
const graphConflicts = Array.isArray(graphWarningMemory.conflicts) ? graphWarningMemory.conflicts : [];
const hasCompetingGraphWarning = graphWarnings.some((warning) => warning.warning_type === "competing_graph_alternatives" && warning.severity === "high");
const hasRelationConflict = graphConflicts.some((conflict) => conflict.primary_relation_id && conflict.conflicting_relation_id);
const hasActiveConflict = graphConflicts.some((conflict) => conflict.status === "open");
if (!hasCompetingGraphWarning && !hasActiveConflict) {
  throw new Error("working memory did not surface competing graph alternatives or active conflicts");
}
if (hasCompetingGraphWarning && !graphWarnings.some((warning) => Array.isArray(warning.relations) && warning.relations.some((relation) => relation.id))) {
  throw new Error("working memory graph warnings did not preserve relation ids");
}
if (!graphWarningMemory.stats || graphWarningMemory.stats.graph_warnings !== graphWarnings.length) {
  throw new Error("working memory graph warning stats did not match graph warnings");
}
if (!graphWarningMemory.verification || !["partial", "unsafe"].includes(graphWarningMemory.verification.verdict)) {
  throw new Error("working memory verifier did not mark competing graph memory as requiring review");
}
if (!hasActiveConflict) {
  throw new Error("working memory did not surface active conflicts");
}
const graphRequiredActions = Array.isArray(graphWarningMemory.agent_decision?.required_actions) ? graphWarningMemory.agent_decision.required_actions : [];
if (!graphWarningMemory.agent_decision || graphWarningMemory.agent_decision.autonomous_allowed !== false || !["caution", "needs_review", "blocked"].includes(graphWarningMemory.agent_decision.decision) || !graphRequiredActions.some((action) => ["review_graph_warnings", "resolve_active_conflicts", "review_conflict_evidence"].includes(action))) {
  throw new Error("working memory agent gate did not require graph conflict review");
}
if (!Array.isArray(graphWarningMemory.agent_decision.allowed_next_actions) || !graphWarningMemory.agent_decision.allowed_next_actions.includes("list_conflicts") || graphWarningMemory.agent_decision.allowed_next_actions.includes("request_approval")) {
  throw new Error("working memory agent gate did not route relation conflicts to conflict review");
}
if (!Array.isArray(graphWarningConflicts.conflicts) || !graphWarningConflicts.conflicts.some((conflict) => conflict.primary_relation_id && conflict.conflicting_relation_id && conflict.detected_by === "auto-graph-detector")) {
  throw new Error("graph warning fixture did not persist a relation conflict");
}
if (!Array.isArray(graphWarningRelationFilterConflicts.conflicts) || graphWarningRelationFilterConflicts.conflicts.length < 1 || !graphWarningRelationFilterConflicts.conflicts.every((conflict) => conflict.primary_relation_id || conflict.conflicting_relation_id)) {
  throw new Error("relation_id conflict filter did not return relation conflicts");
}
if (!codeIngest.document_id || codeIngest.entities < 1 || codeIngest.relations < 1) {
  throw new Error("code ingest did not write structural graph entities and relations");
}
if (!codeRefreshInitial.document_id || codeRefreshInitial.entities < 1 || codeRefreshInitial.relations < 1) {
  throw new Error("code refresh fixture initial ingest did not write graph intelligence");
}
if (!Array.isArray(codeRefreshLegacySummariesInitial.summaries) || !codeRefreshLegacySummariesInitial.summaries.some((summary) => summary.key === "legacy-ui")) {
  throw new Error("code refresh fixture did not create the initial legacy-ui package summary");
}
if (!Array.isArray(codeRefreshRelationsInitial.relations) || !codeRefreshRelationsInitial.relations.some((relation) => relation.to_entity === "legacy-ui" && relation.status === "active")) {
  throw new Error("code refresh fixture did not create the initial legacy-ui active relation");
}
if (!codeRefreshUpdated.document_id || codeRefreshUpdated.deprecated_relations < 1 || codeRefreshUpdated.deleted_summaries < 1) {
  throw new Error("code source refresh did not reconcile previous graph relations and summaries");
}
if (Array.isArray(codeRefreshLegacySummariesUpdated.summaries) && codeRefreshLegacySummariesUpdated.summaries.some((summary) => summary.key === "legacy-ui")) {
  throw new Error("code source refresh left the obsolete legacy-ui package summary active");
}
if (!Array.isArray(codeRefreshReplacementSummariesUpdated.summaries) || !codeRefreshReplacementSummariesUpdated.summaries.some((summary) => summary.key === "example-ui-kit")) {
  throw new Error("code source refresh did not create the replacement example-ui-kit package summary");
}
if (Array.isArray(codeRefreshRelationsUpdated.relations) && codeRefreshRelationsUpdated.relations.some((relation) => relation.to_entity === "legacy-ui" && relation.status === "active")) {
  throw new Error("code source refresh left the obsolete legacy-ui relation active");
}
if (!Array.isArray(codeRefreshRelationsUpdated.relations) || !codeRefreshRelationsUpdated.relations.some((relation) => relation.to_entity === "example-ui-kit" && relation.status === "active")) {
  throw new Error("code source refresh did not create the replacement example-ui-kit active relation");
}
if (webhook.accepted !== 1 || !Array.isArray(webhook.documents) || webhook.documents[0].source_url !== "https://jira.example.invalid/browse/ABRA-" + process.env.STAMP) {
  throw new Error("signed webhook did not ingest exactly one connector document");
}
if (!webhook.documents[0].ingestion_job_id || !["queued", "retry", "running", "succeeded"].includes(webhook.documents[0].job_status)) {
  throw new Error("signed webhook did not return an accepted ingestion job");
}
if (!Array.isArray(webhookJob.ingestion_jobs) || !webhookJob.ingestion_jobs.some((job) => job.id === webhook.documents[0].ingestion_job_id && job.status === "succeeded")) {
  throw new Error("signed webhook ingestion job did not finish through the worker");
}
if (webhookDuplicate.accepted !== 1 || !Array.isArray(webhookDuplicate.documents) || webhookDuplicate.documents[0].duplicate !== true || webhookDuplicate.documents[0].ingestion_job_id !== webhook.documents[0].ingestion_job_id) {
  throw new Error("signed webhook redelivery was not treated as an idempotent duplicate");
}
if (!Array.isArray(recall.supporting_documents) || recall.supporting_documents.length < 1) {
  throw new Error("recall did not return supporting documents");
}
if (!["hybrid", "hybrid_reranked"].includes(recall.retrieval_mode)) {
  throw new Error(`recall did not use hybrid-compatible retrieval: ${recall.retrieval_mode}`);
}
if (!recall.claims.every((claim) => Number.isFinite(Number(claim.text_score)) && Number.isFinite(Number(claim.vector_score)))) {
  throw new Error("recall claims did not include text/vector score components");
}
if (!policy.required || !Array.isArray(policy.queries) || policy.queries.length < 1) {
  throw new Error("policy plan did not require recall queries");
}
const memoryReadyChecks = [
  ["intent", !!memory.intent],
  ["strategy", !!memory.strategy],
  ["retrieval_plan.mode", !!memory.retrieval_plan?.mode],
  ["retrieval_plan.budget.context_tokens", Number(memory.retrieval_plan?.budget?.context_tokens || 0) >= 300],
  ["retrieval_trace", Array.isArray(memory.retrieval_trace) && memory.retrieval_trace.length >= 1],
  ["memory_health.signals", !!memory.memory_health?.status && Array.isArray(memory.memory_health?.signals) && memory.memory_health.signals.length >= 1],
  ["verification.verdict", !!memory.verification?.verdict],
  ["agent_policy_decisions", Array.isArray(memory.agent_policy_decisions) && memory.agent_policy_decisions.length >= 1],
  ["agent_decision", !!memory.agent_decision?.decision && Array.isArray(memory.agent_decision?.allowed_next_actions)],
  ["context_window", Array.isArray(memory.context_window?.blocks) && memory.context_window.blocks.length >= 1 && !!memory.context_window.prompt && memory.context_window.estimated_tokens >= 1 && memory.context_window.estimated_tokens <= memory.context_window.max_tokens],
  ["learning_suggestions", Array.isArray(memory.learning_suggestions)],
  ["summaries", Array.isArray(memory.summaries) && memory.summaries.length >= 1],
  ["facts/supporting_documents", Array.isArray(memory.facts) && Array.isArray(memory.supporting_documents)],
  ["impact_map", Array.isArray(memory.impact_map) && memory.impact_map.length >= 1],
  ["validation_plan", Array.isArray(memory.validation_plan) && memory.validation_plan.length >= 1],
  ["stats.queries_run", Number(memory.stats?.queries_run || 0) >= 1],
  ["stats.graph_relations", Number(memory.stats?.graph_relations || 0) >= 1],
  ["stats.graph_queries", Number(memory.stats?.graph_queries || 0) >= 1],
  ["stats.health_signals", memory.stats?.health_signals === memory.memory_health?.signals?.length],
  ["stats.graph_warnings", memory.stats?.graph_warnings === (Array.isArray(memory.graph_warnings) ? memory.graph_warnings.length : 0)],
  ["stats.impact_items", memory.stats?.impact_items === memory.impact_map?.length],
  ["stats.validation_steps", memory.stats?.validation_steps === memory.validation_plan?.length],
  ["stats.context_blocks", memory.stats?.context_blocks === memory.context_window?.blocks?.length],
  ["stats.context_tokens", memory.stats?.context_tokens === memory.context_window?.estimated_tokens],
  ["stats.context_dropped_blocks", memory.stats?.context_dropped_blocks === (Array.isArray(memory.context_window?.dropped_blocks) ? memory.context_window.dropped_blocks.length : 0)],
  ["stats.retrieval_trace_items", memory.stats?.retrieval_trace_items === memory.retrieval_trace?.length],
  ["stats.retrieval_warnings", memory.stats?.retrieval_warnings === (Array.isArray(memory.retrieval_warnings) ? memory.retrieval_warnings.length : 0)],
  ["stats.total_duration_ms", Number(memory.stats?.total_duration_ms || 0) >= 0],
  ["stats.parallel_queries", Number(memory.stats?.parallel_queries || 0) >= 1],
  ["stats.parallel_graph_queries", Number(memory.stats?.parallel_graph_queries || 0) >= 1],
];
const failedMemoryReadyChecks = memoryReadyChecks.filter(([, ok]) => !ok).map(([name]) => name);
if (failedMemoryReadyChecks.length > 0) {
  throw new Error("memory compose did not return an agent-ready packet; failed checks: " + failedMemoryReadyChecks.join(", ") + "; stats=" + JSON.stringify(memory.stats || {}) + "; health=" + JSON.stringify({status: memory.memory_health?.status, signals: memory.memory_health?.signals?.map((signal) => signal.code) || []}));
}
const memoryCitationRefs = new Set((Array.isArray(memory.citations) ? memory.citations : []).map((citation) => citation.ref).filter(Boolean));
if (memoryCitationRefs.size < 1 || !Array.isArray(memory.evidence) || !memory.evidence.some((item) => item.ref && memoryCitationRefs.has(item.ref))) {
  throw new Error("memory compose did not return citation-linked evidence");
}
if (!memory.context_window.blocks.some((item) => Array.isArray(item.source_urls) && item.source_urls.length > 0 && Array.isArray(item.citation_refs) && item.citation_refs.some((ref) => memoryCitationRefs.has(ref)))) {
  throw new Error("memory context window did not return citation refs for sourced blocks");
}
if (!memory.verification.retrieval_quality || memory.verification.retrieval_quality.result_count < 1 || !memory.verification.checks.some((check) => check.name === "retrieval_quality")) {
  throw new Error("memory compose did not return retrieval quality verification");
}
if (!memory.context_window.blocks.some((item) => item.type === "task")) {
  throw new Error("memory context window did not preserve task gate context");
}
if (!profileMemory.agent_profile || profileMemory.agent_profile.profile_key !== "agent-alpha" || !profileMemory.context_window || profileMemory.context_window.max_tokens !== 900 || !profileMemory.plan || !Array.isArray(profileMemory.plan.queries) || profileMemory.plan.queries.length < 1 || profileMemory.plan.queries.length > 6) {
  throw new Error("agent profile preferences were not applied to working-memory compose");
}
if (!memory.context_window.prompt.includes("Memory health:")) {
  throw new Error("memory context window did not include memory health gate");
}
if (!memory.retrieval_trace.every((item) => ["ok", "degraded"].includes(item.status))) {
  throw new Error("memory compose did not mark every retrieval trace stage with a status");
}
if (!memory.retrieval_trace.some((item) => item.stage === "retrieval" && item.operation === "planned_summary_and_recall" && item.parallel === true)) {
  throw new Error("memory compose did not trace parallel planned recall");
}
if (!memory.retrieval_trace.some((item) => item.stage === "graph" && item.operation === "seed_graph_expansion" && item.parallel === true)) {
  throw new Error("memory compose did not trace parallel graph expansion");
}
if (!memory.impact_map.some((item) => item.kind === "file" && item.name === "src/pages/smoke-" + process.env.STAMP + "/index.tsx")) {
  throw new Error("memory compose did not include the touched file in the impact map");
}
if (!memory.validation_plan.some((item) => item.command === "npm test" && item.required === true)) {
  throw new Error("memory compose did not include the expected package validation plan");
}
const memoryAgentWritePolicy = memory.agent_policy_decisions.find((item) => item.action === "agent_write");
if (!memoryAgentWritePolicy || memoryAgentWritePolicy.decision !== "require_review") {
  throw new Error("memory compose did not surface stored agent-write review policy");
}
if (!["needs_review", "blocked"].includes(memory.agent_decision.decision) || memory.agent_decision.autonomous_allowed !== false) {
  throw new Error("memory compose did not apply stored agent policy to the agent decision");
}
if (!Array.isArray(memory.agent_decision.required_actions) || !memory.agent_decision.required_actions.includes("request_approval_for_agent_write")) {
  throw new Error("memory compose did not require approval for stored agent-write policy");
}
if (!memoryHealth || memoryHealth.scope !== process.env.SCOPE || typeof memoryHealth.score !== "number" || !memoryHealth.status || !Array.isArray(memoryHealth.reasons) || !Array.isArray(memoryHealth.signals) || memoryHealth.signals.length < 1 || !memoryHealth.documents || memoryHealth.documents.total < 1 || !memoryHealth.claims || memoryHealth.claims.verified < 1 || !memoryHealth.graph || memoryHealth.graph.active_relations < 1 || !memoryHealth.summaries || memoryHealth.summaries.total < 1 || !memoryHealth.sources || memoryHealth.sources.total < 1 || !memoryHealth.ingestion || memoryHealth.ingestion.total_jobs < 0 || !memoryHealth.conflicts || typeof memoryHealth.conflicts.open !== "number" || !memoryHealth.learning || typeof memoryHealth.learning.pending !== "number" || !memoryHealth.approvals || typeof memoryHealth.approvals.pending !== "number") {
  throw new Error("memory health did not return scoped aggregate quality signals");
}
for (const signal of memoryHealth.signals) {
  if (!signal.code || !signal.category || !signal.severity || !signal.message || !signal.action || typeof signal.count !== "number" || typeof signal.score_impact !== "number") {
    throw new Error("memory health returned an invalid structured signal");
  }
}
if (!Array.isArray(summaries.summaries) || summaries.summaries.length < 1) {
  throw new Error("memory summaries endpoint did not return hierarchical summaries");
}
const summaryLevels = new Set(summaries.summaries.map((summary) => summary.level));
for (const level of ["repo", "route", "component", "symbol", "package"]) {
  if (!summaryLevels.has(level)) {
    throw new Error(`memory summaries endpoint did not return ${level} code-intelligence summaries`);
  }
}
if (!rebuildSummaries.scope || rebuildSummaries.documents < 1 || rebuildSummaries.summaries < 1) {
  throw new Error("summary rebuild did not process existing documents");
}
if (!learningProposal.learning_proposal || learningProposal.learning_proposal.status !== "pending") {
  throw new Error("learning proposal was not created as pending");
}
if (!Array.isArray(learningProposals.learning_proposals) || !learningProposals.learning_proposals.some((item) => item.id === learningProposal.learning_proposal.id)) {
  throw new Error("learning proposal list did not include pending proposal");
}
if (!learningProposalDecision.learning_proposal || learningProposalDecision.learning_proposal.status !== "accepted") {
  throw new Error("learning proposal decision did not accept proposal");
}
if (!learningProposalDecision.apply_plan || learningProposalDecision.apply_plan.ready !== true || learningProposalDecision.apply_plan.action !== "rebuild_summaries") {
  throw new Error("learning proposal decision did not return a summary rebuild apply plan");
}
if (!observation.observation || !observation.observation.id || observation.observation.status !== "raw") {
  throw new Error("HTTP observation capture did not return a raw observation");
}
if (!Array.isArray(observationsRaw.observations) || !observationsRaw.observations.some((item) => item.id === observation.observation.id)) {
  throw new Error("HTTP raw observation was not listable before proposal");
}
if (!observationLearningProposal.learning_proposal || observationLearningProposal.learning_proposal.status !== "pending" || observationLearningProposal.learning_proposal.target_type !== "observation" || observationLearningProposal.learning_proposal.target_id !== observation.observation.id) {
  throw new Error("HTTP observation learning proposal did not target the captured observation");
}
if (!observationLearningProposal.learning_proposal.payload || observationLearningProposal.learning_proposal.payload.observation_id !== observation.observation.id || observationLearningProposal.learning_proposal.payload.promotion_flow !== "observation_to_claim") {
  throw new Error("HTTP observation proposal did not preserve observation promotion payload");
}
if (!Array.isArray(observationsProposed.observations) || !observationsProposed.observations.some((item) => item.id === observation.observation.id && item.status === "proposed")) {
  throw new Error("HTTP observation was not marked proposed after learning proposal");
}
if (!observationLearningProposalDecision.learning_proposal || observationLearningProposalDecision.learning_proposal.status !== "accepted" || !observationLearningProposalDecision.apply_plan || observationLearningProposalDecision.apply_plan.action !== "review_claim_promotion" || observationLearningProposalDecision.apply_plan.endpoint !== "/claims" || observationLearningProposalDecision.apply_plan.target_type !== "memory_write" || observationLearningProposalDecision.apply_plan.target_id !== process.env.SCOPE) {
  throw new Error("HTTP observation learning decision did not return a claim-promotion apply plan");
}
if (JSON.stringify(observationRecallAfterProposal).includes(`Smoke observation review sentinel ${process.env.STAMP}`)) {
  throw new Error("HTTP accepted observation proposal leaked into trusted recall");
}
if (!Array.isArray(observationProposedAudit.audit_events) || !observationProposedAudit.audit_events.some((event) => event.target_id === observation.observation.id && event.metadata && event.metadata.channel === "http")) {
  throw new Error("HTTP observation proposed audit event was not recorded");
}
if (!mcp.result && !mcp.error) throw new Error("mcp response was not JSON-RPC shaped");
if (!mcpSummaries.result && !mcpSummaries.error) throw new Error("mcp summaries response was not JSON-RPC shaped");
if (!mcpMemory.result && !mcpMemory.error) throw new Error("mcp working memory response was not JSON-RPC shaped");
if (!sourceApproval.approval || sourceApproval.approval.status !== "pending") {
  throw new Error("source approval request was not pending");
}
if (!sourceApprovalDecision.approval || sourceApprovalDecision.approval.status !== "approved") {
  throw new Error("source approval decision did not approve request");
}
if (!sourceStatusApproval.approval || sourceStatusApproval.approval.status !== "pending") {
  throw new Error("source status approval request was not pending");
}
if (!sourceStatusApprovalDecision.approval || sourceStatusApprovalDecision.approval.status !== "approved") {
  throw new Error("source status approval decision did not approve request");
}
if (!source.source_config_id || sourcePaused.source_config_id !== source.source_config_id || sourceResumed.source_config_id !== source.source_config_id) {
  throw new Error("source pause/resume did not preserve source_config_id");
}
if (!Array.isArray(sourceAuditEvents.audit_events) || !sourceAuditEvents.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.status === "paused") || !sourceAuditEvents.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.status === "active")) {
  throw new Error("source config pause/resume audit events were not recorded");
}
if (!aclApproval.approval || aclApproval.approval.status !== "pending") {
  throw new Error("acl approval request was not pending");
}
if (!aclApprovalDecision.approval || aclApprovalDecision.approval.status !== "approved") {
  throw new Error("acl approval decision did not approve request");
}
if (!mcpAclApproval.approval || mcpAclApproval.approval.status !== "pending") {
  throw new Error("mcp acl approval request was not pending");
}
if (!mcpAclApprovalDecision.approval || mcpAclApprovalDecision.approval.status !== "approved") {
  throw new Error("mcp acl approval decision did not approve request");
}
if (!agentPolicyApproval.approval || agentPolicyApproval.approval.status !== "pending") {
  throw new Error("agent policy approval request was not pending");
}
if (!agentPolicyApprovalDecision.approval || agentPolicyApprovalDecision.approval.status !== "approved") {
  throw new Error("agent policy approval decision did not approve request");
}
if (!mcpAgentPolicyApproval.approval || mcpAgentPolicyApproval.approval.status !== "pending") {
  throw new Error("mcp agent policy approval request was not pending");
}
if (!mcpAgentPolicyApprovalDecision.approval || mcpAgentPolicyApprovalDecision.approval.status !== "approved") {
  throw new Error("mcp agent policy approval decision did not approve request");
}
if (!agentProfileApproval.approval || agentProfileApproval.approval.status !== "pending") {
  throw new Error("agent profile approval request was not pending");
}
if (!agentProfileApprovalDecision.approval || agentProfileApprovalDecision.approval.status !== "approved") {
  throw new Error("agent profile approval decision did not approve request");
}
if (!rebuildApproval.approval || rebuildApproval.approval.status !== "pending") {
  throw new Error("summary rebuild approval request was not pending");
}
if (!rebuildApprovalDecision.approval || rebuildApprovalDecision.approval.status !== "approved") {
  throw new Error("summary rebuild approval decision did not approve request");
}
if (!aclPolicy.acl_policy || aclPolicy.acl_policy.effect !== "allow") {
  throw new Error("acl policy was not written");
}
if (!Array.isArray(aclPolicyAudit.audit_events) || !aclPolicyAudit.audit_events.some((event) => event.target_id === aclPolicy.acl_policy.id && event.metadata && event.metadata.channel === "http" && event.metadata.name === "smoke-allow-recall")) {
  throw new Error("acl policy audit event was not recorded");
}
if (aclDecision.allowed !== true || aclDecision.decision !== "allow") {
  throw new Error("acl decision did not allow expected principal");
}
if (aclDeny.allowed !== false || aclDeny.decision !== "deny") {
  throw new Error("acl decision did not deny unknown principal");
}
if (!agentPolicy.agent_policy || agentPolicy.agent_policy.effect !== "require_review") {
  throw new Error("agent action policy was not written");
}
if (!Array.isArray(agentPolicies.agent_policies) || !agentPolicies.agent_policies.some((item) => item.id === agentPolicy.agent_policy.id && item.effect === "require_review")) {
  throw new Error("agent action policy list did not return the configured policy");
}
if (!Array.isArray(agentPolicyAudit.audit_events) || !agentPolicyAudit.audit_events.some((event) => event.target_id === agentPolicy.agent_policy.id && event.metadata && event.metadata.name === "smoke-require-agent-review")) {
  throw new Error("agent action policy audit event was not recorded");
}
if (agentPolicyDecision.allowed !== false || agentPolicyDecision.decision !== "require_review") {
  throw new Error("agent action policy did not require review for expected principal");
}
if (!agentProfile.agent_profile || agentProfile.agent_profile.profile_key !== "agent-alpha" || agentProfile.agent_profile.default_scope !== process.env.SCOPE || !Array.isArray(agentProfile.agent_profile.allowed_scopes) || !agentProfile.agent_profile.allowed_scopes.includes(process.env.SCOPE) || !agentProfile.agent_profile.denied_scopes.includes("team:design-system:secret")) {
  throw new Error("agent profile was not written with configurable scope preferences");
}
if (!Array.isArray(agentProfiles.agent_profiles) || !agentProfiles.agent_profiles.some((item) => item.id === agentProfile.agent_profile.id && item.principal_ref === "agent:agent-alpha")) {
  throw new Error("agent profile list did not return the configured profile");
}
if (!Array.isArray(agentProfileAudit.audit_events) || !agentProfileAudit.audit_events.some((event) => event.target_id === agentProfile.agent_profile.id && event.metadata && event.metadata.profile_key === "agent-alpha")) {
  throw new Error("agent profile audit event was not recorded");
}
if (!conflictClaimA.claim_id || !conflictClaimB.claim_id || conflictClaimA.claim_id === conflictClaimB.claim_id) {
  throw new Error("conflict fixture claims were not created correctly");
}
if (conflictClaimB.conflicts < 1) {
  throw new Error("automatic conflict detection did not flag the second contradictory claim");
}
if (!conflictChallenge.conflict_id || !conflictChallenge.feedback_id) {
  throw new Error("conflict challenge did not write feedback and conflict records");
}
if (!Array.isArray(conflictMemory.conflicts) || conflictMemory.conflicts.length < 1) {
  throw new Error("conflict memory packet did not surface active conflicts");
}
if (!conflictMemory.verification || conflictMemory.verification.verdict !== "unsafe" || !Array.isArray(conflictMemory.verification.active_conflicts) || conflictMemory.verification.active_conflicts.length < 1) {
  throw new Error("conflict memory packet did not mark active conflicts as unsafe");
}
if (!conflictMemory.agent_decision || conflictMemory.agent_decision.decision !== "blocked" || conflictMemory.agent_decision.autonomous_allowed !== false) {
  throw new Error("conflict memory packet did not block autonomous agent action");
}
if (!Array.isArray(conflictMemory.agent_decision.required_actions) || !conflictMemory.agent_decision.required_actions.includes("resolve_active_conflicts") || !Array.isArray(conflictMemory.agent_decision.allowed_next_actions) || !conflictMemory.agent_decision.allowed_next_actions.includes("list_conflicts") || conflictMemory.agent_decision.allowed_next_actions.includes("request_approval")) {
  throw new Error("conflict memory packet did not route active conflicts to conflict review");
}
if (!Array.isArray(conflictMemory.learning_suggestions) || !conflictMemory.learning_suggestions.some((item) => item.title === "Resolve active claim conflict")) {
  throw new Error("conflict memory packet did not propose a conflict-resolution learning action");
}
if (!Array.isArray(conflictsOpen.conflicts) || !conflictsOpen.conflicts.some((item) => item.id === conflictChallenge.conflict_id && item.status === "open")) {
  throw new Error("conflict list did not return the open conflict");
}
if (!conflictResolved.conflict || conflictResolved.conflict.id !== conflictChallenge.conflict_id || conflictResolved.conflict.status !== "resolved" || !conflictResolved.conflict.resolved_at) {
  throw new Error("conflict resolve endpoint did not close the conflict");
}
if (Array.isArray(conflictMemoryResolved.conflicts) && conflictMemoryResolved.conflicts.some((item) => item.id === conflictChallenge.conflict_id)) {
  throw new Error("resolved conflict was still surfaced as an active memory conflict");
}
if (conflictMemoryResolved.verification && Array.isArray(conflictMemoryResolved.verification.active_conflicts) && conflictMemoryResolved.verification.active_conflicts.some((item) => item.id === conflictChallenge.conflict_id)) {
  throw new Error("resolved conflict still appeared in active verifier conflicts");
}
if (!approval.approval || approval.approval.status !== "pending") throw new Error("approval request was not pending");
if (!approvalDecision.approval || approvalDecision.approval.status !== "approved") {
  throw new Error("approval decision did not approve request");
}
	if (!mcpApproval.result && !mcpApproval.error) throw new Error("mcp approval response was not JSON-RPC shaped");
	if (!mcpInit.result || !mcpInit.result.capabilities || !mcpInit.result.capabilities.tools || !mcpInit.result.capabilities.resources || !mcpInit.result.capabilities.prompts) {
	  throw new Error("mcp initialize did not declare tools, resources, and prompts capabilities");
	}
	if (!mcpTools.result || !Array.isArray(mcpTools.result.tools) || !mcpTools.result.tools.some((tool) => tool.name === "propose_learning")) {
	  throw new Error("mcp tools list did not expose learning tools");
	}
	if (!mcpResources.result || !Array.isArray(mcpResources.result.resources) || !mcpResources.result.resources.some((resource) => resource.uri === "abra://guide/agent-workflow")) {
	  throw new Error("mcp resources list did not expose the agent workflow guide");
	}
if (!mcpResourceTemplates.result || !Array.isArray(mcpResourceTemplates.result.resourceTemplates) || !mcpResourceTemplates.result.resourceTemplates.some((template) => template.uriTemplate === "abra://memory/health/{scope}") || !mcpResourceTemplates.result.resourceTemplates.some((template) => template.uriTemplate === "abra://working-memory?scope={scope}&task={task}")) {
	  throw new Error("mcp resource templates did not expose scoped health and working-memory resources");
	}
	if (!mcpResourceGuide.result || !Array.isArray(mcpResourceGuide.result.contents) || !String(mcpResourceGuide.result.contents[0] && mcpResourceGuide.result.contents[0].text).includes("working_memory_compose")) {
	  throw new Error("mcp guide resource did not return agent workflow text");
	}
	if (!mcpResourceHealth.result || !Array.isArray(mcpResourceHealth.result.contents)) {
	  throw new Error("mcp memory-health resource did not return contents");
	}
	const mcpResourceHealthPayload = JSON.parse(mcpResourceHealth.result.contents[0].text);
	if (mcpResourceHealthPayload.scope !== process.env.SCOPE || typeof mcpResourceHealthPayload.score !== "number" || !mcpResourceHealthPayload.status) {
	  throw new Error("mcp memory-health resource did not return scoped health JSON");
	}
	if (!mcpPrompts.result || !Array.isArray(mcpPrompts.result.prompts) || !mcpPrompts.result.prompts.some((prompt) => prompt.name === "abra-before-code")) {
	  throw new Error("mcp prompts list did not expose abra-before-code");
	}
	if (!mcpPromptBeforeCode.result || !Array.isArray(mcpPromptBeforeCode.result.messages) || !String(mcpPromptBeforeCode.result.messages[0] && mcpPromptBeforeCode.result.messages[0].content && mcpPromptBeforeCode.result.messages[0].content.text).includes("working_memory_compose")) {
	  throw new Error("mcp prompt get did not return a working-memory instruction");
	}
for (const tool of ["memory_health", "list_conflicts", "resolve_conflict"]) {
	  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
	    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["ingest_document", "ingest_documents"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_agent_profile", "list_agent_profiles"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_source_config", "list_source_configs", "enqueue_ingestion_job", "list_ingestion_jobs", "retry_ingestion_job", "cancel_ingestion_job"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_acl_policy", "list_acl_policies", "acl_decision"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
for (const tool of ["upsert_agent_policy", "list_agent_policies", "agent_policy_decision"]) {
  if (!mcpTools.result.tools.some((item) => item.name === tool)) {
    throw new Error(`mcp tools list did not expose ${tool}`);
  }
}
if (!mcpAclPolicy.result || mcpAclPolicy.error) throw new Error("mcp acl policy upsert response failed");
const mcpAclPolicyPayload = mcpTextPayload(mcpAclPolicy);
if (!mcpAclPolicyPayload.id || mcpAclPolicyPayload.name !== "mcp-allow-recall" || mcpAclPolicyPayload.effect !== "allow") {
  throw new Error("mcp upsert_acl_policy did not return the configured policy");
}
if (!mcpAclPolicies.result || mcpAclPolicies.error) throw new Error("mcp acl policy list response failed");
if (!mcpTextPayload(mcpAclPolicies).some((item) => item.id === mcpAclPolicyPayload.id && item.effect === "allow")) {
  throw new Error("mcp list_acl_policies did not return the configured policy");
}
if (!mcpAclDecision.result || mcpAclDecision.error) throw new Error("mcp acl decision response failed");
const mcpAclDecisionPayload = mcpTextPayload(mcpAclDecision);
if (mcpAclDecisionPayload.allowed !== true || mcpAclDecisionPayload.decision !== "allow") {
  throw new Error("mcp acl_decision did not allow expected principal");
}
if (!Array.isArray(mcpAclPolicyAudit.audit_events) || !mcpAclPolicyAudit.audit_events.some((event) => event.target_id === mcpAclPolicyPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.name === "mcp-allow-recall")) {
  throw new Error("mcp acl policy audit event was not recorded");
}
if (!mcpAgentPolicy.result || mcpAgentPolicy.error) throw new Error("mcp agent policy upsert response failed");
const mcpAgentPolicyPayload = mcpTextPayload(mcpAgentPolicy);
if (!mcpAgentPolicyPayload.id || mcpAgentPolicyPayload.name !== "mcp-require-agent-review" || mcpAgentPolicyPayload.effect !== "require_review") {
  throw new Error("mcp upsert_agent_policy did not return the configured policy");
}
if (!mcpAgentPolicies.result || mcpAgentPolicies.error) throw new Error("mcp agent policy list response failed");
if (!mcpTextPayload(mcpAgentPolicies).some((item) => item.id === mcpAgentPolicyPayload.id && item.effect === "require_review")) {
  throw new Error("mcp list_agent_policies did not return the configured policy");
}
if (!mcpAgentPolicyDecision.result || mcpAgentPolicyDecision.error) throw new Error("mcp agent policy decision response failed");
const mcpAgentPolicyDecisionPayload = mcpTextPayload(mcpAgentPolicyDecision);
if (mcpAgentPolicyDecisionPayload.allowed !== false || mcpAgentPolicyDecisionPayload.decision !== "require_review") {
  throw new Error("mcp agent_policy_decision did not require review for expected principal");
}
if (!Array.isArray(mcpAgentPolicyAudit.audit_events) || !mcpAgentPolicyAudit.audit_events.some((event) => event.target_id === mcpAgentPolicyPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.name === "mcp-require-agent-review")) {
  throw new Error("mcp agent policy audit event was not recorded");
}
if (!mcpAgentProfiles.result || mcpAgentProfiles.error) throw new Error("mcp agent profile list response failed");
if (!mcpTextPayload(mcpAgentProfiles).some((item) => item.id === agentProfile.agent_profile.id && item.profile_key === "agent-alpha")) {
  throw new Error("mcp list_agent_profiles did not return the configured profile");
}
if (!mcpObservation.result || mcpObservation.error) throw new Error("mcp capture_observation response failed");
const mcpObservationRaw = mcpTextPayload(mcpObservation);
const mcpObservationPayload = mcpObservationRaw.observation || mcpObservationRaw;
if (!mcpObservationPayload.id || mcpObservationPayload.status !== "raw" || mcpObservationPayload.scope !== process.env.SCOPE) {
  throw new Error("mcp capture_observation did not return a raw scoped observation");
}
if (!mcpObservationLearning.result || mcpObservationLearning.error) throw new Error("mcp observation propose_learning response failed");
const mcpObservationLearningRaw = mcpTextPayload(mcpObservationLearning);
const mcpObservationLearningPayload = mcpObservationLearningRaw.learning_proposal || mcpObservationLearningRaw;
if (!mcpObservationLearningPayload.id || mcpObservationLearningPayload.status !== "pending" || mcpObservationLearningPayload.target_type !== "observation" || mcpObservationLearningPayload.target_id !== mcpObservationPayload.id) {
  throw new Error("mcp observation propose_learning did not return a pending observation-target proposal");
}
if (!mcpObservationLearningPayload.payload || mcpObservationLearningPayload.payload.observation_id !== mcpObservationPayload.id || mcpObservationLearningPayload.payload.promotion_flow !== "observation_to_claim") {
  throw new Error("mcp observation proposal did not preserve observation promotion payload");
}
if (!mcpObservationsProposed.result || mcpObservationsProposed.error) throw new Error("mcp list_observations proposed response failed");
if (!mcpTextPayload(mcpObservationsProposed).some((item) => item.id === mcpObservationPayload.id && item.status === "proposed")) {
  throw new Error("mcp observation was not marked proposed after learning proposal");
}
if (!mcpObservationLearningDecision.result || mcpObservationLearningDecision.error) throw new Error("mcp observation learning decision response failed");
const mcpObservationLearningDecisionPayload = mcpTextPayload(mcpObservationLearningDecision);
if (!mcpObservationLearningDecisionPayload.learning_proposal || mcpObservationLearningDecisionPayload.learning_proposal.status !== "accepted" || !mcpObservationLearningDecisionPayload.apply_plan || mcpObservationLearningDecisionPayload.apply_plan.action !== "review_claim_promotion" || mcpObservationLearningDecisionPayload.apply_plan.endpoint !== "/claims" || mcpObservationLearningDecisionPayload.apply_plan.target_type !== "memory_write" || mcpObservationLearningDecisionPayload.apply_plan.target_id !== process.env.SCOPE) {
  throw new Error("mcp observation learning decision did not return a claim-promotion apply plan");
}
if (JSON.stringify(mcpObservationRecallAfterProposal).includes(`MCP observation review sentinel ${process.env.STAMP}`)) {
  throw new Error("mcp accepted observation proposal leaked into trusted recall");
}
if (!Array.isArray(mcpObservationProposedAudit.audit_events) || !mcpObservationProposedAudit.audit_events.some((event) => event.target_id === mcpObservationPayload.id && event.metadata && event.metadata.channel === "mcp")) {
  throw new Error("mcp observation proposed audit event was not recorded");
}
if (!mcpIngestDocument.result || mcpIngestDocument.error) throw new Error("mcp ingest_document response failed");
const mcpIngestDocumentPayload = mcpTextPayload(mcpIngestDocument);
if (!mcpIngestDocumentPayload.document_id || mcpIngestDocumentPayload.chunks < 1 || mcpIngestDocumentPayload.claims < 1) {
  throw new Error("mcp ingest_document did not write source-backed memory");
}
if (!mcpIngestDocuments.result || mcpIngestDocuments.error) throw new Error("mcp ingest_documents response failed");
const mcpIngestDocumentsPayload = mcpTextPayload(mcpIngestDocuments);
if (mcpIngestDocumentsPayload.accepted !== 2 || !Array.isArray(mcpIngestDocumentsPayload.documents) || !mcpIngestDocumentsPayload.documents.every((item) => item.document_id && item.chunks >= 1 && item.claims >= 1 && item.scope === process.env.SCOPE)) {
  throw new Error("mcp ingest_documents did not write the expected batch memory");
}
if (!mcpSourceConfig.result || mcpSourceConfig.error) throw new Error("mcp source config upsert response failed");
const mcpSourceConfigPayload = mcpTextPayload(mcpSourceConfig);
if (mcpSourceConfigPayload.source_config_id !== source.source_config_id || mcpSourceConfigPayload.status !== "upserted") {
  throw new Error("mcp upsert_source_config did not return the configured source");
}
if (!mcpSourceConfigs.result || mcpSourceConfigs.error) throw new Error("mcp source config list response failed");
if (!mcpTextPayload(mcpSourceConfigs).some((item) => item.id === source.source_config_id && item.metadata && item.metadata.owner === "smoke-mcp")) {
  throw new Error("mcp list_source_configs did not return the configured source");
}
if (!mcpIngestionJob.result || mcpIngestionJob.error) throw new Error("mcp ingestion job enqueue response failed");
const mcpIngestionJobPayload = mcpTextPayload(mcpIngestionJob);
if (!mcpIngestionJobPayload.id || mcpIngestionJobPayload.source_config_id !== source.source_config_id || mcpIngestionJobPayload.trigger_type !== "manual") {
  throw new Error("mcp enqueue_ingestion_job did not return the queued manual job");
}
if (!mcpIngestionJobs.result || mcpIngestionJobs.error) throw new Error("mcp ingestion job list response failed");
if (!mcpTextPayload(mcpIngestionJobs).some((item) => item.id === mcpIngestionJobPayload.id)) {
  throw new Error("mcp list_ingestion_jobs did not return the queued manual job");
}
if (!Array.isArray(mcpSourceConfigAudit.audit_events) || !mcpSourceConfigAudit.audit_events.some((event) => event.target_id === source.source_config_id && event.metadata && event.metadata.channel === "mcp" && event.metadata.source_type === "local_repo")) {
  throw new Error("mcp source config audit event was not recorded");
}
if (!mcpMemoryHealth.result || mcpMemoryHealth.error) throw new Error("mcp memory_health response failed");
const mcpHealth = mcpTextPayload(mcpMemoryHealth);
if (mcpHealth.scope !== process.env.SCOPE || typeof mcpHealth.score !== "number" || !mcpHealth.status || !Array.isArray(mcpHealth.signals) || mcpHealth.signals.length < 1) {
  throw new Error("mcp memory_health did not return a scoped health score");
}
if (!mcpLearning.result && !mcpLearning.error) throw new Error("mcp learning response was not JSON-RPC shaped");
const mcpLearningPayload = mcpTextPayload(mcpLearning);
if (!mcpLearningPayload.id || mcpLearningPayload.status !== "pending") {
  throw new Error("mcp learning proposal did not return a pending proposal");
}
if (!mcpLearningDecision.result || mcpLearningDecision.error) throw new Error("mcp learning decision response failed");
const mcpLearningDecisionPayload = mcpTextPayload(mcpLearningDecision);
if (!mcpLearningDecisionPayload.learning_proposal || mcpLearningDecisionPayload.learning_proposal.status !== "accepted" || !mcpLearningDecisionPayload.apply_plan || mcpLearningDecisionPayload.apply_plan.action !== "review_graph_update") {
  throw new Error("mcp learning decision did not return an accepted graph apply plan");
}
if (!Array.isArray(mcpLearningProposedAudit.audit_events) || !mcpLearningProposedAudit.audit_events.some((event) => event.target_id === mcpLearningPayload.id && event.metadata && event.metadata.channel === "mcp")) {
  throw new Error("mcp learning proposal audit event was not recorded");
}
if (!Array.isArray(mcpLearningDecidedAudit.audit_events) || !mcpLearningDecidedAudit.audit_events.some((event) => event.target_id === mcpLearningPayload.id && event.metadata && event.metadata.channel === "mcp" && event.metadata.status === "accepted")) {
  throw new Error("mcp learning decision audit event was not recorded");
}
if (!mcpConflicts.result || mcpConflicts.error) throw new Error("mcp conflicts response failed");
if (!mcpTextPayload(mcpConflicts).some((item) => item.id === conflictChallenge.conflict_id && item.status === "resolved")) {
  throw new Error("mcp list_conflicts did not return the resolved conflict");
}
if (!mcpConflictResolve.result || mcpConflictResolve.error) throw new Error("mcp conflict resolve response failed");
if (mcpTextPayload(mcpConflictResolve).status !== "suppressed") {
  throw new Error("mcp resolve_conflict did not suppress the conflict");
}
if (!Array.isArray(jobs.ingestion_jobs)) throw new Error("ingestion jobs response was not shaped correctly");
for (const metric of [
  "abra_smart_path_requests_total",
  "operation=\"recall\"",
  "operation=\"working_memory\"",
  "abra_smart_path_graph_relations_returned_sum",
  "abra_smart_path_autonomous_allowed_total",
  "abra_working_memory_retrieval_quality_total",
  "abra_working_memory_retrieval_top_rank_score_sum",
  "abra_working_memory_retrieval_last_result_count",
  "abra_working_memory_health_status_total",
  "abra_working_memory_health_signals_returned_sum",
  "abra_working_memory_health_signal_total",
  "health_status=\"",
  "abra_ai_provider_calls_total",
  "abra_ai_provider_calls_total{operation=\"embedding\"",
  "abra_ai_provider_call_duration_milliseconds_sum{operation=\"embedding\"",
  "abra_ai_provider_waits_total{operation=\"embedding\"",
  "abra_ai_provider_wait_duration_milliseconds_sum{operation=\"embedding\"",
  "abra_ai_provider_in_flight{operation=\"embedding\"",
  "abra_ai_provider_waiting{operation=\"embedding\"",
  "abra_ai_provider_max_in_flight{operation=\"embedding\"",
  "abra_ai_provider_max_waiting{operation=\"embedding\"",
  "abra_agent_policy_decisions_total",
  "operation=\"working_memory\",action=\"agent_write\",decision=\"require_review\"",
  "operation=\"decision_api\",action=\"agent_write\",decision=\"require_review\""
]) {
  if (!metricsAfter.includes(metric)) {
    throw new Error(`metrics did not expose ${metric}`);
  }
}
console.log("Abra smoke passed", JSON.stringify({
  document_id: ingest.document_id,
  webhook_accepted: webhook.accepted,
  acl_decision: aclDecision.decision,
  agent_policy_decision: agentPolicyDecision.decision,
  conflict_decision: conflictMemory.agent_decision.decision,
  health_status: memoryHealth.status,
  health_score: memoryHealth.score,
  supporting_documents: recall.supporting_documents.length,
  policy_queries: policy.queries.length,
  approval_status: approvalDecision.approval.status,
  ingestion_jobs: jobs.ingestion_jobs.length
}));
