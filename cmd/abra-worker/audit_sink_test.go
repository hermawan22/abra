package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestAuditSinkBodyWritesNDJSON(t *testing.T) {
	body, err := auditSinkBody([]store.AuditEventRecord{
		{ID: "audit-1", EventType: "claim.remembered", Scope: "team:example", Metadata: map[string]any{"source": "smoke"}},
		{ID: "audit-2", EventType: "claim.forgotten", Scope: "team:example", Metadata: map[string]any{"reason": "stale"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2\n%s", len(lines), string(body))
	}
	if !strings.Contains(lines[0], `"id":"audit-1"`) || !strings.Contains(lines[1], `"id":"audit-2"`) {
		t.Fatalf("unexpected ndjson body:\n%s", string(body))
	}
	if !strings.HasSuffix(string(body), "\n") {
		t.Fatalf("ndjson body should end with newline")
	}
}

func TestAuditSinkSignatureUsesHMACSHA256(t *testing.T) {
	body := []byte("{\"id\":\"audit-1\"}\n")
	secret := "dev-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if got := auditSinkSignature(body, " "+secret+" "); got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}
