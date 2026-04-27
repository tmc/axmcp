#!/usr/bin/env bash

set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

dimensions=(
  brightness
  cursor-scale
  body-opacity
  outline-opacity
  glow-opacity
  glow-scale
  fade-delay
  fade-duration
  move-glow-duration
  slow
  settle
  idle-wait
  end-wait
)

usage() {
  cat <<'EOF'
Usage:
  ./cmd/calc-click-test/sweep.sh list
  ./cmd/calc-click-test/sweep.sh <dimension> [-- <extra calc-click-test args...>]
  ./cmd/calc-click-test/sweep.sh all [-- <extra calc-click-test args...>]

Environment:
  SWEEP_SCREENSHOT_ROOT   directory for saved PNGs (default: /tmp/calc-click-sweeps/<timestamp>)
  SWEEP_SCREENSHOT_LATEST symlink updated to the latest sweep root
  SWEEP_VIDEO             set to 0 to disable run.mp4 recording during the sweep

Examples:
  ./cmd/calc-click-test/sweep.sh glow-opacity
  ./cmd/calc-click-test/sweep.sh fade-duration -- -slow 420ms -repeat 1
  ./cmd/calc-click-test/sweep.sh all -- -sequence 3,7 -repeat 1
EOF
}

list_dimensions() {
  printf '%s\n' "${dimensions[@]}"
}

dimension_flag() {
  case "$1" in
    brightness) echo "-brightness" ;;
    cursor-scale) echo "-cursor-scale" ;;
    body-opacity) echo "-body-opacity" ;;
    outline-opacity) echo "-outline-opacity" ;;
    glow-opacity) echo "-glow-opacity" ;;
    glow-scale) echo "-glow-scale" ;;
    fade-delay) echo "-fade-delay" ;;
    fade-duration) echo "-fade-duration" ;;
    move-glow-duration) echo "-move-glow-duration" ;;
    slow) echo "-slow" ;;
    settle) echo "-settle" ;;
    idle-wait) echo "-idle-wait" ;;
    end-wait) echo "-end-wait" ;;
    *) return 1 ;;
  esac
}

dimension_values() {
  case "$1" in
    brightness) printf '%s\n' 0.75 2.55 ;;
    cursor-scale) printf '%s\n' 1.65 3.5475 ;;
    body-opacity) printf '%s\n' 0.525 3.00 ;;
    outline-opacity) printf '%s\n' 0.60 3.30 ;;
    glow-opacity) printf '%s\n' 0.45 4.20 ;;
    glow-scale) printf '%s\n' 0.975 2.85 ;;
    fade-delay) printf '%s\n' 0ms 2100ms ;;
    fade-duration) printf '%s\n' 105ms 1350ms ;;
    move-glow-duration) printf '%s\n' 90ms 1050ms ;;
    slow) printf '%s\n' 210ms 1650ms ;;
    settle) printf '%s\n' 0ms 480ms ;;
    idle-wait) printf '%s\n' 750ms 5400ms ;;
    end-wait) printf '%s\n' 750ms 6300ms ;;
    *) return 1 ;;
  esac
}

screenshot_root=
latest_link=

ensure_screenshot_root() {
  if [[ -n ${screenshot_root:-} ]]; then
    return
  fi
  if [[ -n ${SWEEP_SCREENSHOT_ROOT:-} ]]; then
    screenshot_root=$SWEEP_SCREENSHOT_ROOT
    mkdir -p "$screenshot_root"
  else
    local base=/tmp/calc-click-sweeps
    mkdir -p "$base"
    screenshot_root=$base/$(date +"%Y%m%d-%H%M%S")
    local suffix=0
    while [[ -e $screenshot_root ]]; do
      suffix=$((suffix + 1))
      screenshot_root=$base/$(date +"%Y%m%d-%H%M%S")-$suffix
    done
    mkdir -p "$screenshot_root"
  fi
  if [[ -n ${SWEEP_SCREENSHOT_LATEST:-} ]]; then
    latest_link=$SWEEP_SCREENSHOT_LATEST
  else
    latest_link="${TMPDIR:-/tmp}/calc-click-sweep-latest"
  fi
  ln -sfn "$screenshot_root" "$latest_link"
  printf 'screenshot root: %s\n' "$screenshot_root"
  printf 'latest link: %s -> %s\n' "$latest_link" "$screenshot_root"
}

