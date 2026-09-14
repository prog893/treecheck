package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allOutcomes is the fixture every behavioral claim is checked against: one
// file per outcome category, all present at once, so a change that fixes one
// category by breaking another cannot pass.
func allOutcomes() tree {
	return tree{
		dirs: []string{"sub", ".Trashes"},
		files: map[string]string{
			"good.bin":       "intact content",
			"bad.bin":        "original content",
			"nosidecar.bin":  "no sidecar here",
			"emptysc.bin":    "empty sidecar",
			"junksc.bin":     "junk sidecar",
			"noperm.bin":     "unreadable",
			"sub/nested.bin": "nested",
		},
		seed: []string{"good.bin", "noperm.bin", "sub/nested.bin"},
		sidecars: map[string]string{
			"bad.bin":     sha256Hex("something else") + "\n",
			"emptysc.bin": "",
			"junksc.bin":  "not-a-digest\n",
		},
		mode: map[string]os.FileMode{
			"noperm.bin": 0o000,
			".Trashes":   0o000,
		},
	}
}

// TestOutcomeCategories is the table from the project's contract: every
// category at once, each landing in exactly one counter.
func TestOutcomeCategories(t *testing.T) {
	root := allOutcomes().build(t)
	r := runCLI(t, "-j", "4", root)

	if r.code != 1 {
		t.Errorf("exit = %d, want 1 (a mismatch and an I/O error are present)", r.code)
	}
	for _, tc := range []struct{ counter, want string }{
		{"Scanned", "7 files"},
		{"Verified", "2"},      // good.bin, sub/nested.bin
		{"Mismatched", "1"},    // bad.bin
		{"Missing/empty", "3"}, // nosidecar, emptysc, junksc
		{"I/O errors", "1"},    // noperm.bin
	} {
		if got := r.counter(t, tc.counter); !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.counter, got, tc.want)
		}
	}
}

// TestVerdictTokens pins the shape of every verdict line: an outcome token in
// a fixed column, then the path, with any detail indented beneath it.
func TestVerdictTokens(t *testing.T) {
	root := allOutcomes().build(t)
	r := runCLI(t, "-j", "1", root)

	want := map[string]string{
		"good.bin":       TokenOK,
		"bad.bin":        TokenMismatch,
		"nosidecar.bin":  TokenMissing,
		"emptysc.bin":    TokenMissing,
		"junksc.bin":     TokenMissing,
		"noperm.bin":     TokenIOError,
		"sub/nested.bin": TokenOK,
	}
	for file, token := range want {
		prefix := padRight(token, verdictWidth) + " "
		found := false
		for _, l := range strings.Split(r.stdout, "\n") {
			if strings.HasPrefix(l, prefix) && strings.HasSuffix(l, file) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no %q verdict for %s in:\n%s", token, file, r.stdout)
		}
	}
}

// TestExitCodes covers the documented interface. It is a table because the
// codes are what callers script against, and a silent change to one is exactly
// the kind of regression that survives a casual reading of the output.
func TestExitCodes(t *testing.T) {
	good := tree{files: map[string]string{"a.bin": "x"}, seed: []string{"a.bin"}}
	tests := []struct {
		name string
		tr   tree
		args []string
		want int
	}{
		{"clean", good, nil, 0},
		{"mismatch", tree{
			files:    map[string]string{"a.bin": "x"},
			sidecars: map[string]string{"a.bin": sha256Hex("y")},
		}, nil, 1},
		{"no sidecar", tree{files: map[string]string{"a.bin": "x"}}, nil, 2},
		{"no sidecar, strict", tree{files: map[string]string{"a.bin": "x"}},
			[]string{"--strict"}, 1},
		{"empty sidecar", tree{
			files:    map[string]string{"a.bin": "x"},
			sidecars: map[string]string{"a.bin": ""},
		}, nil, 2},
		{"unreadable file", tree{
			files: map[string]string{"a.bin": "x"},
			seed:  []string{"a.bin"},
			mode:  map[string]os.FileMode{"a.bin": 0o000},
		}, nil, 1},
		{"create only", good, []string{"-c", "-n"}, 0},
		{"create over clean tree", tree{files: map[string]string{"a.bin": "x"}},
			[]string{"-c"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.tr.build(t)
			r := runCLI(t, append(append([]string{}, tc.args...), root)...)
			if r.code != tc.want {
				t.Errorf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					r.code, tc.want, r.stdout, r.stderr)
			}
		})
	}
}

