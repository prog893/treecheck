# treecheck, Go draft

A rewrite of `bin/treecheck` in Go. Not yet a replacement: the shell
implementation remains the shipped one and the reference for behavior.

## Build and test

```sh
cd go && go build -o /tmp/tcgo .
./difftest.sh /tmp/tcgo
```

`difftest.sh` runs the shell implementation and this one over identical
fixtures and diffs stdout, stderr and exit status. **The shell version is the
reference.** Any difference is a bug here until it is listed as an intentional
divergence in the harness's `norm()`.

## Why a rewrite

Every temporary file in the shell version is IPC implemented on the
filesystem, because `xargs` workers are separate processes that cannot mutate
the parent's counters. Here they are goroutines sharing an address space, so
all thirteen are channels and struct fields:

| Shell | Here |
|---|---|
| `RESULTS_DIR/$idx` | a `Verdict` on a channel |
| `WORK_INPUT` | a `job` channel |
| `DONE_LOG`, `MONBUF` | `Display` counters, updated in place |
| `STREAMED_LOG`, `ALL_IDX`, `NEEDS_PRINT` | a reorder buffer keyed on walk index |
| `FILE_LIST`, `SORTED_LIST` | a sorted `[]File` |
| `ORDERED_PATHS` | `DirEntry.Info().Size()` during the walk |
| `CREATED_LOG` | an `int` |
| `FIND_ERR` | an `error` |

`is_our_tmp`, the `E2BIG` batching in `remove_tmp_dir`, and the rule against
deriving one temp name from another all go with them.

## Measured on a real volume

Sustained **1.5 GiB/s** verifying a 1.8 TiB external SSD, 558 files of mixed
BRAW, MP4 and WAV, at the default 8 workers. The shell implementation hashes
through `/usr/bin/shasum`, a Perl script whose `Digest::SHA` does not use the
ARMv8 SHA-2 instructions that Go's `crypto/sha256` does: 313 MiB/s per core
against 1974 MiB/s on the same file, same digest.

## Views

Two, switched with `tab` at runtime.

The **dashboard** is the default on a terminal, because it shows what a long run
is actually doing.

The **log view** (`--log`) scrolls verdicts past a status block, using a DECSTBM
scrolling region so the block is never erased. The verdict stream is the product
of this tool, and this view keeps it in the terminal's scrollback.

The status block does not jump to the bottom of the screen. It starts wherever
the cursor already was, found by asking the terminal (DSR), and walks down as
lines are committed, pinning to the bottom only once the screen is genuinely
full. A block that pins immediately leaves a screenful of nothing between the
header and itself on any run started near the top of an empty terminal.

The **dashboard** is a full-screen, multi-pane view for watching a long run:
the verdict stream and a statistics pane side by side, a per-worker band with
progress within each file, and a run-total bar. It draws into the alternate
screen buffer, which the terminal discards on exit, so everything it showed is
replayed to the restored screen when you leave it. Beyond 50,000 lines the
replay is dropped and says so, since a run that large wants a pipe.

```
┌─ treecheck · /Volumes/Media · Verify only · 6 workers ─┬─────────────────────────────────────┐
│                                                        │ verified                        168 │
│                                                        │ mismatched                1 corrupt │
│                                                        │ missing                           2 │
│                                                        │ io errors                         0 │
│                                                        │ files                    171 / 1949 │
│ ok       /Volumes/Media/A008_07091214_C068.braw        │ data                1.1TiB / 2.8TiB │
│ MISMATCH /Volumes/Media/A008_07091214_C070.braw        │ eta                           2h04m │
│          recorded 8516299eda3b1cf414041e1e695c9338692de│ throughput               340.5MiB/s │
│ ok       /Volumes/Media/B002_0709_C002.mov             │                      ▃▄▅▇▆▇█▇▅▂▃▆▇▇ │
├─ workers ──────────────────────────────────────────────┴─────────────────────────────────────┤
│  1 ██████▏···  61%  /Volumes/Media/A008_07091214_C071.braw                         2.1GiB    │
│  2 ██▏·······  21%  /Volumes/Media/A008_07091214_C072.braw                         4.7GiB    │
├──────────────────────────────────────────────────────────────────────────────────────────────┤
│ █████████████████████▎······················· 48%  [tab] log  [space] detail  [q] quit       │
└──────────────────────────────────────────────────────────────────────────────────────────────┘
```

Layout is tested rather than eyeballed: `TestDashboardGeometry` renders every
pane combination at eight terminal sizes and asserts no row exceeds the screen
width and no frame exceeds its height, because a row one cell too wide wraps and
shifts every row below it.

## Keys

| key | does |
|---|---|
| `tab` | move focus to the next pane that is actually drawn |
| `↑ ↓` `j k` | act on the focused pane |
| `PgUp` `PgDn` | the same, by one screenful of that pane |
| `g` `G` | jump to the ends |
| `space` | show or hide the worker rows (while scanning) |
| `p` | pause the hashing |
| `q` | stop and exit |

