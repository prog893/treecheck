# treecheck

Walk a directory tree and verify every file against its SHA-256 sidecar.

Storage fails quietly. A drive that sits disconnected for months can lose charge in its NAND cells, a flaky cable can corrupt a transfer, and a bad copy can truncate a file, all without anything reporting an error. The filesystem hands you the damaged bytes exactly as confidently as it handed you the good ones. `treecheck` gives you a way to notice.

It records a `.sha256` file next to each of your files, then re-reads both later and tells you what no longer matches.

It never modifies, moves or deletes your data. The only files it writes are sidecars.

## Install

```bash
brew install prog893/tap/treecheck
```

Or run the script directly. It needs nothing beyond `bash`, `find` and `shasum`.

## Quick start

```bash
# Record hashes for everything, then verify them
treecheck -c /Volumes/Media

# Later, check whether anything has changed
treecheck /Volumes/Media
```

Recursion is the default. Pointing it at a volume means the whole volume.

## Output

```text
Mode: Verify only | Dir: /Volumes/Media | Depth: unlimited

Verifying /Volumes/Media/a001.mxf ... sidecar found, computing hash... ✓
Verifying /Volumes/Media/a002.mxf ... sidecar found, computing hash... ✓
Verifying /Volumes/Media/a003.mxf ... sidecar found, computing hash... hash mismatch! (sidecar: dd0aec17..., computed: 968cc9a4...)
Verifying /Volumes/Media/a004.mxf ... sidecar found, computing hash... unreadable!
Verifying /Volumes/Media/notes.txt ... no sidecar!

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

Files are visited in sorted order, so two completed runs over the same tree
visit files in the same order and can be diffed against each other once the
`Elapsed:` line, which is wall clock, is normalized. An interrupted run stops
wherever it got to, so its counters and recap cover only part of the tree and
do not line up against a full run. Ordering the walk needs a
`sort` that reads NUL-separated records (`sort -z`), which is not POSIX. It is
probed once at startup; where it is missing, the walk runs in filesystem order
instead and says so on stderr. Nothing else about the run changes.

`Mismatched` and `I/O errors` are each followed by the files behind them,
capped at twenty per category. Knowing that one file out of nine thousand is
corrupt is useless without knowing which one, and that answer should not
require going back through the log. `Missing/empty` gets no such list: on a
fresh tree it is every file, and the fix is `-c` rather than a name.

Control bytes in a filename are replaced with `?` for display. A name is
untrusted input, and one carrying terminal escapes could otherwise rewrite the
report about itself.

Each outcome is counted separately, because they mean very different things:

| Line | Meaning |
|---|---|
| **Verified** | The file still hashes to what was recorded |
| **Mismatched** | The contents changed. This is the number that matters |
| **Missing/empty** | No usable sidecar, either absent or empty. Run with `-c` to record one |
| **Not verified** | Create mode with `-n`: a sidecar was written but never read back |
| **I/O errors** | The file or its sidecar could not be read, or the sidecar could not be written |

For a sidecar that already existed, `Verified` means the file was read and
hashed and the result matched. For one this run just created, it means the
sidecar was read back and matched the digest written to it, which is what the
per-file line reports as `created, sidecar verified`. Creation hashes the file
once; a sidecar recorded for a file that is being written to concurrently can
still go stale, the same as one recorded a moment before the write.

`Mismatched` and `I/O errors` are deliberately distinct. A mismatch means the bytes changed. An I/O error means the drive would not hand them over, which points at the hardware rather than at the data.

## Exit status

```text
0   clean
1   verification failed: a mismatch, an I/O error, or a failed directory walk
2   nothing corrupt, but some files have no usable sidecar yet
130 interrupted: only the files reported as scanned were checked
```

Status 130 is what an interrupted run returns. Ctrl-C stops the scan rather
than abandoning it: the summary reports the counters for the files actually
reached plus a `Not reached` count, and an interrupted run never reports the
clean verdict, whatever those counters say.

Status 1 means the run could not confirm your data is intact. That covers three different situations: contents that changed, files the drive would not return, and a tree that could not be fully walked. Only the first is evidence of corruption, so read the counters rather than the exit code alone when diagnosing.

So this does what you would expect:

```bash
treecheck /Volumes/Media && echo "all good"
```

Exit code 2 keeps "something needs looking at" separate from "you added new files that need hashing", which matters when running this from cron. Pass `--strict` to treat missing sidecars as a failure too.

## Options

```text
-c              Create missing sidecars, and verify the ones that exist
-f              Overwrite existing sidecars (use with -c)
-n              Skip verification (create only; requires -c)
-e DIRS         Exclude directories, comma-separated
-j N, --jobs N  Hash across N parallel workers (default: one per CPU)
-v              Verbose (report skipped files)
--no-recurse    Only the named directory (same as --max-depth 1)
--max-depth N   Descend at most N levels
--strict        Treat missing sidecars as a failure too
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

