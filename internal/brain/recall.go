package brain

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/store"
)

func (s *Service) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (store.RecallResult, error) {
	return s.RecallWithOptions(ctx, query, scope, limit, includeUnverified, store.RecallOptions{})
}

func (s *Service) RecallWithOptions(ctx context.Context, query, scope string, limit int, includeUnverified bool, options store.RecallOptions) (store.RecallResult, error) {
	query = strings.TrimSpace(query)
	scope = strings.TrimSpace(scope)
	if query == "" || scope == "" {
		return store.RecallResult{Claims: []store.ClaimResult{}, SupportingDocuments: []store.DocumentResult{}, GraphContext: []store.RelationResult{}, RetrievalMode: "empty"}, nil
	}
	finalLimit := normalizedRecallLimit(limit)
	queryEmbedding, ok, err := s.recallQueryEmbedding(ctx, query)
	if err != nil {
		result, fallbackErr := s.db.RecallWithOptions(ctx, query, scope, limit, includeUnverified, options)
		if fallbackErr != nil {
			return store.RecallResult{}, fmt.Errorf("embed query: %w; fallback recall: %v", err, fallbackErr)
		}
		result.RetrievalMode = "full_text_embedding_error"
		return result, nil
	}
	if !ok {
		result, fallbackErr := s.db.RecallWithOptions(ctx, query, scope, limit, includeUnverified, options)
		if fallbackErr != nil {
			return store.RecallResult{}, fallbackErr
		}
		result.RetrievalMode = "full_text_empty_embedding"
		return result, nil
	}
	result, err := s.db.RecallHybridWithOptions(ctx, query, scope, recallCandidateLimit(finalLimit, s.reranker != nil), includeUnverified, queryEmbedding, options)
	if err != nil {
		return store.RecallResult{}, err
	}
	return s.finalizeRecallResult(ctx, query, result, finalLimit), nil
}

func normalizedRecallLimit(limit int) int {
	if limit < 1 || limit > maxRecallLimit {
		return defaultRecallLimit
	}
	return limit
}

func recallCandidateLimit(limit int, rerankerConfigured bool) int {
	limit = normalizedRecallLimit(limit)
	if !rerankerConfigured {
		return limit
	}
	candidateLimit := limit * rerankCandidatePoolMultiplier
	if candidateLimit > maxRecallLimit {
		return maxRecallLimit
	}
	return candidateLimit
}

func (s *Service) finalizeRecallResult(ctx context.Context, query string, result store.RecallResult, limit int) store.RecallResult {
	result = s.rerankRecall(ctx, query, result)
	result = trimRecallResult(result, limit)
	result.RetrievalReasons = store.RecallRetrievalReasons(result)
	if recallResultHasRerank(result) {
		result.RetrievalReasons = append(result.RetrievalReasons, store.RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "rerank",
			Message: "Configured reranker adjusted candidate ordering after hybrid retrieval.",
			Count:   len(result.Claims) + len(result.SupportingDocuments),
		})
	}
	return result
}

func trimRecallResult(result store.RecallResult, limit int) store.RecallResult {
	limit = normalizedRecallLimit(limit)
	if len(result.Claims) > limit {
		result.Claims = result.Claims[:limit]
	}
	documentLimit := min(limit, maxRecallDocumentLimit)
	if len(result.SupportingDocuments) > documentLimit {
		result.SupportingDocuments = result.SupportingDocuments[:documentLimit]
	}
	graphLimit := min(limit, maxRecallGraphLimit)
	if len(result.GraphContext) > graphLimit {
		result.GraphContext = result.GraphContext[:graphLimit]
	}
	return result
}

func recallResultHasRerank(result store.RecallResult) bool {
	for _, claim := range result.Claims {
		if claim.RerankApplied {
			return true
		}
	}
	for _, doc := range result.SupportingDocuments {
		if doc.RerankApplied {
			return true
		}
	}
	return false
}

func (s *Service) recallQueryEmbedding(ctx context.Context, query string) ([]float64, bool, error) {
	key := s.recallQueryEmbeddingCacheKey(query)
	if cached, ok := s.queryEmbeddingCache.get(key); ok {
		return cached, true, nil
	}
	embedding, err := s.embed(ctx, ai.EmbeddingRequest{
		Input:      []string{query},
		Dimensions: s.cfg.Embedding.Dimensions,
	})
	if err != nil {
		return nil, false, err
	}
	if len(embedding.Embeddings) == 0 || len(embedding.Embeddings[0].Vector) == 0 {
		return nil, false, nil
	}
	vector := cloneVector(embedding.Embeddings[0].Vector)
	s.queryEmbeddingCache.set(key, vector)
	return vector, true, nil
}

