package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/graph"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
)

type Service struct {
	cfg                 config.Config
	db                  *store.Store
	embeddings          ai.EmbeddingProvider
	reranker            ai.RerankerProvider
	providerSlots       chan struct{}
	queryEmbeddingCache *embeddingCache
}

const defaultQueryEmbeddingCacheEntries = 1024
const rerankRankBoostWeight = 0.2

type embeddingCache struct {
	mu      sync.Mutex
	max     int
	order   []string
	entries map[string][]float64
}

type IngestDocumentInput struct {
	SourceType      string         `json:"source_type"`
	SourceURL       string         `json:"source_url"`
	SourceID        string         `json:"source_id,omitempty"`
	Title           string         `json:"title"`
	Scope           string         `json:"scope"`
	Content         string         `json:"content"`
	SourceUpdatedAt string         `json:"source_updated_at,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type IngestDocumentResult struct {
	DocumentID          string `json:"document_id"`
	Chunks              int    `json:"chunks"`
	Claims              int    `json:"claims"`
	DeprecatedClaims    int    `json:"deprecated_claims"`
	DeprecatedRelations int    `json:"deprecated_relations"`
	DeletedSummaries    int    `json:"deleted_summaries"`
	Conflicts           int    `json:"conflicts"`
	Entities            int    `json:"entities"`
	Relations           int    `json:"relations"`
	Summaries           int    `json:"summaries"`
}

type RememberClaimInput struct {
	Claim      string         `json:"claim"`
	Scope      string         `json:"scope"`
	SourceURL  string         `json:"source_url,omitempty"`
	SourceType string         `json:"source_type,omitempty"`
	Authority  string         `json:"authority,omitempty"`
	CreatedBy  string         `json:"created_by,omitempty"`
	ApprovalID string         `json:"approval_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type RememberClaimResult struct {
	ClaimID   string `json:"claim_id"`
	Status    string `json:"status"`
	Conflicts int    `json:"conflicts"`
}

type CaptureObservationInput struct {
	Scope           string         `json:"scope"`
	ObservationText string         `json:"observation_text"`
	ObservationType string         `json:"observation_type,omitempty"`
	Status          string         `json:"status,omitempty"`
	Authority       string         `json:"authority,omitempty"`
	AuthorityScore  float64        `json:"authority_score,omitempty"`
	Confidence      float64        `json:"confidence,omitempty"`
	FreshnessStatus string         `json:"freshness_status,omitempty"`
	SubjectEntityID string         `json:"subject_entity_id,omitempty"`
	ObjectEntityID  string         `json:"object_entity_id,omitempty"`
	RelationID      string         `json:"relation_id,omitempty"`
	ClaimID         string         `json:"claim_id,omitempty"`
	DocumentID      string         `json:"document_id,omitempty"`
	ChunkID         string         `json:"chunk_id,omitempty"`
	SourceConfigID  string         `json:"source_config_id,omitempty"`
	IngestionJobID  string         `json:"ingestion_job_id,omitempty"`
	SourceURL       string         `json:"source_url,omitempty"`
	SourceType      string         `json:"source_type,omitempty"`
	SourceID        string         `json:"source_id,omitempty"`
	ObservedAt      string         `json:"observed_at,omitempty"`
	ValidFrom       string         `json:"valid_from,omitempty"`
	ExpiresAt       string         `json:"expires_at,omitempty"`
	CreatedBy       string         `json:"created_by,omitempty"`
	ApprovalID      string         `json:"approval_id,omitempty"`
	Value           map[string]any `json:"value,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type CaptureObservationResult struct {
	Observation store.ObservationResult `json:"observation"`
}

type ListObservationsInput struct {
	Scope           string `json:"scope"`
	Query           string `json:"query,omitempty"`
	ObservationType string `json:"observation_type,omitempty"`
	Status          string `json:"status,omitempty"`
	Since           string `json:"since,omitempty"`
	Until           string `json:"until,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type ChallengeClaimInput struct {
	ClaimID            string         `json:"claim_id"`
	Reason             string         `json:"reason"`
	SourceURL          string         `json:"source_url,omitempty"`
	CreatedBy          string         `json:"created_by,omitempty"`
	Verdict            string         `json:"verdict,omitempty"`
	ConflictingClaimID string         `json:"conflicting_claim_id,omitempty"`
	Severity           string         `json:"severity,omitempty"`
	ApprovalID         string         `json:"approval_id,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
}

type ChallengeClaimResult struct {
	FeedbackID string `json:"feedback_id"`
	ConflictID string `json:"conflict_id,omitempty"`
}

type ForgetClaimInput struct {
	ClaimID    string `json:"claim_id"`
	Reason     string `json:"reason,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
}

type ForgetClaimResult struct {
	ClaimID   string `json:"claim_id"`
	Forgotten bool   `json:"forgotten"`
}

