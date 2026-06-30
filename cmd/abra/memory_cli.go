package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	memorypkg "github.com/hermawan22/abra/internal/memory"
)

func think(ctx context.Context, args cliArgs) error {
	question := strings.TrimSpace(strings.Join(args.Rest, " "))
	if question == "" {
		question = flag(args, "question", "")
	}
	if question == "" {
		label := "think"
		if args.Command == "ask" {
			label = "ask"
		}
		return fmt.Errorf("%s requires a question, for example: abra %s \"what should I know?\"", label, label)
	}
	scope := scopeOrDefault(args, ".")
	mode, err := retrievalModeFlag(args)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"question":           question,
		"scope":              scope,
		"agent":              flag(args, "agent", ""),
		"mode":               mode,
		"entity":             flag(args, "entity", ""),
		"as_of":              normalizedAsOfFlag(args),
		"include_historical": boolFlag(args, "include-historical"),
		"synthesize":         boolFlag(args, "synthesize"),
		"limit":              intFlag(args, "limit", 5),
		"max_queries":        intFlag(args, "max-queries", 4),
		"include_unverified": boolFlag(args, "include-unverified"),
	}
	if hasFlag(args, "token-budget") {
		payload["token_budget"] = intFlag(args, "token-budget", 0)
	}
	result, err := callMCPTool(ctx, args, "brain_think", payload)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	printThink(result, humanOutputMode(args))
	return nil
}

