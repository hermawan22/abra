package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestComposeReportsNoSourceBackedContextWhenOnlyGateBlocksExist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPToolCall(t, r, "working_memory_compose")
		writeMCPToolResult(t, w, 1, map[string]any{
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "weak",
			},
			"agent_decision": map[string]any{
				"decision": "needs_context",
			},
			"stats": map[string]any{
				"facts":                0,
				"supporting_documents": 0,
				"summaries":            0,
				"graph_relations":      0,
				"context_blocks":       1,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"compose", "ship a change", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("compose error = %v", err)
		}
	})
	for _, want := range []string{"context: facts=0 documents=0 summaries=0 graph=0 blocks=1", "No source-backed context found for this scope.", "abra sync . --code --scope repo:demo"} {
		if !strings.Contains(output, want) {
			t.Fatalf("compose output missing %q:\n%s", want, output)
		}
	}
}

func TestComposeHumanOutputIncludesAgentHandoff(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody = decodeMCPToolCall(t, r, "working_memory_compose")
		writeMCPToolResult(t, w, 1, map[string]any{
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "partial",
				"retrieval_quality": map[string]any{
					"result_count":         4,
					"unique_sources":       1,
					"low_confidence":       false,
					"low_source_diversity": true,
				},
			},
			"agent_decision": map[string]any{
				"decision":             "caution",
				"required_actions":     []any{"corroborate_with_additional_source"},
				"allowed_next_actions": []any{"inspect_validation_plan"},
			},
			"memory_health": map[string]any{
				"status": "needs_review",
				"score":  82,
				"signals": []any{
					map[string]any{"code": "source_refresh_due", "severity": "warning", "count": 2, "action": "refresh_stale_sources"},
				},
			},
			"validation_plan": []any{
				map[string]any{"command": "go test ./...", "required": true, "reason": "Go code changed"},
			},
			"suggested_steps": []any{"Refresh stale source before broad edits."},
			"context_window": map[string]any{
				"prompt": "Task: ship a change\nUse citations.",
			},
			"stats": map[string]any{
				"facts":                1,
				"supporting_documents": 1,
				"summaries":            1,
				"graph_relations":      1,
				"context_blocks":       4,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"compose", "ship a change", "--scope", "repo:demo", "--prompt", "--persist-learning", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("compose error = %v", err)
		}
	})
	if requestBody["persist_learning"] != true {
		t.Fatalf("persist_learning = %#v, want true", requestBody["persist_learning"])
	}
	for _, want := range []string{
		"Compose: partial / caution",
		"health signals:",
		"source_refresh_due (warning, count=2) -> refresh_stale_sources",
		"retrieval: results=4 sources=1 low_confidence=false low_source_diversity=true",
		"required actions:",
		"corroborate_with_additional_source",
		"allowed next actions:",
		"inspect_validation_plan",
		"validation plan:",
		"required go test ./... - Go code changed",
		"suggested steps:",
		"Refresh stale source before broad edits.",
		"prompt-ready context:",
		"Task: ship a change",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("compose output missing %q:\n%s", want, output)
		}
	}
}

func TestComposeAgentOutputIncludesGovernedHandoff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPToolCall(t, r, "working_memory_compose")
		writeMCPToolResult(t, w, 1, map[string]any{
			"task":  "ship a change",
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "strong",
			},
			"agent_decision": map[string]any{
				"decision":             "proceed",
				"allowed_next_actions": []any{"inspect_relevant_files", "run_validation"},
			},
			"memory_health": map[string]any{
				"status": "healthy",
				"score":  100,
			},
			"citations": []any{
				map[string]any{"ref": "C1", "title": "runbook.md", "source_url": "file://repo/runbook.md"},
			},
			"risks": []any{"Keep retry idempotent."},
			"validation_plan": []any{
				map[string]any{"command": "go test ./...", "required": true, "reason": "Go code changed"},
			},
			"suggested_steps": []any{"Inspect callback retry path."},
			"stats": map[string]any{
				"facts":                2,
				"supporting_documents": 1,
				"summaries":            1,
				"graph_relations":      3,
				"context_blocks":       5,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"compose", "ship a change", "--scope", "repo:demo", "--agent-output", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("compose error = %v", err)
		}
	})
	for _, want := range []string{
		"Working memory handoff",
		"Task: ship a change",
		"Trust",
		"scope=repo:demo verdict=strong health=healthy score=100 decision=proceed conflicts=0 risks=1",
		"context: facts=2 documents=1 summaries=1 graph=3 blocks=5",
		"Evidence",
		"[C1] runbook.md - file://repo/runbook.md",
		"Risks",
		"Keep retry idempotent.",
		"Validation",
		"required go test ./... - Go code changed",
		"Allowed next",
		"inspect_relevant_files",
		"Suggested",
		"Inspect callback retry path.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("compose agent output missing %q:\n%s", want, output)
		}
	}
}

