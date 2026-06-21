package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool   *pgxpool.Pool
	runner storeRunner
	inTx   bool
}

type storeRunner interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ScopeSummary struct {
	Scope        string `json:"scope"`
	Documents    int    `json:"documents"`
	Claims       int    `json:"claims"`
	Observations int    `json:"observations"`
	Summaries    int    `json:"summaries"`
	Entities     int    `json:"entities"`
	Relations    int    `json:"relations"`
	Conflicts    int    `json:"conflicts"`
	Sources      int    `json:"sources"`
	Jobs         int    `json:"jobs"`
}

const maxListScopesLimit = 10000

var fullTextTermPattern = regexp.MustCompile(`[A-Za-z0-9_]+`)

func pgInterval(duration time.Duration) string {
	seconds := int64(duration.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool, runner: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) queryRunner() storeRunner {
	if s.runner != nil {
		return s.runner
	}
	return s.pool
}

func (s *Store) WithTx(ctx context.Context, fn func(*Store) error) error {
	if s.inTx {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	txStore := *s
	txStore.runner = tx
	txStore.inTx = true
	if err := fn(&txStore); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) withTxRunner(ctx context.Context, fn func(storeRunner) error) error {
	if s.inTx {
		return fn(s.queryRunner())
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "SELECT '[1,0,0]'::vector(3)"); err != nil {
		return err
	}
	requiredTables := []string{
		"documents",
		"chunks",
		"claims",
		"observations",
		"evidence",
		"audit_events",
		"source_configs",
		"ingestion_jobs",
		"entities",
		"relations",
		"memory_summaries",
		"policies",
		"approval_requests",
		"rate_limit_buckets",
	}
	for _, table := range requiredTables {
		var exists bool
		if err := s.pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("required table %s is missing; run migrations", table)
		}
	}
	return nil
}

func (s *Store) AllowRateLimit(ctx context.Context, key string, window time.Duration, limit int) (bool, time.Time, error) {
	if strings.TrimSpace(key) == "" {
		return false, time.Time{}, fmt.Errorf("rate limit key is required")
	}
	if window <= 0 {
		return false, time.Time{}, fmt.Errorf("rate limit window must be positive")
	}
	if limit < 1 {
		return false, time.Time{}, fmt.Errorf("rate limit limit must be positive")
	}
	var count int
	var resetAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rate_limit_buckets (bucket_key, reset_at, count)
		VALUES ($1, $2, 1)
		ON CONFLICT (bucket_key) DO UPDATE
		SET
		  reset_at = CASE
		    WHEN rate_limit_buckets.reset_at <= now() THEN EXCLUDED.reset_at
		    ELSE rate_limit_buckets.reset_at
		  END,
		  count = CASE
		    WHEN rate_limit_buckets.reset_at <= now() THEN 1
		    ELSE rate_limit_buckets.count + 1
		  END,
		  updated_at = now()
		RETURNING count, reset_at
	`, key, time.Now().Add(window)).Scan(&count, &resetAt)
	if err != nil {
		return false, time.Time{}, err
	}
	return count <= limit, resetAt, nil
}

func (s *Store) PruneRateLimitBuckets(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = 24 * time.Hour
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rate_limit_buckets
		WHERE updated_at < now() - $1::interval
	`, pgInterval(olderThan))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ExpireClaims(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE claims
		SET status = 'expired', updated_at = now()
		WHERE expires_at IS NOT NULL
		  AND expires_at < now()
		  AND status NOT IN ('deprecated', 'expired')
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) EnsureMigrationTable(ctx context.Context) error {
	_, err := s.queryRunner().Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func (s *Store) MigrationApplied(ctx context.Context, filename string) (bool, error) {
	var found int
	err := s.pool.QueryRow(ctx, "SELECT 1 FROM schema_migrations WHERE filename = $1", filename).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (s *Store) ApplyMigration(ctx context.Context, filename string, sql string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", filename); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type DocumentRecord struct {
	SourceType      string
	SourceURL       string
	SourceID        string
	SourceConfigID  string
	IngestionJobID  string
	Title           string
	Scope           string
	ContentChecksum string
	SourceUpdatedAt string
	Authority       string
	AuthorityScore  float64
	Metadata        map[string]any
}

type ChunkRecord struct {
	Content             string
	Embedding           []float64
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingDimensions int
	SourceConfigID      string
	IngestionJobID      string
	Metadata            map[string]any
}

type ClaimRecord struct {
	ClaimText           string
	Scope               string
	SourceURL           string
	SourceType          string
	Authority           string
	Status              string
	Confidence          float64
	Embedding           []float64
	EmbeddingProvider   string
	EmbeddingModel      string
	EmbeddingDimensions int
	SourceConfigID      string
	IngestionJobID      string
	AuthorityScore      float64
	Metadata            map[string]any
}

type SourceRefreshClaimResult struct {
	Deprecated int64
}

type SourceRefreshGraphResult struct {
	DeprecatedRelations int64
	DeletedSummaries    int64
}

type EvidenceRecord struct {
	ClaimID    string
	DocumentID string
	Quote      string
	SourceURL  string
	SourceType string
}

type FeedbackRecord struct {
	ClaimID   string
	Verdict   string
	Reason    string
	SourceURL string
	CreatedBy string
}

type ConflictRecord struct {
	Scope                 string
	ConflictType          string
	Severity              string
	PrimaryClaimID        string
	ConflictingClaimID    string
	PrimaryRelationID     string
	ConflictingRelationID string
	EntityID              string
	DetectedBy            string
	Authority             string
	Metadata              map[string]any
}

type ConflictResult struct {
	ID                    string         `json:"id"`
	Scope                 string         `json:"scope"`
	ConflictType          string         `json:"conflict_type"`
	Status                string         `json:"status"`
	Severity              string         `json:"severity"`
	PrimaryClaimID        string         `json:"primary_claim_id,omitempty"`
	ConflictingClaimID    string         `json:"conflicting_claim_id,omitempty"`
	PrimaryRelationID     string         `json:"primary_relation_id,omitempty"`
	ConflictingRelationID string         `json:"conflicting_relation_id,omitempty"`
	EntityID              string         `json:"entity_id,omitempty"`
	DetectedBy            string         `json:"detected_by,omitempty"`
	Authority             string         `json:"authority"`
	ResolvedBy            string         `json:"resolved_by,omitempty"`
	Resolution            string         `json:"resolution,omitempty"`
	Metadata              map[string]any `json:"metadata"`
	ResolvedAt            *string        `json:"resolved_at,omitempty"`
	UpdatedAt             string         `json:"updated_at"`
}

type ConflictFilter struct {
	Scope      string
	Status     string
	Severity   string
	ClaimID    string
	RelationID string
	Limit      int
}

type ObservationRecord struct {
	ID              string         `json:"id,omitempty"`
	Scope           string         `json:"scope"`
	ObservationType string         `json:"observation_type"`
	ObservationText string         `json:"observation_text"`
	Status          string         `json:"status"`
	Authority       string         `json:"authority"`
	AuthorityScore  float64        `json:"authority_score"`
	Confidence      float64        `json:"confidence"`
	FreshnessStatus string         `json:"freshness_status"`
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
	Value           map[string]any `json:"value,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type ObservationResult struct {
	ID              string         `json:"id"`
	Scope           string         `json:"scope"`
	ObservationType string         `json:"observation_type"`
	ObservationText string         `json:"observation_text"`
	Status          string         `json:"status"`
	Authority       string         `json:"authority"`
	AuthorityScore  float64        `json:"authority_score"`
	Confidence      float64        `json:"confidence"`
	FreshnessStatus string         `json:"freshness_status"`
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
	ObservedAt      string         `json:"observed_at"`
	ValidFrom       *string        `json:"valid_from,omitempty"`
	ExpiresAt       *string        `json:"expires_at,omitempty"`
	LastVerifiedAt  *string        `json:"last_verified_at,omitempty"`
	CreatedBy       string         `json:"created_by,omitempty"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	Value           map[string]any `json:"value"`
	Metadata        map[string]any `json:"metadata"`
}

type ObservationFilter struct {
	Scope           string
	Query           string
	ObservationType string
	Status          string
	Since           string
	Until           string
	Limit           int
}

type ResolveConflictInput struct {
	Status     string         `json:"status"`
	ResolvedBy string         `json:"resolved_by"`
	Resolution string         `json:"resolution"`
	Metadata   map[string]any `json:"metadata"`
}

type EntityRecord struct {
	Scope          string
	EntityType     string
	Name           string
	Description    string
	SourceURL      string
	SourceType     string
	Confidence     float64
	Embedding      []float64
	SourceConfigID string
	IngestionJobID string
	Metadata       map[string]any
}

type RelationRecord struct {
	Scope          string
	RelationType   string
	SourceEntityID string
	TargetEntityID string
	ClaimID        string
	SourceURL      string
	SourceType     string
	Confidence     float64
	SourceConfigID string
	IngestionJobID string
	Metadata       map[string]any
}

type SourceConfigRecord struct {
	ID             string         `json:"id"`
	Scope          string         `json:"scope"`
	SourceType     string         `json:"source_type"`
	Name           string         `json:"name"`
	BaseURL        string         `json:"base_url,omitempty"`
	ConnectorKind  string         `json:"connector_kind"`
	Status         string         `json:"status"`
	Authority      string         `json:"authority"`
	AuthorityScore float64        `json:"authority_score"`
	Config         map[string]any `json:"config"`
	Metadata       map[string]any `json:"metadata"`
	LastSuccessAt  *string        `json:"last_success_at,omitempty"`
	LastErrorAt    *string        `json:"last_error_at,omitempty"`
	LastError      *string        `json:"last_error,omitempty"`
	CreatedBy      string         `json:"created_by,omitempty"`
	ApprovalID     string         `json:"approval_id,omitempty"`
}

type IngestionJobRecord struct {
	ID               string         `json:"id"`
	SourceConfigID   string         `json:"source_config_id,omitempty"`
	Scope            string         `json:"scope"`
	SourceType       string         `json:"source_type"`
	SourceURL        string         `json:"source_url,omitempty"`
	TriggerType      string         `json:"trigger_type"`
	Status           string         `json:"status"`
	Authority        string         `json:"authority"`
	LeaseOwner       string         `json:"lease_owner,omitempty"`
	HeartbeatAt      *string        `json:"heartbeat_at,omitempty"`
	StartedAt        *string        `json:"started_at,omitempty"`
	FinishedAt       *string        `json:"finished_at,omitempty"`
	Attempts         int            `json:"attempts"`
	MaxAttempts      int            `json:"max_attempts"`
	DocumentsSeen    int            `json:"documents_seen"`
	DocumentsChanged int            `json:"documents_changed"`
	ChunksWritten    int            `json:"chunks_written"`
	ClaimsWritten    int            `json:"claims_written"`
	ErrorMessage     *string        `json:"error_message,omitempty"`
	CreatedBy        string         `json:"created_by,omitempty"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
	Metadata         map[string]any `json:"metadata"`
}

type AuditEventRecord struct {
	ID         string         `json:"id"`
	EventType  string         `json:"event_type"`
	Actor      string         `json:"actor,omitempty"`
	TargetType string         `json:"target_type,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Scope      string         `json:"scope,omitempty"`
	SourceURL  string         `json:"source_url,omitempty"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  string         `json:"created_at"`
}

type MemorySummaryRecord struct {
	Scope         string
	Level         string
	Key           string
	Title         string
	Summary       string
	SourceCount   int
	RelationCount int
	TokenEstimate int
	SourceURLs    []string
	Metadata      map[string]any
}

type MemorySummaryResult struct {
	ID            string         `json:"id"`
	Scope         string         `json:"scope"`
	Level         string         `json:"level"`
	Key           string         `json:"key"`
	Title         string         `json:"title"`
	Summary       string         `json:"summary"`
	SourceCount   int            `json:"source_count"`
	RelationCount int            `json:"relation_count"`
	TokenEstimate int            `json:"token_estimate"`
	SourceURLs    []string       `json:"source_urls"`
	Metadata      map[string]any `json:"metadata"`
	Rank          float64        `json:"rank_score"`
	UpdatedAt     string         `json:"updated_at"`
}

type MemoryHealthResult struct {
	Scope       string                `json:"scope,omitempty"`
	Status      string                `json:"status"`
	Score       int                   `json:"score"`
	CheckedAt   string                `json:"checked_at"`
	Reasons     []string              `json:"reasons"`
	Signals     []MemoryHealthSignal  `json:"signals"`
	Documents   MemoryHealthDocument  `json:"documents"`
	Claims      MemoryHealthClaim     `json:"claims"`
	Graph       MemoryHealthGraph     `json:"graph"`
	Summaries   MemoryHealthSummary   `json:"summaries"`
	Sources     MemoryHealthSource    `json:"sources"`
	Ingestion   MemoryHealthIngestion `json:"ingestion"`
	Conflicts   MemoryHealthConflict  `json:"conflicts"`
	Learning    MemoryHealthLearning  `json:"learning"`
	Approvals   MemoryHealthApproval  `json:"approvals"`
	LastUpdated map[string]string     `json:"last_updated,omitempty"`
}

type MemoryHealthSignal struct {
	Code        string `json:"code"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Count       int    `json:"count"`
	ScoreImpact int    `json:"score_impact"`
	Message     string `json:"message"`
	Action      string `json:"action"`
}

type MemoryHealthDocument struct {
	Total      int `json:"total"`
	Active     int `json:"active"`
	Stale      int `json:"stale"`
	Deprecated int `json:"deprecated"`
	Deleted    int `json:"deleted"`
}

type MemoryHealthClaim struct {
	Total                    int `json:"total"`
	Verified                 int `json:"verified"`
	Inferred                 int `json:"inferred"`
	Unverified               int `json:"unverified"`
	Challenged               int `json:"challenged"`
	Deprecated               int `json:"deprecated"`
	Expired                  int `json:"expired"`
	Stale                    int `json:"stale"`
	WithEvidence             int `json:"with_evidence"`
	TrustedFromCodeDocuments int `json:"trusted_from_code_documents"`
}

type MemoryHealthGraph struct {
	Entities            int `json:"entities"`
	ActiveEntities      int `json:"active_entities"`
	Relations           int `json:"relations"`
	ActiveRelations     int `json:"active_relations"`
	ChallengedRelations int `json:"challenged_relations"`
	StaleRelations      int `json:"stale_relations"`
}

type MemoryHealthSummary struct {
	Total         int            `json:"total"`
	Levels        map[string]int `json:"levels"`
	TokenEstimate int            `json:"token_estimate"`
}

type MemoryHealthSource struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Paused   int `json:"paused"`
	Disabled int `json:"disabled"`
	Error    int `json:"error"`
}

type MemoryHealthIngestion struct {
	TotalJobs        int `json:"total_jobs"`
	RecentJobs       int `json:"recent_jobs"`
	SucceededJobs    int `json:"succeeded_jobs"`
	FailedJobs       int `json:"failed_jobs"`
	RunningJobs      int `json:"running_jobs"`
	StaleRunningJobs int `json:"stale_running_jobs"`
	QueuedJobs       int `json:"queued_jobs"`
	RetryJobs        int `json:"retry_jobs"`
	DocumentsSeen    int `json:"documents_seen"`
	DocumentsChanged int `json:"documents_changed"`
	ChunksWritten    int `json:"chunks_written"`
	ClaimsWritten    int `json:"claims_written"`
}

type MemoryHealthConflict struct {
	Total     int `json:"total"`
	Open      int `json:"open"`
	Reviewing int `json:"reviewing"`
	Blocking  int `json:"blocking"`
	High      int `json:"high"`
}

type MemoryHealthLearning struct {
	Total                  int `json:"total"`
	Pending                int `json:"pending"`
	Accepted               int `json:"accepted"`
	Applied                int `json:"applied"`
	Rejected               int `json:"rejected"`
	DuplicatePendingGroups int `json:"duplicate_pending_groups"`
}

type MemoryHealthApproval struct {
	Total    int `json:"total"`
	Pending  int `json:"pending"`
	Approved int `json:"approved"`
	Rejected int `json:"rejected"`
}

type SummaryDocumentRecord struct {
	DocumentID string
	SourceType string
	SourceURL  string
	SourceID   string
	Title      string
	Scope      string
	Content    string
	Metadata   map[string]any
	Relations  int
	Chunks     int
	IngestedAt string
}

type AuditEventFilter struct {
	Scope      string
	EventType  string
	TargetType string
	Since      time.Time
	Until      time.Time
	Limit      int
}

type IntegrationCursorRecord struct {
	ID              string         `json:"id"`
	IntegrationType string         `json:"integration_type"`
	Target          string         `json:"target"`
	CursorValue     string         `json:"cursor_value,omitempty"`
	CursorTime      time.Time      `json:"-"`
	Metadata        map[string]any `json:"metadata"`
	CreatedAt       string         `json:"created_at,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
}

func (s *Store) UpsertDocument(ctx context.Context, record DocumentRecord) (string, error) {
	id := stableID("doc", record.Scope, record.SourceType, record.SourceURL)
	if record.SourceConfigID == "" {
		record.SourceConfigID = metadataString(record.Metadata, "source_config_id")
	}
	if record.IngestionJobID == "" {
		record.IngestionJobID = metadataString(record.Metadata, "ingestion_job_id")
	}
	if record.Authority == "" {
		record.Authority = metadataString(record.Metadata, "authority")
	}
	if record.Authority == "" {
		record.Authority = "manual-unverified"
	}
	if record.AuthorityScore == 0 {
		record.AuthorityScore = metadataFloat(record.Metadata, "authority_score")
	}
	if record.AuthorityScore == 0 {
		record.AuthorityScore = 0.35
	}
	metadata := jsonb(record.Metadata)
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO documents (
		  id, source_type, source_url, source_id, title, scope, content_checksum,
		  source_updated_at, source_config_id, ingestion_job_id, authority,
		  authority_score, metadata
		)
		VALUES (
		  $1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, '')::timestamptz,
		  NULLIF($9, ''), NULLIF($10, ''), $11, $12, $13::jsonb
		)
		ON CONFLICT (source_type, source_url, scope)
		DO UPDATE SET
		  source_id = EXCLUDED.source_id,
		  title = EXCLUDED.title,
		  content_checksum = EXCLUDED.content_checksum,
		  source_updated_at = EXCLUDED.source_updated_at,
		  source_config_id = EXCLUDED.source_config_id,
		  ingestion_job_id = EXCLUDED.ingestion_job_id,
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  ingested_at = now(),
		  metadata = EXCLUDED.metadata
	`, id, record.SourceType, record.SourceURL, record.SourceID, record.Title, record.Scope, record.ContentChecksum, record.SourceUpdatedAt, record.SourceConfigID, record.IngestionJobID, record.Authority, record.AuthorityScore, metadata)
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, "SELECT id FROM documents WHERE source_type = $1 AND source_url = $2 AND scope = $3", record.SourceType, record.SourceURL, record.Scope).Scan(&id)
	return id, err
}

