package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	gib = int64(1) << 30
	tib = int64(1) << 40
)

// fakeDisplay builds a Display with fixed state, so the layout can be rendered
// and asserted without running a scan. Every field the panes read is set here.
func fakeDisplay(jobs int) *Display {
	// Rendered, never painted: tests that exercise the key handlers reach
	// repaint, and a renderer pointed at stdout scribbles over the test output.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		panic(err)
	}
	d := NewDisplay(NewRenderer(devnull), newColors(false), jobs, 1949, 29*tib/10,
		"/Volumes/Media", "Verify only", true)
	d.start = time.Now().Add(-62 * time.Second)
	d.doneFiles.Store(171)
	d.doneBytes.Store(12 * tib / 10)
	d.nOK.Store(168)
	d.nMismatch.Store(1)
	d.nMissing.Store(2)
	d.nIOErr.Store(0)
	for i, s := range d.slots {
		if i == jobs-1 {
			continue // one idle worker, so that row is exercised too
		}
		s.active.Store(true)
		s.path.Store([]string{
			"/Volumes/Media/A008_07091214_C071.braw",
			"/Volumes/Media/A008_07091214_C072.braw",
			"/Volumes/Media/B002_0709_C003.mov",
			"/Volumes/Media/very/deeply/nested/path/that/goes/on/C004.mov",
			"/Volumes/Media/short.mov",
		}[i%5])
		s.size.Store(int64(21*gib/10) >> uint(i%3))
		s.done.Store(s.size.Load() * int64(20+i*17) / 100)
	}
	d.recent = []recentLine{
		{OutcomeOK, "ok       /Volumes/Media/A008_07091214_C068.braw"},
		{OutcomeOK, "ok       /Volumes/Media/A008_07091214_C069.braw"},
		{OutcomeMismatch, "MISMATCH /Volumes/Media/A008_07091214_C070.braw"},
		{OutcomeMismatch, "         recorded 8516299eda3b1cf414041e1e695c9338692dee6af8de11ec68000bde8ebdf0fd"},
		{OutcomeMismatch, "         now      b6f00f283e24783b68eb63deb8c6f492dfafd29a45a11fa7c2725869596f81f4"},
		{OutcomeOK, "ok       /Volumes/Media/B002_0709_C002.mov"},
	}
	d.rateHist = []int64{120, 180, 240, 310, 290, 340, 360, 355, 210, 90, 150, 280, 340, 350}
	for i := range d.rateHist {
		d.rateHist[i] *= 1 << 20
	}
	return d
}

// TestDashboardGeometry is the property that matters for a full-screen layout:
// every row is exactly the screen width and there are exactly as many rows as
// the screen has. A row one cell too wide wraps, which pushes every row below
// it down by one and corrupts the whole frame.
func TestDashboardGeometry(t *testing.T) {
	for _, size := range []struct{ rows, cols int }{
		{32, 100}, {24, 80}, {40, 120}, {24, 200}, {50, 60},
		{12, 80}, {9, 80}, {8, 40},
	} {
		for _, jobs := range []int{1, 4, 8, 16} {
			d := fakeDisplay(jobs)
			frame := d.renderDashboard(size.rows, size.cols)
			if len(frame) > size.rows {
				t.Errorf("%dx%d j=%d: %d rows, screen has %d",
					size.rows, size.cols, jobs, len(frame), size.rows)
			}
			for i, row := range frame {
				if w := visibleLen(row); w > size.cols {
					t.Errorf("%dx%d j=%d: row %d is %d cells wide, screen is %d\n%q",
						size.rows, size.cols, jobs, i, w, size.cols, row)
				}
			}
		}
	}
}

// TestDashboardDegradesRatherThanOverflows: a terminal too short for every pane
// must drop panes, and the statistics are the last thing to go because they are
// the part a reader cannot reconstruct from anywhere else.
func TestDashboardDegradesRatherThanOverflows(t *testing.T) {
	d := fakeDisplay(8)
	hasBand := func(frame []string) bool {
		for _, r := range frame {
			// The band is identified by its own pane title, not by the word
			// appearing anywhere: the stream pane's title reads "N workers"
			// too, and both rules now start the same way.
			if strings.HasPrefix(r, bTL) && strings.Contains(r, bH+" workers "+bH) {
				return true
			}
		}
		return false
	}
	if !hasBand(d.renderDashboard(40, 100)) {
		t.Error("40-row screen should show the worker band")
	}
	if hasBand(d.renderDashboard(12, 100)) {
		t.Error("12-row screen should have dropped the worker band")
	}
	short := strings.Join(d.renderDashboard(12, 100), "\n")
	for _, want := range []string{"verified", "eta", "throughput"} {
		if !strings.Contains(short, want) {
			t.Errorf("statistics lost %q when the screen got short:\n%s", want, short)
		}
	}
}

