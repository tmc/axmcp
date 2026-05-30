#!/usr/bin/env bash
# Verify the native Chrome DevTools UI against one axcdp per-window target.
#
# This is intentionally a live integration loop, not a unit test. It needs a
# running axcdp endpoint, Chrome or Chrome Canary, the sibling cdp CLI, and
# axmcp permission to inspect/screenshot Chrome's UI.

set -euo pipefail

endpoint="${AXCDP_ENDPOINT:-http://127.0.0.1:9221}"
browser="${AXCDP_BROWSER:-Google Chrome Canary}"
selector="${AXCDP_TARGET:-/axcdp/window/}"
outdir="${AXCDP_VERIFY_OUT:-/tmp/axcdp-native-devtools}"
cdp_bin="${CDP_BIN:-}"
frontend="${AXCDP_DEVTOOLS_FRONTEND:-bundled}"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
cdp_src="$repo_root/../cdp/cmd/cdp"

mkdir -p "$outdir"

if [[ -z "$cdp_bin" ]]; then
	if [[ -d "$cdp_src" ]]; then
		cdp_bin="source"
	elif command -v cdp >/dev/null 2>&1; then
		cdp_bin="$(command -v cdp)"
	else
		echo "missing cdp CLI: set CDP_BIN or check out ../cdp" >&2
		exit 1
	fi
fi

need() {
	command -v "$1" >/dev/null 2>&1 || {
		echo "missing required command: $1" >&2
		exit 1
	}
}

need jq
need axmcp

json_only() {
	awk 'seen || /^[[:space:]]*[\{\[]/ { seen=1; print }'
}

run_cdp() {
	if [[ "$cdp_bin" == "source" ]]; then
		(cd "$cdp_src" && env GOWORK=off go run . "$@") </dev/null
	else
		"$cdp_bin" "$@" </dev/null
	fi
}

host="${endpoint#http://}"
host="${host#https://}"
host="${host%/}"

targets_json="$outdir/json-list.json"
version_json="$outdir/json-version.json"
curl -fsS "$endpoint/json/list" >"$targets_json"
curl -fsS "$endpoint/json/version" >"$version_json"
jq -e 'all(.[]; ((.url // "") | contains("/axcdp/app/")) | not)' "$targets_json" >/dev/null || {
	echo "inspect list contains app-level targets; expected window targets only" >&2
	exit 1
}

devtools_url() {
	local ws_path="$1"
	case "$frontend" in
	bundled)
		printf 'devtools://devtools/bundled/inspector.html?ws=%s%s\n' "$host" "$ws_path"
		;;
	remote|serve_rev)
		local rev
		local remote_version
		rev="$(jq -r '.["WebKit-Version"] // "" | capture("\\(@(?<rev>[0-9a-f]+)\\)").rev // empty' "$version_json")"
		remote_version="$(jq -r '.Browser // "" | sub("^[^/]+/"; "")' "$version_json")"
		if [[ -z "$rev" ]]; then
			echo "cannot derive DevTools frontend revision from $version_json" >&2
			exit 1
		fi
		if [[ -z "$remote_version" || "$remote_version" == "null" ]]; then
			remote_version="120.0.0.0"
		fi
		printf 'devtools://devtools/remote/serve_rev/@%s/inspector.html?remoteVersion=%s&remoteFrontend=true&ws=%s%s\n' \
			"$rev" "$remote_version" "$host" "$ws_path"
		;;
	*)
		echo "unsupported AXCDP_DEVTOOLS_FRONTEND=$frontend; use bundled or remote" >&2
		exit 1
		;;
	esac
}