Three marks, one role each, so a reader never has to work out which is which:

| mark | means |
|---|---|
| highlighted border segment | this pane has the keys |
| `▸` in the row gutter | this row is where the keys are pointing |
| gutter space beside it | reserved for selection, once there is something to select |

One set of arrow keys, doing different things depending on where focus is,
rather than a key per pane. With the **stream** focused they scroll the verdict
log, so a long run can be read rather than only watched. With the **counters**
focused they pick one, which filters both the stream and the band to that
category: selecting `mismatched` narrows the problem list to the files that
matter instead of only recolouring a number. With the **band** focused they step
through workers while scanning and through problems afterwards.

The hint row names what the arrows will do right now, which is also how you can
tell where focus is.

`q` quits at any point. Stopping a scan to look at its partial results is what
letting it finish is for, and a key labelled quit that instead moves to another
screen is not one. Ctrl-C behaves the same way.

Pause exists because a long verify saturates the device it is reading, which is
a problem when that device is also the one an edit is playing back from. Paused
time is excluded from the estimate, since a run paused for ten minutes has not
slowed down; the throughput reading is the recent rate rather than the run
average, so it falls to zero when the work does.

The counters sit on the right and the stream on the left, deliberately. The
counters are a narrow column of right-aligned numbers; the stream holds long
variable-width text. Swapping them would put the ragged content against the
right edge, where it is hardest to scan, and move the numbers away from the
column the eye already expects them in.

The frame fills the terminal. What is capped is the elements inside it: a
300-column terminal is not a reason to draw a 100-cell progress bar or to push
a size column three hundred cells from the bar it belongs to.

## Reviewing the problems

`--review` holds the terminal open when the run ends and steps through
everything that failed, one at a time. `--dash` implies it. Neither does
anything unless stdout is a terminal, and neither runs after an interrupted
scan, whose problem list is partial and should not be read as complete.

```
┌─ problems · 2 mismatched · 1 io · 2 no usable sidecar ──────────────────────┐
│  MISMATCH edited.mov                                                        │
│  missing  empty.mov                                                         │
│  io-error noperm.mov                                                        │
│▸ MISMATCH rotted.mov                                                        │
├─ detail ────────────────────────────────────────────────────────────────────┤
│ prob/rotted.mov                                                             │
│                                                                             │
│ file      20 bytes   mode -rw-r--r--   2026-09-15T14:18:33+09:00            │
│ sidecar   65 bytes   2026-09-15T14:18:33+09:00                              │
│                                                                             │
│ recorded  b69ca4eef9d872f36f7dfc914eaa12a2e673b6ff305fc80f9d1e9fe18ae9df27  │
│ computed  1e029b22e70c0e4885af6c85ee5e2e670cf38504707ae0f77e5e9acac2cd7c76  │
│                                                                             │
│ the file's contents changed but its timestamp did not                       │
│ nothing rewrote this file through the filesystem                            │
│ this is what corruption looks like: restore from a known-good copy          │
└─────────────────────────────────────────────────────────────────────────────┘
```

The last three lines are the reason the screen exists. A mismatch on its own
does not say whether the data rotted or somebody edited the file, and those
call for opposite responses: restore from backup, or re-create the sidecar. The
timestamps separate them. A file whose contents changed *and* whose mtime moved
past the sidecar was rewritten, which is what an edit or a re-encode looks like.
A file whose contents changed while its mtime did not was never rewritten by
anything that updates metadata, which is the signature of decay.

It is stated as evidence and its likely reading, never as a verdict: an mtime
can be preserved deliberately (`rsync -t`, a restore from archive), so this
narrows the question rather than answering it. The comparison carries a
one-second tolerance, because sub-second skew is not evidence of anything.

## The test suite owns the contract

`go test ./...` is the primary suite. It does not consult the shell version:
it asserts the documented behavior directly, so it stays meaningful once the
shell version is gone. `difftest.sh` is the migration aid, and is expected to
be retired.

The suite is validated by mutation: each claim was checked by deliberately
breaking the behavior it describes and confirming the intended test fails.
Two mutations initially survived, and both were real findings rather than test
gaps:

- `--max-depth` was enforced twice, once by pruning directories at the limit
  and again by filtering files below it. Either alone was correct, so removing
  either changed nothing. Two checks enforcing one rule means either can rot
  unnoticed; the file-level filter is gone and the prune carries the rule.
- `-e` excluded by whole directory name, but nothing asserted it. A mutation to
  substring matching passed. The fixture now includes `cache.bin`, `precache/`
  and `cache-old/`, all of which must survive `-e cache`.

## Known gaps

- Interrupt handling is verified by driving the built binary, not from `go
  test`: the handler is process-wide, so an in-process test would have to
  signal the test runner itself.
- Sequential and parallel paths are one code path here, so there is no engine
  parity contract to maintain. The shell version's two-engine tests do not
  apply.
- `-v` reports a count of skipped files rather than naming them.
- No Homebrew formula, no release wiring.
