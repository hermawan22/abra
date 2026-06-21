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

func TestSourceConfigApprovalTarget(t *testing.T) {
	if got := sourceConfigApprovalTarget(store.SourceConfigRecord{ID: "source-1"}); got != "source-1" {
		t.Fatalf("target = %q, want source-1", got)
	}
	got := sourceConfigApprovalTarget(store.SourceConfigRecord{Scope: "team:a", SourceType: "local_repo", Name: "docs"})
	if got != "team:a/local_repo/docs" {
		t.Fatalf("target = %q, want team:a/local_repo/docs", got)
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
