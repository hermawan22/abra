package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ingestpkg "github.com/hermawan22/abra/internal/ingest"
	jobspkg "github.com/hermawan22/abra/internal/jobs"
)

func seed(ctx context.Context, args cliArgs) error {
	scope := flag(args, "scope", os.Getenv("ABRA_SCOPE"))
	if strings.TrimSpace(scope) == "" {
		scope = "repo:abra-demo"
	}
	content := strings.TrimSpace(strings.Join(args.Rest, " "))
	if content == "" {
		content = strings.Join([]string{
			"Abra is an agent-first governed brain layer for AI agents.",
			"Agents should use Abra before autonomous code changes.",
			"Abra returns citations, graph context, gap analysis, memory health, and an agent decision gate.",
		}, "\n")
	}
	if err := ingest(ctx, args, map[string]any{
		"source_type": flag(args, "source-type", "markdown"),
		"source_url":  flag(args, "source-url", "cli://seed-"+timestamp()),
		"title":       flag(args, "title", "Abra CLI Seed"),
		"scope":       scope,
		"content":     content,
		"metadata": map[string]any{
			"authority":           flag(args, "authority", "official-doc"),
			"authority_score":     floatFlag(args, "authority-score", 0.75),
			"direct_ingest_trust": "cli-seed",
			"ingest_channel":      "cli-seed",
		},
	}); err != nil {
		return err
	}
	fmt.Println("Seeded memory in " + scope)
	fmt.Println("Agents should use MCP `working_memory_compose`, `brain_think`, or `brain_review`; `abra ask` is an operator fallback.")
	return nil
}

