package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	ingestpkg "github.com/hermawan22/abra/internal/ingest"
	jobspkg "github.com/hermawan22/abra/internal/jobs"
)

func connectorsCommand(ctx context.Context, args cliArgs) error {
	action := "list"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "list", "ls":
		return listConnectors(ctx, args)
	case "status":
		return sourceStatus(ctx, args)
	case "logs", "log":
		return sourceLogs(ctx, args)
	case "sync", "enqueue", "run":
		return syncSource(ctx, args)
	case "mcp":
		return connectorMCP(ctx, args)
	case "webhook":
		return connectorWebhook(ctx, args)
	default:
		return fmt.Errorf("unknown connectors action %q\n\n%s", action, commandUsage("connectors"))
	}
}

func connectorMCP(ctx context.Context, args cliArgs) error {
	var err error
	args, err = applyConnectorManifest(args)
	if err != nil {
		return err
	}
	action := "register"
	if len(args.Rest) > 0 {
		switch strings.ToLower(strings.TrimSpace(args.Rest[0])) {
		case "inspect", "tools", "discover":
			action = "inspect"
			args.Rest = args.Rest[1:]
		case "template", "manifest", "sample":
			action = "template"
			args.Rest = args.Rest[1:]
		case "validate", "dry-run":
			action = "validate"
			args.Rest = args.Rest[1:]
		case "add", "wizard", "onboard":
			action = "add"
			args.Rest = args.Rest[1:]
		case "register", "create":
			action = "register"
			args.Rest = args.Rest[1:]
		}
	}
	if action == "template" {
		return connectorMCPTemplate(args)
	}
	args.Flags["type"] = "mcp"
	if flag(args, "mcp-url", "") == "" && flag(args, "url", "") == "" {
		if len(args.Rest) == 0 {
			return errors.New("connectors mcp requires --mcp-url <url> or a positional MCP HTTP URL")
		}
		args.Flags["mcp-url"] = args.Rest[0]
		args.Rest = args.Rest[1:]
	}
	if action == "inspect" {
		return inspectMCPConnector(ctx, args)
	}
	if action == "add" {
		return addMCPConnector(ctx, args)
	}
	if action == "validate" {
		args.Bools["validate"] = true
	}
	return sourceIngest(ctx, args)
}

func addMCPConnector(ctx context.Context, args cliArgs) error {
	if boolFlag(args, "json") {
		return errors.New("connectors mcp add is a guided human flow; use inspect, validate, and register separately for --json automation")
	}
	if flag(args, "tool", "") == "" && !boolFlag(args, "skip-inspect") {
		fmt.Println("Inspecting MCP connector...")
		tools, err := listMCPConnectorTools(ctx, args)
		if err != nil {
			return err
		}
		switch len(tools) {
		case 0:
			return errors.New("MCP connector returned no tools; pass --tool after fixing the upstream MCP server")
		case 1:
			args.Flags["tool"] = tools[0].Name
			fmt.Println("Selected tool: " + tools[0].Name)
		default:
			names := make([]string, 0, len(tools))
			for _, tool := range tools {
				names = append(names, tool.Name)
			}
			sort.Strings(names)
			return fmt.Errorf("MCP connector has multiple tools (%s); rerun with --tool <name>", strings.Join(names, ", "))
		}
	}
	fmt.Println("Validating MCP connector...")
	validateArgs := copyCLIArgs(args)
	validateArgs.Bools["validate"] = true
	if err := sourceIngest(ctx, validateArgs); err != nil {
		return err
	}
	if boolFlag(args, "dry-run") || boolFlag(args, "validate-only") {
		return nil
	}
	fmt.Println("Registering MCP connector...")
	return sourceIngest(ctx, args)
}