func TestMemoryStatusAndDoctor(t *testing.T) {
	signals := make([]any, 0, 9)
	for i := 1; i <= 9; i++ {
		signals = append(signals, map[string]any{
			"code":     fmt.Sprintf("signal_%02d", i),
			"severity": "warning",
			"count":    i,
			"action":   fmt.Sprintf("fix_signal_%02d", i),
		})
	}
	requests := 0
	payload := map[string]any{
		"status": "needs_review",
		"score":  72,
		"documents": map[string]any{
			"total":      3,
			"active":     2,
			"stale":      1,
			"deprecated": 1,
			"deleted":    1,
		},
		"claims": map[string]any{
			"total":                       5,
			"verified":                    4,
			"inferred":                    1,
			"unverified":                  1,
			"challenged":                  1,
			"deprecated":                  1,
			"expired":                     1,
			"stale":                       1,
			"with_evidence":               4,
			"trusted_from_code_documents": 1,
		},
		"graph": map[string]any{
			"entities":             9,
			"active_entities":      8,
			"relations":            10,
			"active_relations":     7,
			"challenged_relations": 1,
			"stale_relations":      1,
		},
		"sources": map[string]any{
			"total":   2,
			"active":  1,
			"due":     1,
			"overdue": 1,
			"error":   0,
		},
		"ingestion": map[string]any{
			"queued_jobs":        2,
			"running_jobs":       1,
			"retry_jobs":         1,
			"failed_jobs":        1,
			"stale_running_jobs": 1,
		},
		"conflicts": map[string]any{
			"total":     4,
			"open":      2,
			"reviewing": 1,
			"blocking":  1,
			"high":      1,
		},
		"learning": map[string]any{
			"total":                    6,
			"pending":                  3,
			"accepted":                 1,
			"applied":                  1,
			"rejected":                 1,
			"duplicate_pending_groups": 1,
		},
		"signals": signals,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/memory/health" {
			t.Fatalf("request = %s %s, want GET /memory/health", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("scope") != "repo:demo" {
			t.Fatalf("scope query = %q, want repo:demo", r.URL.Query().Get("scope"))
		}
		writeTestJSON(t, w, payload)
	}))
	defer server.Close()

	statusOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "status", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory status error = %v", err)
		}
	})
	for _, want := range []string{
		"Memory: needs_review score=72",
		"scope: repo:demo",
		"documents: total=3 active=2 stale=1 deprecated=1 deleted=1",
		"claims: total=5 verified=4 inferred=1 unverified=1 challenged=1 deprecated=1 expired=1 stale=1 with_evidence=4 trusted_from_code_documents=1",
		"graph: entities=9 active_entities=8 relations=10 active_relations=7 challenged_relations=1 stale_relations=1",
		"sources: total=2 active=1 due=1 overdue=1 error=0",
		"ingestion: queued_jobs=2 running_jobs=1 retry_jobs=1 failed_jobs=1 stale_running_jobs=1",
		"conflicts: total=4 open=2 reviewing=1 blocking=1 high=1",
		"learning: total=6 pending=3 accepted=1 applied=1 rejected=1 duplicate_pending_groups=1",
		"signal_08 (warning, count=8) -> fix_signal_08",
		"- +1 more",
		"abra brain doctor --scope repo:demo",
	} {
		if !strings.Contains(statusOutput, want) {
			t.Fatalf("memory status output missing %q:\n%s", want, statusOutput)
		}
	}
	if strings.Contains(statusOutput, "signal_09") {
		t.Fatalf("memory status should truncate signals:\n%s", statusOutput)
	}

	doctorOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "doctor", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory doctor error = %v", err)
		}
	})
	if !strings.Contains(doctorOutput, "signal_09 (warning, count=9) -> fix_signal_09") {
		t.Fatalf("memory doctor should include all signals:\n%s", doctorOutput)
	}
	if strings.Contains(doctorOutput, "- +1 more") {
		t.Fatalf("memory doctor should not truncate signals:\n%s", doctorOutput)
	}

	jsonOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "health", "--scope", "repo:demo", "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory health --json error = %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("decode memory health json: %v\n%s", err, jsonOutput)
	}
	if decoded["status"] != "needs_review" || intValue(decoded["score"]) != 72 {
		t.Fatalf("memory health json = %#v", decoded)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestGovernLearningCommandsCallMCP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			args := decodeMCPToolCall(t, r, "list_learning_proposals")
			if args["scope"] != "repo:demo" || args["status"] != "pending" || intValue(args["limit"]) != 3 {
				t.Fatalf("list_learning_proposals args = %#v", args)
			}
			writeMCPToolResult(t, w, 1, []any{
				map[string]any{"id": "proposal-1", "status": "pending", "proposal_type": "claim", "title": "Remember setup rule"},
			})
		case 2:
			args := decodeMCPToolCall(t, r, "decide_learning_proposal")
			if args["proposal_id"] != "proposal-1" || args["status"] != "accepted" || args["reviewed_by"] != "codex" || args["review_reason"] != "source-backed" {
				t.Fatalf("decide_learning_proposal args = %#v", args)
			}
			writeMCPToolResult(t, w, 2, map[string]any{
				"id":            "proposal-1",
				"scope":         "repo:demo",
				"status":        "accepted",
				"proposal_type": "claim",
				"title":         "Remember setup rule",
			})
		case 3:
			args := decodeMCPToolCall(t, r, "apply_learning_proposal")
			if args["proposal_id"] != "proposal-1" || args["applied_by"] != "codex" || args["approval_id"] != "approval-1" {
				t.Fatalf("apply_learning_proposal args = %#v", args)
			}
			writeMCPToolResult(t, w, 3, map[string]any{
				"learning_proposal": map[string]any{
					"id":     "proposal-1",
					"scope":  "repo:demo",
					"status": "applied",
					"title":  "Remember setup rule",
				},
			})
		case 4:
			args := decodeMCPToolCall(t, r, "list_learning_proposals")
			if args["scope"] != "repo:demo" || intValue(args["limit"]) != 50 {
				t.Fatalf("list_learning_proposals default args = %#v", args)
			}
			if _, ok := args["status"]; ok {
				t.Fatalf("list_learning_proposals should omit empty status: %#v", args)
			}
			writeMCPToolResult(t, w, 4, map[string]any{"learning_proposals": []any{}})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	listOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"govern", "learning", "list", "--scope", "repo:demo", "--status", "pending", "--limit", "3", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("govern learning list error = %v", err)
		}
	})
	for _, want := range []string{"Learning proposals: 1", "proposal-1", "pending", "Remember setup rule"} {
		if !strings.Contains(listOutput, want) {
			t.Fatalf("learning list output missing %q:\n%s", want, listOutput)
		}
	}

	acceptOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"govern", "learning", "accept", "proposal-1", "--agent", "codex", "--reason", "source-backed", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("govern learning accept error = %v", err)
		}
	})
	for _, want := range []string{"Learning proposal accepted: proposal-1", "status: accepted", "title: Remember setup rule"} {
		if !strings.Contains(acceptOutput, want) {
			t.Fatalf("learning accept output missing %q:\n%s", want, acceptOutput)
		}
	}

	applyOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"govern", "learning", "apply", "proposal-1", "--agent", "codex", "--approval-id", "approval-1", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("govern learning apply error = %v", err)
		}
	})
	for _, want := range []string{"Learning proposal applied: proposal-1", "status: applied", "title: Remember setup rule"} {
		if !strings.Contains(applyOutput, want) {
			t.Fatalf("learning apply output missing %q:\n%s", want, applyOutput)
		}
	}
	if err := run(context.Background(), []string{"govern", "learning", "list", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
		t.Fatalf("govern learning list without status error = %v", err)
	}
	if requests != 4 {
		t.Fatalf("requests = %d, want 4", requests)
	}
}