type RebuildSummariesInput struct {
	Scope      string `json:"scope"`
	Limit      int    `json:"limit,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
}

type RebuildSummariesResult struct {
	Scope     string `json:"scope"`
	Documents int    `json:"documents"`
	Summaries int    `json:"summaries"`
}

func New(cfg config.Config, db *store.Store) (*Service, error) {
	embeddingProvider, err := newEmbeddingProvider(cfg)
	if err != nil {
		return nil, err
	}
	rerankerProvider, err := newRerankerProvider(cfg)
	if err != nil {
		return nil, err
	}
	providerConcurrency := cfg.AIProviderConcurrency
	if providerConcurrency < 1 {
		providerConcurrency = 1
	}
	return &Service{
		cfg:                 cfg,
		db:                  db,
		embeddings:          embeddingProvider,
		reranker:            rerankerProvider,
		providerSlots:       make(chan struct{}, providerConcurrency),
		queryEmbeddingCache: newEmbeddingCache(defaultQueryEmbeddingCacheEntries),
	}, nil
}

func newEmbeddingCache(max int) *embeddingCache {
	if max < 1 {
		max = defaultQueryEmbeddingCacheEntries
	}
	return &embeddingCache{max: max, entries: map[string][]float64{}}
}

func (c *embeddingCache) get(key string) ([]float64, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	return cloneVector(value), true
}

func (c *embeddingCache) set(key string, value []float64) {
	if c == nil || key == "" || len(value) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = cloneVector(value)
	for len(c.entries) > c.max && len(c.order) > 0 {
		evict := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, evict)
	}
}

func cloneVector(value []float64) []float64 {
	if len(value) == 0 {
		return nil
	}
	out := make([]float64, len(value))
	copy(out, value)
	return out
}

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

func (s *Service) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (store.RecallResult, error) {
	query = strings.TrimSpace(query)
	scope = strings.TrimSpace(scope)
	if query == "" || scope == "" {
		return store.RecallResult{Claims: []store.ClaimResult{}, SupportingDocuments: []store.DocumentResult{}, GraphContext: []store.RelationResult{}, RetrievalMode: "empty"}, nil
	}
	queryEmbedding, ok, err := s.recallQueryEmbedding(ctx, query)
	if err != nil {
		result, fallbackErr := s.db.Recall(ctx, query, scope, limit, includeUnverified)
		if fallbackErr != nil {
			return store.RecallResult{}, fmt.Errorf("embed query: %w; fallback recall: %v", err, fallbackErr)
		}
		result.RetrievalMode = "full_text_embedding_error"
		return result, nil
	}
	if !ok {
		result, fallbackErr := s.db.Recall(ctx, query, scope, limit, includeUnverified)
		if fallbackErr != nil {
			return store.RecallResult{}, fallbackErr
		}
		result.RetrievalMode = "full_text_empty_embedding"
		return result, nil
	}
	result, err := s.db.RecallHybrid(ctx, query, scope, limit, includeUnverified, queryEmbedding)
	if err != nil {
		return store.RecallResult{}, err
	}
	return s.rerankRecall(ctx, query, result), nil
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

type preparedIngestDocument struct {
	input               IngestDocumentInput
	content             string
	metadata            map[string]any
	sourceConfigID      string
	ingestionJobID      string
	authority           string
	authorityScore      float64
	chunks              []string
	chunkEmbeddings     []ai.Embedding
	chunkEmbeddingModel string
	claims              []string
	claimEmbeddings     []ai.Embedding
	claimEmbeddingModel string
	codePath            string
}

func (s *Service) IngestDocument(ctx context.Context, input IngestDocumentInput) (IngestDocumentResult, error) {
	doc, err := s.prepareIngestDocument(input)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	docs, err := s.embedPreparedDocuments(ctx, []preparedIngestDocument{doc})
	if err != nil {
		return IngestDocumentResult{}, err
	}
	var result IngestDocumentResult
	err = s.db.WithTx(ctx, func(txStore *store.Store) error {
		txService := *s
		txService.db = txStore
		if err := lockPreparedIngestSources(ctx, txStore, docs); err != nil {
			return err
		}
		persisted, err := txService.persistPreparedIngestDocument(ctx, docs[0])
		if err != nil {
			return err
		}
		result = persisted
		return nil
	})
	return result, err
}

func (s *Service) IngestDocuments(ctx context.Context, inputs []IngestDocumentInput) ([]IngestDocumentResult, error) {
	if len(inputs) == 0 {
		return []IngestDocumentResult{}, nil
	}
	prepared := make([]preparedIngestDocument, 0, len(inputs))
	for index, input := range inputs {
		doc, err := s.prepareIngestDocument(input)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", index, err)
		}
		prepared = append(prepared, doc)
	}
	prepared, err := s.embedPreparedDocuments(ctx, prepared)
	if err != nil {
		return nil, err
	}
	results := make([]IngestDocumentResult, 0, len(prepared))
	err = s.db.WithTx(ctx, func(txStore *store.Store) error {
		txService := *s
		txService.db = txStore
		if err := lockPreparedIngestSources(ctx, txStore, prepared); err != nil {
			return err
		}
		for index, doc := range prepared {
			result, err := txService.persistPreparedIngestDocument(ctx, doc)
			if err != nil {
				return fmt.Errorf("document %d: %w", index, err)
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Service) prepareIngestDocument(input IngestDocumentInput) (preparedIngestDocument, error) {
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.Title = strings.TrimSpace(input.Title)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.SourceType == "" || input.SourceURL == "" || input.Title == "" || input.Scope == "" || strings.TrimSpace(input.Content) == "" {
		return preparedIngestDocument{}, fmt.Errorf("source_type, source_url, title, scope, and content are required")
	}

	content := input.Content
	if s.cfg.RedactPII {
		content = redact(content)
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{"ingest_complete": false})
	sourceConfigID := metadataString(input.Metadata, "source_config_id")
	ingestionJobID := metadataString(input.Metadata, "ingestion_job_id")
	authority := metadataString(input.Metadata, "authority")
	if authority == "" {
		authority = "official-doc"
	}
	authorityScore := metadataFloat(input.Metadata, "authority_score")
	if authorityScore == 0 {
		authorityScore = 0.75
	}
	input.Content = content
	return preparedIngestDocument{
		input:          input,
		content:        content,
		metadata:       metadata,
		sourceConfigID: sourceConfigID,
		ingestionJobID: ingestionJobID,
		authority:      authority,
		authorityScore: authorityScore,
		chunks:         chunkText(content, 1200),
		claims:         extractClaimsForDocument(input, content),
		codePath:       codeGraphPath(input),
	}, nil
}

func (s *Service) embedPreparedDocuments(ctx context.Context, docs []preparedIngestDocument) ([]preparedIngestDocument, error) {
	chunkTexts := []string{}
	chunkRefs := []struct{ doc, index int }{}
	for docIndex, doc := range docs {
		docs[docIndex].chunkEmbeddings = make([]ai.Embedding, len(doc.chunks))
		for chunkIndex, chunk := range doc.chunks {
			chunkRefs = append(chunkRefs, struct{ doc, index int }{doc: docIndex, index: chunkIndex})
			chunkTexts = append(chunkTexts, chunk)
		}
	}
	if len(chunkTexts) > 0 {
		response, err := s.embedTexts(ctx, chunkTexts)
		if err != nil {
			return nil, err
		}
		for globalIndex, ref := range chunkRefs {
			docs[ref.doc].chunkEmbeddings[ref.index] = response.Embeddings[globalIndex]
			docs[ref.doc].chunkEmbeddingModel = response.Model
		}
	}

	claimTexts := []string{}
	claimRefs := []struct{ doc, index int }{}
	for docIndex, doc := range docs {
		docs[docIndex].claimEmbeddings = make([]ai.Embedding, len(doc.claims))
		for claimIndex, claim := range doc.claims {
			claimRefs = append(claimRefs, struct{ doc, index int }{doc: docIndex, index: claimIndex})
			claimTexts = append(claimTexts, claim)
		}
	}
	if len(claimTexts) > 0 {
		response, err := s.embedTexts(ctx, claimTexts)
		if err != nil {
			return nil, err
		}
		for globalIndex, ref := range claimRefs {
			docs[ref.doc].claimEmbeddings[ref.index] = response.Embeddings[globalIndex]
			docs[ref.doc].claimEmbeddingModel = response.Model
		}
	}
	return docs, nil
}

type ingestSourceLock struct {
	scope     string
	sourceURL string
}

func preparedIngestSourceLocks(docs []preparedIngestDocument) []ingestSourceLock {
	seen := map[string]ingestSourceLock{}
	for _, doc := range docs {
		lock := ingestSourceLock{
			scope:     strings.TrimSpace(doc.input.Scope),
			sourceURL: strings.TrimSpace(doc.input.SourceURL),
		}
		if lock.scope == "" || lock.sourceURL == "" {
			continue
		}
		key := lock.scope + "\x00" + lock.sourceURL
		seen[key] = lock
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	locks := make([]ingestSourceLock, 0, len(keys))
	for _, key := range keys {
		locks = append(locks, seen[key])
	}
	return locks
}

func lockPreparedIngestSources(ctx context.Context, db *store.Store, docs []preparedIngestDocument) error {
	for _, lock := range preparedIngestSourceLocks(docs) {
		if err := db.LockSourceIngest(ctx, lock.scope, lock.sourceURL); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistPreparedIngestDocument(ctx context.Context, doc preparedIngestDocument) (IngestDocumentResult, error) {
	input := doc.input
	content := doc.content
	sourceConfigID := doc.sourceConfigID
	ingestionJobID := doc.ingestionJobID
	authority := doc.authority
	authorityScore := doc.authorityScore
	chunks := doc.chunks
	claims := doc.claims
	codePath := doc.codePath

	documentID, err := s.db.UpsertDocument(ctx, store.DocumentRecord{
		SourceType:      input.SourceType,
		SourceURL:       input.SourceURL,
		SourceID:        input.SourceID,
		SourceConfigID:  sourceConfigID,
		IngestionJobID:  ingestionJobID,
		Title:           input.Title,
		Scope:           input.Scope,
		ContentChecksum: checksum(content),
		SourceUpdatedAt: input.SourceUpdatedAt,
		Authority:       authority,
		AuthorityScore:  authorityScore,
		Metadata:        doc.metadata,
	})
	if err != nil {
		return IngestDocumentResult{}, err
	}

	records := make([]store.ChunkRecord, 0, len(chunks))
	for i, chunk := range chunks {
		records = append(records, store.ChunkRecord{
			Content:             chunk,
			Embedding:           doc.chunkEmbeddings[i].Vector,
			EmbeddingProvider:   s.cfg.Embedding.Provider,
			EmbeddingModel:      doc.chunkEmbeddingModel,
			EmbeddingDimensions: doc.chunkEmbeddings[i].Dimensions,
			SourceConfigID:      sourceConfigID,
			IngestionJobID:      ingestionJobID,
			Metadata:            lineageMetadata(sourceConfigID, ingestionJobID),
		})
	}
	if err := s.db.ReplaceChunks(ctx, documentID, input.Scope, records); err != nil {
		return IngestDocumentResult{}, err
	}

	entityCount := 0
	relationCount := 0
	summaryCount := 0
	conflictCount := 0
	deprecatedClaimCount := 0
	deprecatedRelationCount := 0
	deletedSummaryCount := 0
	graphRefreshResult, err := s.db.BeginSourceGraphRefresh(ctx, input.Scope, input.SourceURL, ingestionJobID)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	deprecatedRelationCount = int(graphRefreshResult.DeprecatedRelations)
	deletedSummaryCount = int(graphRefreshResult.DeletedSummaries)
	if codePath != "" && graph.IsCodeGraphPath(codePath) {
		candidates := graph.ExtractCodeFile(graph.CodeFile{
			Path:      codePath,
			Content:   content,
			SourceID:  input.SourceID,
			SourceURL: input.SourceURL,
		})
		entities, relations, err := s.persistGraphCandidates(ctx, graphPersistInput{
			Scope:          input.Scope,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			SourceConfigID: sourceConfigID,
			IngestionJobID: ingestionJobID,
			DocumentID:     documentID,
			Metadata:       lineageMetadata(sourceConfigID, ingestionJobID),
			Description:    "Extracted from code structure: " + codePath,
			Candidates:     candidates,
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		entityCount += entities
		relationCount += relations
	}

	refreshResult, err := s.db.BeginSourceClaimRefresh(ctx, input.Scope, input.SourceType, input.SourceURL, ingestionJobID)
	if err != nil {
		return IngestDocumentResult{}, err
	}
	deprecatedClaimCount = int(refreshResult.Deprecated)
	for i, claim := range claims {
		claimID, err := s.db.InsertClaim(ctx, store.ClaimRecord{
			ClaimText:           claim,
			Scope:               input.Scope,
			SourceURL:           input.SourceURL,
			SourceType:          input.SourceType,
			Authority:           authority,
			Status:              "verified",
			Confidence:          authorityScore,
			Embedding:           doc.claimEmbeddings[i].Vector,
			EmbeddingProvider:   s.cfg.Embedding.Provider,
			EmbeddingModel:      doc.claimEmbeddingModel,
			EmbeddingDimensions: doc.claimEmbeddings[i].Dimensions,
			SourceConfigID:      sourceConfigID,
			IngestionJobID:      ingestionJobID,
			AuthorityScore:      authorityScore,
			Metadata: mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{
				"extracted":       true,
				"document_title":  input.Title,
				"authority_score": authorityScore,
			}),
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		if err := s.db.AddEvidence(ctx, store.EvidenceRecord{
			ClaimID:    claimID,
			DocumentID: documentID,
			Quote:      claim,
			SourceURL:  input.SourceURL,
			SourceType: input.SourceType,
		}); err != nil {
			return IngestDocumentResult{}, err
		}
		conflicts, err := s.detectClaimConflicts(ctx, claimID, claim, input.Scope, input.SourceURL, mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{
			"document_id": documentID,
			"source_type": input.SourceType,
		}))
		if err != nil {
			return IngestDocumentResult{}, err
		}
		conflictCount += conflicts
		entities, relations, err := s.persistGraphCandidates(ctx, graphPersistInput{
			Scope:          input.Scope,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			SourceConfigID: sourceConfigID,
			IngestionJobID: ingestionJobID,
			DocumentID:     documentID,
			ClaimID:        claimID,
			Metadata:       mergeMetadata(lineageMetadata(sourceConfigID, ingestionJobID), map[string]any{"claim_id": claimID, "claim_text": claim}),
			Description:    "Extracted from claim: " + claim,
			Candidates:     graph.ExtractFromClaims([]string{claim}),
		})
		if err != nil {
			return IngestDocumentResult{}, err
		}
		entityCount += entities
		relationCount += relations
	}
	summaries, err := s.upsertMemorySummaries(ctx, summaryInput{
		DocumentID: documentID,
		Input:      input,
		Content:    content,
		CodePath:   codePath,
		Metadata:   lineageMetadata(sourceConfigID, ingestionJobID),
	})
	if err != nil {
		return IngestDocumentResult{}, err
	}
	summaryCount += summaries
	if err := s.db.InsertAuditEvent(ctx, "document.ingested", "document", documentID, input.Scope, input.SourceURL, map[string]any{"chunks": len(chunks), "claims": len(claims), "deprecated_claims": deprecatedClaimCount, "deprecated_relations": deprecatedRelationCount, "deleted_summaries": deletedSummaryCount, "conflicts": conflictCount, "entities": entityCount, "relations": relationCount, "summaries": summaryCount}); err != nil {
		return IngestDocumentResult{}, err
	}
	if err := s.db.MarkDocumentIngestComplete(ctx, documentID); err != nil {
		return IngestDocumentResult{}, err
	}

	return IngestDocumentResult{DocumentID: documentID, Chunks: len(chunks), Claims: len(claims), DeprecatedClaims: deprecatedClaimCount, DeprecatedRelations: deprecatedRelationCount, DeletedSummaries: deletedSummaryCount, Conflicts: conflictCount, Entities: entityCount, Relations: relationCount, Summaries: summaryCount}, nil
}

func (s *Service) RememberClaim(ctx context.Context, input RememberClaimInput) (RememberClaimResult, error) {
	input.Claim = strings.TrimSpace(input.Claim)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Claim == "" || input.Scope == "" {
		return RememberClaimResult{}, fmt.Errorf("claim and scope are required")
	}
	claimText := input.Claim
	if s.cfg.RedactPII {
		claimText = redact(claimText)
	}
	status := "unverified"
	confidence := 0.25
	if strings.TrimSpace(input.SourceURL) != "" {
		status = "verified"
		confidence = 0.65
	}
	authority := input.Authority
	if authority == "" {
		authority = "manual-unverified"
	}
	embedding, err := s.embed(ctx, ai.EmbeddingRequest{Input: []string{claimText}, Dimensions: s.cfg.Embedding.Dimensions})
	if err != nil {
		return RememberClaimResult{}, err
	}
	claimID, err := s.db.InsertClaim(ctx, store.ClaimRecord{
		ClaimText:           claimText,
		Scope:               input.Scope,
		SourceURL:           input.SourceURL,
		SourceType:          input.SourceType,
		Authority:           authority,
		Status:              status,
		Confidence:          confidence,
		Embedding:           embedding.Embeddings[0].Vector,
		EmbeddingProvider:   s.cfg.Embedding.Provider,
		EmbeddingModel:      embedding.Model,
		EmbeddingDimensions: embedding.Embeddings[0].Dimensions,
		Metadata:            input.Metadata,
	})
	if err != nil {
		return RememberClaimResult{}, err
	}
	if input.SourceURL != "" {
		if err := s.db.AddEvidence(ctx, store.EvidenceRecord{ClaimID: claimID, Quote: claimText, SourceURL: input.SourceURL, SourceType: input.SourceType}); err != nil {
			return RememberClaimResult{}, err
		}
	}
	conflicts, err := s.detectClaimConflicts(ctx, claimID, claimText, input.Scope, input.SourceURL, input.Metadata)
	if err != nil {
		return RememberClaimResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.remembered", "claim", claimID, input.Scope, input.SourceURL, map[string]any{"status": status, "created_by": input.CreatedBy, "conflicts": conflicts})
	return RememberClaimResult{ClaimID: claimID, Status: status, Conflicts: conflicts}, nil
}

func (s *Service) CaptureObservation(ctx context.Context, input CaptureObservationInput) (CaptureObservationResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.ObservationText = strings.TrimSpace(input.ObservationText)
	if input.Scope == "" || input.ObservationText == "" {
		return CaptureObservationResult{}, fmt.Errorf("scope and observation_text are required")
	}
	observationText := input.ObservationText
	if s.cfg.RedactPII {
		observationText = redact(observationText)
	}
	observation, err := s.db.InsertObservation(ctx, store.ObservationRecord{
		Scope:           input.Scope,
		ObservationType: input.ObservationType,
		ObservationText: observationText,
		Status:          input.Status,
		Authority:       input.Authority,
		AuthorityScore:  input.AuthorityScore,
		Confidence:      input.Confidence,
		FreshnessStatus: input.FreshnessStatus,
		SubjectEntityID: input.SubjectEntityID,
		ObjectEntityID:  input.ObjectEntityID,
		RelationID:      input.RelationID,
		ClaimID:         input.ClaimID,
		DocumentID:      input.DocumentID,
		ChunkID:         input.ChunkID,
		SourceConfigID:  input.SourceConfigID,
		IngestionJobID:  input.IngestionJobID,
		SourceURL:       input.SourceURL,
		SourceType:      input.SourceType,
		SourceID:        input.SourceID,
		ObservedAt:      input.ObservedAt,
		ValidFrom:       input.ValidFrom,
		ExpiresAt:       input.ExpiresAt,
		CreatedBy:       input.CreatedBy,
		Value:           input.Value,
		Metadata: mergeMetadata(map[string]any{
			"channel": "api",
		}, input.Metadata),
	})
	if err != nil {
		return CaptureObservationResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "observation.captured", "observation", observation.ID, observation.Scope, observation.SourceURL, map[string]any{
		"observation_type": observation.ObservationType,
		"status":           observation.Status,
		"created_by":       input.CreatedBy,
	})
	return CaptureObservationResult{Observation: observation}, nil
}

func (s *Service) ListObservations(ctx context.Context, input ListObservationsInput) ([]store.ObservationResult, error) {
	return s.db.ListObservations(ctx, store.ObservationFilter{
		Scope:           input.Scope,
		Query:           input.Query,
		ObservationType: input.ObservationType,
		Status:          input.Status,
		Since:           input.Since,
		Until:           input.Until,
		Limit:           input.Limit,
	})
}

func (s *Service) ChallengeClaim(ctx context.Context, input ChallengeClaimInput) (ChallengeClaimResult, error) {
	input.ClaimID = strings.TrimSpace(input.ClaimID)
	if input.ClaimID == "" {
		return ChallengeClaimResult{}, fmt.Errorf("claim_id is required")
	}
	scope, err := s.db.ClaimScope(ctx, input.ClaimID)
	if err != nil {
		return ChallengeClaimResult{}, err
	}
	if input.Verdict == "" {
		input.Verdict = "incorrect"
	}
	feedbackID, err := s.db.InsertFeedback(ctx, store.FeedbackRecord{
		ClaimID:   input.ClaimID,
		Verdict:   input.Verdict,
		Reason:    input.Reason,
		SourceURL: input.SourceURL,
		CreatedBy: input.CreatedBy,
	})
	if err != nil {
		return ChallengeClaimResult{}, err
	}
	conflictID := ""
	if input.Verdict == "conflict" && strings.TrimSpace(input.ConflictingClaimID) != "" {
		conflictID, err = s.db.UpsertClaimConflict(ctx, store.ConflictRecord{
			Scope:              scope,
			ConflictType:       "contradicts",
			Severity:           input.Severity,
			PrimaryClaimID:     input.ClaimID,
			ConflictingClaimID: input.ConflictingClaimID,
			DetectedBy:         input.CreatedBy,
			Authority:          "challenge",
			Metadata: mergeMetadata(input.Metadata, map[string]any{
				"feedback_id": feedbackID,
				"reason":      input.Reason,
				"source_url":  input.SourceURL,
			}),
		})
		if err != nil {
			return ChallengeClaimResult{}, err
		}
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.challenged", "claim", input.ClaimID, scope, input.SourceURL, map[string]any{"verdict": input.Verdict, "reason": input.Reason, "feedback_id": feedbackID, "conflict_id": conflictID, "conflicting_claim_id": input.ConflictingClaimID})
	return ChallengeClaimResult{FeedbackID: feedbackID, ConflictID: conflictID}, nil
}

func (s *Service) embedTexts(ctx context.Context, inputs []string) (ai.EmbeddingResponse, error) {
	const maxEmbeddingBatchTokens = 6000
	const maxEmbeddingBatchItems = 16
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
		response, err := s.embed(ctx, ai.EmbeddingRequest{
			Input:      inputs[start:end],
			Dimensions: s.cfg.Embedding.Dimensions,
			Metadata: map[string]any{
				"batch_start":  start,
				"batch_end":    end,
				"batch_size":   end - start,
				"batch_total":  len(inputs),
				"batch_tokens": batchTokens,
			},
		})
		if err != nil {
			return ai.EmbeddingResponse{}, fmt.Errorf("embedding batch %d:%d of %d failed: %w", start, end, len(inputs), err)
		}
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
		for _, embedding := range response.Embeddings {
			embedding.Index += start
			out.Embeddings = append(out.Embeddings, embedding)
		}
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

func (s *Service) ForgetClaim(ctx context.Context, input ForgetClaimInput) (ForgetClaimResult, error) {
	input.ClaimID = strings.TrimSpace(input.ClaimID)
	if input.ClaimID == "" {
		return ForgetClaimResult{}, fmt.Errorf("claim_id is required")
	}
	scope, err := s.db.ClaimScope(ctx, input.ClaimID)
	if err != nil {
		return ForgetClaimResult{}, err
	}
	forgotten, err := s.db.DeprecateClaim(ctx, input.ClaimID, input.Reason, input.CreatedBy)
	if err != nil {
		return ForgetClaimResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.forgotten", "claim", input.ClaimID, scope, "", map[string]any{"reason": input.Reason, "created_by": input.CreatedBy, "forgotten": forgotten})
	return ForgetClaimResult{ClaimID: input.ClaimID, Forgotten: forgotten}, nil
}

func (s *Service) RebuildSummaries(ctx context.Context, input RebuildSummariesInput) (RebuildSummariesResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		return RebuildSummariesResult{}, fmt.Errorf("scope is required")
	}
	if input.Limit < 1 || input.Limit > 10000 {
		input.Limit = 1000
	}
	docs, err := s.db.ListDocumentsForSummary(ctx, input.Scope, input.Limit)
	if err != nil {
		return RebuildSummariesResult{}, err
	}
	total := 0
	for _, doc := range docs {
		summaries, err := s.upsertMemorySummaries(ctx, summaryInput{
			DocumentID: doc.DocumentID,
			Input: IngestDocumentInput{
				SourceType: doc.SourceType,
				SourceURL:  doc.SourceURL,
				SourceID:   doc.SourceID,
				Title:      doc.Title,
				Scope:      doc.Scope,
				Content:    doc.Content,
				Metadata:   doc.Metadata,
			},
			Content:  doc.Content,
			CodePath: codeGraphPath(IngestDocumentInput{Metadata: doc.Metadata}),
			Metadata: map[string]any{
				"rebuilt":        true,
				"document_id":    doc.DocumentID,
				"source_type":    doc.SourceType,
				"relation_count": doc.Relations,
				"chunk_count":    doc.Chunks,
			},
		})
		if err != nil {
			return RebuildSummariesResult{}, err
		}
		total += summaries
	}
	_ = s.db.InsertAuditEvent(ctx, "memory_summaries.rebuilt", "scope", input.Scope, input.Scope, "", map[string]any{"documents": len(docs), "summaries": total, "limit": input.Limit})
	return RebuildSummariesResult{Scope: input.Scope, Documents: len(docs), Summaries: total}, nil
}

type graphPersistInput struct {
	Scope          string
	SourceURL      string
	SourceType     string
	SourceConfigID string
	IngestionJobID string
	DocumentID     string
	ClaimID        string
	Metadata       map[string]any
	Description    string
	Candidates     graph.CandidateSet
}

func (s *Service) persistGraphCandidates(ctx context.Context, input graphPersistInput) (int, int, error) {
	if len(input.Candidates.Entities) == 0 && len(input.Candidates.Relations) == 0 {
		return 0, 0, nil
	}
	entityIDs := map[string]string{}
	entityCount := 0
	relationCount := 0
	for _, entity := range input.Candidates.Entities {
		entityID, err := s.db.UpsertEntity(ctx, store.EntityRecord{
			Scope:          input.Scope,
			EntityType:     entity.Type,
			Name:           entity.Name,
			Description:    input.Description,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			Confidence:     0.5 + float64(min(entity.Mentions, 5))*0.05,
			SourceConfigID: input.SourceConfigID,
			IngestionJobID: input.IngestionJobID,
			Metadata:       input.Metadata,
		})
		if err != nil {
			return entityCount, relationCount, err
		}
		entityIDs[entity.Name] = entityID
		entityCount++
	}
	for _, relation := range input.Candidates.Relations {
		sourceID := entityIDs[relation.From]
		targetID := entityIDs[relation.To]
		if sourceID == "" || targetID == "" || sourceID == targetID {
			continue
		}
		relationID, err := s.db.UpsertRelation(ctx, store.RelationRecord{
			Scope:          input.Scope,
			RelationType:   relation.Type,
			SourceEntityID: sourceID,
			TargetEntityID: targetID,
			ClaimID:        input.ClaimID,
			SourceURL:      firstNonEmpty(relation.SourceURL, input.SourceURL),
			SourceType:     input.SourceType,
			Confidence:     relation.Confidence,
			SourceConfigID: input.SourceConfigID,
			IngestionJobID: input.IngestionJobID,
			Metadata: mergeMetadata(input.Metadata, map[string]any{
				"document_id": input.DocumentID,
				"evidence":    relation.Evidence,
			}),
		})
		if err != nil {
			return entityCount, relationCount, err
		}
		if err := s.detectGraphRelationConflicts(ctx, graphRelationConflictCandidate{
			ID:             relationID,
			Scope:          input.Scope,
			SourceEntityID: sourceID,
			SourceEntity:   relation.From,
			TargetEntity:   relation.To,
			RelationType:   relation.Type,
			SourceURL:      firstNonEmpty(relation.SourceURL, input.SourceURL),
			DocumentID:     input.DocumentID,
			Metadata:       input.Metadata,
		}); err != nil {
			return entityCount, relationCount, err
		}
		relationCount++
	}
	return entityCount, relationCount, nil
}

type graphRelationConflictCandidate struct {
	ID             string
	Scope          string
	SourceEntityID string
	SourceEntity   string
	TargetEntity   string
	RelationType   string
	SourceURL      string
	DocumentID     string
	Metadata       map[string]any
}

func (s *Service) detectGraphRelationConflicts(ctx context.Context, candidate graphRelationConflictCandidate) error {
	relations, err := s.db.ListActiveRelationsFromEntity(ctx, candidate.Scope, candidate.SourceEntityID, 50)
	if err != nil {
		return err
	}
	for _, existing := range relations {
		if existing.ID == candidate.ID {
			continue
		}
		conflictType, severity, reason, ok := graphRelationConflict(candidate, existing)
		if !ok {
			continue
		}
		_, err := s.db.UpsertRelationConflict(ctx, store.ConflictRecord{
			Scope:                 candidate.Scope,
			ConflictType:          conflictType,
			Severity:              severity,
			PrimaryRelationID:     candidate.ID,
			ConflictingRelationID: existing.ID,
			EntityID:              candidate.SourceEntityID,
			DetectedBy:            "auto-graph-detector",
			Authority:             "deterministic-graph-detector",
			Metadata: mergeMetadata(candidate.Metadata, map[string]any{
				"detector":            "graph_relation_contradiction_v1",
				"reason":              reason,
				"document_id":         candidate.DocumentID,
				"new_relation_type":   candidate.RelationType,
				"new_target":          candidate.TargetEntity,
				"new_source_url":      candidate.SourceURL,
				"existing_relation":   existing.ID,
				"existing_type":       existing.Type,
				"existing_target":     existing.ToEntity,
				"existing_source_url": stringPtrValue(existing.SourceURL),
			}),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

var exclusiveGraphAlternativeGroups = map[string]string{
	"playwright":  "browser_test_runner",
	"cypress":     "browser_test_runner",
	"selenium":    "browser_test_runner",
	"webdriverio": "browser_test_runner",
	"testcafe":    "browser_test_runner",
}

func graphRelationConflict(candidate graphRelationConflictCandidate, existing store.GraphRelationResult) (string, string, string, bool) {
	newType := normalizeGraphConflictTerm(candidate.RelationType)
	oldType := normalizeGraphConflictTerm(existing.Type)
	newTarget := normalizeGraphConflictTerm(candidate.TargetEntity)
	oldTarget := normalizeGraphConflictTerm(existing.ToEntity)
	if newTarget == "" || oldTarget == "" {
		return "", "", "", false
	}
	if newTarget == oldTarget && graphOpposingUsePolicy(newType, oldType) {
		return "contradicts", "high", "opposing use policy for " + candidate.TargetEntity, true
	}
	newGroup := exclusiveGraphAlternativeGroups[newTarget]
	oldGroup := exclusiveGraphAlternativeGroups[oldTarget]
	if newGroup != "" && newGroup == oldGroup && newTarget != oldTarget && graphPreferredUseRelation(newType) && graphPreferredUseRelation(oldType) {
		severity := "medium"
		if newType == "should_use" || oldType == "should_use" {
			severity = "high"
		}
		return "competes_with", severity, "competing " + newGroup + " alternatives", true
	}
	return "", "", "", false
}

func graphOpposingUsePolicy(left, right string) bool {
	return (left == "should_not_use" && graphPositiveUseRelation(right)) || (right == "should_not_use" && graphPositiveUseRelation(left))
}

func graphPositiveUseRelation(value string) bool {
	switch value {
	case "should_use", "uses", "depends_on":
		return true
	default:
		return false
	}
}

func graphPreferredUseRelation(value string) bool {
	switch value {
	case "should_use", "uses":
		return true
	default:
		return false
	}
}

func normalizeGraphConflictTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, " \t\r\n\"'`.,;:()[]{}")
	return strings.Join(strings.Fields(value), " ")
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func codeGraphPath(input IngestDocumentInput) string {
	for _, key := range []string{"git_path", "ingest_path", "path", "source_path", "repo_path"} {
		if value := metadataString(input.Metadata, key); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type summaryInput struct {
	DocumentID     string
	Input          IngestDocumentInput
	Content        string
	CodePath       string
	Metadata       map[string]any
	CodeCandidates graph.CandidateSet
}

func (s *Service) upsertMemorySummaries(ctx context.Context, input summaryInput) (int, error) {
	path := input.CodePath
	if path == "" {
		path = firstNonEmpty(metadataString(input.Input.Metadata, "git_path"), metadataString(input.Input.Metadata, "ingest_path"), input.Input.Title)
	}
	path = normalizeSummaryPath(path)
	if path == "" {
		path = input.Input.SourceURL
	}
	input.CodeCandidates = codeCandidatesForSummary(input, path)
	summaries := []store.MemorySummaryRecord{
		documentSummary(input, path),
		sourceSummary(input),
	}
	if module := moduleSummary(input, path); module.Key != "" {
		summaries = append(summaries, module)
	}
	summaries = append(summaries, codeIntelligenceSummaries(input, path)...)

	count := 0
	for _, summary := range summaries {
		if summary.Key == "" || summary.Summary == "" {
			continue
		}
		if _, err := s.db.UpsertMemorySummary(ctx, summary); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func documentSummary(input summaryInput, path string) store.MemorySummaryRecord {
	contentKind := metadataString(input.Input.Metadata, "content_kind")
	summary := ""
	relationCount := 0
	if contentKind == "code" || (input.CodePath != "" && graph.IsCodeGraphPath(input.CodePath)) {
		relationCount = len(input.CodeCandidates.Relations)
		summary = codeSummary(path, input.CodeCandidates)
	} else {
		summary = textSummary(input.Input.Title, input.Content)
	}
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "file",
		Key:           path,
		Title:         path,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: relationCount,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"source_type":   input.Input.SourceType,
			"content_kind":  contentKind,
			"summary_kind":  "deterministic",
			"summary_level": "file",
		}),
	}
}

func codeCandidatesForSummary(input summaryInput, path string) graph.CandidateSet {
	contentKind := metadataString(input.Input.Metadata, "content_kind")
	if contentKind != "code" && (input.CodePath == "" || !graph.IsCodeGraphPath(input.CodePath)) {
		return graph.CandidateSet{}
	}
	return graph.ExtractCodeFile(graph.CodeFile{
		Path:      path,
		Content:   input.Content,
		SourceID:  input.Input.SourceID,
		SourceURL: input.Input.SourceURL,
	})
}

func codeIntelligenceSummaries(input summaryInput, path string) []store.MemorySummaryRecord {
	if len(input.CodeCandidates.Entities) == 0 && len(input.CodeCandidates.Relations) == 0 {
		return nil
	}
	buckets := codeIntelligenceBuckets(input.CodeCandidates)
	repo := repoSummary(input, path, buckets)
	out := []store.MemorySummaryRecord{repo}
	for _, summary := range entitySummaries(input, path, buckets) {
		out = append(out, summary)
	}
	return out
}

type codeSummaryBuckets struct {
	Routes     []string
	Symbols    []string
	Components []string
	Packages   []string
	Imports    []string
	Exports    []string
}

func codeIntelligenceBuckets(candidates graph.CandidateSet) codeSummaryBuckets {
	buckets := codeSummaryBuckets{}
	for _, entity := range candidates.Entities {
		switch entity.Type {
		case "route":
			buckets.Routes = append(buckets.Routes, entity.Name)
		case "symbol":
			buckets.Symbols = append(buckets.Symbols, entity.Name)
		case "component":
			buckets.Components = append(buckets.Components, entity.Name)
		case "package":
			buckets.Packages = append(buckets.Packages, entity.Name)
		}
	}
	for _, relation := range candidates.Relations {
		switch relation.Type {
		case "imports":
			buckets.Imports = append(buckets.Imports, relation.To)
		case "exports":
			buckets.Exports = append(buckets.Exports, relation.To)
		}
	}
	buckets.Routes = uniqueSortedStrings(buckets.Routes)
	buckets.Symbols = uniqueSortedStrings(buckets.Symbols)
	buckets.Components = uniqueSortedStrings(buckets.Components)
	buckets.Packages = uniqueSortedStrings(buckets.Packages)
	buckets.Imports = uniqueSortedStrings(buckets.Imports)
	buckets.Exports = uniqueSortedStrings(buckets.Exports)
	return buckets
}

func repoSummary(input summaryInput, path string, buckets codeSummaryBuckets) store.MemorySummaryRecord {
	key := repoSummaryKey(input.Input)
	module := moduleKey(path)
	parts := []string{"Repository " + key + " code intelligence includes " + path + "."}
	if module != "" {
		parts = append(parts, "Area "+module+".")
	}
	if len(buckets.Routes) > 0 {
		parts = append(parts, "Routes "+strings.Join(limitStrings(buckets.Routes, 6), ", ")+".")
	}
	if len(buckets.Components) > 0 {
		parts = append(parts, "Components "+strings.Join(limitStrings(buckets.Components, 6), ", ")+".")
	}
	if len(buckets.Exports) > 0 {
		parts = append(parts, "Exports "+strings.Join(limitStrings(buckets.Exports, 8), ", ")+".")
	}
	if len(buckets.Packages) > 0 {
		parts = append(parts, "Packages "+strings.Join(limitStrings(buckets.Packages, 8), ", ")+".")
	}
	summary := strings.Join(parts, " ")
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "repo",
		Key:           key,
		Title:         key,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: len(input.CodeCandidates.Relations),
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"source_type":   input.Input.SourceType,
			"summary_kind":  "deterministic",
			"summary_level": "repo",
			"code_path":     path,
		}),
	}
}