func recall(ctx context.Context, args cliArgs) error {
	query := strings.TrimSpace(strings.Join(args.Rest, " "))
	if query == "" {
		query = flag(args, "query", "")
	}
	if query == "" {
		return errors.New("recall requires a query, for example: abra recall \"agent memory\"")
	}
	scope := scopeOrDefault(args, ".")
	result, err := callMCPTool(ctx, args, "recall", map[string]any{
		"query":              query,
		"scope":              scope,
		"limit":              intFlag(args, "limit", 5),
		"include_unverified": boolFlag(args, "include-unverified"),
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	claims, _ := result["claims"].([]any)
	fmt.Printf("Recall: %d claims\n", len(claims))
	for i, raw := range claims {
		if i >= 8 {
			break
		}
		claim, _ := raw.(map[string]any)
		fmt.Printf("- %s (%s)\n", stringValue(claim["claim_text"], ""), stringValue(claim["status"], "unknown"))
	}
	return nil
}

func composeMemory(ctx context.Context, args cliArgs) error {
	task := strings.TrimSpace(strings.Join(args.Rest, " "))
	if task == "" {
		task = flag(args, "task", "")
	}
	if task == "" {
		label := "compose"
		if args.Command == "context" {
			label = "context"
		}
		return fmt.Errorf("%s requires a task, for example: abra %s \"ship a change\"", label, label)
	}
	scope := scopeOrDefault(args, ".")
	mode, err := retrievalModeFlag(args)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"task":               task,
		"scope":              scope,
		"hook":               flag(args, "hook", "before_task"),
		"agent":              flag(args, "agent", ""),
		"mode":               mode,
		"entity":             flag(args, "entity", ""),
		"files":              stringListFlag(args, "files", "file"),
		"changed_files":      stringListFlag(args, "changed-files", "changed-file"),
		"language":           flag(args, "language", ""),
		"as_of":              normalizedAsOfFlag(args),
		"include_historical": boolFlag(args, "include-historical"),
		"limit":              intFlag(args, "limit", 5),
		"max_queries":        intFlag(args, "max-queries", 4),
		"include_unverified": boolFlag(args, "include-unverified"),
		"persist_learning":   boolFlag(args, "persist-learning"),
	}
	if hasFlag(args, "token-budget") {
		payload["token_budget"] = intFlag(args, "token-budget", 0)
	}
	result, err := callMCPTool(ctx, args, "working_memory_compose", payload)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	outputMode := humanOutputMode(args)
	if outputMode == "brief" {
		printComposeBrief(result, args.Command)
		return nil
	}
	if outputMode == "agent" {
		printComposeAgentOutput(result, args.Command)
		return nil
	}
	verification, _ := result["verification"].(map[string]any)
	decision, _ := result["agent_decision"].(map[string]any)
	stats, _ := result["stats"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	scope = stringValue(result["scope"], scope)
	label := "Compose"
	if args.Command == "context" {
		label = "Context"
	}
	fmt.Printf("%s: %s / %s\n", label, stringValue(verification["verdict"], "unknown"), stringValue(decision["decision"], "unknown"))
	fmt.Println("scope: " + scope)
	if len(stats) > 0 {
		fmt.Printf("context: facts=%d documents=%d summaries=%d graph=%d blocks=%d\n",
			intValue(stats["facts"]),
			intValue(stats["supporting_documents"]),
			intValue(stats["summaries"]),
			intValue(stats["graph_relations"]),
			intValue(stats["context_blocks"]),
		)
	}
	if len(health) > 0 {
		fmt.Printf("health: %s score=%d signals=%d\n", stringValue(health["status"], "unknown"), intValue(health["score"]), lenSlice(health["signals"]))
		if signals, _ := health["signals"].([]any); len(signals) > 0 {
			fmt.Println("health signals:")
			printComposeSignals(signals, 5)
		}
	}
	if quality, _ := verification["retrieval_quality"].(map[string]any); len(quality) > 0 {
		fmt.Printf("retrieval: results=%d sources=%d low_confidence=%t low_source_diversity=%t\n",
			intValue(quality["result_count"]),
			intValue(quality["unique_sources"]),
			boolValue(quality["low_confidence"], false),
			boolValue(quality["low_source_diversity"], false),
		)
	}
	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Println("citations:")
		for i, raw := range citations {
			if i >= 5 {
				fmt.Printf("- +%d more\n", len(citations)-i)
				break
			}
			item, _ := raw.(map[string]any)
			fmt.Printf("- %s: %s\n", stringValue(item["ref"], "?"), stringValue(item["source_url"], "unknown"))
		}
	}
	if evidence, _ := result["evidence"].([]any); len(evidence) > 0 {
		fmt.Println("evidence:")
		for i, raw := range evidence {
			if i >= 5 {
				fmt.Printf("- +%d more\n", len(evidence)-i)
				break
			}
			item, _ := raw.(map[string]any)
			ref := stringValue(item["ref"], "")
			if ref != "" {
				ref = "[" + ref + "] "
			}
			fmt.Printf("- %s%s (%d)\n", ref, stringValue(item["source_url"], "unknown"), intValue(item["count"]))
		}
	}
	if actions, ok := decision["required_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("required actions:")
		for _, action := range actions {
			fmt.Println("- " + stringValue(action, ""))
		}
	}
	if actions, ok := decision["allowed_next_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("allowed next actions:")
		printComposeStringList(actions, 6)
	}
	if validation, ok := result["validation_plan"].([]any); ok && len(validation) > 0 {
		fmt.Println("validation plan:")
		printComposeValidationPlan(validation, 6)
	}
	if steps, ok := result["suggested_steps"].([]any); ok && len(steps) > 0 {
		fmt.Println("suggested steps:")
		printComposeStringList(steps, 5)
	}
	if boolFlag(args, "prompt") {
		window, _ := result["context_window"].(map[string]any)
		prompt := stringValue(window["prompt"], "")
		if prompt == "" {
			fmt.Println("prompt-ready context: unavailable")
		} else {
			fmt.Println("prompt-ready context:")
			fmt.Println(prompt)
		}
	}
	if len(stats) > 0 && intValue(stats["facts"])+intValue(stats["supporting_documents"])+intValue(stats["summaries"])+intValue(stats["graph_relations"]) == 0 {
		fmt.Println("No source-backed context found for this scope.")
		if composeResultHasReadinessWarning(result) {
			fmt.Println("Retrieval or memory-health warnings are present; run `abra doctor` and `abra model status` before syncing again.")
		} else {
			fmt.Println("Confirm the project scope: abra scope")
			fmt.Println("Then sync the project with that exact scope: abra sync . --code --scope " + scope)
		}
	}
	return nil
}

func printComposeBrief(result map[string]any, command string) {
	verification, _ := result["verification"].(map[string]any)
	decision, _ := result["agent_decision"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	stats, _ := result["stats"].(map[string]any)
	label := "Compose"
	if command == "context" {
		label = "Context"
	}
	fmt.Printf("%s: %s / %s\n", label, stringValue(verification["verdict"], "unknown"), stringValue(decision["decision"], "unknown"))
	fmt.Printf("Trust: scope=%s health=%s score=%d conflicts=%d risks=%d\n",
		stringValue(result["scope"], ""),
		stringValue(health["status"], "unknown"),
		intValue(health["score"]),
		lenSlice(result["conflicts"]),
		lenSlice(result["risks"]),
	)
	if len(stats) > 0 {
		fmt.Printf("Context: facts=%d documents=%d summaries=%d graph=%d\n",
			intValue(stats["facts"]),
			intValue(stats["supporting_documents"]),
			intValue(stats["summaries"]),
			intValue(stats["graph_relations"]),
		)
	}
	if validation := lenSlice(result["validation_plan"]); validation > 0 {
		fmt.Printf("Validation: %d step(s)\n", validation)
	}
}

func printComposeAgentOutput(result map[string]any, command string) {
	verification, _ := result["verification"].(map[string]any)
	decision, _ := result["agent_decision"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	stats, _ := result["stats"].(map[string]any)
	label := "Working memory handoff"
	if command == "context" {
		label = "Context handoff"
	}
	fmt.Println(label)
	fmt.Println("Task: " + stringValue(result["task"], ""))
	fmt.Println()
	fmt.Println("Trust")
	fmt.Printf("- scope=%s verdict=%s health=%s score=%d decision=%s conflicts=%d risks=%d\n",
		stringValue(result["scope"], ""),
		stringValue(verification["verdict"], "unknown"),
		stringValue(health["status"], "unknown"),
		intValue(health["score"]),
		stringValue(decision["decision"], "unknown"),
		lenSlice(result["conflicts"]),
		lenSlice(result["risks"]),
	)
	if len(stats) > 0 {
		fmt.Printf("- context: facts=%d documents=%d summaries=%d graph=%d blocks=%d\n",
			intValue(stats["facts"]),
			intValue(stats["supporting_documents"]),
			intValue(stats["summaries"]),
			intValue(stats["graph_relations"]),
			intValue(stats["context_blocks"]),
		)
	}
	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Println()
		fmt.Println("Evidence")
		printCitationList(citations, 5)
	}
	if risks, _ := result["risks"].([]any); len(risks) > 0 {
		fmt.Println()
		fmt.Println("Risks")
		printComposeStringList(risks, 5)
	}
	if validation, ok := result["validation_plan"].([]any); ok && len(validation) > 0 {
		fmt.Println()
		fmt.Println("Validation")
		printComposeValidationPlan(validation, 6)
	}
	if actions, ok := decision["required_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println()
		fmt.Println("Required")
		printComposeStringList(actions, 6)
	}
	if actions, ok := decision["allowed_next_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println()
		fmt.Println("Allowed next")
		printComposeStringList(actions, 6)
	}
	if steps, ok := result["suggested_steps"].([]any); ok && len(steps) > 0 {
		fmt.Println()
		fmt.Println("Suggested")
		printComposeStringList(steps, 5)
	}
}

func memoryCommand(ctx context.Context, args cliArgs) error {
	action := "status"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "status", "health", "doctor":
		return memoryStatus(ctx, args, action == "doctor")
	default:
		return fmt.Errorf("unknown memory command %q\n\n%s", action, commandUsage("memory"))
	}
}

func governCommand(ctx context.Context, args cliArgs) error {
	action := "status"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "status", "health":
		return memoryStatus(ctx, args, false)
	case "doctor":
		return memoryStatus(ctx, args, true)
	case "approvals", "approval":
		return approvalsCommand(ctx, args)
	case "learning", "proposal", "proposals":
		return governLearningCommand(ctx, args)
	case "observe":
		return observe(ctx, args)
	case "observations", "episodes":
		return listObservations(ctx, args)
	default:
		return fmt.Errorf("unknown govern command %q\n\n%s", action, commandUsage("govern"))
	}
}