func (s *Store) MarkDocumentIngestComplete(ctx context.Context, documentID string) error {
	_, err := s.queryRunner().Exec(ctx, `
		UPDATE documents
		SET metadata = jsonb_set(COALESCE(metadata, '{}'::jsonb), '{ingest_complete}', 'true'::jsonb, true),
		    ingested_at = now()
		WHERE id = $1
	`, documentID)
	return err
}

func (s *Store) ReplaceChunks(ctx context.Context, documentID, scope string, chunks []ChunkRecord) error {
	return s.withTxRunner(ctx, func(tx storeRunner) error {
		if _, err := tx.Exec(ctx, "DELETE FROM chunks WHERE document_id = $1", documentID); err != nil {
			return err
		}
		for index, chunk := range chunks {
			id := stableID("chunk", documentID, fmt.Sprint(index), chunk.Content)
			if chunk.SourceConfigID == "" {
				chunk.SourceConfigID = metadataString(chunk.Metadata, "source_config_id")
			}
			if chunk.IngestionJobID == "" {
				chunk.IngestionJobID = metadataString(chunk.Metadata, "ingestion_job_id")
			}
			if _, err := tx.Exec(ctx, `
			INSERT INTO chunks (
			  id, document_id, chunk_index, content, embedding, scope,
			  embedding_provider, embedding_model, embedding_dimensions,
			  source_config_id, ingestion_job_id, metadata
			)
				VALUES (
				  $1, $2, $3, $4, $5::vector, $6, $7, $8, $9,
				  NULLIF($10, ''), NULLIF($11, ''), $12::jsonb
				)
			`, id, documentID, index, chunk.Content, vectorLiteral(chunk.Embedding), scope, chunk.EmbeddingProvider, chunk.EmbeddingModel, chunk.EmbeddingDimensions, chunk.SourceConfigID, chunk.IngestionJobID, jsonb(chunk.Metadata)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) InsertClaim(ctx context.Context, claim ClaimRecord) (string, error) {
	id := stableID("claim", claim.Scope, claim.SourceURL, claim.ClaimText)
	if claim.Authority == "" {
		claim.Authority = "manual-unverified"
	}
	if claim.Status == "" {
		claim.Status = "unverified"
	}
	if claim.Confidence == 0 {
		claim.Confidence = 0.35
	}
	if claim.SourceConfigID == "" {
		claim.SourceConfigID = metadataString(claim.Metadata, "source_config_id")
	}
	if claim.IngestionJobID == "" {
		claim.IngestionJobID = metadataString(claim.Metadata, "ingestion_job_id")
	}
	if claim.AuthorityScore == 0 {
		claim.AuthorityScore = metadataFloat(claim.Metadata, "authority_score")
	}
	if claim.AuthorityScore == 0 {
		claim.AuthorityScore = 0.35
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO claims (
		  id, claim_text, scope, source_url, source_type, authority, status,
		  confidence, embedding, last_verified_at, metadata,
		  embedding_provider, embedding_model, embedding_dimensions,
		  source_config_id, ingestion_job_id, authority_score
		)
		VALUES (
		  $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8,
		  $9::vector, now(), $10::jsonb, $11, $12, $13, NULLIF($14, ''), NULLIF($15, ''), $16
		)
		ON CONFLICT DO NOTHING
	`, id, claim.ClaimText, claim.Scope, claim.SourceURL, claim.SourceType, claim.Authority, claim.Status, claim.Confidence, vectorLiteral(claim.Embedding), jsonb(claim.Metadata), claim.EmbeddingProvider, claim.EmbeddingModel, claim.EmbeddingDimensions, claim.SourceConfigID, claim.IngestionJobID, claim.AuthorityScore)
	if err != nil {
		return "", err
	}
	_, err = s.queryRunner().Exec(ctx, `
		UPDATE claims
		SET source_config_id = COALESCE(NULLIF($4, ''), source_config_id),
		    ingestion_job_id = COALESCE(NULLIF($5, ''), ingestion_job_id),
		    authority = CASE
		      WHEN authority = 'manual-unverified' AND NULLIF($6, '') IS NOT NULL THEN $6
		      ELSE authority
		    END,
		    authority_score = GREATEST(authority_score, $7),
		    status = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true' THEN $9
		      ELSE status
		    END,
		    confidence = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true' THEN GREATEST(confidence, $10)
		      ELSE confidence
		    END,
		    last_verified_at = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true' THEN now()
		      ELSE last_verified_at
		    END,
		    metadata = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true'
		        THEN (metadata - 'source_refresh_deprecated' - 'source_refresh_deprecated_at' - 'source_refresh_job_id') || $8::jsonb || jsonb_build_object('source_refresh_reactivated_at', now()::text)
		      ELSE metadata || $8::jsonb
		    END
		WHERE scope = $1
		  AND COALESCE(source_url, '') = COALESCE(NULLIF($2, ''), '')
		  AND claim_text = $3
	`, claim.Scope, claim.SourceURL, claim.ClaimText, claim.SourceConfigID, claim.IngestionJobID, claim.Authority, claim.AuthorityScore, jsonb(claim.Metadata), claim.Status, claim.Confidence)
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		SELECT id FROM claims
		WHERE scope = $1 AND COALESCE(source_url, '') = COALESCE(NULLIF($2, ''), '') AND claim_text = $3
		LIMIT 1
	`, claim.Scope, claim.SourceURL, claim.ClaimText).Scan(&id)
	return id, err
}

func (s *Store) BeginSourceClaimRefresh(ctx context.Context, scope, sourceType, sourceURL, ingestionJobID string) (SourceRefreshClaimResult, error) {
	scope = strings.TrimSpace(scope)
	sourceType = strings.TrimSpace(sourceType)
	sourceURL = strings.TrimSpace(sourceURL)
	if scope == "" || sourceType == "" || sourceURL == "" {
		return SourceRefreshClaimResult{}, fmt.Errorf("scope, source_type, and source_url are required")
	}
	tag, err := s.queryRunner().Exec(ctx, `
		UPDATE claims
		SET status = 'deprecated',
		    confidence = 0,
		    updated_at = now(),
		    metadata = metadata || jsonb_build_object(
		      'source_refresh_deprecated', true,
		      'source_refresh_deprecated_at', now()::text,
		      'source_refresh_job_id', NULLIF($4, '')
		    )
		WHERE scope = $1
		  AND source_type = $2
		  AND source_url = $3
		  AND status NOT IN ('deprecated', 'expired')
	`, scope, sourceType, sourceURL, strings.TrimSpace(ingestionJobID))
	if err != nil {
		return SourceRefreshClaimResult{}, err
	}
	return SourceRefreshClaimResult{Deprecated: tag.RowsAffected()}, nil
}

func (s *Store) BeginSourceGraphRefresh(ctx context.Context, scope, sourceURL, ingestionJobID string) (SourceRefreshGraphResult, error) {
	scope = strings.TrimSpace(scope)
	sourceURL = strings.TrimSpace(sourceURL)
	if scope == "" || sourceURL == "" {
		return SourceRefreshGraphResult{}, fmt.Errorf("scope and source_url are required")
	}
	relationTag, err := s.queryRunner().Exec(ctx, `
		UPDATE relations
		SET status = 'deprecated',
		    confidence = 0,
		    updated_at = now(),
		    metadata = metadata || jsonb_build_object(
		      'source_refresh_deprecated', true,
		      'source_refresh_deprecated_at', now()::text,
		      'source_refresh_job_id', NULLIF($3, '')
		    )
		WHERE scope = $1
		  AND source_url = $2
		  AND status NOT IN ('deprecated', 'expired')
	`, scope, sourceURL, strings.TrimSpace(ingestionJobID))
	if err != nil {
		return SourceRefreshGraphResult{}, err
	}
	summaryTag, err := s.queryRunner().Exec(ctx, `
		DELETE FROM memory_summaries
		WHERE scope = $1
		  AND source_urls @> jsonb_build_array($2::text)
	`, scope, sourceURL)
	if err != nil {
		return SourceRefreshGraphResult{}, err
	}
	return SourceRefreshGraphResult{
		DeprecatedRelations: relationTag.RowsAffected(),
		DeletedSummaries:    summaryTag.RowsAffected(),
	}, nil
}

func (s *Store) AddEvidence(ctx context.Context, evidence EvidenceRecord) error {
	id := stableID("evidence", evidence.ClaimID, evidence.DocumentID, evidence.Quote)
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO evidence (id, claim_id, document_id, quote, source_url, source_type)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''))
		ON CONFLICT DO NOTHING
	`, id, evidence.ClaimID, evidence.DocumentID, evidence.Quote, evidence.SourceURL, evidence.SourceType)
	return err
}

func (s *Store) InsertFeedback(ctx context.Context, feedback FeedbackRecord) (string, error) {
	feedback = normalizeFeedback(feedback)
	id := feedbackID(feedback)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, `
		INSERT INTO feedback (id, claim_id, verdict, reason, source_url, created_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''))
		ON CONFLICT DO NOTHING
	`, id, feedback.ClaimID, feedback.Verdict, feedback.Reason, feedback.SourceURL, feedback.CreatedBy); err != nil {
		return "", err
	}
	if feedback.Verdict == "incorrect" || feedback.Verdict == "stale" || feedback.Verdict == "conflict" {
		if _, err := tx.Exec(ctx, "UPDATE claims SET status = 'challenged', confidence = GREATEST(confidence - 0.25, 0), updated_at = now() WHERE id = $1", feedback.ClaimID); err != nil {
			return "", err
		}
	}
	if feedback.Verdict == "correct" || feedback.Verdict == "useful" {
		if _, err := tx.Exec(ctx, "UPDATE claims SET confidence = LEAST(confidence + 0.1, 1), updated_at = now() WHERE id = $1", feedback.ClaimID); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Store) UpsertClaimConflict(ctx context.Context, conflict ConflictRecord) (string, error) {
	conflict = normalizeConflict(conflict)
	if conflict.PrimaryClaimID == "" || conflict.ConflictingClaimID == "" {
		return "", fmt.Errorf("primary_claim_id and conflicting_claim_id are required")
	}
	if conflict.PrimaryClaimID == conflict.ConflictingClaimID {
		return "", fmt.Errorf("conflicting claim must be different from primary claim")
	}
	primaryID, conflictingID := orderedPair(conflict.PrimaryClaimID, conflict.ConflictingClaimID)
	id := stableID("claim-conflict", conflict.Scope, primaryID, conflictingID, conflict.ConflictType)
	err := s.withTxRunner(ctx, func(tx storeRunner) error {
		var primaryScope, conflictingScope string
		if err := tx.QueryRow(ctx, "SELECT scope FROM claims WHERE id = $1", conflict.PrimaryClaimID).Scan(&primaryScope); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("primary claim %q not found", conflict.PrimaryClaimID)
			}
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT scope FROM claims WHERE id = $1", conflict.ConflictingClaimID).Scan(&conflictingScope); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("conflicting claim %q not found", conflict.ConflictingClaimID)
			}
			return err
		}
		if primaryScope != conflictingScope {
			return fmt.Errorf("conflicting claims must be in the same scope")
		}
		if conflict.Scope == "" {
			conflict.Scope = primaryScope
		}
		if conflict.Scope != primaryScope {
			return fmt.Errorf("conflict scope %q does not match claim scope %q", conflict.Scope, primaryScope)
		}
		if _, err := tx.Exec(ctx, `
		INSERT INTO conflicts (
		  id, scope, conflict_type, status, severity,
		  primary_claim_id, conflicting_claim_id,
		  detected_by, authority, metadata
		)
		VALUES ($1, $2, $3, 'open', $4, $5, $6, NULLIF($7, ''), $8, $9::jsonb)
		ON CONFLICT (id)
		DO UPDATE SET
		  status = CASE WHEN conflicts.status = 'resolved' THEN 'reviewing' ELSE conflicts.status END,
		  severity = EXCLUDED.severity,
		  detected_by = EXCLUDED.detected_by,
		  authority = EXCLUDED.authority,
		  metadata = conflicts.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, id, conflict.Scope, conflict.ConflictType, conflict.Severity, conflict.PrimaryClaimID, conflict.ConflictingClaimID, conflict.DetectedBy, conflict.Authority, jsonb(conflict.Metadata)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE claims
		SET status = 'challenged',
		    confidence = GREATEST(confidence - 0.25, 0),
		    updated_at = now()
		WHERE id = ANY($1)
		  AND status NOT IN ('deprecated', 'expired')
	`, []string{conflict.PrimaryClaimID, conflict.ConflictingClaimID}); err != nil {
			return err
		}
		return nil
	})
	return id, err
}

func (s *Store) UpsertRelationConflict(ctx context.Context, conflict ConflictRecord) (string, error) {
	conflict = normalizeConflict(conflict)
	if conflict.PrimaryRelationID == "" || conflict.ConflictingRelationID == "" {
		return "", fmt.Errorf("primary_relation_id and conflicting_relation_id are required")
	}
	if conflict.PrimaryRelationID == conflict.ConflictingRelationID {
		return "", fmt.Errorf("conflicting relation must be different from primary relation")
	}
	primaryID, conflictingID := orderedPair(conflict.PrimaryRelationID, conflict.ConflictingRelationID)
	id := stableID("relation-conflict", conflict.Scope, primaryID, conflictingID, conflict.ConflictType)
	err := s.withTxRunner(ctx, func(tx storeRunner) error {
		var primaryScope, primaryEntity, conflictingScope, conflictingEntity string
		if err := tx.QueryRow(ctx, "SELECT scope, source_entity_id FROM relations WHERE id = $1", conflict.PrimaryRelationID).Scan(&primaryScope, &primaryEntity); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("primary relation %q not found", conflict.PrimaryRelationID)
			}
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT scope, source_entity_id FROM relations WHERE id = $1", conflict.ConflictingRelationID).Scan(&conflictingScope, &conflictingEntity); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("conflicting relation %q not found", conflict.ConflictingRelationID)
			}
			return err
		}
		if primaryScope != conflictingScope {
			return fmt.Errorf("conflicting relations must be in the same scope")
		}
		if primaryEntity != conflictingEntity {
			return fmt.Errorf("conflicting relations must share the same source entity")
		}
		if conflict.Scope == "" {
			conflict.Scope = primaryScope
		}
		if conflict.Scope != primaryScope {
			return fmt.Errorf("conflict scope %q does not match relation scope %q", conflict.Scope, primaryScope)
		}
		if conflict.EntityID == "" {
			conflict.EntityID = primaryEntity
		}
		if _, err := tx.Exec(ctx, `
		INSERT INTO conflicts (
		  id, scope, conflict_type, status, severity,
		  primary_relation_id, conflicting_relation_id, entity_id,
		  detected_by, authority, metadata
		)
		VALUES ($1, $2, $3, 'open', $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10::jsonb)
		ON CONFLICT (id)
		DO UPDATE SET
		  status = CASE WHEN conflicts.status = 'resolved' THEN 'reviewing' ELSE conflicts.status END,
		  severity = EXCLUDED.severity,
		  entity_id = COALESCE(EXCLUDED.entity_id, conflicts.entity_id),
		  detected_by = EXCLUDED.detected_by,
		  authority = EXCLUDED.authority,
		  metadata = conflicts.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, id, conflict.Scope, conflict.ConflictType, conflict.Severity, conflict.PrimaryRelationID, conflict.ConflictingRelationID, conflict.EntityID, conflict.DetectedBy, conflict.Authority, jsonb(conflict.Metadata)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
		UPDATE relations
		SET status = 'challenged',
		    confidence = GREATEST(confidence - 0.15, 0),
		    updated_at = now()
		WHERE id = ANY($1)
		  AND status NOT IN ('deprecated', 'expired')
	`, []string{conflict.PrimaryRelationID, conflict.ConflictingRelationID}); err != nil {
			return err
		}
		return nil
	})
	return id, err
}

func (s *Store) ListOpenConflictsForClaims(ctx context.Context, scope string, claimIDs []string) ([]ConflictResult, error) {
	claimIDs = cleanStringList(claimIDs)
	if len(claimIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, conflictSelectSQL()+`
		WHERE scope = $1
		  AND status IN ('open', 'reviewing')
		  AND (
		    primary_claim_id = ANY($2)
		    OR conflicting_claim_id = ANY($2)
		  )
		ORDER BY
		  CASE severity
		    WHEN 'blocking' THEN 4
		    WHEN 'high' THEN 3
		    WHEN 'medium' THEN 2
		    ELSE 1
		  END DESC,
		  updated_at DESC
		LIMIT 50
	`, strings.TrimSpace(scope), claimIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConflictResult{}
	for rows.Next() {
		item, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListOpenConflictsForRelations(ctx context.Context, scope string, relationIDs []string) ([]ConflictResult, error) {
	relationIDs = cleanStringList(relationIDs)
	if len(relationIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, conflictSelectSQL()+`
		WHERE scope = $1
		  AND status IN ('open', 'reviewing')
		  AND (
		    primary_relation_id = ANY($2)
		    OR conflicting_relation_id = ANY($2)
		  )
		ORDER BY
		  CASE severity
		    WHEN 'blocking' THEN 4
		    WHEN 'high' THEN 3
		    WHEN 'medium' THEN 2
		    ELSE 1
		  END DESC,
		  updated_at DESC
		LIMIT 50
	`, strings.TrimSpace(scope), relationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConflictResult{}
	for rows.Next() {
		item, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ListConflicts(ctx context.Context, filter ConflictFilter) ([]ConflictResult, error) {
	filter.Scope = strings.TrimSpace(filter.Scope)
	rawStatus := strings.TrimSpace(filter.Status)
	filter.Status = normalizedConflictStatus(filter.Status)
	if rawStatus != "" && filter.Status == "" {
		return nil, fmt.Errorf("status must be open, reviewing, resolved, or suppressed")
	}
	rawSeverity := strings.TrimSpace(filter.Severity)
	filter.Severity = normalizedConflictSeverity(filter.Severity)
	if rawSeverity != "" && filter.Severity == "" {
		return nil, fmt.Errorf("severity must be low, medium, high, or blocking")
	}
	filter.ClaimID = strings.TrimSpace(filter.ClaimID)
	filter.RelationID = strings.TrimSpace(filter.RelationID)
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	query := conflictSelectSQL() + " WHERE true"
	args := []any{}
	add := func(fragment string, value any) {
		args = append(args, value)
		query += fmt.Sprintf(" AND "+fragment, len(args))
	}
	if filter.Scope != "" {
		add("scope = $%d", filter.Scope)
	}
	if filter.Status != "" {
		add("status = $%d", filter.Status)
	}
	if filter.Severity != "" {
		add("severity = $%d", filter.Severity)
	}
	if filter.ClaimID != "" {
		args = append(args, filter.ClaimID)
		placeholder := len(args)
		query += fmt.Sprintf(" AND (primary_claim_id = $%d OR conflicting_claim_id = $%d)", placeholder, placeholder)
	}
	if filter.RelationID != "" {
		args = append(args, filter.RelationID)
		placeholder := len(args)
		query += fmt.Sprintf(" AND (primary_relation_id = $%d OR conflicting_relation_id = $%d)", placeholder, placeholder)
	}
	args = append(args, filter.Limit)
	query += fmt.Sprintf(`
		ORDER BY
		  CASE severity
		    WHEN 'blocking' THEN 4
		    WHEN 'high' THEN 3
		    WHEN 'medium' THEN 2
		    ELSE 1
		  END DESC,
		  updated_at DESC
		LIMIT $%d
	`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConflictResult{}
	for rows.Next() {
		item, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetConflict(ctx context.Context, id string) (ConflictResult, error) {
	rows, err := s.pool.Query(ctx, conflictSelectSQL()+" WHERE id = $1", strings.TrimSpace(id))
	if err != nil {
		return ConflictResult{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ConflictResult{}, err
		}
		return ConflictResult{}, fmt.Errorf("conflict %q not found", id)
	}
	return scanConflict(rows)
}

func (s *Store) ResolveConflict(ctx context.Context, id string, input ResolveConflictInput) (ConflictResult, error) {
	status := normalizedConflictStatus(input.Status)
	if status == "" {
		return ConflictResult{}, fmt.Errorf("status must be open, reviewing, resolved, or suppressed")
	}
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"decided_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	_, err := s.queryRunner().Exec(ctx, `
		UPDATE conflicts
		SET status = $2,
		    resolved_at = CASE WHEN $2 IN ('resolved', 'suppressed') THEN now() ELSE NULL END,
		    resolved_by = CASE WHEN $2 IN ('resolved', 'suppressed') THEN NULLIF($3, '') ELSE NULL END,
		    resolution = CASE WHEN $2 IN ('resolved', 'suppressed') THEN NULLIF($4, '') ELSE NULL END,
		    metadata = metadata || $5::jsonb,
		    updated_at = now()
		WHERE id = $1
	`, strings.TrimSpace(id), status, strings.TrimSpace(input.ResolvedBy), strings.TrimSpace(input.Resolution), jsonb(metadata))
	if err != nil {
		return ConflictResult{}, err
	}
	return s.GetConflict(ctx, id)
}

func conflictSelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  conflict_type,
		  status,
		  severity,
		  COALESCE(primary_claim_id, ''),
		  COALESCE(conflicting_claim_id, ''),
		  COALESCE(primary_relation_id, ''),
		  COALESCE(conflicting_relation_id, ''),
		  COALESCE(entity_id, ''),
		  COALESCE(detected_by, ''),
		  authority,
		  COALESCE(resolved_by, ''),
		  COALESCE(resolution, ''),
		  metadata,
		  resolved_at::text,
		  updated_at::text
		FROM conflicts
	`
}

type conflictScanner interface {
	Scan(dest ...any) error
}

func scanConflict(row conflictScanner) (ConflictResult, error) {
	var item ConflictResult
	var metadataRaw []byte
	if err := row.Scan(
		&item.ID,
		&item.Scope,
		&item.ConflictType,
		&item.Status,
		&item.Severity,
		&item.PrimaryClaimID,
		&item.ConflictingClaimID,
		&item.PrimaryRelationID,
		&item.ConflictingRelationID,
		&item.EntityID,
		&item.DetectedBy,
		&item.Authority,
		&item.ResolvedBy,
		&item.Resolution,
		&metadataRaw,
		&item.ResolvedAt,
		&item.UpdatedAt,
	); err != nil {
		return ConflictResult{}, err
	}
	item.Metadata = decodeJSONMap(metadataRaw)
	return item, nil
}

func (s *Store) ClaimScope(ctx context.Context, claimID string) (string, error) {
	var scope string
	err := s.pool.QueryRow(ctx, "SELECT scope FROM claims WHERE id = $1", strings.TrimSpace(claimID)).Scan(&scope)
	if err == pgx.ErrNoRows {
		return "", fmt.Errorf("claim %q not found", claimID)
	}
	return scope, err
}

func (s *Store) SearchClaims(ctx context.Context, query, scope, excludeID string, limit int) ([]ClaimResult, error) {
	query = strings.TrimSpace(query)
	scope = strings.TrimSpace(scope)
	excludeID = strings.TrimSpace(excludeID)
	if query == "" || scope == "" {
		return nil, nil
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.queryRunner().Query(ctx, searchClaimsSQL(), query, scope, excludeID, limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := []ClaimResult{}
	for rows.Next() {
		var claim ClaimResult
		if err := rows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func searchClaimsSQL() string {
	return `
		SELECT
			id,
			claim_text,
			scope,
			status,
			source_url,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $5)) * 0.65
			  ),
			  0.4
			) AS text_score,
			0::double precision AS vector_score,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $5)) * 0.65
			  ),
			  0.4
			)
			  + confidence
			  + CASE authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
			CASE
			  WHEN expires_at IS NOT NULL AND expires_at < now() THEN 'expired'
			  WHEN last_verified_at IS NULL THEN 'unknown'
			  WHEN last_verified_at < now() - interval '120 days' THEN 'stale'
			  ELSE 'fresh'
			END AS freshness
		FROM claims
		WHERE scope = $2
		  AND status NOT IN ('deprecated', 'expired')
		  AND ($3 = '' OR id != $3)
		  AND (
		    search_vector @@ plainto_tsquery('simple', $1)
		    OR search_vector @@ to_tsquery('simple', $5)
		  )
		ORDER BY rank_score DESC
		LIMIT $4
	`
}

func (s *Store) DeprecateClaim(ctx context.Context, claimID, reason, createdBy string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE claims
		SET status = 'deprecated',
		    confidence = 0,
		    updated_at = now(),
		    metadata = (metadata - 'source_refresh_deprecated' - 'source_refresh_deprecated_at' - 'source_refresh_job_id') || jsonb_build_object(
		      'deprecated_reason', COALESCE(NULLIF($2, ''), 'forgotten'),
		      'deprecated_by', NULLIF($3, '')
		    )
		WHERE id = $1
		  AND status != 'expired'
	`, claimID, reason, createdBy)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Store) InsertAuditEvent(ctx context.Context, eventType, targetType, targetID, scope, sourceURL string, metadata map[string]any) error {
	id := stableID("audit", eventType, targetType, targetID, fmt.Sprint(metadata))
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO audit_events (id, event_type, target_type, target_id, scope, source_url, metadata)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)
		ON CONFLICT DO NOTHING
	`, id, eventType, targetType, targetID, scope, sourceURL, jsonb(metadata))
	return err
}

func (s *Store) InsertObservation(ctx context.Context, record ObservationRecord) (ObservationResult, error) {
	record = normalizeObservation(record)
	if record.Scope == "" {
		return ObservationResult{}, fmt.Errorf("scope is required")
	}
	if record.ObservationText == "" {
		return ObservationResult{}, fmt.Errorf("observation_text is required")
	}
	if record.ID == "" {
		record.ID = stableID("observation", record.Scope, record.SourceURL, record.SourceID, record.ObservationType, record.ObservedAt, record.ObservationText)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO observations (
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, subject_entity_id, object_entity_id, relation_id,
		  claim_id, document_id, chunk_id, source_config_id, ingestion_job_id,
		  source_url, source_type, source_id, observed_at, valid_from, expires_at,
		  created_by, value, metadata
		)
		VALUES (
		  $1, $2, $3, $4, $5, $6, $7,
		  $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''),
		  NULLIF($13, ''), NULLIF($14, ''), NULLIF($15, ''), NULLIF($16, ''), NULLIF($17, ''),
		  NULLIF($18, ''), NULLIF($19, ''), NULLIF($20, ''), $21::timestamptz, NULLIF($22, '')::timestamptz, NULLIF($23, '')::timestamptz,
		  NULLIF($24, ''), $25::jsonb, $26::jsonb
		)
		ON CONFLICT (id) DO UPDATE SET
		  observation_text = EXCLUDED.observation_text,
		  status = EXCLUDED.status,
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  confidence = EXCLUDED.confidence,
		  freshness_status = EXCLUDED.freshness_status,
		  value = observations.value || EXCLUDED.value,
		  metadata = observations.metadata || EXCLUDED.metadata,
		  updated_at = now()
		RETURNING
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
	`, record.ID, record.Scope, record.ObservationType, record.ObservationText, record.Status, record.Authority, record.AuthorityScore,
		record.Confidence, record.FreshnessStatus, record.SubjectEntityID, record.ObjectEntityID, record.RelationID,
		record.ClaimID, record.DocumentID, record.ChunkID, record.SourceConfigID, record.IngestionJobID,
		record.SourceURL, record.SourceType, record.SourceID, record.ObservedAt, record.ValidFrom, record.ExpiresAt,
		record.CreatedBy, jsonb(record.Value), jsonb(record.Metadata))
	return scanObservation(row)
}

func (s *Store) GetObservation(ctx context.Context, id string) (ObservationResult, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
		FROM observations
		WHERE id = $1
	`, strings.TrimSpace(id))
	observation, err := scanObservation(row)
	if err == pgx.ErrNoRows {
		return ObservationResult{}, fmt.Errorf("observation %q not found", strings.TrimSpace(id))
	}
	return observation, err
}

func (s *Store) LinkObservationProposal(ctx context.Context, observationID, proposalID, createdBy string) (ObservationResult, error) {
	observationID = strings.TrimSpace(observationID)
	proposalID = strings.TrimSpace(proposalID)
	if observationID == "" || proposalID == "" {
		return ObservationResult{}, fmt.Errorf("observation_id and proposal_id are required")
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE observations
		SET status = CASE
		      WHEN status IN ('raw', 'challenged') THEN 'proposed'
		      ELSE status
		    END,
		    metadata = metadata || jsonb_build_object(
		      'learning_proposal_id', $2::text,
		      'learning_proposed_at', now()::text,
		      'learning_proposed_by', NULLIF($3, '')
		    ),
		    updated_at = now()
		WHERE id = $1
		  AND status NOT IN ('rejected', 'deprecated', 'expired')
		RETURNING
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
	`, observationID, proposalID, strings.TrimSpace(createdBy))
	observation, err := scanObservation(row)
	if err == pgx.ErrNoRows {
		return ObservationResult{}, fmt.Errorf("observation %q not found or cannot be proposed", observationID)
	}
	return observation, err
}

func (s *Store) ListObservations(ctx context.Context, filter ObservationFilter) ([]ObservationResult, error) {
	filter.Scope = strings.TrimSpace(filter.Scope)
	if filter.Scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	filter.ObservationType = strings.TrimSpace(filter.ObservationType)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Since = strings.TrimSpace(filter.Since)
	filter.Until = strings.TrimSpace(filter.Until)
	if filter.Limit < 1 {
		filter.Limit = 20
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	anyQuery := ""
	if strings.TrimSpace(filter.Query) != "" {
		anyQuery = fullTextAnyQuery(filter.Query)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id, scope, observation_type, observation_text, status, authority, authority_score,
		  confidence, freshness_status, COALESCE(subject_entity_id, ''), COALESCE(object_entity_id, ''),
		  COALESCE(relation_id, ''), COALESCE(claim_id, ''), COALESCE(document_id, ''),
		  COALESCE(chunk_id, ''), COALESCE(source_config_id, ''), COALESCE(ingestion_job_id, ''),
		  COALESCE(source_url, ''), COALESCE(source_type, ''), COALESCE(source_id, ''),
		  observed_at::text, valid_from::text, expires_at::text, last_verified_at::text,
		  COALESCE(created_by, ''), created_at::text, updated_at::text, value, metadata
		FROM observations
		WHERE scope = $1
		  AND ($2 = '' OR observation_type = $2)
		  AND ($3 = '' OR status = $3)
		  AND ($4 = '' OR observed_at >= $4::timestamptz)
		  AND ($5 = '' OR observed_at <= $5::timestamptz)
		  AND ($6 = '' OR search_vector @@ to_tsquery('simple', NULLIF($6, '')))
		ORDER BY
		  CASE WHEN $6 = '' THEN 0 ELSE ts_rank_cd(search_vector, to_tsquery('simple', $6)) END DESC,
		  observed_at DESC,
		  updated_at DESC
		LIMIT $7
	`, filter.Scope, filter.ObservationType, filter.Status, filter.Since, filter.Until, anyQuery, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observations := []ObservationResult{}
	for rows.Next() {
		observation, err := scanObservation(rows)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func (s *Store) ListAuditEvents(ctx context.Context, filter AuditEventFilter) ([]AuditEventRecord, error) {
	query, args := auditEventsQuery(filter)
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []AuditEventRecord{}
	for rows.Next() {
		var event AuditEventRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&event.ID,
			&event.EventType,
			&event.Actor,
			&event.TargetType,
			&event.TargetID,
			&event.Scope,
			&event.SourceURL,
			&metadataRaw,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.Metadata = decodeJSONMap(metadataRaw)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) GetIntegrationCursor(ctx context.Context, id string) (IntegrationCursorRecord, bool, error) {
	var record IntegrationCursorRecord
	var cursorTime sql.NullTime
	var metadataRaw []byte
	err := s.queryRunner().QueryRow(ctx, `
		SELECT
		  id,
		  integration_type,
		  target,
		  COALESCE(cursor_value, ''),
		  cursor_time,
		  metadata,
		  created_at::text,
		  updated_at::text
		FROM integration_cursors
		WHERE id = $1
	`, strings.TrimSpace(id)).Scan(
		&record.ID,
		&record.IntegrationType,
		&record.Target,
		&record.CursorValue,
		&cursorTime,
		&metadataRaw,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return IntegrationCursorRecord{}, false, nil
	}
	if err != nil {
		return IntegrationCursorRecord{}, false, err
	}
	if cursorTime.Valid {
		record.CursorTime = cursorTime.Time
	}
	record.Metadata = decodeJSONMap(metadataRaw)
	return record, true, nil
}

func (s *Store) ListAuditEventsForDelivery(ctx context.Context, scope string, cursor IntegrationCursorRecord, limit int) ([]AuditEventRecord, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	var cursorTime any
	if !cursor.CursorTime.IsZero() {
		cursorTime = cursor.CursorTime.UTC()
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id,
		  event_type,
		  COALESCE(actor, ''),
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  COALESCE(scope, ''),
		  COALESCE(source_url, ''),
		  metadata,
		  created_at::text
		FROM audit_events
		WHERE ($1 = '' OR scope = $1)
		  AND (
		    $2::timestamptz IS NULL
		    OR created_at > $2
		    OR (created_at = $2 AND id > $3)
		  )
		ORDER BY created_at ASC, id ASC
		LIMIT $4
	`, strings.TrimSpace(scope), cursorTime, strings.TrimSpace(cursor.CursorValue), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []AuditEventRecord{}
	for rows.Next() {
		var event AuditEventRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&event.ID,
			&event.EventType,
			&event.Actor,
			&event.TargetType,
			&event.TargetID,
			&event.Scope,
			&event.SourceURL,
			&metadataRaw,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.Metadata = decodeJSONMap(metadataRaw)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) UpsertIntegrationCursorFromAuditEvent(ctx context.Context, record IntegrationCursorRecord, auditEventID string) error {
	record.ID = strings.TrimSpace(record.ID)
	record.IntegrationType = strings.TrimSpace(record.IntegrationType)
	record.Target = strings.TrimSpace(record.Target)
	auditEventID = strings.TrimSpace(auditEventID)
	if record.ID == "" || record.IntegrationType == "" || record.Target == "" || auditEventID == "" {
		return fmt.Errorf("cursor id, integration_type, target, and audit event id are required")
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO integration_cursors (
		  id, integration_type, target, cursor_value, cursor_time, metadata
		)
		SELECT $1, $2, $3, audit_events.id, audit_events.created_at, $5::jsonb
		FROM audit_events
		WHERE audit_events.id = $4
		ON CONFLICT (id)
		DO UPDATE SET
		  integration_type = EXCLUDED.integration_type,
		  target = EXCLUDED.target,
		  cursor_value = EXCLUDED.cursor_value,
		  cursor_time = EXCLUDED.cursor_time,
		  metadata = integration_cursors.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, record.ID, record.IntegrationType, record.Target, auditEventID, jsonb(record.Metadata))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("audit event %q not found", auditEventID)
	}
	return nil
}

func auditEventsQuery(filter AuditEventFilter) (string, []any) {
	limit := filter.Limit
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	conditions := []string{"TRUE"}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if strings.TrimSpace(filter.Scope) != "" {
		add("scope = $%d", strings.TrimSpace(filter.Scope))
	}
	if strings.TrimSpace(filter.EventType) != "" {
		add("event_type = $%d", strings.TrimSpace(filter.EventType))
	}
	if strings.TrimSpace(filter.TargetType) != "" {
		add("target_type = $%d", strings.TrimSpace(filter.TargetType))
	}
	if !filter.Since.IsZero() {
		add("created_at >= $%d", filter.Since.UTC())
	}
	if !filter.Until.IsZero() {
		add("created_at <= $%d", filter.Until.UTC())
	}
	args = append(args, limit)
	return fmt.Sprintf(`
		SELECT
		  id,
		  event_type,
		  COALESCE(actor, ''),
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  COALESCE(scope, ''),
		  COALESCE(source_url, ''),
		  metadata,
		  created_at::text
		FROM audit_events
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d
	`, strings.Join(conditions, " AND "), len(args)), args
}

func (s *Store) UpsertEntity(ctx context.Context, entity EntityRecord) (string, error) {
	id := stableID("entity", entity.Scope, entity.EntityType, strings.ToLower(entity.Name))
	if entity.Confidence == 0 {
		entity.Confidence = 0.5
	}
	embedding := any(nil)
	if len(entity.Embedding) > 0 {
		embedding = vectorLiteral(entity.Embedding)
	}
	if entity.SourceConfigID == "" {
		entity.SourceConfigID = metadataString(entity.Metadata, "source_config_id")
	}
	if entity.IngestionJobID == "" {
		entity.IngestionJobID = metadataString(entity.Metadata, "ingestion_job_id")
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO entities (
		  id, scope, entity_type, canonical_name, description, authority,
		  authority_score, confidence, source_url, source_type, embedding,
		  source_config_id, ingestion_job_id, metadata
		)
		VALUES (
		  $1, $2, $3, $4, NULLIF($5, ''), 'extracted', 0.55, $6,
		  NULLIF($7, ''), NULLIF($8, ''), $9::vector, NULLIF($10, ''), NULLIF($11, ''), $12::jsonb
		)
		ON CONFLICT (id)
		DO UPDATE SET
		  description = COALESCE(NULLIF(EXCLUDED.description, ''), entities.description),
		  confidence = GREATEST(entities.confidence, EXCLUDED.confidence),
		  source_url = COALESCE(EXCLUDED.source_url, entities.source_url),
		  source_type = COALESCE(EXCLUDED.source_type, entities.source_type),
		  source_config_id = COALESCE(EXCLUDED.source_config_id, entities.source_config_id),
		  ingestion_job_id = COALESCE(EXCLUDED.ingestion_job_id, entities.ingestion_job_id),
		  updated_at = now(),
		  metadata = entities.metadata || EXCLUDED.metadata
	`, id, entity.Scope, entity.EntityType, entity.Name, entity.Description, entity.Confidence, entity.SourceURL, entity.SourceType, embedding, entity.SourceConfigID, entity.IngestionJobID, jsonb(entity.Metadata))
	return id, err
}

func (s *Store) UpsertRelation(ctx context.Context, relation RelationRecord) (string, error) {
	id := stableID("relation", relation.Scope, relation.RelationType, relation.SourceEntityID, relation.TargetEntityID, relation.SourceURL)
	if relation.Confidence == 0 {
		relation.Confidence = 0.5
	}
	if relation.SourceConfigID == "" {
		relation.SourceConfigID = metadataString(relation.Metadata, "source_config_id")
	}
	if relation.IngestionJobID == "" {
		relation.IngestionJobID = metadataString(relation.Metadata, "ingestion_job_id")
	}
	err := s.queryRunner().QueryRow(ctx, `
		UPDATE relations
		SET claim_id = COALESCE(NULLIF($2, ''), claim_id),
		    status = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true' THEN 'active'
		      ELSE status
		    END,
		    confidence = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true' THEN GREATEST(confidence, $3)
		      ELSE GREATEST(confidence, $3)
		    END,
		    source_url = COALESCE(NULLIF($4, ''), source_url),
		    source_type = COALESCE(NULLIF($5, ''), source_type),
		    source_config_id = COALESCE(NULLIF($6, ''), source_config_id),
		    ingestion_job_id = COALESCE(NULLIF($7, ''), ingestion_job_id),
		    last_verified_at = now(),
		    updated_at = now(),
		    metadata = CASE
		      WHEN status = 'deprecated' AND metadata->>'source_refresh_deprecated' = 'true'
		        THEN (metadata - 'source_refresh_deprecated' - 'source_refresh_deprecated_at' - 'source_refresh_job_id') || $8::jsonb || jsonb_build_object('source_refresh_reactivated_at', now()::text)
		      ELSE metadata || $8::jsonb
		    END
		WHERE id = $1
		  AND status != 'expired'
		  AND (status != 'deprecated' OR metadata->>'source_refresh_deprecated' = 'true')
		RETURNING id
	`, id, relation.ClaimID, relation.Confidence, relation.SourceURL, relation.SourceType, relation.SourceConfigID, relation.IngestionJobID, jsonb(relation.Metadata)).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		INSERT INTO relations (
		  id, scope, relation_type, source_entity_id, target_entity_id, claim_id,
		  authority, authority_score, confidence, source_url, source_type,
		  source_config_id, ingestion_job_id, metadata
		)
		VALUES (
		  $1, $2, $3, $4, $5, NULLIF($6, ''), 'extracted', 0.55,
		  $7, NULLIF($8, ''), NULLIF($9, ''), NULLIF($10, ''), NULLIF($11, ''), $12::jsonb
		)
		ON CONFLICT (scope, relation_type, source_entity_id, target_entity_id)
		WHERE status NOT IN ('deprecated', 'expired')
		DO UPDATE SET
		  claim_id = COALESCE(EXCLUDED.claim_id, relations.claim_id),
		  confidence = GREATEST(relations.confidence, EXCLUDED.confidence),
		  source_url = COALESCE(EXCLUDED.source_url, relations.source_url),
		  source_type = COALESCE(EXCLUDED.source_type, relations.source_type),
		  source_config_id = COALESCE(EXCLUDED.source_config_id, relations.source_config_id),
		  ingestion_job_id = COALESCE(EXCLUDED.ingestion_job_id, relations.ingestion_job_id),
		  last_verified_at = now(),
		  updated_at = now(),
		  metadata = relations.metadata || EXCLUDED.metadata
		RETURNING id
	`, id, relation.Scope, relation.RelationType, relation.SourceEntityID, relation.TargetEntityID, relation.ClaimID, relation.Confidence, relation.SourceURL, relation.SourceType, relation.SourceConfigID, relation.IngestionJobID, jsonb(relation.Metadata)).Scan(&id)
	return id, err
}

func (s *Store) UpsertSourceConfig(ctx context.Context, source SourceConfigRecord) (string, error) {
	if source.ID == "" {
		source.ID = stableID("source", source.Scope, source.SourceType, source.Name)
	}
	if source.ConnectorKind == "" {
		source.ConnectorKind = "generic"
	}
	if source.Status == "" {
		source.Status = "active"
	}
	if source.Authority == "" {
		source.Authority = "manual-unverified"
	}
	if source.AuthorityScore == 0 {
		source.AuthorityScore = 0.35
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO source_configs (
		  id, scope, source_type, name, base_url, connector_kind, status,
		  authority, authority_score, config, metadata, created_by
		)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10::jsonb, $11::jsonb, NULLIF($12, ''))
		ON CONFLICT (scope, source_type, name)
		DO UPDATE SET
		  base_url = EXCLUDED.base_url,
		  connector_kind = EXCLUDED.connector_kind,
		  status = EXCLUDED.status,
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  config = EXCLUDED.config,
		  metadata = source_configs.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, source.ID, source.Scope, source.SourceType, source.Name, source.BaseURL, source.ConnectorKind, source.Status, source.Authority, source.AuthorityScore, jsonb(source.Config), jsonb(source.Metadata), source.CreatedBy)
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		SELECT id
		FROM source_configs
		WHERE scope = $1 AND source_type = $2 AND name = $3
	`, source.Scope, source.SourceType, source.Name).Scan(&source.ID)
	return source.ID, err
}

func (s *Store) UpsertMemorySummary(ctx context.Context, summary MemorySummaryRecord) (string, error) {
	if summary.Scope == "" || summary.Level == "" || summary.Key == "" || summary.Title == "" || summary.Summary == "" {
		return "", fmt.Errorf("scope, level, key, title, and summary are required")
	}
	id := stableID("summary", summary.Scope, summary.Level, summary.Key)
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO memory_summaries (
		  id, scope, level, summary_key, title, summary, source_count,
		  relation_count, token_estimate, source_urls, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11::jsonb)
		ON CONFLICT (scope, level, summary_key)
		DO UPDATE SET
		  title = EXCLUDED.title,
		  summary = EXCLUDED.summary,
		  source_count = EXCLUDED.source_count,
		  relation_count = EXCLUDED.relation_count,
		  token_estimate = EXCLUDED.token_estimate,
		  source_urls = EXCLUDED.source_urls,
		  metadata = memory_summaries.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, id, summary.Scope, summary.Level, summary.Key, summary.Title, summary.Summary, summary.SourceCount, summary.RelationCount, summary.TokenEstimate, jsonArray(summary.SourceURLs), jsonb(summary.Metadata))
	if err != nil {
		return "", err
	}
	err = s.queryRunner().QueryRow(ctx, `
		SELECT id
		FROM memory_summaries
		WHERE scope = $1 AND level = $2 AND summary_key = $3
	`, summary.Scope, summary.Level, summary.Key).Scan(&id)
	return id, err
}

func (s *Store) ListMemorySummaries(ctx context.Context, query, scope string, limit int) ([]MemorySummaryResult, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, memorySummarySelectSQL(), strings.TrimSpace(query), strings.TrimSpace(scope), limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MemorySummaryResult{}
	for rows.Next() {
		var item MemorySummaryResult
		var urlsRaw, metadataRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.Scope,
			&item.Level,
			&item.Key,
			&item.Title,
			&item.Summary,
			&item.SourceCount,
			&item.RelationCount,
			&item.TokenEstimate,
			&urlsRaw,
			&metadataRaw,
			&item.Rank,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		item.SourceURLs = decodeJSONStringSlice(urlsRaw)
		item.Metadata = decodeJSONMap(metadataRaw)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) MemoryHealth(ctx context.Context, scope string) (MemoryHealthResult, error) {
	scope = strings.TrimSpace(scope)
	result := MemoryHealthResult{
		Scope:       scope,
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		LastUpdated: map[string]string{},
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, 'active') = 'active')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'stale')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'deprecated')::int,
		  COUNT(*) FILTER (WHERE COALESCE(status, '') = 'deleted')::int,
		  COALESCE(MAX(ingested_at)::text, '')
		FROM documents
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Documents.Total,
		&result.Documents.Active,
		&result.Documents.Stale,
		&result.Documents.Deprecated,
		&result.Documents.Deleted,
		stringMapTarget(result.LastUpdated, "documents"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(DISTINCT claims.id)::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'verified')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'inferred')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'unverified')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'challenged')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'deprecated')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE status = 'expired')::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE freshness_status = 'stale' OR expires_at < now())::int,
		  COUNT(DISTINCT claims.id) FILTER (WHERE evidence.id IS NOT NULL)::int,
		  COALESCE(MAX(claims.updated_at)::text, '')
		FROM claims
		LEFT JOIN evidence ON evidence.claim_id = claims.id
		WHERE ($1 = '' OR claims.scope = $1)
	`, scope).Scan(
		&result.Claims.Total,
		&result.Claims.Verified,
		&result.Claims.Inferred,
		&result.Claims.Unverified,
		&result.Claims.Challenged,
		&result.Claims.Deprecated,
		&result.Claims.Expired,
		&result.Claims.Stale,
		&result.Claims.WithEvidence,
		stringMapTarget(result.LastUpdated, "claims"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT claims.id)::int
		FROM claims
		JOIN documents
		  ON documents.scope = claims.scope
		 AND COALESCE(documents.source_url, '') = COALESCE(claims.source_url, '')
		WHERE ($1 = '' OR claims.scope = $1)
		  AND documents.metadata->>'content_kind' = 'code'
		  AND claims.status NOT IN ('deprecated', 'expired')
	`, scope).Scan(&result.Claims.TrustedFromCodeDocuments); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT COUNT(*)::int FROM entities WHERE ($1 = '' OR scope = $1)),
		  (SELECT COUNT(*)::int FROM entities WHERE ($1 = '' OR scope = $1) AND status = 'active'),
		  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1)),
		  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND status = 'active'),
		  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND status = 'challenged'),
		  (SELECT COUNT(*)::int FROM relations WHERE ($1 = '' OR scope = $1) AND (freshness_status = 'stale' OR expires_at < now())),
		  COALESCE((SELECT MAX(updated_at)::text FROM relations WHERE ($1 = '' OR scope = $1)), '')
	`, scope).Scan(
		&result.Graph.Entities,
		&result.Graph.ActiveEntities,
		&result.Graph.Relations,
		&result.Graph.ActiveRelations,
		&result.Graph.ChallengedRelations,
		&result.Graph.StaleRelations,
		stringMapTarget(result.LastUpdated, "graph"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int, COALESCE(SUM(token_estimate), 0)::int, COALESCE(MAX(updated_at)::text, '')
		FROM memory_summaries
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(&result.Summaries.Total, &result.Summaries.TokenEstimate, stringMapTarget(result.LastUpdated, "summaries")); err != nil {
		return MemoryHealthResult{}, err
	}
	levels, err := s.memorySummaryLevels(ctx, scope)
	if err != nil {
		return MemoryHealthResult{}, err
	}
	result.Summaries.Levels = levels
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'active')::int,
		  COUNT(*) FILTER (WHERE status = 'paused')::int,
		  COUNT(*) FILTER (WHERE status = 'disabled')::int,
		  COUNT(*) FILTER (WHERE status = 'error')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM source_configs
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Sources.Total,
		&result.Sources.Active,
		&result.Sources.Paused,
		&result.Sources.Disabled,
		&result.Sources.Error,
		stringMapTarget(result.LastUpdated, "sources"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)::int
		FROM (
		  SELECT 1
		  FROM learning_proposals
		  WHERE ($1 = '' OR scope = $1)
		    AND status = 'pending'
		  GROUP BY
		    scope,
		    proposal_type,
		    title,
		    COALESCE(target_type, ''),
		    COALESCE(target_id, ''),
		    COALESCE(source_url, '')
		  HAVING COUNT(*) > 1
		) duplicates
	`, scope).Scan(&result.Learning.DuplicatePendingGroups); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE created_at >= now() - interval '24 hours')::int,
		  COUNT(*) FILTER (WHERE status = 'succeeded')::int,
		  COUNT(*) FILTER (WHERE status = 'failed')::int,
		  COUNT(*) FILTER (WHERE status = 'running')::int,
		  COUNT(*) FILTER (
		    WHERE status = 'running'
		      AND (heartbeat_at IS NULL OR heartbeat_at < now() - interval '10 minutes')
		  )::int,
		  COUNT(*) FILTER (WHERE status = 'queued')::int,
		  COUNT(*) FILTER (WHERE status = 'retry')::int,
		  COALESCE(SUM(documents_seen), 0)::int,
		  COALESCE(SUM(documents_changed), 0)::int,
		  COALESCE(SUM(chunks_written), 0)::int,
		  COALESCE(SUM(claims_written), 0)::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM ingestion_jobs
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Ingestion.TotalJobs,
		&result.Ingestion.RecentJobs,
		&result.Ingestion.SucceededJobs,
		&result.Ingestion.FailedJobs,
		&result.Ingestion.RunningJobs,
		&result.Ingestion.StaleRunningJobs,
		&result.Ingestion.QueuedJobs,
		&result.Ingestion.RetryJobs,
		&result.Ingestion.DocumentsSeen,
		&result.Ingestion.DocumentsChanged,
		&result.Ingestion.ChunksWritten,
		&result.Ingestion.ClaimsWritten,
		stringMapTarget(result.LastUpdated, "ingestion"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'open')::int,
		  COUNT(*) FILTER (WHERE status = 'reviewing')::int,
		  COUNT(*) FILTER (WHERE status IN ('open', 'reviewing') AND severity = 'blocking')::int,
		  COUNT(*) FILTER (WHERE status IN ('open', 'reviewing') AND severity = 'high')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM conflicts
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Conflicts.Total,
		&result.Conflicts.Open,
		&result.Conflicts.Reviewing,
		&result.Conflicts.Blocking,
		&result.Conflicts.High,
		stringMapTarget(result.LastUpdated, "conflicts"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'pending')::int,
		  COUNT(*) FILTER (WHERE status = 'accepted')::int,
		  COUNT(*) FILTER (WHERE status = 'applied')::int,
		  COUNT(*) FILTER (WHERE status = 'rejected')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM learning_proposals
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Learning.Total,
		&result.Learning.Pending,
		&result.Learning.Accepted,
		&result.Learning.Applied,
		&result.Learning.Rejected,
		stringMapTarget(result.LastUpdated, "learning"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT
		  COUNT(*)::int,
		  COUNT(*) FILTER (WHERE status = 'pending')::int,
		  COUNT(*) FILTER (WHERE status = 'approved')::int,
		  COUNT(*) FILTER (WHERE status = 'rejected')::int,
		  COALESCE(MAX(updated_at)::text, '')
		FROM approval_requests
		WHERE ($1 = '' OR scope = $1)
	`, scope).Scan(
		&result.Approvals.Total,
		&result.Approvals.Pending,
		&result.Approvals.Approved,
		&result.Approvals.Rejected,
		stringMapTarget(result.LastUpdated, "approvals"),
	); err != nil {
		return MemoryHealthResult{}, err
	}
	assessment := assessMemoryHealth(result)
	result.Score = assessment.Score
	result.Status = assessment.Status
	result.Reasons = assessment.Reasons
	result.Signals = assessment.Signals
	return result, nil
}

func (s *Store) memorySummaryLevels(ctx context.Context, scope string) (map[string]int, error) {
	rows, err := s.queryRunner().Query(ctx, `
		SELECT level, COUNT(*)::int
		FROM memory_summaries
		WHERE ($1 = '' OR scope = $1)
		GROUP BY level
		ORDER BY level
	`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	levels := map[string]int{}
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return nil, err
		}
		levels[level] = count
	}
	return levels, rows.Err()
}

type stringMapScanner struct {
	values map[string]string
	key    string
}

func stringMapTarget(values map[string]string, key string) *stringMapScanner {
	return &stringMapScanner{values: values, key: key}
}

func (s *stringMapScanner) Scan(value any) error {
	if s == nil || s.values == nil || s.key == "" {
		return nil
	}
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		if typed != "" {
			s.values[s.key] = typed
		}
	case []byte:
		if len(typed) > 0 {
			s.values[s.key] = string(typed)
		}
	case time.Time:
		if !typed.IsZero() {
			s.values[s.key] = typed.UTC().Format(time.RFC3339)
		}
	default:
		text := fmt.Sprint(typed)
		if text != "" {
			s.values[s.key] = text
		}
	}
	return nil
}

type memoryHealthAssessment struct {
	Score   int
	Status  string
	Reasons []string
	Signals []MemoryHealthSignal
}

func scoreMemoryHealth(result MemoryHealthResult) (int, string, []string) {
	assessment := assessMemoryHealth(result)
	return assessment.Score, assessment.Status, assessment.Reasons
}

func assessMemoryHealth(result MemoryHealthResult) memoryHealthAssessment {
	score := 100
	reasons := []string{}
	signals := []MemoryHealthSignal{}
	penalize := func(points int, code, category, severity string, count int, reason, action string) {
		score -= points
		reasons = append(reasons, reason)
		signals = append(signals, MemoryHealthSignal{
			Code:        code,
			Category:    category,
			Severity:    severity,
			Count:       count,
			ScoreImpact: points,
			Message:     reason,
			Action:      action,
		})
	}
	if result.Documents.Total == 0 {
		penalize(35, "documents_empty", "documents", "critical", 0, "no documents ingested", "ingest at least one source for this scope")
	}
	if result.Claims.Verified+result.Claims.Inferred == 0 {
		penalize(18, "trusted_claims_empty", "claims", "warning", 0, "no trusted claims available", "ingest authoritative knowledge documents or approve inferred claims")
	}
	if result.Claims.Total > 0 && result.Claims.WithEvidence == 0 {
		penalize(15, "claims_missing_evidence", "claims", "warning", result.Claims.Total, "claims have no evidence links", "re-ingest sources or attach evidence before agents rely on these claims")
	}
	if result.Claims.TrustedFromCodeDocuments > 0 {
		penalize(30, "trusted_claims_from_code_documents", "trust_guard", "critical", result.Claims.TrustedFromCodeDocuments, "trusted claims from code documents need cleanup", "deprecate polluted claims and re-ingest code as graph-only knowledge")
	}
	if result.Summaries.Total == 0 {
		penalize(12, "summaries_empty", "summaries", "warning", 0, "no hierarchical summaries", "run summary rebuild or re-ingest sources with summary generation enabled")
	}
	if result.Graph.ActiveRelations == 0 {
		penalize(8, "graph_relations_empty", "graph", "warning", 0, "no active graph relations", "ingest sources that expose relationships or run graph extraction")
	}
	if result.Conflicts.Blocking > 0 {
		penalize(25, "blocking_conflicts", "conflicts", "critical", result.Conflicts.Blocking, "blocking conflicts need review", "resolve blocking conflicts before allowing autonomous agent work")
	} else if result.Conflicts.Open+result.Conflicts.Reviewing > 0 {
		penalize(15, "active_conflicts", "conflicts", "warning", result.Conflicts.Open+result.Conflicts.Reviewing, "active conflicts need review", "review or resolve open memory conflicts")
	}
	if result.Sources.Error > 0 {
		penalize(15, "source_configs_error", "sources", "critical", result.Sources.Error, "source configs are in error", "fix source configuration or credentials and retry ingestion")
	}
	if result.Ingestion.FailedJobs > 0 {
		penalize(12, "ingestion_jobs_failed", "ingestion", "critical", result.Ingestion.FailedJobs, "ingestion jobs failed", "inspect failed jobs and retry after fixing the source error")
	}
	if result.Ingestion.StaleRunningJobs > 0 {
		penalize(25, "ingestion_jobs_stale_running", "ingestion", "critical", result.Ingestion.StaleRunningJobs, "ingestion jobs are stale while running", "restart or cancel stale workers, then retry affected ingestion jobs")
	}
	if result.Ingestion.RetryJobs > 0 {
		penalize(8, "ingestion_jobs_retrying", "ingestion", "warning", result.Ingestion.RetryJobs, "ingestion jobs are waiting to retry", "monitor retrying jobs or inspect repeated failures")
	}
	if result.Claims.Challenged+result.Claims.Stale+result.Claims.Expired > 0 {
		penalize(8, "claims_need_review", "claims", "warning", result.Claims.Challenged+result.Claims.Stale+result.Claims.Expired, "claims need freshness or challenge review", "review challenged, stale, or expired claims before reuse")
	}
	if result.Graph.ChallengedRelations+result.Graph.StaleRelations > 0 {
		penalize(6, "graph_relations_need_review", "graph", "warning", result.Graph.ChallengedRelations+result.Graph.StaleRelations, "graph relations need review", "review challenged or stale graph relations")
	}
	if result.Learning.Pending > 0 {
		penalize(4, "learning_proposals_pending", "learning", "info", result.Learning.Pending, "learning proposals are pending", "accept, reject, or apply queued learning proposals")
	}
	if result.Learning.DuplicatePendingGroups > 0 {
		penalize(25, "learning_duplicate_pending_groups", "trust_guard", "critical", result.Learning.DuplicatePendingGroups, "duplicate pending learning proposals need cleanup", "deduplicate the pending learning queue before operators review it")
	}
	if result.Approvals.Pending > 0 {
		penalize(4, "approval_requests_pending", "approvals", "info", result.Approvals.Pending, "approval requests are pending", "approve or reject pending agent action requests")
	}
	if score < 0 {
		score = 0
	}
	status := "healthy"
	switch {
	case result.Conflicts.Blocking > 0 || result.Sources.Error > 0 || result.Ingestion.FailedJobs > 0 || result.Ingestion.StaleRunningJobs > 0 || result.Claims.TrustedFromCodeDocuments > 0 || result.Learning.DuplicatePendingGroups > 0 || score < 55:
		status = "critical"
	case score < 80 || result.Conflicts.Open+result.Conflicts.Reviewing > 0 || result.Ingestion.RetryJobs > 0 || result.Learning.Pending > 0 || result.Approvals.Pending > 0:
		status = "needs_review"
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "memory is source-backed and ready")
		signals = append(signals, MemoryHealthSignal{
			Code:        "memory_ready",
			Category:    "readiness",
			Severity:    "info",
			Count:       0,
			ScoreImpact: 0,
			Message:     "memory is source-backed and ready",
			Action:      "proceed",
		})
	}
	return memoryHealthAssessment{Score: score, Status: status, Reasons: reasons, Signals: signals}
}

func memorySummarySelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  level,
		  summary_key,
		  title,
		  summary,
		  source_count,
		  relation_count,
		  token_estimate,
		  source_urls,
		  metadata,
		  LEAST(
		    GREATEST(
		      ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
		      ts_rank_cd(search_vector, to_tsquery('simple', $4)) * 0.65
		    ),
		    0.4
		  )
		  + CASE level
		      WHEN 'repo' THEN 0.35
		      WHEN 'module' THEN 0.3
		      WHEN 'route' THEN 0.34
		      WHEN 'component' THEN 0.34
		      WHEN 'symbol' THEN 0.34
		      WHEN 'package' THEN 0.34
		      WHEN 'file' THEN 0.2
		      ELSE 0.1
		    END AS rank_score,
		  updated_at::text
		FROM memory_summaries
		WHERE scope = $2
		  AND (
		    search_vector @@ plainto_tsquery('simple', $1)
		    OR search_vector @@ to_tsquery('simple', $4)
		    OR $1 = ''
		  )
		ORDER BY rank_score DESC, updated_at DESC
		LIMIT $3
	`
}

