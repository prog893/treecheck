package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// verdictPaths pulls the paths off the verdict lines, in the order printed.
func verdictPaths(stdout string) []string {
	var out []string
	for _, l := range strings.Split(stdout, "\n") {
		for _, tok := range []string{TokenOK, TokenCreated, TokenMismatch,
			TokenMissing, TokenIOError, TokenSkipped} {
			p := padRight(tok, verdictWidth) + " "
			if strings.HasPrefix(l, p) {
				out = append(out, strings.TrimPrefix(l, p))
			}
		}
	}
	return out
}

// TestWalkOrderIsDeterministic: two runs over one tree must be diffable against
// each other, which means byte order and not whatever order the filesystem
// hands back. The shell implementation could only promise this for piped
// output; here the reorder buffer gives it to a terminal too.
func TestWalkOrderIsDeterministic(t *testing.T) {
	files := map[string]string{}
	var want []string
	for _, n := range []string{"zeta", "alpha", "Mike", "beta", "10", "2", "_x"} {
		files[n+".bin"] = "content-" + n
	}
	root := tree{files: files}.build(t)
	for n := range files {
		want = append(want, filepath.Join(root, n))
	}
	sort.Strings(want)

	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
	first := verdictPaths(runCLI(t, "-c", "-j", "4", root).stdout)
	if len(first) != len(want) {
		t.Fatalf("got %d verdicts, want %d", len(first), len(want))
	}
	for i := range want {
		if first[i] != want[i] {
			t.Fatalf("verdict %d = %q, want %q (byte order)", i, first[i], want[i])
		}
	}
	// Repeat under a different worker count: completion order changes, the
	// printed order must not.
	for _, j := range []string{"1", "2", "8"} {
		got := verdictPaths(runCLI(t, "-j", j, root).stdout)
		if strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Errorf("-j %s reordered the output", j)
		}
	}
}

// TestOrderHoldsWhenOneFileIsSlow forces completions to arrive out of order by
// making the first file much larger than the rest. The reorder buffer has to
// hold everything behind it rather than printing on arrival.
func TestOrderHoldsWhenOneFileIsSlow(t *testing.T) {
	files := map[string]string{"000-big.bin": strings.Repeat("x", 12<<20)}
	for i := 1; i < 40; i++ {
		files[fmt.Sprintf("%03d-small.bin", i)] = "s"
	}
	root := tree{files: files}.build(t)
	got := verdictPaths(runCLI(t, "-c", "-j", "8", root).stdout)
	if len(got) != len(files) {
		t.Fatalf("got %d verdicts, want %d", len(got), len(files))
	}
	if !sort.StringsAreSorted(got) {
		t.Errorf("verdicts are not in byte order; the large file did not hold its place")
	}
}

// TestRecapNamesTheFiles: a count on its own sends the reader back through the
// whole log to find out which file rotted, which is the one question this tool
// exists to answer.
func TestRecapNamesTheFiles(t *testing.T) {
	files := map[string]string{}
	sidecars := map[string]string{}
	for i := 0; i < 25; i++ {
		n := fmt.Sprintf("f%02d.bin", i)
		files[n] = "content"
		sidecars[n] = sha256Hex("different")
	}
	root := tree{files: files, sidecars: sidecars}.build(t)
	r := runCLI(t, "-j", "4", root)

	if got := r.counter(t, "Mismatched"); !strings.HasPrefix(got, "25") {
		t.Fatalf("Mismatched = %s, want 25", got)
	}
	named := strings.Count(r.stdout, ".bin\n") - len(verdictPaths(r.stdout))
	if named != recapCap {
		t.Errorf("recap named %d paths, want the cap of %d", named, recapCap)
	}
	if !strings.Contains(r.stdout, fmt.Sprintf("... and %d more", 25-recapCap)) {
		t.Errorf("recap does not say how many it elided:\n%s", r.stdout)
	}
}

