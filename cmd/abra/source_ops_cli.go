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

func listConnectors(ctx context.Context, args cliArgs) error {
	path := "/sources/configs?limit=" + strconv.Itoa(intFlag(args, "limit", 50))
	if scope := flag(args, "scope", os.Getenv("ABRA_SCOPE")); scope != "" {
		path += "&scope=" + urlQueryEscape(scope)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["source_configs"].([]any)
	fmt.Printf("Connectors: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		sourceType := stringValue(item["source_type"], "")
		connectorKind := stringValue(item["connector_kind"], defaultConnectorKind(sourceType))
		fmt.Printf("- %s  %s  %s  %s  %s\n",
			stringValue(item["id"], ""),
			stringValue(item["status"], ""),
			connectorKind,
			sourceType,
			stringValue(item["name"], ""),
		)
	}
	return nil
}

func listSources(ctx context.Context, args cliArgs) error {
	if len(args.Rest) > 0 {
		action := strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
		switch action {
		case "sync", "enqueue", "run":
			return syncSource(ctx, args)
		case "backfill":
			return backfillSource(ctx, args)
		case "status":
			return sourceStatus(ctx, args)
		case "logs", "log":
			return sourceLogs(ctx, args)
		case "pause":
			return setSourceStatus(ctx, args, "pause")
		case "resume":
			return setSourceStatus(ctx, args, "resume")
		default:
			return fmt.Errorf("unknown sources action %q\n\n%s", action, commandUsage("sources"))
		}
	}
	path := "/sources/configs?limit=" + strconv.Itoa(intFlag(args, "limit", 50))
	if scope := flag(args, "scope", os.Getenv("ABRA_SCOPE")); scope != "" {
		path += "&scope=" + urlQueryEscape(scope)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["source_configs"].([]any)
	fmt.Printf("Sources: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		fmt.Printf("- %s  %s  %s  %s\n", stringValue(item["id"], ""), stringValue(item["status"], ""), stringValue(item["source_type"], ""), stringValue(item["name"], ""))
	}
	return nil
}

func syncSource(ctx context.Context, args cliArgs) error {
	return enqueueSourceJob(ctx, args, "sync", flag(args, "trigger", "manual"))
}

func backfillSource(ctx context.Context, args cliArgs) error {
	return enqueueSourceJob(ctx, args, "backfill", flag(args, "trigger", "backfill"))
}

func enqueueSourceJob(ctx context.Context, args cliArgs, command, triggerType string) error {
	sourceID := firstNonEmpty(flag(args, "source-config-id", ""), flag(args, "source-id", ""), flag(args, "id", ""))
	if sourceID == "" && len(args.Rest) > 0 {
		sourceID = strings.TrimSpace(args.Rest[0])
		args.Rest = args.Rest[1:]
	}
	if sourceID == "" {
		return fmt.Errorf("sources %s requires a source config id, for example: abra sources %s source-123", command, command)
	}
	result, err := postJSON(ctx, args, "/ingestion/jobs", map[string]any{
		"source_config_id": sourceID,
		"trigger_type":     triggerType,
		"created_by":       flag(args, "created-by", "abra-cli"),
		"approval_id":      flag(args, "approval-id", ""),
		"max_attempts":     intFlag(args, "max-attempts", 3),
		"metadata":         map[string]any{"channel": "cli", "command": "sources " + command},
	})
	if err != nil {
		return err
	}
	job, _ := result["ingestion_job"].(map[string]any)
	jobID := stringValue(job["id"], "")
	scope := firstNonEmpty(stringValue(job["scope"], ""), flag(args, "scope", ""), os.Getenv("ABRA_SCOPE"))
	if boolFlag(args, "wait") {
		if scope == "" {
			return errors.New("--wait requires --scope when the enqueue response does not include scope")
		}
		if boolFlag(args, "json") {
			waitedJob, err := waitForSourceJob(ctx, args, scope, sourceID, jobID)
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"enqueue": result, "job": waitedJob})
		}
		fmt.Println("Source queued: " + sourceID)
		if jobID != "" {
			fmt.Println("Job queued: " + jobID)
		}
		fmt.Println("Check jobs: abra jobs --scope " + scope + " --source-config-id " + sourceID)
		_, err := waitForSourceJob(ctx, args, scope, sourceID, jobID)
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Source queued: " + sourceID)
	if jobID != "" {
		fmt.Println("Job queued: " + jobID)
	}
	if scope != "" {
		fmt.Println("Check jobs: abra jobs --scope " + scope + " --source-config-id " + sourceID)
	}
	return nil
}

func sourceStatus(ctx context.Context, args cliArgs) error {
	sourceID, err := sourceIDArg(args, "status")
	if err != nil {
		return err
	}
	source, err := getSourceConfigByID(ctx, args, sourceID)
	if err != nil {
		return err
	}
	scope := firstNonEmpty(flag(args, "scope", ""), stringValue(source["scope"], ""), os.Getenv("ABRA_SCOPE"))
	if scope == "" {
		return fmt.Errorf("source config %q response did not include scope; pass --scope", sourceID)
	}
	jobsResult, _, err := getJSON(ctx, args, sourceJobsPath(scope, sourceID, 1))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{"source_config": source, "latest_job": firstIngestionJob(jobsResult)})
	}
	printSourceStatus(source, firstIngestionJob(jobsResult))
	return nil
}