func memoryStatus(ctx context.Context, args cliArgs, doctor bool) error {
	scope := scopeOrDefault(args, ".")
	result, _, err := getJSON(ctx, args, "/memory/health?scope="+urlQueryEscape(scope))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	status := stringValue(result["status"], "unknown")
	score := intValue(result["score"])
	fmt.Printf("Memory: %s", status)
	if score > 0 {
		fmt.Printf(" score=%d", score)
	}
	fmt.Println()
	fmt.Println("scope: " + scope)
	printMemoryHealthSection("documents", result["documents"], []string{"total", "active", "stale", "deprecated", "deleted"})
	printMemoryHealthSection("claims", result["claims"], []string{"total", "verified", "inferred", "unverified", "challenged", "deprecated", "expired", "stale", "with_evidence", "trusted_from_code_documents"})
	printMemoryHealthSection("graph", result["graph"], []string{"entities", "active_entities", "relations", "active_relations", "challenged_relations", "stale_relations"})
	printMemorySourceSection(result["sources"])
	printMemoryHealthSection("ingestion", result["ingestion"], []string{"queued_jobs", "running_jobs", "retry_jobs", "failed_jobs", "stale_running_jobs"})
	printMemoryHealthSection("conflicts", result["conflicts"], []string{"total", "open", "reviewing", "blocking", "high"})
	printMemoryHealthSection("learning", result["learning"], []string{"total", "pending", "accepted", "applied", "rejected", "duplicate_pending_groups"})
	sourceDiagnostics := memorySourceDiagnosticItems(result)
	sourceHints := memorySourceHints(result)
	if doctor {
		printMemorySourceDiagnostics(sourceDiagnostics, sourceHints, scope)
	} else if len(sourceDiagnostics) > 0 || len(sourceHints) > 0 {
		fmt.Println("Run `abra brain doctor --scope " + scope + "` for source diagnostics.")
	}
	signals, _ := result["signals"].([]any)
	if len(signals) == 0 {
		fmt.Println("signals: none")
		return nil
	}
	fmt.Println("signals:")
	limit := 8
	if doctor {
		limit = len(signals)
	}
	printComposeSignals(signals, limit)
	if !doctor && len(signals) > limit {
		fmt.Println("Run `abra brain doctor --scope " + scope + "` for all health signals.")
	}
	return nil
}

