package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool   *pgxpool.Pool
	runner storeRunner
	inTx   bool
}

type storeRunner interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ScopeSummary struct {
	Scope        string `json:"scope"`
	Documents    int    `json:"documents"`
	Claims       int    `json:"claims"`
	Observations int    `json:"observations"`
	Summaries    int    `json:"summaries"`
	Entities     int    `json:"entities"`
	Relations    int    `json:"relations"`
	Conflicts    int    `json:"conflicts"`
	Sources      int    `json:"sources"`
	Jobs         int    `json:"jobs"`
}

const maxListScopesLimit = 10000
const maxMemoryHealthSourceDetails = 10

var fullTextTermPattern = regexp.MustCompile(`[A-Za-z0-9_]+`)

func pgInterval(duration time.Duration) string {
	seconds := int64(duration.Round(time.Second).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d seconds", seconds)
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool, runner: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) queryRunner() storeRunner {
	if s.runner != nil {
		return s.runner
	}
	return s.pool
}

func (s *Store) WithTx(ctx context.Context, fn func(*Store) error) error {
	if s.inTx {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	txStore := *s
	txStore.runner = tx
	txStore.inTx = true
	if err := fn(&txStore); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) withTxRunner(ctx context.Context, fn func(storeRunner) error) error {
	if s.inTx {
		return fn(s.queryRunner())
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) LockSourceIngest(ctx context.Context, scope, sourceURL string) error {
	if !s.inTx {
		return fmt.Errorf("source ingest advisory lock requires active transaction")
	}
	key := sourceIngestLockKey(scope, sourceURL)
	_, err := s.queryRunner().Exec(ctx, "SELECT pg_advisory_xact_lock($1)", key)
	return err
}

func sourceIngestLockKey(scope, sourceURL string) int64 {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"source-ingest",
		strings.TrimSpace(scope),
		strings.TrimSpace(sourceURL),
	}, "\x00")))
	return int64(binary.BigEndian.Uint64(sum[:8]))
}

func (s *Store) Ready(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, "SELECT '[1,0,0]'::vector(3)"); err != nil {
		return err
	}
	requiredTables := []string{
		"documents",
		"chunks",
		"claims",
		"observations",
		"evidence",
		"audit_events",
		"source_configs",
		"ingestion_jobs",
		"entities",
		"relations",
		"memory_summaries",
		"policies",
		"approval_requests",
		"rate_limit_buckets",
		"brain_traces",
		"brain_eval_runs",
	}
	for _, table := range requiredTables {
		var exists bool
		if err := s.pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("required table %s is missing; run migrations", table)
		}
	}
	var summaryLevelConstraint string
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(pg_get_constraintdef(oid), '')
		FROM pg_constraint
		WHERE conname = 'memory_summaries_level_check'
		  AND conrelid = 'public.memory_summaries'::regclass
		LIMIT 1
	`).Scan(&summaryLevelConstraint); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("required memory_summaries level constraint is missing; run migrations")
		}
		return err
	}
	if !memorySummaryLevelConstraintReady(summaryLevelConstraint) {
		return fmt.Errorf("memory_summaries level constraint is stale; run migrations")
	}
	return nil
}

func (s *Store) AllowRateLimit(ctx context.Context, key string, window time.Duration, limit int) (bool, time.Time, error) {
	if strings.TrimSpace(key) == "" {
		return false, time.Time{}, fmt.Errorf("rate limit key is required")
	}
	if window <= 0 {
		return false, time.Time{}, fmt.Errorf("rate limit window must be positive")
	}
	if limit < 1 {
		return false, time.Time{}, fmt.Errorf("rate limit limit must be positive")
	}
	var count int
	var resetAt time.Time
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rate_limit_buckets (bucket_key, reset_at, count)
		VALUES ($1, $2, 1)
		ON CONFLICT (bucket_key) DO UPDATE
		SET
		  reset_at = CASE
		    WHEN rate_limit_buckets.reset_at <= now() THEN EXCLUDED.reset_at
		    ELSE rate_limit_buckets.reset_at
		  END,
		  count = CASE
		    WHEN rate_limit_buckets.reset_at <= now() THEN 1
		    ELSE rate_limit_buckets.count + 1
		  END,
		  updated_at = now()
		RETURNING count, reset_at
	`, key, time.Now().Add(window)).Scan(&count, &resetAt)
	if err != nil {
		return false, time.Time{}, err
	}
	return count <= limit, resetAt, nil
}

func (s *Store) PruneRateLimitBuckets(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		olderThan = 24 * time.Hour
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM rate_limit_buckets
		WHERE updated_at < now() - $1::interval
	`, pgInterval(olderThan))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ExpireClaims(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE claims
		SET status = 'expired',
		    freshness_status = 'expired',
		    freshness_checked_at = now(),
		    updated_at = now()
		WHERE expires_at IS NOT NULL
		  AND expires_at <= now()
		  AND status NOT IN ('deprecated', 'expired')
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) EnsureMigrationTable(ctx context.Context) error {
	_, err := s.queryRunner().Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func (s *Store) MigrationApplied(ctx context.Context, filename string) (bool, error) {
	var found int
	err := s.pool.QueryRow(ctx, "SELECT 1 FROM schema_migrations WHERE filename = $1", filename).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == pgx.ErrNoRows {
		return false, nil
	}
	return false, err
}

func (s *Store) ApplyMigration(ctx context.Context, filename string, sql string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	if _, err := tx.Exec(ctx, sql); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", filename); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
