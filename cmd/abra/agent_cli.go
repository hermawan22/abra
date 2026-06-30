package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func mcp(ctx context.Context, args cliArgs) error {
	action := ""
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "install-codex", "codex":
		return installCodexMCP(ctx, args)
	case "status", "doctor", "check":
		return mcpStatus(ctx, args)
	case "":
	default:
		return fmt.Errorf("unknown mcp command %q\n\n%s", action, commandUsage("mcp"))
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	server := map[string]any{
		"type":                 "http",
		"url":                  strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp",
		"bearer_token_env_var": tokenEnv,
	}
	if boolFlag(args, "literal-token") {
		server["headers"] = map[string]string{
			"Authorization": "Bearer " + cfg(args).Token,
		}
		delete(server, "bearer_token_env_var")
	}
	body := map[string]any{
		"mcpServers": map[string]any{
			"abra": server,
		},
	}
	return printJSON(body)
}

func mcpStatus(ctx context.Context, args cliArgs) error {
	checks := []map[string]any{}
	result, code, err := getJSON(ctx, args, "/readyz")
	if err != nil || code < 200 || code >= 300 {
		checks = append(checks, map[string]any{
			"name":   "readyz",
			"ok":     false,
			"status": code,
			"error":  readyFailureDetail(result, err),
			"hint":   "run: abra up, then retry `abra mcp status`",
		})
		return printDoctor(args, checks)
	}
	checks = append(checks, map[string]any{
		"name":               "readyz",
		"ok":                 true,
		"detail":             "Abra API is ready at " + strings.TrimRight(cfg(args).BaseURL, "/"),
		"embedding_provider": stringValue(result["embedding_provider"], "unknown"),
	})
	checks = append(checks, mcpCheck(ctx, args))
	checks = append(checks, codexMCPClientCheck(args))
	checks = append(checks, codexLaunchEnvCheck(args))
	return printDoctor(args, checks)
}

func scopeCommand(args cliArgs) error {
	path := "."
	if len(args.Rest) > 0 {
		path = args.Rest[0]
	}
	scope := scopeOrDefault(args, path)
	if boolFlag(args, "json") {
		return printJSON(map[string]any{
			"scope": scope,
			"path":  path,
			"examples": map[string]string{
				"bootstrap":        "abra agent bootstrap " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope),
				"agent_install":    "abra agent install codex",
				"agent_init":       "abra agent init " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope),
				"agent_verify":     "abra agent verify " + shellQuote(path) + " --scope " + shellQuote(scope),
				"sync":             "abra sync " + shellQuote(path) + " --code --scope " + shellQuote(scope),
				"operator_ask":     "abra ask \"what should I know before changing this project?\" --scope " + scope,
				"codex":            agentReadyPrompt(scope),
				"operator_context": "abra context \"ship this change\" --scope " + scope + " --agent codex",
				"troubleshooting":  "If an AI client says Abra has no context, run agent_verify --json first. Run abra doctor and repair MCP/API/token/model readiness when verify reports readiness errors. Sync only when verify proves the exact scope or source-backed memory is missing.",
			},
		})
	}
	fmt.Println("Scope: " + scope)
	fmt.Println("Use this exact scope with Abra MCP and AI agents.")
	fmt.Println("Bootstrap: abra agent bootstrap " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope))
	fmt.Println("MCP:    abra agent install codex")
	fmt.Println("Agent:  abra agent init " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope))
	fmt.Println("Check:  abra agent verify " + shellQuote(path) + " --scope " + shellQuote(scope))
	fmt.Println("Sync only if Check proves missing scope or empty memory: abra sync " + shellQuote(path) + " --code --scope " + shellQuote(scope))
	fmt.Println("MCP:    agents call working_memory_compose / brain_think with this exact scope")
	fmt.Println("Ask:    optional operator check: abra ask \"what should I know before changing this project?\" --scope " + scope)
	fmt.Println("Codex:  " + agentReadyPrompt(scope))
	fmt.Println("Fix:    If Codex says Abra has no context, run Check first. Run abra doctor for readiness errors, reinstall/restart MCP when server_ready=true but agent_ready=false, and sync only when Check proves missing scope or empty memory.")
	return nil
}

