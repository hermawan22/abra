package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestObservePostsObservation(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/observations" {
			t.Fatalf("request = %s %s, want POST /observations", r.Method, r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{"observation": map[string]any{
			"id":               "obs-1",
			"scope":            got["scope"],
			"observation_type": got["observation_type"],
			"status":           got["status"],
		}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observe", "Agents should rerun release checks",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--type", "episode",
			"--source-url", "file://notes.md",
			"--confidence", "0.7",
		}); err != nil {
			t.Fatalf("observe error = %v", err)
		}
	})
	if got["scope"] != "repo:demo" || got["observation_text"] != "Agents should rerun release checks" || got["observation_type"] != "episode" {
		t.Fatalf("observe body = %#v", got)
	}
	if got["source_url"] != "file://notes.md" || got["created_by"] != "abra-cli" {
		t.Fatalf("observe lineage = %#v", got)
	}
	if !strings.Contains(output, "Observation captured: obs-1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("observe output = %s", output)
	}
}

func TestObserveRequiresText(t *testing.T) {
	err := run(context.Background(), []string{"observe", "--scope", "repo:demo"})
	if err == nil || !strings.Contains(err.Error(), "observe requires text") {
		t.Fatalf("err = %v, want observe requires text", err)
	}
}

func TestObserveConversationCapturesPreferenceTurnsAndProposes(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "conversation.md")
	mustWrite(t, transcript, strings.Join([]string{
		"User: saya lebih suka jawaban yang singkat dan langsung.",
		"Assistant: siap.",
		"User: ini cuma konteks biasa tanpa preferensi.",
	}, "\n"))

	observationRequests := []map[string]any{}
	proposalRequests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/observations":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode observation body: %v", err)
			}
			observationRequests = append(observationRequests, body)
			writeTestJSON(t, w, map[string]any{"observation": map[string]any{
				"id":               "obs-1",
				"scope":            body["scope"],
				"observation_type": body["observation_type"],
				"status":           body["status"],
				"source_url":       body["source_url"],
			}})
		case "/learning/proposals":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode proposal body: %v", err)
			}
			proposalRequests = append(proposalRequests, body)
			writeTestJSON(t, w, map[string]any{"learning_proposal": map[string]any{
				"id":            "lp-1",
				"scope":         body["scope"],
				"proposal_type": body["proposal_type"],
				"target_type":   body["target_type"],
				"target_id":     body["target_id"],
				"status":        "pending",
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observe", "conversation",
			"--file", transcript,
			"--scope", "repo:demo",
			"--propose",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("observe conversation error = %v", err)
		}
	})
	if len(observationRequests) != 1 || len(proposalRequests) != 1 {
		t.Fatalf("observations=%#v proposals=%#v", observationRequests, proposalRequests)
	}
	observation := observationRequests[0]
	if observation["observation_type"] != "preference" || observation["source_type"] != "conversation" {
		t.Fatalf("observation body = %#v", observation)
	}
	if !strings.Contains(stringValue(observation["observation_text"], ""), "lebih suka") {
		t.Fatalf("observation text = %#v", observation["observation_text"])
	}
	metadata, _ := observation["metadata"].(map[string]any)
	if metadata["adapter"] != "conversation" || metadata["role"] != "user" {
		t.Fatalf("metadata = %#v", metadata)
	}
	proposal := proposalRequests[0]
	if proposal["proposal_type"] != "claim" || proposal["target_type"] != "observation" || proposal["target_id"] != "obs-1" {
		t.Fatalf("proposal body = %#v", proposal)
	}
	if !strings.Contains(output, "Conversation observations captured: 1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("output = %s", output)
	}
}

func TestIsPreferenceTurnSkipsNegatedPreferenceMentions(t *testing.T) {
	cases := []string{
		"ini cuma konteks biasa tanpa preferensi.",
		"not a preference, just background context.",
		"no preference here, only a note.",
	}
	for _, content := range cases {
		if isPreferenceTurn(conversationTurn{Role: "user", Content: content}) {
			t.Fatalf("negated preference mention should be skipped: %q", content)
		}
	}
	if !isPreferenceTurn(conversationTurn{Role: "user", Content: "saya lebih suka jawaban singkat"}) {
		t.Fatal("positive preference was not detected")
	}
}

func TestListObservationsUsesScopedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/observations" {
			t.Fatalf("request = %s %s, want GET /observations", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		for key, want := range map[string]string{
			"scope":  "repo:demo",
			"query":  "release",
			"type":   "episode",
			"status": "raw",
			"since":  "2026-06-20T00:00:00Z",
			"limit":  "3",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q; full query %s", key, got, want, r.URL.RawQuery)
			}
		}
		writeTestJSON(t, w, map[string]any{"observations": []map[string]any{{
			"id":               "obs-1",
			"observed_at":      "2026-06-20 00:00:00+00",
			"observation_type": "episode",
			"status":           "raw",
			"observation_text": "release check note",
		}}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observations", "release",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--type", "episode",
			"--status", "raw",
			"--since", "2026-06-20T00:00:00Z",
			"--limit", "3",
		}); err != nil {
			t.Fatalf("observations error = %v", err)
		}
	})
	if !strings.Contains(output, "Observations: 1") || !strings.Contains(output, "obs-1") || !strings.Contains(output, "release check note") {
		t.Fatalf("observations output = %s", output)
	}
}

func TestProposeObservationPostsLearningProposal(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/learning/proposals" {
			t.Fatalf("request = %s %s, want POST /learning/proposals", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{"learning_proposal": map[string]any{
			"id":            "lp-1",
			"scope":         got["scope"],
			"proposal_type": got["proposal_type"],
			"target_type":   got["target_type"],
			"target_id":     got["target_id"],
			"status":        "pending",
		}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observations", "propose", "obs-1",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--claim", "Agents should rerun release checks before tagging.",
			"--source-url", "file://release-runbook.md",
			"--confidence", "0.7",
		}); err != nil {
			t.Fatalf("observations propose error = %v", err)
		}
	})
	if got["scope"] != "repo:demo" || got["proposal_type"] != "claim" || got["target_type"] != "observation" || got["target_id"] != "obs-1" {
		t.Fatalf("proposal body = %#v", got)
	}
	if got["source_url"] != "file://release-runbook.md" {
		t.Fatalf("source_url = %#v", got["source_url"])
	}
	payload, _ := got["payload"].(map[string]any)
	if payload["observation_id"] != "obs-1" || payload["claim"] != "Agents should rerun release checks before tagging." || payload["promotion_flow"] != "observation_to_claim" {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(output, "Observation proposed: lp-1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("output = %s", output)
	}
}
