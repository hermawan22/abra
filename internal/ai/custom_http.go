package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CustomHTTPProvider struct {
	name   string
	config CustomHTTPProviderConfig
	client *http.Client
}

func NewCustomHTTPProvider(config CustomHTTPProviderConfig, client *http.Client) (*CustomHTTPProvider, error) {
	provider := &CustomHTTPProvider{name: config.Name, config: config, client: client}
	if provider.name == "" {
		provider.name = "custom-http"
	}
	if provider.client == nil {
		provider.client = &http.Client{Timeout: 30 * time.Second}
	}
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *CustomHTTPProvider) Name() string {
	return p.name
}

func (p *CustomHTTPProvider) Kind() ProviderKind {
	return ProviderCustomHTTP
}

func (p *CustomHTTPProvider) Validate() error {
	if p.config.Embeddings == nil && p.config.Extractor == nil {
		return fmt.Errorf("%w: at least one endpoint is required", ErrInvalidConfig)
	}
	if p.config.Embeddings != nil && strings.TrimSpace(p.config.Embeddings.URL) == "" {
		return fmt.Errorf("%w: embeddings url is required", ErrInvalidConfig)
	}
	if p.config.Extractor != nil && strings.TrimSpace(p.config.Extractor.URL) == "" {
		return fmt.Errorf("%w: extractor url is required", ErrInvalidConfig)
	}
	return nil
}

func (p *CustomHTTPProvider) Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResponse, error) {
	if p.config.Embeddings == nil {
		return EmbeddingResponse{}, fmt.Errorf("%w: embeddings endpoint is not configured", ErrInvalidConfig)
	}
	if err := validateEmbeddingRequest(request); err != nil {
		return EmbeddingResponse{}, err
	}
	endpoint := *p.config.Embeddings
	body := map[string]any{
		"input": request.Input,
		"model": firstNonEmpty(request.Model, endpoint.Model),
	}
	if request.Dimensions > 0 {
		body["dimensions"] = request.Dimensions
	}
	raw, err := p.post(ctx, endpoint, body)
	if err != nil {
		return EmbeddingResponse{}, err
	}

	var payload struct {
		Embeddings [][]float64 `json:"embeddings"`
		Data       []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return EmbeddingResponse{}, fmt.Errorf("%w: decode custom embeddings: %v", ErrInvalidResponse, err)
	}
	vectors := payload.Embeddings
	if len(vectors) == 0 && len(payload.Data) > 0 {
		vectors = make([][]float64, len(payload.Data))
		for _, item := range payload.Data {
			index := item.Index
			if index < 0 || index >= len(vectors) {
				index = len(vectors) - 1
			}
			vectors[index] = item.Embedding
		}
	}
	if len(vectors) != len(request.Input) {
		return EmbeddingResponse{}, fmt.Errorf("%w: embedding count mismatch", ErrInvalidResponse)
	}
	dimensions := embeddingDimensions(request, endpoint.Dimensions)
	embeddings := make([]Embedding, 0, len(vectors))
	for index, vector := range vectors {
		if err := ValidateEmbeddingDimensions(vector, dimensions); err != nil {
			return EmbeddingResponse{}, err
		}
		embeddings = append(embeddings, Embedding{Index: index, Vector: vector, Dimensions: len(vector)})
	}
	return EmbeddingResponse{Provider: p.name, Model: firstNonEmpty(payload.Model, endpoint.Model), Embeddings: embeddings, Usage: payload.Usage, Raw: raw}, nil
}

func (p *CustomHTTPProvider) Extract(ctx context.Context, request ExtractionRequest) (ExtractionResponse, error) {
	if p.config.Extractor == nil {
		return ExtractionResponse{}, fmt.Errorf("%w: extractor endpoint is not configured", ErrInvalidConfig)
	}
	if strings.TrimSpace(request.Input) == "" {
		return ExtractionResponse{}, fmt.Errorf("%w: input is required", ErrInvalidRequest)
	}
	endpoint := *p.config.Extractor
	body := map[string]any{
		"input":        request.Input,
		"instructions": request.Instructions,
		"model":        firstNonEmpty(request.Model, endpoint.Model),
		"schema":       request.Schema,
	}
	raw, err := p.post(ctx, endpoint, body)
	if err != nil {
		return ExtractionResponse{}, err
	}
	var payload struct {
		Value any    `json:"value"`
		Text  string `json:"text"`
		Model string `json:"model"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ExtractionResponse{}, fmt.Errorf("%w: decode custom extractor: %v", ErrInvalidResponse, err)
	}
	if payload.Value != nil {
		validationErrors, err := ValidateJSONValue(payload.Value, request.Schema)
		if err != nil {
			return ExtractionResponse{}, err
		}
		return ExtractionResponse{Provider: p.name, Model: firstNonEmpty(payload.Model, endpoint.Model), Value: payload.Value, Raw: string(raw), ValidationErrors: validationErrors, Usage: payload.Usage}, nil
	}
	value, repaired, validationErrors, err := ParseAndValidateJSON(ctx, payload.Text, request.Schema, DefaultJSONRepairer())
	if err != nil {
		return ExtractionResponse{}, err
	}
	return ExtractionResponse{Provider: p.name, Model: firstNonEmpty(payload.Model, endpoint.Model), Value: value, Raw: payload.Text, Repaired: repaired, ValidationErrors: validationErrors, Usage: payload.Usage}, nil
}

func (p *CustomHTTPProvider) post(ctx context.Context, endpoint CustomHTTPEndpointConfig, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: encode custom request: %v", ErrInvalidRequest, err)
	}
	method := endpoint.Method
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: create custom request: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("content-type", "application/json")
	for key, value := range p.config.Headers {
		request.Header.Set(key, value)
	}
	for key, value := range endpoint.Headers {
		request.Header.Set(key, value)
	}
	applyCustomAuth(request, endpoint.Auth)
	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read custom response: %v", ErrInvalidResponse, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("custom provider failed: status=%d body=%s", response.StatusCode, string(raw))
	}
	return raw, nil
}

func applyCustomAuth(request *http.Request, auth *CustomHTTPAuthConfig) {
	if auth == nil {
		return
	}
	if auth.BearerToken != "" {
		request.Header.Set("authorization", "Bearer "+auth.BearerToken)
	}
	if auth.HeaderName != "" && auth.Token != "" {
		request.Header.Set(auth.HeaderName, auth.Token)
	}
	if auth.QueryName != "" && auth.QueryValue != "" {
		query := request.URL.Query()
		query.Set(auth.QueryName, auth.QueryValue)
		request.URL.RawQuery = query.Encode()
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
