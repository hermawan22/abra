package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hermawan22/abra/internal/memory"
)

var (
	errMCPAccessDenied     = errors.New("access denied")
	errMCPResourceNotFound = errors.New("resource not found")
	errMCPPromptNotFound   = errors.New("prompt not found")
)

func mcpCapabilities() map[string]any {
	return map[string]any{
		"tools":     map[string]any{},
		"resources": map[string]any{},
		"prompts":   map[string]any{},
	}
}

func mcpResources() []map[string]any {
	return []map[string]any{
		{
			"uri":         "abra://guide/agent-workflow",
			"name":        "agent-workflow",
			"title":       "Abra Agent Workflow",
			"description": "Read-only guide for using Abra as a source-backed working-memory and decision gate.",
			"mimeType":    "text/markdown",
			"annotations": map[string]any{"audience": []string{"assistant"}, "priority": 0.9},
		},
	}
}

func mcpResourceTemplates() []map[string]any {
	return []map[string]any{
		{
			"uriTemplate": "abra://memory/health/{scope}",
			"name":        "memory-health",
			"title":       "Scoped Memory Health",
			"description": "Read a scoped memory-health snapshot. Requires read access to the requested scope.",
			"mimeType":    "application/json",
			"annotations": map[string]any{"audience": []string{"assistant"}, "priority": 0.8},
		},
		{
			"uriTemplate": "abra://working-memory/{scope}/{task}",
			"name":        "working-memory",
			"title":       "Scoped Working Memory",
			"description": "Compose a bounded working-memory packet for a task. Requires read access to the requested scope.",
			"mimeType":    "application/json",
			"annotations": map[string]any{"audience": []string{"assistant"}, "priority": 1.0},
		},
	}
}

func (h *handler) mcpReadResource(r *http.Request, uri string) (map[string]any, error) {
	switch {
	case uri == "abra://guide/agent-workflow":
		return mcpTextResource(uri, "text/markdown", strings.TrimSpace(`
# Abra Agent Workflow

1. Use `+"`policy_plan`"+` before task, before code, or after task to get scoped recall queries.
2. Use `+"`working_memory_compose`"+` before implementation to get source-backed facts, summaries, graph context, risks, validation steps, memory health, and the agent decision gate.
3. Obey `+"`agent_decision`"+`. If it returns `+"`blocked`"+` or `+"`needs_review`"+`, use the allowed next actions instead of bypassing the gate.
4. Treat `+"`conflicts`"+` and `+"`graph_warnings`"+` as review work, not approval work.
5. Use `+"`propose_learning`"+` for candidate improvements. Do not promote memory without review.
6. Use ACL and agent-action policies for durable runtime controls rather than embedding permissions in prompt text.
`)), nil
	case strings.HasPrefix(uri, "abra://memory/health/"):
		scope, err := decodeResourceTail(uri, "abra://memory/health/")
		if err != nil {
			return nil, err
		}
		if !mcpAllows(r, authActionRead, scope) {
			return nil, errMCPAccessDenied
		}
		health, err := h.db.MemoryHealth(r.Context(), scope)
		if err != nil {
			return nil, err
		}
		return mcpJSONResource(uri, health)
	case strings.HasPrefix(uri, "abra://working-memory/"):
		scope, task, err := decodeWorkingMemoryURI(uri)
		if err != nil {
			return nil, err
		}
		if !mcpAllows(r, authActionRead, scope) {
			return nil, errMCPAccessDenied
		}
		packet, err := h.memory.Compose(r.Context(), memoryComposeInput(scope, task))
		if err != nil {
			return nil, err
		}
		return mcpJSONResource(uri, packet)
	default:
		return nil, errMCPResourceNotFound
	}
}

func mcpPrompts() []map[string]any {
	return []map[string]any{
		{
			"name":        "abra-before-code",
			"title":       "Abra Before Code",
			"description": "Guide an agent to fetch policy-planned, source-backed working memory before changing code.",
			"arguments": []map[string]any{
				{"name": "task", "description": "The implementation or investigation task.", "required": true},
				{"name": "scope", "description": "The Abra memory scope to use.", "required": true},
				{"name": "agent", "description": "Optional agent/profile key.", "required": false},
			},
		},
		{
			"name":        "abra-review-memory",
			"title":       "Abra Review Memory",
			"description": "Guide an operator or agent to inspect health, conflicts, and learning proposals before trusting memory.",
			"arguments": []map[string]any{
				{"name": "scope", "description": "The Abra memory scope to review.", "required": true},
			},
		},
	}
}

