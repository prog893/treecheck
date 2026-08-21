# treecheck Development Guide

A single POSIX-ish bash script at `bin/treecheck`. No build step, no
dependencies beyond `bash`, `find` and `shasum`.

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

Never trust a change that has not been run. Every fix is verified against a
fixture holding **all** outcome categories at once:

| Fixture | Expected |
|---|---|
| intact file with matching sidecar | `Verified` |
| file whose contents were changed | `Mismatched` |
| file with no sidecar | `Missing/empty` |
| file with an empty sidecar | `Missing/empty` |
| file with mode `000` | `I/O errors` |
| nested subdirectory | scanned |
| unreadable **hidden** dir (`.Trashes`) | pruned, no error |
| unreadable **non-hidden** dir | walk fails, exit 1 |

Confirm exit status every time, since it is a documented interface:

```text
0   clean
1   verification failed: a mismatch, an I/O error, or a failed walk
2   nothing corrupt, but some files have no usable sidecar
```

Status 2 is the non-strict case. Under `--strict` a missing or empty sidecar
is a failure like any other, so those runs return 1 and never 2.

Check `-h` exits 0, invalid options exit 1, and that the help text and the
README option list still agree. They have drifted apart before.

## Shell traps that have bitten this script

- `((counter++))` evaluates to the **pre-increment** value, so the first
  increment from zero returns status 1 and trips `set -e`. Use assignment
  form. This was masked for a long time because the caller ran inside an `if`,
  which suppresses errexit for the whole call, and it surfaced the moment that
  `if` was restructured.
- A `find` inside a process substitution loses its exit status, and its stderr
  is easy to discard by accident. Run the walk to a file so failure survives.
- `! -path "*/.*"` filters hidden entries out of results but does **not** stop
  `find` descending into them. Use `-name '.?*' -prune`, and note the `?`: the
  walk starts at `.`, which a bare `.*` matches, pruning the entire tree.
- Inside a formula, `Pathname#write` refuses to overwrite an existing file.
  Homebrew replaces the stdlib method via `WriteMkpathExtension`, which raises
  `"Will not overwrite #{self}"` when the path already exists
  (`Library/Homebrew/extend/pathname/write_mkpath_extension.rb`). Ruby's own
  `Pathname#write` and `File.write` both overwrite happily, so use `File.write`
  when a test deliberately corrupts a fixture.

## Merge gate

**CodeRabbit is the merge gate.** A PR merges when CodeRabbit has posted an
`APPROVED` review. Never merge on `CHANGES_REQUESTED`.

```bash
gh pr view <N> --repo prog893/treecheck --json reviewDecision,reviews \
  --jq '{decision:.reviewDecision, reviews:[.reviews[]|.state]}'
```

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
  # Select the blocking review specifically. Taking [0] unconditionally would
  # target whatever review happens to be first, including an APPROVED one.
  RID=$(gh api repos/prog893/treecheck/pulls/<N>/reviews \
    --jq '[.[] | select(.user.login | startswith("coderabbitai"))
                | select(.state == "CHANGES_REQUESTED")] | last | .id // empty')
  if [ -z "$RID" ]; then
    echo "no blocking CodeRabbit review to dismiss"
  else
    gh api -X PUT repos/prog893/treecheck/pulls/<N>/reviews/"$RID"/dismissals \
      -f message="Superseded by CodeRabbit's later APPROVED assessment" -f event=DISMISS
  fi
  ```

- **Argue when it is wrong.** It verifies and withdraws findings that do not
  hold up, but only against evidence: a measurement, a reproduction, or a
  reason the suggested fix defeats the feature.

## Releasing

Version lives in exactly one place: `VERSION` at the top of `bin/treecheck`.

1. Bump `VERSION`, land the change through a PR and the merge gate above.
2. Tag `vX.Y.Z` on `main` and push the tag.
3. In `prog893/homebrew-tap`, update the tag in `Formula/treecheck.rb`.
4. `brew update && brew upgrade treecheck`, then `brew test treecheck` and
   `brew audit --formula prog893/tap/treecheck`. Both must pass clean.

The formula has two non-obvious requirements, both already encoded in it:

- **No `version` line.** Homebrew scans it from the tag, and declaring both is
  flagged as redundant.
- **The tag is written out literally**, not interpolated as `"v#{version}"`.
  Style autocorrect sorts `url` above `version`, at which point the
  interpolation resolves to a bare `"v"` and the clone fails.

## Publishing

Commits, PR titles and bodies, and review replies all publish from the
account's own identity and read as its own words. Write findings impersonally,
by their evidence. Never reference the requester, "the user", or the
conversation that produced the change.

Do not use em dashes or en dashes anywhere, including commit messages and PR
bodies. Use a plain hyphen for ranges, and restructure sentences rather than
reaching for a dash.
