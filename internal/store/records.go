package store

import "time"

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
	FreshnessStatus string
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
	ValidFrom           string
	ExpiresAt           string
	SupersedesClaimID   string
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
	StartChar  int
	EndChar    int
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
	ID              string         `json:"id"`
	Scope           string         `json:"scope"`
	SourceType      string         `json:"source_type"`
	Name            string         `json:"name"`
	BaseURL         string         `json:"base_url,omitempty"`
	ConnectorKind   string         `json:"connector_kind"`
	Status          string         `json:"status"`
	Authority       string         `json:"authority"`
	AuthorityScore  float64        `json:"authority_score"`
	FreshnessPolicy map[string]any `json:"freshness_policy"`
	ScheduleCron    string         `json:"schedule_cron,omitempty"`
	Config          map[string]any `json:"config"`
	Metadata        map[string]any `json:"metadata"`
	LastSuccessAt   *string        `json:"last_success_at,omitempty"`
	LastErrorAt     *string        `json:"last_error_at,omitempty"`
	LastError       *string        `json:"last_error,omitempty"`
	CreatedBy       string         `json:"created_by,omitempty"`
	ApprovalID      string         `json:"approval_id,omitempty"`
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
	Scope        string                     `json:"scope,omitempty"`
	Status       string                     `json:"status"`
	Score        int                        `json:"score"`
	CheckedAt    string                     `json:"checked_at"`
	Reasons      []string                   `json:"reasons"`
	Signals      []MemoryHealthSignal       `json:"signals"`
	Documents    MemoryHealthDocument       `json:"documents"`
	Claims       MemoryHealthClaim          `json:"claims"`
	Graph        MemoryHealthGraph          `json:"graph"`
	Summaries    MemoryHealthSummary        `json:"summaries"`
	Sources      MemoryHealthSource         `json:"sources"`
	Ingestion    MemoryHealthIngestion      `json:"ingestion"`
	Conflicts    MemoryHealthConflict       `json:"conflicts"`
	Learning     MemoryHealthLearning       `json:"learning"`
	Approvals    MemoryHealthApproval       `json:"approvals"`
	SourceHealth []MemoryHealthSourceDetail `json:"source_health,omitempty"`
	LastUpdated  map[string]string          `json:"last_updated,omitempty"`
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
	Due      int `json:"due"`
	Overdue  int `json:"overdue"`
}

type MemoryHealthSourceDetail struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Type             string  `json:"type"`
	Status           string  `json:"status"`
	LastSuccessAt    *string `json:"last_success_at,omitempty"`
	LastErrorAt      *string `json:"last_error_at,omitempty"`
	LastError        string  `json:"last_error,omitempty"`
	Due              bool    `json:"due"`
	Overdue          bool    `json:"overdue"`
	RetryJobs        int     `json:"retry_jobs"`
	FailedJobs       int     `json:"failed_jobs"`
	RunningJobs      int     `json:"running_jobs"`
	QueuedJobs       int     `json:"queued_jobs"`
	StaleRunningJobs int     `json:"stale_running_jobs,omitempty"`
	LatestJobID      string  `json:"latest_job_id,omitempty"`
	LatestJobStatus  string  `json:"latest_job_status,omitempty"`
	LatestJobUpdated string  `json:"latest_job_updated,omitempty"`
	RemediationHint  string  `json:"remediation_hint"`
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

type RecallResult struct {
	Claims              []ClaimResult      `json:"claims"`
	SupportingDocuments []DocumentResult   `json:"supporting_documents"`
	GraphContext        []RelationResult   `json:"graph_context,omitempty"`
	RetrievalMode       string             `json:"retrieval_mode,omitempty"`
	RetrievalReasons    []RetrievalReason  `json:"retrieval_reasons,omitempty"`
	RetrievalWarnings   []RetrievalWarning `json:"retrieval_warnings,omitempty"`
}

type RecallOptions struct {
	AsOf              time.Time `json:"as_of,omitempty"`
	IncludeHistorical bool      `json:"include_historical,omitempty"`
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

type EvidenceAnchorCandidate struct {
	ClaimID        string `json:"claim_id"`
	Claim          string `json:"claim_text"`
	Scope          string `json:"scope"`
	Status         string `json:"status"`
	SourceURL      string `json:"source_url,omitempty"`
	SourceType     string `json:"source_type,omitempty"`
	DocumentID     string `json:"document_id,omitempty"`
	DocumentTitle  string `json:"document_title,omitempty"`
	DocumentChunk  string `json:"document_chunk,omitempty"`
	Freshness      string `json:"freshness,omitempty"`
	LastVerifiedAt string `json:"last_verified_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type EvidenceAnchorResult struct {
	ClaimID       string `json:"claim_id"`
	DocumentID    string `json:"document_id,omitempty"`
	Quote         string `json:"quote"`
	StartChar     int    `json:"start_char,omitempty"`
	EndChar       int    `json:"end_char,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	SourceType    string `json:"source_type,omitempty"`
	DocumentTitle string `json:"document_title,omitempty"`
}

type RelationResult struct {
	ID         string  `json:"id,omitempty"`
	ClaimID    string  `json:"claim_id,omitempty"`
	FromEntity string  `json:"from_entity"`
	ToEntity   string  `json:"to_entity"`
	Type       string  `json:"relation_type"`
	Status     string  `json:"status,omitempty"`
	Freshness  string  `json:"freshness,omitempty"`
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

type EntityResolutionResult struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Status     string   `json:"status"`
	Confidence float64  `json:"confidence"`
	Aliases    []string `json:"aliases,omitempty"`
	SourceURL  *string  `json:"source_url,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
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
