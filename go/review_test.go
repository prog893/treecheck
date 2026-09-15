package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fakeReview(n int) *review {
	fs := make([]Failure, 0, n)
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			fs = append(fs, Failure{
				Path: "/Volumes/Media/A00" + itoa(i) + ".braw", Token: TokenMismatch,
				Outcome: OutcomeMismatch, Size: 2 << 30,
				Recorded: strings.Repeat("a", 64), Computed: strings.Repeat("b", 64),
			})
		case 1:
			fs = append(fs, Failure{
				Path: "/Volumes/Media/B00" + itoa(i) + ".mov", Token: TokenIOError,
				Outcome: OutcomeIOError, Err: os.ErrPermission,
			})
		default:
			fs = append(fs, Failure{
				Path: "/Volumes/Media/C00" + itoa(i) + ".mxf", Token: TokenMissing,
				Outcome: OutcomeMissing,
			})
		}
	}
	return &review{
		failures: fs, c: newColors(false), out: os.Stdout, root: "/Volumes/Media",
		sc: NewScreen(os.Stdout, 30, 100), cache: map[int]forensics{},
	}
}

// TestReviewGeometry is the same property the dashboard has to hold: every row
// exactly fits the screen and the frame never exceeds its height. A row one
// cell too wide wraps and shifts everything below it.
func TestReviewGeometry(t *testing.T) {
	for _, size := range []struct{ rows, cols int }{
		{30, 100}, {24, 80}, {50, 200}, {14, 60}, {10, 40}, {9, 100}, {40, 41},
	} {
		for _, n := range []int{1, 5, 200} {
			r := fakeReview(n)
			r.sc = NewScreen(os.Stdout, size.rows, size.cols)
			r.sel = n - 1
			frame := r.render()
			if len(frame) > size.rows {
				t.Errorf("%dx%d n=%d: %d rows, screen has %d",
					size.rows, size.cols, n, len(frame), size.rows)
			}
			for i, row := range frame {
				if w := visibleLen(row); w > size.cols {
					t.Errorf("%dx%d n=%d: row %d is %d wide, screen is %d\n%q",
						size.rows, size.cols, n, i, w, size.cols, row)
				}
			}
		}
	}
}

// TestReviewKeepsSelectionVisible: with more problems than rows, the selected
// row has to stay on screen or the keys move something the reader cannot see.
func TestReviewKeepsSelectionVisible(t *testing.T) {
	r := fakeReview(200)
	for _, sel := range []int{0, 1, 50, 150, 199} {
		r.sel = sel
		frame := r.render()
		found := false
		for _, row := range frame {
			if strings.Contains(row, "▸") {
				found = true
			}
		}
		if !found {
			t.Errorf("selection %d is not on screen", sel)
		}
	}
}

// TestAssessSeparatesEditFromCorruption is the judgement this screen exists to
// make. A mismatch alone does not say whether to restore from backup or to
// re-create the sidecar, and the timestamps are what separate the two.
func TestAssessSeparatesEditFromCorruption(t *testing.T) {
	base := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	f := Failure{Outcome: OutcomeMismatch}

	edited := assess(f, forensics{fileOK: true, sideOK: true,
		sideMod: base, fileMod: base.Add(2 * time.Hour)})
	if !strings.Contains(strings.Join(edited, " "), "edit") {
		t.Errorf("a file rewritten after its sidecar should read as an edit: %v", edited)
	}

	rotted := assess(f, forensics{fileOK: true, sideOK: true,
		sideMod: base, fileMod: base})
	if !strings.Contains(strings.Join(rotted, " "), "corruption") {
		t.Errorf("unchanged timestamp with changed contents should read as corruption: %v", rotted)
	}

	// Sub-second skew is not evidence of anything, and must not be reported
	// as "0s newer".
	skew := assess(f, forensics{fileOK: true, sideOK: true,
		sideMod: base.Add(300 * time.Millisecond), fileMod: base})
	joined := strings.Join(skew, " ")
	if strings.Contains(joined, "0s") {
		t.Errorf("sub-second skew reported as a duration: %v", skew)
	}
	if !strings.Contains(joined, "corruption") {
		t.Errorf("sub-second skew should read as the unchanged-timestamp case: %v", skew)
	}
}

// TestAssessNamesTheRemedy: every category has to end with what to do, because
// a screen that only restates the problem sends the reader back to the docs.
func TestAssessNamesTheRemedy(t *testing.T) {
	base := time.Now()
	cases := []struct {
		name string
		f    Failure
		fo   forensics
		want string
	}{
		{"no sidecar", Failure{Outcome: OutcomeMissing}, forensics{fileOK: true}, "-c"},
		{"empty sidecar", Failure{Outcome: OutcomeMissing},
			forensics{fileOK: true, sideOK: true, sideSize: 0}, "-c -f"},
		{"junk sidecar", Failure{Outcome: OutcomeMissing},
			forensics{fileOK: true, sideOK: true, sideSize: 12}, "-c -f"},
		{"edited", Failure{Outcome: OutcomeMismatch},
			forensics{fileOK: true, sideOK: true, sideMod: base, fileMod: base.Add(time.Hour)}, "-c -f"},
		{"corrupt", Failure{Outcome: OutcomeMismatch},
			forensics{fileOK: true, sideOK: true, sideMod: base, fileMod: base}, "restore"},
		{"unreadable", Failure{Outcome: OutcomeIOError},
			forensics{fileOK: true, fileMode: 0o000}, "permissions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(assess(tc.f, tc.fo), " ")
			if got == "" {
				t.Fatal("no assessment at all")
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("assessment does not mention %q: %s", tc.want, got)
			}
		})
	}
}

// TestDecodeKey covers the escape sequences, which arrive as several bytes and
// are the easy half to get wrong.
func TestDecodeKey(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want key
	}{
		{[]byte("\x1b[A"), keyUp},
		{[]byte("\x1b[B"), keyDown},
		{[]byte("\x1b[H"), keyHome},
		{[]byte("\x1b[F"), keyEnd},
		{[]byte("k"), keyUp},
		{[]byte("j"), keyDown},
		{[]byte("g"), keyHome},
		{[]byte("G"), keyEnd},
		{[]byte("q"), keyQuit},
		{[]byte("\x1b"), keyQuit},
		{[]byte("z"), keyNone},
	} {
		if got := decodeKey(tc.in); got != tc.want {
			t.Errorf("decodeKey(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
