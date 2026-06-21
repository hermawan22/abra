package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type ProviderError struct {
	Operation   string
	Provider    string
	Model       string
	Code        string
	Status      int
	Retryable   bool
	Attempts    int
	BatchStart  int
	BatchEnd    int
	BatchSize   int
	BatchTokens int
	Message     string
	Err         error
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (e *ProviderError) Error() string {
	parts := []string{"ai provider request failed"}
	if e.Operation != "" {
		parts = append(parts, "operation="+e.Operation)
	}
	if e.Provider != "" {
		parts = append(parts, "provider="+e.Provider)
	}
	if e.Model != "" {
		parts = append(parts, "model="+e.Model)
	}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if e.Status > 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.Status))
	}
	if e.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("attempts=%d", e.Attempts))
	}
	if e.BatchSize > 0 {
		parts = append(parts, fmt.Sprintf("batch_size=%d", e.BatchSize))
	}
	if e.BatchTokens > 0 {
		parts = append(parts, fmt.Sprintf("batch_tokens=%d", e.BatchTokens))
	}
	if e.Retryable {
		parts = append(parts, "retryable=true")
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	if e.Err != nil {
		parts = append(parts, "cause="+redactProviderErrorText(e.Err.Error()))
	}
	return strings.Join(parts, " ")
}

func (e *ProviderError) Unwrap() error {
	return e.Err
}

func (e *ProviderError) HTTPStatus() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Code {
	case "auth_failed":
		return http.StatusUnauthorized
	case "rate_limited":
		return http.StatusTooManyRequests
	case "provider_timeout":
		return http.StatusGatewayTimeout
	case "provider_unreachable", "provider_unavailable":
		return http.StatusServiceUnavailable
	case "invalid_request", "invalid_response", "dimension_mismatch":
		return http.StatusBadRequest
	default:
		if e.Status >= 400 && e.Status < 500 {
			return http.StatusBadRequest
		}
		if e.Status >= 500 {
			return http.StatusServiceUnavailable
		}
		return http.StatusBadGateway
	}
}

func ProviderErrorInfo(err error) (*ProviderError, bool) {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr, true
	}
	return nil, false
}

func classifyHTTPStatus(status int) (string, bool) {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth_failed", false
	case status == http.StatusTooManyRequests:
		return "rate_limited", true
	case retryableProviderStatus(status):
		return "provider_unavailable", true
	case status == http.StatusBadRequest:
		return "invalid_request", false
	case status >= 400 && status < 500:
		return "invalid_request", false
	default:
		return "provider_error", false
	}
}

func classifyRequestError(err error) (string, bool) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "provider_timeout", true
	case errors.Is(err, context.Canceled):
		return "provider_canceled", false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "provider_timeout", true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "provider_unreachable", true
	}
	var pathErr *os.SyscallError
	if errors.As(err, &pathErr) {
		return "provider_unreachable", true
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "connection refused") || strings.Contains(text, "no such host") || strings.Contains(text, "connect:") {
		return "provider_unreachable", true
	}
	return "provider_error", true
}