## Progress

On an interactive terminal the parallel engine shows live progress: completed
verdicts scroll above a status line carrying the file count, how far along the
run is, bytes done out of bytes total, elapsed time, throughput, an estimate
of the time remaining, and what is currently being hashed.

```text
 / 171/1949 42% 1.2TiB/2.9TiB 11m03s 340.5MiB/s eta 2h04m hashing: A008_07091214_C071.braw
```

The percentage and the estimate are weighted by bytes, not by file count. A
media tree mixes multi-gigabyte originals with kilobyte metadata files, so
"171 of 1949 files" can mean anything between one percent and ninety-nine
percent of the actual work. Sizes come from `du`. If it is unavailable, or if
it cannot return a size for some path the walk turned up, the status line falls
back to counting files and drops the estimate rather than showing one it cannot
support. A path `du` skips because it is a second link to an inode already
counted is asked about individually first, so an ordinary hardlink does not
cost the whole run its byte weighting. Sizes and rates use binary units, because `du`
reports kibibytes: a `GiB` here is 1024 MiB, not 1000 MB.

The status line is truncated to a single terminal row, using the width
reported by `tput cols`, or 80 columns if that fails. That fallback is best
effort rather than a guarantee: a terminal narrower than 80 columns can still
wrap it. It is erased by returning to column zero and clearing to end of line,
and the row a terminal returns to is the last one, so a status line allowed to
wrap would leave its first row stranded in the scrollback for the rest of the
run.

The status line exists only on a terminal. Piped or redirected output carries
none of it: every verdict appears exactly once, in walk order, so it can go
through `grep` or into a log. The final `Elapsed` line is printed either way.

Every verdict is one line, in both engines, because control bytes in a
filename are replaced with `?` before it is printed. That is what the display
guarantees.

Separately, and for a different reason, the parallel engine refuses to hash a
path whose real name holds a newline or a `0x01` byte, and tells you to rerun
with `-j 1`. That rule is about the format workers use to report results, not
about the display: a newline would split a record and `0x01` is the escape
byte a record travels with. `-j 1` needs no such format and takes those paths
happily, printing them sanitized like any other.

## How it works

For `video.mxf`, `treecheck` writes `video.mxf.sha256` containing that file's SHA-256 digest. On a later run it re-hashes `video.mxf` and compares.

One hash per file, rather than a single hash over the whole tree, is deliberate. It means one corrupted file tells you exactly which file is corrupt instead of invalidating everything around it, and it means sidecars survive being moved alongside their data.

Hidden files and directories found during the walk are skipped at every level, and are never descended into. That matters on macOS volume roots, where `.Trashes`, `.DocumentRevisions-V100` and `.TemporaryItems` are unreadable: merely looking inside them makes the directory walk fail, which would otherwise be reported as a failed run.

The directory you name is always scanned, even if it is itself hidden, so `treecheck ~/.config` works as expected. Only hidden entries *inside* the tree are pruned.

## Things worth knowing

**Verify before you create.** If a file's sidecar is missing, `-c` hashes whatever is there now and records it as correct. Run a plain verify pass first, so you find out whether a file was already damaged before blessing its current contents.

**Sidecars live beside the data.** If a directory is lost, its sidecars go with it. This tool detects corruption, it does not protect against it. It is a smoke alarm, not a fire extinguisher, and it is no substitute for backups.

**Every run re-reads everything.** Verifying terabytes means reading terabytes, so a full pass over a large archive takes as long as reading the whole archive.

## Requirements

- `bash`
- `shasum` (standard on macOS and Linux)
- `find` with `-print0` support
- standard userland: `tr`, `sed`, `rm`, `mktemp`, `wc`
- `sort` accepting `-z`, for the ordered walk only; without it the walk keeps
  filesystem order and says so, and nothing else changes

Parallel hashing (the default on multi-core machines) additionally uses an
`xargs` built with `-P`. That flag is common but not POSIX; where it is
missing the tool says so up front and `-j 1` always works with the core set
alone - an explicit serial run needs nothing from this tier. The live
progress display runs only when stdout is a terminal and additionally uses
`tail`, `head`, `awk`, `grep`, `mv`, `sleep`, `tput` and `du`. `grep` belongs
to this tier alone. `du` supplies the byte weights behind the percentage and
the estimate; without it the display counts files instead and drops the
estimate.

## License

MIT