// TestFailedWalkIsNotAnEmptyDirectory guards the failure this tool exists to
// prevent: a traversal that could not complete must never report success over
// whatever subset happened to be reachable.
func TestFailedWalkIsNotAnEmptyDirectory(t *testing.T) {
	root := tree{
		dirs:  []string{"locked"},
		files: map[string]string{"a.bin": "x"},
		seed:  []string{"a.bin"},
		mode:  map[string]os.FileMode{"locked": 0o000},
	}.build(t)
	r := runCLI(t, root)
	if r.code == 0 {
		t.Fatalf("exit = 0 over a tree that could not be fully walked:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "could not walk") {
		t.Errorf("stderr does not name the walk failure: %q", r.stderr)
	}
}

// TestHiddenDirectoryIsPruned is the other half: unreadable hidden directories
// are normal on a real volume (.Trashes, .DocumentRevisions-V100) and must be
// stepped over silently rather than turning an ordinary scan into a failure.
func TestHiddenDirectoryIsPruned(t *testing.T) {
	root := tree{
		dirs:  []string{".Trashes"},
		files: map[string]string{"a.bin": "x"},
		seed:  []string{"a.bin"},
		mode:  map[string]os.FileMode{".Trashes": 0o000},
	}.build(t)
	r := runCLI(t, root)
	if r.code != 0 {
		t.Errorf("exit = %d, want 0\nstderr: %s", r.code, r.stderr)
	}
	if r.stderr != "" {
		t.Errorf("stderr should be empty, got %q", r.stderr)
	}
}

// TestCreateOnlyNeverCountsAsVerified pins a bug that has shipped before:
// -c -n writes sidecars without reading any file back, so those files have not
// been verified and must not be counted as though they had been.
func TestCreateOnlyNeverCountsAsVerified(t *testing.T) {
	root := tree{files: map[string]string{
		"a.bin": "one", "b.bin": "two",
	}}.build(t)
	r := runCLI(t, "-c", "-n", root)

	if got := r.counter(t, "Verified"); got != "0" {
		t.Errorf("Verified = %s, want 0: nothing was read back", got)
	}
	if got := r.counter(t, "Created"); got != "2" {
		t.Errorf("Created = %s, want 2", got)
	}
	if got := r.counter(t, "Not verified"); got != "2" {
		t.Errorf("Not verified = %s, want 2", got)
	}
	if r.code != 0 {
		t.Errorf("exit = %d, want 0: nothing is corrupt", r.code)
	}
}

// TestSidecarMustBeADigest treats sidecar contents as untrusted input. Anything
// that is not 64 hex characters is unusable, and none of its bytes may reach
// the terminal: a sidecar carrying ESC could otherwise rewrite the report.
func TestSidecarMustBeADigest(t *testing.T) {
	for _, body := range []string{
		"not-a-digest",
		"\x1b[31mRED\x1b[0m",
		sha256Hex("x")[:63],
		sha256Hex("x") + "extra",
		"zz" + sha256Hex("x")[2:],
	} {
		t.Run(fmt.Sprintf("%q", body), func(t *testing.T) {
			root := tree{
				files:    map[string]string{"a.bin": "x"},
				sidecars: map[string]string{"a.bin": body},
			}.build(t)
			r := runCLI(t, root)
			if got := r.counter(t, "Missing/empty"); got != "1" {
				t.Errorf("Missing/empty = %s, want 1", got)
			}
			if r.code != 2 {
				t.Errorf("exit = %d, want 2", r.code)
			}
			if strings.Contains(r.stdout, "\x1b[31m") {
				t.Error("sidecar bytes reached the terminal as escapes")
			}
		})
	}
}

// TestControlBytesInFilenames: a filename is untrusted input too. A name that
// can erase its own MISMATCH line is precisely the silent failure this tool
// exists to prevent.
func TestControlBytesInFilenames(t *testing.T) {
	root := t.TempDir()
	name := "esc\x1b[31mRED.bin"
	if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
		t.Skipf("filesystem rejected the name: %v", err)
	}
	r := runCLI(t, "-c", root)
	if strings.Contains(r.stdout, "\x1b[31m") {
		t.Errorf("raw escape reached stdout:\n%q", r.stdout)
	}
	if !strings.Contains(r.stdout, "esc?[31mRED.bin") {
		t.Errorf("control byte not neutralized:\n%s", r.stdout)
	}
}

// TestNewlineInPathIsHandled is a capability the shell implementation refused
// outright under -j > 1, because its worker records were newline-delimited.
func TestNewlineInPathIsHandled(t *testing.T) {
	root := t.TempDir()
	name := "a\nb.bin"
	if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
		t.Skipf("filesystem rejected the name: %v", err)
	}
	if r := runCLI(t, "-c", "-j", "4", root); r.code != 0 {
		t.Errorf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if r := runCLI(t, "-j", "4", root); r.code != 0 {
		t.Errorf("verify after create: exit = %d, want 0", r.code)
	}
}

// TestStoppingIsNotFailing pins the distinction that pressing q got wrong: a
// run the operator stopped reports 130 and says so, while a run that lost
// records with nothing to account for them is a fault in this tool and reports
// 1. Both checked less than they claimed, and neither may report clean.
func TestStoppingIsNotFailing(t *testing.T) {
	stopped := &Counters{Scanned: 5, OK: 5, Unreached: 20}
	if got := stopped.ExitCode(false, true, false, false); got != 130 {
		t.Errorf("a stopped run exits %d, want 130", got)
	}
	lost := &Counters{Scanned: 5, OK: 5, Unreached: 20}
	if got := lost.ExitCode(false, false, false, false); got != 1 {
		t.Errorf("records lost with no stop to explain them exits %d, want 1", got)
	}
	clean := &Counters{Scanned: 25, OK: 25}
	if got := clean.ExitCode(false, false, false, false); got != 0 {
		t.Errorf("a complete clean run exits %d, want 0", got)
	}
	// A run that finished before the stop arrived is complete, and must not be
	// downgraded by a flag that was set after the last record came back.
	raced := &Counters{Scanned: 25, OK: 25}
	if got := raced.ExitCode(false, true, false, false); got != 130 {
		t.Errorf("stop observed exits %d, want 130", got)
	}
}
