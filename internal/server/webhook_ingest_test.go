package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/hermawan22/abra/internal/config"
)

func TestWebhookSignatureAcceptsConfiguredSecret(t *testing.T) {
	body := []byte(`{"ok":true}`)
	h := handler{cfg: config.Config{WebhookSecrets: []string{"secret"}}}
	request := httptest.NewRequest("POST", "/ingest/webhooks", nil)
	request.Header.Set("x-abra-signature", "sha256="+webhookSignature("secret", body))

	if !h.validWebhookSignature(request, body) {
		t.Fatal("expected valid webhook signature")
	}
}

func TestWebhookSignatureRejectsMissingSignatureWhenSecretConfigured(t *testing.T) {
	h := handler{cfg: config.Config{WebhookSecrets: []string{"secret"}}}
	request := httptest.NewRequest("POST", "/ingest/webhooks", nil)

	if h.validWebhookSignature(request, []byte(`{"ok":true}`)) {
		t.Fatal("expected missing webhook signature to be rejected")
	}
}

func TestWebhookDocumentsExpandsBatchDefaults(t *testing.T) {
	docs, err := webhookDocuments(webhookIngestRequest{
		ConnectorKind:  "jira",
		EventType:      "issue.updated",
		DeliveryID:     "delivery-1",
		Scope:          "team:platform",
		SourceType:     "jira",
		Authority:      "jira-project",
		AuthorityScore: 0.8,
		Documents: []webhookDocumentInput{
			{
				SourceURL: "https://jira.example/browse/PLAT-1",
				Title:     "PLAT-1",
				Content:   "PLAT-1 should use Abra for source-cited memory.",
			},
			{
				SourceURL: "https://jira.example/browse/PLAT-2",
				Title:     "PLAT-2",
				Content:   "PLAT-2 should keep approval records.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2", len(docs))
	}
	for _, doc := range docs {
		if doc.Scope != "team:platform" {
			t.Fatalf("scope = %q", doc.Scope)
		}
		if doc.SourceType != "jira" {
			t.Fatalf("source_type = %q", doc.SourceType)
		}
		if doc.Metadata["connector_kind"] != "jira" || doc.Metadata["webhook_event_type"] != "issue.updated" {
			t.Fatalf("missing webhook metadata: %#v", doc.Metadata)
		}
		if doc.Metadata["authority"] != "jira-project" || doc.Metadata["authority_score"] != 0.8 {
			t.Fatalf("missing authority metadata: %#v", doc.Metadata)
		}
	}
}

func TestStringMetadataTrimsAndFormatsValues(t *testing.T) {
	metadata := map[string]any{
		"authority": " jira-project ",
		"score":     0.75,
		"empty":     nil,
	}
	if got := stringMetadata(metadata, "authority"); got != "jira-project" {
		t.Fatalf("authority metadata = %q", got)
	}
	if got := stringMetadata(metadata, "score"); got != "0.75" {
		t.Fatalf("score metadata = %q", got)
	}
	if got := stringMetadata(metadata, "missing"); got != "" {
		t.Fatalf("missing metadata = %q", got)
	}
	if got := stringMetadata(metadata, "empty"); got != "" {
		t.Fatalf("nil metadata = %q", got)
	}
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
