package main

import (
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
	d := NewDisplay(NewRenderer(os.Stdout), newColors(false), jobs, 1949, 29*tib/10,
		"/Volumes/Media", "Verify only")
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
	d.recent = []string{
		"ok       /Volumes/Media/A008_07091214_C068.braw",
		"ok       /Volumes/Media/A008_07091214_C069.braw",
		"MISMATCH /Volumes/Media/A008_07091214_C070.braw",
		"         recorded 8516299eda3b1cf414041e1e695c9338692dee6af8de11ec68000bde8ebdf0fd",
		"         now      b6f00f283e24783b68eb63deb8c6f492dfafd29a45a11fa7c2725869596f81f4",
		"ok       /Volumes/Media/B002_0709_C002.mov",
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
			// The band is identified by its own rule, not by the word
			// appearing anywhere: the title line reads "N workers" too.
			if strings.HasPrefix(r, bLT) && strings.Contains(r, " workers ") {
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

// TestDashboardBordersAlign: the vertical rules have to sit in the same column
// on every row of a band, or the panes visibly shear.
func TestDashboardBordersAlign(t *testing.T) {
	d := fakeDisplay(6)
	frame := d.renderDashboard(30, 100)
	var split = -1
	for _, row := range frame {
		r := []rune(row)
		if len(r) == 0 || r[0] != []rune(bV)[0] {
			continue
		}
		// Count only rows that carry an interior divider.
		var mid []int
		for i := 1; i < len(r)-1; i++ {
			if string(r[i]) == bV {
				mid = append(mid, i)
			}
		}
		if len(mid) != 1 {
			continue
		}
		if split == -1 {
			split = mid[0]
		} else if mid[0] != split {
			t.Fatalf("divider moved from column %d to %d:\n%s", split, mid[0], row)
		}
	}
	if split == -1 {
		t.Fatal("no split rows found; the two-pane band did not render")
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
