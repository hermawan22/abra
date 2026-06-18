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

type OpenAICompatibleProvider struct {
	name   string
	config OpenAICompatibleConfig
	client *http.Client
}

func NewOpenAICompatibleProvider(config OpenAICompatibleConfig, client *http.Client) (*OpenAICompatibleProvider, error) {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	provider := &OpenAICompatibleProvider{
		name:   config.Name,
		config: config,
		client: client,
	}
	if provider.name == "" {
		provider.name = "openai-compatible"
	}
	if provider.client == nil {
		provider.client = &http.Client{Timeout: config.Timeout}
	}
	if err := provider.Validate(); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *OpenAICompatibleProvider) Name() string {
	return p.name
}

func (p *OpenAICompatibleProvider) Kind() ProviderKind {
	return ProviderOpenAICompatible
}

func (p *OpenAICompatibleProvider) Validate() error {
	if strings.TrimSpace(p.config.BaseURL) == "" {
		return fmt.Errorf("%w: base url is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(p.config.APIKey) == "" {
		return fmt.Errorf("%w: api key is required", ErrInvalidConfig)
	}
	if p.config.EmbeddingModel == "" && p.config.ChatModel == "" {
		return fmt.Errorf("%w: at least one model is required", ErrInvalidConfig)
	}
	if p.config.EmbeddingDimensions < 0 {
		return fmt.Errorf("%w: embedding dimensions must be non-negative", ErrInvalidConfig)
	}
	return nil
}

func (p *OpenAICompatibleProvider) Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResponse, error) {
	if err := validateEmbeddingRequest(request); err != nil {
		return EmbeddingResponse{}, err
	}
	model := request.Model
	if model == "" {
		model = p.config.EmbeddingModel
	}
	if model == "" {
		return EmbeddingResponse{}, fmt.Errorf("%w: embedding model is required", ErrInvalidRequest)
	}

	body := map[string]any{
		"model": model,
		"input": request.Input,
	}
	dimensions := embeddingDimensions(request, p.config.EmbeddingDimensions)
	if dimensions > 0 {
		body["dimensions"] = dimensions
	}
	if request.User != "" {
		body["user"] = request.User
	}

	raw, err := p.postJSON(ctx, "/embeddings", body)
	if err != nil {
		return EmbeddingResponse{}, err
	}

	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return EmbeddingResponse{}, fmt.Errorf("%w: decode embeddings response: %v", ErrInvalidResponse, err)
	}
	if len(payload.Data) != len(request.Input) {
		return EmbeddingResponse{}, fmt.Errorf("%w: embedding count mismatch: got %d, want %d", ErrInvalidResponse, len(payload.Data), len(request.Input))
	}

	embeddings := make([]Embedding, 0, len(payload.Data))
	for responseIndex, item := range payload.Data {
		index := item.Index
		if index == 0 && responseIndex > 0 {
			index = responseIndex
		}
		if err := ValidateEmbeddingDimensions(item.Embedding, dimensions); err != nil {
			return EmbeddingResponse{}, err
		}
		embeddings = append(embeddings, Embedding{
			Index:      index,
			Vector:     item.Embedding,
			Dimensions: len(item.Embedding),
		})
	}
	if payload.Model == "" {
		payload.Model = model
	}

	return EmbeddingResponse{
		Provider:   p.name,
		Model:      payload.Model,
		Embeddings: embeddings,
		Usage:      payload.Usage,
		Raw:        raw,
	}, nil
}

func (p *OpenAICompatibleProvider) Extract(ctx context.Context, request ExtractionRequest) (ExtractionResponse, error) {
	if strings.TrimSpace(request.Input) == "" {
		return ExtractionResponse{}, fmt.Errorf("%w: input is required", ErrInvalidRequest)
	}
	model := request.Model
	if model == "" {
		model = p.config.ChatModel
	}
	if model == "" {
		return ExtractionResponse{}, fmt.Errorf("%w: chat model is required", ErrInvalidRequest)
	}

	messages := []map[string]string{}
	if strings.TrimSpace(request.Instructions) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": request.Instructions,
		})
	}
	messages = append(messages, map[string]string{
		"role":    "user",
		"content": request.Input,
	})

	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.MaxTokens > 0 {
		body["max_tokens"] = request.MaxTokens
	}
	if len(request.Schema) > 0 {
		name := request.SchemaName
		if name == "" {
			name = "extraction"
		}
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   name,
				"schema": request.Schema,
				"strict": true,
			},
		}
	}

	raw, err := p.postJSON(ctx, "/chat/completions", body)
	if err != nil {
		return ExtractionResponse{}, err
	}

	var payload struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ExtractionResponse{}, fmt.Errorf("%w: decode chat response: %v", ErrInvalidResponse, err)
	}
	if len(payload.Choices) == 0 || strings.TrimSpace(payload.Choices[0].Message.Content) == "" {
		return ExtractionResponse{}, fmt.Errorf("%w: chat response did not include choices[0].message.content", ErrInvalidResponse)
	}

	value, repaired, validationErrors, err := ParseAndValidateJSON(ctx, payload.Choices[0].Message.Content, request.Schema, DefaultJSONRepairer())
	if err != nil {
		return ExtractionResponse{}, err
	}
	if payload.Model == "" {
		payload.Model = model
	}

	return ExtractionResponse{
		Provider:         p.name,
		Model:            payload.Model,
		Value:            value,
		Raw:              payload.Choices[0].Message.Content,
		Repaired:         repaired,
		ValidationErrors: validationErrors,
		Usage:            payload.Usage,
	}, nil
}

func (p *OpenAICompatibleProvider) postJSON(ctx context.Context, path string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrInvalidRequest, err)
	}
	url := strings.TrimRight(p.config.BaseURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%w: create request: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+p.config.APIKey)
	if p.config.Organization != "" {
		request.Header.Set("OpenAI-Organization", p.config.Organization)
	}
	if p.config.Project != "" {
		request.Header.Set("OpenAI-Project", p.config.Project)
	}
	for key, value := range p.config.Headers {
		request.Header.Set(key, value)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ai provider request failed: %w", err)
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if readErr != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrInvalidResponse, readErr)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("ai provider request failed: status=%d body=%s", response.StatusCode, string(raw))
	}
	return raw, nil
}
