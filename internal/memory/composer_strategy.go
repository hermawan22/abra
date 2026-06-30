package memory

import (
	"context"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

func (c *Composer) agentPolicyDecisions(ctx context.Context, input ComposeInput) ([]AgentPolicyDecision, error) {
	principalID := strings.TrimSpace(input.Agent)
	if principalID == "" {
		principalID = "unknown"
	}
	actions := []struct {
		action     string
		targetType string
		targetID   string
	}{
		{action: "agent_write", targetType: "memory_write", targetID: input.Scope},
		{action: "challenge_claim", targetType: "claim", targetID: "*"},
		{action: "forget_claim", targetType: "claim", targetID: "*"},
		{action: "backfill", targetType: "memory_summaries", targetID: input.Scope},
		{action: "source_authority_change", targetType: "source_config", targetID: "*"},
		{action: "acl_change", targetType: "policy", targetID: "*"},
	}
	inputs := make([]store.AgentActionDecisionInput, 0, len(actions))
	for _, action := range actions {
		inputs = append(inputs, store.AgentActionDecisionInput{
			Action:        action.action,
			Scope:         input.Scope,
			TargetType:    action.targetType,
			TargetID:      action.targetID,
			PrincipalType: "agent",
			PrincipalID:   principalID,
		})
	}
	results, err := c.store.EvaluateAgentActionPolicies(ctx, inputs)
	if err != nil {
		return nil, err
	}
	decisions := make([]AgentPolicyDecision, 0, len(results))
	for i, result := range results {
		decisions = append(decisions, AgentPolicyDecision{
			Action:        inputs[i].Action,
			TargetType:    inputs[i].TargetType,
			TargetID:      inputs[i].TargetID,
			Allowed:       result.Allowed,
			Decision:      result.Decision,
			Reason:        result.Reason,
			MatchedPolicy: result.MatchedPolicy,
		})
	}
	return decisions, nil
}

func normalizeInput(input ComposeInput) ComposeInput {
	input.Task = strings.Join(strings.Fields(input.Task), " ")
	input.Scope = strings.TrimSpace(input.Scope)
	input.Hook = strings.TrimSpace(input.Hook)
	input.Entity = strings.Join(strings.Fields(input.Entity), " ")
	input.AsOf = normalizeAsOfInput(input.AsOf)
	input.Mode = NormalizeRetrievalMode(string(input.Mode))
	if input.Hook == "" {
		input.Hook = string(policy.HookBeforeTask)
	}
	if input.Limit < 1 || input.Limit > 20 {
		input.Limit = 6
	}
	if input.MaxQueries < 1 || input.MaxQueries > 12 {
		input.MaxQueries = 6
	}
	if input.TokenBudget < 1 {
		input.TokenBudget = 1600
	}
	if input.TokenBudget < 300 {
		input.TokenBudget = 300
	}
	if input.TokenBudget > 12000 {
		input.TokenBudget = 12000
	}
	input.Files = compactList(input.Files)
	input.ChangedFiles = compactList(input.ChangedFiles)
	return input
}

func applyRetrievalMode(input ComposeInput) ComposeInput {
	input.Mode = NormalizeRetrievalMode(string(input.Mode))
	switch input.Mode {
	case RetrievalModeFast:
		if input.Limit > 3 {
			input.Limit = 3
		}
		if input.MaxQueries > 1 {
			input.MaxQueries = 1
		}
		if input.TokenBudget > 700 {
			input.TokenBudget = 700
		}
	case RetrievalModeDeep:
		if input.Limit < 10 {
			input.Limit = 10
		}
		if input.MaxQueries < 8 {
			input.MaxQueries = 8
		}
		if input.TokenBudget < 2600 {
			input.TokenBudget = 2600
		}
	}
	return input
}

func normalizeAsOfInput(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return value
}

func classifyIntent(input ComposeInput) string {
	text := strings.ToLower(input.Task + " " + input.Language + " " + strings.Join(input.Files, " ") + " " + strings.Join(input.ChangedFiles, " "))
	switch {
	case containsAny(text, "upgrade", "migration", "migrate", "breaking", "dependency", "version"):
		return "migration"
	case containsAny(text, "bug", "fix", "error", "incident", "regression", "failing", "fail"):
		return "debugging"
	case containsAny(text, "implement", "build", "add", "feature", "refactor", "code"):
		return "implementation"
	case containsAny(text, "architecture", "design", "how", "explain", "overview", "flow"):
		return "architecture"
	default:
		return "general"
	}
}

func strategyQueries(input ComposeInput, intent string) []policy.RecallQuery {
	base := "Task: " + input.Task
	query := func(text, reason string) policy.RecallQuery {
		return policy.RecallQuery{Query: text, Scope: input.Scope, Limit: input.Limit, IncludeUnverified: input.IncludeUnverified, Reason: reason}
	}
	queries := []policy.RecallQuery{
		query("Hierarchical summaries, code intelligence overview, source areas, package operations, and verified facts for "+base, "load compact high-signal memory before detailed retrieval"),
	}
	if len(input.Files)+len(input.ChangedFiles) > 0 {
		queries = append(queries, query("File-specific decisions, symbols, owners, tests, and dependency context for "+strings.Join(append(input.Files, input.ChangedFiles...), " "), "anchor memory to touched files"))
	}
	switch intent {
	case "migration":
		queries = append(queries,
			query("Dependency versions, package scripts, runtime constraints, breaking changes, compatibility risks, and rollout notes for "+base, "plan migration safely"),
			query("Known failures, stale claims, disputed assumptions, and verification gates related to "+base, "avoid unsafe upgrades"),
		)
	case "debugging":
		queries = append(queries,
			query("Known incidents, regression risks, failing tests, error handling, and ownership context for "+base, "prioritize likely failure causes"),
			query("Relevant source files, call paths, data flow, and graph relations for "+base, "trace the issue through the system"),
		)
	case "architecture":
		queries = append(queries,
			query("Architecture summaries, module boundaries, source areas, routes, APIs, entities, and relations for "+base, "answer with global structure"),
			query("Important decisions, constraints, and evidence-backed tradeoffs for "+base, "separate facts from guesses"),
		)
	default:
		queries = append(queries,
			query("Implementation conventions, reusable components, APIs, tests, and validation expectations for "+base, "prepare an agent to change code"),
			query("Graph relations, dependencies, impacted modules, and related symbols for "+base, "find cross-file impact"),
		)
	}
	return queries
}

func mergeQueries(first []policy.RecallQuery, rest ...policy.RecallQuery) []policy.RecallQuery {
	seen := map[string]struct{}{}
	out := []policy.RecallQuery{}
	for _, query := range append(first, rest...) {
		query.Query = strings.Join(strings.Fields(query.Query), " ")
		query.Scope = strings.TrimSpace(query.Scope)
		if query.Query == "" || query.Scope == "" {
			continue
		}
		key := query.Scope + "\x00" + strings.ToLower(query.Query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
	}
	return out
}

func strategyDescription(intent string) string {
	switch intent {
	case "migration":
		return "migration-aware packet: summaries, dependency facts, compatibility risks, graph impact, and verification gates"
	case "debugging":
		return "debugging packet: known failures, likely impacted files, graph context, stale claims, and test guidance"
	case "architecture":
		return "architecture packet: hierarchical summaries, module boundaries, source areas, graph context, and evidence"
	case "implementation":
		return "implementation packet: conventions, relevant files, reusable components, dependencies, graph impact, and checks"
	default:
		return "general packet: source-backed facts, documents, graph context, risks, and next steps"
	}
}
