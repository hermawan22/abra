package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type LearningProposalRecord struct {
	ID           string         `json:"id"`
	Scope        string         `json:"scope"`
	ProposalType string         `json:"proposal_type"`
	Title        string         `json:"title"`
	Rationale    string         `json:"rationale"`
	Status       string         `json:"status"`
	TargetType   string         `json:"target_type,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	SourceURL    string         `json:"source_url,omitempty"`
	Confidence   float64        `json:"confidence"`
	Payload      map[string]any `json:"payload"`
	CreatedBy    string         `json:"created_by,omitempty"`
	ReviewedBy   string         `json:"reviewed_by,omitempty"`
	ReviewReason string         `json:"review_reason,omitempty"`
	ApprovalID   string         `json:"approval_id,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	ReviewedAt   *string        `json:"reviewed_at,omitempty"`
}

type CreateLearningProposalInput struct {
	Scope        string         `json:"scope"`
	ProposalType string         `json:"proposal_type"`
	Title        string         `json:"title"`
	Rationale    string         `json:"rationale"`
	TargetType   string         `json:"target_type"`
	TargetID     string         `json:"target_id"`
	SourceURL    string         `json:"source_url"`
	Confidence   float64        `json:"confidence"`
	Payload      map[string]any `json:"payload"`
	CreatedBy    string         `json:"created_by"`
	ApprovalID   string         `json:"approval_id"`
}

type DecideLearningProposalInput struct {
	Status       string         `json:"status"`
	ReviewedBy   string         `json:"reviewed_by"`
	ReviewReason string         `json:"review_reason"`
	ApprovalID   string         `json:"approval_id"`
	Metadata     map[string]any `json:"metadata"`
}

type ApplyLearningProposalInput struct {
	AppliedBy  string         `json:"applied_by"`
	ApprovalID string         `json:"approval_id"`
	Metadata   map[string]any `json:"metadata"`
}

func (s *Store) CreateLearningProposal(ctx context.Context, input CreateLearningProposalInput) (LearningProposalRecord, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.ProposalType = normalizedLearningProposalType(input.ProposalType)
	input.Title = strings.TrimSpace(input.Title)
	input.Rationale = strings.TrimSpace(input.Rationale)
	if input.Scope == "" || input.ProposalType == "" || input.Title == "" || input.Rationale == "" {
		return LearningProposalRecord{}, fmt.Errorf("scope, proposal_type, title, and rationale are required")
	}
	if input.Confidence <= 0 {
		input.Confidence = 0.5
	}
	if input.Confidence > 1 {
		input.Confidence = 1
	}
	id := stableID("learning-proposal", input.Scope, input.ProposalType, input.Title, input.Rationale, time.Now().UTC().Format(time.RFC3339Nano))
	_, err := s.pool.Exec(ctx, `
		INSERT INTO learning_proposals (
		  id, scope, proposal_type, title, rationale, status,
		  target_type, target_id, source_url, confidence, payload, created_by, approval_id
		)
		VALUES (
		  $1, $2, $3, $4, $5, 'pending',
		  NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), $9, $10::jsonb, NULLIF($11, ''), NULLIF($12, '')
		)
	`, id, input.Scope, input.ProposalType, input.Title, input.Rationale, strings.TrimSpace(input.TargetType), strings.TrimSpace(input.TargetID), strings.TrimSpace(input.SourceURL), input.Confidence, jsonb(input.Payload), strings.TrimSpace(input.CreatedBy), strings.TrimSpace(input.ApprovalID))
	if err != nil {
		return LearningProposalRecord{}, err
	}
	return s.GetLearningProposal(ctx, id)
}

func (s *Store) CreateLearningProposalOnce(ctx context.Context, input CreateLearningProposalInput) (LearningProposalRecord, bool, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.ProposalType = normalizedLearningProposalType(input.ProposalType)
	input.Title = strings.TrimSpace(input.Title)
	input.Rationale = strings.TrimSpace(input.Rationale)
	if input.Scope == "" || input.ProposalType == "" || input.Title == "" || input.Rationale == "" {
		return LearningProposalRecord{}, false, fmt.Errorf("scope, proposal_type, title, and rationale are required")
	}
	existing, err := s.getPendingLearningProposal(ctx, input)
	if err == nil {
		return existing, false, nil
	}
	if err != pgx.ErrNoRows {
		return LearningProposalRecord{}, false, err
	}
	created, err := s.CreateLearningProposal(ctx, input)
	if err != nil {
		existing, findErr := s.getPendingLearningProposal(ctx, input)
		if findErr == nil {
			return existing, false, nil
		}
		return LearningProposalRecord{}, false, err
	}
	return created, true, nil
}

func (s *Store) getPendingLearningProposal(ctx context.Context, input CreateLearningProposalInput) (LearningProposalRecord, error) {
	rows, err := s.pool.Query(ctx, pendingLearningProposalSelectSQL(),
		input.Scope,
		input.ProposalType,
		input.Title,
		strings.TrimSpace(input.TargetType),
		strings.TrimSpace(input.TargetID),
		strings.TrimSpace(input.SourceURL),
	)
	if err != nil {
		return LearningProposalRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return LearningProposalRecord{}, err
		}
		return LearningProposalRecord{}, pgx.ErrNoRows
	}
	return scanLearningProposal(rows)
}