func agentsCommand(ctx context.Context, args cliArgs) error {
	action := "init"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	if action != "init" && action != "verify" && action != "check" && action != "bootstrap" && action != "ready" {
		return fmt.Errorf("unknown agents command %q\n\n%s", action, commandUsage("agents"))
	}
	path := flag(args, "path", ".")
	if len(args.Rest) > 0 {
		path = args.Rest[0]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	scope := scopeOrDefault(args, abs)
	if action == "verify" || action == "check" || action == "ready" {
		return verifyAgentContext(ctx, args, abs, scope)
	}
	if action == "bootstrap" {
		return bootstrapAgentContext(ctx, args, abs, scope)
	}
	agent := flag(args, "agent", "codex")
	force := boolFlag(args, "force")
	dryRun := boolFlag(args, "dry-run")
	results, err := writeAgentInstructionFiles(abs, scope, agent, force, dryRun)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{
			"scope": scope,
			"agent": agent,
			"path":  abs,
			"files": results,
		})
	}
	fmt.Println("Agent instructions for scope: " + scope)
	for _, result := range results {
		fmt.Println(stringValue(result["action"], "") + ": " + stringValue(result["path"], ""))
	}
	if isCodexAgent(agent) {
		fmt.Println("MCP:    abra agent install codex")
	} else {
		fmt.Println("MCP:    abra mcp > .tmp/abra.mcp.json")
	}
	fmt.Println("Check:  abra agent verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent))
	fmt.Println("Sync only if Check proves missing scope or empty memory: abra sync " + shellQuote(path) + " --code --scope " + shellQuote(scope))
	fmt.Println("Then:   tell your AI agent to read AGENTS.md or CLAUDE.md before changing code.")
	return nil
}

func bootstrapAgentContext(ctx context.Context, args cliArgs, path, scope string) error {
	if boolFlag(args, "json") {
		return errors.New("agents bootstrap does not support --json yet")
	}
	agent := flag(args, "agent", "codex")
	force := boolFlag(args, "force")
	fmt.Println("Bootstrapping Abra agent context")
	fmt.Println("scope: " + scope)
	results, err := writeAgentInstructionFiles(path, scope, agent, force, false)
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Println(stringValue(result["action"], "") + ": " + stringValue(result["path"], ""))
	}

	ingestArgs := copyCLIArgs(args)
	ingestArgs.Flags["path"] = path
	ingestArgs.Flags["scope"] = scope
	ingestArgs.Flags["authority"] = "source-code"
	ingestArgs.Flags["authority-score"] = "0.82"
	ingestArgs.Bools["code"] = true
	ingestArgs.Bools["wait"] = true
	delete(ingestArgs.Bools, "json")
	ingestArgs.Rest = nil
	fmt.Println("Ingesting repo with exact scope...")
	if err := sourceIngest(ctx, ingestArgs); err != nil {
		return err
	}

	if boolFlag(args, "no-mcp") || boolFlag(args, "skip-mcp") {
		fmt.Println("MCP install skipped by flag.")
	} else if isCodexAgent(agent) {
		fmt.Println("Installing Abra MCP into Codex...")
		mcpArgs := copyCLIArgs(args)
		delete(mcpArgs.Bools, "json")
		if err := installCodexMCP(ctx, mcpArgs); err != nil {
			return err
		}
	} else {
		fmt.Println("Automatic MCP install is currently Codex-only.")
		fmt.Println("MCP config: abra mcp > .tmp/abra.mcp.json")
		fmt.Println("Then configure " + agent + " to use the generated MCP config or " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp.")
	}

	fmt.Println("Verifying source-backed working memory...")
	verifyArgs := copyCLIArgs(args)
	delete(verifyArgs.Bools, "json")
	if err := verifyAgentContext(ctx, verifyArgs, path, scope); err != nil {
		return err
	}
	fmt.Println("Ready prompt:")
	fmt.Println(agentReadyPrompt(scope, agent))
	return nil
}

