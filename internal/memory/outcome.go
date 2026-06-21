package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/store"
)

const (
	MaxTaskOutcomeFiles          = 50
	MaxTaskOutcomeCommands       = 20
	MaxTaskOutcomeTestCommands   = 10
	MaxTaskOutcomeMissingContext = 20
	MaxTaskOutcomeMemoryRefs     = 50
	MaxTaskOutcomeTextLength     = 500
	MaxTaskOutcomeCommandLength  = 300
)

type TaskOutcomeInput struct {
	Task           string                `json:"task"`
	Scope          string                `json:"scope"`
	Hook           string                `json:"hook,omitempty"`
	Agent          string                `json:"agent,omitempty"`
	Outcome        string                `json:"outcome,omitempty"`
	Summary        string                `json:"summary,omitempty"`
	FilesChanged   []string              `json:"files_changed,omitempty"`
	CommandsRun    []CommandOutcome      `json:"commands_run,omitempty"`
	Tests          TestOutcome           `json:"tests_result,omitempty"`
	MissingContext []string              `json:"missing_context,omitempty"`
	MemoryRefsUsed []MemoryReferenceUsed `json:"memory_refs_used,omitempty"`
	CompletedAt    string                `json:"completed_at,omitempty"`
	SourceURL      string                `json:"source_url,omitempty"`
	CreatedBy      string                `json:"created_by,omitempty"`
	ApprovalID     string                `json:"approval_id,omitempty"`
	Metadata       map[string]any        `json:"metadata,omitempty"`
}

type CommandOutcome struct {
	Command    string `json:"command"`
	Status     string `json:"status,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

type TestOutcome struct {
	Status   string   `json:"status,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Commands []string `json:"commands,omitempty"`
}

