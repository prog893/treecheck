package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files from the current output")

// goldenCase is one invocation whose complete, non-interactive output is
// pinned byte for byte: stdout, stderr and the exit status.
//
// They were recorded from output checked against the 1.x shell implementation
// on the same fixtures, so they carry its interface forward. Scripts and logs
// depend on them: a change to any of them is a change to the interface, made
// on purpose with -update and reviewed as such.
type goldenCase struct {
	name string
	tree func() tree
	args []string // "ROOT" is replaced by the fixture's path
}

func freshTree() tree {
	return tree{files: map[string]string{"f1.bin": "one", "a/f2.bin": "two"}}
}

var goldenCases = []goldenCase{
	{"verify_j1", allOutcomes, []string{"-j", "1", "ROOT"}},
	{"verify_j4", allOutcomes, []string{"-j", "4", "ROOT"}},
	{"verify_strict", allOutcomes, []string{"--strict", "-j", "4", "ROOT"}},
	{"verify_no_recurse", allOutcomes, []string{"--no-recurse", "-j", "4", "ROOT"}},
	{"verify_exclude", allOutcomes, []string{"-e", "sub", "-j", "4", "ROOT"}},
	{"create_j4", allOutcomes, []string{"-c", "-j", "4", "ROOT"}},
	{"create_force", allOutcomes, []string{"-c", "-f", "-j", "4", "ROOT"}},
	{"create_only", allOutcomes, []string{"-c", "-n", "-j", "4", "ROOT"}},
	{"fresh_verify", freshTree, []string{"-j", "1", "ROOT"}},
	{"fresh_create", freshTree, []string{"-c", "-j", "1", "ROOT"}},
	{"fresh_create_only", freshTree, []string{"-c", "-n", "-j", "1", "ROOT"}},
	{"fresh_strict", freshTree, []string{"--strict", "-j", "1", "ROOT"}},
	{"help", nil, []string{"-h"}},
	{"version", nil, []string{"--version"}},
	{"unknown_short", freshTree, []string{"-q", "ROOT"}},
	{"unknown_long", freshTree, []string{"--bogus", "ROOT"}},
	{"no_directory", nil, nil},
	{"directory_not_found", nil, []string{"/treecheck-golden/definitely/missing"}},
	{"jobs_zero", freshTree, []string{"-j", "0", "ROOT"}},
	{"jobs_not_a_number", freshTree, []string{"-j", "abc", "ROOT"}},
	{"no_verify_without_create", freshTree, []string{"-n", "ROOT"}},
}

var elapsedLine = regexp.MustCompile(`(?m)^Elapsed:.*$`)

// normalizeGolden removes what legitimately differs between runs: the fixture's
// temporary path and the wall clock.
func normalizeGolden(s, root string) string {
	if root != "" {
		s = strings.ReplaceAll(s, root, "ROOT")
	}
	return elapsedLine.ReplaceAllString(s, "Elapsed:         X")
}

func renderGolden(r result, root string) string {
	return fmt.Sprintf("exit: %d\n--- stdout ---\n%s--- stderr ---\n%s",
		r.code, normalizeGolden(r.stdout, root), normalizeGolden(r.stderr, root))
}

func TestGoldenOutput(t *testing.T) {
	for _, gc := range goldenCases {
		t.Run(gc.name, func(t *testing.T) {
			root := ""
			if gc.tree != nil {
				root = gc.tree().build(t)
			}
			args := make([]string, len(gc.args))
			for i, a := range gc.args {
				if a == "ROOT" {
					a = root
				}
				args[i] = a
			}
			got := renderGolden(runCLI(t, args...), root)

			path := filepath.Join("testdata", gc.name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden file (run with -update to create it): %v", err)
			}
			if got != string(want) {
				t.Errorf("output changed from %s\n--- want ---\n%s\n--- got ---\n%s",
					path, want, got)
			}
		})
	}
}