func ingestCommand(ctx context.Context, args cliArgs) error {
	if flag(args, "path", "") == "" && flag(args, "file", "") == "" && flag(args, "text", "") == "" && len(args.Rest) > 0 {
		if info, err := os.Stat(args.Rest[0]); err == nil {
			if info.IsDir() {
				args.Flags["path"] = args.Rest[0]
				args.Rest = args.Rest[1:]
			} else {
				args.Flags["file"] = args.Rest[0]
				args.Rest = args.Rest[1:]
			}
		}
	}
	if flag(args, "path", "") != "" {
		if boolFlag(args, "tracked") || boolFlag(args, "worker") {
			if !boolFlag(args, "no-wait") {
				args.Bools["wait"] = true
			}
			return sourceIngest(ctx, args)
		}
		if boolFlag(args, "direct") && boolFlag(args, "wait") {
			return errors.New("--direct cannot be combined with --wait; direct local ingest runs synchronously")
		}
		return localPathIngest(ctx, args)
	}
	if flag(args, "git", "") != "" || flag(args, "repo", "") != "" {
		return sourceIngest(ctx, args)
	}
	scope := scopeOrDefault(args, ".")
	content := flag(args, "text", "")
	sourceURL := flag(args, "source-url", "")
	title := flag(args, "title", "CLI Note")
	if file := flag(args, "file", ""); file != "" {
		bytes, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		content = string(bytes)
		if sourceURL == "" {
			abs, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			sourceURL = localFileURL(filepath.Dir(abs), filepath.Base(abs))
		}
		if title == "CLI Note" {
			title = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		}
	}
	if content == "" {
		content = strings.TrimSpace(strings.Join(args.Rest, " "))
	}
	if content == "" {
		return errors.New("ingest requires a path, --text, --file, or positional content")
	}
	if sourceURL == "" {
		sourceURL = "cli://" + slug(title) + "-" + timestamp()
	}
	body := map[string]any{
		"source_type": flag(args, "source-type", "markdown"),
		"source_url":  sourceURL,
		"title":       title,
		"scope":       scope,
		"content":     content,
		"metadata": map[string]any{
			"authority":       flag(args, "authority", "manual-unverified"),
			"authority_score": floatFlag(args, "authority-score", 0.35),
			"ingest_channel":  "cli-direct",
		},
	}
	if approvalID := flag(args, "approval-id", ""); approvalID != "" {
		body["approval_id"] = approvalID
	}
	result, err := postJSONWithTimeout(ctx, args, "/ingest/documents", body, cliTimeout(args, defaultIngestTimeout))
	if err != nil {
		return friendlyProviderError(err)
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Ingested: " + stringValue(result["document_id"], stringValue(body["source_url"], "")))
	fmt.Println("scope: " + scope)
	return nil
}

func localPathIngest(ctx context.Context, args cliArgs) error {
	root := flag(args, "path", ".")
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	scope := scopeOrDefault(args, abs)
	sourceID := flag(args, "source-id", "")
	if sourceID == "" {
		sourceID = flag(args, "name", "")
	}
	if sourceID == "" {
		sourceID = slug(abs)
	}
	if sourceID == "" {
		sourceID = "local-" + timestamp()
	}
	source := ingestpkg.SourceSpec{
		ID:          sourceID,
		Type:        ingestpkg.SourceTypeLocalRepo,
		Root:        abs,
		Scope:       scope,
		Include:     csv(flag(args, "include", "")),
		Exclude:     csv(flag(args, "exclude", "")),
		IncludeCode: boolFlag(args, "code"),
		CodeInclude: csv(flag(args, "code-include", "")),
		CodeExclude: csv(flag(args, "code-exclude", "")),
		MaxFileBytes: int64(intFlag(
			args,
			"max-file-bytes",
			int(ingestpkg.DefaultMaxFileBytes),
		)),
		IncludeGenerated: boolFlag(args, "include-generated"),
		Metadata: map[string]string{
			"created_by":      flag(args, "created-by", "abra-cli"),
			"ingest_channel":  "cli-local",
			"authority":       flag(args, "authority", "manual-unverified"),
			"authority_score": strconv.FormatFloat(floatFlag(args, "authority-score", 0.35), 'f', -1, 64),
		},
	}
	if len(source.Include) == 0 {
		source.Include = []string{"**/*.md"}
	}
	ingestor, err := ingestpkg.NewLocalRepoMarkdownIngestor(source)
	if err != nil {
		return err
	}
	ingestResult, err := ingestor.IngestWithStats(ctx)
	if err != nil {
		return err
	}
	documents := ingestResult.Documents
	if len(documents) == 0 {
		if len(ingestResult.Skipped) > 0 {
			return fmt.Errorf("no ingestable files found; skipped %d file(s) before read (raise --max-file-bytes or pass --include-generated when appropriate)", len(ingestResult.Skipped))
		}
		return errors.New("no matching files found; adjust --include, add --code, or check --path")
	}
	continueOnError := boolFlag(args, "continue-on-error") || boolFlag(args, "continue")
	progress := !boolFlag(args, "json") && !boolFlag(args, "quiet")
	if progress {
		fmt.Printf("Ingesting files: %d\n", len(documents))
		fmt.Println("scope: " + scope)
		fmt.Println("model work can take a while on the first local embedding call; files are sent in batches.")
	}
	payloads := buildLocalIngestPayloads(abs, documents, progress)
	results, failures, err := postLocalIngestBatches(ctx, args, payloads, continueOnError, progress)
	if err != nil {
		return err
	}
	if len(results) == 0 && len(failures) == 0 {
		return fmt.Errorf("no non-empty matching files found; skipped %d empty file(s)", payloads.SkippedEmpty)
	}
	if boolFlag(args, "json") {
		if err := printJSON(map[string]any{"scope": scope, "documents": results, "failures": failures, "skipped_empty": payloads.SkippedEmpty, "skipped_files": skippedFilesForJSON(ingestResult.Skipped)}); err != nil {
			return err
		}
		if len(failures) > 0 {
			return fmt.Errorf("ingest completed with %d failure(s)", len(failures))
		}
		return nil
	}
	fmt.Printf("Ingested files: %d\n", len(results))
	if payloads.SkippedEmpty > 0 {
		fmt.Printf("Skipped empty files: %d\n", payloads.SkippedEmpty)
	}
	if len(ingestResult.Skipped) > 0 {
		fmt.Printf("Skipped files before read: %d\n", len(ingestResult.Skipped))
		for i, skipped := range ingestResult.Skipped {
			if i >= 5 {
				fmt.Printf("- ... %d more skipped file(s)\n", len(ingestResult.Skipped)-i)
				break
			}
			fmt.Printf("- %s: %s (%d bytes)\n", skipped.Path, skipped.Reason, skipped.Bytes)
		}
	}
	if len(failures) > 0 {
		fmt.Printf("Failed files: %d\n", len(failures))
		for i, failure := range failures {
			if i >= 5 {
				fmt.Printf("- ... %d more failure(s)\n", len(failures)-i)
				break
			}
			fmt.Printf("- %s: %s\n", stringValue(failure["path"], ""), stringValue(failure["error"], "unknown error"))
		}
	}
	fmt.Println("scope: " + scope)
	fmt.Println("source: " + source.ID)
	if len(failures) > 0 {
		return fmt.Errorf("ingest completed with %d failure(s)", len(failures))
	}
	return nil
}

func validateBatchResponse(result map[string]any, expected int, continueOnError bool) ([]map[string]any, error) {
	rawDocs, _ := result["documents"].([]any)
	docs := make([]map[string]any, 0, len(rawDocs))
	seen := map[int]bool{}
	successes := 0
	failures := 0
	for _, raw := range rawDocs {
		doc, _ := raw.(map[string]any)
		if doc == nil {
			return nil, errors.New("batch ingest response contains an invalid document entry")
		}
		index := intValue(doc["index"])
		if index < 0 || index >= expected {
			return nil, fmt.Errorf("batch ingest response index %d is outside expected range 0-%d", index, expected-1)
		}
		if seen[index] {
			return nil, fmt.Errorf("batch ingest response contains duplicate index %d", index)
		}
		seen[index] = true
		if stringValue(doc["status"], "") == "error" {
			failures++
		} else {
			successes++
		}
		docs = append(docs, doc)
	}
	if len(seen) != expected {
		return nil, fmt.Errorf("batch ingest response returned %d document result(s), expected %d", len(seen), expected)
	}
	accepted := intValue(result["accepted"])
	if accepted != 0 && accepted != successes {
		return nil, fmt.Errorf("batch ingest response accepted=%d, expected %d successful document result(s)", accepted, successes)
	}
	failed := intValue(result["failed"])
	if continueOnError && failed != 0 && failed != failures {
		return nil, fmt.Errorf("batch ingest response failed=%d, expected %d failed document result(s)", failed, failures)
	}
	return docs, nil
}

func planDirectIngestBatches(documents []map[string]any, limits directIngestBatchLimits) []directIngestBatch {
	limits = normalizeDirectIngestBatchLimits(limits)
	batches := make([]directIngestBatch, 0, (len(documents)+limits.MaxDocuments-1)/limits.MaxDocuments)
	start := 0
	batchBytes := directIngestBatchBasePayloadBytes()
	batchChunks := 0
	batchDocuments := 0
	for index, doc := range documents {
		docBytes := estimateDirectIngestDocumentPayloadBytes(doc)
		docChunks := estimateDirectIngestDocumentChunks(doc)
		if batchDocuments > 0 && (batchDocuments >= limits.MaxDocuments || batchBytes+docBytes > limits.MaxPayloadBytes || batchChunks+docChunks > limits.MaxChunks) {
			batches = append(batches, directIngestBatch{Start: start, End: index})
			start = index
			batchBytes = directIngestBatchBasePayloadBytes()
			batchChunks = 0
			batchDocuments = 0
		}
		batchBytes += docBytes
		batchChunks += docChunks
		batchDocuments++
	}
	if batchDocuments > 0 {
		batches = append(batches, directIngestBatch{Start: start, End: len(documents)})
	}
	return batches
}

func normalizeDirectIngestBatchLimits(limits directIngestBatchLimits) directIngestBatchLimits {
	if limits.MaxDocuments < 1 {
		limits.MaxDocuments = directIngestBatchMaxDocuments
	}
	if limits.MaxPayloadBytes < 1 {
		limits.MaxPayloadBytes = directIngestBatchMaxPayloadBytes
	}
	if limits.MaxChunks < 1 {
		limits.MaxChunks = directIngestBatchMaxChunks
	}
	return limits
}

func directIngestBatchBasePayloadBytes() int {
	return len(`{"documents":[]}`) + 256
}

func estimateDirectIngestDocumentPayloadBytes(doc map[string]any) int {
	raw, err := json.Marshal(doc)
	if err == nil {
		return len(raw) + 1
	}
	return len(stringValue(doc["content"], "")) + 1024
}

func estimateDirectIngestDocumentChunks(doc map[string]any) int {
	content := strings.TrimSpace(stringValue(doc["content"], ""))
	if content == "" {
		return 0
	}
	chunkChars := directIngestChunkEstimateChars
	if chunkChars < 1 {
		chunkChars = 1
	}
	return (len(content) + chunkChars - 1) / chunkChars
}

func buildLocalIngestPayloads(abs string, documents []ingestpkg.Document, progress bool) localIngestPayloads {
	payloads := localIngestPayloads{
		Documents:  make([]map[string]any, 0, len(documents)),
		Paths:      make([]string, 0, len(documents)),
		SourceURLs: make([]string, 0, len(documents)),
	}
	for index, doc := range documents {
		if strings.TrimSpace(doc.Content) == "" {
			payloads.SkippedEmpty++
			if progress {
				fmt.Printf("[%d/%d] skip empty %s\n", index+1, len(documents), doc.Path)
			}
			continue
		}
		metadata := stringMapToAny(doc.Metadata)
		metadata["ingest_path"] = doc.Path
		metadata["ingest_checksum"] = doc.Checksum
		metadata["ingest_fingerprint"] = doc.Fingerprint
		sourceURL := localFileURL(abs, doc.Path)
		payloads.Documents = append(payloads.Documents, map[string]any{
			"source_type": string(doc.SourceType),
			"source_url":  sourceURL,
			"source_id":   doc.SourceID,
			"title":       doc.Title,
			"scope":       doc.Scope,
			"content":     doc.Content,
			"metadata":    metadata,
		})
		payloads.Paths = append(payloads.Paths, doc.Path)
		payloads.SourceURLs = append(payloads.SourceURLs, sourceURL)
	}
	return payloads
}

func postLocalIngestBatches(ctx context.Context, args cliArgs, payloads localIngestPayloads, continueOnError, progress bool) ([]map[string]any, []map[string]any, error) {
	results := make([]map[string]any, 0, len(payloads.Documents))
	failures := make([]map[string]any, 0)
	for _, batch := range planDirectIngestBatches(payloads.Documents, defaultDirectIngestBatchLimits) {
		batchResults, batchFailures, err := postLocalIngestBatch(ctx, args, payloads, batch, continueOnError, progress)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, batchResults...)
		failures = append(failures, batchFailures...)
	}
	return results, failures, nil
}