func TestBrainStatusIsThinOperatorOverview(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/memory/health" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Query().Get("scope") != "repo:demo" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeTestJSON(t, w, map[string]any{
			"scope":  "repo:demo",
			"status": "healthy",
			"score":  97,
			"sources": map[string]any{
				"total": 2,
				"due":   0,
			},
			"conflicts": map[string]any{
				"open": 0,
			},
			"learning": map[string]any{
				"pending": 1,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "status", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain status error = %v", err)
		}
	})
	for _, want := range []string{"Brain: healthy score=97", "scope: repo:demo", "MCP tools: discover_scopes, working_memory_compose, brain_think, brain_entity_dossier"} {
		if !strings.Contains(output, want) {
			t.Fatalf("brain status output missing %q:\n%s", want, output)
		}
	}
	jsonOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "status", "--scope", "repo:demo", "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain status json error = %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("decode brain status json: %v\n%s", err, jsonOutput)
	}
	surface, _ := decoded["agent_surface"].([]any)
	wantSurface := make([]any, 0, len(requiredMCPToolNames()))
	for _, name := range requiredMCPToolNames() {
		wantSurface = append(wantSurface, name)
	}
	if !reflect.DeepEqual(surface, wantSurface) {
		t.Fatalf("agent surface = %#v", surface)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestBrainReviewAndScorecardCommandsCallMCP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			args := decodeMCPToolCall(t, r, "brain_review")
			if args["scope"] != "repo:demo" || intValue(args["limit"]) != 7 {
				t.Fatalf("brain_review args = %#v", args)
			}
			writeMCPToolResult(t, w, 1, map[string]any{
				"scope":  "repo:demo",
				"status": "needs_review",
				"score":  72,
				"review_items": []any{
					map[string]any{"severity": "warning", "code": "weak_evidence_anchors", "count": 3, "message": "claims need anchors", "suggested_action": "run anchor backfill"},
				},
				"actions": []any{"abra brain anchor-backfill --scope repo:demo --dry-run"},
			})
		case 2:
			args := decodeMCPToolCall(t, r, "brain_scorecard")
			if args["scope"] != "repo:demo" || intValue(args["limit"]) != 5 {
				t.Fatalf("brain_scorecard args = %#v", args)
			}
			writeMCPToolResult(t, w, 2, map[string]any{
				"scope":  "repo:demo",
				"status": "healthy",
				"score":  91,
				"categories": []any{
					map[string]any{"name": "evidence", "score": 95, "status": "healthy", "detail": "source-backed claims"},
				},
				"actions": []any{"keep eval suite green"},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	reviewOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "review", "--scope", "repo:demo", "--limit", "7", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain review error = %v", err)
		}
	})
	for _, want := range []string{"Brain review: needs_review score=72", "weak_evidence_anchors", "run anchor backfill", "abra brain anchor-backfill"} {
		if !strings.Contains(reviewOutput, want) {
			t.Fatalf("brain review output missing %q:\n%s", want, reviewOutput)
		}
	}

	scorecardOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "scorecard", "--scope", "repo:demo", "--limit", "5", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain scorecard error = %v", err)
		}
	})
	for _, want := range []string{"Brain scorecard: healthy score=91", "evidence score=95 healthy", "keep eval suite green"} {
		if !strings.Contains(scorecardOutput, want) {
			t.Fatalf("brain scorecard output missing %q:\n%s", want, scorecardOutput)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestBrainMaintenanceCommandsUseSafeProposalMode(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			args := decodeMCPToolCall(t, r, "brain_anchor_backfill")
			if args["scope"] != "repo:demo" || intValue(args["limit"]) != 3 || boolValue(args["propose"], true) || !boolValue(args["dry_run"], false) {
				t.Fatalf("brain_anchor_backfill args = %#v", args)
			}
			if args["created_by"] != "cli" {
				t.Fatalf("created_by = %#v, want cli", args["created_by"])
			}
			writeMCPToolResult(t, w, 1, map[string]any{
				"scope":          "repo:demo",
				"status":         "needs_review",
				"score":          80,
				"dry_run":        true,
				"propose":        false,
				"checked":        4,
				"checked_claims": 4,
				"candidates": []any{
					map[string]any{"claim_id": "claim-1", "score": 88, "source_url": "file://README.md", "quote": "source-backed"},
				},
				"actions": []any{"rerun with --propose after review"},
			})
		case 2:
			args := decodeMCPToolCall(t, r, "brain_maintain")
			if args["scope"] != "repo:demo" || intValue(args["limit"]) != 9 || !boolValue(args["propose"], false) || boolValue(args["dry_run"], true) {
				t.Fatalf("brain_maintain args = %#v", args)
			}
			if args["agent"] != "codex" || args["created_by"] != "codex" {
				t.Fatalf("agent/created_by args = %#v", args)
			}
			writeMCPToolResult(t, w, 2, map[string]any{
				"scope":   "repo:demo",
				"status":  "needs_review",
				"score":   82,
				"dry_run": false,
				"propose": true,
				"review_items": []any{
					map[string]any{"severity": "info", "code": "stale_claims", "count": 2, "message": "claims need refresh", "action": "review proposals"},
				},
				"proposals": []any{map[string]any{"id": "proposal-1"}},
				"actions":   []any{"review learning proposals"},
			})
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	anchorOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "anchor-backfill", "--scope", "repo:demo", "--limit", "3", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain anchor-backfill error = %v", err)
		}
	})
	for _, want := range []string{"Brain anchor backfill: needs_review score=80", "dry_run: true propose: false", "claim-1 score=88", "rerun with --propose"} {
		if !strings.Contains(anchorOutput, want) {
			t.Fatalf("brain anchor-backfill output missing %q:\n%s", want, anchorOutput)
		}
	}

	maintainOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "maintain", "--scope", "repo:demo", "--limit", "9", "--propose", "--agent", "codex", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain maintain error = %v", err)
		}
	})
	for _, want := range []string{"Brain maintain: needs_review score=82", "dry_run: false propose: true", "proposals: 1", "stale_claims", "review learning proposals"} {
		if !strings.Contains(maintainOutput, want) {
			t.Fatalf("brain maintain output missing %q:\n%s", want, maintainOutput)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestBrainDossierCommandCallsMCP(t *testing.T) {
	var gotArgs map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotArgs = decodeMCPToolCall(t, r, "brain_entity_dossier")
		writeMCPToolResult(t, w, 1, map[string]any{
			"entity": "Abra",
			"scope":  "repo:demo",
			"dossier": map[string]any{
				"entity":      "Abra",
				"trust":       "source_backed",
				"next_action": "cite sources",
				"stats":       map[string]any{"claims": 2, "active_claims": 2, "relations": 1, "anchors": 1, "conflicts": 0},
				"active_claims": []any{
					map[string]any{"claim_text": "Abra is MCP-first.", "status": "verified", "citation_ref": "C1"},
				},
				"active_relations": []any{
					map[string]any{"from_entity": "Abra", "relation_type": "uses", "to_entity": "MCP", "citation_ref": "C2"},
				},
			},
			"agent_decision": map[string]any{"decision": "proceed"},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "dossier", "Abra", "--scope", "repo:demo", "--agent", "codex", "--mode", "deep", "--as-of", "2026-06-30", "--include-historical", "--include-unverified", "--limit", "4", "--token-budget", "900", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain dossier error = %v", err)
		}
	})
	if gotArgs["entity"] != "Abra" || gotArgs["scope"] != "repo:demo" || gotArgs["agent"] != "codex" || gotArgs["mode"] != "deep" || gotArgs["as_of"] != "2026-06-30T00:00:00Z" {
		t.Fatalf("brain_entity_dossier args = %#v", gotArgs)
	}
	if !boolValue(gotArgs["include_historical"], false) || !boolValue(gotArgs["include_unverified"], false) || intValue(gotArgs["limit"]) != 4 || intValue(gotArgs["token_budget"]) != 900 {
		t.Fatalf("brain_entity_dossier flags = %#v", gotArgs)
	}
	for _, want := range []string{"Brain dossier: Abra", "trust: source_backed", "claims=2 active=2 relations=1 anchors=1 conflicts=0", "Abra is MCP-first. [C1]", "Abra --uses--> MCP [C2]", "next: cite sources", "decision: proceed"} {
		if !strings.Contains(output, want) {
			t.Fatalf("brain dossier output missing %q:\n%s", want, output)
		}
	}
}

func TestBrainExplainFetchesPersistedTrace(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
			t.Fatalf("request = %s %s, want POST /mcp", r.Method, r.URL.Path)
		}
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode mcp request: %v", err)
		}
		params, _ := rpc["params"].(map[string]any)
		arguments, _ := params["arguments"].(map[string]any)
		if rpc["method"] != "tools/call" || params["name"] != "brain_explain" || arguments["trace_id"] != "trace-123" {
			t.Fatalf("unexpected mcp request: %#v", rpc)
		}
		trace := map[string]any{
			"trace_id":   "trace-123",
			"scope":      "repo:demo",
			"question":   "what should I know?",
			"answer":     "Retry callbacks stay idempotent [C1].",
			"created_at": "2026-06-22T01:02:03Z",
			"trace": map[string]any{
				"claims": []any{
					map[string]any{"id": "claim-1", "ref": "C1", "summary": "Retry callbacks stay idempotent."},
				},
			},
		}
		raw, _ := json.MarshalIndent(trace, "", "  ")
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc["id"],
			"result": map[string]any{
				"content":           []map[string]any{{"type": "text", "text": string(raw)}},
				"structuredContent": trace,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "explain", "trace-123", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain explain error = %v", err)
		}
	})
	for _, want := range []string{"Brain explain: trace-123", "scope: repo:demo", "Retry callbacks stay idempotent [C1].", "claims:", "- C1 Retry callbacks stay idempotent."} {
		if !strings.Contains(output, want) {
			t.Fatalf("brain explain output missing %q:\n%s", want, output)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestBrainExplainNotFoundJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
			t.Fatalf("request = %s %s, want POST /mcp", r.Method, r.URL.Path)
		}
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode mcp request: %v", err)
		}
		payload := map[string]any{
			"trace_id": "missing",
			"status":   "not_found",
			"message":  "no persisted why_trace found for this trace id",
		}
		raw, _ := json.MarshalIndent(payload, "", "  ")
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc["id"],
			"result": map[string]any{
				"content":           []map[string]any{{"type": "text", "text": string(raw)}},
				"structuredContent": payload,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"brain", "explain", "missing", "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("brain explain error = %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatalf("decode json output: %v\n%s", err, output)
	}
	if decoded["status"] != "not_found" || decoded["trace_id"] != "missing" {
		t.Fatalf("unexpected not found payload: %#v", decoded)
	}
}