func verifyAgentContext(ctx context.Context, args cliArgs, path, scope string) error {
	filesOnly := boolFlag(args, "files-only")
	strict := boolFlag(args, "strict")
	agent := normalizedAgentFlag(args)
	checks := []map[string]any{
		agentFileCheck(filepath.Join(path, "AGENTS.md"), scope, []string{"working_memory_compose", "discover_scopes", `expected_scope: "` + scope + `"`, "current task", `agent: "` + agent + `"`, "agent_ready", "abra doctor", "sync only when verify"}),
		optionalAgentFileCheck(filepath.Join(path, "CLAUDE.md"), "@AGENTS.md"),
	}
	if filesOnly {
		checks = append(checks, map[string]any{
			"name":   "mcp",
			"ok":     true,
			"level":  "skip",
			"detail": "skipped by --files-only",
		})
	} else if toolCount, err := validateMCPTools(ctx, args); err != nil {
		checks = append(checks, map[string]any{
			"name":  "mcp",
			"ok":    false,
			"hint":  "start Abra with `abra up`, check `abra doctor`, then retry",
			"error": err.Error(),
		})
	} else {
		checks = append(checks, map[string]any{
			"name":   "mcp",
			"ok":     true,
			"detail": fmt.Sprintf("tools=%d required=%s", toolCount, strings.Join(requiredMCPToolNames(), ",")),
		})
		scopeCheck := discoverScopeCheck(ctx, args, scope)
		checks = append(checks, scopeCheck)
		if boolValue(scopeCheck["ok"], false) {
			checks = append(checks, workingMemoryContextCheck(ctx, args, scope, agent))
		}
		if isCodexAgent(agent) {
			checks = append(checks, agentClientAdvisoryChecks(args)...)
		}
	}
	ok := checksOK(checks, strict)
	serverReady, clientReady, clientWarnings := agentReadinessSummary(checks, filesOnly)
	agentReady := serverReady && clientReady
	readyPrompt := agentReadyPrompt(scope, agent)
	nextSteps := agentVerifyNextSteps(path, scope, agent, ok, filesOnly, checks)
	if ok && !filesOnly && !clientReady {
		nextSteps = append([]string{"Fix the client warning(s) above before relying on the active AI client, then fully restart that client."}, nextSteps...)
	}
	if boolFlag(args, "json") {
		if err := printJSON(map[string]any{
			"ok":              ok,
			"server_ready":    serverReady,
			"client_ready":    clientReady,
			"agent_ready":     agentReady,
			"client_warnings": clientWarnings,
			"scope":           scope,
			"path":            path,
			"files_only":      filesOnly,
			"strict":          strict,
			"checks":          checks,
			"ready_prompt":    readyPrompt,
			"next_steps":      nextSteps,
		}); err != nil {
			return err
		}
		if !ok {
			if strict && serverReady && !clientReady {
				return errors.New("agent client readiness failed under --strict; fix AI client advisory checks and rerun")
			}
			return errors.New("agent context verification failed")
		}
		return nil
	}
	fmt.Println("Agent context check for scope: " + scope)
	for _, check := range checks {
		status := "ok"
		if stringValue(check["level"], "") == "warn" {
			status = "warn"
		}
		if stringValue(check["level"], "") == "skip" {
			status = "skip"
		}
		if !boolValue(check["ok"], false) {
			status = "fail"
		}
		line := status + "  " + stringValue(check["name"], "")
		if detail := stringValue(check["detail"], ""); detail != "" {
			line += " " + detail
		}
		fmt.Println(line)
		if hint := stringValue(check["hint"], ""); hint != "" {
			fmt.Println("hint " + hint)
		}
		if errText := stringValue(check["error"], ""); errText != "" {
			fmt.Println("error " + errText)
		}
	}
	if !ok {
		printAgentNextSteps(nextSteps)
		if strict && serverReady && !clientReady {
			return errors.New("agent client readiness failed under --strict; fix AI client advisory checks and rerun")
		}
		if filesOnly {
			return errors.New("agent instruction verification failed; run `abra agent init --force` after confirming local custom instructions are backed up")
		}
		return errors.New("agent context verification failed; follow the printed Next steps and rerun `abra agent verify`")
	}
	if filesOnly {
		fmt.Println("Ready: agent instruction files are ready for scope " + scope + ".")
		fmt.Println("Prompt: " + readyPrompt)
		printAgentNextSteps(nextSteps)
		return nil
	}
	if clientReady {
		fmt.Println("Ready: server and Codex MCP config can use scope " + scope + " with working_memory_compose. Restart Codex if this changed after the active app launched.")
	} else {
		fmt.Printf("Ready: Abra server can use scope %s with working_memory_compose, but AI client readiness has %d warning(s).\n", scope, clientWarnings)
	}
	fmt.Println("Prompt: " + readyPrompt)
	printAgentNextSteps(nextSteps)
	return nil
}

