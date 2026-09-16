# treecheck Development Guide

A single Go program at the repository root, module
`github.com/prog893/treecheck/v2`. Standard library only: no third-party
dependencies, so there is no `go.sum`. It builds for macOS and Linux on amd64
and arm64 with `CGO_ENABLED=0`, and nothing outside the binary is needed at
run time.

Versions 1.x were a bash script. It was retired in 2.0.0; its interface
(output format, error wording, exit codes, flags) is carried forward and
pinned by the golden files described under Testing.

| file | holds |
|---|---|
| `main.go` | flags, the run, worker dispatch, exit status |
| `walk.go` | the tree walk: pruning, depth, exclusions, byte order |
| `check.go` | per-file verify and create, sidecar parsing, hashing |
| `report.go` | counters, the recap, the exit-code mapping |
| `display.go`, `dashboard.go` | the full-screen view's state and layout |
| `render.go` | the `--log` view's pinned status block |
| `screen.go` | the alternate-screen double buffer |
| `keys.go`, `termios_*.go` | raw terminal input, per-platform ioctl names |
| `review.go` | per-file evidence, the results loop, key decoding |
| `pause.go` | the pause gate |

## What this tool promises

When it reports clean, the data is intact. Every bug worth fixing here is one
that breaks that promise **without being visible in the output**. That has
happened repeatedly:

- Paths resolved against the wrong directory, so every file reported
  "no sidecar" and no corruption was ever detected.
- Recursion off by default, so a volume root scan silently examined only the
  top level and still printed a confident summary.
- A failed directory walk indistinguishable from an empty directory, so an
  unreadable tree reported success on whatever subset happened to be reachable.
- Create-only mode counting files as `Verified` that were never read back.

The pattern is always the same: the tool checks less than it claims and says
nothing. Weight review effort accordingly. A cosmetic bug here is far less
serious than a silent no-op.

## Testing

Never trust a change that has not been run.

```bash
gofmt -l . && go vet ./... && go test ./... && go test -race ./...
```

`go test` asserts the contract directly:

| Test | Pins |
|---|---|
| `TestGoldenOutput` | complete piped stdout, stderr and exit status for every mode and error path, byte for byte, in `testdata/*.golden` |
| `TestOutcomeCategories`, `TestVerdictTokens` | the all-outcomes fixture below |
| `TestExitCodes`, `TestStoppingIsNotFailing` | the exit-code contract |
| `TestFailedWalkIsNotAnEmptyDirectory`, `TestHiddenDirectoryIsPruned` | walk failure versus pruning |
| `TestCreateOnlyNeverCountsAsVerified` | `-c -n` never reports `Verified` |
| `TestSidecarMustBeADigest`, `TestControlBytesInFilenames` | untrusted input never reaches the terminal |
| `TestWalkOrderIsDeterministic`, `TestOrderHoldsWhenOneFileIsSlow` | byte order whatever the worker count |
| `TestScopeFlags`, `TestExcludeDirectories`, `TestForceOverwritesOnlyWithF` | flag semantics, including near-miss names |
| `TestDashboard*`, `TestRowsFillTheWidth`, `TestPanesDoNotShareBorders`, `TestReview*` | layout at many terminal sizes |
| `TestDisplayConcurrency` (under `-race`) | the display's shared state |

The golden files are the interface. They were recorded from output checked
against the 1.x shell implementation on the same fixtures. A change to any of
them is an interface change: regenerate with
`go test -run TestGoldenOutput -update .`, and review the diff as one.

The all-outcomes fixture holds every category at once, so a fix to one cannot
pass by breaking another:

| Fixture | Expected |
|---|---|
| intact file with matching sidecar | `Verified` |
| file whose contents were changed | `Mismatched` |
| file with no sidecar | `Missing/empty` |
| file with an empty sidecar | `Missing/empty` |
| sidecar holding anything but a 64-character hex digest | `Missing/empty`, exit 2, none of its bytes printed |
| file with mode `000` | `I/O errors` |
| nested subdirectory | scanned |
| unreadable **hidden** dir (`.Trashes`) | pruned, no error |
| unreadable **non-hidden** dir | walk fails, exit 1 |

New tests are validated by mutation: break the behavior the test names and
confirm that test fails. Two mutations have survived before, and both were
real findings: a rule enforced twice, and an exclusion nothing asserted.

Some things cannot be driven from `go test` and are checked through a pty
against the built binary before a change to the terminal code lands:

- Interrupts: `SIGINT`, `SIGTERM`, `q`, and each of those while paused, all
  exit 130 and restore the terminal. A run that finished before the signal
  still exits 0.
- Keys arrive with echo disabled; typing into a run must not echo.
- Resizing mid-run in both directions, and a terminal too small for the frame.
- The exit status of a full-screen run equals the piped one on every fixture.
- Piped output carries no escape bytes under any flag combination.

The platforms disagree about enough filesystem and terminal behavior that CI
runs the suite on both ubuntu and macOS, and cross-builds every release target.

## Things that have bitten this code