func printMemoryHealthSection(name string, raw any, keys []string) {
	section, _ := raw.(map[string]any)
	if len(section) == 0 {
		return
	}
	parts := []string{}
	for _, key := range keys {
		if part, ok := memoryHealthSectionPart(key, section[key]); ok {
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 {
		fmt.Println(name + ": " + strings.Join(parts, " "))
	}
}

func printMemorySourceSection(raw any) {
	section, _ := raw.(map[string]any)
	if len(section) == 0 {
		return
	}
	keys := []string{"total", "active", "healthy", "unhealthy", "paused", "disabled", "due", "overdue", "refresh_due", "refresh_overdue", "error", "errors", "failed"}
	skip := map[string]bool{
		"diagnostics":        true,
		"details":            true,
		"hints":              true,
		"items":              true,
		"remediation_hints":  true,
		"source_diagnostics": true,
		"sources":            true,
		"unhealthy_sources":  true,
	}
	seen := map[string]bool{}
	parts := []string{}
	for _, key := range keys {
		seen[key] = true
		if part, ok := memoryHealthSectionPart(key, section[key]); ok {
			parts = append(parts, part)
		}
	}
	extraKeys := make([]string, 0, len(section))
	for key := range section {
		if seen[key] || skip[key] {
			continue
		}
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		if part, ok := memoryHealthSectionPart(key, section[key]); ok {
			parts = append(parts, part)
		}
	}
	if len(parts) > 0 {
		fmt.Println("sources: " + strings.Join(parts, " "))
	}
}

func memoryHealthSectionPart(key string, value any) (string, bool) {
	switch typed := value.(type) {
	case float64:
		return fmt.Sprintf("%s=%d", key, int(typed)), true
	case int:
		return fmt.Sprintf("%s=%d", key, typed), true
	case json.Number:
		return fmt.Sprintf("%s=%d", key, intValue(typed)), true
	case string:
		if strings.TrimSpace(typed) != "" {
			return key + "=" + typed, true
		}
	case bool:
		return fmt.Sprintf("%s=%t", key, typed), true
	}
	return "", false
}

func memorySourceDiagnosticItems(result map[string]any) []any {
	items := []any{}
	for _, key := range []string{"source_diagnostics", "source_health", "unhealthy_sources", "source_details"} {
		items = append(items, memorySourceDiagnosticItemsFromRaw(result[key])...)
	}
	if sources, _ := result["sources"].(map[string]any); len(sources) > 0 {
		for _, key := range []string{"diagnostics", "details", "items", "source_diagnostics", "unhealthy_sources", "unhealthy"} {
			items = append(items, memorySourceDiagnosticItemsFromRaw(sources[key])...)
		}
	}
	return items
}

func memorySourceDiagnosticItemsFromRaw(raw any) []any {
	switch typed := raw.(type) {
	case []any:
		return typed
	case map[string]any:
		if looksLikeSourceDiagnostic(typed) {
			return []any{typed}
		}
		items := []any{}
		for _, key := range []string{"diagnostics", "details", "items", "sources", "source_diagnostics", "unhealthy_sources", "unhealthy"} {
			items = append(items, memorySourceDiagnosticItemsFromRaw(typed[key])...)
		}
		return items
	case string:
		if strings.TrimSpace(typed) != "" {
			return []any{typed}
		}
	}
	return nil
}

func looksLikeSourceDiagnostic(item map[string]any) bool {
	for _, key := range []string{"id", "source_config_id", "source_id", "source_url", "name", "title"} {
		if strings.TrimSpace(stringValue(item[key], "")) != "" {
			return true
		}
	}
	for _, key := range []string{"last_error", "error", "message", "reason", "detail", "remediation", "hint", "action"} {
		if strings.TrimSpace(stringValue(item[key], "")) != "" {
			return true
		}
	}
	return false
}

func memorySourceHints(result map[string]any) []any {
	hints := []any{}
	for _, key := range []string{"source_remediation_hints", "remediation_hints", "source_hints"} {
		hints = appendStringItems(hints, result[key])
	}
	if sources, _ := result["sources"].(map[string]any); len(sources) > 0 {
		for _, key := range []string{"remediation_hints", "hints"} {
			hints = appendStringItems(hints, sources[key])
		}
	}
	return hints
}

func appendStringItems(items []any, raw any) []any {
	switch typed := raw.(type) {
	case []any:
		return append(items, typed...)
	case string:
		if strings.TrimSpace(typed) != "" {
			return append(items, typed)
		}
	}
	return items
}

func printMemorySourceDiagnostics(items []any, hints []any, scope string) {
	if len(items) == 0 && len(hints) == 0 {
		return
	}
	fmt.Println("source diagnostics:")
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if len(item) == 0 {
			if text := strings.TrimSpace(stringValue(raw, "")); text != "" {
				fmt.Println("- " + text)
			}
			continue
		}
		sourceIDForCommand := firstNonEmpty(
			stringValue(item["id"], ""),
			stringValue(item["source_config_id"], ""),
			stringValue(item["source_id"], ""),
		)
		sourceLabel := firstNonEmpty(
			sourceIDForCommand,
			stringValue(item["name"], ""),
			stringValue(item["source_url"], ""),
			"unknown",
		)
		labels := []string{}
		if sourceType := firstNonEmpty(stringValue(item["type"], ""), stringValue(item["source_type"], ""), stringValue(item["connector_kind"], "")); sourceType != "" {
			labels = append(labels, sourceType)
		}
		if status := firstNonEmpty(stringValue(item["status"], ""), stringValue(item["health"], ""), stringValue(item["severity"], "")); status != "" {
			labels = append(labels, status)
		}
		line := "- " + sourceLabel
		if len(labels) > 0 {
			line += " (" + strings.Join(labels, ", ") + ")"
		}
		if message := firstNonEmpty(stringValue(item["message"], ""), stringValue(item["reason"], ""), stringValue(item["detail"], ""), stringValue(item["last_error"], ""), stringValue(item["error"], "")); message != "" {
			line += ": " + message
		}
		fmt.Println(line)
		for _, key := range []string{"source_url", "last_success_at", "last_error_at", "next_due_at"} {
			if value := strings.TrimSpace(stringValue(item[key], "")); value != "" && value != sourceLabel {
				fmt.Println("  " + key + ": " + value)
			}
		}
		for _, key := range []string{"remediation_hint", "remediation", "hint", "action"} {
			if value := strings.TrimSpace(stringValue(item[key], "")); value != "" {
				fmt.Println("  " + key + ": " + value)
			}
		}
		for _, key := range []string{"remediation_hints", "next_steps", "commands", "actions"} {
			printIndentedStringList(key, item[key])
		}
		if sourceIDForCommand != "" {
			fmt.Println("  inspect: abra connect status " + shellQuote(sourceIDForCommand))
			fmt.Println("  logs: abra connect logs " + shellQuote(sourceIDForCommand) + " --scope " + shellQuote(scope))
		}
	}
	if len(hints) > 0 {
		fmt.Println("source remediation:")
		printComposeStringList(hints, len(hints))
	}
}

func printIndentedStringList(name string, raw any) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return
	}
	fmt.Println("  " + name + ":")
	for _, rawItem := range items {
		if text := strings.TrimSpace(stringValue(rawItem, "")); text != "" {
			fmt.Println("  - " + text)
		}
	}
}