func (s *Store) ListLearningProposals(ctx context.Context, scope, status string, limit int) ([]LearningProposalRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, learningProposalSelectSQL()+`
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, strings.TrimSpace(scope), strings.TrimSpace(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LearningProposalRecord{}
	for rows.Next() {
		record, err := scanLearningProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

func (s *Store) GetLearningProposal(ctx context.Context, id string) (LearningProposalRecord, error) {
	rows, err := s.pool.Query(ctx, learningProposalSelectSQL()+" WHERE id = $1", strings.TrimSpace(id))
	if err != nil {
		return LearningProposalRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return LearningProposalRecord{}, err
		}
		return LearningProposalRecord{}, fmt.Errorf("learning proposal %q not found", id)
	}
	return scanLearningProposal(rows)
}

func (s *Store) DecideLearningProposal(ctx context.Context, id string, input DecideLearningProposalInput) (LearningProposalRecord, error) {
	status := normalizedLearningProposalStatus(input.Status)
	if status == "" || status == "pending" {
		return LearningProposalRecord{}, fmt.Errorf("status must be accepted, rejected, applied, or canceled")
	}
	payload := mergeMetadata(input.Metadata, map[string]any{
		"decided_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	tag, err := s.pool.Exec(ctx, `
		UPDATE learning_proposals
		SET status = $2,
		    reviewed_by = NULLIF($3, ''),
		    review_reason = NULLIF($4, ''),
		    approval_id = COALESCE(NULLIF($5, ''), approval_id),
		    payload = payload || $6::jsonb,
		    reviewed_at = now(),
		    updated_at = now()
		WHERE id = $1
		  AND status = 'pending'
	`, strings.TrimSpace(id), status, strings.TrimSpace(input.ReviewedBy), strings.TrimSpace(input.ReviewReason), strings.TrimSpace(input.ApprovalID), jsonb(payload))
	if err != nil {
		return LearningProposalRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.GetLearningProposal(ctx, id)
		if getErr != nil {
			return LearningProposalRecord{}, getErr
		}
		return LearningProposalRecord{}, fmt.Errorf("learning proposal %q is %s and cannot be decided", id, current.Status)
	}
	return s.GetLearningProposal(ctx, id)
}

func (s *Store) MarkLearningProposalApplied(ctx context.Context, id string, input ApplyLearningProposalInput) (LearningProposalRecord, error) {
	payload := mergeMetadata(input.Metadata, map[string]any{
		"applied_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	tag, err := s.pool.Exec(ctx, `
		UPDATE learning_proposals
		SET status = 'applied',
		    reviewed_by = COALESCE(NULLIF($2, ''), reviewed_by),
		    approval_id = COALESCE(NULLIF($3, ''), approval_id),
		    payload = payload || $4::jsonb,
		    reviewed_at = COALESCE(reviewed_at, now()),
		    updated_at = now()
		WHERE id = $1
		  AND status = 'accepted'
	`, strings.TrimSpace(id), strings.TrimSpace(input.AppliedBy), strings.TrimSpace(input.ApprovalID), jsonb(payload))
	if err != nil {
		return LearningProposalRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.GetLearningProposal(ctx, id)
		if getErr != nil {
			return LearningProposalRecord{}, getErr
		}
		return LearningProposalRecord{}, fmt.Errorf("learning proposal %q is %s and cannot be applied", id, current.Status)
	}
	return s.GetLearningProposal(ctx, id)
}

func learningProposalSelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  proposal_type,
		  title,
		  rationale,
		  status,
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  COALESCE(source_url, ''),
		  confidence,
		  payload,
		  COALESCE(created_by, ''),
		  COALESCE(reviewed_by, ''),
		  COALESCE(review_reason, ''),
		  COALESCE(approval_id, ''),
		  created_at::text,
		  updated_at::text,
		  reviewed_at::text
		FROM learning_proposals
	`
}

func pendingLearningProposalSelectSQL() string {
	return learningProposalSelectSQL() + `
		WHERE scope = $1
		  AND proposal_type = $2
		  AND title = $3
		  AND status = 'pending'
		  AND COALESCE(target_type, '') = $4
		  AND COALESCE(target_id, '') = $5
		  AND COALESCE(source_url, '') = $6
		ORDER BY created_at DESC
		LIMIT 1
	`
}

type learningProposalScanner interface {
	Scan(dest ...any) error
}

func scanLearningProposal(row learningProposalScanner) (LearningProposalRecord, error) {
	var record LearningProposalRecord
	var payloadRaw []byte
	if err := row.Scan(
		&record.ID,
		&record.Scope,
		&record.ProposalType,
		&record.Title,
		&record.Rationale,
		&record.Status,
		&record.TargetType,
		&record.TargetID,
		&record.SourceURL,
		&record.Confidence,
		&payloadRaw,
		&record.CreatedBy,
		&record.ReviewedBy,
		&record.ReviewReason,
		&record.ApprovalID,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.ReviewedAt,
	); err != nil {
		return LearningProposalRecord{}, err
	}
	record.Payload = decodeJSONMap(payloadRaw)
	return record, nil
}

func normalizedLearningProposalType(value string) string {
	switch strings.TrimSpace(value) {
	case "claim", "challenge", "source_refresh", "summary_rebuild", "ingestion", "policy", "graph", "other":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func normalizedLearningProposalStatus(value string) string {
	switch strings.TrimSpace(value) {
	case "pending", "accepted", "rejected", "applied", "canceled":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
