# treecheck

Walk a directory tree and verify every file against its SHA-256 sidecar.

Storage fails quietly. A drive that sits disconnected for months can lose charge in its NAND cells, a flaky cable can corrupt a transfer, and a bad copy can truncate a file, all without anything reporting an error. The filesystem hands you the damaged bytes exactly as confidently as it handed you the good ones. `treecheck` gives you a way to notice.

It records a `.sha256` file next to each of your files, then re-reads both later and tells you what no longer matches.

It never modifies, moves or deletes your data. The only files it writes are sidecars.

## Install

```bash
brew install prog893/tap/treecheck
```

Or with Go 1.22 or later:

```bash
go install github.com/prog893/treecheck/v2@latest
```

Prebuilt archives for macOS and Linux (amd64 and arm64) are attached to each [release](https://github.com/prog893/treecheck/releases). It is a single static binary with no runtime dependencies.

## Quick start

```bash
# Record hashes for everything, then verify them
treecheck -c /Volumes/Media

# Later, check whether anything has changed
treecheck /Volumes/Media
```

Recursion is the default. Pointing it at a volume means the whole volume.

## On a terminal

A run in a terminal takes over the screen, shows the scan as it happens, and stays up with the results when it finishes. `q` exits and hands the terminal back exactly as it was, with nothing left in the scrollback. Output meant to be kept goes through a pipe or a redirect (see [Output](#output)), and `--log` asks for that plain output in a terminal too.

```
 treecheck  /Volumes/Media                                      Verify only · 8 workers
┌─ log ─────────────────────────────────────────────┐┌─ filter ───────────────────────────┐
│ ok       A008_07091214_C068.braw                  ││▸  all                        171   │
│ ok       A008_07091214_C069.braw                  ││     verified                 168   │
│ MISMATCH A008_07091214_C070.braw                  ││     needs attention            3   │
│          recorded 8516299eda3b1cf414041e1e69...   ││       mismatched       1 corrupt   │
│          now      b6f00f283e24783b68eb63deb8...   ││       missing sidecar          2   │
│ ok       B002_0709_C002.mov                       ││       io errors                0   │
│                                                   │└────────────────────────────────────┘
│                                                   │┌─ stats ────────────────────────────┐
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

Each worker row shows progress within its file, so a 30 GiB original shows a moving bar rather than sitting still for minutes. The percentage, the estimate and the throughput are weighted by bytes, since "171 of 1949 files" on a tree mixing multi-gigabyte originals with kilobyte metadata can mean anything between one and ninety-nine percent of the work.

When the scan finishes the screen keeps its shape. The outcome joins the header, the band lists the files that need attention, and the left pane shows the evidence for the selected one.

### Keys

| key | does |
|---|---|
| `tab` | move focus between the log, the filter and the file list |
| `↑ ↓` `j k` | scroll or select in the focused pane |
| `PgUp` `PgDn` | the same, by one screenful of that pane |
| `g` `G` | jump to the ends |
| `space` | show or hide the worker rows, while scanning |
| `p` | pause or resume the hashing, while scanning |
| `q` | stop and exit, at any point; Ctrl-C does the same |

The focused pane has a highlighted outline, and `▸` marks the row the keys point at. Picking a category in the filter limits both the log and the file list to it, so selecting `mismatched` shows only the files that matter. The hint row names what the arrows will do right now.

Pause exists because a long verify saturates the device it is reading, which is a problem when that device is also the one an edit is playing back from. It takes effect part-way through a file. Paused time is left out of the estimate, and the throughput reading falls to zero while paused.

### Evidence for each file

A mismatch on its own does not say whether the data decayed or somebody edited the file, and those call for opposite responses: restore from a backup, or record a new sidecar. The detail pane separates them by timestamp:

- **Contents changed and the file's mtime moved past its sidecar's.** The file was rewritten, which is what an edit, a re-encode or a restore looks like. If the change was intended, re-create the sidecar with `-c -f`.
- **Contents changed and the mtime did not.** Nothing rewrote the file through the filesystem. That is what corruption looks like.

An unreadable file is sorted the same way: mode `000` is a permissions problem, while a file that is readable by permission and still fails to read points at the device. Each reading is stated as evidence and its likely meaning, never as a verdict, because an mtime can be preserved on purpose (`rsync -t`, a restore from an archive).

## Output

Piped or redirected, `treecheck` prints plain text: one line per file, then a summary.

```text
Mode: Verify only | Dir: /Volumes/Media | Depth: unlimited

ok       /Volumes/Media/a001.mxf
ok       /Volumes/Media/a002.mxf
MISMATCH /Volumes/Media/a003.mxf
         recorded dd0aec17e0d1b0a4bb4a06e6d8f2c1907c5b3a44de91f0c2ab7e5d63f8091b2c
         now      968cc9a41f7b2e05c3d8a96b40e17d2fa5c8b31e9047d6ca2b8f3e05179ad4b6
io-error /Volumes/Media/a004.mxf
         file could not be read
missing  /Volumes/Media/notes.txt

Scanned:         5 files
Verified:        2
Mismatched:      1   <- corrupt
  /Volumes/Media/a003.mxf
Missing/empty:   1
I/O errors:      1
  /Volumes/Media/a004.mxf
Elapsed:         4m12s
ERROR: Completed with errors
```

Files are listed in sorted byte order however many workers run, so two completed runs over the same tree can be diffed once the `Elapsed:` line is normalized. An interrupted run stops wherever it got to, so its counters cover only part of the tree.

Each line is an outcome in a fixed column, then the path. The tokens are `ok`, `created`, `MISMATCH`, `missing`, `io-error` and `skipped`, so a run can be read down that column or filtered with `grep '^MISMATCH'`. Details, such as the two hashes behind a mismatch or the reason for an I/O error, go on indented lines underneath.

`Mismatched` and `I/O errors` are each followed by the files behind them, capped at twenty per category. `Missing/empty` gets no such list: on a fresh tree it is every file, and the fix is `-c` rather than a name.

Control bytes in a filename are replaced with `?` for display. A name is untrusted input, and one carrying terminal escapes could otherwise rewrite the report about itself. The same goes for a sidecar's contents: anything that is not a 64-character hex digest is treated as no sidecar, and none of its bytes are printed.

Each outcome is counted separately, because they mean very different things:

| Line | Meaning |
|---|---|
| **Verified** | The file still hashes to what was recorded |
| **Mismatched** | The contents changed. This is the number that matters |
| **Missing/empty** | No usable sidecar: absent, empty, or not holding a SHA-256 digest. Run with `-c` to record one |
| **Not verified** | Create mode with `-n`: a sidecar was written but never read back |
| **I/O errors** | The file or its sidecar could not be read, or the sidecar could not be written |

For a sidecar that already existed, `Verified` means the file was read and hashed and the result matched, and the line reads `ok`. For one this run just created, it means the sidecar was read back and matched the digest written to it, and the line reads `created`. Creation reads each file once.

`Mismatched` and `I/O errors` are deliberately distinct. A mismatch means the bytes changed. An I/O error means the drive would not hand them over, which points at the hardware rather than at the data.

## Exit status

```text
0   clean
1   verification failed: a mismatch, an I/O error, or a failed directory walk
2   nothing corrupt, but some files have no usable sidecar yet
130 interrupted: only the files reported as scanned were checked
```

The exit status is the same whether or not the output is a terminal.

Ctrl-C (or `q`) stops the scan rather than abandoning it, and an interrupted run never reports the clean verdict. A `Not reached` line accounts for the files the run never got to.

Status 1 means the run could not confirm your data is intact. That covers three different situations: contents that changed, files the drive would not return, and a tree that could not be fully walked. Only the first is evidence of corruption, so read the counters rather than the exit code alone when diagnosing.

```bash
treecheck /Volumes/Media > /dev/null && echo "all good"
```

Exit code 2 keeps "something needs looking at" separate from "you added new files that need hashing", which matters when running this from cron. Pass `--strict` to treat missing sidecars as a failure too.

## Options

```text
-c              Create missing sidecars, and verify the ones that exist
-f              Overwrite existing sidecars (use with -c)
-n              Skip verification (create only; requires -c)
-e DIRS         Exclude directories, comma-separated
-j N, --jobs N  Hash across N parallel workers (default: one per CPU, up to 8)
-v              Verbose (report how many files were skipped)
--no-recurse    Only the named directory (same as --max-depth 1)
--max-depth N   Descend at most N levels
--strict        Treat missing sidecars as a failure too
--log           Print verdicts to stdout instead of taking over the screen
--review        With --log, show the results view when the run ends
--no-review     Never show the results view
-V, --version   Print version and exit
-h              Show this help
```

Mode combinations:

| Flags | Behavior |
|---|---|
| none | Verify only |
| `-c` | Create missing sidecars and verify existing ones |
| `-c -n` | Create only, skip verification |
| `-n` alone | Nothing to do, exits with a message |

`-e` excludes directories by whole name at any depth: `-e cache` skips `cache/` and `a/cache/`, but not `precache/` or a file named `cache.bin`.

The default of at most eight workers is measured rather than guessed. One worker hashes at roughly 2 GiB/s, so eight already outpace most single devices, and more only contend for the disk; on a tree of small files the core count was 80% slower than eight on a 24-core machine. `-j` overrides it for devices that genuinely want more in flight.

## Performance

Hashing uses Go's `crypto/sha256`, which uses the CPU's SHA-2 instructions where present, and runs in-process with no per-file process start. Measured on an Apple M2 Ultra:

| Setup | Throughput |
|---|---|
| single core, one 2 GiB file | about 1.9 GiB/s |
| NVMe over a dedicated Thunderbolt 4 link, PCIe Gen 4 | peaks of 3.1 GiB/s |
| four NVMe drives sharing one enclosure's link | 1.5 GiB/s sustained, 1.8 TiB of mixed BRAW, MP4 and WAV |

On most hardware the device, not the hashing, sets the pace.

## How it works

For `video.mxf`, `treecheck` writes `video.mxf.sha256` containing that file's SHA-256 digest. On a later run it re-hashes `video.mxf` and compares.

One hash per file, rather than a single hash over the whole tree, is deliberate. It means one corrupted file tells you exactly which file is corrupt instead of invalidating everything around it, and it means sidecars survive being moved alongside their data.

Hidden files and directories found during the walk are skipped at every level, and are never descended into. That matters on macOS volume roots, where `.Trashes`, `.DocumentRevisions-V100` and `.TemporaryItems` are unreadable: merely looking inside them would make the walk fail, which would otherwise be reported as a failed run. A directory that is not hidden and cannot be read does fail the run, since the files under it were never checked.

The directory you name is always scanned, even if it is itself hidden, so `treecheck ~/.config` works as expected.

## Things worth knowing

**Verify before you create.** If a file's sidecar is missing, `-c` hashes whatever is there now and records it as correct. Run a plain verify pass first, so you find out whether a file was already damaged before blessing its current contents.

**Sidecars live beside the data.** If a directory is lost, its sidecars go with it. This tool detects corruption, it does not protect against it. It is a smoke alarm, not a fire extinguisher, and it is no substitute for backups.

**Every run re-reads everything.** Verifying terabytes means reading terabytes, so a full pass over a large archive takes as long as reading the whole archive.

## Development

```bash
go test ./...
go test -race ./...
go test -run TestGoldenOutput -update .   # after an intentional output change
```

The golden files under `testdata/` pin the complete non-interactive output, stderr and exit status for every mode and error path. They carry forward the interface of the 1.x shell implementation, against which they were checked, so a diff there is an interface change and is reviewed as one.

## License

MIT
