package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type BrainEvalRunRecord struct {
	Scope     string
	SuiteName string
	SuiteFile string
	Agent     string
	Total     int
	Passed    int
	Success   bool
	Reports   []byte
	Metadata  map[string]any
}

type BrainEvalRunResult struct {
	ID        string           `json:"id"`
	Scope     string           `json:"scope,omitempty"`
	SuiteName string           `json:"suite_name,omitempty"`
	SuiteFile string           `json:"suite_file,omitempty"`
	Agent     string           `json:"agent,omitempty"`
	Total     int              `json:"total"`
	Passed    int              `json:"passed"`
	Success   bool             `json:"success"`
	Reports   []map[string]any `json:"reports"`
	Metadata  map[string]any   `json:"metadata"`
	CreatedAt string           `json:"created_at"`
}

func (s *Store) InsertBrainEvalRun(ctx context.Context, record BrainEvalRunRecord) (BrainEvalRunResult, error) {
	if record.Total < 1 {
		return BrainEvalRunResult{}, fmt.Errorf("total must be positive")
	}
	if record.Passed < 0 || record.Passed > record.Total {
		return BrainEvalRunResult{}, fmt.Errorf("passed must be between 0 and total")
	}
	if !json.Valid(record.Reports) {
		return BrainEvalRunResult{}, fmt.Errorf("reports payload must be valid json")
	}
	var reports []map[string]any
	if err := json.Unmarshal(record.Reports, &reports); err != nil || reports == nil {
		return BrainEvalRunResult{}, fmt.Errorf("reports payload must be a json array")
	}
	if len(reports) != record.Total {
		return BrainEvalRunResult{}, fmt.Errorf("reports length must equal total")
	}
	id := brainEvalRunID()
	var result BrainEvalRunResult
	var reportsRaw, metadataRaw []byte
	err := s.queryRunner().QueryRow(ctx, `
		INSERT INTO brain_eval_runs (
		  id, scope, suite_name, suite_file, agent, total, passed, success, reports, metadata
		)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''), $6, $7, $8, $9::jsonb, $10::jsonb)
		RETURNING id, COALESCE(scope, ''), COALESCE(suite_name, ''), COALESCE(suite_file, ''), COALESCE(agent, ''),
		          total, passed, success, reports, metadata, created_at::text
	`, id, strings.TrimSpace(record.Scope), strings.TrimSpace(record.SuiteName), strings.TrimSpace(record.SuiteFile), strings.TrimSpace(record.Agent), record.Total, record.Passed, record.Success, string(record.Reports), jsonb(record.Metadata)).Scan(
		&result.ID,
		&result.Scope,
		&result.SuiteName,
		&result.SuiteFile,
		&result.Agent,
		&result.Total,
		&result.Passed,
		&result.Success,
		&reportsRaw,
		&metadataRaw,
		&result.CreatedAt,
	)
	if err != nil {
		return BrainEvalRunResult{}, err
	}
	result.Reports = decodeJSONArrayMaps(reportsRaw)
	result.Metadata = decodeJSONMap(metadataRaw)
	return result, nil
}

func brainEvalRunID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return "brain-eval-" + hex.EncodeToString(random[:])
	}
	return stableID("brain-eval", time.Now().UTC().Format(time.RFC3339Nano))
}

func (s *Store) ListBrainEvalRuns(ctx context.Context, scope string, limit int) ([]BrainEvalRunResult, error) {
	if limit < 1 || limit > 50 {
		limit = 10
	}
	rows, err := s.queryRunner().Query(ctx, `
		SELECT id, COALESCE(scope, ''), COALESCE(suite_name, ''), COALESCE(suite_file, ''), COALESCE(agent, ''),
		       total, passed, success, reports, metadata, created_at::text
		FROM brain_eval_runs
		WHERE NULLIF($1, '') IS NULL OR scope = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, strings.TrimSpace(scope), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrainEvalRunResult{}
	for rows.Next() {
		var result BrainEvalRunResult
		var reportsRaw, metadataRaw []byte
		if err := rows.Scan(
			&result.ID,
			&result.Scope,
			&result.SuiteName,
			&result.SuiteFile,
			&result.Agent,
			&result.Total,
			&result.Passed,
			&result.Success,
			&reportsRaw,
			&metadataRaw,
			&result.CreatedAt,
		); err != nil {
			return nil, err
		}
		result.Reports = decodeJSONArrayMaps(reportsRaw)
		result.Metadata = decodeJSONMap(metadataRaw)
		out = append(out, result)
	}
	return out, rows.Err()
}

func decodeJSONArrayMaps(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return []map[string]any{}
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return []map[string]any{}
	}
	if out == nil {
		return []map[string]any{}
	}
	return out
}
