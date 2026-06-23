package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type BrainTraceRecord struct {
	TraceID   string         `json:"trace_id"`
	Scope     string         `json:"scope"`
	Question  string         `json:"question"`
	Mode      string         `json:"mode,omitempty"`
	Answer    string         `json:"answer,omitempty"`
	Trace     map[string]any `json:"trace"`
	Result    map[string]any `json:"result,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
	ExpiresAt string         `json:"expires_at,omitempty"`
}

func (s *Store) UpsertBrainTrace(ctx context.Context, record BrainTraceRecord) error {
	record.TraceID = strings.TrimSpace(record.TraceID)
	record.Scope = strings.TrimSpace(record.Scope)
	record.Question = strings.TrimSpace(record.Question)
	if record.TraceID == "" || record.Scope == "" || record.Question == "" {
		return fmt.Errorf("trace_id, scope, and question are required")
	}
	if record.Trace == nil {
		record.Trace = map[string]any{}
	}
	if record.Result == nil {
		record.Result = map[string]any{}
	}
	_, err := s.queryRunner().Exec(ctx, `
		INSERT INTO brain_traces (
		  trace_id, scope, question, mode, answer, trace, result
		)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
		ON CONFLICT (trace_id)
		DO UPDATE SET
		  scope = EXCLUDED.scope,
		  question = EXCLUDED.question,
		  mode = EXCLUDED.mode,
		  answer = EXCLUDED.answer,
		  trace = EXCLUDED.trace,
		  result = EXCLUDED.result,
		  created_at = now(),
		  expires_at = now() + interval '30 days'
	`, record.TraceID, record.Scope, record.Question, record.Mode, record.Answer, jsonb(record.Trace), jsonb(record.Result))
	return err
}

func (s *Store) GetBrainTrace(ctx context.Context, traceID string) (BrainTraceRecord, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return BrainTraceRecord{}, fmt.Errorf("trace_id is required")
	}
	var record BrainTraceRecord
	var traceRaw, resultRaw []byte
	err := s.queryRunner().QueryRow(ctx, `
		SELECT trace_id, scope, question, COALESCE(mode, ''), COALESCE(answer, ''),
		       trace, result, created_at::text, expires_at::text
		FROM brain_traces
		WHERE trace_id = $1
		  AND (expires_at IS NULL OR expires_at > now())
	`, traceID).Scan(&record.TraceID, &record.Scope, &record.Question, &record.Mode, &record.Answer, &traceRaw, &resultRaw, &record.CreatedAt, &record.ExpiresAt)
	if err != nil {
		return BrainTraceRecord{}, err
	}
	record.Trace = map[string]any{}
	if len(traceRaw) > 0 {
		_ = json.Unmarshal(traceRaw, &record.Trace)
	}
	record.Result = map[string]any{}
	if len(resultRaw) > 0 {
		_ = json.Unmarshal(resultRaw, &record.Result)
	}
	return record, nil
}