func printComposeSignals(signals []any, limit int) {
	for i, raw := range signals {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(signals)-i)
			return
		}
		signal, _ := raw.(map[string]any)
		code := stringValue(signal["code"], "unknown")
		severity := stringValue(signal["severity"], "unknown")
		action := stringValue(signal["action"], "")
		count := intValue(signal["count"])
		line := "- " + code + " (" + severity
		if count > 0 {
			line += fmt.Sprintf(", count=%d", count)
		}
		line += ")"
		if action != "" {
			line += " -> " + action
		}
		fmt.Println(line)
	}
}

func printComposeStringList(values []any, limit int) {
	for i, raw := range values {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(values)-i)
			return
		}
		text := strings.TrimSpace(stringValue(raw, ""))
		if text == "" {
			continue
		}
		fmt.Println("- " + text)
	}
}

func printComposeValidationPlan(values []any, limit int) {
	for i, raw := range values {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(values)-i)
			return
		}
		item, _ := raw.(map[string]any)
		label := firstNonEmpty(stringValue(item["command"], ""), stringValue(item["type"], ""), stringValue(item["name"], "validation"))
		if boolValue(item["required"], false) {
			label = "required " + label
		} else {
			label = "optional " + label
		}
		reason := strings.TrimSpace(stringValue(item["reason"], ""))
		if reason != "" {
			label += " - " + reason
		}
		fmt.Println("- " + label)
	}
}