func agentReadinessSummary(checks []map[string]any, filesOnly bool) (serverReady bool, clientReady bool, clientWarnings int) {
	serverReady = !filesOnly
	clientReady = !filesOnly
	for _, check := range checks {
		if boolValue(check["advisory"], false) {
			if stringValue(check["level"], "") == "warn" || !boolValue(check["client_ok"], true) {
				clientWarnings++
				clientReady = false
			}
			continue
		}
		if !boolValue(check["ok"], false) {
			serverReady = false
		}
	}
	return serverReady, clientReady, clientWarnings
}

func agentReadyPrompt(scope string, agents ...string) string {
	agent := "codex"
	if len(agents) > 0 && strings.TrimSpace(agents[0]) != "" {
		agent = strings.ToLower(strings.TrimSpace(agents[0]))
	}
	return `Use Abra MCP first. Exact scope: ` + scope + `. Call discover_scopes with expected_scope="` + scope + `", then call working_memory_compose with task=<current task>, scope="` + scope + `", and agent="` + agent + `" before answering or changing code. If Abra MCP tools are unavailable or the AI client says Abra has no context, run abra agent verify . --scope ` + scope + ` --agent ` + agent + ` --json first. Run abra doctor and repair MCP/API/token/model readiness when verify reports readiness errors; when server_ready=true but agent_ready=false, reinstall/restart the AI client MCP integration. Sync only when verify proves the exact scope is missing or source-backed memory is empty, then rerun verify with this exact scope.`
}

func normalizedAgentFlag(args cliArgs) string {
	agent := strings.ToLower(strings.TrimSpace(flag(args, "agent", "codex")))
	if agent == "" {
		return "codex"
	}
	return agent
}

func isCodexAgent(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "codex")
}

func agentVerifyNextSteps(path, scope, agent string, ok, filesOnly bool, checks []map[string]any) []string {
	if ok && filesOnly {
		return []string{
			"Run `abra agent verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent) + "` against a live Abra MCP server before giving the prompt to an AI client.",
			"Give the ready_prompt to the AI client.",
		}
	}
	if ok {
		return []string{
			"Give the ready_prompt to the AI client.",
			"If the AI client still says Abra has no context, fully restart that client and rerun `abra agent verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent) + "`.",
		}
	}
	steps := []string{}
	if hasFailedCheck(checks, "AGENTS.md") || hasFailedCheck(checks, "CLAUDE.md") {
		steps = append(steps, "Run `abra agent init "+shellQuote(path)+" --agent "+shellQuote(agent)+" --scope "+shellQuote(scope)+"` if instruction files are missing or stale.")
	}
	if hasFailedCheck(checks, "mcp") || failedCheckHasError(checks, "scope_discovery") || failedCheckHasError(checks, "working_memory") || failedCheckHasReadinessIssue(checks, "working_memory") {
		steps = append(steps,
			"Run `abra doctor` to check API, MCP, token, and local model readiness.",
			"If this is Codex, run `abra agent install codex`, fully quit and reopen Codex, then retry.",
		)
	}
	if hasFailedCheckWithoutError(checks, "scope_discovery") || (hasFailedCheckWithoutError(checks, "working_memory") && !failedCheckHasReadinessIssue(checks, "working_memory")) {
		steps = append(steps, "Run `abra sync "+shellQuote(path)+" --code --scope "+shellQuote(scope)+"` only because verify proved the exact scope or source-backed memory is missing.")
	}
	if len(steps) == 0 {
		steps = append(steps, "Run `abra doctor` to check API, MCP, token, local model readiness, and agent setup.")
	}
	steps = appendUniqueStrings(steps, "Rerun `abra agent verify "+shellQuote(path)+" --scope "+shellQuote(scope)+" --agent "+shellQuote(agent)+"`.")
	return steps
}

