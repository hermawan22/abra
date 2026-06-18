package server

import (
	"context"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/store"
)

type composerStore struct {
	*store.Store
	brain *brain.Service
}

func (s *composerStore) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (store.RecallResult, error) {
	return s.brain.Recall(ctx, query, scope, limit, includeUnverified)
}