func postLocalIngestBatch(ctx context.Context, args cliArgs, payloads localIngestPayloads, batch directIngestBatch, continueOnError, progress bool) ([]map[string]any, []map[string]any, error) {
	start, end := batch.Start, batch.End
	if progress {
		fmt.Printf("[%d-%d/%d] ingest batch\n", start+1, end, len(payloads.Documents))
	}
	payload := map[string]any{"documents": payloads.Documents[start:end]}
	if continueOnError {
		payload["continue_on_error"] = true
	}
	if approvalID := flag(args, "approval-id", ""); approvalID != "" {
		payload["approval_id"] = approvalID
	}
	result, err := postJSONWithTimeout(ctx, args, "/ingest/documents/batch", payload, cliTimeout(args, defaultIngestTimeout))
	if err != nil {
		friendly := friendlyProviderError(err)
		return nil, localBatchFailures(payloads, start, end, friendly.Error()), localBatchError(start, end, friendly, continueOnError)
	}
	items, err := validateBatchResponse(result, end-start, continueOnError)
	if err != nil {
		return nil, localBatchFailures(payloads, start, end, err.Error()), localBatchError(start, end, err, continueOnError)
	}
	results, failures := localBatchResults(payloads, start, end, items)
	if progress {
		status := "ok"
		if len(failures) > 0 {
			status = "partial"
		}
		fmt.Printf("[%d-%d/%d] %s batch\n", start+1, end, len(payloads.Documents), status)
	}
	return results, failures, nil
}

