package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDecideLearningProposalRejectsApplyStatuses(t *testing.T) {
	store := &Store{}
	for _, status := range []string{"applied", "applying"} {
		t.Run(status, func(t *testing.T) {
			_, err := store.DecideLearningProposal(context.Background(), "proposal-1", DecideLearningProposalInput{
				Status: status,
			})
			if err == nil {
				t.Fatalf("DecideLearningProposal(%q) error = nil, want rejection before database access", status)
			}
			if !strings.Contains(err.Error(), "status") {
				t.Fatalf("DecideLearningProposal(%q) error = %q, want status rejection", status, err)
			}
		})
	}
}

func TestLearningProposalApplyingMigrationExtendsStatusConstraint(t *testing.T) {
	content, err := os.ReadFile("../../migrations/017_learning_proposal_applying_status.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := compactSQL(string(content))
	for _, fragment := range []string{
		"DROP CONSTRAINT IF EXISTS learning_proposals_status_check",
		"ADD CONSTRAINT learning_proposals_status_check",
		"'applying'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing %q:\n%s", fragment, sql)
		}
	}
}