func mcpPrompt(name string, args map[string]any) (map[string]any, error) {
	switch name {
	case "abra-before-code":
		task := strings.TrimSpace(stringArg(args, "task"))
		scope := strings.TrimSpace(stringArg(args, "scope"))
		agent := strings.TrimSpace(stringArg(args, "agent"))
		if task == "" || scope == "" {
			return nil, fmt.Errorf("task and scope are required")
		}
		text := "Before changing code, call `policy_plan` with hook `before_code`, task " + quoteText(task) + ", and scope " + quoteText(scope) + ". Then call `working_memory_compose` with the same task and scope"
		if agent != "" {
			text += " and agent " + quoteText(agent)
		}
		text += ". Use the returned `agent_decision`, `verification`, `memory_health`, `conflicts`, `impact_map`, and `validation_plan` as the implementation gate. Do not proceed autonomously if the decision is blocked or needs review."
		return mcpPromptResult("Fetch source-backed working memory before code changes.", text), nil
	case "abra-review-memory":
		scope := strings.TrimSpace(stringArg(args, "scope"))
		if scope == "" {
			return nil, fmt.Errorf("scope is required")
		}
		text := "Review Abra memory scope " + quoteText(scope) + " before trusting it. Call `memory_health`, inspect critical or review signals, call `list_conflicts` for open conflicts, and review pending learning proposals. Resolve conflicts through conflict-review tools; do not use approval requests to bypass contradictory memory."
		return mcpPromptResult("Review memory health and conflict readiness.", text), nil
	default:
		return nil, fmt.Errorf("%w: %s", errMCPPromptNotFound, name)
	}
}

func (h *handler) mcpResourceRead(w http.ResponseWriter, r *http.Request, id any, params map[string]any) {
	uri := strings.TrimSpace(stringArg(params, "uri"))
	if uri == "" {
		writeMCPError(w, id, -32602, "uri is required")
		return
	}
	result, err := h.mcpReadResource(r, uri)
	if err != nil {
		switch {
		case errors.Is(err, errMCPResourceNotFound):
			writeMCPError(w, id, -32002, "resource not found")
		case errors.Is(err, errMCPAccessDenied):
			writeMCPError(w, id, -32001, "access denied")
		default:
			writeMCPError(w, id, -32000, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (h *handler) mcpPromptGet(w http.ResponseWriter, id any, params map[string]any) {
	name := strings.TrimSpace(stringArg(params, "name"))
	if name == "" {
		writeMCPError(w, id, -32602, "name is required")
		return
	}
	args := mapArg(params, "arguments")
	if args == nil {
		args = map[string]any{}
	}
	result, err := mcpPrompt(name, args)
	if err != nil {
		if errors.Is(err, errMCPPromptNotFound) {
			writeMCPError(w, id, -32003, "prompt not found")
			return
		}
		writeMCPError(w, id, -32602, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func mcpPromptResult(description, text string) map[string]any {
	return map[string]any{
		"description": description,
		"messages": []map[string]any{
			{"role": "user", "content": map[string]any{"type": "text", "text": text}},
		},
	}
}

func mcpTextResource(uri, mimeType, text string) map[string]any {
	return map[string]any{"contents": []map[string]any{{"uri": uri, "mimeType": mimeType, "text": text}}}
}

func mcpJSONResource(uri string, value any) (map[string]any, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpTextResource(uri, "application/json", string(raw)), nil
}

func decodeResourceTail(uri, prefix string) (string, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(uri, prefix))
	if raw == "" {
		return "", fmt.Errorf("resource path is required")
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", fmt.Errorf("invalid resource uri: %w", err)
	}
	if strings.TrimSpace(decoded) == "" {
		return "", fmt.Errorf("resource path is required")
	}
	return decoded, nil
}

func decodeWorkingMemoryURI(uri string) (string, string, error) {
	tail := strings.TrimSpace(strings.TrimPrefix(uri, "abra://working-memory/"))
	parts := strings.SplitN(tail, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("working-memory resource must include scope and task")
	}
	scope, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("invalid scope in resource uri: %w", err)
	}
	task, err := url.PathUnescape(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("invalid task in resource uri: %w", err)
	}
	scope = strings.TrimSpace(scope)
	task = strings.TrimSpace(task)
	if scope == "" || task == "" {
		return "", "", fmt.Errorf("scope and task are required")
	}
	return scope, task, nil
}

func memoryComposeInput(scope, task string) memory.ComposeInput {
	return memory.ComposeInput{Scope: scope, Task: task, Hook: "before_task", Limit: 5, MaxQueries: 5, TokenBudget: 900}
}

func mcpAllows(r *http.Request, action authAction, scope string) bool {
	principal := principalFromContext(r.Context())
	if principal == nil {
		return false
	}
	return principal.allows(action, strings.TrimSpace(scope))
}

func writeMCPError(w http.ResponseWriter, id any, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	})
}

func quoteText(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "'") + "`"
}