func entitySummaries(input summaryInput, path string, buckets codeSummaryBuckets) []store.MemorySummaryRecord {
	type entityGroup struct {
		level  string
		values []string
		text   string
	}
	groups := []entityGroup{
		{level: "route", values: buckets.Routes, text: "Route"},
		{level: "component", values: buckets.Components, text: "Component"},
		{level: "symbol", values: buckets.Symbols, text: "Symbol"},
		{level: "package", values: buckets.Packages, text: "Package"},
	}
	out := []store.MemorySummaryRecord{}
	for _, group := range groups {
		for _, value := range limitStrings(group.values, 24) {
			summary := group.text + " " + value + " is connected to " + path + "."
			if len(buckets.Imports) > 0 && group.level != "package" {
				summary += " Nearby imports: " + strings.Join(limitStrings(buckets.Imports, 5), ", ") + "."
			}
			if len(buckets.Exports) > 0 && (group.level == "route" || group.level == "component") {
				summary += " Nearby exports: " + strings.Join(limitStrings(buckets.Exports, 5), ", ") + "."
			}
			out = append(out, store.MemorySummaryRecord{
				Scope:         input.Input.Scope,
				Level:         group.level,
				Key:           value,
				Title:         value,
				Summary:       summary,
				SourceCount:   1,
				RelationCount: len(input.CodeCandidates.Relations),
				TokenEstimate: tokenEstimate(summary),
				SourceURLs:    []string{input.Input.SourceURL},
				Metadata: mergeMetadata(input.Metadata, map[string]any{
					"document_id":   input.DocumentID,
					"source_type":   input.Input.SourceType,
					"summary_kind":  "deterministic",
					"summary_level": group.level,
					"code_path":     path,
				}),
			})
		}
	}
	return out
}