func appendUniqueStrings(values []string, extra ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)+len(extra))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range extra {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func hasFailedCheck(checks []map[string]any, name string) bool {
	for _, check := range checks {
		if stringValue(check["name"], "") == name && !boolValue(check["ok"], false) {
			return true
		}
	}
	return false
}

func failedCheckHasError(checks []map[string]any, name string) bool {
	for _, check := range checks {
		if stringValue(check["name"], "") == name && !boolValue(check["ok"], false) && stringValue(check["error"], "") != "" {
			return true
		}
	}
	return false
}

func failedCheckHasReadinessIssue(checks []map[string]any, name string) bool {
	for _, check := range checks {
		if stringValue(check["name"], "") == name && !boolValue(check["ok"], false) && boolValue(check["readiness_issue"], false) {
			return true
		}
	}
	return false
}

func hasFailedCheckWithoutError(checks []map[string]any, name string) bool {
	for _, check := range checks {
		if stringValue(check["name"], "") == name && !boolValue(check["ok"], false) && stringValue(check["error"], "") == "" {
			return true
		}
	}
	return false
}

func agentClientAdvisoryChecks(args cliArgs) []map[string]any {
	checks := []map[string]any{
		codexMCPClientCheck(args),
		codexLaunchEnvCheck(args),
	}
	for _, check := range checks {
		check["advisory"] = true
		if boolValue(check["ok"], false) {
			continue
		}
		check["client_ok"] = false
		check["ok"] = true
		check["level"] = "warn"
	}
	return checks
}

func printAgentNextSteps(steps []string) {
	if len(steps) == 0 {
		return
	}
	fmt.Println("Next:")
	for _, step := range steps {
		fmt.Println("- " + step)
	}
}

func optionalAgentFileCheck(path, required string) map[string]any {
	check := agentFileCheck(path, required, nil)
	if boolValue(check["ok"], false) {
		return check
	}
	check["ok"] = true
	check["level"] = "warn"
	if _, hasDetail := check["detail"]; !hasDetail {
		check["detail"] = "optional compatibility file missing"
	}
	check["hint"] = "run `abra agent init` if this repository should support tools that require " + filepath.Base(path)
	delete(check, "error")
	return check
}

func agentFileCheck(path, required string, extra []string) map[string]any {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{
			"name":  filepath.Base(path),
			"ok":    false,
			"hint":  "run `abra agent init` in the project root",
			"error": err.Error(),
		}
	}
	text := string(content)
	missing := []string{}
	for _, want := range append([]string{required}, extra...) {
		if strings.TrimSpace(want) != "" && !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return map[string]any{
			"name":   filepath.Base(path),
			"ok":     false,
			"detail": "missing " + strings.Join(missing, ", "),
			"hint":   "run `abra agent init --force` after confirming local custom instructions are backed up",
		}
	}
	return map[string]any{"name": filepath.Base(path), "ok": true}
}

func discoverScopeCheck(ctx context.Context, args cliArgs, scope string) map[string]any {
	result, err := callMCPTool(ctx, args, "discover_scopes", map[string]any{
		"expected_scope": scope,
		"limit":          10,
	})
	if err != nil {
		return map[string]any{
			"name":  "scope_discovery",
			"ok":    false,
			"hint":  "repair Abra MCP/API/token readiness with `abra doctor`, then rerun `abra agent verify`; sync only if discovery succeeds but the exact scope is missing",
			"error": err.Error(),
		}
	}
	if mcpScopeResultHasScope(result, scope) {
		return map[string]any{
			"name":   "scope_discovery",
			"ok":     true,
			"detail": "discover_scopes returned " + scope,
		}
	}
	hint := "run `abra sync . --code --scope " + scope + "` and retry with the exact scope"
	if boolValue(result["candidate_truncated"], false) {
		hint += "; discovery candidates were truncated"
	}
	return map[string]any{
		"name":   "scope_discovery",
		"ok":     false,
		"detail": "discover_scopes did not return " + scope,
		"hint":   hint,
	}
}

