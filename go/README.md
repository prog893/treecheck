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

## Known gaps

- Sequential and parallel paths are one code path here, so there is no engine
  parity contract to maintain. The shell version's two-engine tests do not
  apply.
- The default worker count is `NumCPU`, which is tuned for the shell version's
  per-file process overhead. On a tree of small files this build is measurably
  faster at `-j 8` than at `-j 24`; the default should be revisited.
- `-v` reports a count of skipped files rather than naming them.
- No Homebrew formula, no release wiring.
