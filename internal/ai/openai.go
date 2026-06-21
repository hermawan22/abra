package ai

import (
	"context"
	"encoding/json"
	"fmt"
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
	if p.config.EmbeddingModel == "" && p.config.RerankerModel == "" && p.config.ChatModel == "" {
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

	raw, err := p.postJSON(ctx, "/embeddings", body, providerRequestContext{
		Operation: "embedding",
		Provider:  p.name,
		Model:     model,
		BatchSize: len(request.Input),
		Metadata:  request.Metadata,
	})
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

func (p *OpenAICompatibleProvider) Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error) {
	if err := validateRerankRequest(request); err != nil {
		return RerankResponse{}, err
	}
	model := request.Model
	if model == "" {
		model = p.config.RerankerModel
	}
	body := map[string]any{
		"query": request.Query,
		"texts": request.Documents,
	}
	if model != "" {
		body["model"] = model
	}
	if request.TopN > 0 {
		body["top_n"] = request.TopN
	}

	raw, err := p.postJSON(ctx, "/rerank", body, providerRequestContext{
		Operation: "rerank",
		Provider:  p.name,
		Model:     model,
		BatchSize: len(request.Documents),
		Metadata:  request.Metadata,
	})
	if err != nil {
		return RerankResponse{}, err
	}
	results, responseModel, usage, err := decodeRerankResponse(raw)
	if err != nil {
		return RerankResponse{}, err
	}
	if responseModel == "" {
		responseModel = model
	}
	return RerankResponse{
		Provider: p.name,
		Model:    responseModel,
		Results:  results,
		Usage:    usage,
		Raw:      raw,
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

	raw, err := p.postJSON(ctx, "/chat/completions", body, providerRequestContext{
		Operation: "extract",
		Provider:  p.name,
		Model:     model,
		BatchSize: 1,
		Metadata:  request.Metadata,
	})
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

func decodeRerankResponse(raw []byte) ([]RerankResult, string, *Usage, error) {
	var asArray []rerankPayloadResult
	if err := json.Unmarshal(raw, &asArray); err == nil && asArray != nil {
		return normalizeRerankResults(asArray), "", nil, nil
	}

	var payload struct {
		Results []rerankPayloadResult `json:"results"`
		Data    []rerankPayloadResult `json:"data"`
		Model   string                `json:"model"`
		Usage   *Usage                `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", nil, fmt.Errorf("%w: decode rerank response: %v", ErrInvalidResponse, err)
	}
	results := payload.Results
	if len(results) == 0 {
		results = payload.Data
	}
	if len(results) == 0 {
		return nil, payload.Model, payload.Usage, fmt.Errorf("%w: rerank response did not include results", ErrInvalidResponse)
	}
	return normalizeRerankResults(results), payload.Model, payload.Usage, nil
}

type rerankPayloadResult struct {
	Index          int     `json:"index"`
	Score          float64 `json:"score"`
	RelevanceScore float64 `json:"relevance_score"`
	Text           string  `json:"text"`
	Document       any     `json:"document"`
}

func normalizeRerankResults(items []rerankPayloadResult) []RerankResult {
	results := make([]RerankResult, 0, len(items))
	for responseIndex, item := range items {
		index := item.Index
		if index == 0 && responseIndex > 0 {
			index = responseIndex
		}
		score := item.Score
		if score == 0 && item.RelevanceScore != 0 {
			score = item.RelevanceScore
		}
		text := item.Text
		if text == "" {
			if value, ok := item.Document.(string); ok {
				text = value
			}
		}
		results = append(results, RerankResult{Index: index, Score: score, Text: text})
	}
	return results
}

func (p *OpenAICompatibleProvider) postJSON(ctx context.Context, path string, body any, requestContext providerRequestContext) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrInvalidRequest, err)
	}
	url := strings.TrimRight(p.config.BaseURL, "/") + path
	requestCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	return doProviderHTTPRequest(requestCtx, p.client, providerHTTPRequest{
		Method:        http.MethodPost,
		URL:           url,
		Operation:     firstNonEmpty(requestContext.Operation, operationForPath(path)),
		Provider:      requestContext.Provider,
		Model:         requestContext.Model,
		BatchStart:    metadataInt(requestContext.Metadata, "batch_start"),
		BatchEnd:      metadataInt(requestContext.Metadata, "batch_end"),
		BatchSize:     firstPositive(requestContext.BatchSize, metadataInt(requestContext.Metadata, "batch_size")),
		BatchTokens:   metadataInt(requestContext.Metadata, "batch_tokens"),
		Body:          payload,
		FailurePrefix: "ai provider request failed",
		ReadPrefix:    "read response",
		Configure: func(request *http.Request) {
			request.Header.Set("content-type", "application/json")
			if strings.TrimSpace(p.config.APIKey) != "" {
				request.Header.Set("authorization", "Bearer "+p.config.APIKey)
			}
			if p.config.Organization != "" {
				request.Header.Set("OpenAI-Organization", p.config.Organization)
			}
			if p.config.Project != "" {
				request.Header.Set("OpenAI-Project", p.config.Project)
			}
			for key, value := range p.config.Headers {
				request.Header.Set(key, value)
			}
		},
	})
}

type providerRequestContext struct {
	Operation string
	Provider  string
	Model     string
	BatchSize int
	Metadata  map[string]any
}

func operationForPath(path string) string {
	switch {
	case strings.Contains(path, "embeddings"):
		return "embedding"
	case strings.Contains(path, "rerank"):
		return "rerank"
	case strings.Contains(path, "chat"):
		return "extract"
	default:
		return "provider"
	}
}
