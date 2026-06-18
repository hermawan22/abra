#!/usr/bin/env bash
set -euo pipefail

DATABASE_URL="${DATABASE_URL:-${ABRA_DATABASE_URL:-}}"
BACKUP_DIR="${ABRA_BACKUP_DIR:-backups}"
STAMP="$(date -u +%Y%m%d_%H%M%S)"
BACKUP_FILE="${ABRA_BACKUP_FILE:-${BACKUP_DIR}/abra_${STAMP}.dump}"
MANIFEST_FILE="${ABRA_BACKUP_MANIFEST:-${BACKUP_FILE}.manifest.txt}"
DRY_RUN="${ABRA_DRY_RUN:-0}"
PG_DUMP_BIN="${PG_DUMP_BIN:-pg_dump}"
PG_RESTORE_BIN="${PG_RESTORE_BIN:-pg_restore}"

fail() {
  echo "abra-backup: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

is_dry_run() {
  [[ "$DRY_RUN" == "1" || "$DRY_RUN" == "true" ]]
}

if [[ -z "$DATABASE_URL" ]]; then
  fail "set DATABASE_URL or ABRA_DATABASE_URL"
fi

echo "abra-backup: destination=${BACKUP_FILE}"
echo "abra-backup: manifest=${MANIFEST_FILE}"

if is_dry_run; then
  echo "abra-backup: dry-run; would create logical custom-format dump and manifest"
  exit 0
fi

require_command "$PG_DUMP_BIN"
require_command "$PG_RESTORE_BIN"

mkdir -p "$(dirname "$BACKUP_FILE")"
"$PG_DUMP_BIN" "$DATABASE_URL" \
  --format=custom \
  --no-owner \
  --no-acl \
  --file "$BACKUP_FILE"

"$PG_RESTORE_BIN" --list "$BACKUP_FILE" >"$MANIFEST_FILE"

echo "abra-backup: wrote ${BACKUP_FILE}"
echo "abra-backup: validated manifest ${MANIFEST_FILE}"
