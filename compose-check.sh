#!/usr/bin/env bash
set -uo pipefail

# Validate the minimal native Go compose example. Exit code only:
#   0 = /health answers, 1 = service unavailable.

BASE_URL="${WATERMARKS_SERVICE_URL:-http://127.0.0.1:8765}"

if curl -fsS "$BASE_URL/health" >/dev/null 2>&1; then
  echo "aiwr: OK"
  exit 0
fi

echo "aiwr: FAIL (no /health at $BASE_URL)"
exit 1
