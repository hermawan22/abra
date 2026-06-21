package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordingLocker struct {
	events    *[]string
	lockErr   error
	unlockErr error
}

func (l recordingLocker) Lock(context.Context) (func() error, error) {
	*l.events = append(*l.events, "lock")
	if l.lockErr != nil {
		return nil, l.lockErr
	}
	return func() error {
		*l.events = append(*l.events, "unlock")
		return l.unlockErr
	}, nil
}

type recordingStore struct {
	events  *[]string
	applied map[string]bool
}

func (s recordingStore) EnsureMigrationTable(context.Context) error {
	*s.events = append(*s.events, "ensure")
	return nil
}

func (s recordingStore) MigrationApplied(_ context.Context, filename string) (bool, error) {
	*s.events = append(*s.events, "applied:"+filename)
	return s.applied[filename], nil
}

func (s recordingStore) ApplyMigration(_ context.Context, filename string, sql string) error {
	*s.events = append(*s.events, "apply:"+filename+":"+sql)
	return nil
}

func TestRunHoldsAdvisoryLockAroundMigrationExecution(t *testing.T) {
	dir := t.TempDir()
	mustWriteMigration(t, dir, "002_pending.sql", "-- +goose Up\nSELECT 2;\n-- +goose Down\nSELECT 0;\n")
	mustWriteMigration(t, dir, "001_done.sql", "SELECT 1;\n")

	var events []string
	err := run(
		context.Background(),
		recordingStore{
			events:  &events,
			applied: map[string]bool{"001_done.sql": true},
		},
		recordingLocker{events: &events},
		dir,
	)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	want := []string{
		"lock",
		"ensure",
		"applied:001_done.sql",
		"applied:002_pending.sql",
		"apply:002_pending.sql:-- +goose Up\nSELECT 2;\n",
		"unlock",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events mismatch\nwant: %#v\n got: %#v", want, events)
	}
}

func TestRunReturnsLockErrorBeforeMigrationWork(t *testing.T) {
	dir := t.TempDir()
	mustWriteMigration(t, dir, "001_init.sql", "SELECT 1;\n")

	var events []string
	lockErr := errors.New("database is busy")
	err := run(
		context.Background(),
		recordingStore{events: &events},
		recordingLocker{events: &events, lockErr: lockErr},
		dir,
	)
	if !errors.Is(err, lockErr) {
		t.Fatalf("expected lock error, got %v", err)
	}
	if want := []string{"lock"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events mismatch\nwant: %#v\n got: %#v", want, events)
	}
}

func TestRunReturnsUnlockError(t *testing.T) {
	dir := t.TempDir()
	mustWriteMigration(t, dir, "001_init.sql", "SELECT 1;\n")

	var events []string
	unlockErr := errors.New("unlock failed")
	err := run(
		context.Background(),
		recordingStore{events: &events},
		recordingLocker{events: &events, unlockErr: unlockErr},
		dir,
	)
	if !errors.Is(err, unlockErr) {
		t.Fatalf("expected unlock error, got %v", err)
	}
	if !strings.Contains(err.Error(), "unlock failed") {
		t.Fatalf("expected unlock error text, got %v", err)
	}
}

func TestMigrationLockTimeoutUsesPositiveDurationOverride(t *testing.T) {
	t.Setenv("ABRA_MIGRATION_LOCK_TIMEOUT", "2s")

	if got := migrationLockTimeout(); got != 2*time.Second {
		t.Fatalf("timeout = %s, want 2s", got)
	}
}

func TestMigrationLockTimeoutFallsBackForInvalidOverride(t *testing.T) {
	t.Setenv("ABRA_MIGRATION_LOCK_TIMEOUT", "0s")

	if got := migrationLockTimeout(); got != defaultMigrationLockTimeout {
		t.Fatalf("timeout = %s, want %s", got, defaultMigrationLockTimeout)
	}
}

func mustWriteMigration(t *testing.T, dir string, name string, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
