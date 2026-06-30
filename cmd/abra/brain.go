package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	memorypkg "github.com/hermawan22/abra/internal/memory"
)

type brainEvalSuite struct {
	Cases []memorypkg.BrainEvalCase `json:"cases"`
}

const canonicalBrainEvalSuiteJSON = `{
  "cases": [
    {
      "name": "anchored answer passes",
      "question": "What answer is safe to cite from governed memory?",
      "scope": "repo:abra",
      "min_verdict": "partial",
      "require_decision": "proceed",
      "require_anchored_claim": true
    },
    {
      "name": "answer keeps citations",
      "question": "What should an agent do before changing this repo?",
      "scope": "repo:abra",
      "min_verdict": "partial",
      "require_answer_text": ["Evidence"]
    },
    {
      "name": "entity dossier recall",
      "question": "What does Abra remember about Abra as an entity?",
      "scope": "repo:abra",
      "min_verdict": "partial",
      "require_answer_text": ["Entity dossier"],
      "require_entity_dossier": "Abra",
      "min_entity_active_claims": 1
    },
    {
      "name": "decision gate is present",
      "question": "What should Abra tell an agent before it uses governed memory?",
      "scope": "repo:abra",
      "min_verdict": "partial",
      "require_decision": "proceed",
      "require_answer_text": ["Decision gate"]
    },
    {
      "name": "memory health is usable",
      "question": "Is the repo brain memory healthy enough for agent use?",
      "scope": "repo:abra",
      "min_verdict": "partial",
      "require_memory_health": "healthy"
    },
    {
      "name": "citations and anchors are present",
      "question": "Which source-backed evidence can Abra cite for this repo?",
      "scope": "repo:abra",
      "min_citations": 1,
      "min_evidence_anchors": 1
    },
    {
      "name": "deterministic synthesis status",
      "question": "What answer can be produced without LLM synthesis?",
      "scope": "repo:abra",
      "require_synthesis": "disabled"
    },
    {
      "name": "context window respects default token budget",
      "question": "What repo brain context should fit inside the default bounded context window?",
      "scope": "repo:abra",
      "max_context_tokens": 1600
    }
  ]
}`

func evalCommand(ctx context.Context, args cliArgs) error {
	action := "brain"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "brain", "think":
		return evalBrain(ctx, args)
	default:
		return fmt.Errorf("unknown eval command %q\n\n%s", action, commandUsage("eval"))
	}
}

func evalBrain(ctx context.Context, args cliArgs) error {
	raw, err := loadBrainEvalSuite(args)
	if err != nil {
		return err
	}
	var suite brainEvalSuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		return fmt.Errorf("decode eval suite: %w", err)
	}
	if len(suite.Cases) == 0 {
		return errors.New("eval suite has no cases")
	}
	reports := []memorypkg.BrainEvalReport{}
	allPassed := true
	passed := 0
	for _, tc := range suite.Cases {
		resultMap, err := callMCPTool(ctx, args, "brain_think", map[string]any{
			"question":    tc.Question,
			"scope":       tc.Scope,
			"agent":       flag(args, "agent", ""),
			"synthesize":  boolFlag(args, "synthesize"),
			"limit":       intFlag(args, "limit", 5),
			"max_queries": intFlag(args, "max-queries", 4),
		})
		if err != nil {
			return err
		}
		var result memorypkg.ThinkResult
		encoded, _ := json.Marshal(resultMap)
		if err := json.Unmarshal(encoded, &result); err != nil {
			return fmt.Errorf("decode brain think result for eval case %q: %w", tc.Name, err)
		}
		report := memorypkg.EvaluateThinkResult(tc, result)
		reports = append(reports, report)
		if !report.Passed {
			allPassed = false
		} else {
			passed++
		}
	}
	run, persistErr := persistBrainEvalRun(ctx, args, suite, reports, passed, allPassed)
	if boolFlag(args, "json") {
		payload := map[string]any{"passed": allPassed, "reports": reports}
		if run != nil {
			payload["run"] = run
		}
		if persistErr != nil {
			payload["history_warning"] = persistErr.Error()
		}
		return printJSON(payload)
	}
	for _, report := range reports {
		status := "fail"
		if report.Passed {
			status = "pass"
		}
		fmt.Printf("%s  %s  score=%.2f\n", status, report.Name, report.Score)
		for _, check := range report.Checks {
			if check.Passed {
				continue
			}
			fmt.Printf("  - %s: %s\n", check.Name, check.Message)
		}
	}
	fmt.Printf("Brain eval: %d/%d passed\n", passed, len(reports))
	if run != nil {
		fmt.Println("history: " + stringValue(run["id"], "persisted"))
	} else if persistErr != nil {
		fmt.Println("history: not persisted (" + persistErr.Error() + ")")
	}
	if !allPassed {
		return errors.New("brain eval failed")
	}
	return nil
}

