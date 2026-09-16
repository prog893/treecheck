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

## Interactive view

On a terminal the run takes the screen and gives it back untouched: nothing is
written to stdout, the view stays up with the results when the scan finishes,
and `q` restores the terminal as it was. Output meant to be kept goes through a
pipe or a redirect, neither of which is a terminal, and both of which get the
same bytes the shell version prints. `--log` asks for that plain output on a
terminal too. Genuine faults, a failed walk above all, still go to stderr, which
survives the screen being restored.

```
 treecheck  /Volumes/m2-3                                       Verify only · 8 workers
┌─ log ─────────────────────────────────────────────┐┌─ filter ───────────────────────────┐
│ ok       A008_07091214_C068.braw                  ││▸  all                        171   │
│ ok       A008_07091214_C069.braw                  ││     verified                 168   │
│ MISMATCH A008_07091214_C070.braw                  ││     needs attention            3   │
│          recorded 8516299eda3b1cf414041e1e69...   ││       mismatched       1 corrupt   │
│          now      b6f00f283e24783b68eb63deb8...   ││       missing sidecar          2   │
│ ok       B002_0709_C002.mov                       ││       io errors                0   │
│                                                   │└────────────────────────────────────┘
│                                                   │┌─ run ──────────────────────────────┐
│                                                   ││   files               171 / 1949   │
│                                                   ││   data           1.1TiB / 2.8TiB   │
│                                                   ││   elapsed                  1m02s   │
│                                                   ││   eta                      2h04m   │
│                                                   ││   throughput          340.5MiB/s   │
└───────────────────────────────────────────────────┘└────────────────────────────────────┘
┌─ workers ────────────────────────────────────────────────────────────────────────────────┐
│  1 ██████▏···  61%  A008_07091214_C071.braw                                     2.1GiB   │
│  2 ██▏·······  21%  A008_07091214_C072.braw                                     4.7GiB   │
└──────────────────────────────────────────────────────────────────────────────────────────┘
 ██████████▎·············  43%   [space] hide workers  [tab] focus  [↑↓] scroll  [p] pause  [q] quit
```

When the scan finishes the frame keeps its shape. The log pane shows the
evidence for the selected file, the band lists the files that need attention,
and the bar becomes the summary. A results screen with a different shape makes
the reader re-find everything at the moment there is something to act on.

### Keys

| key | does |
|---|---|
| `tab` | move focus to the next pane that is drawn and has something to point at |
| `↑ ↓` `j k` | act on the focused pane |
| `PgUp` `PgDn` | the same, by one screenful of that pane |
| `g` `G` | jump to the ends |
| `space` | show or hide the worker rows, while scanning |
| `p` | pause the hashing, while scanning |
| `q` | stop and exit, at any point; Ctrl-C does the same |

With the **log** focused the arrows scroll it, so a long run can be read rather
than only watched. With the **filter** focused they pick a category, which
limits both the log and the band to it. With the **band** focused, once the scan
has finished, they step through the files that need attention. The hint row
names what the arrows will do right now, and sheds labels from its least useful
end on a narrow terminal rather than cutting one in half.

Three marks, one role each:

| mark | means |
|---|---|
| highlighted pane outline | this pane has the keys |
| `▸` in the row gutter | this row is where the keys are pointing |
| gutter space beside it | reserved for selection, once there is something to select |

### Layout decisions

- **Panes do not share borders.** A shared segment belongs to two panes at once,
  so highlighting it to show focus says "one of these two". The terminal window
  is the outer frame; a second one inside it would cost rows to repeat that.
- **The filter and the run's readings are separate boxes.** One is a control and
  takes focus, the other is not and does not. A terminal too short to give the
  readings room of their own gets both in one box.
- **"needs attention", not "failures" or "not verified".** A missing sidecar is
  a file nobody has checked yet, not one found wrong, and "Not verified" already
  names sidecars created under `-c -n` without a read-back, which this category
  deliberately excludes.
- **The header shows the target as an absolute path.** The view replaces the
  shell prompt, so a relative path loses what it was relative to. The path gives
  way from its front on a short row. Verdict lines keep the path as given, since
  they are the same bytes a pipe receives.
- **The frame fills the terminal; the elements inside it are capped.** A
  300-column terminal is not a reason for a 100-cell bar or a size column three
  hundred cells from its bar.
- **Readings do not move for layout reasons.** Hiding the worker rows does not
  change which readings are shown, and once the scan has finished they are
  frozen rather than recomputed from the wall clock.
- **Resize is read from the terminal on every frame**, not from a cached size.

Layout is tested rather than eyeballed: every row is exactly the terminal width,
no frame exceeds the terminal height, panes never share a divider, and the
readings of a finished run do not change over time.

### Pause and throughput

Pause exists because a long verify saturates the device it is reading, which is
a problem when that device is also the one an edit is playing back from. It
takes hold between reads, so part-way through a large file. Paused time is
excluded from the estimate. The throughput reading is the recent rate, so it
falls to zero when the work does, while the sparkline freezes rather than
filling with zeros and losing the run's history. The estimate changes at most
every two seconds.

### Evidence for each file

A mismatch on its own does not say whether the data rotted or somebody edited
the file, and those call for opposite responses: restore from backup, or
re-create the sidecar. The detail pane separates them by timestamp. A file whose
contents changed and whose mtime moved past its sidecar was rewritten, which is
what an edit or a re-encode looks like. A file whose contents changed while its
mtime did not was never rewritten by anything that updates metadata, which is
the signature of decay. An unreadable file is sorted the same way: mode `000` is
a permissions problem, while a file readable by permission that still fails to
read points at the device.

Each reading is stated as evidence and its likely interpretation, never as a
verdict, because an mtime can be preserved deliberately (`rsync -t`, a restore
from archive). The comparison carries a one-second tolerance.

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
- Saving the log from the interactive view, selecting files to retry or
  recreate, and a tree view are tracked as #8, #9 and #10.