// TestPanesDoNotShareBorders is the property that makes focus legible. A
// shared border segment belongs to two panes at once, so highlighting it to
// show focus says "one of these two", which is not what focus means. Where the
// stream pane ends and the counters pane begins there must be two adjacent
// verticals, one owned by each, and the boundary must sit in the same column
// on every row of the band.
func TestPanesDoNotShareBorders(t *testing.T) {
	d := fakeDisplay(6)
	frame := d.renderDashboard(30, 100)

	seam := -1
	for _, row := range frame {
		r := []rune(row)
		var verticals []int
		for i, ch := range r {
			if string(ch) == bV {
				verticals = append(verticals, i)
			}
		}
		if len(verticals) != 4 {
			continue // not a two-pane row
		}
		// Middle two are the facing borders, and they must be adjacent.
		if verticals[2] != verticals[1]+1 {
			t.Fatalf("panes share a divider at %d/%d:\n%s",
				verticals[1], verticals[2], row)
		}
		if seam == -1 {
			seam = verticals[1]
		} else if verticals[1] != seam {
			t.Fatalf("pane boundary moved from %d to %d:\n%s", seam, verticals[1], row)
		}
	}
	if seam == -1 {
		t.Fatal("no two-pane rows found; the layout did not render")
	}
}

// TestFinishedStatsAreFrozen: every reading in the pane is derived from the
// wall clock, so recomputing them on a repaint made elapsed climb and
// throughput fall while the reader did nothing but scroll a finished run.
func TestFinishedStatsAreFrozen(t *testing.T) {
	d := fakeDisplay(4)
	d.ShowResults(results{counts: &Counters{Scanned: 10, OK: 10}, status: 0})
	first := d.snapshot()
	time.Sleep(1100 * time.Millisecond)
	second := d.snapshot()
	if first.elapsed != second.elapsed {
		t.Errorf("elapsed moved after the run finished: %d then %d",
			first.elapsed, second.elapsed)
	}
	if first.rate != second.rate || first.average != second.average {
		t.Errorf("throughput moved after the run finished: %d/%d then %d/%d",
			first.rate, first.average, second.rate, second.average)
	}
}

// TestSparklineShape pins the two cases that read wrong if the scaling is off:
// a flat series must not render at full height, and the peak must.
func TestSparklineShape(t *testing.T) {
	// Zero-based scaling: a steady series is at its own maximum throughout,
	// and a stall must visibly reach the floor rather than being rescaled away.
	if got := sparkline([]int64{5, 5, 5, 5}, 4); got != "████" {
		t.Errorf("steady series rendered %q, want full height (it is at its max)", got)
	}
	if got := sparkline([]int64{400, 400, 0, 400}, 4); !strings.Contains(got, "▁") {
		t.Errorf("a stall must reach the floor, got %q", got)
	}
	got := sparkline([]int64{0, 50, 100}, 3)
	if !strings.HasSuffix(got, "█") {
		t.Errorf("peak did not reach full height: %q", got)
	}
	if n := len([]rune(sparkline([]int64{1, 2}, 10))); n != 10 {
		t.Errorf("sparkline width = %d, want 10 (should pad)", n)
	}
	if n := len([]rune(sparkline(nil, 6))); n != 6 {
		t.Errorf("empty sparkline width = %d, want 6", n)
	}
}

// TestDashboardSnapshot prints one frame so a reviewer can see what the layout
// actually looks like. It asserts nothing beyond the geometry tests above; it
// exists because a layout is easier to judge by looking at it.
func TestDashboardSnapshot(t *testing.T) {
	d := fakeDisplay(6)
	t.Log("\n" + strings.Join(d.renderDashboard(28, 96), "\n"))
}

// TestHintsNeverTruncate: a narrow terminal must shed hints, not cut one in
// half. "[q" as the last thing on screen is worse than no hint at all.
func TestHintsNeverTruncate(t *testing.T) {
	for _, w := range []int{120, 90, 70, 54, 40, 24, 12, 6} {
		for _, done := range []bool{false, true} {
			d := fakeDisplay(4)
			d.done.Store(done)
			if done {
				d.res = results{counts: &Counters{}}
			}
			got := d.hintText(done, false, w)
			if visibleLen(got) > w && w >= 6 {
				t.Errorf("w=%d done=%v: hint is %d wide: %q",
					w, done, visibleLen(got), got)
			}
			// Quit survives every width, because it is the one key a reader
			// cannot do without.
			if !strings.Contains(got, "[q") {
				t.Errorf("w=%d done=%v: quit hint was shed: %q", w, done, got)
			}
			// Whatever survives is whole.
			for _, part := range strings.Fields(got) {
				if strings.HasPrefix(part, "[") && !strings.Contains(part, "]") {
					t.Errorf("w=%d: hint cut mid-key: %q", w, got)
				}
			}
		}
	}
}

