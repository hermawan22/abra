package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidateEmbeddingDimensionsRejectsMismatch(t *testing.T) {
	err := ValidateEmbeddingDimensions([]float64{0.1, 0.2}, 3)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error = %v, want ErrInvalidResponse", err)
	}
}

func TestOpenAICompatibleProviderEmbedsAndValidatesDimensions(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path = %s, want /embeddings", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"embed-model","data":[{"index":0,"embedding":[0.1,0.2,0.3]}],"usage":{"total_tokens":4}}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:             server.URL,
		APIKey:              "test-key",
		EmbeddingModel:      "embed-model",
		EmbeddingDimensions: 3,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	response, err := provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"hello"}})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}

	if requestBody["model"] != "embed-model" {
		t.Fatalf("model = %v, want embed-model", requestBody["model"])
	}
	if requestBody["dimensions"] != float64(3) {
		t.Fatalf("dimensions = %v, want 3", requestBody["dimensions"])
	}
	if got := response.Embeddings[0].Dimensions; got != 3 {
		t.Fatalf("dimensions = %d, want 3", got)
	}
}

func TestOpenAICompatibleProviderRejectsEmbeddingDimensionMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:             server.URL,
		APIKey:              "test-key",
		EmbeddingModel:      "embed-model",
		EmbeddingDimensions: 3,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"hello"}})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("Embed() error = %v, want ErrInvalidResponse", err)
	}
}

func TestOpenAICompatibleProviderRetriesRetryableStatus(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"model":"embed-model","data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:             server.URL,
		EmbeddingModel:      "embed-model",
		EmbeddingDimensions: 2,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	response, err := provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"hello"}})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if len(response.Embeddings) != 1 {
		t.Fatalf("embeddings = %d, want 1", len(response.Embeddings))
	}
}