func sourceLogs(ctx context.Context, args cliArgs) error {
	sourceID, err := sourceIDArg(args, "logs")
	if err != nil {
		return err
	}
	scope := firstNonEmpty(flag(args, "scope", ""), os.Getenv("ABRA_SCOPE"))
	if scope == "" {
		source, err := getSourceConfigByID(ctx, args, sourceID)
		if err != nil {
			return err
		}
		scope = stringValue(source["scope"], "")
	}
	if scope == "" {
		return fmt.Errorf("source config %q response did not include scope; pass --scope", sourceID)
	}
	result, _, err := getJSON(ctx, args, sourceJobsPath(scope, sourceID, intFlag(args, "limit", 20)))
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["ingestion_jobs"].([]any)
	fmt.Printf("Source logs: %s  jobs=%d\n", sourceID, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		printSourceJobLine(item)
	}
	return nil
}

func getSourceConfigByID(ctx context.Context, args cliArgs, sourceID string) (map[string]any, error) {
	result, _, err := getJSON(ctx, args, "/sources/configs/"+url.PathEscape(sourceID))
	if err != nil {
		return nil, err
	}
	source, _ := result["source_config"].(map[string]any)
	if source == nil {
		return nil, fmt.Errorf("source config %q response did not include source_config", sourceID)
	}
	return source, nil
}

func sourceIDArg(args cliArgs, action string) (string, error) {
	sourceID := firstNonEmpty(flag(args, "source-config-id", ""), flag(args, "source-id", ""), flag(args, "id", ""))
	if sourceID == "" && len(args.Rest) > 0 {
		sourceID = strings.TrimSpace(args.Rest[0])
	}
	if sourceID == "" {
		return "", fmt.Errorf("sources %s requires a source config id, for example: abra sources %s source-123", action, action)
	}
	return sourceID, nil
}

func sourceJobsPath(scope, sourceID string, limit int) string {
	return "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&source_config_id=" + urlQueryEscape(sourceID) + "&limit=" + strconv.Itoa(limit)
}

func firstIngestionJob(result map[string]any) map[string]any {
	items, _ := result["ingestion_jobs"].([]any)
	if len(items) == 0 {
		return nil
	}
	item, _ := items[0].(map[string]any)
	return item
}

func printSourceStatus(source, latestJob map[string]any) {
	fmt.Printf("Source: %s\n", stringValue(source["id"], ""))
	fmt.Printf("status: %s\n", stringValue(source["status"], ""))
	fmt.Printf("type: %s\n", stringValue(source["source_type"], ""))
	fmt.Printf("name: %s\n", stringValue(source["name"], ""))
	fmt.Printf("authority: %s score=%v\n", stringValue(source["authority"], ""), source["authority_score"])
	if schedule := stringValue(source["schedule_cron"], ""); schedule != "" {
		fmt.Println("schedule: " + schedule)
	}
	if value := stringValue(source["last_success_at"], ""); value != "" {
		fmt.Println("last_success_at: " + value)
	}
	if value := stringValue(source["last_error"], ""); value != "" {
		fmt.Println("last_error: " + value)
	}
	if latestJob == nil {
		fmt.Println("latest_job: none")
		return
	}
	fmt.Print("latest_job: ")
	printSourceJobLine(latestJob)
}

func printSourceJobLine(item map[string]any) {
	fmt.Printf("- %s  %s  trigger=%s attempts=%v/%v seen=%v changed=%v chunks=%v claims=%v",
		stringValue(item["id"], ""),
		stringValue(item["status"], ""),
		stringValue(item["trigger_type"], ""),
		item["attempts"],
		item["max_attempts"],
		item["documents_seen"],
		item["documents_changed"],
		item["chunks_written"],
		item["claims_written"],
	)
	if message := stringValue(item["error_message"], ""); message != "" {
		fmt.Print(" error=" + message)
	}
	fmt.Println()
}

func setSourceStatus(ctx context.Context, args cliArgs, action string) error {
	sourceID := firstNonEmpty(flag(args, "source-config-id", ""), flag(args, "source-id", ""), flag(args, "id", ""))
	if sourceID == "" && len(args.Rest) > 0 {
		sourceID = strings.TrimSpace(args.Rest[0])
		args.Rest = args.Rest[1:]
	}
	if sourceID == "" {
		return fmt.Errorf("sources %s requires a source config id, for example: abra sources %s source-123", action, action)
	}
	body := map[string]any{
		"created_by": flag(args, "created-by", "abra-cli"),
		"metadata":   map[string]any{"channel": "cli", "command": "sources " + action},
	}
	if approvalID := flag(args, "approval-id", ""); approvalID != "" {
		body["approval_id"] = approvalID
	}
	result, err := postJSON(ctx, args, "/sources/configs/"+url.PathEscape(sourceID)+"/"+action, body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	source, _ := result["source_config"].(map[string]any)
	fmt.Printf("Source %s: %s  status=%s\n", action+"d", sourceID, stringValue(source["status"], ""))
	return nil
}

func listJobs(ctx context.Context, args cliArgs) error {
	scope := scopeOrDefault(args, ".")
	path := "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&limit=" + strconv.Itoa(intFlag(args, "limit", 20))
	if sourceID := flag(args, "source-config-id", ""); sourceID != "" {
		path += "&source_config_id=" + urlQueryEscape(sourceID)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["ingestion_jobs"].([]any)
	fmt.Printf("Jobs: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		fmt.Printf("- %s  %s  seen=%v changed=%v source=%s\n",
			stringValue(item["id"], ""),
			stringValue(item["status"], ""),
			item["documents_seen"],
			item["documents_changed"],
			stringValue(item["source_config_id"], ""),
		)
	}
	return nil
}