func repoSummaryKey(input IngestDocumentInput) string {
	for _, key := range []string{"repo", "repository", "repository_slug", "repo_slug", "project", "source_id"} {
		if value := metadataString(input.Metadata, key); value != "" {
			return normalizeSummaryPath(value)
		}
	}
	if strings.TrimSpace(input.SourceID) != "" {
		return normalizeSummaryPath(input.SourceID)
	}
	if strings.TrimSpace(input.SourceURL) != "" {
		value := strings.TrimSuffix(strings.TrimSpace(input.SourceURL), "/")
		if base := filepath.Base(value); base != "." && base != "/" && base != "" {
			return normalizeSummaryPath(base)
		}
	}
	return "repository"
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func moduleSummary(input summaryInput, path string) store.MemorySummaryRecord {
	module := moduleKey(path)
	if module == "" {
		return store.MemorySummaryRecord{}
	}
	summary := "Module " + module + " includes " + path + ". Latest observed source: " + input.Input.Title + "."
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "module",
		Key:           module,
		Title:         module,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: 0,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"summary_kind":  "deterministic",
			"summary_level": "module",
		}),
	}
}

func sourceSummary(input summaryInput) store.MemorySummaryRecord {
	key := strings.TrimSpace(input.Input.SourceType)
	if key == "" {
		key = "source"
	}
	summary := "Source type " + key + " contributed " + input.Input.Title + " to scope " + input.Input.Scope + "."
	return store.MemorySummaryRecord{
		Scope:         input.Input.Scope,
		Level:         "source",
		Key:           key,
		Title:         key,
		Summary:       summary,
		SourceCount:   1,
		RelationCount: 0,
		TokenEstimate: tokenEstimate(summary),
		SourceURLs:    []string{input.Input.SourceURL},
		Metadata: mergeMetadata(input.Metadata, map[string]any{
			"document_id":   input.DocumentID,
			"summary_kind":  "deterministic",
			"summary_level": "source",
		}),
	}
}

