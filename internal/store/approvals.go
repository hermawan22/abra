package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type ApprovalRequestRecord struct {
	ID             string         `json:"id"`
	Action         string         `json:"action"`
	Scope          string         `json:"scope"`
	TargetType     string         `json:"target_type,omitempty"`
	TargetID       string         `json:"target_id,omitempty"`
	Status         string         `json:"status"`
	RequestedBy    string         `json:"requested_by,omitempty"`
	DecidedBy      string         `json:"decided_by,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	DecisionReason string         `json:"decision_reason,omitempty"`
	Payload        map[string]any `json:"payload"`
	Metadata       map[string]any `json:"metadata"`
	ExpiresAt      *string        `json:"expires_at,omitempty"`
	DecidedAt      *string        `json:"decided_at,omitempty"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
}

type CreateApprovalRequestInput struct {
	Action      string         `json:"action"`
	Scope       string         `json:"scope"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	RequestedBy string         `json:"requested_by"`
	Reason      string         `json:"reason"`
	Payload     map[string]any `json:"payload"`
	Metadata    map[string]any `json:"metadata"`
	ExpiresAt   string         `json:"expires_at"`
}

type DecideApprovalRequestInput struct {
	DecidedBy      string         `json:"decided_by"`
	DecisionReason string         `json:"decision_reason"`
	Metadata       map[string]any `json:"metadata"`
}

func (s *Store) CreateApprovalRequest(ctx context.Context, input CreateApprovalRequestInput) (ApprovalRequestRecord, error) {
	input.Action = normalizedApprovalAction(input.Action)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Action == "" {
		return ApprovalRequestRecord{}, fmt.Errorf("unsupported approval action")
	}
	if input.Scope == "" {
		return ApprovalRequestRecord{}, fmt.Errorf("scope is required")
	}
	id := stableID("approval", input.Scope, input.Action, input.TargetType, input.TargetID, time.Now().UTC().Format(time.RFC3339Nano))
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"requested_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	_, err := s.pool.Exec(ctx, `
		INSERT INTO approval_requests (
		  id, action, scope, target_type, target_id, status,
		  requested_by, reason, payload, metadata, expires_at
		)
		VALUES (
		  $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), 'pending',
		  NULLIF($6, ''), NULLIF($7, ''), $8::jsonb, $9::jsonb, NULLIF($10, '')::timestamptz
		)
	`, id, input.Action, input.Scope, strings.TrimSpace(input.TargetType), strings.TrimSpace(input.TargetID), strings.TrimSpace(input.RequestedBy), strings.TrimSpace(input.Reason), jsonb(input.Payload), jsonb(metadata), strings.TrimSpace(input.ExpiresAt))
	if err != nil {
		return ApprovalRequestRecord{}, err
	}
	return s.GetApprovalRequest(ctx, id)
}

func (s *Store) ListApprovalRequests(ctx context.Context, scope, status string, limit int) ([]ApprovalRequestRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	status = strings.TrimSpace(status)
	rows, err := s.pool.Query(ctx, approvalRequestSelectSQL()+`
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, strings.TrimSpace(scope), status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ApprovalRequestRecord{}
	for rows.Next() {
		record, err := scanApprovalRequest(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) GetApprovalRequest(ctx context.Context, id string) (ApprovalRequestRecord, error) {
	rows, err := s.pool.Query(ctx, approvalRequestSelectSQL()+" WHERE id = $1", strings.TrimSpace(id))
	if err != nil {
		return ApprovalRequestRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ApprovalRequestRecord{}, err
		}
		return ApprovalRequestRecord{}, fmt.Errorf("approval %q not found", id)
	}
	return scanApprovalRequest(rows)
}

