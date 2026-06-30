package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func governLearningCommand(ctx context.Context, args cliArgs) error {
	action := "list"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "list", "ls":
		return governLearningList(ctx, args)
	case "accept", "approve":
		return governLearningDecide(ctx, args, "accepted")
	case "reject":
		return governLearningDecide(ctx, args, "rejected")
	case "cancel":
		return governLearningDecide(ctx, args, "canceled")
	case "apply":
		return governLearningApply(ctx, args)
	default:
		return fmt.Errorf("unknown govern learning command %q\n\n%s", action, commandUsage("govern"))
	}
}

func governLearningList(ctx context.Context, args cliArgs) error {
	payload := map[string]any{
		"scope": scopeOrDefault(args, "."),
		"limit": intFlag(args, "limit", 50),
	}
	if status := strings.TrimSpace(flag(args, "status", "")); status != "" {
		payload["status"] = status
	}
	raw, err := callMCPToolRaw(ctx, args, "list_learning_proposals", payload)
	if err != nil {
		return err
	}
	result := normalizeLearningProposalListResult(raw)
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items := learningProposalItems(result)
	fmt.Printf("Learning proposals: %d\n", len(items))
	printLearningProposals(items, 20)
	return nil
}

func normalizeLearningProposalListResult(raw any) map[string]any {
	if result, ok := raw.(map[string]any); ok {
		return result
	}
	if proposals, ok := raw.([]any); ok {
		return map[string]any{"learning_proposals": proposals}
	}
	return map[string]any{"learning_proposals": []any{}}
}

func governLearningDecide(ctx context.Context, args cliArgs, status string) error {
	proposalID := learningProposalIDArg(args)
	if proposalID == "" {
		return errors.New("govern learning decision requires a proposal id")
	}
	result, err := callMCPTool(ctx, args, "decide_learning_proposal", map[string]any{
		"proposal_id":   proposalID,
		"status":        status,
		"reviewed_by":   firstNonEmpty(flag(args, "reviewed-by", ""), flag(args, "agent", ""), "cli"),
		"review_reason": flag(args, "reason", ""),
		"approval_id":   flag(args, "approval-id", ""),
		"metadata":      map[string]any{"channel": "cli", "command": "govern learning"},
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Learning proposal %s: %s\n", status, proposalID)
	printLearningProposal(result)
	return nil
}

func governLearningApply(ctx context.Context, args cliArgs) error {
	proposalID := learningProposalIDArg(args)
	if proposalID == "" {
		return errors.New("govern learning apply requires a proposal id")
	}
	result, err := callMCPTool(ctx, args, "apply_learning_proposal", map[string]any{
		"proposal_id": proposalID,
		"applied_by":  firstNonEmpty(flag(args, "applied-by", ""), flag(args, "agent", ""), "cli"),
		"approval_id": flag(args, "approval-id", ""),
		"metadata":    map[string]any{"channel": "cli", "command": "govern learning apply"},
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Learning proposal applied: " + proposalID)
	printLearningProposal(result)
	return nil
}

func learningProposalIDArg(args cliArgs) string {
	if id := firstNonEmpty(flag(args, "proposal-id", ""), flag(args, "id", "")); id != "" {
		return id
	}
	if len(args.Rest) > 0 {
		return strings.TrimSpace(args.Rest[0])
	}
	return ""
}

func learningProposalItems(result map[string]any) []any {
	if items, ok := result["learning_proposals"].([]any); ok {
		return items
	}
	if items, ok := result["proposals"].([]any); ok {
		return items
	}
	return nil
}

func printLearningProposals(items []any, limit int) {
	for i, raw := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		fmt.Printf("- %s  %s  %s  %s\n", stringValue(item["id"], ""), stringValue(item["status"], "unknown"), stringValue(item["proposal_type"], "proposal"), stringValue(item["title"], ""))
	}
}

func printLearningProposal(result map[string]any) {
	proposal := result
	if nested, ok := result["learning_proposal"].(map[string]any); ok {
		proposal = nested
	}
	if len(proposal) == 0 {
		return
	}
	if id := stringValue(proposal["id"], ""); id != "" {
		fmt.Println("id: " + id)
	}
	if scope := stringValue(proposal["scope"], ""); scope != "" {
		fmt.Println("scope: " + scope)
	}
	if title := stringValue(proposal["title"], ""); title != "" {
		fmt.Println("title: " + title)
	}
	if status := stringValue(proposal["status"], ""); status != "" {
		fmt.Println("status: " + status)
	}
}