func TestMemoryStatusAndDoctorSourceDiagnostics(t *testing.T) {
	payload := map[string]any{
		"status": "critical",
		"score":  41,
		"sources": map[string]any{
			"total":             3,
			"healthy":           1,
			"unhealthy":         2,
			"refresh_due":       1,
			"custom_new_metric": 7,
			"unhealthy_sources": []any{
				map[string]any{
					"source_config_id":  "source-wiki",
					"source_type":       "mcp",
					"status":            "error",
					"last_error":        "401 unauthorized",
					"last_error_at":     "2026-06-21T02:03:04Z",
					"remediation_hints": []any{"Rotate CONFLUENCE_MCP_TOKEN.", "Retry after credentials are valid."},
				},
			},
			"remediation_hints": []any{"Run `abra sources logs <source-config-id>` for failed connector details."},
		},
		"source_diagnostics": []any{
			map[string]any{
				"id":      "source-code",
				"status":  "overdue",
				"message": "last successful refresh is older than freshness policy",
				"action":  "run abra sources sync source-code --scope repo:demo",
			},
		},
		"signals": []any{
			map[string]any{"code": "source_configs_error", "severity": "critical", "count": 1, "action": "fix source configuration or credentials and retry ingestion"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/memory/health" {
			t.Fatalf("request = %s %s, want GET /memory/health", r.Method, r.URL.Path)
		}
		writeTestJSON(t, w, payload)
	}))
	defer server.Close()

	statusOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "status", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory status error = %v", err)
		}
	})
	for _, want := range []string{
		"sources: total=3 healthy=1 unhealthy=2 refresh_due=1 custom_new_metric=7",
		"Run `abra brain doctor --scope repo:demo` for source diagnostics.",
	} {
		if !strings.Contains(statusOutput, want) {
			t.Fatalf("memory status output missing %q:\n%s", want, statusOutput)
		}
	}
	for _, notWant := range []string{" due=", " overdue=", " error=", "source-wiki", "401 unauthorized"} {
		if strings.Contains(statusOutput, notWant) {
			t.Fatalf("memory status output should not include %q:\n%s", notWant, statusOutput)
		}
	}

	doctorOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "doctor", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory doctor error = %v", err)
		}
	})
	for _, want := range []string{
		"source diagnostics:",
		"source-code (overdue): last successful refresh is older than freshness policy",
		"action: run abra sources sync source-code --scope repo:demo",
		"inspect: abra connect status 'source-code'",
		"logs: abra connect logs 'source-code' --scope 'repo:demo'",
		"source-wiki (mcp, error): 401 unauthorized",
		"last_error_at: 2026-06-21T02:03:04Z",
		"remediation_hints:",
		"Rotate CONFLUENCE_MCP_TOKEN.",
		"source remediation:",
		"Run `abra sources logs <source-config-id>` for failed connector details.",
	} {
		if !strings.Contains(doctorOutput, want) {
			t.Fatalf("memory doctor output missing %q:\n%s", want, doctorOutput)
		}
	}
}