func (s *Store) ApprovedApprovalFor(ctx context.Context, id, action, scope, targetType, targetID string) (ApprovalRequestRecord, error) {
	action = normalizedApprovalAction(action)
	id = strings.TrimSpace(id)
	scope = strings.TrimSpace(scope)
	targetType = strings.TrimSpace(targetType)
	targetID = strings.TrimSpace(targetID)
	if id == "" {
		return ApprovalRequestRecord{}, fmt.Errorf("approval_id is required")
	}
	if action == "" {
		return ApprovalRequestRecord{}, fmt.Errorf("unsupported approval action")
	}
	rows, err := s.pool.Query(ctx, approvalRequestSelectSQL()+`
		WHERE id = $1
		  AND action = $2
		  AND scope = $3
		  AND COALESCE(target_type, '') = $4
		  AND COALESCE(target_id, '') = $5
		  AND status = 'approved'
		  AND (expires_at IS NULL OR expires_at > now())
	`, id, action, scope, targetType, targetID)
	if err != nil {
		return ApprovalRequestRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ApprovalRequestRecord{}, err
		}
		return ApprovalRequestRecord{}, fmt.Errorf("approval %q is not approved for %s %s/%s in scope %s", id, action, targetType, targetID, scope)
	}
	return scanApprovalRequest(rows)
}

func (s *Store) ApproveApprovalRequest(ctx context.Context, id string, input DecideApprovalRequestInput) (ApprovalRequestRecord, error) {
	return s.decideApprovalRequest(ctx, id, "approved", input)
}

func (s *Store) RejectApprovalRequest(ctx context.Context, id string, input DecideApprovalRequestInput) (ApprovalRequestRecord, error) {
	return s.decideApprovalRequest(ctx, id, "rejected", input)
}

func (s *Store) decideApprovalRequest(ctx context.Context, id, status string, input DecideApprovalRequestInput) (ApprovalRequestRecord, error) {
	metadata := mergeMetadata(input.Metadata, map[string]any{
		"decided_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
	tag, err := s.pool.Exec(ctx, `
		UPDATE approval_requests
		SET status = $2,
		    decided_by = NULLIF($3, ''),
		    decision_reason = NULLIF($4, ''),
		    decided_at = now(),
		    metadata = metadata || $5::jsonb
		WHERE id = $1
		  AND status = 'pending'
		  AND (expires_at IS NULL OR expires_at > now())
	`, strings.TrimSpace(id), status, strings.TrimSpace(input.DecidedBy), strings.TrimSpace(input.DecisionReason), jsonb(metadata))
	if err != nil {
		return ApprovalRequestRecord{}, err
	}
	if tag.RowsAffected() == 0 {
		current, getErr := s.GetApprovalRequest(ctx, id)
		if getErr != nil {
			return ApprovalRequestRecord{}, getErr
		}
		return ApprovalRequestRecord{}, fmt.Errorf("approval %q with status %q cannot be %s", id, current.Status, status)
	}
	return s.GetApprovalRequest(ctx, id)
}

func approvalRequestSelectSQL() string {
	return `
		SELECT
		  id,
		  action,
		  scope,
		  COALESCE(target_type, ''),
		  COALESCE(target_id, ''),
		  status,
		  COALESCE(requested_by, ''),
		  COALESCE(decided_by, ''),
		  COALESCE(reason, ''),
		  COALESCE(decision_reason, ''),
		  payload,
		  metadata,
		  expires_at::text,
		  decided_at::text,
		  created_at::text,
		  updated_at::text
		FROM approval_requests
	`
}

func scanApprovalRequest(rows pgx.Rows) (ApprovalRequestRecord, error) {
	var record ApprovalRequestRecord
	var payloadRaw, metadataRaw []byte
	if err := rows.Scan(
		&record.ID,
		&record.Action,
		&record.Scope,
		&record.TargetType,
		&record.TargetID,
		&record.Status,
		&record.RequestedBy,
		&record.DecidedBy,
		&record.Reason,
		&record.DecisionReason,
		&payloadRaw,
		&metadataRaw,
		&record.ExpiresAt,
		&record.DecidedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return ApprovalRequestRecord{}, err
	}
	record.Payload = decodeJSONMap(payloadRaw)
	record.Metadata = decodeJSONMap(metadataRaw)
	return record, nil
}

func normalizedApprovalAction(value string) string {
	switch strings.TrimSpace(value) {
	case "agent_write", "forget_claim", "challenge_claim", "source_authority_change", "scope_expansion", "backfill", "connector_enable", "acl_change", "other":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
