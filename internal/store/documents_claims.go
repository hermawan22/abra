package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

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
	record.FreshnessStatus = normalizeFreshnessStatus(firstNonEmptyStoreString(record.FreshnessStatus, metadataString(record.Metadata, "freshness"), metadataString(record.Metadata, "freshness_status")))
	metadata := jsonb(record.Metadata)
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO documents (
		  id, source_type, source_url, source_id, title, scope, content_checksum,
		  source_updated_at, source_config_id, ingestion_job_id, authority,
		  authority_score, freshness_status, metadata
		)
		VALUES (
		  $1, $2, $3, NULLIF($4, ''), $5, $6, $7, NULLIF($8, '')::timestamptz,
		  NULLIF($9, ''), NULLIF($10, ''), $11, $12, $13, $14::jsonb
		)
		ON CONFLICT (source_type, source_url, scope)
		DO UPDATE SET
		  source_id = EXCLUDED.source_id,
		  title = EXCLUDED.title,
		  content_checksum = EXCLUDED.content_checksum,
		  source_updated_at = EXCLUDED.source_updated_at,
		  source_config_id = EXCLUDED.source_config_id,
		  ingestion_job_id = EXCLUDED.ingestion_job_id,
		  status = 'active',
		  authority = EXCLUDED.authority,
		  authority_score = EXCLUDED.authority_score,
		  freshness_status = EXCLUDED.freshness_status,
		  ingested_at = now(),
		  updated_at = now(),
		  metadata = EXCLUDED.metadata
	`, id, record.SourceType, record.SourceURL, record.SourceID, record.Title, record.Scope, record.ContentChecksum, record.SourceUpdatedAt, record.SourceConfigID, record.IngestionJobID, record.Authority, record.AuthorityScore, record.FreshnessStatus, metadata)
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
	claim.ValidFrom = strings.TrimSpace(claim.ValidFrom)
	claim.ExpiresAt = strings.TrimSpace(claim.ExpiresAt)
	claim.SupersedesClaimID = strings.TrimSpace(claim.SupersedesClaimID)
	if claim.SupersedesClaimID == id {
		return "", fmt.Errorf("claim cannot supersede itself")
	}
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
		  valid_from, expires_at, supersedes_claim_id,
		  source_config_id, ingestion_job_id, authority_score
		)
		VALUES (
		  $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8,
		  $9::vector, now(), $10::jsonb, $11, $12, $13,
		  NULLIF($14, '')::timestamptz, NULLIF($15, '')::timestamptz, NULLIF($16, ''),
		  NULLIF($17, ''), NULLIF($18, ''), $19
		)
		ON CONFLICT DO NOTHING
	`, id, claim.ClaimText, claim.Scope, claim.SourceURL, claim.SourceType, claim.Authority, claim.Status, claim.Confidence, vectorLiteral(claim.Embedding), jsonb(claim.Metadata), claim.EmbeddingProvider, claim.EmbeddingModel, claim.EmbeddingDimensions, claim.ValidFrom, claim.ExpiresAt, claim.SupersedesClaimID, claim.SourceConfigID, claim.IngestionJobID, claim.AuthorityScore)
	if err != nil {
		return "", err
	}
	_, err = s.queryRunner().Exec(ctx, `
		UPDATE claims
		SET valid_from = COALESCE(NULLIF($4, '')::timestamptz, valid_from),
		    expires_at = COALESCE(NULLIF($5, '')::timestamptz, expires_at),
		    supersedes_claim_id = COALESCE(NULLIF($6, ''), supersedes_claim_id),
		    source_config_id = COALESCE(NULLIF($7, ''), source_config_id),
		    ingestion_job_id = COALESCE(NULLIF($8, ''), ingestion_job_id),
		    authority = CASE
		      WHEN authority = 'manual-unverified' AND NULLIF($9, '') IS NOT NULL THEN $9
		      ELSE authority
		    END,
		    authority_score = GREATEST(authority_score, $10),
		    status = CASE
		      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true') THEN $12
		      ELSE status
		    END,
		    confidence = CASE
		      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true') THEN GREATEST(confidence, $13)
		      ELSE confidence
		    END,
		    last_verified_at = CASE
		      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true') THEN now()
		      ELSE last_verified_at
		    END,
		    metadata = CASE
		      WHEN status = 'deprecated' AND (metadata->>'source_refresh_deprecated' = 'true' OR metadata->>'source_sync_deleted' = 'true')
		        THEN (metadata - 'source_refresh_deprecated' - 'source_refresh_deprecated_at' - 'source_refresh_job_id' - 'source_sync_deleted' - 'source_sync_deleted_at') || $11::jsonb || jsonb_build_object('source_refresh_reactivated_at', now()::text)
		      ELSE metadata || $11::jsonb
		    END
		WHERE scope = $1
		  AND COALESCE(source_url, '') = COALESCE(NULLIF($2, ''), '')
		  AND claim_text = $3
	`, claim.Scope, claim.SourceURL, claim.ClaimText, claim.ValidFrom, claim.ExpiresAt, claim.SupersedesClaimID, claim.SourceConfigID, claim.IngestionJobID, claim.Authority, claim.AuthorityScore, jsonb(claim.Metadata), claim.Status, claim.Confidence)
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
	id := stableID("evidence", evidence.ClaimID, evidence.DocumentID, evidence.Quote, fmt.Sprint(evidence.StartChar), fmt.Sprint(evidence.EndChar))
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO evidence (id, claim_id, document_id, quote, start_char, end_char, source_url, source_type)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, NULLIF($8, ''))
		ON CONFLICT DO NOTHING
	`, id, evidence.ClaimID, evidence.DocumentID, evidence.Quote, evidence.StartChar, evidence.EndChar, evidence.SourceURL, evidence.SourceType)
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

func (s *Store) ClaimScope(ctx context.Context, claimID string) (string, error) {
	var scope string
	err := s.pool.QueryRow(ctx, "SELECT scope FROM claims WHERE id = $1", strings.TrimSpace(claimID)).Scan(&scope)
	if err == pgx.ErrNoRows {
		return "", fmt.Errorf("claim %q not found", claimID)
	}
	return scope, err
}

func normalizeFeedback(feedback FeedbackRecord) FeedbackRecord {
	if feedback.Verdict == "" {
		feedback.Verdict = "incorrect"
	}
	return feedback
}

func feedbackID(feedback FeedbackRecord) string {
	feedback = normalizeFeedback(feedback)
	return stableID("feedback", feedback.ClaimID, feedback.Verdict, feedback.Reason, feedback.CreatedBy)
}