func (s *Service) recallQueryEmbeddingCacheKey(query string) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(s.cfg.Embedding.Provider)),
		s.cfg.Embedding.BaseURL,
		s.cfg.Embedding.Model,
		fmt.Sprintf("%d", s.cfg.Embedding.Dimensions),
		strings.TrimSpace(query),
	}
	return strings.Join(parts, "\x00")
}

func (s *Service) rerankRecall(ctx context.Context, query string, result store.RecallResult) store.RecallResult {
	ensureRecallBaseRanks(&result)
	if s.reranker == nil || strings.TrimSpace(query) == "" {
		return result
	}
	reranked := false
	claimTexts := make([]string, 0, len(result.Claims))
	for _, claim := range result.Claims {
		claimTexts = append(claimTexts, claim.Claim)
	}
	if len(claimTexts) > 0 {
		if response, err := s.rerank(ctx, ai.RerankRequest{Query: query, Documents: claimTexts, Model: s.cfg.Reranker.Model, TopN: len(claimTexts)}); err == nil {
			if applied := applyClaimRerank(result.Claims, response.Results); applied > 0 {
				reranked = true
				if !strings.Contains(result.RetrievalMode, "reranked") {
					result.RetrievalMode += "_reranked"
				}
			}
		} else {
			result.RetrievalWarnings = append(result.RetrievalWarnings, store.RetrievalWarning{
				Stage:     "retrieval",
				Operation: "rerank_claims",
				Message:   compactBrainError(err),
			})
		}
	}
	docTexts := make([]string, 0, len(result.SupportingDocuments))
	for _, doc := range result.SupportingDocuments {
		docTexts = append(docTexts, doc.Title+"\n"+doc.Content)
	}
	if len(docTexts) > 0 {
		if response, err := s.rerank(ctx, ai.RerankRequest{Query: query, Documents: docTexts, Model: s.cfg.Reranker.Model, TopN: len(docTexts)}); err == nil {
			if applied := applyDocumentRerank(result.SupportingDocuments, response.Results); applied > 0 {
				reranked = true
				if !strings.Contains(result.RetrievalMode, "reranked") {
					result.RetrievalMode += "_reranked"
				}
			}
		} else {
			result.RetrievalWarnings = append(result.RetrievalWarnings, store.RetrievalWarning{
				Stage:     "retrieval",
				Operation: "rerank_documents",
				Message:   compactBrainError(err),
			})
		}
	}
	if reranked {
		result.RetrievalReasons = append(result.RetrievalReasons, store.RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "rerank",
			Message: "Configured reranker adjusted candidate ordering after hybrid retrieval.",
			Count:   len(result.Claims) + len(result.SupportingDocuments),
		})
	}
	return result
}

func ensureRecallBaseRanks(result *store.RecallResult) {
	if result == nil {
		return
	}
	for i := range result.Claims {
		if result.Claims[i].BaseRank == 0 && result.Claims[i].Rank != 0 {
			result.Claims[i].BaseRank = result.Claims[i].Rank
		}
	}
	for i := range result.SupportingDocuments {
		if result.SupportingDocuments[i].BaseRank == 0 && result.SupportingDocuments[i].Rank != 0 {
			result.SupportingDocuments[i].BaseRank = result.SupportingDocuments[i].Rank
		}
	}
}

func applyClaimRerank(claims []store.ClaimResult, results []ai.RerankResult) int {
	applied := 0
	for _, reranked := range results {
		if reranked.Index < 0 || reranked.Index >= len(claims) {
			continue
		}
		score := normalizedRerankScore(reranked.Score)
		if claims[reranked.Index].BaseRank == 0 && claims[reranked.Index].Rank != 0 {
			claims[reranked.Index].BaseRank = claims[reranked.Index].Rank
		}
		claims[reranked.Index].RerankScore = score
		claims[reranked.Index].RerankApplied = true
		claims[reranked.Index].Rank += score * rerankRankBoostWeight
		applied++
	}
	sort.SliceStable(claims, func(i, j int) bool {
		return claims[i].Rank > claims[j].Rank
	})
	return applied
}

func applyDocumentRerank(documents []store.DocumentResult, results []ai.RerankResult) int {
	applied := 0
	for _, reranked := range results {
		if reranked.Index < 0 || reranked.Index >= len(documents) {
			continue
		}
		score := normalizedRerankScore(reranked.Score)
		if documents[reranked.Index].BaseRank == 0 && documents[reranked.Index].Rank != 0 {
			documents[reranked.Index].BaseRank = documents[reranked.Index].Rank
		}
		documents[reranked.Index].RerankScore = score
		documents[reranked.Index].RerankApplied = true
		documents[reranked.Index].Rank += score * rerankRankBoostWeight
		applied++
	}
	sort.SliceStable(documents, func(i, j int) bool {
		return documents[i].Rank > documents[j].Rank
	})
	return applied
}

func normalizedRerankScore(score float64) float64 {
	if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

func compactBrainError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) <= 240 {
		return message
	}
	return message[:240] + "...<truncated>"
}
