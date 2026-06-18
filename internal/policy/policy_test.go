package policy

import "testing"

func TestEnginePlansBeforeTask(t *testing.T) {
	engine := NewEngine(Config{DefaultScope: "team:platform", DefaultLimit: 7})

	plan := engine.Plan(Event{
		Hook: HookBeforeTask,
		Task: "implement source ingestion",
	})

	if !plan.Required {
		t.Fatal("expected recall to be required")
	}
	if plan.Hook != HookBeforeTask {
		t.Fatalf("unexpected hook: %s", plan.Hook)
	}
	if len(plan.Queries) != 2 {
		t.Fatalf("expected 2 queries, got %d", len(plan.Queries))
	}
	if plan.Queries[0].Scope != "team:platform" || plan.Queries[0].Limit != 7 {
		t.Fatalf("defaults not applied: %+v", plan.Queries[0])
	}
}

func TestEnginePlansBeforeCodeWithFiles(t *testing.T) {
	engine := NewEngine(Config{DefaultScope: "company", MaxQueries: 3})

	plan := engine.Plan(Event{
		Hook:     HookBeforeCode,
		Task:     "add markdown ingestor",
		Scope:    "team:search",
		Files:    []string{"internal/ingest/markdown.go", "internal/ingest/match.go"},
		Language: "go",
	})

	if len(plan.Queries) != 3 {
		t.Fatalf("expected max 3 queries, got %d", len(plan.Queries))
	}
	for _, query := range plan.Queries {
		if query.Scope != "team:search" {
			t.Fatalf("scope override not used: %+v", query)
		}
	}
}

func TestEnginePlansAfterTask(t *testing.T) {
	engine := NewEngine(Config{IncludeUnverified: true})

	plan := engine.Plan(Event{
		Hook:         HookAfterTask,
		Task:         "wire recall policies",
		ChangedFiles: []string{"internal/policy/policy.go"},
	})

	if len(plan.Queries) != 3 {
		t.Fatalf("expected 3 queries, got %d", len(plan.Queries))
	}
	for _, query := range plan.Queries {
		if !query.IncludeUnverified {
			t.Fatalf("include_unverified not propagated: %+v", query)
		}
	}
}

func TestEngineUnknownHookReturnsOptionalEmptyPlan(t *testing.T) {
	engine := NewEngine(Config{})
	plan := engine.Plan(Event{Hook: Hook("during_task"), Task: "anything"})
	if plan.Required || len(plan.Queries) != 0 {
		t.Fatalf("unexpected plan for unknown hook: %+v", plan)
	}
}
