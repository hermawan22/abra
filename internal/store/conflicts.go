package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type conflictScanner interface {
	Scan(dest ...any) error
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
