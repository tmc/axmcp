# hygiene-verify: structural floor for the v0.2.0 commit baseline

`.github/workflows/hygiene-verify.yml` runs on every push to `main` and
every pull request. It encodes the audit / verify discipline that
shipped v0.2.0 — six gates that, together, prevent the regressions the
v0.2.0 audit caught from re-entering the tree. The workflow is the
gate, not the documentation; this file explains the choices behind it.

## What ships in the workflow

Six gates run sequentially on a `macos-14` runner:

1. **Trailer scan.** Reject any commit whose body contains a
   structured `Co-Authored-By: ... claude ...` trailer or a trailer
   ending in `noreply@anthropic`.
2. **Subject length.** Reject any non-merge commit whose subject
   exceeds 72 characters. Merge commits are skipped.
3. **Subject case.** Reject any non-merge commit whose first word
   after a `<scope>: ` prefix begins with an uppercase letter, with
   an explicit allowance for all-caps acronyms.
4. **`go mod tidy` clean.** Run it, fail if `go.mod` or `go.sum`
   change.
5. **Build / vet / test.** `go build ./...`, `go vet ./...` capped at
   8 warnings (the v0.2.0 baseline), `go test ./...`.
6. **Scratch leakage.** Reject tracked `.swp`, `.bak`, `.orig`,
   `.DS_Store` files; reject inter-agent collab path literals
   matching `/tmp/collab` followed by `-` (the convention used by
   collab-msg run files) embedded in tracked `.go` or `.md`; reject
   the iTerm session-ID env var literal (`ITERM` followed by
   `_SESSION_ID`) in the same.

The commit range scanned is `${{ github.event.pull_request.base.sha
}}..HEAD` for pull requests and `${{ github.event.before }}..${{
github.sha }}` for pushes to `main`. The push-side range falls back to
`HEAD^..HEAD` when `before` is the zero SHA or unreachable (force-push
cases where the prior tip is gone from the remote).

## Why `git interpret-trailers --parse`, not body grep

The first version of the trailer gate used
`grep -qiE '^Co-Authored-By:.*claude|noreply@anthropic'` against the
full commit body. The very first run on the very first PR — the one
adding this workflow — failed. The commit message describes what the
gate is checking for; one paragraph contained the words "noreply@anthropic"
mid-sentence as part of the explanation. Because shell `|` alternation
binds *after* `^`, the regex parsed as `(^Co-Authored-By:.*claude) |
(noreply@anthropic)`: the second branch was unanchored and matched
prose. The gate flagged its own description.

The fix is structural rather than regex-tightening:
`git interpret-trailers --parse` reads only the structured
`Key: Value` trailer block at the bottom of a commit message and
emits each parsed trailer on its own line. Body prose containing the
literal token `Co-Authored-By` cannot be parsed as a trailer because
it isn't in the trailer block. The check then reduces to: parse
trailers, grep the parsed output. Both alternations in the grep can
now be properly anchored to `^` because we're matching against
single-trailer lines, not multi-line bodies:

```
git interpret-trailers --parse \
  | grep -qiE '^Co-Authored-By:.*claude|^[A-Za-z-]+:.*noreply@anthropic'
```

The general lesson: when a gate's input may *describe* the gate, prefer
structured parsing over body grep. Any check that grep-matches free
text against text that documents the check has a built-in false-
positive surface; structured parsers eliminate that surface entirely.

## Why `macos-14`

The build, vet, and test gates need a darwin runner. The Accessibility
APIs, the SkyLight bindings, the simulator tooling, and the AX private
SPI all assume a macOS host. `macos-14` (Sonoma) is the current GitHub
Actions default for the macOS-14 family and matches the OS most
contributors develop against. The `go-version-file: go.mod` directive
on `actions/setup-go@v5` keeps the runner Go version pinned to whatever
the module declares (currently 1.26), so a Go bump in `go.mod` becomes
a single-file change that the workflow inherits automatically.

`macos-latest` would auto-roll to whichever macOS version Actions
adopts as default, which historically has produced silent breakage on
darwin-only API behaviour. Pinning a major version is the right
trade-off between cost (a quarterly version-bump PR) and predictability
(no CI breakage on the morning Apple ships a new macOS).

## Why no third-party actions

