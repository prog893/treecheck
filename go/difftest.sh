#!/bin/bash
# Differential harness: the bash implementation is the reference, the Go
# implementation is under test. Same fixture, same flags, compare stdout,
# stderr and exit status.
BASH_TC=/Users/prog893/Documents/code/treecheck/bin/treecheck
GO_TC=$1
PASS=0; FAIL=0

# Normalization, each line an intentional divergence or a timing artifact:
#   Elapsed  wall clock, legitimately differs between runs
#   workers  a parallel-only banner line
#   KEYS     the Go build adds interactive key bindings the shell one has no
#            way to offer; the block is new help text, not changed behavior
#   colors   stripped so a tty and a pipe compare equal
norm() { sed -e 's/^Elapsed:.*/Elapsed: X/' \
             -e '/parallel workers\.\.\./d' \
             -e '/^KEYS (interactive runs):/,/^$/d' \
             -e 's/\x1b\[[0-9;]*m//g'; }

mkfixture() {
  chmod -R u+rwX fxd 2>/dev/null; rm -rf fxd
  mkdir -p fxd/sub fxd/.Trashes
  printf 'intact content' > fxd/good.bin
  printf 'will change'    > fxd/bad.bin
  printf 'no sidecar'     > fxd/nosidecar.bin
  printf 'empty sc'       > fxd/emptysc.bin
  printf 'unreadable'     > fxd/noperm.bin
  printf 'junk sidecar'   > fxd/junksc.bin
  printf 'nested'         > fxd/sub/nested.bin
  "$BASH_TC" -c fxd >/dev/null 2>&1
  python3 -c "open('fxd/bad.bin','w').write('CHANGED')"
  : > fxd/emptysc.bin.sha256
  printf 'not-a-digest'  > fxd/junksc.bin.sha256
  rm -f fxd/nosidecar.bin.sha256
  chmod 000 fxd/noperm.bin fxd/.Trashes
}

run_case() {
  local name="$1"; shift
  mkfixture
  "$BASH_TC" "$@" > b.out 2> b.err; local brc=$?
  mkfixture
  "$GO_TC"   "$@" > g.out 2> g.err; local grc=$?
  local ok=1
  diff <(norm < b.out) <(norm < g.out) > d.out 2>&1 || ok=0
  [ "$brc" = "$grc" ] || ok=0
  if [ $ok -eq 1 ]; then
    PASS=$((PASS+1)); printf '  PASS  %-34s (exit %s)\n' "$name" "$brc"
  else
    FAIL=$((FAIL+1)); printf '  FAIL  %-34s (bash %s / go %s)\n' "$name" "$brc" "$grc"
    sed 's/^/          /' d.out | head -20
  fi
}

echo "=== differential: bash (reference) vs go ==="
run_case "verify -j 1"            -j 1 fxd
run_case "verify -j 4"            -j 4 fxd
run_case "verify --strict"        --strict -j 4 fxd
run_case "verify --no-recurse"    --no-recurse -j 4 fxd
run_case "verify --max-depth 2"   --max-depth 2 -j 4 fxd
run_case "verify -e sub"          -e sub -j 4 fxd
run_case "reject unknown -q"       -q -j 4 fxd
run_case "create -c -j 1"         -c -j 1 fxd
run_case "create -c -j 4"         -c -j 4 fxd
run_case "create -c -n"           -c -n -j 4 fxd
run_case "create -c -f"           -c -f -j 4 fxd

# Fresh-tree cases, no pre-seeded fixture.
mkclean() { chmod -R u+rwX ct 2>/dev/null; rm -rf ct; mkdir -p ct/a
            printf 'one' > ct/f1.bin; printf 'two' > ct/a/f2.bin; }
run_clean() {
  local name="$1"; shift
  mkclean; "$BASH_TC" "$@" > b.out 2> b.err; local brc=$?
  mkclean; "$GO_TC"   "$@" > g.out 2> g.err; local grc=$?
  local ok=1
  diff <(norm < b.out) <(norm < g.out) > d.out 2>&1 || ok=0
  [ "$brc" = "$grc" ] || ok=0
  if [ $ok -eq 1 ]; then PASS=$((PASS+1)); printf '  PASS  %-34s (exit %s)\n' "$name" "$brc"
  else FAIL=$((FAIL+1)); printf '  FAIL  %-34s (bash %s / go %s)\n' "$name" "$brc" "$grc"
       sed 's/^/          /' d.out | head -20; fi
}
run_clean "fresh tree: verify (no sidecars)"  -j 4 ct
run_clean "fresh tree: -c"                    -c -j 4 ct
run_clean "fresh tree: -c -n"                 -c -n -j 4 ct
run_clean "fresh tree: -c then verify"        -c -j 1 ct
run_clean "fresh tree: --strict"              --strict -j 4 ct
echo
echo "PASS=$PASS FAIL=$FAIL"
[ $FAIL -eq 0 ]
