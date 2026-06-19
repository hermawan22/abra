package observability

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type AIProviderMetric struct {
	Operation      string
	Provider       string
	Status         string
	Calls          int64
	Waits          int64
	DurationMS     int64
	WaitMS         int64
	LastDurationMS int64
	LastWaitMS     int64
	InFlight       int64
	Waiting        int64
	MaxInFlight    int64
	MaxWaiting     int64
}

var aiProviderMetrics = &aiProviderMetricStore{
	calls:  map[string]*AIProviderMetric{},
	waits:  map[string]*AIProviderMetric{},
	gauges: map[string]*AIProviderMetric{},
}

type aiProviderMetricStore struct {
	mu     sync.Mutex
	calls  map[string]*AIProviderMetric
	waits  map[string]*AIProviderMetric
	gauges map[string]*AIProviderMetric
}

func AIProviderWaitingStart(operation, provider string) {
	operation = normalizeAIProviderOperation(operation)
	provider = normalizeAIProviderName(provider)
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	metric := aiProviderMetrics.gaugeMetric(operation, provider)
	metric.Waiting++
	if metric.Waiting > metric.MaxWaiting {
		metric.MaxWaiting = metric.Waiting
	}
}

func AIProviderWaitingDone(operation, provider, status string, duration time.Duration) {
	operation = normalizeAIProviderOperation(operation)
	provider = normalizeAIProviderName(provider)
	status = normalizeAIProviderStatus(status)
	durationMS := duration.Milliseconds()
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	gauge := aiProviderMetrics.gaugeMetric(operation, provider)
	if gauge.Waiting > 0 {
		gauge.Waiting--
	}
	metric := aiProviderMetrics.waitMetric(operation, provider, status)
	metric.Waits++
	metric.WaitMS += durationMS
	metric.LastWaitMS = durationMS
}

func AIProviderInFlightStart(operation, provider string) {
	operation = normalizeAIProviderOperation(operation)
	provider = normalizeAIProviderName(provider)
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	metric := aiProviderMetrics.gaugeMetric(operation, provider)
	metric.InFlight++
	if metric.InFlight > metric.MaxInFlight {
		metric.MaxInFlight = metric.InFlight
	}
}

func AIProviderInFlightDone(operation, provider string) {
	operation = normalizeAIProviderOperation(operation)
	provider = normalizeAIProviderName(provider)
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	metric := aiProviderMetrics.gaugeMetric(operation, provider)
	if metric.InFlight > 0 {
		metric.InFlight--
	}
}

func ObserveAIProviderCall(operation, provider, status string, duration time.Duration) {
	operation = normalizeAIProviderOperation(operation)
	provider = normalizeAIProviderName(provider)
	status = normalizeAIProviderStatus(status)
	durationMS := duration.Milliseconds()
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	metric := aiProviderMetrics.callMetric(operation, provider, status)
	metric.Calls++
	metric.DurationMS += durationMS
	metric.LastDurationMS = durationMS
}

func AIProviderMetricsSnapshot() []AIProviderMetric {
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	out := make([]AIProviderMetric, 0, len(aiProviderMetrics.calls)+len(aiProviderMetrics.waits)+len(aiProviderMetrics.gauges))
	for _, metric := range aiProviderMetrics.calls {
		out = append(out, *metric)
	}
	for _, metric := range aiProviderMetrics.waits {
		out = append(out, *metric)
	}
	for _, metric := range aiProviderMetrics.gauges {
		out = append(out, *metric)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Operation != out[j].Operation {
			return out[i].Operation < out[j].Operation
		}
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Status < out[j].Status
	})
	return out
}

func ResetAIProviderMetricsForTest() {
	aiProviderMetrics.mu.Lock()
	defer aiProviderMetrics.mu.Unlock()
	aiProviderMetrics.calls = map[string]*AIProviderMetric{}
	aiProviderMetrics.waits = map[string]*AIProviderMetric{}
	aiProviderMetrics.gauges = map[string]*AIProviderMetric{}
}

func (s *aiProviderMetricStore) callMetric(operation, provider, status string) *AIProviderMetric {
	key := operation + "\n" + provider + "\n" + status
	metric := s.calls[key]
	if metric == nil {
		metric = &AIProviderMetric{Operation: operation, Provider: provider, Status: status}
		s.calls[key] = metric
	}
	return metric
}

func (s *aiProviderMetricStore) waitMetric(operation, provider, status string) *AIProviderMetric {
	key := operation + "\n" + provider + "\n" + status
	metric := s.waits[key]
	if metric == nil {
		metric = &AIProviderMetric{Operation: operation, Provider: provider, Status: status}
		s.waits[key] = metric
	}
	return metric
}

func (s *aiProviderMetricStore) gaugeMetric(operation, provider string) *AIProviderMetric {
	key := operation + "\n" + provider
	metric := s.gauges[key]
	if metric == nil {
		metric = &AIProviderMetric{Operation: operation, Provider: provider}
		s.gauges[key] = metric
	}
	return metric
}

func normalizeAIProviderOperation(value string) string {
	switch strings.TrimSpace(value) {
	case "embedding", "rerank":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}

func normalizeAIProviderName(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "local", "compatible", "openai-compatible", "openai", "qwen3", "local-smart", "tei", "embeddinggemma", "bge-m3", "voyage", "zeroentropy":
		return strings.ToLower(strings.TrimSpace(value))
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func normalizeAIProviderStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "ok", "error", "canceled":
		return strings.TrimSpace(value)
	default:
		return "other"
	}
}