func codeSummary(path string, candidates graph.CandidateSet) string {
	imports := []string{}
	exports := []string{}
	symbols := []string{}
	components := []string{}
	routes := []string{}
	for _, relation := range candidates.Relations {
		switch relation.Type {
		case "defines_symbol":
			symbols = append(symbols, relation.To)
		case "imports":
			imports = append(imports, relation.To)
		case "exports":
			exports = append(exports, relation.To)
		case "defines_component":
			components = append(components, relation.To)
		case "implemented_by":
			routes = append(routes, relation.From)
		}
	}
	parts := []string{"Code file " + path + "."}
	if len(routes) > 0 {
		parts = append(parts, "Implements route "+strings.Join(limitStrings(routes, 5), ", ")+".")
	}
	if len(imports) > 0 {
		parts = append(parts, "Imports "+strings.Join(limitStrings(imports, 8), ", ")+".")
	}
	if len(exports) > 0 {
		parts = append(parts, "Exports "+strings.Join(limitStrings(exports, 8), ", ")+".")
	}
	if len(symbols) > 0 {
		parts = append(parts, "Defines symbols "+strings.Join(limitStrings(uniqueSortedStrings(symbols), 8), ", ")+".")
	}
	if len(components) > 0 {
		parts = append(parts, "Defines component "+strings.Join(limitStrings(components, 5), ", ")+".")
	}
	return strings.Join(parts, " ")
}