func humanOutputMode(args cliArgs) string {
	if boolFlag(args, "brief") {
		return "brief"
	}
	if boolFlag(args, "agent-output") || boolFlag(args, "agent-handoff") {
		return "agent"
	}
	mode := strings.ToLower(strings.TrimSpace(flag(args, "output", "")))
	switch mode {
	case "brief", "agent", "agent-output", "handoff", "full":
		if mode == "agent-output" || mode == "handoff" {
			return "agent"
		}
		return mode
	default:
		return "full"
	}
}

func retrievalModeFlag(args cliArgs) (string, error) {
	raw := strings.TrimSpace(flag(args, "mode", "balanced"))
	if raw == "" {
		raw = "balanced"
	}
	mode := string(memorypkg.NormalizeRetrievalMode(raw))
	if mode != strings.ToLower(raw) && strings.ToLower(raw) != "balanced" {
		return "", fmt.Errorf("invalid --mode %q, want fast, balanced, or deep", raw)
	}
	return mode, nil
}

func printThink(result map[string]any, mode string) {
	fmt.Println()
	if mode == "brief" {
		printThinkBrief(result)
		if thinkNeedsRecovery(result) {
			printThinkRecovery(stringValue(result["scope"], ""))
		}
		return
	}
	fmt.Println("Answer")
	fmt.Println(cleanThinkAnswer(stringValue(result["answer"], "No answer.")))

	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Println()
		fmt.Println("Evidence")
		printCitationList(citations, 5)
	}
	printThinkTrust(result)
	printThinkSynthesis(result)
	printThinkGaps(result)
	printThinkConflicts(result)
	printThinkNextActions(result)
	printThinkAgentGate(result)

	if thinkNeedsRecovery(result) {
		printThinkRecovery(stringValue(result["scope"], ""))
	}
}