- **The full-screen view writes nothing to stdout.** It is gated on stdout
  being a terminal, and `--log` opts out. Every flag combination must leave
  piped output byte-identical to a plain run. A view that leaks an escape
  sequence into a redirected log has broken the tool's only output contract.
- **The exit status must not depend on where output goes.** A scan that
  finished before the first frame never created a screen, the teardown
  dereferenced it, and a terminal run died with Go's panic status 2 where a pipe
  reported 1. `Screen` is nil-safe on every method.
- **A deferred method call evaluates its receiver at the `defer`.**
  `defer d.Screen().Leave()` captured the screen before it existed. Wrap it:
  `defer func() { d.Screen().Leave() }()`.
- **Only one reader on `/dev/tty` at a time.** The live key watcher and the
  results view both read it; whichever blocks first takes the keystroke, and
  the cursor-position reply to DSR arrives on the same stream. The watcher is
  shut down, and waited for, before anything else reads.
- **A blocking read on a terminal cannot be interrupted** by closing the
  descriptor, so the watcher reads with termios `VMIN=0`/`VTIME`.
  `os.File.SetReadDeadline` does not work on `/dev/tty` ("file type does not
  support deadline").
- **`os.File.Read` reports a zero-byte read as `io.EOF`**, and under `VTIME`
  every timeout is a zero-byte read. Use `syscall.Read`. Treating the timeout
  as an error made the watcher exit 100ms into the run, restoring cooked mode:
  keys echoed and nothing responded.
- **Termios ioctl names differ by platform.** `TIOCGETA`/`TIOCSETA` exist only
  on the BSDs; Linux has `TCGETS`/`TCSETS`. They live in build-tagged files.
- **Writing into a terminal's last column leaves the cursor in the pending-wrap
  state**, and erase-to-end-of-line from there clears that column. Rows are
  cleared only when they are short of the width (`writeRow`); otherwise the
  right border is drawn and wiped on every row.
- **The full-screen view has no SIGWINCH handler.** It asks the terminal for
  its size every frame. A cached size drew the frame at the startup geometry
  forever.
- **Measure text by visible cells, never `len()`.** Escape sequences occupy no
  cells, and `↑` is three bytes. Use `visibleLen`, `truncVisible`,
  `padVisible`, and index titles by rune.
- **One write per frame, rows diffed against the previous frame.** A write per
  row lets the terminal paint a partial frame, which reads as flicker.
- **Panes do not share borders.** A shared segment cannot show which of two
  panes has focus.
- **Readings must not move for layout reasons.** Toggling the worker rows
  changed which statistics fit until the column was laid out against the
  band-shown height; a finished run's readings were recomputed from the wall
  clock until they were frozen at the end of the scan.
- **A filename is untrusted input**, and so is a sidecar's contents. Every path
  goes through `displayPath`; anything that is not a 64-character hex digest is
  not a sidecar and is never printed.
- **Hidden entries are pruned, not filtered**, so the walk never descends into
  `.Trashes` and friends. The named root is never pruned, however it is named.
- **Enforce a rule once.** `--max-depth` was checked by the directory prune and
  again per file; either alone was correct, so a test could not tell when one
  of them rotted.
- **Inside a formula, `Pathname#write` refuses to overwrite an existing file**
  (Homebrew's `WriteMkpathExtension`). Use `File.write` when a formula test
  deliberately corrupts a fixture.

## Working the review

CodeRabbit reviews every push on its own. Do not ask for anything after a
normal push; just wait. `@coderabbitai resume` is for restarting it when
reviews have been paused, and `full review` only for recovering after a
rate-limited attempt has marked commits as seen.

Findings are answered in the **commit message** that fixes them, or in the PR
description when they change what the PR is. A PR comment says which commit
carries the fix and nothing else. Long per-finding writeups in comments are
noise: they duplicate what the commit already records and bury the diff.

## Merge gate

**CodeRabbit is the merge gate.** A PR merges when CodeRabbit has posted an
`APPROVED` review. Never merge on `CHANGES_REQUESTED`.

```bash
gh pr view <N> --repo prog893/treecheck --json reviewDecision,reviews \
  --jq '{decision:.reviewDecision,
         reviews:[.reviews[] | {author:.author.login, state:.state}]}'
```

An approval also has to cover the branch head: a stale `APPROVED` from an
earlier commit satisfies nothing once new commits are pushed without
re-review. Compare the approving review against the head before trusting it:

```bash
export HEAD=$(gh pr view <N> --repo prog893/treecheck --json headRefOid --jq .headRefOid)
gh api "repos/prog893/treecheck/pulls/<N>/reviews?per_page=100" \
  --jq '[.[] | select(.user.login == "coderabbitai[bot]"
      and .state == "APPROVED" and .commit_id == $ENV.HEAD)] | length'
# 0 means no approval covering the current head: do not merge yet
```

(Here the login is written exactly as the REST API returns it; this check
never runs through `gh pr view`, where the bare name applies.)

Quirks that have cost real time on this repo:

- **The login is `coderabbitai[bot]`, not `coderabbitai`.** A `gh api` filter
  on the wrong string matches nothing and silently reports zero activity,
  which reads identically to a stuck review. `gh pr view --json reviews` uses
  the bare name, `gh api` uses the `[bot]` suffix. Prefer `startswith`.
- **A `COMMENTED` follow-up does not clear a `CHANGES_REQUESTED`.** Prose
  saying "no remaining issues" leaves the gate shut. Ask for an explicit
  verdict listing each finding, and watch `reviewDecision`, not the wording.
- **Pick the right command.** `review` is incremental and skips commits it has
  already seen. A rate-limited attempt still marks them seen, so after one,
  only `full review` recovers.
- **Never kick on a guess.** Compute the window first. A rate-limit comment
  states the wait relative to its `updated_at`, not `created_at`, and does not
  tick down. A review body instead reports the hourly quota, in which case the
  next slot is that review's `submitted_at` plus one hour. Wait, then kick
  **once**. Do not poll a rate-limited PR.
- **An APPROVED assessment in prose is not an approving review.** CodeRabbit
  will state "Current assessment: APPROVED" in a comment and leave
  `reviewDecision` sitting at CHANGES_REQUESTED. Asking it to submit the
  disposition does not help: it acknowledges the mismatch, runs another full
  review, and still posts no review object, because it will not submit one
  when the incremental diff has no new findings. The resolution is to dismiss
  the stale review with the reason recorded, not to keep re-kicking:

  ```bash
  # Select the blocking review specifically: every page (not just the first
  # 30), CodeRabbit only, CHANGES_REQUESTED only, latest by submitted_at.
  # Taking [0] or the first page unconditionally would target whatever review
  # happens to be there, including an APPROVED one.
  BLOCKING=$(gh api --paginate "repos/prog893/treecheck/pulls/<N>/reviews?per_page=100" \
    --jq '.[] | select(.user.login | startswith("coderabbitai"))
                | select(.state == "CHANGES_REQUESTED")
                | "\(.submitted_at) \(.id)"') || {
    echo "review lookup failed; refusing to dismiss on a guess" >&2
    exit 1
  }
  RID=$(printf '%s\n' "$BLOCKING" | sort | tail -n 1 | cut -d ' ' -f2)
  if [ -z "$RID" ]; then
    echo "no blocking CodeRabbit review to dismiss"
  else
    gh api -X PUT repos/prog893/treecheck/pulls/<N>/reviews/"$RID"/dismissals \
      -f message="Superseded by CodeRabbit's later APPROVED assessment" -f event=DISMISS
  fi
  ```

  Note that `gh api --slurp` refuses to combine with its built-in `--jq`
  (though slurped output can still be piped to an external `jq` process, at
  the cost of depending on one); the filter above runs per page and the TSV
  lines are sorted afterwards. ISO timestamps sort correctly as text.

- **Argue when it is wrong.** It verifies and withdraws findings that do not
  hold up, but only against evidence: a measurement, a reproduction, or a
  reason the suggested fix defeats the feature.

## Releasing

`var version` in `main.go` is the source of truth. The git tag and the formula
URL are copies of it, synchronized during a release. The module path carries
the major version (`/v2`); a new major version changes it.

1. Bump `version`, land the change through a PR and the merge gate above.
2. Optionally run the `release` workflow by hand (`workflow_dispatch`). It
   builds the archives and keeps them as workflow artifacts without
   publishing anything.
3. Tag `vX.Y.Z` on `main` and push the tag. The `release` workflow refuses a
   tag that does not match `version`, runs the tests, builds
   `treecheck_X.Y.Z_{darwin,linux}_{amd64,arm64}.tar.gz` with `SHA256SUMS`, and
   publishes a GitHub release with them.
4. Homebrew is updated by hand and is not touched by any workflow. Copy
   `packaging/homebrew/treecheck.rb` to `Formula/treecheck.rb` in
   `prog893/homebrew-tap` with the new tag, commit, and push. The formula only
   exists for anyone else once the tap repository has the commit.
5. `brew update && brew upgrade treecheck`, then `brew test treecheck` and
   `brew audit --formula prog893/tap/treecheck`. Both must pass clean.

The tap still serves 1.3.0, the last shell release, until step 4 is done for
2.0.0. Its `head` points at `main`, so `brew install --HEAD` breaks until then:
the 1.x formula installs `bin/treecheck`, which no longer exists.

The formula has three non-obvious requirements, all encoded in the template:

- **No `version` line.** Homebrew scans it from the tag, and declaring both is
  flagged as redundant.
- **The tag is written out literally**, not interpolated as `"v#{version}"`.
  Style autocorrect sorts `url` above `version`, at which point the
  interpolation resolves to a bare `"v"` and the clone fails.
- **It builds from source** with `depends_on "go" => :build`, so the tap needs
  no binaries of its own.

## Publishing

Commits, PR titles and bodies, and review replies all publish from the
account's own identity and read as its own words. Write findings impersonally,
by their evidence. Never reference the requester, "the user", or the
conversation that produced the change.

Do not use em dashes or en dashes anywhere, including commit messages and PR
bodies. Use a plain hyphen for ranges, and restructure sentences rather than
reaching for a dash.