func textSummary(title, content string) string {
	content = cleanClaim(content)
	if len(content) > 360 {
		content = strings.TrimSpace(content[:360]) + "..."
	}
	if title == "" {
		return content
	}
	return title + ": " + content
}

func moduleKey(path string) string {
	path = normalizeSummaryPath(path)
	if path == "" || !strings.Contains(path, "/") {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) >= 3 && parts[0] == "src" {
		return strings.Join(parts[:2], "/")
	}
	return parts[0]
}

func normalizeSummaryPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	return strings.Trim(path, "/")
}

func tokenEstimate(value string) int {
	words := len(strings.Fields(value))
	runes := utf8.RuneCountInString(value)
	charEstimate := (runes + 3) / 4
	if words == 0 {
		return charEstimate
	}
	wordEstimate := (words * 4) / 3
	return max(1, max(wordEstimate, charEstimate))
}

func limitStrings(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func checksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func chunkText(content string, maxChars int) []string {
	if maxChars < 1 {
		maxChars = 1200
	}
	parts := regexp.MustCompile(`\n{2,}`).Split(content, -1)
	var chunks []string
	current := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, piece := range splitOversizedPart(part, maxChars, minInt(120, maxChars/5)) {
			next := strings.TrimSpace(current + "\n\n" + piece)
			if len(next) > maxChars && current != "" {
				chunks = append(chunks, strings.TrimSpace(current))
				current = piece
				continue
			}
			current = next
		}
	}
	if strings.TrimSpace(current) != "" {
		chunks = append(chunks, strings.TrimSpace(current))
	}
	return chunks
}

