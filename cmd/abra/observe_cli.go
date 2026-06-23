package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func observe(ctx context.Context, args cliArgs) error {
	if len(args.Rest) > 0 {
		mode := strings.ToLower(strings.TrimSpace(args.Rest[0]))
		if mode == "conversation" || mode == "transcript" {
			args.Rest = args.Rest[1:]
			return observeConversation(ctx, args)
		}
	}
	text := strings.TrimSpace(strings.Join(args.Rest, " "))
	if text == "" {
		text = strings.TrimSpace(flag(args, "text", ""))
	}
	if text == "" {
		return errors.New("observe requires text, for example: abra observe \"Agents should rerun release checks before tagging\"")
	}
	scope := scopeOrDefault(args, ".")
	metadata := map[string]any{"channel": "cli"}
	if title := strings.TrimSpace(flag(args, "title", "")); title != "" {
		metadata["title"] = title
	}
	body := map[string]any{
		"scope":            scope,
		"observation_text": text,
		"observation_type": flag(args, "type", flag(args, "observation-type", "episode")),
		"status":           flag(args, "status", "raw"),
		"authority":        flag(args, "authority", "manual-unverified"),
		"authority_score":  floatFlag(args, "authority-score", 0.35),
		"confidence":       floatFlag(args, "confidence", 0.35),
		"source_url":       flag(args, "source-url", ""),
		"source_type":      flag(args, "source-type", ""),
		"source_id":        flag(args, "source-id", ""),
		"observed_at":      flag(args, "observed-at", ""),
		"created_by":       flag(args, "created-by", "abra-cli"),
		"approval_id":      flag(args, "approval-id", ""),
		"metadata":         metadata,
	}
	result, err := postJSON(ctx, args, "/observations", body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") && !boolFlag(args, "propose") {
		return printJSON(result)
	}
	observation, _ := result["observation"].(map[string]any)
	if boolFlag(args, "propose") {
		observationID := stringValue(observation["id"], "")
		proposed, err := proposeObservation(ctx, args, observationID, text)
		if err != nil {
			return err
		}
		if boolFlag(args, "json") {
			return printJSON(proposed)
		}
		proposal, _ := proposed["learning_proposal"].(map[string]any)
		fmt.Println("Observation proposed: " + stringValue(proposal["id"], "unknown"))
		fmt.Println("scope: " + stringValue(observation["scope"], scope))
		fmt.Println("type: " + stringValue(proposal["proposal_type"], "claim"))
		fmt.Println("status: " + stringValue(proposal["status"], "pending"))
		fmt.Println("trusted: no, accepted proposal still requires explicit apply")
		return nil
	}
	fmt.Println("Observation captured: " + stringValue(observation["id"], "unknown"))
	fmt.Println("scope: " + stringValue(observation["scope"], scope))
	fmt.Println("type: " + stringValue(observation["observation_type"], stringValue(body["observation_type"], "episode")))
	fmt.Println("status: " + stringValue(observation["status"], stringValue(body["status"], "raw")))
	fmt.Println("trusted: no, promote through review before treating as a claim")
	return nil
}

func observeConversation(ctx context.Context, args cliArgs) error {
	raw, sourceURL, err := conversationInput(args)
	if err != nil {
		return err
	}
	turns := parseConversationTurns(raw)
	if len(turns) == 0 {
		return errors.New("observe conversation found no transcript turns")
	}
	scope := scopeOrDefault(args, ".")
	conversationID := firstNonEmpty(flag(args, "conversation-id", ""), stableConversationID(sourceURL, raw))
	observations := []any{}
	proposals := []any{}
	for _, turn := range turns {
		if !boolFlag(args, "all-turns") && !isPreferenceTurn(turn) {
			continue
		}
		observationType := flag(args, "type", flag(args, "observation-type", "preference"))
		if boolFlag(args, "all-turns") && flag(args, "type", flag(args, "observation-type", "")) == "" {
			observationType = "conversation"
		}
		body := map[string]any{
			"scope":            scope,
			"observation_text": turn.Content,
			"observation_type": observationType,
			"status":           flag(args, "status", "raw"),
			"authority":        flag(args, "authority", "conversation-unverified"),
			"authority_score":  floatFlag(args, "authority-score", 0.3),
			"confidence":       floatFlag(args, "confidence", 0.4),
			"source_url":       sourceURL,
			"source_type":      flag(args, "source-type", "conversation"),
			"source_id":        firstNonEmpty(flag(args, "source-id", ""), conversationID),
			"created_by":       flag(args, "created-by", "abra-cli"),
			"approval_id":      flag(args, "approval-id", ""),
			"value": map[string]any{
				"role":            turn.Role,
				"content":         turn.Content,
				"turn_index":      turn.Index,
				"conversation_id": conversationID,
			},
			"metadata": map[string]any{
				"channel":         "cli",
				"adapter":         "conversation",
				"conversation_id": conversationID,
				"turn_index":      turn.Index,
				"role":            turn.Role,
			},
		}
		result, err := postJSON(ctx, args, "/observations", body)
		if err != nil {
			return err
		}
		observations = append(observations, result["observation"])
		if boolFlag(args, "propose") {
			observation, _ := result["observation"].(map[string]any)
			observationID := stringValue(observation["id"], "")
			proposed, err := proposeObservation(ctx, args, observationID, turn.Content)
			if err != nil {
				return err
			}
			proposals = append(proposals, proposed["learning_proposal"])
		}
	}
	if len(observations) == 0 {
		return errors.New("observe conversation found no preference-like user turns; use --all-turns to capture every transcript turn")
	}
	if boolFlag(args, "json") {
		payload := map[string]any{"observations": observations, "conversation_id": conversationID}
		if boolFlag(args, "propose") {
			payload["learning_proposals"] = proposals
		}
		return printJSON(payload)
	}
	fmt.Printf("Conversation observations captured: %d\n", len(observations))
	if boolFlag(args, "propose") {
		fmt.Printf("Conversation observations proposed: %d\n", len(proposals))
		fmt.Println("trusted: no, accepted proposals still require explicit apply")
	} else {
		fmt.Println("trusted: no, promote through review before treating as claims")
	}
	fmt.Println("conversation: " + conversationID)
	fmt.Println("scope: " + scope)
	return nil
}

