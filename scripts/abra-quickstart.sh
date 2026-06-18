#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

case "${1:-demo}" in
  install|setup)
    exec go run ./cmd/abra install "${@:2}"
    ;;
  up|start)
    exec go run ./cmd/abra up "${@:2}"
    ;;
  down|stop)
    exec go run ./cmd/abra down "${@:2}"
    ;;
  demo|quickstart)
    exec go run ./cmd/abra demo "${@:2}"
    ;;
  *)
    exec go run ./cmd/abra "$@"
    ;;
esac