func localBatchError(start, end int, err error, continueOnError bool) error {
	if continueOnError {
		return nil
	}
	return fmt.Errorf("ingest batch %d-%d: %w", start+1, end, err)
}

func localBatchFailures(payloads localIngestPayloads, start, end int, message string) []map[string]any {
	failures := make([]map[string]any, 0, end-start)
	for index := start; index < end; index++ {
		failures = append(failures, map[string]any{
			"path":       payloads.Paths[index],
			"source_url": payloads.SourceURLs[index],
			"error":      message,
		})
	}
	return failures
}

func localBatchResults(payloads localIngestPayloads, start, end int, items []map[string]any) ([]map[string]any, []map[string]any) {
	results := []map[string]any{}
	failures := []map[string]any{}
	for _, item := range items {
		index := start + intValue(item["index"])
		if index < start || index >= end {
			continue
		}
		if stringValue(item["status"], "") == "error" {
			friendly := friendlyProviderError(errors.New(stringValue(item["error"], "unknown error")))
			failures = append(failures, map[string]any{
				"path":       payloads.Paths[index],
				"source_url": stringValue(item["source_url"], payloads.SourceURLs[index]),
				"error":      friendly.Error(),
			})
			continue
		}
		results = append(results, map[string]any{
			"path":        payloads.Paths[index],
			"source_url":  stringValue(item["source_url"], payloads.SourceURLs[index]),
			"document_id": stringValue(item["document_id"], ""),
			"chunks":      item["chunks"],
			"claims":      item["claims"],
			"entities":    item["entities"],
			"relations":   item["relations"],
		})
	}
	return results, failures
}