func workingMemoryContextCheck(ctx context.Context, args cliArgs, scope, agent string) map[string]any {
	result, err := callMCPTool(ctx, args, "working_memory_compose", map[string]any{
		"task":         "verify agent context for " + scope,
		"scope":        scope,
		"agent":        agent,
		"limit":        3,
		"max_queries":  3,
		"token_budget": 600,
		"diagnostic":   true,
	})
	if err != nil {
		return map[string]any{
			"name":  "working_memory",
			"ok":    false,
			"hint":  "repair Abra MCP/API/token readiness with `abra doctor`, then rerun `abra agent verify`; sync only if compose succeeds but returns no source-backed context",
			"error": err.Error(),
		}
	}
	facts, documents, summaries, graph := memoryContextCounts(result)
	if facts+documents+summaries+graph > 0 {
		return map[string]any{
			"name":   "working_memory",
			"ok":     true,
			"detail": fmt.Sprintf("facts=%d documents=%d summaries=%d graph=%d", facts, documents, summaries, graph),
		}
	}
	if composeResultHasReadinessWarning(result) {
		return map[string]any{
			"name":            "working_memory",
			"ok":              false,
			"detail":          fmt.Sprintf("facts=%d documents=%d summaries=%d graph=%d with retrieval or memory-health warnings", facts, documents, summaries, graph),
			"hint":            "run `abra doctor` and `abra model status`, then retry `abra agent verify`; sync only if readiness is healthy and memory is still empty",
			"readiness_issue": true,
		}
	}
	return map[string]any{
		"name":   "working_memory",
		"ok":     false,
		"detail": fmt.Sprintf("facts=%d documents=%d summaries=%d graph=%d", facts, documents, summaries, graph),
		"hint":   "run `abra sync . --code --scope " + scope + "`, then retry `abra agent verify . --scope " + scope + "`",
	}
}

func composeResultHasReadinessWarning(result map[string]any) bool {
	if lenSlice(result["retrieval_warnings"]) > 0 {
		return true
	}
	if health, _ := result["memory_health"].(map[string]any); health != nil {
		status := strings.ToLower(strings.TrimSpace(stringValue(health["status"], "")))
		if status != "" && status != "healthy" {
			return true
		}
		if lenSlice(health["signals"]) > 0 && status == "" {
			return true
		}
	}
	return false
}

func memoryContextCounts(result map[string]any) (facts, documents, summaries, graph int) {
	if stats, ok := result["stats"].(map[string]any); ok {
		facts = intValue(stats["facts"])
		documents = intValue(stats["supporting_documents"])
		summaries = intValue(stats["summaries"])
		graph = intValue(stats["graph_relations"])
	}
	if facts == 0 {
		facts = lenSlice(result["facts"])
	}
	if documents == 0 {
		documents = lenSlice(result["supporting_documents"])
	}
	if summaries == 0 {
		summaries = lenSlice(result["summaries"])
	}
	if graph == 0 {
		graph = lenSlice(result["graph_context"])
	}
	return facts, documents, summaries, graph
}

func checksOK(checks []map[string]any, strict bool) bool {
	for _, check := range checks {
		if !boolValue(check["ok"], false) {
			return false
		}
		if strict && stringValue(check["level"], "") == "warn" {
			return false
		}
	}
	return true
}

