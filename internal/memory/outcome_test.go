package memory

import (
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestNormalizeTaskOutcomeBoundsPayload(t *testing.T) {
	input := TaskOutcomeInput{
		Task:    strings.Repeat("ship ", 200),
		Scope:   " repo:app ",
		Outcome: "success",
	}
	for i := 0; i < MaxTaskOutcomeFiles+5; i++ {
		input.FilesChanged = append(input.FilesChanged, "internal/file.go")
	}
	for i := 0; i < MaxTaskOutcomeCommands+5; i++ {
		input.CommandsRun = append(input.CommandsRun, CommandOutcome{Command: strings.Repeat("go test ", 100), ExitCode: 1})
	}
	for i := 0; i < MaxTaskOutcomeMissingContext+5; i++ {
		input.MissingContext = append(input.MissingContext, "setup docs")
	}
	for i := 0; i < MaxTaskOutcomeMemoryRefs+5; i++ {
		input.MemoryRefsUsed = append(input.MemoryRefsUsed, MemoryReferenceUsed{Ref: "C1", SourceURL: "file:///README.md"})
	}

	got := NormalizeTaskOutcome(input)
	if got.Scope != "repo:app" {
		t.Fatalf("scope = %q, want trimmed", got.Scope)
	}
	if got.Outcome != "succeeded" {
		t.Fatalf("outcome = %q, want succeeded", got.Outcome)
	}
	if len(got.Task) > MaxTaskOutcomeTextLength {
		t.Fatalf("task length = %d, want <= %d", len(got.Task), MaxTaskOutcomeTextLength)
	}
	if len(got.FilesChanged) != 1 {
		t.Fatalf("files changed = %d, want deduped", len(got.FilesChanged))
	}
	if len(got.CommandsRun) != MaxTaskOutcomeCommands {
		t.Fatalf("commands = %d, want %d", len(got.CommandsRun), MaxTaskOutcomeCommands)
	}
	if len(got.CommandsRun[0].Command) > MaxTaskOutcomeCommandLength {
		t.Fatalf("command length = %d, want bounded", len(got.CommandsRun[0].Command))
	}
	if len(got.MissingContext) != 1 {
		t.Fatalf("missing context = %d, want deduped", len(got.MissingContext))
	}
	if len(got.MemoryRefsUsed) != MaxTaskOutcomeMemoryRefs {
		t.Fatalf("memory refs = %d, want %d", len(got.MemoryRefsUsed), MaxTaskOutcomeMemoryRefs)
	}
	if TaskOutcomeSourceURL(got) == "" || !strings.HasPrefix(TaskOutcomeSourceURL(got), "abra://task-outcome/") {
		t.Fatalf("source url = %q, want generated abra URL", TaskOutcomeSourceURL(got))
	}
	value := TaskOutcomeValue(got)
	if _, ok := value["commands_run"].([]map[string]any); !ok {
		t.Fatalf("commands_run value has unexpected type: %#v", value["commands_run"])
	}
}

func TestOutcomeObservationPatternPredicates(t *testing.T) {
	observation := store.ObservationResult{
		ID:              "observation-1",
		ObservationText: "Missing context: setup docs.",
		Value: map[string]any{
			"missing_context": []any{"setup docs"},
			"tests_result": map[string]any{
				"status":   "failed",
				"commands": []any{"go test ./internal/server"},
			},
			"commands_run": []any{
				map[string]any{
					"command":   "go test ./internal/server",
					"status":    "failed",
					"exit_code": float64(1),
				},
			},
		},
	}
	if !ObservationHasMissingContext(observation, " setup   docs ") {
		t.Fatal("expected missing context predicate to match normalized value")
	}
	if !ObservationHasFailedCommand(observation, "go test ./internal/server") {
		t.Fatal("expected failed command predicate to match test command")
	}
	if ObservationHasFailedCommand(observation, "go test ./internal/memory") {
		t.Fatal("unexpected failed command match for different command")
	}
}