// TestScopeFlags pins --no-recurse and --max-depth, which have been wrong
// before in the direction that matters: a volume root scan that silently
// examined only the top level and still printed a confident summary.
func TestScopeFlags(t *testing.T) {
	tr := tree{files: map[string]string{
		"top.bin":         "a",
		"one/mid.bin":     "b",
		"one/two/low.bin": "c",
	}}
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"unlimited", nil, 3},
		{"no-recurse", []string{"--no-recurse"}, 1},
		{"max-depth 1", []string{"--max-depth", "1"}, 1},
		{"max-depth 2", []string{"--max-depth", "2"}, 2},
		{"max-depth 3", []string{"--max-depth", "3"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := tr.build(t)
			r := runCLI(t, append(append([]string{"-c"}, tc.args...), root)...)
			if got := len(verdictPaths(r.stdout)); got != tc.want {
				t.Errorf("scanned %d files, want %d\n%s", got, tc.want, r.stdout)
			}
		})
	}
}

// TestExcludeDirectories pins -e as a whole-name match on directories.
//
// The near misses are the point: a FILE named cache.bin, and DIRECTORIES named
// precache and cache-old, all have to survive -e cache. Excluding by substring
// would silently skip real data, and skipping data while reporting success is
// the failure mode this tool exists to prevent.
func TestExcludeDirectories(t *testing.T) {
	root := tree{files: map[string]string{
		"keep.bin":           "a",
		"cache.bin":          "b",
		"cache/drop.bin":     "c",
		"precache/keep.bin":  "d",
		"cache-old/keep.bin": "e",
		"sub/cache/drop.bin": "f",
		"sub/keep.bin":       "g",
	}}.build(t)
	r := runCLI(t, "-c", "-e", "cache", root)

	got := verdictPaths(r.stdout)
	want := []string{
		"cache-old/keep.bin", "cache.bin", "keep.bin",
		"precache/keep.bin", "sub/keep.bin",
	}
	if len(got) != len(want) {
		t.Fatalf("scanned %d files, want %d:\n%s", len(got), len(want), r.stdout)
	}
	for i, w := range want {
		if !strings.HasSuffix(got[i], w) {
			t.Errorf("verdict %d = %q, want one ending %q", i, got[i], w)
		}
	}
	// Nested occurrences of the excluded name are excluded too, at any depth.
	for _, p := range got {
		if strings.Contains(p, string(filepath.Separator)+"cache"+string(filepath.Separator)) {
			t.Errorf("excluded directory was scanned: %s", p)
		}
	}
}

// TestSidecarsAreNotThemselvesChecked: a .sha256 file is bookkeeping, not data.
func TestSidecarsAreNotThemselvesChecked(t *testing.T) {
	root := tree{
		files: map[string]string{"a.bin": "x"},
		seed:  []string{"a.bin"},
	}.build(t)
	r := runCLI(t, root)
	if got := r.counter(t, "Scanned"); !strings.HasPrefix(got, "1 file") {
		t.Errorf("Scanned = %s, want 1 file: the sidecar must not be checked", got)
	}
}

// TestForceOverwritesOnlyWithF: creating must never silently destroy the only
// record of what a file used to hash to.
func TestForceOverwritesOnlyWithF(t *testing.T) {
	stale := sha256Hex("what it used to be")
	build := func() string {
		return tree{
			files:    map[string]string{"a.bin": "current"},
			sidecars: map[string]string{"a.bin": stale},
		}.build(t)
	}

	root := build()
	if r := runCLI(t, "-c", root); r.code != 1 {
		t.Errorf("-c over a stale sidecar: exit = %d, want 1 (a mismatch)", r.code)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.bin"+hashExt)); strings.TrimSpace(string(b)) != stale {
		t.Error("-c overwrote an existing sidecar without -f")
	}

	root = build()
	if r := runCLI(t, "-c", "-f", root); r.code != 0 {
		t.Errorf("-c -f: exit = %d, want 0", r.code)
	}
	b, _ := os.ReadFile(filepath.Join(root, "a.bin"+hashExt))
	if strings.TrimSpace(string(b)) != sha256Hex("current") {
		t.Error("-c -f did not rewrite the sidecar")
	}
}