func writeAgentInstructionFiles(abs, scope, agent string, force, dryRun bool) ([]map[string]any, error) {
	files := []agentInstructionFile{
		{
			Path:    filepath.Join(abs, "AGENTS.md"),
			Content: agentInstructions(scope, agent),
		},
		{
			Path:    filepath.Join(abs, "CLAUDE.md"),
			Content: "@AGENTS.md\n",
		},
	}
	results := make([]map[string]any, 0, len(files))
	for _, file := range files {
		exists := fileExists(file.Path)
		action := "created"
		switch {
		case dryRun && exists && !force:
			action = "would_skip"
		case dryRun:
			action = "would_write"
		case exists && !force:
			action = "skipped"
		default:
			if err := os.WriteFile(file.Path, []byte(file.Content), 0o644); err != nil {
				return nil, err
			}
			if exists {
				action = "updated"
			}
		}
		results = append(results, map[string]any{
			"path":   file.Path,
			"action": action,
		})
	}
	return results, nil
}

func agentInstructions(scope, agent string) string {
	return `# Agent Instructions

Before answering architecture questions or changing code in this repository, use Abra MCP when it is available.

1. Use exact scope ` + "`" + scope + "`" + `.
2. If discovering scopes first, call ` + "`discover_scopes`" + ` with ` + "`expected_scope: \"" + scope + "\"`" + ` so this repo is not hidden by unrelated scopes.
3. Call ` + "`working_memory_compose`" + ` with the current task, scope ` + "`" + scope + "`" + `, and ` + "`agent: \"" + agent + "\"`" + ` before implementation work.
4. Follow the returned ` + "`agent_decision`" + `, verification, memory health, conflicts, impact map, and validation plan.
5. If Abra MCP tools are unavailable or an AI client says Abra has no context, run ` + "`abra agent verify . --scope " + scope + " --agent " + agent + " --json`" + ` first, then run ` + "`abra doctor`" + ` and fix MCP/API/token/model readiness before syncing.
6. If ` + "`server_ready=true`" + ` but ` + "`agent_ready=false`" + `, reinstall/restart the AI client's MCP integration, fully restart the AI client, and retry before syncing. Run sync only when verify proves the exact scope is missing or source-backed memory is empty: ` + "`abra sync . --code --scope " + scope + "`" + `.
7. Do not include secrets, API keys, local tokens, or private business context in committed files.
`
}