func conversationInput(args cliArgs) (string, string, error) {
	sourceURL := strings.TrimSpace(flag(args, "source-url", ""))
	file := firstNonEmpty(flag(args, "file", ""), flag(args, "transcript-file", ""))
	if file != "" {
		bytes, err := os.ReadFile(file)
		if err != nil {
			return "", "", err
		}
		if sourceURL == "" {
			abs, err := filepath.Abs(file)
			if err != nil {
				return "", "", err
			}
			sourceURL = localFileURL(filepath.Dir(abs), filepath.Base(abs))
		}
		return string(bytes), sourceURL, nil
	}
	raw := strings.TrimSpace(flag(args, "text", ""))
	if raw == "" {
		raw = strings.TrimSpace(strings.Join(args.Rest, " "))
	}
	if raw == "" {
		return "", "", errors.New("observe conversation requires --file, --text, or transcript text")
	}
	if sourceURL == "" {
		sourceURL = "conversation://" + timestamp()
	}
	return raw, sourceURL, nil
}

func parseConversationTurns(raw string) []conversationTurn {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if turns := parseJSONConversationTurns(raw); len(turns) > 0 {
		return turns
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	turns := []conversationTurn{}
	current := conversationTurn{Role: "unknown"}
	for _, line := range lines {
		role, content, ok := splitConversationLine(line)
		if ok {
			if strings.TrimSpace(current.Content) != "" {
				current.Index = len(turns)
				current.Content = strings.TrimSpace(current.Content)
				turns = append(turns, current)
			}
			current = conversationTurn{Role: role, Content: strings.TrimSpace(content)}
			continue
		}
		if strings.TrimSpace(line) == "" && strings.TrimSpace(current.Content) == "" {
			continue
		}
		if current.Content != "" {
			current.Content += "\n"
		}
		current.Content += line
	}
	if strings.TrimSpace(current.Content) != "" {
		current.Index = len(turns)
		current.Content = strings.TrimSpace(current.Content)
		turns = append(turns, current)
	}
	return turns
}

func parseJSONConversationTurns(raw string) []conversationTurn {
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	switch value := payload.(type) {
	case []any:
		return turnsFromJSONArray(value)
	case map[string]any:
		for _, key := range []string{"messages", "turns", "conversation"} {
			if items, ok := value[key].([]any); ok {
				return turnsFromJSONArray(items)
			}
		}
	}
	return nil
}

func turnsFromJSONArray(items []any) []conversationTurn {
	turns := []conversationTurn{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := firstNonEmpty(stringValue(item["role"], ""), stringValue(item["speaker"], ""), stringValue(item["author"], ""), "unknown")
		content := firstNonEmpty(stringValue(item["content"], ""), stringValue(item["text"], ""), stringValue(item["message"], ""))
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		turns = append(turns, conversationTurn{Role: normalizeConversationRole(role), Content: content, Index: len(turns)})
	}
	return turns
}

func splitConversationLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", "", false
	}
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return "", "", false
	}
	role := normalizeConversationRole(trimmed[:idx])
	if !knownConversationRole(role) {
		return "", "", false
	}
	return role, trimmed[idx+1:], true
}

func normalizeConversationRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "human", "customer", "operator":
		return "user"
	case "ai", "assistant", "agent", "codex":
		return "assistant"
	default:
		return role
	}
}

func knownConversationRole(role string) bool {
	switch role {
	case "user", "assistant", "system", "developer":
		return true
	default:
		return false
	}
}

