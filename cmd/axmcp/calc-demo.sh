#!/usr/bin/env bash
#
# calc-demo.sh — drive macOS Calculator through the axmcp CLI, showing the
# unified ghost-cursor rendering. Demonstrates:
#   • bringing Calculator to the front via the pipe `raise` stage
#   • a chained click sequence (Clear, 7, ×, 8, +, 3, =) so the overlay
#     glides through every step in a single process
#   • optional pacing knobs via AXMCP_CURSOR_GLIDE_MS / SETTLE_MS / HOLD_MS
#
# Usage:
#   ./cmd/axmcp/calc-demo.sh           # default sequence: 7 × 8 + 3
#   ./cmd/axmcp/calc-demo.sh --no-raise   # leave whichever app is frontmost
#   ./cmd/axmcp/calc-demo.sh --slow       # double the per-click pacing

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

raise=true
slow=false
for arg in "$@"; do
  case "$arg" in
    --no-raise) raise=false ;;
    --raise) raise=true ;;
    --slow) slow=true ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *)
      printf 'unknown flag: %s\n' "$arg" >&2
      exit 2
      ;;
  esac
done

if $slow; then
  export AXMCP_CURSOR_GLIDE_MS=560
  export AXMCP_CURSOR_SETTLE_MS=180
  export AXMCP_CURSOR_HOLD_MS=400
fi

stages=()
if $raise; then
  stages+=("raise")
fi
stages+=(
  'find --contains "Clear"'
  click
  'find --contains 7'
  click
  'find --contains Multiply'
  click
  'find --contains 8'
  click
  'find --contains Add'
  click
  'find --contains 3'
  click
  'find --contains Equals'
  click
)

pipeline="app Calculator"
for stage in "${stages[@]}"; do
  pipeline+=" // $stage"
done

printf 'pipeline: %s\n\n' "$pipeline"
exec axmcp pipe "$pipeline"