func installCodexMCP(ctx context.Context, args cliArgs) error {
	codex, err := codexCommandPath()
	if err != nil {
		return err
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	token := cfg(args).Token
	if token == "" {
		return errors.New("missing Abra token")
	}
	if err := runQuiet(codex, "mcp", "list"); err != nil {
		return fmt.Errorf("codex CLI could not read its MCP configuration: %w\nfix the Codex config, then retry `abra agent install codex`", err)
	}
	toolCount, err := validateMCPTools(ctx, args)
	if err != nil {
		return fmt.Errorf("abra MCP endpoint validation failed before changing Codex config: %w\n\nrecovery:\n  1. Start or repair Abra: abra up\n  2. Check API, MCP, token env, and model readiness: abra doctor\n  3. If local embeddings are not ready: abra model status && abra model up\n  4. Retry after the endpoint is ready: %s", err, codexInstallCommand(tokenEnv))
	}
	launchctlWarning := ""
	if runtime.GOOS == "darwin" {
		if err := runQuiet("launchctl", "setenv", tokenEnv, token); err != nil {
			launchctlWarning = err.Error()
		}
	}
	os.Setenv(tokenEnv, token)
	_ = runQuiet(codex, "mcp", "remove", "abra")
	if err := runQuiet(codex, "mcp", "add", "abra", "--url", strings.TrimRight(cfg(args).BaseURL, "/")+"/mcp", "--bearer-token-env-var", tokenEnv); err != nil {
		return fmt.Errorf("codex mcp add failed: %w", err)
	}
	fmt.Println("Installed Abra MCP for Codex future launches:")
	fmt.Println("  url:       " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
	fmt.Println("  token env: " + tokenEnv)
	fmt.Printf("  endpoint:  validated (%d tools)\n", toolCount)
	fmt.Println("Codex MCP config was updated by the CLI; no manual Codex config editing is required.")
	if launchctlWarning != "" {
		fmt.Println("Warning: could not set macOS launch environment: " + launchctlWarning)
		fmt.Println("Set " + tokenEnv + " in the shell that starts Codex, then retry.")
	}
	if runtime.GOOS != "darwin" {
		fmt.Println("Set " + tokenEnv + " in the shell that starts Codex, then retry.")
	}
	fmt.Println("Verify runtime: abra doctor")
	fmt.Println("For each repo: cd /path/to/project && abra agent bootstrap --agent codex")
	fmt.Println("Active Codex sessions will not see this until you fully quit and reopen Codex Desktop.")
	fmt.Println("If Codex says Abra has no context after restart: cd into the repo and run `abra agent verify . --scope <scope-from-abra-scope>`; sync only if verify says scope or source-backed memory is missing.")
	return nil
}

func codexInstallCommand(tokenEnv string) string {
	if strings.TrimSpace(tokenEnv) == "" || tokenEnv == "ABRA_API_TOKEN" {
		return "abra agent install codex"
	}
	return "abra agent install codex --token-env " + tokenEnv
}

func validateMCPTools(ctx context.Context, args cliArgs) (int, error) {
	result, err := postJSON(ctx, args, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if err != nil {
		return 0, err
	}
	names := mcpToolNames(result)
	for _, required := range requiredMCPToolNames() {
		if !names[required] {
			return len(names), fmt.Errorf("missing required MCP tool %q", required)
		}
	}
	return len(names), nil
}

func requiredMCPToolNames() []string {
	return []string{
		"discover_scopes",
		"working_memory_compose",
		"brain_think",
		"brain_entity_dossier",
		"brain_review",
		"brain_scorecard",
		"brain_anchor_backfill",
		"brain_maintain",
		"capture_observation",
		"capture_task_outcome",
		"propose_learning",
		"list_learning_proposals",
		"decide_learning_proposal",
		"apply_learning_proposal",
	}
}

func callMCPTool(ctx context.Context, args cliArgs, name string, arguments map[string]any) (map[string]any, error) {
	decoded, err := callMCPToolRaw(ctx, args, name, arguments)
	if err != nil {
		return nil, err
	}
	result, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decode MCP %s response: expected JSON object, got %T", name, decoded)
	}
	return result, nil
}

func callMCPToolRaw(ctx context.Context, args cliArgs, name string, arguments map[string]any) (any, error) {
	result, err := postJSON(ctx, args, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	if err != nil {
		return nil, err
	}
	if rawError, ok := result["error"].(map[string]any); ok {
		return nil, errors.New(stringValue(rawError["message"], "mcp tool call failed"))
	}
	rawResult, _ := result["result"].(map[string]any)
	rawContent, _ := rawResult["content"].([]any)
	for _, item := range rawContent {
		content, _ := item.(map[string]any)
		if stringValue(content["type"], "") != "text" {
			continue
		}
		text := stringValue(content["text"], "")
		if text == "" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, fmt.Errorf("decode MCP %s response: %w", name, err)
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("MCP %s response did not include text JSON content", name)
}

func mcpScopeResultHasScope(result map[string]any, scope string) bool {
	if stringValue(result["recommended_scope"], "") == scope {
		return true
	}
	for _, key := range []string{"matches", "scopes"} {
		rawItems, _ := result[key].([]any)
		for _, rawItem := range rawItems {
			item, _ := rawItem.(map[string]any)
			if stringValue(item["scope"], "") == scope {
				return true
			}
		}
	}
	return false
}

func mcpToolNames(result map[string]any) map[string]bool {
	rawResult, _ := result["result"].(map[string]any)
	rawTools, _ := rawResult["tools"].([]any)
	names := map[string]bool{}
	for _, rawTool := range rawTools {
		tool, _ := rawTool.(map[string]any)
		name := strings.TrimSpace(stringValue(tool["name"], ""))
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func codexCommandPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ABRA_CODEX_COMMAND")); override != "" {
		return override, nil
	}
	macPath := "/Applications/Codex.app/Contents/Resources/codex"
	if runtime.GOOS == "darwin" && fileExists(macPath) {
		return macPath, nil
	}
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}
	return "", errors.New("missing Codex CLI; install Codex or add `codex` to PATH")
}

func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