Only two actions are used: `actions/checkout@v4` (to clone the repo
with `fetch-depth: 0` so commit-range scans work) and
`actions/setup-go@v5` (to install the Go toolchain). Both are
first-party `actions/*` repositories from the GitHub Actions
organisation.

The trailer scan, subject scans, and scratch scan are inline `bash`
steps. They are five-to-fifteen lines each. A third-party action that
"checks for AI trailers" or "lints commit subjects" would add a
supply-chain dependency — an external `uses:` reference whose
pinned SHA can be silently re-tagged or whose maintainer can be
compromised — to replace fifteen lines of shell. The cost / benefit
favours the inline approach: shell is auditable in-place, has no
update cadence, and the GitHub-Actions runtime ships with `git`,
`bash`, `awk`, `sed`, and `grep` pre-installed.

This matches the "no supply-chain-hostile dependencies" line in
`ROADMAP.md`'s out-of-scope section.

## Why no `workflow_dispatch`

`workflow_dispatch` exposes a manual "Run workflow" button in the
Actions UI. The hygiene-verify gate is structural: it runs
unconditionally on `pull_request` and on `push: main`, and there is
no scenario where someone needs to hand-trigger a check that was
already performed automatically. Re-runs of failed gates happen via
the standard "Re-run jobs" UI that every workflow gets for free.

Adding `workflow_dispatch` would create a button that, by being
visible, looks important. Removing it leaves only one path — open a
PR or push to main — through which the gate fires. One path is easier
to reason about than two.

## Vet ceiling

`go vet` runs with a documented ceiling of 8 warnings. The v0.2.0
audit established that exact count: six `unsafe.Pointer` warnings in
`internal/purego/coresim` and one each in `internal/ghostcursor` and
`internal/ui`. The audit judged them acceptable at ship time. The
gate accepts that judgment as the floor and fails on regressions
(`> 8`). New warnings have to be either fixed or explicitly waived
by raising the ceiling — both visible in PR diff, neither silent.
Replacing the ceiling with `-vet=off` would lose the regression
signal entirely; chasing it down to zero is deferred work that
shouldn't block unrelated PRs.

## Open review feedback

PR #1 merged with two Copilot observations not yet addressed in
the workflow. Recording them here so the next iteration of the gate
has a clear punch list rather than rediscovering them from PR
history.

- **Process-substitution `git rev-list` failure can pass silently.**
  The gates that scan a commit range use the pattern
  `done < <(git rev-list "$base..$head")`. If `git rev-list` exits
  nonzero (invalid SHA range, force-push edge cases where the prior
  tip is gone), the loop receives no input and the gate passes
  without examining any commit. The intended behavior is a hard
  fail. Fix is to resolve the SHA list once into a variable, fail
  the step if `rev-list` errors, then iterate. Same fix unblocks
  three gates at once (trailer, length, case).
- **Subject-case scope-stripping sed is too permissive.** The current
  `sed -E 's/^[^:]+: //'` strips any `non-colon characters + colon
  + space` prefix, including `Fix:`, `WIP:`, or any other ad-hoc
  prefix. The lowercase-imperative check then applies to the word
  *after* the prefix, so `Fix: Add foo` would pass even though the
  scope itself violates the convention. Tightening the regex to
  `^[a-z0-9][a-z0-9/_-]*: ` (Conventional-Commits-shaped lowercase
  scope) would catch these.

Neither bug bites today against the v0.2.x commit shape — every
commit in `v0.1.5..main` uses a lowercase Conventional-Commits-
shaped scope, and no force-push has invalidated a range scan. They
become real on the day someone tries to push a branch with an
uppercase prefix or force-pushes over the gate's `before` SHA.

The "gate matches its own description" pattern has another concrete
instance in this design doc: the scratch-leak gate flags any tracked
markdown file containing the inter-agent collab path prefix (the
`/tmp/collab` plus hyphen convention from the iTerm collab-msg
protocol) or the iTerm session-id env var literal (`ITERM` plus
`_SESSION_ID`). Documenting the gate forces the doc to refer to
those literals indirectly. Same structural lesson as the trailer
gate: when a gate's input may *describe* the gate, prefer
structured parsing over body grep, or — if a body grep is
unavoidable — require the doc to refer to the literal indirectly.