func printThinkBrief(result map[string]any) {
	fmt.Println("Answer")
	fmt.Println(firstLine(cleanThinkAnswer(stringValue(result["answer"], "No answer."))))
	verification, _ := result["verification"].(map[string]any)
	decision, _ := result["agent_decision"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	fmt.Printf("Trust: scope=%s verdict=%s health=%s decision=%s\n",
		stringValue(result["scope"], ""),
		stringValue(verification["verdict"], "unknown"),
		stringValue(health["status"], "unknown"),
		stringValue(decision["decision"], "unknown"),
	)
	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Printf("Evidence: %d source(s)\n", len(citations))
	}
	if synthesis, _ := result["synthesis"].(map[string]any); len(synthesis) > 0 {
		if status := stringValue(synthesis["status"], ""); status != "" {
			fmt.Printf("Synthesis: %s\n", status)
		}
	}
	if gaps := lenSlice(result["gaps"]); gaps > 0 {
		fmt.Printf("Gaps: %d\n", gaps)
	}
	if conflicts := lenSlice(result["conflicts"]); conflicts > 0 {
		fmt.Printf("Conflicts: %d\n", conflicts)
	}
}

func cleanThinkAnswer(answer string) string {
	answer = strings.TrimSpace(answer)
	answer = strings.TrimPrefix(answer, "Abra's governed answer:\n")
	answer = strings.TrimPrefix(answer, "Abra's governed answer:")
	return strings.TrimSpace(answer)
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if before, _, ok := strings.Cut(value, "\n"); ok {
		return strings.TrimSpace(before)
	}
	return value
}

