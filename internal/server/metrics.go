package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

type metricsCollector struct {
	mu            sync.Mutex
	started       time.Time
	requests      map[string]*requestMetric
	smart         map[string]*smartPathMetric
	policies      map[string]int64
	recall        map[string]int64
	quality       map[string]*retrievalQualityMetric
	health        map[string]*memoryHealthMetric
	healthLookups map[string]int64
	signals       map[string]int64
}

type requestMetric struct {
	Count          int64
	DurationMS     int64
	LastDurationMS int64
}

type smartPathMetric struct {
	Count                 int64
	DurationMS            int64
	LastDurationMS        int64
	Facts                 int64
	SupportingDocuments   int64
	GraphRelations        int64
	LearningSuggestions   int64
	ReviewRequired        int64
	AutonomousAllowed     int64
	LastFacts             int64
	LastSupportingDocs    int64
	LastGraphRelations    int64
	LastLearning          int64
	LastReviewRequired    int64
	LastAutonomousAllowed int64
}

type retrievalQualityMetric struct {
	Count           int64
	TopRankSum      float64
	TopTextSum      float64
	TopVectorSum    float64
	LastTopRank     float64
	LastTopText     float64
	LastTopVector   float64
	LastResultCount int64
}

type memoryHealthMetric struct {
	Count           int64
	SignalCount     int64
	CriticalSignals int64
	WarningSignals  int64
	LastScore       int64
	LastSignals     int64
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func newMetricsCollector() *metricsCollector {
	return &metricsCollector{
		started:       time.Now().UTC(),
		requests:      map[string]*requestMetric{},
		smart:         map[string]*smartPathMetric{},
		policies:      map[string]int64{},
		recall:        map[string]int64{},
		quality:       map[string]*retrievalQualityMetric{},
		health:        map[string]*memoryHealthMetric{},
		healthLookups: map[string]int64{},
		signals:       map[string]int64{},
	}
}

func (m *metricsCollector) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.observe(r.Method, route, recorder.status, time.Since(started))
	})
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (m *metricsCollector) observe(method, route string, status int, duration time.Duration) {
	key := method + "\n" + route + "\n" + fmt.Sprint(status)
	durationMS := duration.Milliseconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.requests[key]
	if metric == nil {
		metric = &requestMetric{}
		m.requests[key] = metric
	}
	metric.Count++
	metric.DurationMS += durationMS
	metric.LastDurationMS = durationMS
}

func (m *metricsCollector) observeRecall(status string, duration time.Duration, result store.RecallResult) {
	m.observeRecallRetrievalMode(status, result.RetrievalMode)
	m.observeSmartPath("recall", status, "", "", duration, smartPathCounts{
		Facts:               len(result.Claims),
		SupportingDocuments: len(result.SupportingDocuments),
		GraphRelations:      len(result.GraphContext),
	})
}

func (m *metricsCollector) observeMemory(status string, duration time.Duration, result memory.ComposeResult) {
	m.observeMemoryRetrievalQuality(status, result.Verification)
	m.observeMemoryHealth(status, result.MemoryHealth)
	m.observeMemoryHealthLookup(status, result.RetrievalTrace)
	m.observeSmartPath("working_memory", status, result.Verification.Verdict, result.AgentDecision.Decision, duration, smartPathCounts{
		Facts:               len(result.Facts),
		SupportingDocuments: len(result.SupportingDocuments),
		GraphRelations:      len(result.GraphContext),
		LearningSuggestions: len(result.LearningSuggestions),
		ReviewRequired:      boolInt(result.AgentDecision.ReviewRequired),
		AutonomousAllowed:   boolInt(result.AgentDecision.AutonomousAllowed),
	})
	for _, decision := range result.AgentPolicyDecisions {
		m.observeAgentPolicyDecision("working_memory", decision.Action, decision.Decision)
	}
}

