package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func postJSON(ctx context.Context, args cliArgs, path string, body map[string]any) (map[string]any, error) {
	return postJSONWithTimeout(ctx, args, path, body, cliTimeout(args, defaultHTTPTimeout))
}

func postJSONWithTimeout(ctx context.Context, args cliArgs, path string, body map[string]any, timeout time.Duration) (map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg(args).BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	return doJSON(req, timeout)
}

func getJSON(ctx context.Context, args cliArgs, path string) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg(args).BaseURL, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	body, err := doJSON(req, cliTimeout(args, defaultHTTPTimeout))
	if err != nil {
		if statusErr := (&httpStatusError{}); errors.As(err, &statusErr) {
			return body, statusErr.Code, err
		}
		return body, 0, err
	}
	return body, 200, nil
}

func (e *httpStatusError) Error() string {
	if stringValue(e.Payload["error"], "") == "approval_required" {
		if approval, _ := e.Payload["approval"].(map[string]any); approval != nil {
			action := stringValue(approval["action"], "")
			scope := stringValue(approval["scope"], "")
			targetType := stringValue(approval["target_type"], "")
			targetID := stringValue(approval["target_id"], "")
			parts := []string{"approval required"}
			if detail := stringValue(e.Payload["detail"], ""); detail != "" {
				parts = append(parts, "detail: "+detail)
			}
			request := "abra approvals request --scope " + shellQuote(scope) + " --action " + shellQuote(action)
			if targetType != "" {
				request += " --target-type " + shellQuote(targetType)
			}
			if targetID != "" {
				request += " --target-id " + shellQuote(targetID)
			}
			parts = append(parts, "request: "+request)
			parts = append(parts, "after approval, retry the original command with --approval-id <approval-id>")
			return strings.Join(parts, "\n")
		}
	}
	return fmt.Sprintf("http %d: %s", e.Code, e.Body)
}

func doJSON(req *http.Request, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxCLIResponseBody+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(raw) > maxCLIResponseBody {
		return nil, fmt.Errorf("response body exceeded %d bytes", maxCLIResponseBody)
	}
	var out map[string]any
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(raw)), Payload: out}
	}
	return out, nil
}