func splitOversizedPart(part string, maxChars, overlap int) []string {
	part = strings.TrimSpace(part)
	if part == "" {
		return nil
	}
	if len(part) <= maxChars {
		return []string{part}
	}
	lines := strings.Split(part, "\n")
	if len(lines) > 1 {
		pieces := []string{}
		current := ""
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if len(line) > maxChars {
				if current != "" {
					pieces = append(pieces, current)
					current = ""
				}
				pieces = append(pieces, hardSplitText(line, maxChars, overlap)...)
				continue
			}
			next := strings.TrimSpace(current + "\n" + line)
			if len(next) > maxChars && current != "" {
				pieces = append(pieces, current)
				current = line
				continue
			}
			current = next
		}
		if current != "" {
			pieces = append(pieces, current)
		}
		return pieces
	}
	return hardSplitText(part, maxChars, overlap)
}

func hardSplitText(value string, maxChars, overlap int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if maxChars < 1 {
		maxChars = 1200
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 5
	}
	pieces := []string{}
	for start := 0; start < len(value); {
		end := utf8BoundaryAtOrBefore(value, minInt(start+maxChars, len(value)), start)
		if end <= start {
			end = minInt(start+maxChars, len(value))
		}
		piece := strings.TrimSpace(value[start:end])
		if piece != "" {
			pieces = append(pieces, piece)
		}
		if end >= len(value) {
			break
		}
		nextStart := end - overlap
		if nextStart <= start {
			nextStart = end
		}
		start = utf8BoundaryAtOrBefore(value, nextStart, 0)
		if start < 0 || start >= end {
			start = end
		}
	}
	return pieces
}

func utf8BoundaryAtOrBefore(value string, index, minIndex int) int {
	if index >= len(value) {
		return len(value)
	}
	if index < minIndex {
		return minIndex
	}
	for index > minIndex && !utf8.RuneStart(value[index]) {
		index--
	}
	return index
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func extractClaimsForDocument(input IngestDocumentInput, content string) []string {
	contentKind := metadataString(input.Metadata, "content_kind")
	codePath := codeGraphPath(input)
	if contentKind == "code" || (codePath != "" && graph.IsCodeGraphPath(codePath)) {
		return nil
	}
	return extractClaims(stripFencedCodeBlocks(content))
}

func extractClaims(content string) []string {
	candidates := map[string]struct{}{}
	proseLines := []string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			claim := cleanClaim(line[2:])
			if isExtractableClaim(claim) {
				candidates[claim] = struct{}{}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if line != "" {
			proseLines = append(proseLines, line)
		}
	}
	sentences := regexp.MustCompile(`(?m)([A-Z][^.!?]{39,260}[.!?])`).FindAllString(strings.Join(proseLines, "\n"), -1)
	keywords := regexp.MustCompile(`(?i)\b(should|must|required|default|standard|use|uses|prefer|avoid|deprecated|supersedes|replaces|duplicates|derives)\b`)
	for _, sentence := range sentences {
		claim := cleanClaim(sentence)
		if keywords.MatchString(claim) && isExtractableClaim(claim) {
			candidates[claim] = struct{}{}
		}
	}
	claims := make([]string, 0, len(candidates))
	for claim := range candidates {
		claims = append(claims, claim)
	}
	sort.Strings(claims)
	if len(claims) > 25 {
		claims = claims[:25]
	}
	return claims
}

func cleanClaim(value string) string {
	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(value, " "))
}