func (m *metricsCollector) observeBrainThink(status string, duration time.Duration, result memory.ThinkResult) {
	m.observeMemoryRetrievalQuality(status, result.Verification)
	m.observeMemoryHealth(status, result.MemoryHealth)
	m.observeMemoryHealthLookup(status, result.RetrievalTrace)
	m.observeSmartPath("brain_think", status, result.Verification.Verdict, result.AgentDecision.Decision, duration, smartPathCounts{
		Facts:               result.Stats.Facts,
		SupportingDocuments: result.Stats.SupportingDocuments,
		GraphRelations:      result.Stats.GraphRelations,
		ReviewRequired:      boolInt(result.AgentDecision.ReviewRequired),
		AutonomousAllowed:   boolInt(result.AgentDecision.AutonomousAllowed),
	})
}

func (m *metricsCollector) observeMemoryHealthLookup(apiStatus string, trace []memory.RetrievalTraceItem) {
	cacheStatus := "unknown"
	for _, item := range trace {
		if item.Stage == "health" && item.Operation == "memory_health_lookup" {
			cacheStatus = normalizeMemoryHealthCacheStatus(item.CacheStatus)
			break
		}
	}
	key := strings.Join([]string{
		normalizeAgentPolicyMetricValue(apiStatus, "unknown"),
		cacheStatus,
	}, "\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthLookups[key]++
}

func (m *metricsCollector) observeMemoryHealth(apiStatus string, health store.MemoryHealthResult) {
	healthStatus := normalizeMemoryHealthStatus(health.Status)
	signals := health.Signals
	key := strings.Join([]string{
		normalizeAgentPolicyMetricValue(apiStatus, "unknown"),
		healthStatus,
	}, "\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.health[key]
	if metric == nil {
		metric = &memoryHealthMetric{}
		m.health[key] = metric
	}
	metric.Count++
	metric.SignalCount += int64(len(signals))
	metric.LastScore = int64(health.Score)
	metric.LastSignals = int64(len(signals))
	for _, signal := range signals {
		severity := normalizeMemoryHealthSeverity(signal.Severity)
		switch severity {
		case "critical":
			metric.CriticalSignals++
		case "warning":
			metric.WarningSignals++
		}
		signalKey := strings.Join([]string{
			normalizeAgentPolicyMetricValue(apiStatus, "unknown"),
			healthStatus,
			normalizeMemoryHealthCategory(signal.Category),
			severity,
			normalizeMemoryHealthSignalCode(signal.Code),
		}, "\n")
		m.signals[signalKey]++
	}
}

type smartPathCounts struct {
	Facts               int
	SupportingDocuments int
	GraphRelations      int
	LearningSuggestions int
	ReviewRequired      int
	AutonomousAllowed   int
}

func (m *metricsCollector) observeSmartPath(operation, status, verdict, decision string, duration time.Duration, counts smartPathCounts) {
	key := strings.Join([]string{operation, status, verdict, decision}, "\n")
	durationMS := duration.Milliseconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.smart[key]
	if metric == nil {
		metric = &smartPathMetric{}
		m.smart[key] = metric
	}
	metric.Count++
	metric.DurationMS += durationMS
	metric.LastDurationMS = durationMS
	metric.Facts += int64(counts.Facts)
	metric.SupportingDocuments += int64(counts.SupportingDocuments)
	metric.GraphRelations += int64(counts.GraphRelations)
	metric.LearningSuggestions += int64(counts.LearningSuggestions)
	metric.ReviewRequired += int64(counts.ReviewRequired)
	metric.AutonomousAllowed += int64(counts.AutonomousAllowed)
	metric.LastFacts = int64(counts.Facts)
	metric.LastSupportingDocs = int64(counts.SupportingDocuments)
	metric.LastGraphRelations = int64(counts.GraphRelations)
	metric.LastLearning = int64(counts.LearningSuggestions)
	metric.LastReviewRequired = int64(counts.ReviewRequired)
	metric.LastAutonomousAllowed = int64(counts.AutonomousAllowed)
}

func (m *metricsCollector) observeAgentPolicyDecision(operation, action, decision string) {
	key := strings.Join([]string{
		normalizeAgentPolicyMetricValue(operation, "unknown"),
		normalizeAgentPolicyAction(action),
		normalizeAgentPolicyDecision(decision),
	}, "\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policies[key]++
}

func (m *metricsCollector) observeRecallRetrievalMode(status, mode string) {
	key := strings.Join([]string{
		normalizeRecallMode(mode),
		normalizeAgentPolicyMetricValue(status, "unknown"),
	}, "\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recall[key]++
}

func (m *metricsCollector) observeMemoryRetrievalQuality(status string, report memory.VerificationReport) {
	quality := report.RetrievalQuality
	key := strings.Join([]string{
		normalizeAgentPolicyMetricValue(status, "unknown"),
		normalizeVerificationVerdict(report.Verdict),
		normalizeRetrievalQuality(quality, report.Verdict),
	}, "\n")
	m.mu.Lock()
	defer m.mu.Unlock()
	metric := m.quality[key]
	if metric == nil {
		metric = &retrievalQualityMetric{}
		m.quality[key] = metric
	}
	metric.Count++
	metric.TopRankSum += quality.TopRankScore
	metric.TopTextSum += quality.TopTextScore
	metric.TopVectorSum += quality.TopVectorScore
	metric.LastTopRank = quality.TopRankScore
	metric.LastTopText = quality.TopTextScore
	metric.LastTopVector = quality.TopVectorScore
	metric.LastResultCount = int64(quality.ResultCount)
}

func normalizeRecallMode(value string) string {
	switch strings.TrimSpace(value) {
	case "hybrid", "full_text", "full_text_embedding_error", "full_text_empty_embedding", "empty":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeVerificationVerdict(value string) string {
	switch strings.TrimSpace(value) {
	case "strong", "partial", "weak", "unsafe":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeRetrievalQuality(quality memory.RetrievalQuality, verdict string) string {
	if quality.LowConfidence {
		return "low_confidence"
	}
	if quality.ResultCount > 0 {
		return "ok"
	}
	if strings.TrimSpace(verdict) == "" {
		return "unknown"
	}
	return "missing"
}

func normalizeMemoryHealthStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "healthy", "needs_review", "critical":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeMemoryHealthCacheStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "fresh", "cache_hit", "coalesced", "disabled":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeMemoryHealthSeverity(value string) string {
	switch strings.TrimSpace(value) {
	case "critical", "warning", "info":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeMemoryHealthCategory(value string) string {
	switch strings.TrimSpace(value) {
	case "readiness", "documents", "claims", "trust_guard", "summaries", "graph", "conflicts", "sources", "ingestion", "learning", "approvals":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeMemoryHealthSignalCode(value string) string {
	switch strings.TrimSpace(value) {
	case "memory_ready",
		"documents_empty",
		"trusted_claims_empty",
		"claims_missing_evidence",
		"trusted_claims_from_code_documents",
		"summaries_empty",
		"graph_relations_empty",
		"blocking_conflicts",
		"active_conflicts",
		"source_configs_error",
		"ingestion_jobs_failed",
		"ingestion_jobs_stale_running",
		"ingestion_jobs_retrying",
		"claims_need_review",
		"graph_relations_need_review",
		"learning_proposals_pending",
		"learning_duplicate_pending_groups",
		"approval_requests_pending",
		"memory_health_unavailable":
		return strings.TrimSpace(value)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeAgentPolicyMetricValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeAgentPolicyAction(value string) string {
	switch strings.TrimSpace(value) {
	case "agent_write", "challenge_claim", "forget_claim", "backfill", "source_authority_change", "acl_change", "connector_enable", "scope_expansion":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func normalizeAgentPolicyDecision(value string) string {
	switch strings.TrimSpace(value) {
	case "allow", "deny", "require_review", "no_policy":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (m *metricsCollector) prometheus() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out strings.Builder
	out.WriteString("# HELP abra_build_info Abra service build and runtime information.\n")
	out.WriteString("# TYPE abra_build_info gauge\n")
	out.WriteString("abra_build_info{runtime=\"go\",version=\"0.1.0\"} 1\n")
	out.WriteString("# HELP abra_uptime_seconds Seconds since this API process started.\n")
	out.WriteString("# TYPE abra_uptime_seconds gauge\n")
	out.WriteString(fmt.Sprintf("abra_uptime_seconds %.0f\n", time.Since(m.started).Seconds()))
	out.WriteString("# HELP abra_http_requests_total Total HTTP requests by method, route, and status.\n")
	out.WriteString("# TYPE abra_http_requests_total counter\n")

	keys := make([]string, 0, len(m.requests))
	for key := range m.requests {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		parts := strings.Split(key, "\n")
		metric := m.requests[key]
		out.WriteString(fmt.Sprintf(
			"abra_http_requests_total{method=%q,route=%q,status=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			metric.Count,
		))
	}

	out.WriteString("# HELP abra_http_request_duration_milliseconds_sum Total request duration in milliseconds.\n")
	out.WriteString("# TYPE abra_http_request_duration_milliseconds_sum counter\n")
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		metric := m.requests[key]
		out.WriteString(fmt.Sprintf(
			"abra_http_request_duration_milliseconds_sum{method=%q,route=%q,status=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			metric.DurationMS,
		))
	}

	out.WriteString("# HELP abra_http_request_last_duration_milliseconds Last observed request duration in milliseconds.\n")
	out.WriteString("# TYPE abra_http_request_last_duration_milliseconds gauge\n")
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		metric := m.requests[key]
		out.WriteString(fmt.Sprintf(
			"abra_http_request_last_duration_milliseconds{method=%q,route=%q,status=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			metric.LastDurationMS,
		))
	}

	smartKeys := make([]string, 0, len(m.smart))
	for key := range m.smart {
		smartKeys = append(smartKeys, key)
	}
	sort.Strings(smartKeys)

	out.WriteString("# HELP abra_smart_path_requests_total Total recall, brain-think, and working-memory smart-path executions.\n")
	out.WriteString("# TYPE abra_smart_path_requests_total counter\n")
	for _, key := range smartKeys {
		parts := strings.Split(key, "\n")
		metric := m.smart[key]
		out.WriteString(fmt.Sprintf(
			"abra_smart_path_requests_total{operation=%q,status=%q,verdict=%q,decision=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			parts[3],
			metric.Count,
		))
	}

	out.WriteString("# HELP abra_smart_path_duration_milliseconds_sum Total smart-path duration in milliseconds.\n")
	out.WriteString("# TYPE abra_smart_path_duration_milliseconds_sum counter\n")
	for _, key := range smartKeys {
		parts := strings.Split(key, "\n")
		metric := m.smart[key]
		out.WriteString(fmt.Sprintf(
			"abra_smart_path_duration_milliseconds_sum{operation=%q,status=%q,verdict=%q,decision=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			parts[3],
			metric.DurationMS,
		))
	}

	out.WriteString("# HELP abra_smart_path_last_duration_milliseconds Last smart-path duration in milliseconds.\n")
	out.WriteString("# TYPE abra_smart_path_last_duration_milliseconds gauge\n")
	for _, key := range smartKeys {
		parts := strings.Split(key, "\n")
		metric := m.smart[key]
		out.WriteString(fmt.Sprintf(
			"abra_smart_path_last_duration_milliseconds{operation=%q,status=%q,verdict=%q,decision=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			parts[3],
			metric.LastDurationMS,
		))
	}

	writeSmartPathCounter(&out, "abra_smart_path_facts_returned_sum", "Total claims or facts returned by smart-path executions.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.Facts })
	writeSmartPathCounter(&out, "abra_smart_path_supporting_documents_returned_sum", "Total supporting documents returned by smart-path executions.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.SupportingDocuments })
	writeSmartPathCounter(&out, "abra_smart_path_graph_relations_returned_sum", "Total graph relations returned by smart-path executions.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.GraphRelations })
	writeSmartPathCounter(&out, "abra_smart_path_learning_suggestions_returned_sum", "Total learning suggestions returned by working-memory executions.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.LearningSuggestions })
	writeSmartPathCounter(&out, "abra_smart_path_review_required_total", "Total working-memory executions that required review.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.ReviewRequired })
	writeSmartPathCounter(&out, "abra_smart_path_autonomous_allowed_total", "Total working-memory executions that allowed autonomous action.", smartKeys, m.smart, func(metric *smartPathMetric) int64 { return metric.AutonomousAllowed })

	recallKeys := make([]string, 0, len(m.recall))
	for key := range m.recall {
		recallKeys = append(recallKeys, key)
	}
	sort.Strings(recallKeys)
	out.WriteString("# HELP abra_recall_retrieval_mode_total Total recall executions by bounded retrieval mode and status.\n")
	out.WriteString("# TYPE abra_recall_retrieval_mode_total counter\n")
	for _, key := range recallKeys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"abra_recall_retrieval_mode_total{mode=%q,status=%q} %d\n",
			parts[0],
			parts[1],
			m.recall[key],
		))
	}

	qualityKeys := make([]string, 0, len(m.quality))
	for key := range m.quality {
		qualityKeys = append(qualityKeys, key)
	}
	sort.Strings(qualityKeys)
	out.WriteString("# HELP abra_working_memory_retrieval_quality_total Total working-memory executions by bounded retrieval-quality state.\n")
	out.WriteString("# TYPE abra_working_memory_retrieval_quality_total counter\n")
	for _, key := range qualityKeys {
		parts := strings.Split(key, "\n")
		metric := m.quality[key]
		out.WriteString(fmt.Sprintf(
			"abra_working_memory_retrieval_quality_total{status=%q,verdict=%q,quality=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			metric.Count,
		))
	}
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_top_rank_score_sum", "Total top rank score observed by working-memory retrieval quality.", "counter", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.TopRankSum })
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_top_text_score_sum", "Total top lexical score observed by working-memory retrieval quality.", "counter", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.TopTextSum })
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_top_vector_score_sum", "Total top vector score observed by working-memory retrieval quality.", "counter", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.TopVectorSum })
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_last_top_rank_score", "Last top rank score observed by working-memory retrieval quality.", "gauge", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.LastTopRank })
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_last_top_text_score", "Last top lexical score observed by working-memory retrieval quality.", "gauge", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.LastTopText })
	writeRetrievalQualityFloat(&out, "abra_working_memory_retrieval_last_top_vector_score", "Last top vector score observed by working-memory retrieval quality.", "gauge", qualityKeys, m.quality, func(metric *retrievalQualityMetric) float64 { return metric.LastTopVector })
	writeRetrievalQualityInt(&out, "abra_working_memory_retrieval_last_result_count", "Last result count observed by working-memory retrieval quality.", qualityKeys, m.quality, func(metric *retrievalQualityMetric) int64 { return metric.LastResultCount })

	healthKeys := make([]string, 0, len(m.health))
	for key := range m.health {
		healthKeys = append(healthKeys, key)
	}
	sort.Strings(healthKeys)
	out.WriteString("# HELP abra_working_memory_health_status_total Total working-memory executions by API status and scoped memory health status.\n")
	out.WriteString("# TYPE abra_working_memory_health_status_total counter\n")
	for _, key := range healthKeys {
		parts := strings.Split(key, "\n")
		metric := m.health[key]
		out.WriteString(fmt.Sprintf(
			"abra_working_memory_health_status_total{api_status=%q,health_status=%q} %d\n",
			parts[0],
			parts[1],
			metric.Count,
		))
	}
	writeMemoryHealthInt(&out, "abra_working_memory_health_signals_returned_sum", "Total memory health signals returned by working-memory executions.", "counter", healthKeys, m.health, func(metric *memoryHealthMetric) int64 { return metric.SignalCount })
	writeMemoryHealthInt(&out, "abra_working_memory_health_critical_signals_sum", "Total critical memory health signals returned by working-memory executions.", "counter", healthKeys, m.health, func(metric *memoryHealthMetric) int64 { return metric.CriticalSignals })
	writeMemoryHealthInt(&out, "abra_working_memory_health_warning_signals_sum", "Total warning memory health signals returned by working-memory executions.", "counter", healthKeys, m.health, func(metric *memoryHealthMetric) int64 { return metric.WarningSignals })
	writeMemoryHealthInt(&out, "abra_working_memory_health_last_score", "Last memory health score observed by working-memory executions.", "gauge", healthKeys, m.health, func(metric *memoryHealthMetric) int64 { return metric.LastScore })
	writeMemoryHealthInt(&out, "abra_working_memory_health_last_signal_count", "Last memory health signal count observed by working-memory executions.", "gauge", healthKeys, m.health, func(metric *memoryHealthMetric) int64 { return metric.LastSignals })

	healthLookupKeys := make([]string, 0, len(m.healthLookups))
	for key := range m.healthLookups {
		healthLookupKeys = append(healthLookupKeys, key)
	}
	sort.Strings(healthLookupKeys)
	out.WriteString("# HELP abra_working_memory_health_lookup_total Total working-memory memory-health lookups by API status and bounded cache status.\n")
	out.WriteString("# TYPE abra_working_memory_health_lookup_total counter\n")
	for _, key := range healthLookupKeys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"abra_working_memory_health_lookup_total{api_status=%q,cache_status=%q} %d\n",
			parts[0],
			parts[1],
			m.healthLookups[key],
		))
	}

	signalKeys := make([]string, 0, len(m.signals))
	for key := range m.signals {
		signalKeys = append(signalKeys, key)
	}
	sort.Strings(signalKeys)
	out.WriteString("# HELP abra_working_memory_health_signal_total Total memory health signals returned by working-memory executions with bounded signal labels.\n")
	out.WriteString("# TYPE abra_working_memory_health_signal_total counter\n")
	for _, key := range signalKeys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"abra_working_memory_health_signal_total{api_status=%q,health_status=%q,category=%q,severity=%q,code=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			parts[3],
			parts[4],
			m.signals[key],
		))
	}

	policyKeys := make([]string, 0, len(m.policies))
	for key := range m.policies {
		policyKeys = append(policyKeys, key)
	}
	sort.Strings(policyKeys)
	out.WriteString("# HELP abra_agent_policy_decisions_total Total stored agent-action policy decisions by bounded operation, action, and decision labels.\n")
	out.WriteString("# TYPE abra_agent_policy_decisions_total counter\n")
	for _, key := range policyKeys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"abra_agent_policy_decisions_total{operation=%q,action=%q,decision=%q} %d\n",
			parts[0],
			parts[1],
			parts[2],
			m.policies[key],
		))
	}

	return out.String()
}

func writeRetrievalQualityFloat(out *strings.Builder, name, help, metricType string, keys []string, metrics map[string]*retrievalQualityMetric, value func(*retrievalQualityMetric) float64) {
	out.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	out.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, metricType))
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"%s{status=%q,verdict=%q,quality=%q} %.6f\n",
			name,
			parts[0],
			parts[1],
			parts[2],
			value(metrics[key]),
		))
	}
}

func writeRetrievalQualityInt(out *strings.Builder, name, help string, keys []string, metrics map[string]*retrievalQualityMetric, value func(*retrievalQualityMetric) int64) {
	out.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	out.WriteString(fmt.Sprintf("# TYPE %s gauge\n", name))
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"%s{status=%q,verdict=%q,quality=%q} %d\n",
			name,
			parts[0],
			parts[1],
			parts[2],
			value(metrics[key]),
		))
	}
}

func writeMemoryHealthInt(out *strings.Builder, name, help, metricType string, keys []string, metrics map[string]*memoryHealthMetric, value func(*memoryHealthMetric) int64) {
	out.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	out.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, metricType))
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		out.WriteString(fmt.Sprintf(
			"%s{api_status=%q,health_status=%q} %d\n",
			name,
			parts[0],
			parts[1],
			value(metrics[key]),
		))
	}
}

func writeSmartPathCounter(out *strings.Builder, name, help string, keys []string, metrics map[string]*smartPathMetric, value func(*smartPathMetric) int64) {
	out.WriteString(fmt.Sprintf("# HELP %s %s\n", name, help))
	out.WriteString(fmt.Sprintf("# TYPE %s counter\n", name))
	for _, key := range keys {
		parts := strings.Split(key, "\n")
		metric := metrics[key]
		out.WriteString(fmt.Sprintf(
			"%s{operation=%q,status=%q,verdict=%q,decision=%q} %d\n",
			name,
			parts[0],
			parts[1],
			parts[2],
			parts[3],
			value(metric),
		))
	}
}
