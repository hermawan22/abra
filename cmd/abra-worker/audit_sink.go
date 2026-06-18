package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/store"
)

const auditSinkCursorID = "audit-sink:default"

func deliverAuditSink(ctx context.Context, cfg config.AuditSinkConfig, db *store.Store, logger *slog.Logger) (int, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return 0, nil
	}
	cursor, found, err := db.GetIntegrationCursor(ctx, auditSinkCursorID)
	if err != nil {
		return 0, fmt.Errorf("load audit sink cursor: %w", err)
	}
	if !found {
		cursor = store.IntegrationCursorRecord{}
	}
	events, err := db.ListAuditEventsForDelivery(ctx, cfg.Scope, cursor, cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("list audit events for sink: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	body, err := auditSinkBody(events)
	if err != nil {
		return 0, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build audit sink request: %w", err)
	}
	req.Header.Set("content-type", "application/x-ndjson")
	req.Header.Set("user-agent", "abra-worker/0.1")
	req.Header.Set("x-abra-delivery-kind", "audit-events")
	req.Header.Set("x-abra-events", strconv.Itoa(len(events)))
	req.Header.Set("x-abra-cursor-event-id", events[len(events)-1].ID)
	if strings.TrimSpace(cfg.Scope) != "" {
		req.Header.Set("x-abra-scope", strings.TrimSpace(cfg.Scope))
	}
	if strings.TrimSpace(cfg.Token) != "" {
		req.Header.Set("authorization", "Bearer "+strings.TrimSpace(cfg.Token))
	}
	if strings.TrimSpace(cfg.Secret) != "" {
		req.Header.Set("x-abra-signature", "sha256="+auditSinkSignature(body, cfg.Secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("post audit sink: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("audit sink returned %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	last := events[len(events)-1]
	if err := db.UpsertIntegrationCursorFromAuditEvent(ctx, store.IntegrationCursorRecord{
		ID:              auditSinkCursorID,
		IntegrationType: "audit_sink",
		Target:          cfg.URL,
		Metadata: map[string]any{
			"last_status_code": resp.StatusCode,
			"last_batch_size":  len(events),
			"last_scope":       strings.TrimSpace(cfg.Scope),
		},
	}, last.ID); err != nil {
		return 0, fmt.Errorf("advance audit sink cursor: %w", err)
	}
	logger.Info("audit sink delivery finished", "events", len(events), "last_event_id", last.ID)
	return len(events), nil
}

func auditSinkBody(events []store.AuditEventRecord) ([]byte, error) {
	var body bytes.Buffer
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("encode audit event %q: %w", event.ID, err)
		}
		body.Write(line)
		body.WriteByte('\n')
	}
	return body.Bytes(), nil
}

func auditSinkSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