func connectorMCPTemplate(args cliArgs) error {
	template := connectorManifest{
		ID:                 firstNonEmpty(flag(args, "id", ""), "knowledge-base-example"),
		Name:               firstNonEmpty(flag(args, "name", ""), "Example Knowledge Base"),
		Scope:              scopeOrDefault(args, "."),
		MCPURL:             firstNonEmpty(flag(args, "mcp-url", ""), flag(args, "url", ""), "https://mcp.example.com/mcp"),
		Tool:               firstNonEmpty(flag(args, "tool", ""), "export_documents"),
		Arguments:          map[string]any{"collection": "docs", "limit": 50},
		ConnectorKind:      firstNonEmpty(flag(args, "connector", ""), "knowledge-base"),
		DocumentSourceType: firstNonEmpty(flag(args, "document-source-type", ""), firstNonEmpty(flag(args, "connector", ""), "markdown")),
		BearerTokenEnv:     firstNonEmpty(flag(args, "bearer-token-env", ""), "MCP_EXPORT_TOKEN"),
		HeaderEnv:          map[string]string{"X-Workspace-ID": "MCP_WORKSPACE_ID"},
		Status:             firstNonEmpty(flag(args, "status", ""), "active"),
		Authority:          firstNonEmpty(flag(args, "authority", ""), "team-convention"),
		FreshnessSeconds:   intFlag(args, "freshness-seconds", 600),
		Schedule:           firstNonEmpty(flag(args, "schedule", ""), "@every 10m"),
		VerifyQuery:        firstNonEmpty(flag(args, "verify-query", ""), "service runbook"),
		Metadata: map[string]any{
			"owner":            firstNonEmpty(flag(args, "owner", ""), "docs"),
			"connector_model":  "user_owned_mcp",
			"acl_groups":       []string{"docs"},
			"acl_passthrough":  true,
			"acl_source_field": "metadata.acl_groups",
		},
	}
	score := floatFlag(args, "authority-score", 0.7)
	template.AuthorityScore = &score
	if raw := flag(args, "arguments-json", flag(args, "args-json", "")); raw != "" {
		arguments, err := parseJSONObjectFlag(raw, "arguments-json")
		if err != nil {
			return err
		}
		template.Arguments = arguments
	}
	if headerEnv, err := parseHeaderEnvFlag(flag(args, "header-env", "")); err != nil {
		return err
	} else if len(headerEnv) > 0 {
		template.HeaderEnv = headerEnv
	}
	if metadataJSON := flag(args, "metadata-json", ""); metadataJSON != "" {
		metadata, err := parseJSONObjectFlag(metadataJSON, "metadata-json")
		if err != nil {
			return err
		}
		template.Metadata = mergeAnyMaps(template.Metadata, metadata)
	}
	encoded, err := json.MarshalIndent(template, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if output := flag(args, "output", flag(args, "out", "")); output != "" {
		if err := os.WriteFile(output, encoded, 0o644); err != nil {
			return err
		}
		if !boolFlag(args, "json") {
			fmt.Println("Wrote connector manifest: " + output)
		}
		return nil
	}
	fmt.Print(string(encoded))
	return nil
}

func applyConnectorManifest(args cliArgs) (cliArgs, error) {
	path := firstNonEmpty(flag(args, "manifest", ""), flag(args, "connector-manifest", ""))
	if path == "" {
		return args, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return args, err
	}
	var manifest connectorManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return args, fmt.Errorf("parse connector manifest %s: %w", path, err)
	}
	setFlagDefault := func(name, value string) {
		if strings.TrimSpace(value) != "" && flag(args, name, "") == "" {
			args.Flags[name] = value
		}
	}
	setFlagDefault("id", manifest.ID)
	setFlagDefault("name", manifest.Name)
	setFlagDefault("scope", manifest.Scope)
	setFlagDefault("mcp-url", firstNonEmpty(manifest.MCPURL, manifest.ServerURL, manifest.URL))
	setFlagDefault("tool", manifest.Tool)
	setFlagDefault("connector", manifest.ConnectorKind)
	setFlagDefault("document-source-type", manifest.DocumentSourceType)
	setFlagDefault("bearer-token-env", manifest.BearerTokenEnv)
	setFlagDefault("status", manifest.Status)
	setFlagDefault("authority", manifest.Authority)
	setFlagDefault("schedule", manifest.Schedule)
	setFlagDefault("verify-query", manifest.VerifyQuery)
	if manifest.AuthorityScore != nil && flag(args, "authority-score", "") == "" {
		args.Flags["authority-score"] = strconv.FormatFloat(*manifest.AuthorityScore, 'f', -1, 64)
	}
	if manifest.FreshnessSeconds > 0 && flag(args, "freshness-seconds", "") == "" {
		args.Flags["freshness-seconds"] = strconv.Itoa(manifest.FreshnessSeconds)
	}
	if len(manifest.Arguments) > 0 && flag(args, "arguments-json", "") == "" && flag(args, "args-json", "") == "" {
		encoded, err := json.Marshal(manifest.Arguments)
		if err != nil {
			return args, err
		}
		args.Flags["arguments-json"] = string(encoded)
	}
	if len(manifest.HeaderEnv) > 0 && flag(args, "header-env", "") == "" {
		parts := make([]string, 0, len(manifest.HeaderEnv))
		for header, envName := range manifest.HeaderEnv {
			parts = append(parts, header+"="+envName)
		}
		args.Flags["header-env"] = strings.Join(parts, ",")
	}
	if len(manifest.Metadata) > 0 && flag(args, "metadata-json", "") == "" {
		encoded, err := json.Marshal(manifest.Metadata)
		if err != nil {
			return args, err
		}
		args.Flags["metadata-json"] = string(encoded)
	}
	return args, nil
}

