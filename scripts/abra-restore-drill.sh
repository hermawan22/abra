#!/usr/bin/env bash
set -euo pipefail

DUMP_FILE="${ABRA_RESTORE_DUMP:-${1:-}}"
SOURCE_DATABASE_URL="${DATABASE_URL:-${ABRA_DATABASE_URL:-}}"
TARGET_DATABASE_URL="${ABRA_RESTORE_DATABASE_URL:-}"
DRY_RUN="${ABRA_DRY_RUN:-1}"
PG_RESTORE_BIN="${PG_RESTORE_BIN:-pg_restore}"
MIGRATE_CMD="${ABRA_MIGRATE_CMD:-}"
RUN_SMOKE="${ABRA_RUN_SMOKE:-0}"

fail() {
  echo "abra-restore-drill: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

is_dry_run() {
  [[ "$DRY_RUN" == "1" || "$DRY_RUN" == "true" ]]
}

if [[ -z "$DUMP_FILE" ]]; then
  fail "set ABRA_RESTORE_DUMP or pass a dump file path"
fi

if [[ ! -f "$DUMP_FILE" ]]; then
  if is_dry_run; then
    echo "abra-restore-drill: dry-run; dump file not found, skipping manifest validation: ${DUMP_FILE}"
  else
    fail "dump file not found: $DUMP_FILE"
  fi
elif command -v "$PG_RESTORE_BIN" >/dev/null 2>&1; then
  echo "abra-restore-drill: validating dump manifest"
  "$PG_RESTORE_BIN" --list "$DUMP_FILE" >/dev/null
elif is_dry_run; then
  echo "abra-restore-drill: pg_restore not found; skipping manifest validation in dry-run"
else
  fail "missing required command: $PG_RESTORE_BIN"
fi

if is_dry_run; then
  echo "abra-restore-drill: dry-run; restore is not executed by default"
  if [[ -n "$TARGET_DATABASE_URL" ]]; then
    echo "abra-restore-drill: would restore into ABRA_RESTORE_DATABASE_URL"
  else
    echo "abra-restore-drill: set ABRA_RESTORE_DATABASE_URL for the isolated target database"
  fi
  echo "abra-restore-drill: set ABRA_DRY_RUN=0 to execute"
  exit 0
fi

if [[ -z "$TARGET_DATABASE_URL" ]]; then
  fail "set ABRA_RESTORE_DATABASE_URL to an isolated restore target"
fi

if [[ -n "$SOURCE_DATABASE_URL" && "$TARGET_DATABASE_URL" == "$SOURCE_DATABASE_URL" && "${ABRA_ALLOW_RESTORE_TO_DATABASE_URL:-0}" != "1" ]]; then
  fail "restore target matches DATABASE_URL; refusing unless ABRA_ALLOW_RESTORE_TO_DATABASE_URL=1"
fi

require_command "$PG_RESTORE_BIN"

echo "abra-restore-drill: restoring dump into isolated target"
"$PG_RESTORE_BIN" \
  --dbname "$TARGET_DATABASE_URL" \
  --clean \
  --if-exists \
  --no-owner \
  --no-acl \
  "$DUMP_FILE"

if [[ -n "$MIGRATE_CMD" ]]; then
  echo "abra-restore-drill: running migration command"
  DATABASE_URL="$TARGET_DATABASE_URL" bash -lc "$MIGRATE_CMD"
else
  echo "abra-restore-drill: migration command skipped; set ABRA_MIGRATE_CMD to run one"
fi

if [[ "$RUN_SMOKE" == "1" || "$RUN_SMOKE" == "true" ]]; then
  echo "abra-restore-drill: running smoke suite against ABRA_BASE_URL"
  bash scripts/abra-smoke.sh
else
  echo "abra-restore-drill: smoke skipped; set ABRA_RUN_SMOKE=1 after pointing API at the restored database"
fi

echo "abra-restore-drill: complete"
