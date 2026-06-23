package brain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/observability"
)

func newEmbeddingProvider(cfg config.Config) (ai.EmbeddingProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Embedding.Provider))
	switch provider {
	case "local", "compatible", "openai-compatible", "openai", "qwen3", "local-smart", "tei", "embeddinggemma", "bge-m3", "voyage", "zeroentropy":
		return ai.NewOpenAICompatibleProvider(ai.OpenAICompatibleConfig{
			Name:                provider,
			BaseURL:             cfg.Embedding.BaseURL,
			APIKey:              cfg.Embedding.APIKey,
			EmbeddingModel:      cfg.Embedding.Model,
			EmbeddingDimensions: cfg.Embedding.Dimensions,
			Timeout:             cfg.Embedding.Timeout,
		}, nil)
	default:
		return nil, fmt.Errorf("unsupported embedding provider %q", cfg.Embedding.Provider)
	}
}

func newRerankerProvider(cfg config.Config) (ai.RerankerProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Reranker.Provider))
	if provider == "" || provider == "none" || provider == "off" || provider == "disabled" {
		return nil, nil
	}
	switch provider {
	case "local", "compatible", "openai-compatible", "qwen3", "local-smart", "tei", "voyage", "zeroentropy":
		return ai.NewOpenAICompatibleProvider(ai.OpenAICompatibleConfig{
			Name:          provider,
			BaseURL:       cfg.Reranker.BaseURL,
			APIKey:        cfg.Reranker.APIKey,
			RerankerModel: cfg.Reranker.Model,
			Timeout:       cfg.Reranker.Timeout,
		}, nil)
	default:
		return nil, fmt.Errorf("unsupported reranker provider %q", cfg.Reranker.Provider)
	}
}

func (s *Service) withProviderSlot(ctx context.Context, operation, provider string) (func(), error) {
	if s.providerSlots == nil {
		return func() {}, nil
	}
	started := time.Now()
	observability.AIProviderWaitingStart(operation, provider)
	select {
	case s.providerSlots <- struct{}{}:
		observability.AIProviderWaitingDone(operation, provider, "ok", time.Since(started))
		observability.AIProviderInFlightStart(operation, provider)
		return func() {
			<-s.providerSlots
			observability.AIProviderInFlightDone(operation, provider)
		}, nil
	case <-ctx.Done():
		observability.AIProviderWaitingDone(operation, provider, providerMetricStatus(ctx.Err()), time.Since(started))
		return nil, ctx.Err()
	}
}

func (s *Service) embed(ctx context.Context, request ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	operation := "embedding"
	provider := s.cfg.Embedding.Provider
	release, err := s.withProviderSlot(ctx, operation, provider)
	if err != nil {
		return ai.EmbeddingResponse{}, err
	}
	defer release()
	started := time.Now()
	response, err := s.embeddings.Embed(ctx, request)
	observability.ObserveAIProviderCall(operation, provider, providerMetricStatus(err), time.Since(started))
	return response, err
}

func (s *Service) rerank(ctx context.Context, request ai.RerankRequest) (ai.RerankResponse, error) {
	if s.reranker == nil {
		return ai.RerankResponse{}, fmt.Errorf("reranker is not configured")
	}
	operation := "rerank"
	provider := s.cfg.Reranker.Provider
	release, err := s.withProviderSlot(ctx, operation, provider)
	if err != nil {
		return ai.RerankResponse{}, err
	}
	defer release()
	started := time.Now()
	response, err := s.reranker.Rerank(ctx, request)
	observability.ObserveAIProviderCall(operation, provider, providerMetricStatus(err), time.Since(started))
	return response, err
}

func providerMetricStatus(err error) string {
	if err == nil {
		return "ok"
	}
	if providerErr, ok := ai.ProviderErrorInfo(err); ok {
		return providerErr.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "canceled"
	}
	if errors.Is(err, ai.ErrInvalidResponse) {
		return "invalid_response"
	}
	if errors.Is(err, ai.ErrInvalidRequest) {
		return "invalid_request"
	}
	return "error"
}

func (s *Service) CheckEmbeddingReady(ctx context.Context) error {
	response, err := s.embed(ctx, ai.EmbeddingRequest{
		Input:      []string{"abra readiness check"},
		Dimensions: s.cfg.Embedding.Dimensions,
	})
	if err != nil {
		return err
	}
	if len(response.Embeddings) == 0 {
		return fmt.Errorf("embedding provider returned no embeddings")
	}
	if len(response.Embeddings[0].Vector) == 0 {
		return fmt.Errorf("embedding provider returned an empty vector")
	}
	return nil
}
