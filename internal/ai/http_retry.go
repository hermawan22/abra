package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	providerHTTPMaxAttempts   = 3
	providerHTTPMaxBodyBytes  = 8 << 20
	providerHTTPRetryAfterCap = 5 * time.Second
)

type providerHTTPRequest struct {
	Method        string
	URL           string
	Body          []byte
	FailurePrefix string
	ReadPrefix    string
	Configure     func(*http.Request)
}

func doProviderHTTPRequest(ctx context.Context, client *http.Client, options providerHTTPRequest) ([]byte, error) {
	method := options.Method
	if method == "" {
		method = http.MethodPost
	}
	failurePrefix := firstNonEmpty(options.FailurePrefix, "ai provider request failed")
	readPrefix := firstNonEmpty(options.ReadPrefix, "read response")
	var lastErr error
	for attempt := 0; attempt < providerHTTPMaxAttempts; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, options.URL, bytes.NewReader(options.Body))
		if err != nil {
			return nil, fmt.Errorf("%w: create request: %v", ErrInvalidRequest, err)
		}
		if options.Configure != nil {
			options.Configure(request)
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil || attempt == providerHTTPMaxAttempts-1 {
				return nil, fmt.Errorf("%s: %w", failurePrefix, err)
			}
			lastErr = err
			if sleepErr := sleepProviderRetry(ctx, backoffForAttempt(attempt)); sleepErr != nil {
				return nil, fmt.Errorf("%s: %w", failurePrefix, sleepErr)
			}
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, providerHTTPMaxBodyBytes))
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidResponse, readPrefix, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalidResponse, readPrefix, closeErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return raw, nil
		}
		if retryableProviderStatus(response.StatusCode) && attempt < providerHTTPMaxAttempts-1 {
			if sleepErr := sleepProviderRetry(ctx, retryDelay(response.Header.Get("Retry-After"), attempt)); sleepErr != nil {
				return nil, fmt.Errorf("%s: %w", failurePrefix, sleepErr)
			}
			continue
		}
		return nil, fmt.Errorf("%s: status=%d body=%s", failurePrefix, response.StatusCode, string(raw))
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%s: %w", failurePrefix, lastErr)
	}
	return nil, fmt.Errorf("%s: exhausted retries", failurePrefix)
}

func retryableProviderStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return status >= 520 && status <= 599
	}
}

func retryDelay(header string, attempt int) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return backoffForAttempt(attempt)
	}
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds < 0 {
			return 0
		}
		return minDuration(time.Duration(seconds)*time.Second, providerHTTPRetryAfterCap)
	}
	if when, err := http.ParseTime(header); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0
		}
		return minDuration(delay, providerHTTPRetryAfterCap)
	}
	return backoffForAttempt(attempt)
}

func backoffForAttempt(attempt int) time.Duration {
	delay := time.Duration(200*(1<<attempt)) * time.Millisecond
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sleepProviderRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