func isPreferenceTurn(turn conversationTurn) bool {
	if normalizeConversationRole(turn.Role) != "user" {
		return false
	}
	text := strings.ToLower(turn.Content)
	if hasPreferenceNegation(text) {
		return false
	}
	for _, marker := range []string{
		"i prefer", "i like", "i want", "i don't want", "i do not want",
		"please", "my preference", "my taste", "style",
		"saya prefer", "saya suka", "aku suka", "lebih suka", "preferensi",
		"jangan", "tolong", "mending", "harusnya", "sebaiknya",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func hasPreferenceNegation(text string) bool {
	for _, marker := range []string{
		"no preference",
		"not a preference",
		"not preference",
		"without preference",
		"tanpa preferensi",
		"bukan preferensi",
		"tidak ada preferensi",
		"nggak ada preferensi",
		"gak ada preferensi",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func stableConversationID(sourceURL, raw string) string {
	seed := firstNonEmpty(sourceURL, raw)
	if len(seed) > 512 {
		seed = seed[:512]
	}
	sum := sha256.Sum256([]byte(seed))
	return "conversation-" + fmt.Sprintf("%x", sum[:8])
}

func listObservations(ctx context.Context, args cliArgs) error {
	if len(args.Rest) > 0 && args.Rest[0] == "propose" {
		observationID := ""
		if len(args.Rest) > 1 {
			observationID = args.Rest[1]
		}
		if observationID == "" {
			observationID = flag(args, "observation-id", "")
		}
		if observationID == "" {
			return errors.New("observations propose requires an observation id")
		}
		result, err := proposeObservation(ctx, args, observationID, flag(args, "claim", ""))
		if err != nil {
			return err
		}
		if boolFlag(args, "json") {
			return printJSON(result)
		}
		proposal, _ := result["learning_proposal"].(map[string]any)
		observation, _ := result["observation"].(map[string]any)
		fmt.Println("Observation proposed: " + stringValue(proposal["id"], "unknown"))
		fmt.Println("observation: " + stringValue(observation["id"], observationID))
		fmt.Println("scope: " + stringValue(observation["scope"], scopeOrDefault(args, ".")))
		fmt.Println("type: " + stringValue(proposal["proposal_type"], "claim"))
		fmt.Println("status: " + stringValue(proposal["status"], "pending"))
		fmt.Println("trusted: no, accepted proposal still requires explicit apply")
		return nil
	}
	scope := scopeOrDefault(args, ".")
	params := url.Values{}
	params.Set("scope", scope)
	if query := strings.TrimSpace(strings.Join(args.Rest, " ")); query != "" {
		params.Set("query", query)
	}
	if query := strings.TrimSpace(flag(args, "query", "")); query != "" {
		params.Set("query", query)
	}
	for _, pair := range []struct {
		flag  string
		param string
	}{
		{"type", "type"},
		{"observation-type", "observation_type"},
		{"status", "status"},
		{"since", "since"},
		{"until", "until"},
	} {
		if value := strings.TrimSpace(flag(args, pair.flag, "")); value != "" {
			params.Set(pair.param, value)
		}
	}
	params.Set("limit", strconv.Itoa(intFlag(args, "limit", 20)))
	result, _, err := getJSON(ctx, args, "/observations?"+params.Encode())
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	observations, _ := result["observations"].([]any)
	fmt.Printf("Observations: %d\n", len(observations))
	for _, raw := range observations {
		observation, _ := raw.(map[string]any)
		text := stringValue(observation["observation_text"], "")
		if len(text) > 96 {
			text = text[:93] + "..."
		}
		fmt.Printf("- %s  %s  %s/%s  %s\n",
			stringValue(observation["id"], ""),
			stringValue(observation["observed_at"], ""),
			stringValue(observation["observation_type"], "episode"),
			stringValue(observation["status"], "raw"),
			text,
		)
	}
	return nil
}

func proposeObservation(ctx context.Context, args cliArgs, observationID, candidateClaim string) (map[string]any, error) {
	observationID = strings.TrimSpace(observationID)
	if observationID == "" {
		return nil, errors.New("observation id is required")
	}
	scope := scopeOrDefault(args, ".")
	payload := map[string]any{"channel": "cli", "observation_id": observationID, "promotion_flow": "observation_to_claim"}
	if candidateClaim = strings.TrimSpace(candidateClaim); candidateClaim != "" {
		payload["claim"] = candidateClaim
	}
	body := map[string]any{
		"scope":         scope,
		"proposal_type": flag(args, "proposal-type", "claim"),
		"title":         flag(args, "title", ""),
		"rationale":     flag(args, "rationale", ""),
		"target_type":   "observation",
		"target_id":     observationID,
		"source_url":    flag(args, "source-url", ""),
		"confidence":    floatFlag(args, "confidence", 0),
		"created_by":    flag(args, "created-by", "abra-cli"),
		"approval_id":   flag(args, "approval-id", ""),
		"payload":       payload,
	}
	return postJSON(ctx, args, "/learning/proposals", body)
}