func skippedFilesForJSON(skipped []ingestpkg.SkippedFile) []map[string]any {
	out := make([]map[string]any, 0, len(skipped))
	for _, file := range skipped {
		out = append(out, map[string]any{
			"path":   file.Path,
			"reason": file.Reason,
			"bytes":  file.Bytes,
		})
	}
	return out
}

func validateMCPSource(ctx context.Context, args cliArgs, scope, sourceURL string, config map[string]any) error {
	source := jobspkg.SourceConfig{
		ID:             "cli-dry-run",
		Scope:          scope,
		SourceType:     ingestpkg.SourceTypeMCP,
		Name:           flag(args, "name", "mcp-dry-run"),
		BaseURL:        sourceURL,
		ConnectorKind:  flag(args, "connector", "mcp"),
		Authority:      flag(args, "authority", "manual-unverified"),
		AuthorityScore: floatFlag(args, "authority-score", 0.35),
		Config:         config,
		Metadata:       map[string]any{"channel": "cli", "dry_run": true},
	}
	timeout := cliTimeout(args, defaultHTTPTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	report, err := jobspkg.ValidateMCPSourceReport(ctx, source)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(report)
	}
	fmt.Printf("MCP source valid: %d document(s)\n", report.Count)
	for i, doc := range report.Documents {
		if i >= 5 {
			fmt.Printf("- ... %d more document(s)\n", len(report.Documents)-i)
			break
		}
		fmt.Printf("- %s  %s  %s  bytes=%d\n", doc.Scope, doc.SourceType, doc.Title, doc.ContentBytes)
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s", warning.Code)
		if warning.SourceURL != "" {
			fmt.Print(" " + warning.SourceURL)
		}
		if warning.Message != "" {
			fmt.Print(" - " + warning.Message)
		}
		fmt.Println()
	}
	return nil
}

func ingest(ctx context.Context, args cliArgs, body map[string]any) error {
	_, err := postJSONWithTimeout(ctx, args, "/ingest/documents", body, cliTimeout(args, defaultIngestTimeout))
	return err
}