// TestFilterResetsStreamScroll pins a confusing jump: the stream offset is
// counted from the newest line, so carrying it across a filter change pointed
// it at an unrelated place in a list of a different length. The stream
// appeared to leap, or to empty itself, with nothing on screen explaining why.
func TestFilterResetsStreamScroll(t *testing.T) {
	d := fakeDisplay(4)
	for i := 0; i < 50; i++ {
		d.recent = append(d.recent, recentLine{OutcomeOK, "ok       f" + itoa(i)})
	}
	d.focus.Store(focusStream)
	d.Scroll(-20)
	if d.streamOff.Load() == 0 {
		t.Fatal("stream did not scroll")
	}
	d.focus.Store(focusStats)
	d.Scroll(1)
	if got := d.streamOff.Load(); got != 0 {
		t.Errorf("stream offset survived a filter change: %d", got)
	}
}

// TestBandNotFocusableWhileScanning: worker rows have nothing to select and
// nothing to scroll, so offering focus for them is a stop where the keys do
// nothing.
func TestBandNotFocusableWhileScanning(t *testing.T) {
	d := fakeDisplay(4)
	if d.focusable(focusBand) {
		t.Error("worker band should not take focus during a scan")
	}
	d.done.Store(true)
	d.res = results{counts: &Counters{}}
	if d.focusable(focusBand) {
		t.Error("an empty problem band should not take focus")
	}
	d.res = results{counts: &Counters{Failures: []Failure{{Path: "a"}}}}
	if !d.focusable(focusBand) {
		t.Error("a band holding problems should take focus")
	}
	// Cycling never parks on a pane that cannot take focus.
	for i := 0; i < 8; i++ {
		d2 := fakeDisplay(4)
		for j := 0; j <= i; j++ {
			d2.CycleFocus()
		}
		if f := d2.focus.Load(); !d2.focusable(f) {
			t.Fatalf("focus landed on unfocusable pane %d", f)
		}
	}
}

// TestRowsFillTheWidth: every row of the full-screen view is exactly as wide as
// the terminal, the status row included. A row one cell short leaves the
// bottom line visibly out of step with the panes above it.
func TestRowsFillTheWidth(t *testing.T) {
	for _, cols := range []int{80, 100, 104, 150} {
		for _, done := range []bool{false, true} {
			d := fakeDisplay(4)
			if done {
				d.ShowResults(results{counts: &Counters{Scanned: 5, OK: 5}})
			}
			for i, row := range d.renderDashboard(28, cols) {
				if w := visibleLen(row); w != cols {
					t.Errorf("cols=%d done=%v row %d is %d wide: %q", cols, done, i, w, row)
				}
			}
		}
	}
}

// TestFullRowKeepsItsLastColumn pins the missing right border. A row that
// fills the width leaves the cursor on the last column in the pending-wrap
// state, and an erase-to-end-of-line written after it clears that column.
func TestFullRowKeepsItsLastColumn(t *testing.T) {
	var b bytes.Buffer
	writeRow(&b, strings.Repeat("x", 9)+"│", 10)
	if strings.Contains(b.String(), "\x1b[K") {
		t.Errorf("full-width row followed by erase-to-end-of-line: %q", b.String())
	}
	b.Reset()
	writeRow(&b, "short", 10)
	if !strings.HasSuffix(b.String(), "\x1b[K") {
		t.Errorf("short row not cleared to the end: %q", b.String())
	}
}

// TestPausedKeepsPercentage: a paused run still has a position, and the bar
// exists to carry it.
func TestPausedKeepsPercentage(t *testing.T) {
	d := fakeDisplay(4)
	d.gate = &pauseGate{}
	d.gate.toggle()
	line := d.bottomLine(100, d.snapshot(), false)
	if !strings.Contains(line, "%") || !strings.Contains(line, "PAUSED") {
		t.Errorf("paused status row must show both the percentage and PAUSED: %q", line)
	}
}

// TestEstimateIsThrottled: recomputed every frame, the estimate flickered
// between neighbouring values faster than it could be read.
func TestEstimateIsThrottled(t *testing.T) {
	d := fakeDisplay(4)
	first := d.estimate(1<<30, 1<<20)
	if got := d.estimate(2<<30, 1<<28); got != first {
		t.Errorf("estimate changed within %v: %q then %q", etaEvery, first, got)
	}
}

// TestFilterValuesAlign: every filter row puts its value in the same column,
// at every width the layout allows, including the longest label.
func TestFilterValuesAlign(t *testing.T) {
	for _, cols := range []int{64, 70, 80, 104, 150} {
		d := fakeDisplay(2)
		st := d.snapshot()
		w := capAt(cols*2/5, maxStatsWidth)
		if w < minStatsWidth {
			w = minStatsWidth
		}
		rows := d.filterRows(w-2, st)
		end := -1
		for _, r := range rows {
			n := visibleLen(strings.TrimRight(r.text, " "))
			if end == -1 {
				end = n
			} else if n != end {
				t.Errorf("cols=%d: value column moved from %d to %d: %q", cols, end, n, r.text)
			}
		}
	}
}
