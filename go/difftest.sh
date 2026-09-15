#!/usr/bin/env bash
# Differential harness: the shell implementation is the reference, the Go build
# is under test. Same fixtures, same flags, compare stdout, stderr and exit
# status.
#
# Usage: ./difftest.sh /path/to/go-binary
#
# Any difference is a bug in the Go build until it is listed as an intentional
# divergence in norm() below, with the reason.
set -u

GO_TC=${1:-}
if [ -z "$GO_TC" ] || [ ! -x "$GO_TC" ]; then
    echo "usage: $0 <path-to-go-binary>" >&2
    exit 2
fi
case "$GO_TC" in /*) ;; *) GO_TC="$PWD/$GO_TC" ;; esac

# Resolved from this script's own location, not from the caller's directory, so
# the harness runs from anywhere and in CI as well as by hand.
HERE=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
BASH_TC="$HERE/../bin/treecheck"
if [ ! -x "$BASH_TC" ]; then
    echo "reference implementation not found at $BASH_TC" >&2
    exit 2
fi

# The reference implementation is a shell script with its own dependencies, so
# a missing one is reported here rather than surfacing later as a diff nobody
# can explain.
missing=""
for tool in shasum find sort xargs mktemp sed diff; do
    command -v "$tool" > /dev/null 2>&1 || missing="$missing $tool"
done
if [ -n "$missing" ]; then
    echo "the reference implementation needs these, and they are not here:$missing" >&2
    exit 2
fi

# Everything happens in a scratch directory. Fixtures include unreadable
# entries, so the cleanup restores permissions before removing them, or the
# harness leaves its own litter behind.
WORK=$(mktemp -d "${TMPDIR:-/tmp}/treecheck-difftest.XXXXXX") || exit 2
cleanup() { chmod -R u+rwX "$WORK" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT
cd "$WORK" || exit 2

PASS=0
FAIL=0

# Normalization, each line an intentional divergence or a timing artifact:
#   Elapsed  wall clock, legitimately differs between runs
#   workers  a parallel-only banner line
#   KEYS     the Go build adds interactive key bindings the shell one has no
#            way to offer; the block is new help text, not changed behavior
#   Go-only flags
#            --dash, --review and --no-review drive views the shell build cannot
#            render, so their help lines have no counterpart to differ from.
#            Listed one by one rather than matched by pattern, so adding a flag
#            to the Go build is a deliberate act here too.
#   version  the two builds report different versions, by design
#   colors   stripped from BOTH streams. The shell build emits colour escapes
#            whether or not its output is a terminal, so a redirected log picks
#            up escape sequences; the Go build suppresses them off a terminal
#            and honours NO_COLOR. That is a deliberate improvement, not a
#            regression, so the comparison is made on the text.
norm() {
    sed -e 's/^Elapsed:.*/Elapsed: X/' \
        -e '/parallel workers\.\.\./d' \
        -e '/^KEYS (interactive runs):/,/^$/d' \
        -e '/^  --dash /d' \
        -e '/^  --review /d' \
        -e '/^  --no-review /d' \
        -e 's/^treecheck [0-9].*/treecheck VERSION/' \
        -e 's/\x1b\[[0-9;]*m//g'
}

mkfixture() {
    chmod -R u+rwX fxd 2>/dev/null
    rm -rf fxd
    mkdir -p fxd/sub fxd/.Trashes
    printf 'intact content' > fxd/good.bin
    printf 'will change'    > fxd/bad.bin
    printf 'no sidecar'     > fxd/nosidecar.bin
    printf 'empty sc'       > fxd/emptysc.bin
    printf 'junk sidecar'   > fxd/junksc.bin
    printf 'unreadable'     > fxd/noperm.bin
    printf 'nested'         > fxd/sub/nested.bin
    "$BASH_TC" -c fxd > /dev/null 2>&1
    printf 'CHANGED'       > fxd/bad.bin
    : > fxd/emptysc.bin.sha256
    printf 'not-a-digest'  > fxd/junksc.bin.sha256
    rm -f fxd/nosidecar.bin.sha256
    chmod 000 fxd/noperm.bin fxd/.Trashes
}

mkclean() {
    chmod -R u+rwX ct 2>/dev/null
    rm -rf ct
    mkdir -p ct/a
    printf 'one' > ct/f1.bin
    printf 'two' > ct/a/f2.bin
}

# compare <name> <fixture-builder> <args...>
compare() {
    local name="$1" build="$2"
    shift 2
    local brc grc ok=1

    "$build"
    "$BASH_TC" "$@" > b.out 2> b.err; brc=$?
    "$build"
    "$GO_TC" "$@" > g.out 2> g.err; grc=$?

    # Both streams, always. Comparing only stdout once hid a parallel run
    # that printed its summary and then exited non-zero with no banner at all,
    # for as long as that bug existed.
    norm < b.out > b.norm; norm < g.out > g.norm
    norm < b.err > b.enorm; norm < g.err > g.enorm
    diff b.norm g.norm > d.out 2>&1 || ok=0
    diff b.enorm g.enorm >> d.out 2>&1 || ok=0
    [ "$brc" = "$grc" ] || ok=0

    if [ "$ok" -eq 1 ]; then
        PASS=$((PASS + 1))
        printf '  PASS  %-34s (exit %s)\n' "$name" "$brc"
    else
        FAIL=$((FAIL + 1))
        printf '  FAIL  %-34s (bash %s / go %s)\n' "$name" "$brc" "$grc"
        sed 's/^/          /' d.out | head -20
    fi
}

echo "=== differential: bash (reference) vs go ==="
compare "verify -j 1"          mkfixture -j 1 fxd
compare "verify -j 4"          mkfixture -j 4 fxd
compare "verify --strict"      mkfixture --strict -j 4 fxd
compare "verify --no-recurse"  mkfixture --no-recurse -j 4 fxd
compare "verify --max-depth 2" mkfixture --max-depth 2 -j 4 fxd
compare "verify -e sub"        mkfixture -e sub -j 4 fxd
compare "reject unknown -q"    mkfixture -q -j 4 fxd
compare "create -c -j 1"       mkfixture -c -j 1 fxd
compare "create -c -j 4"       mkfixture -c -j 4 fxd
compare "create -c -n"         mkfixture -c -n -j 4 fxd
compare "create -c -f"         mkfixture -c -f -j 4 fxd
compare "fresh: verify"        mkclean -j 4 ct
compare "fresh: -c"            mkclean -c -j 4 ct
compare "fresh: -c -n"         mkclean -c -n -j 4 ct
compare "fresh: -c -j 1"       mkclean -c -j 1 ct
compare "fresh: --strict"      mkclean --strict -j 4 ct

# Error paths. These exercise stderr specifically, which is why they are worth
# having: the exit status alone cannot tell a wrong message from a right one,
# and a run that fails silently is indistinguishable from one that failed loudly
# if only stdout is compared.
compare "reject --bogus"       mkclean --bogus ct
compare "-n without -c"        mkclean -n ct
compare "no directory"         mkclean
compare "directory not found"  mkclean /treecheck/definitely/not/here
compare "-j 0"                 mkclean -j 0 ct
compare "-j not a number"      mkclean -j abc ct
compare "-h exits 0"           mkclean -h
compare "--version"            mkclean --version

echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