func printThinkTrust(result map[string]any) {
	verification, _ := result["verification"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	quality, _ := verification["retrieval_quality"].(map[string]any)
	scope := stringValue(result["scope"], "")
	verdict := stringValue(verification["verdict"], "unknown")
	healthStatus := stringValue(health["status"], "unknown")
	score := intValue(health["score"])
	conflicts := lenSlice(result["conflicts"])
	gaps := lenSlice(result["gaps"])

	fmt.Println()
	fmt.Println("Trust")
	line := fmt.Sprintf("- scope=%s verdict=%s health=%s", scope, verdict, healthStatus)
	if score > 0 {
		line += fmt.Sprintf(" score=%d", score)
	}
	line += fmt.Sprintf(" conflicts=%d gaps=%d", conflicts, gaps)
	fmt.Println(line)
	if len(quality) > 0 {
		fmt.Printf("- retrieval: results=%d sources=%d low_confidence=%t low_source_diversity=%t\n",
			intValue(quality["result_count"]),
			intValue(quality["unique_sources"]),
			boolValue(quality["low_confidence"], false),
			boolValue(quality["low_source_diversity"], false),
		)
	}
}

func printThinkSynthesis(result map[string]any) {
	synthesis, _ := result["synthesis"].(map[string]any)
	if len(synthesis) == 0 {
		return
	}
	status := stringValue(synthesis["status"], "")
	if status == "" {
		return
	}
	fmt.Println()
	fmt.Println("Synthesis")
	line := "- status=" + status
	if provider := stringValue(synthesis["provider"], ""); provider != "" {
		line += " provider=" + provider
	}
	if model := stringValue(synthesis["model"], ""); model != "" {
		line += " model=" + model
	}
	fmt.Println(line)
	if warning := stringValue(synthesis["warning"], ""); warning != "" {
		fmt.Println("- warning: " + warning)
	}
}

func printThinkAgentGate(result map[string]any) {
	decision, _ := result["agent_decision"].(map[string]any)
	if len(decision) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Agent handoff")
	if question := stringValue(result["question"], ""); question != "" {
		fmt.Println("- question: " + question)
	}
	line := "- decision=" + stringValue(decision["decision"], "unknown")
	if autonomous := boolValue(decision["autonomous_allowed"], false); autonomous {
		line += " autonomous_allowed=true"
	}
	if review := boolValue(decision["review_required"], false); review {
		line += " review_required=true"
	}
	fmt.Println(line)
	if actions, ok := decision["required_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("  required:")
		printThinkIndentedStringList(actions, 5)
	}
	if actions, ok := decision["allowed_next_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("  allowed next:")
		printThinkIndentedStringList(actions, 5)
	}
}

func printThinkGaps(result map[string]any) {
	gaps, _ := result["gaps"].([]any)
	if len(gaps) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Gaps")
	for i, raw := range gaps {
		if i >= 5 {
			fmt.Printf("- +%d more\n", len(gaps)-i)
			return
		}
		gap, _ := raw.(map[string]any)
		code := stringValue(gap["code"], "gap")
		severity := stringValue(gap["severity"], "unknown")
		message := stringValue(gap["message"], "")
		action := stringValue(gap["suggested_action"], "")
		line := "- " + code + " (" + severity + ")"
		if message != "" {
			line += ": " + message
		}
		if action != "" {
			line += " -> " + action
		}
		fmt.Println(line)
	}
}

func printThinkConflicts(result map[string]any) {
	conflicts, _ := result["conflicts"].([]any)
	if len(conflicts) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Conflicts")
	for i, raw := range conflicts {
		if i >= 5 {
			fmt.Printf("- +%d more\n", len(conflicts)-i)
			return
		}
		conflict, _ := raw.(map[string]any)
		severity := stringValue(conflict["severity"], "unknown")
		conflictType := firstNonEmpty(stringValue(conflict["conflict_type"], ""), stringValue(conflict["type"], ""), "conflict")
		reason := firstNonEmpty(stringValue(conflict["reason"], ""), stringValue(conflict["status"], ""))
		line := "- " + conflictType + " (" + severity + ")"
		if reason != "" {
			line += ": " + reason
		}
		fmt.Println(line)
	}
}

func printThinkNextActions(result map[string]any) {
	actions, _ := result["next_actions"].([]any)
	if len(actions) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Next")
	printComposeStringList(actions, 6)
}

func printCitationList(citations []any, limit int) {
	for i, raw := range citations {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(citations)-i)
			return
		}
		item, _ := raw.(map[string]any)
		ref := stringValue(item["ref"], "?")
		title := firstNonEmpty(stringValue(item["title"], ""), stringValue(item["kind"], ""), "source")
		source := stringValue(item["source_url"], "unknown")
		fmt.Printf("- [%s] %s - %s\n", ref, title, source)
	}
}

func printThinkIndentedStringList(values []any, limit int) {
	for i, raw := range values {
		if i >= limit {
			fmt.Printf("  - +%d more\n", len(values)-i)
			return
		}
		text := strings.TrimSpace(stringValue(raw, ""))
		if text == "" {
			continue
		}
		fmt.Println("  - " + text)
	}
}

func thinkNeedsRecovery(result map[string]any) bool {
	if citations, _ := result["citations"].([]any); len(citations) == 0 {
		return true
	}
	verification, _ := result["verification"].(map[string]any)
	if verdict := strings.ToLower(strings.TrimSpace(stringValue(verification["verdict"], ""))); verdict != "" && verdict != "strong" {
		return true
	}
	decision, _ := result["agent_decision"].(map[string]any)
	if value := strings.ToLower(strings.TrimSpace(stringValue(decision["decision"], ""))); value != "" && value != "proceed" {
		return true
	}
	return false
}

func stringListFlag(args cliArgs, names ...string) []string {
	values := []string{}
	for _, name := range names {
		raw := flag(args, name, "")
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				values = append(values, part)
			}
		}
	}
	return values
}

func normalizedAsOfFlag(args cliArgs) string {
	return normalizeAsOfInput(flag(args, "as-of", ""))
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

func printThinkRecovery(scope string) {
	if strings.TrimSpace(scope) == "" {
		scope = "<scope-from-abra-scope>"
	}
	fmt.Println("next:")
	fmt.Println("- abra scope")
	fmt.Println("- abra agent verify . --scope " + shellQuote(scope) + " --json")
	fmt.Println("- abra doctor")
	fmt.Println("- abra sync . --code --scope " + shellQuote(scope) + "   # only if verify reports missing scope or empty source-backed memory")
}