func (s *Store) ListDocumentsForSummary(ctx context.Context, scope string, limit int) ([]SummaryDocumentRecord, error) {
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT
		  d.id,
		  d.source_type,
		  d.source_url,
		  COALESCE(d.source_id, ''),
		  d.title,
		  d.scope,
		  COALESCE(ch.content, ''),
		  d.metadata,
		  COALESCE(rel.relation_count, 0),
		  COALESCE(ch.chunk_count, 0),
		  d.ingested_at::text
		FROM documents d
		LEFT JOIN LATERAL (
		  SELECT
		    string_agg(content, E'\n\n' ORDER BY chunk_index) AS content,
		    count(*) AS chunk_count
		  FROM chunks
		  WHERE document_id = d.id
		    AND chunk_index < 4
		) ch ON TRUE
		LEFT JOIN LATERAL (
		  SELECT count(*) AS relation_count
		  FROM relations
		  WHERE scope = d.scope
		    AND source_url = d.source_url
		    AND status NOT IN ('deprecated', 'expired')
		) rel ON TRUE
		WHERE d.scope = $1
		  AND d.status NOT IN ('deprecated', 'deleted')
		ORDER BY d.ingested_at DESC, d.id ASC
		LIMIT $2
	`, strings.TrimSpace(scope), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SummaryDocumentRecord{}
	for rows.Next() {
		var record SummaryDocumentRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&record.DocumentID,
			&record.SourceType,
			&record.SourceURL,
			&record.SourceID,
			&record.Title,
			&record.Scope,
			&record.Content,
			&metadataRaw,
			&record.Relations,
			&record.Chunks,
			&record.IngestedAt,
		); err != nil {
			return nil, err
		}
		record.Metadata = decodeJSONMap(metadataRaw)
		out = append(out, record)
	}
	return out, rows.Err()
}

func stableID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func normalizeFeedback(feedback FeedbackRecord) FeedbackRecord {
	if feedback.Verdict == "" {
		feedback.Verdict = "incorrect"
	}
	return feedback
}

func normalizeConflict(conflict ConflictRecord) ConflictRecord {
	conflict.Scope = strings.TrimSpace(conflict.Scope)
	conflict.PrimaryClaimID = strings.TrimSpace(conflict.PrimaryClaimID)
	conflict.ConflictingClaimID = strings.TrimSpace(conflict.ConflictingClaimID)
	conflict.PrimaryRelationID = strings.TrimSpace(conflict.PrimaryRelationID)
	conflict.ConflictingRelationID = strings.TrimSpace(conflict.ConflictingRelationID)
	conflict.EntityID = strings.TrimSpace(conflict.EntityID)
	conflict.ConflictType = strings.TrimSpace(conflict.ConflictType)
	if conflict.ConflictType == "" {
		conflict.ConflictType = "contradicts"
	}
	conflict.Severity = normalizedConflictSeverity(conflict.Severity)
	if conflict.Severity == "" {
		conflict.Severity = "high"
	}
	conflict.DetectedBy = strings.TrimSpace(conflict.DetectedBy)
	conflict.Authority = strings.TrimSpace(conflict.Authority)
	if conflict.Authority == "" {
		conflict.Authority = "system-detected"
	}
	return conflict
}

func normalizeObservation(record ObservationRecord) ObservationRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.Scope = strings.TrimSpace(record.Scope)
	record.ObservationType = strings.TrimSpace(record.ObservationType)
	if record.ObservationType == "" {
		record.ObservationType = "episode"
	}
	record.ObservationText = strings.TrimSpace(record.ObservationText)
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = "raw"
	}
	record.Authority = strings.TrimSpace(record.Authority)
	if record.Authority == "" {
		record.Authority = "manual-unverified"
	}
	if record.AuthorityScore <= 0 {
		record.AuthorityScore = 0.35
	}
	if record.Confidence <= 0 {
		record.Confidence = 0.35
	}
	record.FreshnessStatus = strings.TrimSpace(record.FreshnessStatus)
	if record.FreshnessStatus == "" {
		record.FreshnessStatus = "unknown"
	}
	record.SubjectEntityID = strings.TrimSpace(record.SubjectEntityID)
	record.ObjectEntityID = strings.TrimSpace(record.ObjectEntityID)
	record.RelationID = strings.TrimSpace(record.RelationID)
	record.ClaimID = strings.TrimSpace(record.ClaimID)
	record.DocumentID = strings.TrimSpace(record.DocumentID)
	record.ChunkID = strings.TrimSpace(record.ChunkID)
	record.SourceConfigID = strings.TrimSpace(record.SourceConfigID)
	record.IngestionJobID = strings.TrimSpace(record.IngestionJobID)
	record.SourceURL = strings.TrimSpace(record.SourceURL)
	record.SourceType = strings.TrimSpace(record.SourceType)
	record.SourceID = strings.TrimSpace(record.SourceID)
	record.ObservedAt = strings.TrimSpace(record.ObservedAt)
	if record.ObservedAt == "" {
		record.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	record.ValidFrom = strings.TrimSpace(record.ValidFrom)
	record.ExpiresAt = strings.TrimSpace(record.ExpiresAt)
	record.CreatedBy = strings.TrimSpace(record.CreatedBy)
	return record
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanObservation(row rowScanner) (ObservationResult, error) {
	var observation ObservationResult
	var valueRaw, metadataRaw []byte
	if err := row.Scan(
		&observation.ID,
		&observation.Scope,
		&observation.ObservationType,
		&observation.ObservationText,
		&observation.Status,
		&observation.Authority,
		&observation.AuthorityScore,
		&observation.Confidence,
		&observation.FreshnessStatus,
		&observation.SubjectEntityID,
		&observation.ObjectEntityID,
		&observation.RelationID,
		&observation.ClaimID,
		&observation.DocumentID,
		&observation.ChunkID,
		&observation.SourceConfigID,
		&observation.IngestionJobID,
		&observation.SourceURL,
		&observation.SourceType,
		&observation.SourceID,
		&observation.ObservedAt,
		&observation.ValidFrom,
		&observation.ExpiresAt,
		&observation.LastVerifiedAt,
		&observation.CreatedBy,
		&observation.CreatedAt,
		&observation.UpdatedAt,
		&valueRaw,
		&metadataRaw,
	); err != nil {
		return ObservationResult{}, err
	}
	observation.Value = decodeJSONMap(valueRaw)
	observation.Metadata = decodeJSONMap(metadataRaw)
	return observation, nil
}

func normalizedConflictStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "", "open", "reviewing", "resolved", "suppressed":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizedConflictSeverity(value string) string {
	switch strings.TrimSpace(value) {
	case "", "low", "medium", "high", "blocking":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func orderedPair(left, right string) (string, string) {
	if strings.Compare(left, right) <= 0 {
		return left, right
	}
	return right, left
}

func cleanStringList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func feedbackID(feedback FeedbackRecord) string {
	feedback = normalizeFeedback(feedback)
	return stableID("feedback", feedback.ClaimID, feedback.Verdict, feedback.Reason, feedback.CreatedBy)
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	switch value := metadata[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
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
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	default:
		return 0
	}
}

func jsonb(value map[string]any) string {
	if value == nil {
		value = map[string]any{}
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func jsonArray(values []string) string {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func vectorLiteral(values []float64) string {
	out := "["
	for index, value := range values {
		if index > 0 {
			out += ","
		}
		out += fmt.Sprintf("%.8f", value)
	}
	return out + "]"
}

type RecallResult struct {
	Claims              []ClaimResult      `json:"claims"`
	SupportingDocuments []DocumentResult   `json:"supporting_documents"`
	GraphContext        []RelationResult   `json:"graph_context,omitempty"`
	RetrievalMode       string             `json:"retrieval_mode,omitempty"`
	RetrievalReasons    []RetrievalReason  `json:"retrieval_reasons,omitempty"`
	RetrievalWarnings   []RetrievalWarning `json:"retrieval_warnings,omitempty"`
}

type RetrievalReason struct {
	Mode    string `json:"mode"`
	Signal  string `json:"signal"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type RetrievalWarning struct {
	Stage     string `json:"stage"`
	Operation string `json:"operation"`
	Query     string `json:"query,omitempty"`
	Message   string `json:"message"`
}

type ClaimResult struct {
	ID            string  `json:"id"`
	Claim         string  `json:"claim_text"`
	Scope         string  `json:"scope"`
	Status        string  `json:"status"`
	Source        *string `json:"source_url,omitempty"`
	Rank          float64 `json:"rank_score"`
	BaseRank      float64 `json:"base_rank_score"`
	TextScore     float64 `json:"text_score"`
	VectorScore   float64 `json:"vector_score"`
	RerankScore   float64 `json:"rerank_score"`
	RerankApplied bool    `json:"rerank_applied"`
	Freshness     string  `json:"freshness"`
}

type DocumentResult struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Source        string  `json:"source_url"`
	Content       string  `json:"content"`
	Rank          float64 `json:"rank_score"`
	BaseRank      float64 `json:"base_rank_score"`
	TextScore     float64 `json:"text_score"`
	VectorScore   float64 `json:"vector_score"`
	RerankScore   float64 `json:"rerank_score"`
	RerankApplied bool    `json:"rerank_applied"`
}

