package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func pluginCommand(ctx context.Context, args cliArgs) error {
	action := "list"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "list", "ls":
		if boolFlag(args, "json") {
			return printJSON(map[string]any{
				"core_contracts": []string{"mcp-source", "http-ingest", "signed-webhook", "agent-mcp"},
				"boundary":       "plugins adapt external systems into normalized Abra documents; core owns transform, governance, memory, and retrieval",
			})
		}
		fmt.Println("Plugin contracts:")
		fmt.Println("- mcp-source     external MCP tool returns normalized Abra documents")
		fmt.Println("- http-ingest    external job pushes normalized documents to /ingest")
		fmt.Println("- signed-webhook external event source pushes signed ingestion events")
		fmt.Println("- agent-mcp      AI client connects through Abra MCP")
		fmt.Println("Core stays frozen: transform, chunking, embeddings, graph, citations, approvals, and decision gates are not plugin bypass points.")
		return nil
	case "mcp", "source":
		return connectorMCP(ctx, args)
	case "webhook":
		return connectorWebhook(ctx, args)
	case "contract", "schema":
		return printPluginContract(args)
	default:
		return fmt.Errorf("unknown plugin command %q\n\n%s", action, commandUsage("plugin"))
	}
}

func printPluginContract(args cliArgs) error {
	contract := map[string]any{
		"documents": []map[string]any{{
			"source_type":       "markdown",
			"source_url":        "https://source.example/doc/123",
			"source_id":         "123",
			"title":             "Source-backed document",
			"scope":             "team:docs",
			"content":           "Markdown or plain text content.",
			"source_updated_at": "2026-06-22T00:00:00Z",
			"metadata": map[string]any{
				"authority":          "official-doc",
				"authority_score":    0.8,
				"allowed_principals": []string{"group:docs"},
				"owner":              "team:docs",
			},
		}},
	}
	if boolFlag(args, "json") {
		return printJSON(contract)
	}
	bytes, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(bytes))
	return nil
}
