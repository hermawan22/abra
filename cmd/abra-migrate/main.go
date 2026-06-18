package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/store"
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

	if err := run(ctx, db, "migrations"); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, db *store.Store, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migrations found in %s", dir)
	}
	sort.Strings(files)

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