func persistBrainEvalRun(ctx context.Context, args cliArgs, suite brainEvalSuite, reports []memorypkg.BrainEvalReport, passed int, success bool) (map[string]any, error) {
	reportsPayload := []any{}
	for _, report := range reports {
		reportsPayload = append(reportsPayload, report)
	}
	scope := commonBrainEvalScope(suite)
	if scope == "" {
		return nil, errors.New("eval history requires all cases to share one scope")
	}
	result, err := callMCPTool(ctx, args, "brain_eval_record", map[string]any{
		"scope":      scope,
		"suite_name": flag(args, "suite", ""),
		"suite_file": flag(args, "file", ""),
		"agent":      flag(args, "agent", ""),
		"total":      len(reports),
		"passed":     passed,
		"success":    success,
		"reports":    reportsPayload,
		"metadata": map[string]any{
			"command": "eval brain",
		},
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func commonBrainEvalScope(suite brainEvalSuite) string {
	scope := ""
	for _, tc := range suite.Cases {
		next := strings.TrimSpace(tc.Scope)
		if next == "" {
			continue
		}
		if scope == "" {
			scope = next
			continue
		}
		if scope != next {
			return ""
		}
	}
	return scope
}

func loadBrainEvalSuite(args cliArgs) ([]byte, error) {
	path := flag(args, "file", "")
	suite := strings.TrimSpace(flag(args, "suite", ""))
	if path != "" {
		return os.ReadFile(path)
	}
	switch suite {
	case "canonical":
		return []byte(canonicalBrainEvalSuiteJSON), nil
	case "":
		return nil, errors.New("eval brain requires --file <suite.json> or --suite canonical")
	default:
		if strings.HasSuffix(suite, ".json") || strings.ContainsAny(suite, `/\`) {
			return os.ReadFile(suite)
		}
		return nil, fmt.Errorf("unknown brain eval suite %q; available: canonical", suite)
	}
}

func brainCommand(ctx context.Context, args cliArgs) error {
	action := "status"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "status":
		return brainOverview(ctx, args)
	case "health", "doctor":
		args.Rest = append([]string{action}, args.Rest...)
		return memoryCommand(ctx, args)
	case "review":
		return brainReview(ctx, args)
	case "scorecard":
		return brainScorecard(ctx, args)
	case "anchor-backfill", "anchors", "backfill":
		return brainAnchorBackfill(ctx, args)
	case "maintain", "maintenance":
		return brainMaintain(ctx, args)
	case "dossier", "entity":
		return brainEntityDossier(ctx, args)
	case "explain":
		return brainExplain(ctx, args)
	default:
		return fmt.Errorf("unknown brain command %q\n\n%s", action, commandUsage("brain"))
	}
}

func brainOverview(ctx context.Context, args cliArgs) error {
	scope := scopeOrDefault(args, ".")
	result, _, err := getJSON(ctx, args, "/memory/health?scope="+urlQueryEscape(scope))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{
			"scope":         scope,
			"memory_health": result,
			"agent_surface": requiredMCPToolNames(),
		})
	}
	fmt.Printf("Brain: %s score=%d\n", stringValue(result["status"], "unknown"), intValue(result["score"]))
	fmt.Println("scope: " + scope)
	printMemoryHealthSection("sources", result["sources"], []string{"total", "active", "due", "overdue", "error"})
	printMemoryHealthSection("conflicts", result["conflicts"], []string{"open", "reviewing", "blocking", "high"})
	printMemoryHealthSection("learning", result["learning"], []string{"pending", "duplicate_pending_groups"})
	fmt.Println("next:")
	fmt.Println("- abra agent verify . --scope " + scope + " --agent codex")
	fmt.Println("- abra brain review --scope " + scope)
	fmt.Println("- abra brain scorecard --scope " + scope)
	fmt.Println("- MCP tools: " + strings.Join(requiredMCPToolNames(), ", "))
	return nil
}

func brainReview(ctx context.Context, args cliArgs) error {
	result, err := callMCPTool(ctx, args, "brain_review", brainQualityArgs(args, false))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Brain review: %s score=%d\n", stringValue(result["status"], "unknown"), intValue(result["score"]))
	fmt.Println("scope: " + stringValue(result["scope"], scopeOrDefault(args, ".")))
	printBrainReviewItems(result["review_items"], 10)
	printBrainActions(result["actions"], 10)
	return nil
}

func brainScorecard(ctx context.Context, args cliArgs) error {
	result, err := callMCPTool(ctx, args, "brain_scorecard", brainQualityArgs(args, false))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Brain scorecard: %s score=%d\n", stringValue(result["status"], "unknown"), intValue(result["score"]))
	fmt.Println("scope: " + stringValue(result["scope"], scopeOrDefault(args, ".")))
	printBrainScorecardCategories(result["categories"], 12)
	printBrainActions(result["actions"], 10)
	return nil
}

func brainAnchorBackfill(ctx context.Context, args cliArgs) error {
	toolArgs := brainQualityArgs(args, true)
	result, err := callMCPTool(ctx, args, "brain_anchor_backfill", toolArgs)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Brain anchor backfill: %s score=%d\n", stringValue(result["status"], "unknown"), intValue(result["score"]))
	fmt.Println("scope: " + stringValue(result["scope"], scopeOrDefault(args, ".")))
	fmt.Printf("dry_run: %t propose: %t\n", boolValue(result["dry_run"], boolValue(toolArgs["dry_run"], true)), boolValue(result["propose"], boolValue(toolArgs["propose"], false)))
	fmt.Printf("checked: %d claims=%d candidates=%d proposals=%d\n", intValue(result["checked"]), intValue(result["checked_claims"]), lenSlice(result["candidates"]), lenSlice(result["proposals"]))
	printBrainAnchorCandidates(result["candidates"], 10)
	printBrainActions(result["actions"], 10)
	return nil
}

func brainMaintain(ctx context.Context, args cliArgs) error {
	toolArgs := brainQualityArgs(args, true)
	result, err := callMCPTool(ctx, args, "brain_maintain", toolArgs)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Brain maintain: %s score=%d\n", stringValue(result["status"], "unknown"), intValue(result["score"]))
	fmt.Println("scope: " + stringValue(result["scope"], scopeOrDefault(args, ".")))
	fmt.Printf("dry_run: %t propose: %t\n", boolValue(result["dry_run"], boolValue(toolArgs["dry_run"], true)), boolValue(result["propose"], boolValue(toolArgs["propose"], false)))
	fmt.Printf("proposals: %d\n", lenSlice(result["proposals"]))
	printBrainReviewItems(result["review_items"], 10)
	printBrainActions(result["actions"], 10)
	return nil
}

func brainEntityDossier(ctx context.Context, args cliArgs) error {
	entity := strings.TrimSpace(strings.Join(args.Rest, " "))
	if entity == "" {
		entity = strings.TrimSpace(flag(args, "entity", ""))
	}
	if entity == "" {
		return errors.New("brain dossier requires an entity, for example: abra brain dossier Abra --scope repo:demo")
	}
	mode, err := retrievalModeFlag(args)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"entity":             entity,
		"scope":              scopeOrDefault(args, "."),
		"agent":              flag(args, "agent", ""),
		"mode":               mode,
		"as_of":              normalizedAsOfFlag(args),
		"include_historical": boolFlag(args, "include-historical"),
		"include_unverified": boolFlag(args, "include-unverified"),
		"limit":              intFlag(args, "limit", 5),
	}
	if hasFlag(args, "token-budget") {
		payload["token_budget"] = intFlag(args, "token-budget", 0)
	}
	result, err := callMCPTool(ctx, args, "brain_entity_dossier", payload)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Brain dossier: " + stringValue(result["entity"], entity))
	fmt.Println("scope: " + stringValue(result["scope"], scopeOrDefault(args, ".")))
	dossier, _ := result["dossier"].(map[string]any)
	if len(dossier) > 0 {
		fmt.Printf("trust: %s\n", stringValue(dossier["trust"], "unknown"))
		stats, _ := dossier["stats"].(map[string]any)
		if len(stats) > 0 {
			fmt.Printf("stats: claims=%d active=%d relations=%d anchors=%d conflicts=%d\n", intValue(stats["claims"]), intValue(stats["active_claims"]), intValue(stats["relations"]), intValue(stats["anchors"]), intValue(stats["conflicts"]))
		}
		printBrainDossierClaims(dossier["active_claims"], "active claims", 8)
		printBrainDossierRelations(dossier["active_relations"], "active relations", 8)
		if action := stringValue(dossier["next_action"], ""); action != "" {
			fmt.Println("next: " + action)
		}
	}
	decision, _ := result["agent_decision"].(map[string]any)
	if len(decision) > 0 {
		fmt.Println("decision: " + stringValue(decision["decision"], "unknown"))
	}
	return nil
}

func brainQualityArgs(args cliArgs, proposalMode bool) map[string]any {
	payload := map[string]any{
		"scope": scopeOrDefault(args, "."),
		"limit": intFlag(args, "limit", 50),
	}
	if agent := strings.TrimSpace(flag(args, "agent", "")); agent != "" {
		payload["agent"] = agent
	}
	if proposalMode {
		propose := boolFlag(args, "propose")
		dryRun := !propose || boolFlag(args, "dry-run")
		payload["propose"] = propose
		payload["dry_run"] = dryRun
		if createdBy := strings.TrimSpace(flag(args, "created-by", "")); createdBy != "" {
			payload["created_by"] = createdBy
		} else if agent := stringValue(payload["agent"], ""); agent != "" {
			payload["created_by"] = agent
		} else {
			payload["created_by"] = "cli"
		}
	}
	return payload
}

func printBrainDossierClaims(raw any, label string, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println(label + ":")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		ref := stringValue(item["citation_ref"], "")
		if ref != "" {
			ref = " [" + ref + "]"
		}
		fmt.Printf("- %s%s (%s)\n", stringValue(item["claim_text"], ""), ref, stringValue(item["status"], "unknown"))
	}
}

func printBrainDossierRelations(raw any, label string, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println(label + ":")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		ref := stringValue(item["citation_ref"], "")
		if ref != "" {
			ref = " [" + ref + "]"
		}
		fmt.Printf("- %s --%s--> %s%s\n", stringValue(item["from_entity"], ""), stringValue(item["relation_type"], "relates_to"), stringValue(item["to_entity"], ""), ref)
	}
}

func printBrainReviewItems(raw any, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println("review:")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		code := stringValue(item["code"], "")
		if code == "" {
			code = stringValue(item["category"], "item")
		}
		message := stringValue(item["message"], "")
		if message == "" {
			message = stringValue(item["title"], "")
		}
		fmt.Printf("- %s %s count=%d: %s\n", stringValue(item["severity"], "info"), code, intValue(item["count"]), message)
		if action := firstNonEmpty(stringValue(item["suggested_action"], ""), stringValue(item["action"], "")); action != "" {
			fmt.Println("  action: " + action)
		}
	}
}

func printBrainScorecardCategories(raw any, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println("categories:")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		name := firstNonEmpty(stringValue(item["name"], ""), stringValue(item["category"], "category"))
		fmt.Printf("- %s score=%d %s\n", name, intValue(item["score"]), stringValue(item["status"], "unknown"))
		if detail := firstNonEmpty(stringValue(item["detail"], ""), stringValue(item["message"], "")); detail != "" {
			fmt.Println("  " + detail)
		}
	}
}

func printBrainAnchorCandidates(raw any, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println("candidates:")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		id := firstNonEmpty(stringValue(item["claim_id"], ""), stringValue(item["id"], ""))
		source := firstNonEmpty(stringValue(item["source_url"], ""), stringValue(item["document_url"], ""))
		fmt.Printf("- %s score=%d %s\n", id, intValue(item["score"]), source)
		if text := firstNonEmpty(stringValue(item["quote"], ""), stringValue(item["text"], "")); text != "" {
			fmt.Println("  " + text)
		}
	}
}

func printBrainActions(raw any, limit int) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	fmt.Println("actions:")
	for i, rawItem := range items {
		if i >= limit {
			fmt.Printf("- ... %d more\n", len(items)-limit)
			return
		}
		switch item := rawItem.(type) {
		case string:
			if strings.TrimSpace(item) != "" {
				fmt.Println("- " + strings.TrimSpace(item))
			}
		case map[string]any:
			action := firstNonEmpty(stringValue(item["action"], ""), stringValue(item["message"], ""), stringValue(item["command"], ""))
			if action != "" {
				fmt.Println("- " + action)
			}
		}
	}
}

func brainExplain(ctx context.Context, args cliArgs) error {
	traceID := strings.TrimSpace(strings.Join(args.Rest, " "))
	if traceID == "" {
		traceID = flag(args, "trace-id", "")
	}
	if traceID == "" {
		return errors.New("brain explain requires a trace id from why_trace.trace_id")
	}
	result, err := callMCPTool(ctx, args, "brain_explain", map[string]any{"trace_id": traceID})
	if err != nil {
		return err
	}
	if stringValue(result["status"], "") == "not_found" {
		if boolFlag(args, "json") {
			return printJSON(result)
		}
		fmt.Println("Brain explain: " + traceID)
		fmt.Println(stringValue(result["message"], "No persisted why_trace was found for this trace id."))
		return nil
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Brain explain: " + traceID)
	fmt.Println("scope: " + stringValue(result["scope"], ""))
	fmt.Println("question: " + stringValue(result["question"], ""))
	fmt.Println("created: " + stringValue(result["created_at"], ""))
	fmt.Println("answer:")
	fmt.Println(stringValue(result["answer"], ""))
	printBrainTraceRefs(result["trace"])
	return nil
}

func printBrainTraceRefs(raw any) {
	trace, _ := raw.(map[string]any)
	if trace == nil {
		return
	}
	for _, section := range []string{"claims", "documents", "relations", "anchors"} {
		items, _ := trace[section].([]any)
		if len(items) == 0 {
			continue
		}
		fmt.Println(section + ":")
		for _, rawItem := range items {
			item, _ := rawItem.(map[string]any)
			if item == nil {
				continue
			}
			ref := stringValue(item["ref"], "")
			if ref == "" {
				ref = stringValue(item["citation_ref"], "")
			}
			summary := stringValue(item["summary"], "")
			id := stringValue(item["id"], "")
			if ref != "" {
				fmt.Printf("- %s %s\n", ref, summary)
			} else if id != "" {
				fmt.Printf("- %s %s\n", id, summary)
			} else {
				fmt.Println("- " + summary)
			}
		}
	}
}