func TestComposeConcurrencyCheckWarnsWhenLocalRecallExceedsProviderCapacity(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"ABRA_AI_PROVIDER_CONCURRENCY=1",
		"ABRA_COMPOSE_RECALL_CONCURRENCY=3",
		"ABRA_COMPOSE_GRAPH_CONCURRENCY=4",
		"",
	}, "\n"))

	check := composeConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "recall=3") || !strings.Contains(detail, "local provider concurrency=1") {
		t.Fatalf("detail = %q", detail)
	}
	if hint := stringValue(check["hint"], ""); !strings.Contains(hint, "ABRA_COMPOSE_RECALL_CONCURRENCY=1") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestComposeConcurrencyCheckRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, "EMBEDDING_PROVIDER=compatible\nABRA_COMPOSE_RECALL_CONCURRENCY=33\n")

	check := composeConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "between 1 and 32") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestPrintThinkIncludesRecoveryWhenContextIsWeak(t *testing.T) {
	output := captureStdout(t, func() {
		printThink(map[string]any{
			"answer": "No source-backed answer.",
			"scope":  "repo:demo",
			"verification": map[string]any{
				"verdict": "weak",
			},
			"agent_decision": map[string]any{
				"decision": "needs_review",
			},
		}, "full")
	})
	for _, want := range []string{
		"next:",
		"abra agent verify . --scope 'repo:demo' --json",
		"abra doctor",
		"abra sync . --code --scope 'repo:demo'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("think recovery output missing %q:\n%s", want, output)
		}
	}
}