type MemoryReferenceUsed struct {
	Ref       string `json:"ref,omitempty"`
	Kind      string `json:"kind,omitempty"`
	ID        string `json:"id,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

type TaskOutcomeCaptureResult struct {
	Observation         store.ObservationResult        `json:"observation"`
	LearningProposals   []store.LearningProposalRecord `json:"learning_proposals,omitempty"`
	LearningProposalNew int                            `json:"learning_proposal_new"`
	PatternsConsidered  []OutcomePattern               `json:"patterns_considered,omitempty"`
}

type OutcomePattern struct {
	Kind           string   `json:"kind"`
	Key            string   `json:"key"`
	Occurrences    int      `json:"occurrences"`
	ObservationIDs []string `json:"observation_ids,omitempty"`
}

func NormalizeTaskOutcome(input TaskOutcomeInput) TaskOutcomeInput {
	input.Task = trimBounded(input.Task, MaxTaskOutcomeTextLength)
	input.Scope = strings.TrimSpace(input.Scope)
	input.Hook = strings.TrimSpace(input.Hook)
	if input.Hook != "after_task" {
		input.Hook = "after_task"
	}
	input.Agent = trimBounded(input.Agent, 120)
	input.Outcome = normalizeOutcomeStatus(input.Outcome)
	input.Summary = trimBounded(input.Summary, MaxTaskOutcomeTextLength)
	input.FilesChanged = boundedStringList(input.FilesChanged, MaxTaskOutcomeFiles, MaxTaskOutcomeCommandLength)
	input.CommandsRun = boundedCommandOutcomes(input.CommandsRun)
	input.Tests = boundedTestOutcome(input.Tests)
	input.MissingContext = boundedStringList(input.MissingContext, MaxTaskOutcomeMissingContext, MaxTaskOutcomeTextLength)
	input.MemoryRefsUsed = boundedMemoryRefs(input.MemoryRefsUsed)
	input.CompletedAt = strings.TrimSpace(input.CompletedAt)
	if input.CompletedAt == "" {
		input.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	input.SourceURL = strings.TrimSpace(input.SourceURL)
	input.CreatedBy = trimBounded(input.CreatedBy, 120)
	input.ApprovalID = strings.TrimSpace(input.ApprovalID)
	if input.Metadata == nil {
		input.Metadata = map[string]any{}
	}
	return input
}

func TaskOutcomeSourceID(input TaskOutcomeInput) string {
	input = NormalizeTaskOutcome(input)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		input.Scope,
		input.Task,
		input.Agent,
		input.Outcome,
		input.CompletedAt,
		strings.Join(input.FilesChanged, "\x00"),
	}, "\x00")))
	return "task-outcome-" + hex.EncodeToString(sum[:])[:24]
}

func TaskOutcomeSourceURL(input TaskOutcomeInput) string {
	input = NormalizeTaskOutcome(input)
	if input.SourceURL != "" {
		return input.SourceURL
	}
	return "abra://task-outcome/" + TaskOutcomeSourceID(input)
}

func TaskOutcomeObservationText(input TaskOutcomeInput) string {
	input = NormalizeTaskOutcome(input)
	parts := []string{"Task outcome: " + input.Outcome + "."}
	if input.Task != "" {
		parts = append(parts, "Task: "+input.Task+".")
	}
	if input.Summary != "" {
		parts = append(parts, "Summary: "+input.Summary+".")
	}
	if len(input.FilesChanged) > 0 {
		parts = append(parts, fmt.Sprintf("Files changed: %s.", strings.Join(input.FilesChanged, ", ")))
	}
	if input.Tests.Status != "" {
		parts = append(parts, "Tests: "+input.Tests.Status+".")
	}
	if input.Tests.Summary != "" {
		parts = append(parts, "Test summary: "+input.Tests.Summary+".")
	}
	if len(input.MissingContext) > 0 {
		parts = append(parts, "Missing context: "+strings.Join(input.MissingContext, "; ")+".")
	}
	if len(input.MemoryRefsUsed) > 0 {
		parts = append(parts, fmt.Sprintf("Memory refs used: %d.", len(input.MemoryRefsUsed)))
	}
	return strings.Join(parts, " ")
}

func TaskOutcomeValue(input TaskOutcomeInput) map[string]any {
	input = NormalizeTaskOutcome(input)
	return map[string]any{
		"task":             input.Task,
		"hook":             input.Hook,
		"agent":            input.Agent,
		"outcome":          input.Outcome,
		"summary":          input.Summary,
		"files_changed":    input.FilesChanged,
		"commands_run":     commandOutcomeValues(input.CommandsRun),
		"tests_result":     testOutcomeValue(input.Tests),
		"missing_context":  input.MissingContext,
		"memory_refs_used": memoryRefValues(input.MemoryRefsUsed),
		"completed_at":     input.CompletedAt,
	}
}

func TaskOutcomeMetadata(input TaskOutcomeInput) map[string]any {
	input = NormalizeTaskOutcome(input)
	metadata := map[string]any{}
	for key, value := range input.Metadata {
		metadata[key] = value
	}
	metadata["task_outcome"] = true
	metadata["bounded"] = true
	metadata["hook"] = input.Hook
	metadata["agent"] = input.Agent
	metadata["outcome"] = input.Outcome
	metadata["files_changed_count"] = len(input.FilesChanged)
	metadata["commands_run_count"] = len(input.CommandsRun)
	metadata["missing_context_count"] = len(input.MissingContext)
	metadata["memory_refs_used_count"] = len(input.MemoryRefsUsed)
	metadata["tests_status"] = input.Tests.Status
	return metadata
}

func OutcomePatternID(scope, kind, key string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(scope),
		strings.TrimSpace(kind),
		normalizePatternKey(key),
	}, "\x00")))
	return "outcome-pattern-" + hex.EncodeToString(sum[:])[:24]
}

func ObservationHasMissingContext(observation store.ObservationResult, missing string) bool {
	key := normalizePatternKey(missing)
	if key == "" {
		return false
	}
	if listContainsPattern(observation.Value["missing_context"], key) {
		return true
	}
	return strings.Contains(normalizePatternKey(observation.ObservationText), key)
}

func ObservationHasFailedCommand(observation store.ObservationResult, command string) bool {
	key := normalizePatternKey(command)
	if key == "" {
		return false
	}
	if tests, ok := observation.Value["tests_result"].(map[string]any); ok {
		if normalizePatternKey(fmt.Sprint(tests["status"])) == "failed" && listContainsPattern(tests["commands"], key) {
			return true
		}
	}
	rawCommands, ok := observation.Value["commands_run"].([]any)
	if !ok {
		return false
	}
	for _, raw := range rawCommands {
		commandMap, ok := raw.(map[string]any)
		if !ok || normalizePatternKey(commandMap["command"]) != key {
			continue
		}
		status := normalizePatternKey(commandMap["status"])
		exitCode, _ := commandMap["exit_code"].(float64)
		if status == "failed" || exitCode != 0 {
			return true
		}
	}
	return false
}

func normalizeOutcomeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "success", "ok", "passed", "pass":
		return "succeeded"
	case "failed", "failure", "error":
		return "failed"
	case "partial", "partially_succeeded", "incomplete":
		return "partial"
	case "blocked":
		return "blocked"
	default:
		return "unknown"
	}
}

func boundedCommandOutcomes(commands []CommandOutcome) []CommandOutcome {
	if len(commands) > MaxTaskOutcomeCommands {
		commands = commands[:MaxTaskOutcomeCommands]
	}
	out := make([]CommandOutcome, 0, len(commands))
	for _, command := range commands {
		command.Command = trimBounded(command.Command, MaxTaskOutcomeCommandLength)
		if command.Command == "" {
			continue
		}
		command.Status = normalizeCommandStatus(command.Status, command.ExitCode)
		if command.DurationMS < 0 {
			command.DurationMS = 0
		}
		out = append(out, command)
	}
	return out
}

func boundedTestOutcome(test TestOutcome) TestOutcome {
	test.Status = normalizeTestStatus(test.Status)
	test.Summary = trimBounded(test.Summary, MaxTaskOutcomeTextLength)
	test.Commands = boundedStringList(test.Commands, MaxTaskOutcomeTestCommands, MaxTaskOutcomeCommandLength)
	return test
}

func boundedMemoryRefs(refs []MemoryReferenceUsed) []MemoryReferenceUsed {
	if len(refs) > MaxTaskOutcomeMemoryRefs {
		refs = refs[:MaxTaskOutcomeMemoryRefs]
	}
	out := make([]MemoryReferenceUsed, 0, len(refs))
	for _, ref := range refs {
		ref.Ref = trimBounded(ref.Ref, 80)
		ref.Kind = trimBounded(ref.Kind, 80)
		ref.ID = trimBounded(ref.ID, 160)
		ref.SourceURL = trimBounded(ref.SourceURL, MaxTaskOutcomeCommandLength)
		if ref.Ref == "" && ref.ID == "" && ref.SourceURL == "" {
			continue
		}
		out = append(out, ref)
	}
	return out
}

func boundedStringList(values []string, limit, maxLength int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = trimBounded(value, maxLength)
		key := normalizePatternKey(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func trimBounded(value string, maxLength int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if maxLength > 0 && len(value) > maxLength {
		value = strings.TrimSpace(value[:maxLength])
	}
	return value
}

func normalizeCommandStatus(status string, exitCode int) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "succeeded", "success", "ok", "passed", "pass":
		return "succeeded"
	case "failed", "failure", "error":
		return "failed"
	case "skipped", "not_run":
		return "skipped"
	}
	if exitCode != 0 {
		return "failed"
	}
	return "unknown"
}

func normalizeTestStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "success", "ok", "passed", "pass":
		return "passed"
	case "failed", "failure", "error":
		return "failed"
	case "skipped", "not_run":
		return "skipped"
	default:
		return "unknown"
	}
}

func commandOutcomeValues(commands []CommandOutcome) []map[string]any {
	out := make([]map[string]any, 0, len(commands))
	for _, command := range commands {
		out = append(out, map[string]any{
			"command":     command.Command,
			"status":      command.Status,
			"exit_code":   command.ExitCode,
			"duration_ms": command.DurationMS,
		})
	}
	return out
}

func testOutcomeValue(test TestOutcome) map[string]any {
	return map[string]any{
		"status":   test.Status,
		"summary":  test.Summary,
		"commands": test.Commands,
	}
}

func memoryRefValues(refs []MemoryReferenceUsed) []map[string]any {
	out := make([]map[string]any, 0, len(refs))
	for _, ref := range refs {
		out = append(out, map[string]any{
			"ref":        ref.Ref,
			"kind":       ref.Kind,
			"id":         ref.ID,
			"source_url": ref.SourceURL,
		})
	}
	return out
}

func normalizePatternKey(value any) string {
	return strings.ToLower(strings.Join(strings.Fields(fmt.Sprint(value)), " "))
}

func listContainsPattern(raw any, key string) bool {
	values, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if normalizePatternKey(value) == key {
			return true
		}
	}
	return false
}