candidates_file="$outdir/candidates.tsv"
jq -r --arg selector "$selector" '
	[.[] | select(.type == "page") |
	select((.url // "" | contains($selector)) or
	       (.title // "" | contains($selector)) or
	       (.id // "" | contains($selector)))] |
	sort_by(
		if (.url // "" | contains("/axcdp/window/")) then 0 else 1 end,
		if (.description // "") == "Finder" then 1 else 0 end
	) |
	.[] | [.id, .title, .url, .webSocketDebuggerUrl] | @tsv' "$targets_json" >"$candidates_file"
if [[ ! -s "$candidates_file" ]]; then
	echo "no page target matching $selector" >&2
	exit 1
fi

remote_host="${host%%:*}"
remote_port="${host##*:}"

dom_json="$outdir/dom.json"
layout_json="$outdir/layout.json"
screenshot_len="$outdir/screenshot-len.txt"

target_id=
target_title=
target_url=
native_url=
while IFS=$'\t' read -r candidate_id candidate_title candidate_url ws_url; do
	ws_path="${ws_url#ws://$host}"
	candidate_native_url="$(devtools_url "$ws_path")"

	echo "candidate: $candidate_title"
	echo "candidate id: $candidate_id"
	echo "candidate url: $candidate_url"

	if ! run_cdp -remote-host "$remote_host" -remote-port "$remote_port" -tab "$candidate_id" \
		-command 'DOM.getDocument {"depth":2}' -format json -timeout 10 | json_only >"$dom_json"; then
		continue
	fi
	if ! jq -e '
		.root.nodeName == "#document" and
		(.root.children[0].nodeName == "AXWindow" or
		 .root.children[0].children[0].nodeName == "AXWindow") and
		([.. | objects | select(.description? == "AX tree timed out")] | length == 0)
	' "$dom_json" >/dev/null; then
		continue
	fi
	target_id="$candidate_id"
	target_title="$candidate_title"
	target_url="$candidate_url"
	native_url="$candidate_native_url"
	break
done <"$candidates_file"

if [[ -z "$target_id" ]]; then
	echo "no matching target produced a live AXWindow DOM" >&2
	exit 1
fi

echo "target: $target_title"
echo "target id: $target_id"
echo "target url: $target_url"
echo "frontend: $frontend"
echo "native devtools: $native_url"

open -a "$browser" "$native_url"
sleep "${AXCDP_DEVTOOLS_SETTLE:-3}"

run_cdp -remote-host "$remote_host" -remote-port "$remote_port" -tab "$target_id" \
	-command 'Page.getLayoutMetrics {}' -format json -timeout 10 | json_only >"$layout_json"

jq -e '.contentSize.x == 0 and .contentSize.y == 0 and .contentSize.width > 32 and .contentSize.height > 32' "$layout_json" >/dev/null

run_cdp -remote-host "$remote_host" -remote-port "$remote_port" -tab "$target_id" \
	-command 'Page.captureScreenshot {"format":"jpeg","quality":50}' -format json -timeout 10 |
	json_only |
	jq -r '.data | length' >"$screenshot_len"
if [[ "$(cat "$screenshot_len")" -le 1024 ]]; then
	echo "screenshot data too small: $(cat "$screenshot_len")" >&2
	exit 1
fi

windows_txt="$outdir/axmcp-windows.txt"
tree_txt="$outdir/axmcp-tree.txt"
ui_png="$outdir/native-devtools.png"

axmcp pipe "app $browser // windows" >"$windows_txt"
target_path="${target_url#http://$host}"
if ! grep -F "$target_path" "$windows_txt" >/dev/null; then
	echo "native DevTools window for $target_url not found in axmcp windows" >&2
	cat "$windows_txt" >&2
	exit 1
fi

axmcp pipe "app $browser // window $target_path // tree --depth 5" >"$tree_txt"
grep -E 'Elements|Console|AXWindow|AXSplitGroup|AXTabGroup' "$tree_txt" >/dev/null || {
	echo "native DevTools tree did not expose expected UI/AX text" >&2
	exit 1
}

axmcp pipe "app $browser // window $target_path // screenshot --out $ui_png" >/dev/null
test -s "$ui_png" || {
	echo "native DevTools screenshot not written: $ui_png" >&2
	exit 1
}

cat <<EOF
native DevTools verification passed
artifacts:
  $dom_json
  $layout_json
  $screenshot_len
  $windows_txt
  $tree_txt
  $ui_png
EOF
