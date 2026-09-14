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

## Views

Two, switched with `tab` at runtime, or started in the dashboard with `--dash`.

The **log view** is the default and scrolls verdicts past a pinned status block,
using a DECSTBM scrolling region so the block is never erased. The verdict
stream is the product of this tool, and this view keeps it in the terminal's
scrollback.

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
