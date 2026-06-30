package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func watch(ctx context.Context, args cliArgs) error {
	if len(args.Rest) == 0 {
		return errors.New(commandUsage("watch"))
	}
	mode := strings.ToLower(strings.TrimSpace(args.Rest[0]))
	args.Rest = args.Rest[1:]
	switch mode {
	case "local", "path", "repo":
		if flag(args, "path", "") == "" {
			args.Flags["path"] = "."
		}
	case "git", "github", "remote":
		if flag(args, "git", "") == "" && flag(args, "repo", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("watch git requires --git <url> or a positional repo URL")
			}
			args.Flags["git"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
	case "mcp":
		if flag(args, "mcp-url", "") == "" && flag(args, "url", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("source mcp requires --mcp-url <url> or a positional MCP HTTP URL")
			}
			args.Flags["mcp-url"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
	default:
		return fmt.Errorf("unknown watch mode %q\n\n%s", mode, commandUsage("watch"))
	}
	return sourceIngest(ctx, args)
}

func connectCommand(ctx context.Context, args cliArgs) error {
	if len(args.Rest) == 0 {
		return errors.New(commandUsage("connect"))
	}
	action := strings.ToLower(strings.TrimSpace(args.Rest[0]))
	args.Rest = args.Rest[1:]
	switch action {
	case "list", "ls", "sources":
		return listSources(ctx, args)
	case "status":
		return sourceStatus(ctx, args)
	case "logs", "log":
		return sourceLogs(ctx, args)
	case "pause":
		return setSourceStatus(ctx, args, "pause")
	case "resume":
		return setSourceStatus(ctx, args, "resume")
	case "local", "path", "repo":
		if flag(args, "path", "") == "" {
			if len(args.Rest) > 0 {
				args.Flags["path"] = args.Rest[0]
				args.Rest = args.Rest[1:]
			} else {
				args.Flags["path"] = "."
			}
		}
		if !boolFlag(args, "direct") {
			args.Bools["tracked"] = true
			if !boolFlag(args, "no-wait") {
				args.Bools["wait"] = true
			}
		}
		return ingestCommand(ctx, args)
	case "git", "github", "remote":
		if flag(args, "git", "") == "" && flag(args, "repo", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("connect git requires a repository URL")
			}
			args.Flags["git"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
		if !boolFlag(args, "no-wait") {
			args.Bools["wait"] = true
		}
		return sourceIngest(ctx, args)
	case "mcp":
		if flag(args, "mcp-url", "") == "" && flag(args, "url", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("connect mcp requires an MCP HTTP URL")
			}
			args.Flags["mcp-url"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
		if boolFlag(args, "dry-run") || boolFlag(args, "validate") {
			return sourceIngest(ctx, args)
		}
		if !boolFlag(args, "no-wait") {
			args.Bools["wait"] = true
		}
		return sourceIngest(ctx, args)
	case "webhook":
		return connectorWebhook(ctx, args)
	case "agent":
		return agentCommand(ctx, args)
	case "model":
		return modelCommand(ctx, args)
	default:
		if _, err := os.Stat(action); err == nil {
			args.Rest = append([]string{action}, args.Rest...)
			args.Flags["path"] = action
			args.Bools["tracked"] = true
			if !boolFlag(args, "no-wait") {
				args.Bools["wait"] = true
			}
			return ingestCommand(ctx, args)
		}
		return fmt.Errorf("unknown connect target %q\n\n%s", action, commandUsage("connect"))
	}
}

func syncCommand(ctx context.Context, args cliArgs) error {
	if len(args.Rest) == 0 {
		if id := firstNonEmpty(flag(args, "source-config-id", ""), flag(args, "source-id", ""), flag(args, "id", "")); id != "" {
			args.Rest = []string{id}
			return syncSource(ctx, args)
		}
		return errors.New(commandUsage("sync"))
	}
	action := strings.ToLower(strings.TrimSpace(args.Rest[0]))
	switch action {
	case "jobs":
		args.Rest = args.Rest[1:]
		return listJobs(ctx, args)
	case "status":
		args.Rest = args.Rest[1:]
		if len(args.Rest) > 0 || flag(args, "source-config-id", "") != "" || flag(args, "source-id", "") != "" || flag(args, "id", "") != "" {
			return sourceStatus(ctx, args)
		}
		return listJobs(ctx, args)
	case "logs", "log":
		args.Rest = args.Rest[1:]
		return sourceLogs(ctx, args)
	case "git", "github", "remote":
		args.Rest = args.Rest[1:]
		if flag(args, "git", "") == "" && flag(args, "repo", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("sync git requires a repository URL")
			}
			args.Flags["git"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
		if !boolFlag(args, "no-wait") {
			args.Bools["wait"] = true
		}
		return sourceIngest(ctx, args)
	case "mcp":
		args.Rest = args.Rest[1:]
		if !boolFlag(args, "no-wait") {
			args.Bools["wait"] = true
		}
		return sourceIngest(ctx, args)
	}
	if _, err := os.Stat(args.Rest[0]); err == nil {
		return ingestCommand(ctx, args)
	}
	return syncSource(ctx, args)
}

func sourceIngestSpecFromArgs(args cliArgs) (sourceIngestSpec, error) {
	config := map[string]any{}
	if repo := firstNonEmpty(flag(args, "git", ""), flag(args, "repo", "")); repo != "" {
		config["repository_url"] = repo
		if ref := firstNonEmpty(flag(args, "ref", ""), flag(args, "branch", "")); ref != "" {
			config["git_ref"] = ref
		}
		config["git_depth"] = intFlag(args, "depth", 1)
		return sourceIngestSpec{SourceType: "git_repo", SourceURL: repo, ScopeHint: repo, Config: config}, nil
	}
	if flag(args, "mcp-url", "") != "" || flag(args, "url", "") != "" || strings.EqualFold(flag(args, "type", ""), "mcp") {
		return mcpSourceIngestSpec(args, config)
	}
	path := flag(args, "path", ".")
	abs, err := filepath.Abs(path)
	if err != nil {
		return sourceIngestSpec{}, err
	}
	config["root"] = abs
	sourceURL := "file://" + filepath.ToSlash(abs)
	return sourceIngestSpec{SourceType: "local_repo", SourceURL: sourceURL, ScopeHint: abs, Config: config}, nil
}

func mcpSourceIngestSpec(args cliArgs, config map[string]any) (sourceIngestSpec, error) {
	sourceURL := firstNonEmpty(flag(args, "mcp-url", ""), flag(args, "url", ""))
	tool := flag(args, "tool", "")
	if tool == "" {
		return sourceIngestSpec{}, errors.New("source mcp requires --tool <mcp-tool-name>")
	}
	config["server_url"] = sourceURL
	config["tool"] = tool
	if raw := flag(args, "arguments-json", flag(args, "args-json", "")); raw != "" {
		arguments, err := parseJSONObjectFlag(raw, "arguments-json")
		if err != nil {
			return sourceIngestSpec{}, err
		}
		config["arguments"] = arguments
	} else {
		config["arguments"] = map[string]any{}
	}
	if envName := flag(args, "bearer-token-env", ""); envName != "" {
		config["bearer_token_env"] = envName
	}
	if headerEnv, err := parseHeaderEnvFlag(flag(args, "header-env", "")); err != nil {
		return sourceIngestSpec{}, err
	} else if len(headerEnv) > 0 {
		config["header_env"] = headerEnv
	}
	if docSourceType := flag(args, "document-source-type", ""); docSourceType != "" {
		config["document_source_type"] = docSourceType
	}
	if boolFlag(args, "allow-private-network") {
		config["allow_private_network"] = true
	}
	if boolFlag(args, "allow-scope-expansion") {
		config["allow_scope_expansion"] = true
	}
	return sourceIngestSpec{SourceType: "mcp", SourceURL: sourceURL, ScopeHint: sourceURL, Config: config}, nil
}

func sourceIngest(ctx context.Context, args cliArgs) error {
	var err error
	args, err = applyConnectorManifest(args)
	if err != nil {
		return err
	}
	spec, err := sourceIngestSpecFromArgs(args)
	if err != nil {
		return err
	}
	scope := scopeOrDefault(args, spec.ScopeHint)
	if spec.SourceType == "mcp" && (boolFlag(args, "dry-run") || boolFlag(args, "validate")) {
		return validateMCPSource(ctx, args, scope, spec.SourceURL, spec.Config)
	}
	if spec.SourceType != "mcp" {
		applyNonMCPSourceConfig(args, spec.Config)
	}
	name := flag(args, "name", "")
	if name == "" {
		name = slug(strings.TrimPrefix(strings.TrimPrefix(spec.SourceURL, "file://"), "https://"))
		if name == "" {
			name = "source-" + timestamp()
		}
	}
	body := map[string]any{
		"id":              flag(args, "id", ""),
		"name":            name,
		"source_type":     spec.SourceType,
		"scope":           scope,
		"base_url":        spec.SourceURL,
		"connector_kind":  flag(args, "connector", defaultConnectorKind(spec.SourceType)),
		"status":          flag(args, "status", "active"),
		"authority":       flag(args, "authority", "manual-unverified"),
		"authority_score": floatFlag(args, "authority-score", 0.35),
		"config":          spec.Config,
		"metadata": map[string]any{
			"created_by": "abra-cli",
		},
		"created_by": flag(args, "created-by", "abra-cli"),
	}
	if metadataJSON := flag(args, "metadata-json", ""); metadataJSON != "" {
		metadata, err := parseJSONObjectFlag(metadataJSON, "metadata-json")
		if err != nil {
			return err
		}
		body["metadata"] = mergeAnyMaps(body["metadata"].(map[string]any), metadata)
	}
	if freshnessSeconds := intFlag(args, "freshness-seconds", 0); freshnessSeconds > 0 {
		body["freshness_policy"] = map[string]any{"max_age_seconds": freshnessSeconds}
	}
	if schedule := flag(args, "schedule", ""); schedule != "" {
		body["schedule_cron"] = schedule
	}
	if approvalID := flag(args, "approval-id", ""); approvalID != "" {
		body["approval_id"] = approvalID
	}
	source, err := postJSON(ctx, args, "/sources/configs", body)
	if err != nil {
		return err
	}
	sourceID := stringValue(source["source_config_id"], "")
	if sourceID == "" {
		return errors.New("source config response did not include source_config_id")
	}
	status := flag(args, "status", "active")
	shouldQueue := status == "active" || status == "error"
	if !shouldQueue && (boolFlag(args, "wait") || boolFlag(args, "verify")) {
		return fmt.Errorf("--wait/--verify require an active connector source; current status is %q", status)
	}
	job := map[string]any{}
	jobID := ""
	if shouldQueue {
		job, err = postJSON(ctx, args, "/ingestion/jobs", map[string]any{
			"source_config_id": sourceID,
			"trigger_type":     flag(args, "trigger", "manual"),
			"created_by":       flag(args, "created-by", "abra-cli"),
			"approval_id":      flag(args, "approval-id", ""),
			"max_attempts":     intFlag(args, "max-attempts", 3),
			"metadata":         map[string]any{"channel": "cli"},
		})
		if err != nil {
			return err
		}
		if ingestionJob, _ := job["ingestion_job"].(map[string]any); ingestionJob != nil {
			jobID = stringValue(ingestionJob["id"], "")
		}
	}
	return printSourceIngestResult(ctx, args, sourceIngestOutput{
		Scope:       scope,
		SourceID:    sourceID,
		SourceName:  name,
		SourceURL:   spec.SourceURL,
		SourceType:  spec.SourceType,
		Status:      status,
		ShouldQueue: shouldQueue,
		Source:      source,
		Job:         job,
		JobID:       jobID,
	})
}

func applyNonMCPSourceConfig(args cliArgs, config map[string]any) {
	if include := csv(flag(args, "include", "")); len(include) > 0 {
		config["include"] = include
	} else {
		config["include"] = []string{"**/*.md"}
	}
	if exclude := csv(flag(args, "exclude", "")); len(exclude) > 0 {
		config["exclude"] = exclude
	}
	if boolFlag(args, "code") {
		config["include_code"] = true
		if codeInclude := csv(flag(args, "code-include", "")); len(codeInclude) > 0 {
			config["code_include"] = codeInclude
		}
		if codeExclude := csv(flag(args, "code-exclude", "")); len(codeExclude) > 0 {
			config["code_exclude"] = codeExclude
		}
	}
	if maxFileBytes := intFlag(args, "max-file-bytes", 0); maxFileBytes > 0 {
		config["max_file_bytes"] = maxFileBytes
	}
	if boolFlag(args, "include-generated") {
		config["include_generated"] = true
	}
}

func printSourceIngestResult(ctx context.Context, args cliArgs, out sourceIngestOutput) error {
	if boolFlag(args, "json") {
		return printSourceIngestJSON(ctx, args, out)
	}
	fmt.Println("Source configured: " + out.SourceID)
	fmt.Println("scope: " + out.Scope)
	if out.JobID != "" {
		fmt.Println("Job queued: " + out.JobID)
	} else if !out.ShouldQueue {
		fmt.Println("Job skipped: source status is " + out.Status)
	}
	fmt.Println("Check jobs: abra jobs --scope " + out.Scope)
	if boolFlag(args, "wait") {
		if _, err := waitForSourceJob(ctx, args, out.Scope, out.SourceID, out.JobID); err != nil {
			return err
		}
	}
	if boolFlag(args, "verify") {
		return verifySourceRecall(ctx, args, out.Scope, out.SourceID, sourceVerifyQuery(args, out))
	}
	if out.SourceType == "local_repo" {
		fmt.Println("Tip: local tracked sources require the worker to see the same path. Use `abra sync . --code` for direct local ingestion.")
	}
	return nil
}

func printSourceIngestJSON(ctx context.Context, args cliArgs, out sourceIngestOutput) error {
	payload := map[string]any{"source": out.Source, "job": out.Job}
	if boolFlag(args, "wait") {
		waitedJob, err := waitForSourceJob(ctx, args, out.Scope, out.SourceID, out.JobID)
		if err != nil {
			return err
		}
		payload["waited_job"] = waitedJob
	}
	if boolFlag(args, "verify") {
		verification, err := verifySourceRecallPayload(ctx, args, out.Scope, out.SourceID, sourceVerifyQuery(args, out))
		if err != nil {
			return err
		}
		payload["verification"] = verification
	}
	return printJSON(payload)
}

func sourceVerifyQuery(args cliArgs, out sourceIngestOutput) string {
	return firstNonEmpty(flag(args, "verify-query", ""), out.SourceName, out.SourceURL)
}
