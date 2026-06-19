package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	if len(response.Results) != 2 || response.Results[0].Index != 1 {
		t.Fatalf("results = %#v", response.Results)
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

func TestCustomHTTPProviderMapsEmbeddingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "secret" {
			t.Fatalf("x-api-key = %q", got)
		}
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2],[0.3,0.4]]}`))
	}))
	defer server.Close()

	provider, err := NewCustomHTTPProvider(CustomHTTPProviderConfig{
		Name: "vendor",
		Embeddings: &CustomHTTPEndpointConfig{
			URL:               server.URL,
			ResponseValuePath: "vectors",
			Dimensions:        2,
			Auth: &CustomHTTPAuthConfig{
				Type:       "header",
				HeaderName: "x-api-key",
				Token:      "secret",
			},
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewCustomHTTPProvider() error = %v", err)
	}

	response, err := provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(response.Embeddings) != 2 {
		t.Fatalf("embeddings = %d, want 2", len(response.Embeddings))
	}
}

func TestCustomHTTPProviderRetriesRetryableStatus(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2]]}`))
	}))
	defer server.Close()

	provider, err := NewCustomHTTPProvider(CustomHTTPProviderConfig{
		Name: "vendor",
		Embeddings: &CustomHTTPEndpointConfig{
			URL:               server.URL,
			ResponseValuePath: "vectors",
			Dimensions:        2,
		},
	}, server.Client())
	if err != nil {
		t.Fatalf("NewCustomHTTPProvider() error = %v", err)
	}

	response, err := provider.Embed(context.Background(), EmbeddingRequest{Input: []string{"a"}})
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