type RelationResult struct {
	ID         string  `json:"id,omitempty"`
	FromEntity string  `json:"from_entity"`
	ToEntity   string  `json:"to_entity"`
	Type       string  `json:"relation_type"`
	Confidence float64 `json:"confidence"`
	SourceURL  *string `json:"source_url,omitempty"`
}

type GraphEntityResult struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	SourceURL  *string `json:"source_url,omitempty"`
	UpdatedAt  string  `json:"updated_at"`
}

type GraphRelationResult struct {
	ID         string  `json:"id"`
	FromID     string  `json:"from_id"`
	FromEntity string  `json:"from_entity"`
	ToID       string  `json:"to_id"`
	ToEntity   string  `json:"to_entity"`
	Type       string  `json:"relation_type"`
	Status     string  `json:"status"`
	Confidence float64 `json:"confidence"`
	SourceURL  *string `json:"source_url,omitempty"`
	UpdatedAt  string  `json:"updated_at"`
}

func (s *Store) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (RecallResult, error) {
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	statusFilter := "status IN ('verified', 'inferred')"
	if includeUnverified {
		statusFilter = "status NOT IN ('deprecated')"
	}

	claimsRows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT
			id,
			claim_text,
			scope,
			status,
			source_url,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $4)) * 0.65
			  ),
			  0.4
			) AS text_score,
			0::double precision AS vector_score,
			LEAST(
			  GREATEST(
			    ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
			    ts_rank_cd(search_vector, to_tsquery('simple', $4)) * 0.65
			  ),
			  0.4
			)
			  + confidence
			  + CASE authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
			CASE
			  WHEN expires_at IS NOT NULL AND expires_at < now() THEN 'expired'
			  WHEN last_verified_at IS NULL THEN 'unknown'
			  WHEN last_verified_at < now() - interval '120 days' THEN 'stale'
			  ELSE 'fresh'
			END AS freshness
		FROM claims
		WHERE scope = $2
		  AND %s
		  AND (
		    search_vector @@ plainto_tsquery('simple', $1)
		    OR search_vector @@ to_tsquery('simple', $4)
		  )
		ORDER BY rank_score DESC
		LIMIT $3
	`, statusFilter), query, scope, limit, anyQuery)
	if err != nil {
		return RecallResult{}, err
	}
	defer claimsRows.Close()

	result := RecallResult{
		Claims:              []ClaimResult{},
		SupportingDocuments: []DocumentResult{},
		GraphContext:        []RelationResult{},
		RetrievalMode:       "full_text",
	}
	for claimsRows.Next() {
		var claim ClaimResult
		if err := claimsRows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return RecallResult{}, err
		}
		result.Claims = append(result.Claims, claim)
	}
	if err := claimsRows.Err(); err != nil {
		return RecallResult{}, err
	}

	docRows, err := s.pool.Query(ctx, `
		SELECT d.id, d.title, d.source_url, ch.content,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS text_score,
		       0::double precision AS vector_score,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS rank_score
		FROM chunks ch
		JOIN documents d ON d.id = ch.document_id
		WHERE ch.scope = $2
		  AND d.scope = $2
		  AND d.status NOT IN ('deprecated', 'deleted')
		  AND (
		    ch.search_vector @@ plainto_tsquery('simple', $1)
		    OR ch.search_vector @@ to_tsquery('simple', $4)
		  )
		ORDER BY rank_score DESC
		LIMIT $3
	`, query, scope, min(limit, 5), anyQuery)
	if err != nil {
		return RecallResult{}, err
	}
	defer docRows.Close()
	for docRows.Next() {
		var doc DocumentResult
		if err := docRows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.TextScore, &doc.VectorScore, &doc.Rank); err != nil {
			return RecallResult{}, err
		}
		result.SupportingDocuments = append(result.SupportingDocuments, doc)
	}
	if err := docRows.Err(); err != nil {
		return RecallResult{}, err
	}
	relations, err := s.RelatedGraph(ctx, query, scope, min(limit, 8))
	if err != nil {
		return RecallResult{}, err
	}
	result.GraphContext = relations
	applyBaseRankScores(&result)
	result.RetrievalReasons = recallRetrievalReasons(result)
	return result, nil
}

func (s *Store) RecallHybrid(ctx context.Context, query, scope string, limit int, includeUnverified bool, queryEmbedding []float64) (RecallResult, error) {
	if len(queryEmbedding) == 0 {
		return s.Recall(ctx, query, scope, limit, includeUnverified)
	}
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	statusFilter := "status IN ('verified', 'inferred')"
	if includeUnverified {
		statusFilter = "status NOT IN ('deprecated')"
	}
	vector := vectorLiteral(queryEmbedding)

	dimensions := len(queryEmbedding)
	claimsRows, err := s.pool.Query(ctx, hybridRecallClaimsSQL(statusFilter, dimensions), query, scope, limit, anyQuery, vector, dimensions)
	if err != nil {
		return RecallResult{}, err
	}
	defer claimsRows.Close()

	result := RecallResult{
		Claims:              []ClaimResult{},
		SupportingDocuments: []DocumentResult{},
		GraphContext:        []RelationResult{},
		RetrievalMode:       "hybrid",
	}
	for claimsRows.Next() {
		var claim ClaimResult
		if err := claimsRows.Scan(&claim.ID, &claim.Claim, &claim.Scope, &claim.Status, &claim.Source, &claim.TextScore, &claim.VectorScore, &claim.Rank, &claim.Freshness); err != nil {
			return RecallResult{}, err
		}
		result.Claims = append(result.Claims, claim)
	}
	if err := claimsRows.Err(); err != nil {
		return RecallResult{}, err
	}

	docRows, err := s.pool.Query(ctx, hybridRecallDocumentsSQL(dimensions), query, scope, min(limit, 5), anyQuery, vector, dimensions)
	if err != nil {
		return RecallResult{}, err
	}
	defer docRows.Close()
	for docRows.Next() {
		var doc DocumentResult
		if err := docRows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.TextScore, &doc.VectorScore, &doc.Rank); err != nil {
			return RecallResult{}, err
		}
		result.SupportingDocuments = append(result.SupportingDocuments, doc)
	}
	if err := docRows.Err(); err != nil {
		return RecallResult{}, err
	}
	relations, err := s.RelatedGraph(ctx, query, scope, min(limit, 8))
	if err != nil {
		return RecallResult{}, err
	}
	result.GraphContext = relations
	applyBaseRankScores(&result)
	result.RetrievalReasons = recallRetrievalReasons(result)
	return result, nil
}

func applyBaseRankScores(result *RecallResult) {
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

func recallRetrievalReasons(result RecallResult) []RetrievalReason {
	reasons := []RetrievalReason{}
	textCount := 0
	vectorCount := 0
	for _, claim := range result.Claims {
		if claim.TextScore > 0 {
			textCount++
		}
		if claim.VectorScore > 0 {
			vectorCount++
		}
	}
	for _, doc := range result.SupportingDocuments {
		if doc.TextScore > 0 {
			textCount++
		}
		if doc.VectorScore > 0 {
			vectorCount++
		}
	}
	if textCount > 0 {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "text",
			Message: "Full-text/BM25-style matches contributed to recalled claims or documents.",
			Count:   textCount,
		})
	}
	if vectorCount > 0 {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "vector",
			Message: "Semantic vector similarity contributed to recalled claims or documents.",
			Count:   vectorCount,
		})
	}
	if len(result.GraphContext) > 0 {
		reasons = append(reasons, RetrievalReason{
			Mode:    "entity_local",
			Signal:  "graph",
			Message: "Entity-neighborhood graph relations expanded the packet beyond lexical matches.",
			Count:   len(result.GraphContext),
		})
	}
	if len(reasons) == 0 && (len(result.Claims) > 0 || len(result.SupportingDocuments) > 0) {
		reasons = append(reasons, RetrievalReason{
			Mode:    result.RetrievalMode,
			Signal:  "rank",
			Message: "Ranked recall returned context without exposed text/vector sub-scores.",
			Count:   len(result.Claims) + len(result.SupportingDocuments),
		})
	}
	return reasons
}

func hybridRecallClaimsSQL(statusFilter string, dimensions int) string {
	embeddingExpr, queryExpr := vectorComparisonExpr("embedding", "$5", dimensions)
	return fmt.Sprintf(`
		WITH text_matches AS (
		  SELECT
		    id,
		    LEAST(
		      GREATEST(
		        ts_rank_cd(search_vector, plainto_tsquery('simple', $1)),
		        ts_rank_cd(search_vector, to_tsquery('simple', $4)) * 0.65
		      ),
		      0.4
		    ) AS text_score
		  FROM claims
		  WHERE scope = $2
		    AND %s
		    AND (
		      search_vector @@ plainto_tsquery('simple', $1)
		      OR search_vector @@ to_tsquery('simple', $4)
		    )
		  ORDER BY text_score DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		vector_matches AS (
		  SELECT
		    id,
		    GREATEST(0, 1 - (%s <=> %s)) AS vector_score
		  FROM claims
		  WHERE scope = $2
		    AND %s
		    AND embedding_dimensions = $6
		  ORDER BY %s <=> %s
		  LIMIT GREATEST($3 * 3, 12)
		),
		candidates AS (
		  SELECT id FROM text_matches
		  UNION
		  SELECT id FROM vector_matches
		)
		SELECT
			c.id,
			c.claim_text,
			c.scope,
			c.status,
			c.source_url,
			COALESCE(tm.text_score, 0) AS text_score,
			COALESCE(vm.vector_score, 0) AS vector_score,
			COALESCE(tm.text_score, 0)
			  + COALESCE(vm.vector_score, 0) * 0.45
			  + c.confidence
			  + CASE c.authority
				  WHEN 'official-doc' THEN 0.25
				  WHEN 'adr' THEN 0.2
				  WHEN 'team-convention' THEN 0.15
				  WHEN 'jira-resolved' THEN 0.1
				  ELSE 0
				END AS rank_score,
			CASE
			  WHEN c.expires_at IS NOT NULL AND c.expires_at < now() THEN 'expired'
			  WHEN c.last_verified_at IS NULL THEN 'unknown'
			  WHEN c.last_verified_at < now() - interval '120 days' THEN 'stale'
			  ELSE 'fresh'
			END AS freshness
		FROM candidates candidate
		JOIN claims c ON c.id = candidate.id
		LEFT JOIN text_matches tm ON tm.id = c.id
		LEFT JOIN vector_matches vm ON vm.id = c.id
		ORDER BY rank_score DESC, c.updated_at DESC
		LIMIT $3
	`, statusFilter, embeddingExpr, queryExpr, statusFilter, embeddingExpr, queryExpr)
}

func hybridRecallDocumentsSQL(dimensions int) string {
	embeddingExpr, queryExpr := vectorComparisonExpr("ch.embedding", "$5", dimensions)
	return fmt.Sprintf(`
		WITH text_matches AS (
		  SELECT
		    ch.id,
		    LEAST(
		      GREATEST(
		        ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		        ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		      ),
		      0.4
		    ) AS text_score
		  FROM chunks ch
		  JOIN documents d ON d.id = ch.document_id
		  WHERE ch.scope = $2
		    AND d.scope = $2
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND (
		      ch.search_vector @@ plainto_tsquery('simple', $1)
		      OR ch.search_vector @@ to_tsquery('simple', $4)
		    )
		  ORDER BY text_score DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		vector_matches AS (
		  SELECT
		    ch.id,
		    GREATEST(0, 1 - (%s <=> %s)) AS vector_score
		  FROM chunks ch
		  JOIN documents d ON d.id = ch.document_id
		  WHERE ch.scope = $2
		    AND d.scope = $2
		    AND d.status NOT IN ('deprecated', 'deleted')
		    AND ch.embedding_dimensions = $6
		  ORDER BY %s <=> %s
		  LIMIT GREATEST($3 * 3, 12)
		),
		candidates AS (
		  SELECT id FROM text_matches
		  UNION
		  SELECT id FROM vector_matches
		)
		SELECT d.id, d.title, d.source_url, ch.content,
		       COALESCE(tm.text_score, 0) AS text_score,
		       COALESCE(vm.vector_score, 0) AS vector_score,
		       COALESCE(tm.text_score, 0)
		         + COALESCE(vm.vector_score, 0) * 0.45 AS rank_score
		FROM candidates candidate
		JOIN chunks ch ON ch.id = candidate.id
		JOIN documents d ON d.id = ch.document_id
		LEFT JOIN text_matches tm ON tm.id = ch.id
		LEFT JOIN vector_matches vm ON vm.id = ch.id
		ORDER BY rank_score DESC, d.ingested_at DESC, ch.chunk_index ASC
		LIMIT $3
	`, embeddingExpr, queryExpr, embeddingExpr, queryExpr)
}

func vectorComparisonExpr(column, parameter string, dimensions int) (string, string) {
	switch dimensions {
	case 768, 1024, 1280, 1536:
		cast := fmt.Sprintf("vector(%d)", dimensions)
		return fmt.Sprintf("%s::%s", column, cast), fmt.Sprintf("%s::%s", parameter, cast)
	default:
		return column, parameter + "::vector"
	}
}

func (s *Store) Sources(ctx context.Context, query, scope string, limit int) ([]DocumentResult, error) {
	if limit < 1 || limit > 20 {
		limit = 5
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.title, d.source_url, ch.content,
		       LEAST(
		         GREATEST(
		           ts_rank_cd(ch.search_vector, plainto_tsquery('simple', $1)),
		           ts_rank_cd(ch.search_vector, to_tsquery('simple', $4)) * 0.65
		         ),
		         0.4
		       ) AS rank_score
		FROM chunks ch
		JOIN documents d ON d.id = ch.document_id
		WHERE ch.scope = $2
		  AND d.scope = $2
		  AND d.status NOT IN ('deprecated', 'deleted')
		  AND (
		    ch.search_vector @@ plainto_tsquery('simple', $1)
		    OR ch.search_vector @@ to_tsquery('simple', $4)
		  )
		ORDER BY rank_score DESC
		LIMIT $3
	`, query, scope, limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []DocumentResult
	for rows.Next() {
		var doc DocumentResult
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.Source, &doc.Content, &doc.Rank); err != nil {
			return nil, err
		}
		doc.TextScore = doc.Rank
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

func fullTextAnyQuery(query string) string {
	seen := map[string]struct{}{}
	terms := []string{}
	for _, term := range fullTextTermPattern.FindAllString(strings.ToLower(query), -1) {
		if len(term) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
		if len(terms) >= 16 {
			break
		}
	}
	if len(terms) == 0 {
		return "__abra_no_match__"
	}
	return strings.Join(terms, " | ")
}

func (s *Store) ListSourceConfigs(ctx context.Context, scope string, limit int) ([]SourceConfigRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id, scope, source_type, name, COALESCE(base_url, ''), connector_kind,
		  status, authority, authority_score, config, metadata,
		  last_success_at::text, last_error_at::text, last_error, COALESCE(created_by, '')
		FROM source_configs
		WHERE ($1 = '' OR scope = $1)
		ORDER BY priority ASC, updated_at DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := []SourceConfigRecord{}
	for rows.Next() {
		var source SourceConfigRecord
		var configRaw, metadataRaw []byte
		if err := rows.Scan(
			&source.ID,
			&source.Scope,
			&source.SourceType,
			&source.Name,
			&source.BaseURL,
			&source.ConnectorKind,
			&source.Status,
			&source.Authority,
			&source.AuthorityScore,
			&configRaw,
			&metadataRaw,
			&source.LastSuccessAt,
			&source.LastErrorAt,
			&source.LastError,
			&source.CreatedBy,
		); err != nil {
			return nil, err
		}
		source.Config = decodeJSONMap(configRaw)
		source.Metadata = decodeJSONMap(metadataRaw)
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *Store) ListScopes(ctx context.Context, limit int) ([]ScopeSummary, error) {
	if limit < 1 {
		limit = 50
	}
	if limit > maxListScopesLimit {
		limit = maxListScopesLimit
	}
	rows, err := s.pool.Query(ctx, `
		WITH known_scopes AS (
		  SELECT scope FROM documents
		  UNION
		  SELECT scope FROM claims
		  UNION
		  SELECT scope FROM observations
		  UNION
		  SELECT scope FROM memory_summaries
		  UNION
		  SELECT scope FROM entities
		  UNION
		  SELECT scope FROM relations
		  UNION
		  SELECT scope FROM conflicts
		  UNION
		  SELECT scope FROM source_configs
		  UNION
		  SELECT scope FROM ingestion_jobs
		)
		SELECT
		  known_scopes.scope,
		  (SELECT COUNT(*) FROM documents WHERE documents.scope = known_scopes.scope) AS documents,
		  (SELECT COUNT(*) FROM claims WHERE claims.scope = known_scopes.scope) AS claims,
		  (SELECT COUNT(*) FROM observations WHERE observations.scope = known_scopes.scope) AS observations,
		  (SELECT COUNT(*) FROM memory_summaries WHERE memory_summaries.scope = known_scopes.scope) AS summaries,
		  (SELECT COUNT(*) FROM entities WHERE entities.scope = known_scopes.scope) AS entities,
		  (SELECT COUNT(*) FROM relations WHERE relations.scope = known_scopes.scope) AS relations,
		  (SELECT COUNT(*) FROM conflicts WHERE conflicts.scope = known_scopes.scope) AS conflicts,
		  (SELECT COUNT(*) FROM source_configs WHERE source_configs.scope = known_scopes.scope) AS sources,
		  (SELECT COUNT(*) FROM ingestion_jobs WHERE ingestion_jobs.scope = known_scopes.scope) AS jobs
		FROM known_scopes
		WHERE TRIM(known_scopes.scope) <> ''
		ORDER BY documents DESC, claims DESC, observations DESC, summaries DESC, relations DESC, entities DESC, conflicts DESC, sources DESC, jobs DESC, known_scopes.scope ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scopes := []ScopeSummary{}
	for rows.Next() {
		var scope ScopeSummary
		if err := rows.Scan(&scope.Scope, &scope.Documents, &scope.Claims, &scope.Observations, &scope.Summaries, &scope.Entities, &scope.Relations, &scope.Conflicts, &scope.Sources, &scope.Jobs); err != nil {
			return nil, err
		}
		scopes = append(scopes, scope)
	}
	return scopes, rows.Err()
}

func (s *Store) GetSourceConfig(ctx context.Context, id string) (SourceConfigRecord, error) {
	var source SourceConfigRecord
	var configRaw, metadataRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT
		  id, scope, source_type, name, COALESCE(base_url, ''), connector_kind,
		  status, authority, authority_score, config, metadata,
		  last_success_at::text, last_error_at::text, last_error, COALESCE(created_by, '')
		FROM source_configs
		WHERE id = $1
	`, strings.TrimSpace(id)).Scan(
		&source.ID,
		&source.Scope,
		&source.SourceType,
		&source.Name,
		&source.BaseURL,
		&source.ConnectorKind,
		&source.Status,
		&source.Authority,
		&source.AuthorityScore,
		&configRaw,
		&metadataRaw,
		&source.LastSuccessAt,
		&source.LastErrorAt,
		&source.LastError,
		&source.CreatedBy,
	)
	if err == pgx.ErrNoRows {
		return SourceConfigRecord{}, fmt.Errorf("source_config_id %q not found", strings.TrimSpace(id))
	}
	if err != nil {
		return SourceConfigRecord{}, err
	}
	source.Config = decodeJSONMap(configRaw)
	source.Metadata = decodeJSONMap(metadataRaw)
	return source, nil
}

func (s *Store) ListIngestionJobs(ctx context.Context, scope, sourceConfigID string, limit int) ([]IngestionJobRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  id,
		  COALESCE(source_config_id, ''),
		  scope,
		  source_type,
		  COALESCE(source_url, ''),
		  trigger_type,
		  status,
		  authority,
		  COALESCE(lease_owner, ''),
		  heartbeat_at::text,
		  started_at::text,
		  finished_at::text,
		  attempts,
		  max_attempts,
		  documents_seen,
		  documents_changed,
		  chunks_written,
		  claims_written,
		  error_message,
		  COALESCE(created_by, ''),
		  created_at::text,
		  updated_at::text,
		  metadata
		FROM ingestion_jobs
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR source_config_id = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, scope, sourceConfigID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []IngestionJobRecord{}
	for rows.Next() {
		var job IngestionJobRecord
		var metadataRaw []byte
		if err := rows.Scan(
			&job.ID,
			&job.SourceConfigID,
			&job.Scope,
			&job.SourceType,
			&job.SourceURL,
			&job.TriggerType,
			&job.Status,
			&job.Authority,
			&job.LeaseOwner,
			&job.HeartbeatAt,
			&job.StartedAt,
			&job.FinishedAt,
			&job.Attempts,
			&job.MaxAttempts,
			&job.DocumentsSeen,
			&job.DocumentsChanged,
			&job.ChunksWritten,
			&job.ClaimsWritten,
			&job.ErrorMessage,
			&job.CreatedBy,
			&job.CreatedAt,
			&job.UpdatedAt,
			&metadataRaw,
		); err != nil {
			return nil, err
		}
		job.Metadata = decodeJSONMap(metadataRaw)
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *Store) ListGraphEntities(ctx context.Context, scope string, limit int) ([]GraphEntityResult, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, canonical_name, entity_type, status, confidence, source_url, updated_at::text
		FROM entities
		WHERE ($1 = '' OR scope = $1)
		ORDER BY updated_at DESC, confidence DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := []GraphEntityResult{}
	for rows.Next() {
		var entity GraphEntityResult
		if err := rows.Scan(&entity.ID, &entity.Name, &entity.Type, &entity.Status, &entity.Confidence, &entity.SourceURL, &entity.UpdatedAt); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

func (s *Store) ListGraphRelations(ctx context.Context, scope string, limit int) ([]GraphRelationResult, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  r.id,
		  src.id,
		  src.canonical_name,
		  dst.id,
		  dst.canonical_name,
		  r.relation_type,
		  r.status,
		  r.confidence,
		  r.source_url,
		  r.updated_at::text
		FROM relations r
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		WHERE ($1 = '' OR r.scope = $1)
		ORDER BY r.updated_at DESC, r.confidence DESC
		LIMIT $2
	`, scope, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []GraphRelationResult{}
	for rows.Next() {
		var relation GraphRelationResult
		if err := rows.Scan(&relation.ID, &relation.FromID, &relation.FromEntity, &relation.ToID, &relation.ToEntity, &relation.Type, &relation.Status, &relation.Confidence, &relation.SourceURL, &relation.UpdatedAt); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (s *Store) ListActiveRelationsFromEntity(ctx context.Context, scope, sourceEntityID string, limit int) ([]GraphRelationResult, error) {
	scope = strings.TrimSpace(scope)
	sourceEntityID = strings.TrimSpace(sourceEntityID)
	if scope == "" || sourceEntityID == "" {
		return nil, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  r.id,
		  src.id,
		  src.canonical_name,
		  dst.id,
		  dst.canonical_name,
		  r.relation_type,
		  r.status,
		  r.confidence,
		  r.source_url,
		  r.updated_at::text
		FROM relations r
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		WHERE r.scope = $1
		  AND r.source_entity_id = $2
		  AND r.status NOT IN ('deprecated', 'expired')
		  AND src.status NOT IN ('deprecated', 'deleted')
		  AND dst.status NOT IN ('deprecated', 'deleted')
		ORDER BY r.confidence DESC, r.updated_at DESC
		LIMIT $3
	`, scope, sourceEntityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []GraphRelationResult{}
	for rows.Next() {
		var relation GraphRelationResult
		if err := rows.Scan(&relation.ID, &relation.FromID, &relation.FromEntity, &relation.ToID, &relation.ToEntity, &relation.Type, &relation.Status, &relation.Confidence, &relation.SourceURL, &relation.UpdatedAt); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func (s *Store) RelatedGraph(ctx context.Context, query, scope string, limit int) ([]RelationResult, error) {
	if limit < 1 || limit > 50 {
		limit = 8
	}
	anyQuery := fullTextAnyQuery(query)
	rows, err := s.pool.Query(ctx, `
		WITH seed_entities AS (
		  SELECT e.id
		  FROM entities e
		  LEFT JOIN entity_aliases ea
		    ON ea.entity_id = e.id
		   AND ea.scope = e.scope
		   AND ea.status NOT IN ('deprecated', 'deleted')
		  WHERE e.scope = $2
		    AND e.status NOT IN ('deprecated', 'deleted')
		    AND (
		      e.search_vector @@ plainto_tsquery('simple', $1)
		      OR e.search_vector @@ to_tsquery('simple', $4)
		      OR e.canonical_name ILIKE '%' || $1 || '%'
		      OR ea.alias ILIKE '%' || $1 || '%'
		    )
		  GROUP BY e.id, e.confidence, e.updated_at
		  ORDER BY e.confidence DESC, e.updated_at DESC
		  LIMIT GREATEST($3 * 3, 12)
		),
		seed_edges AS (
		  SELECT r.id, 1 AS distance, 1.0::double precision AS seed_score
		  FROM relations r
		  JOIN entities src ON src.id = r.source_entity_id
		  JOIN entities dst ON dst.id = r.target_entity_id
		  WHERE r.scope = $2
		    AND r.status NOT IN ('deprecated', 'expired')
		    AND src.status NOT IN ('deprecated', 'deleted')
		    AND dst.status NOT IN ('deprecated', 'deleted')
		    AND (
		      r.source_entity_id IN (SELECT id FROM seed_entities)
		      OR r.target_entity_id IN (SELECT id FROM seed_entities)
		      OR src.search_vector @@ plainto_tsquery('simple', $1)
		      OR src.search_vector @@ to_tsquery('simple', $4)
		      OR dst.search_vector @@ plainto_tsquery('simple', $1)
		      OR dst.search_vector @@ to_tsquery('simple', $4)
		      OR r.relation_type ILIKE '%' || $1 || '%'
		    )
		  ORDER BY r.confidence DESC, r.updated_at DESC
		  LIMIT GREATEST($3 * 4, 16)
		),
		frontier_entities AS (
		  SELECT id FROM seed_entities
		  UNION
		  SELECT r.source_entity_id
		  FROM relations r
		  JOIN seed_edges se ON se.id = r.id
		  UNION
		  SELECT r.target_entity_id
		  FROM relations r
		  JOIN seed_edges se ON se.id = r.id
		),
		neighbor_edges AS (
		  SELECT r.id, 2 AS distance, 0.65::double precision AS seed_score
		  FROM relations r
		  JOIN entities src ON src.id = r.source_entity_id
		  JOIN entities dst ON dst.id = r.target_entity_id
		  WHERE r.scope = $2
		    AND r.status NOT IN ('deprecated', 'expired')
		    AND src.status NOT IN ('deprecated', 'deleted')
		    AND dst.status NOT IN ('deprecated', 'deleted')
		    AND (
		      r.source_entity_id IN (SELECT id FROM frontier_entities)
		      OR r.target_entity_id IN (SELECT id FROM frontier_entities)
		    )
		  ORDER BY r.confidence DESC, r.updated_at DESC
		  LIMIT GREATEST($3 * 5, 20)
		),
		ranked_edges AS (
		  SELECT id, MIN(distance) AS distance, MAX(seed_score) AS seed_score
		  FROM (
		    SELECT * FROM seed_edges
		    UNION ALL
		    SELECT * FROM neighbor_edges
		  ) edges
		  GROUP BY id
		)
		SELECT r.id, src.canonical_name, dst.canonical_name, r.relation_type, r.confidence, r.source_url
		FROM ranked_edges ranked
		JOIN relations r ON r.id = ranked.id
		JOIN entities src ON src.id = r.source_entity_id
		JOIN entities dst ON dst.id = r.target_entity_id
		ORDER BY
		  ranked.distance ASC,
		  (r.confidence * ranked.seed_score) DESC,
		  r.updated_at DESC
		LIMIT $3
	`, query, scope, limit, anyQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []RelationResult
	for rows.Next() {
		var relation RelationResult
		if err := rows.Scan(&relation.ID, &relation.FromEntity, &relation.ToEntity, &relation.Type, &relation.Confidence, &relation.SourceURL); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func decodeJSONStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}
