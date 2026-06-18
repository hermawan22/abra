package brain

import (
	"context"
	"regexp"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

var policyUsePattern = regexp.MustCompile(`(?i)^\s*(.+?)\s+(must|should|shall|needs to|required to)\s+(not\s+)?use\s+(.+?)(?:\s+for\s+(.+?))?\s*[.!]?\s*$`)

type policyAssertion struct {
	Subject  string
	Strength string
	Negated  bool
	Value    string
	Purpose  string
	Key      string
}

func extractPolicyAssertion(claim string) (policyAssertion, bool) {
	matches := policyUsePattern.FindStringSubmatch(strings.TrimSpace(claim))
	if len(matches) == 0 {
		return policyAssertion{}, false
	}
	assertion := policyAssertion{
		Subject:  normalizePolicyPart(matches[1]),
		Strength: strings.ToLower(strings.TrimSpace(matches[2])),
		Negated:  strings.TrimSpace(matches[3]) != "",
		Value:    normalizePolicyPart(matches[4]),
		Purpose:  normalizePolicyPart(matches[5]),
	}
	if assertion.Subject == "" || assertion.Value == "" {
		return policyAssertion{}, false
	}
	assertion.Key = assertion.Subject + " :: " + assertion.Purpose
	return assertion, true
}

func policyAssertionsConflict(left, right policyAssertion) bool {
	if left.Key == "" || right.Key == "" || left.Key != right.Key {
		return false
	}
	if left.Value == "" || right.Value == "" {
		return false
	}
	if left.Negated != right.Negated {
		return left.Value == right.Value
	}
	if left.Negated && right.Negated {
		return false
	}
	return left.Value != right.Value
}

func policyConflictSeverity(left, right policyAssertion) string {
	if isHardPolicyStrength(left.Strength) || isHardPolicyStrength(right.Strength) {
		return "high"
	}
	return "medium"
}

func isHardPolicyStrength(value string) bool {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "must", "shall", "needs to", "required to":
		return true
	default:
		return false
	}
}

func normalizePolicyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, " \t\r\n\"'`.,;:()[]{}")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.TrimPrefix(value, "the ")
	value = strings.TrimPrefix(value, "a ")
	value = strings.TrimPrefix(value, "an ")
	return strings.TrimSpace(value)
}

func (s *Service) detectClaimConflicts(ctx context.Context, claimID, claimText, scope, sourceURL string, metadata map[string]any) (int, error) {
	assertion, ok := extractPolicyAssertion(claimText)
	if !ok {
		return 0, nil
	}
	candidates, err := s.db.SearchClaims(ctx, conflictCandidateQuery(assertion), scope, claimID, 20)
	if err != nil {
		return 0, err
	}
	conflicts := 0
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidateAssertion, ok := extractPolicyAssertion(candidate.Claim)
		if !ok || !policyAssertionsConflict(assertion, candidateAssertion) {
			continue
		}
		if _, ok := seen[candidate.ID]; ok {
			continue
		}
		seen[candidate.ID] = struct{}{}
		_, err := s.db.UpsertClaimConflict(ctx, store.ConflictRecord{
			Scope:              scope,
			ConflictType:       "contradicts",
			Severity:           policyConflictSeverity(assertion, candidateAssertion),
			PrimaryClaimID:     claimID,
			ConflictingClaimID: candidate.ID,
			DetectedBy:         "auto-policy-detector",
			Authority:          "deterministic-policy-detector",
			Metadata: mergeMetadata(metadata, map[string]any{
				"detector":          "policy_use_contradiction_v1",
				"policy_key":        assertion.Key,
				"new_value":         assertion.Value,
				"existing_value":    candidateAssertion.Value,
				"new_negated":       assertion.Negated,
				"existing_negated":  candidateAssertion.Negated,
				"new_source_url":    sourceURL,
				"existing_claim_id": candidate.ID,
				"existing_source":   candidate.Source,
			}),
		})
		if err != nil {
			return conflicts, err
		}
		conflicts++
	}
	return conflicts, nil
}

func conflictCandidateQuery(assertion policyAssertion) string {
	parts := []string{assertion.Subject, assertion.Purpose, "use"}
	return strings.Join(nonEmpty(parts), " ")
}

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
