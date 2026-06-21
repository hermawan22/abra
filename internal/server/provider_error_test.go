package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/ai"
)

func TestProviderErrorPayloadIsStructuredAndBounded(t *testing.T) {
	providerErr := &ai.ProviderError{
		Operation:   "embedding",
		Provider:    "local",
		Model:       "qwen",
		Code:        "auth_failed",
		Status:      http.StatusUnauthorized,
		Retryable:   false,
		Attempts:    1,
		BatchStart:  4,
		BatchEnd:    6,
		BatchSize:   2,
		BatchTokens: 90,
		Message:     "missing auth",
	}
	payload := providerErrorPayload(fmt.Errorf("ingest failed: %w", providerErr), providerErr)

	if payload["error_kind"] != "provider_error" {
		t.Fatalf("error_kind = %v", payload["error_kind"])
	}
	detail, ok := payload["provider_error"].(map[string]any)
	if !ok {
		t.Fatalf("provider_error = %#v", payload["provider_error"])
	}
	if detail["operation"] != "embedding" || detail["code"] != "auth_failed" || detail["status_code"] != http.StatusUnauthorized {
		t.Fatalf("provider detail = %#v", detail)
	}
	if detail["batch_size"] != 2 || detail["batch_start"] != 4 || detail["batch_end"] != 6 || detail["batch_tokens"] != 90 {
		t.Fatalf("batch detail = %#v", detail)
	}
	if strings.Contains(fmt.Sprint(payload["error"]), "sk-") {
		t.Fatalf("payload error leaked secret: %#v", payload)
	}
}