func TestOpenAICompatibleProviderDoesNotRetryValidationStatus(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:        server.URL,
		EmbeddingModel: "embed-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"hello"}})
	if err == nil || !strings.Contains(err.Error(), "status=400") {
		t.Fatalf("Embed() error = %v, want status=400", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestOpenAICompatibleProviderReturnsStructuredProviderError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		http.Error(w, `{"error":{"message":"bad key sk-secret12345678"}}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:             server.URL,
		EmbeddingModel:      "embed-model",
		EmbeddingDimensions: 2,
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Embed(context.Background(), EmbeddingRequest{
		Input: []string{"hello"},
		Metadata: map[string]any{
			"batch_start":  2,
			"batch_end":    3,
			"batch_size":   1,
			"batch_tokens": 11,
		},
	})
	providerErr, ok := ProviderErrorInfo(err)
	if !ok {
		t.Fatalf("Embed() error = %T %[1]v, want ProviderError", err)
	}
	if providerErr.Operation != "embedding" || providerErr.Provider != "openai-compatible" || providerErr.Model != "embed-model" {
		t.Fatalf("provider error identity = %#v", providerErr)
	}
	if providerErr.Code != "rate_limited" || providerErr.Status != http.StatusTooManyRequests || !providerErr.Retryable || providerErr.Attempts != providerHTTPMaxAttempts {
		t.Fatalf("provider error classification = %#v", providerErr)
	}
	if providerErr.BatchStart != 2 || providerErr.BatchEnd != 3 || providerErr.BatchSize != 1 || providerErr.BatchTokens != 11 {
		t.Fatalf("provider error batch metadata = %#v", providerErr)
	}
	if strings.Contains(providerErr.Error(), "sk-secret") {
		t.Fatalf("provider error leaked secret: %s", providerErr.Error())
	}
	if attempts != providerHTTPMaxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, providerHTTPMaxAttempts)
	}
}

func TestOpenAICompatibleProviderClassifiesContextOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"requested tokens exceeds model context n_ctx"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:        server.URL,
		EmbeddingModel: "embed-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	_, err = provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"hello"}})
	providerErr, ok := ProviderErrorInfo(err)
	if !ok {
		t.Fatalf("Embed() error = %T %[1]v, want ProviderError", err)
	}
	if providerErr.Code != "context_overflow" || providerErr.Status != http.StatusBadRequest || !providerErr.Retryable {
		t.Fatalf("provider error classification = %#v", providerErr)
	}
	if providerErr.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("HTTPStatus = %d, want %d", providerErr.HTTPStatus(), http.StatusBadRequest)
	}
	if !strings.Contains(providerErr.Hint, "batch") {
		t.Fatalf("hint = %q", providerErr.Hint)
	}
}

func TestProviderErrorRedactsTransportURLSecrets(t *testing.T) {
	transportErr := &url.Error{
		Op:  "Post",
		URL: "https://provider.example/v1/embeddings?api_key=sk-secret12345678&token=secret-token-123",
		Err: fmt.Errorf("dial tcp: connection refused"),
	}
	err := &ProviderError{
		Operation: "embedding",
		Code:      "provider_unreachable",
		Err:       transportErr,
	}
	text := err.Error()
	for _, leaked := range []string{"sk-secret", "secret-token", "api_key=sk-", "token=secret"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("provider error leaked %q in %s", leaked, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("provider error did not show redacted marker: %s", text)
	}
}

func TestOpenAICompatibleProviderReranks(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Fatalf("path = %s, want /rerank", r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "" {
			t.Fatalf("authorization = %q, want empty for local provider", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"rerank-model","results":[{"index":1,"score":0.91},{"index":0,"score":0.42}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:       server.URL,
		RerankerModel: "rerank-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	response, err := provider.Rerank(context.Background(), RerankRequest{
		Query:     "abra memory",
		Documents: []string{"doc one", "doc two"},
		TopN:      2,
	})
	if err != nil {
		t.Fatalf("Rerank() error = %v", err)
	}
	if requestBody["query"] != "abra memory" {
		t.Fatalf("query = %v", requestBody["query"])
	}
	if requestBody["model"] != "rerank-model" {
		t.Fatalf("model = %v", requestBody["model"])
	}
	if _, ok := requestBody["texts"]; !ok {
		t.Fatalf("compatible rerank body missing texts: %#v", requestBody)
	}
	if _, ok := requestBody["documents"]; ok {
		t.Fatalf("compatible rerank body should not include documents: %#v", requestBody)
	}
	if len(response.Results) != 2 || response.Results[0].Index != 1 {
		t.Fatalf("results = %#v", response.Results)
	}
}

func TestOpenAICompatibleProviderLocalRerankUsesDocuments(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rerank" {
			t.Fatalf("path = %s, want /rerank", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"model":"rerank-model","results":[{"index":0,"score":0.91}]}`))
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		Name:          "local",
		BaseURL:       server.URL,
		RerankerModel: "rerank-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}

	if _, err := provider.Rerank(context.Background(), RerankRequest{
		Query:     "abra memory",
		Documents: []string{"doc one"},
		TopN:      1,
	}); err != nil {
		t.Fatalf("Rerank() error = %v", err)
	}
	if _, ok := requestBody["documents"]; !ok {
		t.Fatalf("local rerank body missing documents: %#v", requestBody)
	}
	if _, ok := requestBody["texts"]; ok {
		t.Fatalf("local rerank body should not include texts: %#v", requestBody)
	}
}

func TestOpenAICompatibleProviderExtractsWithJSONSchemaAndRepair(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "chat-model",
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"content": "```json\n{\"claims\":[\"a\",]}\n```",
					},
				},
			},
		})
	}))
	defer server.Close()

	provider, err := NewOpenAICompatibleProvider(OpenAICompatibleConfig{
		BaseURL:   server.URL,
		APIKey:    "test-key",
		ChatModel: "chat-model",
	}, server.Client())
	if err != nil {
		t.Fatalf("NewOpenAICompatibleProvider() error = %v", err)
	}
	schema := JSONSchema{
		"type": "object",
		"required": []any{
			"claims",
		},
		"properties": map[string]any{
			"claims": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "string",
				},
			},
		},
	}

	response, err := provider.Extract(context.Background(), ExtractionRequest{
		Input:      "extract claims",
		Schema:     schema,
		SchemaName: "claims",
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	format, ok := requestBody["response_format"].(map[string]any)
	if !ok || format["type"] != "json_schema" {
		t.Fatalf("response_format = %#v, want json_schema", requestBody["response_format"])
	}
	if !response.Repaired {
		t.Fatalf("Repaired = false, want true")
	}
	value := response.Value.(map[string]any)
	claims := value["claims"].([]any)
	if claims[0] != "a" {
		t.Fatalf("claim = %v, want a", claims[0])
	}
}

func TestValidateJSONValueRejectsMissingRequiredAndAdditionalProperties(t *testing.T) {
	schema := JSONSchema{
		"type":                 "object",
		"required":             []any{"id"},
		"additionalProperties": false,
		"properties": map[string]any{
			"id": map[string]any{"type": "string"},
		},
	}

	validationErrors, err := ValidateJSONValue(map[string]any{"extra": "nope"}, schema)
	if !errors.Is(err, ErrValidationFailed) {
		t.Fatalf("error = %v, want ErrValidationFailed", err)
	}
	if len(validationErrors) != 2 {
		t.Fatalf("validation errors = %v, want 2 errors", validationErrors)
	}
}
