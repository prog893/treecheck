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

Verifying /Volumes/Media/a001.mxf ... sidecar found, computing hash.... ✓
Verifying /Volumes/Media/a002.mxf ... sidecar found, computing hash.... ✓
Verifying /Volumes/Media/notes.txt ... no sidecar!
Verifying /Volumes/Media/a003.mxf ... sidecar found, computing hash.... hash mismatch! (sidecar: dd0aec17..., computed: 968cc9a4...)
Verifying /Volumes/Media/a004.mxf ... sidecar found, computing hash.... unreadable!

Scanned:         5 files
Verified:        2
Mismatched:      1   <- corrupt
Missing/empty:   1
I/O errors:      1
ERROR: Completed with errors
```

Each outcome is counted separately, because they mean very different things:

| Line | Meaning |
|---|---|
| **Verified** | The file still hashes to what was recorded |
| **Mismatched** | The contents changed. This is the number that matters |
| **Missing/empty** | No usable sidecar, either absent or empty. Run with `-c` to record one |
| **Not verified** | Create mode with `-n`: a sidecar was written but never read back |
| **I/O errors** | The file or its sidecar could not be read, or the sidecar could not be written |

`Mismatched` and `I/O errors` are deliberately distinct. A mismatch means the bytes changed. An I/O error means the drive would not hand them over, which points at the hardware rather than at the data.

## Exit status

```text
0   clean
1   verification failed: a mismatch, an I/O error, or a failed directory walk
2   nothing corrupt, but some files have no usable sidecar yet
```

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
-v              Verbose (report skipped files)
-r, --recursive Accepted for compatibility; recursion is already the default
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

## How it works

For `video.mxf`, `treecheck` writes `video.mxf.sha256` containing that file's SHA-256 digest. On a later run it re-hashes `video.mxf` and compares.

One hash per file, rather than a single hash over the whole tree, is deliberate. It means one corrupted file tells you exactly which file is corrupt instead of invalidating everything around it, and it means sidecars survive being moved alongside their data.

Hidden files and directories are skipped, along with the usual macOS metadata (`.Spotlight-V100`, `.fseventsd`, `.Trashes` and friends).

## Things worth knowing

**Verify before you create.** If a file's sidecar is missing, `-c` hashes whatever is there now and records it as correct. Run a plain verify pass first, so you find out whether a file was already damaged before blessing its current contents.

**Sidecars live beside the data.** If a directory is lost, its sidecars go with it. This tool detects corruption, it does not protect against it. It is a smoke alarm, not a fire extinguisher, and it is no substitute for backups.

**Every run re-reads everything.** Verifying terabytes means reading terabytes, so a full pass over a large archive takes as long as reading the whole archive.

## Requirements

- `bash`
- `shasum` (standard on macOS and Linux)
- `find` with `-print0` support

## License

MIT
