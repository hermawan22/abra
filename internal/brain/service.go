package brain

import (
	"regexp"
	"sync"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/config"
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
const (
	rerankRankBoostWeight         = 0.2
	defaultRecallLimit            = 5
	maxRecallLimit                = 20
	maxRecallDocumentLimit        = 5
	maxRecallGraphLimit           = 8
	rerankCandidatePoolMultiplier = 3
)

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
	Authority       string         `json:"authority,omitempty"`
	AuthorityScore  float64        `json:"authority_score,omitempty"`
	ApprovalID      string         `json:"approval_id,omitempty"`
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
	Claim             string         `json:"claim"`
	Scope             string         `json:"scope"`
	SourceURL         string         `json:"source_url,omitempty"`
	SourceType        string         `json:"source_type,omitempty"`
	Authority         string         `json:"authority,omitempty"`
	ValidFrom         string         `json:"valid_from,omitempty"`
	ExpiresAt         string         `json:"expires_at,omitempty"`
	SupersedesClaimID string         `json:"supersedes_claim_id,omitempty"`
	CreatedBy         string         `json:"created_by,omitempty"`
	ApprovalID        string         `json:"approval_id,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
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

var (
	emailRE          = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	phoneRE          = regexp.MustCompile(`(?m)(^|[^\d])((?:\+?62|0)8\d{7,12})([^\d]|$)`)
	longIDRE         = regexp.MustCompile(`(^|[^\d])(\d{12,20})([^\d]|$)`)
	credentialNameRE = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*(?:PASSWORD|PASS|TOKEN|SECRET|API_KEY|ACCESS_KEY|PRIVATE_KEY|CREDENTIAL|USERNAME|_USER|_KEYS)[A-Z0-9_]*\b`)
	secretContextRE  = regexp.MustCompile(`(?i)\b(?:request|rotate|rotated|stored|store|fetch|set|export|configure|vault|workspace variable|workspace variables|credential|credentials|password|secret|api key)[^\n.]{0,180}`)
)
