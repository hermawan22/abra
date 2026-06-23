package brain

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/hermawan22/abra/internal/ai"
)

func (s *Service) embedTexts(ctx context.Context, inputs []string) (ai.EmbeddingResponse, error) {
	maxEmbeddingBatchItems := s.cfg.EmbeddingBatchMaxItems
	if maxEmbeddingBatchItems < 1 {
		maxEmbeddingBatchItems = 16
	}
	maxEmbeddingBatchTokens := s.cfg.EmbeddingBatchMaxTokens
	if maxEmbeddingBatchTokens < 1 {
		maxEmbeddingBatchTokens = 6000
	}
	out := ai.EmbeddingResponse{}
	if len(inputs) == 0 {
		return out, nil
	}
	for start := 0; start < len(inputs); {
		end := start
		batchTokens := 0
		for end < len(inputs) {
			estimate := max(1, tokenEstimate(inputs[end]))
			if end > start && (end-start >= maxEmbeddingBatchItems || batchTokens+estimate > maxEmbeddingBatchTokens) {
				break
			}
			batchTokens += estimate
			end++
		}
		response, err := s.embedBatchAdaptive(ctx, inputs, start, end, batchTokens, len(inputs))
		if err != nil {
			return ai.EmbeddingResponse{}, err
		}
		mergeEmbeddingResponse(&out, response)
		start = end
	}
	if len(out.Embeddings) != len(inputs) {
		return ai.EmbeddingResponse{}, &ai.ProviderError{Operation: "embedding", Provider: s.cfg.Embedding.Provider, Model: s.cfg.Embedding.Model, Code: "invalid_response", Retryable: false, Message: fmt.Sprintf("embedding count mismatch after batching: got %d, want %d", len(out.Embeddings), len(inputs)), Err: ai.ErrInvalidResponse}
	}
	sort.SliceStable(out.Embeddings, func(i, j int) bool {
		return out.Embeddings[i].Index < out.Embeddings[j].Index
	})
	return out, nil
}

func (s *Service) embedBatchAdaptive(ctx context.Context, inputs []string, start, end, batchTokens, total int) (ai.EmbeddingResponse, error) {
	response, err := s.embed(ctx, ai.EmbeddingRequest{
		Input:      inputs[start:end],
		Dimensions: s.cfg.Embedding.Dimensions,
		Metadata: map[string]any{
			"batch_start":  start,
			"batch_end":    end,
			"batch_size":   end - start,
			"batch_total":  total,
			"batch_tokens": batchTokens,
		},
	})
	if err == nil {
		for i := range response.Embeddings {
			response.Embeddings[i].Index += start
		}
		return response, nil
	}
	if shouldSplitEmbeddingBatch(err) && end-start > 1 {
		mid := start + (end-start)/2
		left, leftErr := s.embedBatchAdaptive(ctx, inputs, start, mid, estimateEmbeddingTokens(inputs[start:mid]), total)
		if leftErr != nil {
			return ai.EmbeddingResponse{}, leftErr
		}
		right, rightErr := s.embedBatchAdaptive(ctx, inputs, mid, end, estimateEmbeddingTokens(inputs[mid:end]), total)
		if rightErr != nil {
			return ai.EmbeddingResponse{}, rightErr
		}
		mergeEmbeddingResponse(&left, right)
		return left, nil
	}
	return ai.EmbeddingResponse{}, fmt.Errorf("embedding batch %d:%d of %d failed: %w", start, end, total, err)
}

func shouldSplitEmbeddingBatch(err error) bool {
	providerErr, ok := ai.ProviderErrorInfo(err)
	if !ok {
		return errors.Is(err, context.DeadlineExceeded)
	}
	return providerErr.Code == "context_overflow" || providerErr.Code == "provider_timeout"
}

func estimateEmbeddingTokens(inputs []string) int {
	total := 0
	for _, input := range inputs {
		total += max(1, tokenEstimate(input))
	}
	return total
}

func mergeEmbeddingResponse(out *ai.EmbeddingResponse, response ai.EmbeddingResponse) {
	if out.Provider == "" {
		out.Provider = response.Provider
	}
	if out.Model == "" {
		out.Model = response.Model
	}
	if response.Usage != nil {
		if out.Usage == nil {
			out.Usage = &ai.Usage{}
		}
		out.Usage.PromptTokens += response.Usage.PromptTokens
		out.Usage.CompletionTokens += response.Usage.CompletionTokens
		out.Usage.TotalTokens += response.Usage.TotalTokens
	}
	out.Embeddings = append(out.Embeddings, response.Embeddings...)
}