func stripFencedCodeBlocks(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isExtractableClaim(claim string) bool {
	claim = strings.TrimSpace(claim)
	if len(claim) < 20 || len(claim) > 260 {
		return false
	}
	if looksLikeCodeClaim(claim) {
		return false
	}
	return true
}

func looksLikeCodeClaim(claim string) bool {
	lower := strings.ToLower(strings.TrimSpace(claim))
	codePrefixes := []string{
		"case ",
		"const ",
		"else ",
		"export ",
		"for ",
		"func ",
		"function ",
		"if ",
		"import ",
		"insert ",
		"let ",
		"return ",
		"select ",
		"switch ",
		"type ",
		"update ",
		"var ",
		"where ",
	}
	for _, prefix := range codePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.Contains(claim, " := ") || strings.Contains(claim, " => ") || strings.Contains(claim, "($") || strings.Contains(claim, "`)") {
		return true
	}
	if strings.Count(claim, "{")+strings.Count(claim, "}") >= 2 {
		return true
	}
	if strings.Count(claim, ";") >= 2 {
		return true
	}
	return false
}

var (
	emailRE          = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	phoneRE          = regexp.MustCompile(`(?m)(^|[^\d])((?:\+?62|0)8\d{7,12})([^\d]|$)`)
	longIDRE         = regexp.MustCompile(`(^|[^\d])(\d{12,20})([^\d]|$)`)
	credentialNameRE = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(?:PASSWORD|PASS|TOKEN|SECRET|API_KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIAL|USERNAME|_USER|_KEYS)[A-Z0-9_]*\b`)
	secretContextRE  = regexp.MustCompile(`(?i)\b(?:request|rotate|rotated|stored|store|fetch|set|export|configure|vault|workspace variable|workspace variables|credential|credentials|password|secret|api key)[^\n.]{0,180}`)
)

func redact(input string) string {
	input = emailRE.ReplaceAllString(input, "[REDACTED_EMAIL]")
	input = phoneRE.ReplaceAllString(input, "${1}[REDACTED_PHONE]${3}")
	input = longIDRE.ReplaceAllString(input, "${1}[REDACTED_ID]${3}")
	input = credentialNameRE.ReplaceAllString(input, "[REDACTED_SECRET_NAME]")
	input = secretContextRE.ReplaceAllStringFunc(input, func(match string) string {
		if credentialNameRE.MatchString(match) || strings.Contains(strings.ToLower(match), "password") || strings.Contains(strings.ToLower(match), "secret") || strings.Contains(strings.ToLower(match), "token") || strings.Contains(strings.ToLower(match), "credential") {
			return "[REDACTED_SECRET_CONTEXT]"
		}
		return match
	})
	return input
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func metadataFloat(metadata map[string]any, key string) float64 {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	default:
		return 0
	}
}

func lineageMetadata(sourceConfigID, ingestionJobID string) map[string]any {
	metadata := map[string]any{}
	if strings.TrimSpace(sourceConfigID) != "" {
		metadata["source_config_id"] = strings.TrimSpace(sourceConfigID)
	}
	if strings.TrimSpace(ingestionJobID) != "" {
		metadata["ingestion_job_id"] = strings.TrimSpace(ingestionJobID)
	}
	return metadata
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range extra {
		base[key] = value
	}
	return base
}

type graphEntity struct {
	Name string
	Type string
}

type graphRelation struct {
	Source string
	Target string
	Type   string
}

func extractGraph(claim string) ([]graphEntity, []graphRelation) {
	entitiesByName := map[string]graphEntity{}
	addEntity := func(name string) {
		name = strings.Trim(strings.TrimSpace(name), "`.,:;()[]{}")
		if len(name) < 2 || len(name) > 80 {
			return
		}
		entitiesByName[name] = graphEntity{Name: name, Type: inferEntityType(name)}
	}

	backticked := regexp.MustCompile("`([^`]{2,80})`").FindAllStringSubmatch(claim, -1)
	for _, match := range backticked {
		addEntity(match[1])
	}
	capitalized := regexp.MustCompile(`\b([A-Z][A-Za-z0-9_-]*(?:\s+[A-Z][A-Za-z0-9_-]*){0,3})\b`).FindAllStringSubmatch(claim, -1)
	for _, match := range capitalized {
		if isStopEntity(match[1]) {
			continue
		}
		addEntity(match[1])
	}

	var relations []graphRelation
	patterns := []struct {
		re *regexp.Regexp
		ty string
	}{
		{regexp.MustCompile(`(?i)\b([A-Za-z0-9_` + "`" + ` .-]{2,80})\s+(?:should\s+use|must\s+use|uses|use)\s+([A-Za-z0-9_` + "`" + ` .-]{2,80})\b`), "uses"},
		{regexp.MustCompile(`(?i)\b([A-Za-z0-9_` + "`" + ` .-]{2,80})\s+(?:depends\s+on|requires)\s+([A-Za-z0-9_` + "`" + ` .-]{2,80})\b`), "depends_on"},
		{regexp.MustCompile(`(?i)\b([A-Za-z0-9_` + "`" + ` .-]{2,80})\s+(?:owns|owned\s+by)\s+([A-Za-z0-9_` + "`" + ` .-]{2,80})\b`), "owns"},
	}
	for _, pattern := range patterns {
		for _, match := range pattern.re.FindAllStringSubmatch(claim, -1) {
			source := cleanEntityPhrase(match[1])
			target := cleanEntityPhrase(match[2])
			if source == "" || target == "" || strings.EqualFold(source, target) {
				continue
			}
			addEntity(source)
			addEntity(target)
			relations = append(relations, graphRelation{Source: source, Target: target, Type: pattern.ty})
		}
	}

	entities := make([]graphEntity, 0, len(entitiesByName))
	for _, entity := range entitiesByName {
		entities = append(entities, entity)
	}
	return entities, relations
}

func cleanEntityPhrase(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "`.,:;()[]{}")
	value = regexp.MustCompile(`(?i)\b(apps?|services?|teams?|the|a|an|before|for|critical|user|journeys|release)\b`).ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	return strings.Trim(strings.TrimSpace(value), "`.,:;()[]{}")
}

func inferEntityType(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "team"):
		return "team"
	case strings.Contains(lower, "service") || strings.Contains(lower, "api"):
		return "service"
	case strings.Contains(lower, "ticket") || strings.HasPrefix(lower, "jira"):
		return "ticket"
	case strings.Contains(lower, "playwright") || strings.Contains(lower, "react") || strings.Contains(lower, "postgres"):
		return "technology"
	default:
		return "concept"
	}
}

func isStopEntity(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "frontend", "backend":
		return false
	case "owner", "source", "scope", "claim":
		return true
	default:
		return false
	}
}
