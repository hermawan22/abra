package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var (
	ErrInvalidConfig     = errors.New("invalid ai provider config")
	ErrInvalidRequest    = errors.New("invalid ai request")
	ErrInvalidResponse   = errors.New("invalid ai provider response")
	ErrValidationFailed  = errors.New("json validation failed")
	ErrRepairUnavailable = errors.New("json repair unavailable")
)

type ProviderKind string

const (
	ProviderOpenAICompatible ProviderKind = "openai-compatible"
	ProviderCustomHTTP       ProviderKind = "custom-http"
)

type Provider interface {
	Name() string
	Kind() ProviderKind
	Validate() error
}

type EmbeddingProvider interface {
	Provider
	Embed(ctx context.Context, request EmbeddingRequest) (EmbeddingResponse, error)
}

type RerankerProvider interface {
	Provider
	Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error)
}

type ExtractorProvider interface {
	Provider
	Extract(ctx context.Context, request ExtractionRequest) (ExtractionResponse, error)
}

type EmbeddingRequest struct {
	Input      []string
	Model      string
	Dimensions int
	User       string
	Metadata   map[string]any
}

type EmbeddingResponse struct {
	Provider   string
	Model      string
	Embeddings []Embedding
	Usage      *Usage
	Raw        []byte
}

type Embedding struct {
	Index      int
	Vector     []float64
	Dimensions int
}

type RerankRequest struct {
	Query     string
	Documents []string
	Model     string
	TopN      int
	Metadata  map[string]any
}

type RerankResponse struct {
	Provider string
	Model    string
	Results  []RerankResult
	Usage    *Usage
	Raw      []byte
}

type RerankResult struct {
	Index int
	Score float64
	Text  string
}

type ExtractionRequest struct {
	Input        string
	Instructions string
	Model        string
	SchemaName   string
	Schema       JSONSchema
	Temperature  *float64
	MaxTokens    int
	Metadata     map[string]any
}

type ExtractionResponse struct {
	Provider         string
	Model            string
	Value            any
	Raw              string
	Repaired         bool
	ValidationErrors []string
	Usage            *Usage
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type JSONSchema map[string]any

type ProviderConfig struct {
	Name               string
	Kind               ProviderKind
	OpenAICompatible   *OpenAICompatibleConfig
	CustomHTTPProvider *CustomHTTPProviderConfig
}

type OpenAICompatibleConfig struct {
	Name                string
	BaseURL             string
	APIKey              string
	EmbeddingModel      string
	RerankerModel       string
	ChatModel           string
	EmbeddingDimensions int
	Organization        string
	Project             string
	Headers             map[string]string
	Timeout             time.Duration
}

type CustomHTTPProviderConfig struct {
	Name       string
	Headers    map[string]string
	Embeddings *CustomHTTPEndpointConfig
	Extractor  *CustomHTTPEndpointConfig
}

type CustomHTTPEndpointConfig struct {
	URL               string
	Method            string
	Model             string
	Headers           map[string]string
	Timeout           time.Duration
	Auth              *CustomHTTPAuthConfig
	RequestTemplate   map[string]any
	InputPath         string
	ModelPath         string
	SchemaPath        string
	ResponseValuePath string
	ResponseUsagePath string
	Dimensions        int
}

type CustomHTTPAuthConfig struct {
	Type        string
	HeaderName  string
	Token       string
	QueryName   string
	QueryValue  string
	BearerToken string
}

func NewProvider(config ProviderConfig, client *http.Client) (Provider, error) {
	switch config.Kind {
	case ProviderOpenAICompatible:
		if config.OpenAICompatible == nil {
			return nil, fmt.Errorf("%w: openai-compatible config is required", ErrInvalidConfig)
		}
		return NewOpenAICompatibleProvider(*config.OpenAICompatible, client)
	case ProviderCustomHTTP:
		if config.CustomHTTPProvider == nil {
			return nil, fmt.Errorf("%w: custom-http config is required", ErrInvalidConfig)
		}
		return NewCustomHTTPProvider(*config.CustomHTTPProvider, client)
	default:
		return nil, fmt.Errorf("%w: unsupported provider kind %q", ErrInvalidConfig, config.Kind)
	}
}

func NewEmbeddingProvider(config ProviderConfig, client *http.Client) (EmbeddingProvider, error) {
	provider, err := NewProvider(config, client)
	if err != nil {
		return nil, err
	}
	embeddingProvider, ok := provider.(EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("%w: provider %q does not support embeddings", ErrInvalidConfig, provider.Name())
	}
	return embeddingProvider, nil
}

func NewRerankerProvider(config ProviderConfig, client *http.Client) (RerankerProvider, error) {
	provider, err := NewProvider(config, client)
	if err != nil {
		return nil, err
	}
	rerankerProvider, ok := provider.(RerankerProvider)
	if !ok {
		return nil, fmt.Errorf("%w: provider %q does not support reranking", ErrInvalidConfig, provider.Name())
	}
	return rerankerProvider, nil
}

func NewExtractorProvider(config ProviderConfig, client *http.Client) (ExtractorProvider, error) {
	provider, err := NewProvider(config, client)
	if err != nil {
		return nil, err
	}
	extractorProvider, ok := provider.(ExtractorProvider)
	if !ok {
		return nil, fmt.Errorf("%w: provider %q does not support extraction", ErrInvalidConfig, provider.Name())
	}
	return extractorProvider, nil
}

func validateRerankRequest(request RerankRequest) error {
	if strings.TrimSpace(request.Query) == "" {
		return fmt.Errorf("%w: query is required", ErrInvalidRequest)
	}
	if len(request.Documents) == 0 {
		return fmt.Errorf("%w: documents are required", ErrInvalidRequest)
	}
	for index, document := range request.Documents {
		if strings.TrimSpace(document) == "" {
			return fmt.Errorf("%w: documents[%d] is empty", ErrInvalidRequest, index)
		}
	}
	if request.TopN < 0 {
		return fmt.Errorf("%w: top_n must be non-negative", ErrInvalidRequest)
	}
	return nil
}

func ValidateEmbeddingDimensions(vector []float64, expected int) error {
	if len(vector) == 0 {
		return fmt.Errorf("%w: embedding vector is empty", ErrInvalidResponse)
	}
	if expected > 0 && len(vector) != expected {
		return fmt.Errorf("%w: embedding dimension mismatch: got %d, want %d", ErrInvalidResponse, len(vector), expected)
	}
	for index, value := range vector {
		if value != value {
			return fmt.Errorf("%w: embedding contains NaN at index %d", ErrInvalidResponse, index)
		}
	}
	return nil
}

func embeddingDimensions(request EmbeddingRequest, fallback int) int {
	if request.Dimensions > 0 {
		return request.Dimensions
	}
	return fallback
}

func validateEmbeddingRequest(request EmbeddingRequest) error {
	if len(request.Input) == 0 {
		return fmt.Errorf("%w: input is required", ErrInvalidRequest)
	}
	for index, input := range request.Input {
		if input == "" {
			return fmt.Errorf("%w: input[%d] is empty", ErrInvalidRequest, index)
		}
	}
	if request.Dimensions < 0 {
		return fmt.Errorf("%w: dimensions must be non-negative", ErrInvalidRequest)
	}
	return nil
}
