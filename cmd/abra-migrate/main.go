package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/store"
	"github.com/jackc/pgx/v5"
)

const (
	migrationLockClassID        int32 = 0x61627261 // "abra"
	migrationLockObjectID       int32 = 0x6d696772 // "migr"
	defaultMigrationLockTimeout       = 5 * time.Minute
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config failed", "error", err)
		os.Exit(1)
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := run(ctx, db, postgresMigrationLocker{databaseURL: cfg.DatabaseURL}, "migrations"); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

type migrationStore interface {
	EnsureMigrationTable(context.Context) error
	MigrationApplied(context.Context, string) (bool, error)
	ApplyMigration(context.Context, string, string) error
}

type migrationLocker interface {
	Lock(context.Context) (func() error, error)
}

type postgresMigrationLocker struct {
	databaseURL string
}

func (l postgresMigrationLocker) Lock(ctx context.Context) (func() error, error) {
	lockCtx, cancelLock := context.WithTimeout(ctx, migrationLockTimeout())
	defer cancelLock()

	conn, err := pgx.Connect(lockCtx, l.databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect for migration advisory lock: %w", err)
	}
	if _, err := conn.Exec(lockCtx, "SELECT pg_advisory_lock($1, $2)", migrationLockClassID, migrationLockObjectID); err != nil {
		_ = conn.Close(ctx)
		return nil, fmt.Errorf("acquire migration advisory lock: %w", err)
	}

	unlocked := false
	return func() error {
		if unlocked {
			return nil
		}
		unlocked = true

		unlockCtx, cancelUnlock := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelUnlock()

		var released bool
		unlockErr := conn.QueryRow(unlockCtx, "SELECT pg_advisory_unlock($1, $2)", migrationLockClassID, migrationLockObjectID).Scan(&released)

		closeCtx, cancelClose := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelClose()
		closeErr := conn.Close(closeCtx)

		if unlockErr != nil {
			return fmt.Errorf("release migration advisory lock: %w", unlockErr)
		}
		if !released {
			return fmt.Errorf("release migration advisory lock: lock was not held")
		}
		if closeErr != nil {
			return fmt.Errorf("close migration advisory lock connection: %w", closeErr)
		}
		return nil
	}, nil
}

func migrationLockTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ABRA_MIGRATION_LOCK_TIMEOUT"))
	if raw == "" {
		return defaultMigrationLockTimeout
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultMigrationLockTimeout
	}
	return timeout
}

func run(ctx context.Context, db migrationStore, locker migrationLocker, dir string) (err error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migrations found in %s", dir)
	}
	sort.Strings(files)

	unlock, err := locker.Lock(ctx)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlock())
	}()

	if err := db.EnsureMigrationTable(ctx); err != nil {
		return err
	}
	for _, path := range files {
		name := filepath.Base(path)
		applied, err := db.MigrationApplied(ctx, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := db.ApplyMigration(ctx, name, stripGooseDown(string(sql))); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("applied %s\n", name)
	}
	return nil
}

func stripGooseDown(sql string) string {
	const marker = "-- +goose Down"
	index := strings.Index(sql, marker)
	if index < 0 {
		return sql
	}
	return sql[:index]
}
