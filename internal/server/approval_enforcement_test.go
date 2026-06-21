package server

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestSourceAuthorityApprovalRequired(t *testing.T) {
	cases := []struct {
		name  string
		input store.SourceConfigRecord
		want  bool
	}{
		{
			name:  "empty authority is not an authority change",
			input: store.SourceConfigRecord{},
			want:  false,
		},
		{
			name:  "manual default is not an authority change",
			input: store.SourceConfigRecord{Authority: "manual-unverified", AuthorityScore: 0.35},
			want:  false,
		},
		{
			name:  "named trusted authority requires approval",
			input: store.SourceConfigRecord{Authority: "team-convention"},
			want:  true,
		},
		{
			name:  "elevated authority score requires approval",
			input: store.SourceConfigRecord{AuthorityScore: 0.7},
			want:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceAuthorityApprovalRequired(tc.input); got != tc.want {
				t.Fatalf("sourceAuthorityApprovalRequired() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSourceConfigApprovalAction(t *testing.T) {
	cases := []struct {
		name  string
		input store.SourceConfigRecord
		want  string
	}{
		{
			name:  "active untrusted source is connector enablement",
			input: store.SourceConfigRecord{Status: "active", Authority: "manual-unverified", AuthorityScore: 0.35},
			want:  "connector_enable",
		},
		{
			name:  "empty status defaults to active connector enablement",
			input: store.SourceConfigRecord{},
			want:  "connector_enable",
		},
		{
			name:  "paused source does not enable connector",
			input: store.SourceConfigRecord{Status: "paused", Authority: "manual-unverified", AuthorityScore: 0.35},
			want:  "",
		},
		{
			name:  "trusted paused source is authority change",
			input: store.SourceConfigRecord{Status: "paused", Authority: "official-doc"},
			want:  "source_authority_change",
		},
		{
			name:  "trusted active source prefers authority action",
			input: store.SourceConfigRecord{Status: "active", AuthorityScore: 0.8},
			want:  "source_authority_change",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceConfigApprovalAction(tc.input); got != tc.want {
				t.Fatalf("sourceConfigApprovalAction() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourceConfigApprovalActionForStatusUsesTargetStatus(t *testing.T) {
	source := store.SourceConfigRecord{Status: "paused", Authority: "manual-unverified", AuthorityScore: 0.35}
	if got := sourceConfigApprovalActionForStatus(source, "active"); got != "connector_enable" {
		t.Fatalf("activate approval action = %q, want connector_enable", got)
	}
	if got := sourceConfigApprovalActionForStatus(source, "paused"); got != "" {
		t.Fatalf("paused approval action = %q, want empty", got)
	}
}

func TestSourceValidationApprovalActionRequiresApprovalForServerCredentialEnv(t *testing.T) {
	if got := sourceValidationApprovalAction(store.SourceConfigRecord{Config: map[string]any{"bearer_token_env": "MCP_TOKEN"}}); got != "connector_enable" {
		t.Fatalf("bearer env approval action = %q, want connector_enable", got)
	}
	if got := sourceValidationApprovalAction(store.SourceConfigRecord{Config: map[string]any{"header_env": map[string]any{"X-API-Key": "MCP_API_KEY"}}}); got != "connector_enable" {
		t.Fatalf("header env approval action = %q, want connector_enable", got)
	}
	if got := sourceValidationApprovalAction(store.SourceConfigRecord{Config: map[string]any{"arguments": map[string]any{"space": "ENG"}}}); got != "" {
		t.Fatalf("plain validation approval action = %q, want empty", got)
	}
}

func TestSourceConfigApprovalTarget(t *testing.T) {
	if got := sourceConfigApprovalTarget(store.SourceConfigRecord{ID: "source-1"}); got != "source-1" {
		t.Fatalf("target = %q, want source-1", got)
	}
	got := sourceConfigApprovalTarget(store.SourceConfigRecord{Scope: "team:a", SourceType: "local_repo", Name: "docs"})
	if got != "team:a/local_repo/docs" {
		t.Fatalf("target = %q, want team:a/local_repo/docs", got)
	}
}

func TestSourceStatusMetadataProtectsServerOwnedFields(t *testing.T) {
	got := sourceStatusMetadata(map[string]any{
		"channel":           "cli",
		"status_change":     "active",
		"status_changed_by": "caller",
	}, "paused", "api")
	if got["channel"] != "cli" {
		t.Fatalf("custom metadata missing: %#v", got)
	}
	if got["status_change"] != "paused" {
		t.Fatalf("status_change = %#v", got["status_change"])
	}
	if got["status_changed_by"] != "api" {
		t.Fatalf("status_changed_by = %#v", got["status_changed_by"])
	}
}

func TestValidateSourceConfigInputRejectsInvalidCoreWorkerSource(t *testing.T) {
	err := validateSourceConfigInput(store.SourceConfigRecord{
		Scope:      "team:a",
		SourceType: "local_repo",
		Name:       "docs",
		BaseURL:    "https://example.com/repo",
		Config:     map[string]any{},
	})
	if err == nil {
		t.Fatal("expected invalid core worker source to fail validation")
	}
}

func TestValidateSourceConfigInputAllowsOverlaySource(t *testing.T) {
	err := validateSourceConfigInput(store.SourceConfigRecord{
		Scope:         "team:a",
		SourceType:    "jira",
		Name:          "project",
		BaseURL:       "https://jira.example.local",
		ConnectorKind: "overlay",
		Config:        map[string]any{"project": "ABRA"},
	})
	if err != nil {
		t.Fatalf("overlay source should be accepted by core API contract: %v", err)
	}
}

func TestValidateSourceConfigInputValidatesFreshnessPolicy(t *testing.T) {
	base := store.SourceConfigRecord{
		Scope:           "team:a",
		SourceType:      "jira",
		Name:            "project",
		BaseURL:         "https://jira.example.local",
		ConnectorKind:   "overlay",
		Config:          map[string]any{"project": "ABRA"},
		FreshnessPolicy: map[string]any{"max_age_seconds": float64(300)},
		ScheduleCron:    "@every 10m",
	}
	if err := validateSourceConfigInput(base); err != nil {
		t.Fatalf("valid freshness policy rejected: %v", err)
	}

	invalidPolicy := base
	invalidPolicy.FreshnessPolicy = map[string]any{"max_age_seconds": float64(0)}
	if err := validateSourceConfigInput(invalidPolicy); err == nil {
		t.Fatal("expected zero freshness policy to be rejected")
	}

	invalidKey := base
	invalidKey.FreshnessPolicy = map[string]any{"cron": float64(1)}
	if err := validateSourceConfigInput(invalidKey); err == nil {
		t.Fatal("expected unsupported freshness policy key to be rejected")
	}

	invalidSchedule := base
	invalidSchedule.ScheduleCron = "0 * * * *"
	if err := validateSourceConfigInput(invalidSchedule); err == nil {
		t.Fatal("expected full cron schedule to be rejected")
	}
}