func TestPrintThinkIncludesGovernedSections(t *testing.T) {
	output := captureStdout(t, func() {
		printThink(map[string]any{
			"answer": "Abra's governed answer:\n- Retry callbacks should remain idempotent [C1] (verified, fresh).",
			"scope":  "repo:demo",
			"citations": []any{
				map[string]any{"ref": "C1", "title": "callback-runbook.md", "source_url": "file://repo/callback-runbook.md"},
			},
			"verification": map[string]any{
				"verdict": "strong",
				"retrieval_quality": map[string]any{
					"result_count":         3,
					"unique_sources":       2,
					"low_confidence":       false,
					"low_source_diversity": false,
				},
			},
			"memory_health": map[string]any{
				"status": "healthy",
				"score":  100,
			},
			"gaps": []any{
				map[string]any{
					"code":             "stale_claims",
					"severity":         "medium",
					"message":          "Stale or expired claims are present.",
					"suggested_action": "refresh sources",
				},
			},
			"conflicts": []any{
				map[string]any{
					"conflict_type": "contradicts",
					"severity":      "high",
					"reason":        "newer deployment doc disagrees with runbook",
				},
			},
			"agent_decision": map[string]any{
				"decision":             "proceed",
				"autonomous_allowed":   true,
				"allowed_next_actions": []any{"cite_evidence", "run_validation"},
			},
			"next_actions": []any{"cite_evidence", "run_validation"},
		}, "full")
	})
	for _, want := range []string{
		"Answer",
		"- Retry callbacks should remain idempotent [C1] (verified, fresh).",
		"Evidence",
		"[C1] callback-runbook.md - file://repo/callback-runbook.md",
		"Trust",
		"scope=repo:demo verdict=strong health=healthy score=100 conflicts=1 gaps=1",
		"retrieval: results=3 sources=2 low_confidence=false low_source_diversity=false",
		"Agent handoff",
		"decision=proceed autonomous_allowed=true",
		"allowed next:",
		"cite_evidence",
		"Gaps",
		"stale_claims (medium): Stale or expired claims are present. -> refresh sources",
		"Conflicts",
		"contradicts (high): newer deployment doc disagrees with runbook",
		"Next",
		"run_validation",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("think output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Abra's governed answer:") {
		t.Fatalf("think output should trim internal answer preamble:\n%s", output)
	}
	if strings.Contains(output, "abra sync") {
		t.Fatalf("strong think output should not include recovery guidance:\n%s", output)
	}
}

func TestPrintThinkRecoveryUsesPlaceholderWhenScopeMissing(t *testing.T) {
	output := captureStdout(t, func() {
		printThink(map[string]any{
			"answer": "No source-backed answer.",
			"verification": map[string]any{
				"verdict": "weak",
			},
			"agent_decision": map[string]any{
				"decision": "needs_review",
			},
		}, "full")
	})
	if !strings.Contains(output, "<scope-from-abra-scope>") {
		t.Fatalf("think recovery output missing placeholder scope:\n%s", output)
	}
}

func TestPrintThinkLimitsCitationList(t *testing.T) {
	citations := []any{}
	for i := 1; i <= 7; i++ {
		citations = append(citations, map[string]any{
			"ref":        fmt.Sprintf("C%d", i),
			"title":      fmt.Sprintf("source-%d.md", i),
			"source_url": fmt.Sprintf("file://repo/source-%d.md", i),
		})
	}
	output := captureStdout(t, func() {
		printThink(map[string]any{
			"answer":    "Source-backed answer.",
			"scope":     "repo:demo",
			"citations": citations,
			"verification": map[string]any{
				"verdict": "strong",
			},
			"agent_decision": map[string]any{
				"decision": "proceed",
			},
		}, "full")
	})
	for _, want := range []string{"[C1] source-1.md", "[C5] source-5.md", "+2 more"} {
		if !strings.Contains(output, want) {
			t.Fatalf("think citation limit output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "[C6] source-6.md") {
		t.Fatalf("think citation output did not stop at limit:\n%s", output)
	}
}

func TestAskBriefHumanOutput(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody = decodeMCPToolCall(t, r, "brain_think")
		writeMCPToolResult(t, w, 1, map[string]any{
			"question": "what should I know?",
			"answer":   "Abra's governed answer:\nUse source-backed memory.\nMore detail.",
			"scope":    "repo:demo",
			"citations": []any{
				map[string]any{"ref": "C1", "source_url": "file://repo/runbook.md"},
			},
			"verification": map[string]any{"verdict": "strong"},
			"memory_health": map[string]any{
				"status": "healthy",
			},
			"agent_decision": map[string]any{
				"decision": "proceed",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"ask", "what should I know?", "--scope", "repo:demo", "--agent", "codex", "--entity", "Abra", "--as-of", "2026-06-30", "--include-historical", "--limit", "3", "--max-queries", "2", "--token-budget", "500", "--include-unverified", "--brief", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("ask error = %v", err)
		}
	})
	if requestBody["question"] != "what should I know?" || requestBody["scope"] != "repo:demo" || requestBody["agent"] != "codex" {
		t.Fatalf("unexpected ask request body: %#v", requestBody)
	}
	if requestBody["entity"] != "Abra" || requestBody["as_of"] != "2026-06-30T00:00:00Z" || !boolValue(requestBody["include_historical"], false) {
		t.Fatalf("ask request did not preserve temporal/entity flags: %#v", requestBody)
	}
	if intValue(requestBody["limit"]) != 3 || intValue(requestBody["max_queries"]) != 2 || intValue(requestBody["token_budget"]) != 500 || !boolValue(requestBody["include_unverified"], false) {
		t.Fatalf("ask request did not preserve flags: %#v", requestBody)
	}
	for _, want := range []string{
		"Answer",
		"Use source-backed memory.",
		"Trust: scope=repo:demo verdict=strong health=healthy decision=proceed",
		"Evidence: 1 source(s)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("ask brief output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "More detail.") || strings.Contains(output, "Evidence\n") {
		t.Fatalf("ask brief output should stay compact:\n%s", output)
	}
}

func TestAskOmitsUnsetTokenBudget(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody = decodeMCPToolCall(t, r, "brain_think")
		writeMCPToolResult(t, w, 1, map[string]any{
			"question":       "what changed?",
			"answer":         "No source-backed context yet.",
			"scope":          "repo:demo",
			"verification":   map[string]any{"verdict": "weak"},
			"agent_decision": map[string]any{"decision": "pause"},
		})
	}))
	defer server.Close()

	if err := run(context.Background(), []string{"ask", "what changed?", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
		t.Fatalf("ask error = %v", err)
	}
	if _, ok := requestBody["token_budget"]; ok {
		t.Fatalf("ask should omit unset token_budget: %#v", requestBody)
	}
}

func TestAskAndContextPassRetrievalMode(t *testing.T) {
	requests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		params, _ := rpc["params"].(map[string]any)
		requestBody, _ := params["arguments"].(map[string]any)
		requests = append(requests, requestBody)
		switch params["name"] {
		case "brain_think":
			writeMCPToolResult(t, w, rpc["id"], map[string]any{
				"answer":         "Use source-backed memory.",
				"scope":          "repo:demo",
				"verification":   map[string]any{"verdict": "strong"},
				"agent_decision": map[string]any{"decision": "proceed"},
			})
		case "working_memory_compose":
			writeMCPToolResult(t, w, rpc["id"], map[string]any{
				"task":           "ship a change",
				"scope":          "repo:demo",
				"verification":   map[string]any{"verdict": "strong"},
				"agent_decision": map[string]any{"decision": "proceed"},
				"stats":          map[string]any{"facts": 0, "supporting_documents": 0, "summaries": 0, "graph_relations": 0},
			})
		default:
			t.Fatalf("unexpected mcp tool %v", params["name"])
		}
	}))
	defer server.Close()

	if err := run(context.Background(), []string{"ask", "what changed?", "--scope", "repo:demo", "--mode", "deep", "--base-url", server.URL, "--token", "test-token"}); err != nil {
		t.Fatalf("ask error = %v", err)
	}
	if err := run(context.Background(), []string{"context", "ship a change", "--scope", "repo:demo", "--mode", "fast", "--entity", "Abra", "--as-of", "2026-06-30", "--include-historical", "--files", "cmd/abra/main.go,cmd/abra/brain.go", "--changed-file", "cmd/abra/memory_cli.go", "--language", "go", "--base-url", server.URL, "--token", "test-token"}); err != nil {
		t.Fatalf("context error = %v", err)
	}
	if len(requests) != 2 || requests[0]["mode"] != "deep" || requests[1]["mode"] != "fast" {
		t.Fatalf("retrieval modes = %#v", requests)
	}
	if requests[1]["entity"] != "Abra" || requests[1]["as_of"] != "2026-06-30T00:00:00Z" || !boolValue(requests[1]["include_historical"], false) || requests[1]["language"] != "go" {
		t.Fatalf("context request did not preserve entity/temporal/language flags: %#v", requests[1])
	}
	if _, ok := requests[1]["token_budget"]; ok {
		t.Fatalf("context should omit unset token_budget: %#v", requests[1])
	}
	if !reflect.DeepEqual(requests[1]["files"], []any{"cmd/abra/main.go", "cmd/abra/brain.go"}) || !reflect.DeepEqual(requests[1]["changed_files"], []any{"cmd/abra/memory_cli.go"}) {
		t.Fatalf("context request did not preserve file flags: %#v", requests[1])
	}
	if err := run(context.Background(), []string{"ask", "what changed?", "--scope", "repo:demo", "--mode", "wide", "--base-url", server.URL, "--token", "test-token"}); err == nil || !strings.Contains(err.Error(), "invalid --mode") {
		t.Fatalf("invalid mode error = %v", err)
	}
}

func TestThinkJSONBypassesHumanOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPToolCall(t, r, "brain_think")
		writeMCPToolResult(t, w, 1, map[string]any{
			"answer": "Raw governed answer.",
			"scope":  "repo:demo",
			"citations": []any{
				map[string]any{"ref": "C1", "source_url": "file://repo/runbook.md"},
			},
			"verification": map[string]any{"verdict": "strong"},
			"agent_decision": map[string]any{
				"decision": "proceed",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"think", "what should I know?", "--scope", "repo:demo", "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("think error = %v", err)
		}
	})
	if !strings.Contains(output, `"answer": "Raw governed answer."`) || !strings.Contains(output, `"scope": "repo:demo"`) {
		t.Fatalf("think JSON output missing raw fields:\n%s", output)
	}
	for _, unwanted := range []string{"Answer\n", "Evidence\n", "Trust\n", "Agent gate\n"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("think JSON output included human label %q:\n%s", unwanted, output)
		}
	}
}

func TestThinkAgentOutputHumanMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeMCPToolCall(t, r, "brain_think")
		writeMCPToolResult(t, w, 1, map[string]any{
			"question": "what should I know?",
			"answer":   "Abra's governed answer:\nUse source-backed memory.",
			"scope":    "repo:demo",
			"citations": []any{
				map[string]any{"ref": "C1", "title": "runbook.md", "source_url": "file://repo/runbook.md"},
			},
			"verification": map[string]any{"verdict": "strong"},
			"memory_health": map[string]any{
				"status": "healthy",
				"score":  100,
			},
			"agent_decision": map[string]any{
				"decision":             "proceed",
				"autonomous_allowed":   true,
				"allowed_next_actions": []any{"cite_evidence", "run_validation"},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"think", "what should I know?", "--scope", "repo:demo", "--agent-output", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("think error = %v", err)
		}
	})
	for _, want := range []string{
		"Agent handoff",
		"question: what should I know?",
		"Answer",
		"Use source-backed memory.",
		"Evidence",
		"[C1] runbook.md - file://repo/runbook.md",
		"Trust",
		"scope=repo:demo verdict=strong health=healthy score=100 conflicts=0 gaps=0",
		"decision=proceed autonomous_allowed=true",
		"allowed next:",
		"run_validation",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("think agent output missing %q:\n%s", want, output)
		}
	}
}

func TestThinkSynthesizePassesFlagAndPrintsStatus(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody = decodeMCPToolCall(t, r, "brain_think")
		writeMCPToolResult(t, w, 1, map[string]any{
			"question":             "what should I know?",
			"answer":               "Synthesized answer [C1].",
			"deterministic_answer": "Deterministic answer [C1].",
			"scope":                "repo:demo",
			"synthesis": map[string]any{
				"enabled":  true,
				"status":   "ok",
				"provider": "fake",
				"model":    "synth-model",
			},
			"citations": []any{
				map[string]any{"ref": "C1", "title": "runbook.md", "source_url": "file://repo/runbook.md"},
			},
			"verification":  map[string]any{"verdict": "strong"},
			"memory_health": map[string]any{"status": "healthy"},
			"agent_decision": map[string]any{
				"decision": "proceed",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"think", "what should I know?", "--scope", "repo:demo", "--synthesize", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("think error = %v", err)
		}
	})
	if !boolValue(requestBody["synthesize"], false) {
		t.Fatalf("synthesize flag not sent: %#v", requestBody)
	}
	for _, want := range []string{"Synthesized answer [C1].", "Synthesis", "status=ok provider=fake model=synth-model"} {
		if !strings.Contains(output, want) {
			t.Fatalf("think synth output missing %q:\n%s", want, output)
		}
	}
}

