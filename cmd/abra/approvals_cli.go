package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func approvalsCommand(ctx context.Context, args cliArgs) error {
	action := "list"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "list", "ls":
		return listApprovals(ctx, args)
	case "request", "create":
		return requestApproval(ctx, args)
	case "approve":
		return decideApproval(ctx, args, "approve")
	case "reject":
		return decideApproval(ctx, args, "reject")
	default:
		return fmt.Errorf("unknown approvals action %q\n\n%s", action, commandUsage("approvals"))
	}
}

func listApprovals(ctx context.Context, args cliArgs) error {
	path := "/approvals?limit=" + strconv.Itoa(intFlag(args, "limit", 50))
	if scope := flag(args, "scope", os.Getenv("ABRA_SCOPE")); scope != "" {
		path += "&scope=" + urlQueryEscape(scope)
	}
	if status := flag(args, "status", ""); status != "" {
		path += "&status=" + urlQueryEscape(status)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["approvals"].([]any)
	fmt.Printf("Approvals: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		fmt.Printf("- %s  %s  %s  %s  %s/%s\n",
			stringValue(item["id"], ""),
			stringValue(item["status"], ""),
			stringValue(item["action"], ""),
			stringValue(item["scope"], ""),
			stringValue(item["target_type"], ""),
			stringValue(item["target_id"], ""),
		)
	}
	return nil
}

func requestApproval(ctx context.Context, args cliArgs) error {
	action := firstNonEmpty(flag(args, "action", ""), flag(args, "approval-action", ""))
	if action == "" && len(args.Rest) > 0 {
		action = strings.TrimSpace(args.Rest[0])
		args.Rest = args.Rest[1:]
	}
	if action == "" {
		return errors.New("approvals request requires --action, for example: abra approvals request --scope repo:demo --action agent_write")
	}
	scope := scopeOrDefault(args, ".")
	body := map[string]any{
		"action":      action,
		"scope":       scope,
		"target_type": flag(args, "target-type", ""),
		"target_id":   flag(args, "target-id", ""),
		"reason":      flag(args, "reason", ""),
		"expires_at":  flag(args, "expires-at", ""),
		"metadata":    map[string]any{"channel": "cli", "command": "approvals request"},
	}
	if requestedBy := strings.TrimSpace(flag(args, "requested-by", "")); requestedBy != "" {
		body["requested_by"] = requestedBy
	}
	if payloadJSON := flag(args, "payload-json", ""); payloadJSON != "" {
		payload, err := parseJSONObjectFlag(payloadJSON, "payload-json")
		if err != nil {
			return err
		}
		body["payload"] = payload
	}
	if metadataJSON := flag(args, "metadata-json", ""); metadataJSON != "" {
		metadata, err := parseJSONObjectFlag(metadataJSON, "metadata-json")
		if err != nil {
			return err
		}
		body["metadata"] = mergeAnyMaps(body["metadata"].(map[string]any), metadata)
	}
	result, err := postJSON(ctx, args, "/approvals", body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	approval, _ := result["approval"].(map[string]any)
	id := stringValue(approval["id"], "unknown")
	fmt.Println("Approval requested: " + id)
	fmt.Println("scope: " + stringValue(approval["scope"], scope))
	fmt.Println("action: " + stringValue(approval["action"], action))
	fmt.Println("status: " + stringValue(approval["status"], "pending"))
	fmt.Println("Use: abra approvals approve " + id)
	return nil
}

func decideApproval(ctx context.Context, args cliArgs, action string) error {
	approvalID := firstNonEmpty(flag(args, "approval-id", ""), flag(args, "id", ""))
	if approvalID == "" && len(args.Rest) > 0 {
		approvalID = strings.TrimSpace(args.Rest[0])
		args.Rest = args.Rest[1:]
	}
	if approvalID == "" {
		return fmt.Errorf("approvals %s requires an approval id, for example: abra approvals %s approval-123", action, action)
	}
	decisionReason := firstNonEmpty(flag(args, "decision-reason", ""), flag(args, "reason", ""))
	body := map[string]any{
		"decision_reason": decisionReason,
		"metadata":        map[string]any{"channel": "cli", "command": "approvals " + action},
	}
	if decidedBy := strings.TrimSpace(flag(args, "decided-by", "")); decidedBy != "" {
		body["decided_by"] = decidedBy
	}
	if metadataJSON := flag(args, "metadata-json", ""); metadataJSON != "" {
		metadata, err := parseJSONObjectFlag(metadataJSON, "metadata-json")
		if err != nil {
			return err
		}
		body["metadata"] = mergeAnyMaps(body["metadata"].(map[string]any), metadata)
	}
	result, err := postJSON(ctx, args, "/approvals/"+url.PathEscape(approvalID)+"/"+action, body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	approval, _ := result["approval"].(map[string]any)
	status := stringValue(approval["status"], "")
	if status == "" {
		status = action + "d"
	}
	fmt.Printf("Approval %s: %s\n", status, approvalID)
	return nil
}