slugify() {
  local value
  value=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  value=${value//[^a-z0-9]/-}
  value=$(printf '%s' "$value" | sed -E 's/-+/-/g; s/^-//; s/-$//')
  if [[ -z $value ]]; then
    value=unnamed
  fi
  printf '%s\n' "$value"
}

sweep_video_enabled() {
  case "${SWEEP_VIDEO:-}" in
    0|false|FALSE|no|NO|off|OFF) return 1 ;;
    *) return 0 ;;
  esac
}

run_dimension() {
  local dimension=$1
  shift

  local flag
  flag=$(dimension_flag "$dimension")
  ensure_screenshot_root

  local values=()
  while IFS= read -r value; do
    values+=("$value")
  done < <(dimension_values "$dimension")
  local total=${#values[@]}
  local i

  printf '\n===== sweeping %s =====\n' "$dimension"
  for ((i = 0; i < total; i++)); do
    local value=${values[i]}
    local run_dir=$screenshot_root/$dimension/$(printf '%02d' "$((i + 1))")-$(slugify "$value")
    local extra_args=("$@")
    local cmd_prefix=(env MACGO_NO_RELAUNCH=1)
    local cmd_args=(go run ./cmd/calc-click-test -repeat 1 -slow 0ms -screenshot-dir "$run_dir")
    if sweep_video_enabled; then
      cmd_args+=(-video)
    fi
    cmd_args+=("$flag" "$value")
    if ((${#extra_args[@]} > 0)); then
      cmd_args+=("${extra_args[@]}")
    fi
    mkdir -p "$run_dir"
    local command
    printf -v command '%q ' "${cmd_prefix[@]}" "${cmd_args[@]}"
    command=${command% }
    cat >"$run_dir/run.meta" <<EOF
dimension=$dimension
value=$value
index=$((i + 1))
total=$total
command=$command
EOF
    printf '\n[%d/%d] %s=%s\n' "$((i + 1))" "$total" "$dimension" "$value"
    printf 'screenshots: %s\n' "$run_dir"
    printf 'command: %s\n\n' "$command"
    "${cmd_prefix[@]}" "${cmd_args[@]}"
    if ! find "$run_dir" -maxdepth 1 -name '*.png' -print -quit | grep -q .; then
      printf 'sweep run produced no PNGs: %s\n' "$run_dir" >&2
      return 1
    fi
  done
}

generate_review() {
  if [[ -z ${screenshot_root:-} ]]; then
    return
  fi
  go run ./cmd/calc-click-report -root "$screenshot_root"
  printf 'review index: %s\n' "$screenshot_root/index.md"
  printf 'review manifest: %s\n' "$screenshot_root/review.json"
}

if (($# == 0)); then
  usage
  printf '\nDimensions:\n'
  list_dimensions
  exit 0
fi

dimension=$1
shift

extra=()
if (($# > 0)); then
  if [[ $1 == "--" ]]; then
    shift
  fi
  extra=("$@")
fi

case "$dimension" in
  list)
    list_dimensions
    ;;
  all)
    for dim in "${dimensions[@]}"; do
      if ((${#extra[@]})); then
        run_dimension "$dim" "${extra[@]}"
      else
        run_dimension "$dim"
      fi
    done
    generate_review
    ;;
  *)
    if ! dimension_flag "$dimension" >/dev/null; then
      usage
      printf '\nunknown dimension: %s\n' "$dimension" >&2
      exit 2
    fi
    if ((${#extra[@]})); then
      run_dimension "$dimension" "${extra[@]}"
    else
      run_dimension "$dimension"
    fi
    generate_review
    ;;
esac