func inspectMCPConnector(ctx context.Context, args cliArgs) error {
	tools, err := listMCPConnectorTools(ctx, args)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{"status": "ok", "tools": tools, "count": len(tools)})
	}
	fmt.Printf("MCP tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("- %s", tool.Name)
		if tool.Description != "" {
			fmt.Print("  " + tool.Description)
		}
		fmt.Println()
	}
	return nil
}

func listMCPConnectorTools(ctx context.Context, args cliArgs) ([]jobspkg.MCPToolInfo, error) {
	sourceURL := firstNonEmpty(flag(args, "mcp-url", ""), flag(args, "url", ""))
	scope := scopeOrDefault(args, sourceURL)
	config := map[string]any{"server_url": sourceURL}
	if envName := flag(args, "bearer-token-env", ""); envName != "" {
		config["bearer_token_env"] = envName
	}
	if headerEnv, err := parseHeaderEnvFlag(flag(args, "header-env", "")); err != nil {
		return nil, err
	} else if len(headerEnv) > 0 {
		config["header_env"] = headerEnv
	}
	tools, err := jobspkg.ListMCPTools(ctx, jobspkg.SourceConfig{
		ID:            firstNonEmpty(flag(args, "id", ""), "cli-inspect"),
		Scope:         scope,
		SourceType:    ingestpkg.SourceTypeMCP,
		Name:          firstNonEmpty(flag(args, "name", ""), "mcp-inspect"),
		BaseURL:       sourceURL,
		ConnectorKind: flag(args, "connector", "mcp"),
		Config:        config,
	})
	return tools, err
}

func connectorWebhook(ctx context.Context, args cliArgs) error {
	action := "sample"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	body, err := connectorWebhookPayload(args)
	if err != nil {
		return err
	}
	switch action {
	case "sample":
		if secret := connectorWebhookSecret(args); secret != "" {
			return printJSON(map[string]any{"body": body, "signature": webhookSignature(secret, body)})
		}
		fmt.Println(string(body))
		return nil
	case "sign":
		secret := connectorWebhookSecret(args)
		if secret == "" {
			return errors.New("connectors webhook sign requires --secret or --secret-env")
		}
		fmt.Println(webhookSignature(secret, body))
		return nil
	case "test":
		return postConnectorWebhook(ctx, args, body)
	default:
		return fmt.Errorf("unknown connectors webhook action %q\n\n%s", action, commandUsage("connectors"))
	}
}

func connectorWebhookPayload(args cliArgs) ([]byte, error) {
	if raw := flag(args, "payload-json", ""); raw != "" {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("parse payload-json: %w", err)
		}
		return json.Marshal(value)
	}
	body := map[string]any{
		"connector_kind":  flag(args, "connector", "generic"),
		"event_type":      flag(args, "event-type", "manual_test"),
		"delivery_id":     firstNonEmpty(flag(args, "delivery-id", ""), "cli-"+timestamp()),
		"scope":           scopeOrDefault(args, "."),
		"source_type":     flag(args, "source-type", flag(args, "connector", "generic")),
		"source_url":      firstNonEmpty(flag(args, "source-url", ""), "https://example.invalid/abra-connector-test"),
		"source_id":       flag(args, "source-id", "abra-connector-test"),
		"title":           flag(args, "title", "Abra connector webhook test"),
		"content":         flag(args, "content", "Abra connector webhook test document."),
		"authority":       flag(args, "authority", "manual-unverified"),
		"authority_score": floatFlag(args, "authority-score", 0.35),
		"metadata": map[string]any{
			"channel": "cli",
		},
	}
	if metadataJSON := flag(args, "metadata-json", ""); metadataJSON != "" {
		metadata, err := parseJSONObjectFlag(metadataJSON, "metadata-json")
		if err != nil {
			return nil, err
		}
		body["metadata"] = mergeAnyMaps(body["metadata"].(map[string]any), metadata)
	}
	return json.Marshal(body)
}

func connectorWebhookSecret(args cliArgs) string {
	if secret := flag(args, "secret", ""); secret != "" {
		return secret
	}
	if envName := flag(args, "secret-env", ""); envName != "" {
		return strings.TrimSpace(os.Getenv(envName))
	}
	return ""
}

func webhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postConnectorWebhook(ctx context.Context, args cliArgs, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg(args).BaseURL, "/")+"/ingest/webhooks", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	if secret := connectorWebhookSecret(args); secret != "" {
		req.Header.Set("x-abra-signature", webhookSignature(secret, body))
	}
	result, err := doJSON(req, cliTimeout(args, defaultHTTPTimeout))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Printf("Webhook accepted: accepted=%v delivery_id=%s\n", result["accepted"], stringValue(result["delivery_id"], ""))
	return nil
}