func TestEvalBrainRunsSuiteAndFailsOnBadCase(t *testing.T) {
	var thinkCalls, historyCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/mcp" {
			var rpc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				t.Fatalf("decode eval mcp request: %v", err)
			}
			params, _ := rpc["params"].(map[string]any)
			if rpc["method"] != "tools/call" {
				t.Fatalf("unexpected eval mcp request: %#v", rpc)
			}
			body, _ := params["arguments"].(map[string]any)
			switch params["name"] {
			case "brain_eval_record":
				if body["scope"] != "repo:demo" || intValue(body["total"]) != 2 || intValue(body["passed"]) != 1 || body["success"] != false {
					t.Fatalf("unexpected eval history body: %#v", body)
				}
				historyCalls++
				writeMCPToolResult(t, w, rpc["id"], map[string]any{"id": "eval-run-1"})
			case "brain_think":
				thinkCalls++
				writeMCPToolResult(t, w, rpc["id"], map[string]any{
					"question": "what should I know?",
					"answer":   "Retry callbacks stay idempotent [C1].",
					"scope":    "repo:demo",
					"citations": []any{
						map[string]any{"ref": "C1", "source_url": "file://repo/runbook.md"},
					},
					"evidence_anchors": []any{
						map[string]any{"kind": "claim", "ref": "C1", "quote": "Retry callbacks stay idempotent."},
					},
					"verification": map[string]any{"verdict": "strong"},
					"agent_decision": map[string]any{
						"decision": "proceed",
					},
				})
			default:
				t.Fatalf("unexpected eval mcp tool: %#v", rpc)
			}
			return
		}
		t.Fatalf("request = %s %s, want POST /mcp", r.Method, r.URL.Path)
	}))
	defer server.Close()

	suitePath := filepath.Join(t.TempDir(), "brain-eval.json")
	mustWrite(t, suitePath, `{"cases":[{"name":"pass","question":"what should I know?","scope":"repo:demo","min_verdict":"strong","require_decision":"proceed","require_citation_refs":["C1"],"require_answer_text":["idempotent"],"require_anchored_claim":true},{"name":"fail","question":"what should I know?","scope":"repo:demo","require_answer_text":["missing phrase"]}]}`)
	output := captureStdout(t, func() {
		err := run(context.Background(), []string{"eval", "brain", "--file", suitePath, "--base-url", server.URL, "--token", "test-token"})
		if err == nil || !strings.Contains(err.Error(), "brain eval failed") {
			t.Fatalf("eval error = %v, want brain eval failed", err)
		}
	})
	if thinkCalls != 2 {
		t.Fatalf("think calls = %d, want 2", thinkCalls)
	}
	if historyCalls != 1 {
		t.Fatalf("history calls = %d, want 1", historyCalls)
	}
	for _, want := range []string{"pass  pass  score=1.00", "fail  fail", "answer_contains", "Brain eval: 1/2 passed", "history: eval-run-1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("eval output missing %q:\n%s", want, output)
		}
	}
}

func TestComposeCommandArgsUsesDevOverrideWhenEnabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "docker-compose.dev.yml"), "services: {}\n")

	got := composeCommandArgs(root, "/tmp/abra.env", true, "up", "-d", "api")
	want := []string{
		"compose",
		"--project-name",
		"abra",
		"--env-file",
		"/tmp/abra.env",
		"-f",
		"docker-compose.yml",
		"-f",
		"docker-compose.dev.yml",
		"up",
		"-d",
		"api",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeCommandArgs = %#v, want %#v", got, want)
	}
}

func TestComposeCommandArgsUsesBaseComposeWhenDevOverrideDisabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "docker-compose.dev.yml"), "services: {}\n")

	got := composeCommandArgs(root, "/tmp/abra.env", false, "down")
	want := []string{"compose", "--project-name", "abra", "--env-file", "/tmp/abra.env", "down"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeCommandArgs = %#v, want %#v", got, want)
	}
}

func TestComposeUpStepsBuildOnlyForSourceCheckout(t *testing.T) {
	checkout := t.TempDir()
	mustWrite(t, filepath.Join(checkout, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(checkout, "docker-compose.dev.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(checkout, "go.mod"), "module github.com/hermawan22/abra\n")
	mustWrite(t, filepath.Join(checkout, "cmd", "abra", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(checkout, "migrations", "001_init.sql"), "-- init\n")

	devSteps := composeUpSteps(checkout, "/tmp/abra.env")
	if len(devSteps) == 0 || !containsString(devSteps[0], "build") || !containsString(devSteps[0], "docker-compose.dev.yml") {
		t.Fatalf("dev compose steps should build with dev override: %#v", devSteps)
	}

	runtimeDir := t.TempDir()
	mustWrite(t, filepath.Join(runtimeDir, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(runtimeDir, "docker-compose.dev.yml"), "services: {}\n")
	runtimeSteps := composeUpSteps(runtimeDir, "/tmp/abra.env")
	if len(runtimeSteps) == 0 || !containsString(runtimeSteps[0], "pull") || containsString(runtimeSteps[0], "build") {
		t.Fatalf("runtime compose steps should pull, not build: %#v", runtimeSteps)
	}
	for _, step := range runtimeSteps {
		if containsString(step, "docker-compose.dev.yml") {
			t.Fatalf("runtime compose step used dev override: %#v", runtimeSteps)
		}
	}
}
