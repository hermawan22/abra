package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmbeddingReadinessStatusDistinguishesTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	if got := embeddingReadinessStatus(ctx, ctx.Err()); got != "timeout" {
		t.Fatalf("status = %q", got)
	}
	if got := embeddingReadinessStatus(context.Background(), context.DeadlineExceeded); got != "timeout" {
		t.Fatalf("deadline status = %q", got)
	}
	if got := embeddingReadinessStatus(context.Background(), errors.New("connection refused")); got != "error" {
		t.Fatalf("error status = %q", got)
	}
}
