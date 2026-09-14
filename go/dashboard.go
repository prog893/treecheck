package main

import (
	"fmt"
	"strings"
)

// Box-drawing pieces, kept in one place so a terminal that renders them badly
// can be accommodated by changing this table rather than the layout code.
const (
	bTL, bTR, bBL, bBR = "┌", "┐", "└", "┘"
	bH, bV             = "─", "│"
	bLT, bRT, bTT, bBT = "├", "┤", "┬", "┴"
	bX                 = "┼"
)

// pane titles are drawn into the border rather than costing a row of their own,
// which on a 24-row terminal is the difference between showing six worker rows
// and showing three.
func hrule(left, right string, cols int, title string, joins map[int]string) string {
	var b strings.Builder
	b.WriteString(left)
	inner := cols - 2
	cells := make([]string, inner)
	for i := range cells {
		cells[i] = bH
	}
	if title != "" {
		// Indexed by rune, not by byte. Ranging a string yields byte
		// offsets, so a multi-byte glyph in the title used to skip cells
		// and leave border segments stranded inside the text.
		t := []rune(" " + title + " ")
		if len(t) < inner-2 {
			for i, r := range t {
				cells[i+1] = string(r)
			}
		}
	}
	for at, glyph := range joins {
		if at > 0 && at < inner {
			cells[at] = glyph
		}
	}
	b.WriteString(strings.Join(cells, ""))
	b.WriteString(right)
	return b.String()
}

func boxRow(cols int, content string) string {
	return bV + padVisible(truncVisible(content, cols-2), cols-2) + bV
}

// splitRow draws one row of a two-pane band, with the divider at a fixed column
// so the panes stay aligned down the whole band.
func splitRow(cols, split int, left, right string) string {
	l := padVisible(truncVisible(left, split-1), split-1)
	r := padVisible(truncVisible(right, cols-split-2), cols-split-2)
	return bV + l + bV + r + bV
}

// renderDashboard composes the whole screen. It degrades by dropping panes
// rather than by overflowing: a terminal too short for the worker band loses
// the worker band, and one too short for both bands still shows the headline
// and the statistics, which is the part you cannot reconstruct by looking
// elsewhere.
func (d *Display) renderDashboard(rows, cols int) []string {
	if cols < 40 || rows < 8 {
		return d.renderTooSmall(rows, cols)
	}
	st := d.snapshot()

	nWorkers := len(d.slots)
	workerBand := nWorkers + 1 // rows + its rule
	// top rule + body + mid rule + workers + mid rule + progress + bottom rule
	bodyH := rows - (1 + 1 + workerBand + 1 + 1)
	showWorkers := true
	if bodyH < 4 {
		showWorkers = false
		bodyH = rows - (1 + 1 + 1 + 1)
	}
	if bodyH < 1 {
		bodyH = 1
	}

	split := cols * 3 / 5
	if split < 24 {
		split = 24
	}
	if split > cols-26 {
		split = cols - 26
	}
	statsW := cols - split - 2

	out := make([]string, 0, rows)

	title := fmt.Sprintf("treecheck · %s · %s · %d workers",
		truncRunes(displayPath(d.root), 40), d.mode, nWorkers)
	out = append(out, hrule(bTL, bTR, cols, title, map[int]string{split - 1: bTT}))

	left := d.recentLines(bodyH)
	right := d.statsLines(statsW, bodyH, st)
	for i := 0; i < bodyH; i++ {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, splitRow(cols, split, l, r))
	}

	if showWorkers {
		out = append(out, hrule(bLT, bRT, cols, "workers", map[int]string{split - 1: bBT}))
		for i, s := range d.slots {
			out = append(out, boxRow(cols, d.slotRow(i, s, cols-2)))
		}
		out = append(out, hrule(bLT, bRT, cols, "", nil))
	} else {
		out = append(out, hrule(bLT, bRT, cols, "", map[int]string{split - 1: bBT}))
	}

	out = append(out, boxRow(cols, d.progressLine(cols-2, st)))
	out = append(out, hrule(bBL, bBR, cols, "", nil))
	return out
}

func (d *Display) renderTooSmall(rows, cols int) []string {
	st := d.snapshot()
	out := []string{truncVisible(fmt.Sprintf(" %d/%d  %.0f%%", st.doneFiles, d.total, st.frac*100), cols)}
	if rows > 1 {
		out = append(out, truncVisible(" terminal too small for the dashboard", cols))
	}
	return out
}

// recentLines is the verdict stream, newest at the bottom so it reads the way
// a scrolling log does.
func (d *Display) recentLines(h int) []string {
	d.mu.Lock()
	rec := make([]string, len(d.recent))
	copy(rec, d.recent)
	d.mu.Unlock()

	if len(rec) > h {
		rec = rec[len(rec)-h:]
	}
	out := make([]string, h)
	for i := range out {
		out[i] = ""
	}
	for i, l := range rec {
		out[h-len(rec)+i] = " " + l
	}
	return out
}

// statsLines builds the statistics pane to fit the height it is given.
//
// The pane is taller than a short terminal can show, so it has to shed rows
// somewhere. Truncating the tail is the wrong answer: throughput and the
// estimate live at the bottom and are the readings you cannot reconstruct by
// looking at the verdict stream. Instead the rows carry a priority, the
// spacers go first, and what survives is drawn in its original order.
func (d *Display) statsLines(w, h int, st statsSnapshot) []string {
	c := d.c
	pair := func(label, value string) string {
		gap := w - 2 - len(label) - visibleLen(value)
		if gap < 1 {
			gap = 1
		}
		return " " + label + strings.Repeat(" ", gap) + value
	}
	mism := itoa(int(st.mismatch))
	if st.mismatch > 0 {
		mism = c.red(mism + " corrupt")
	}

	// prio 0 is kept as long as there is any room at all; 2 goes first.
	type row struct {
		text string
		prio int
	}
	rows := []row{
		{"", 2},
		{pair("verified", c.green(itoa(int(st.ok)))), 0},
		{pair("mismatched", mism), 0},
		{pair("missing", itoa(int(st.missing))), 1},
		{pair("io errors", itoa(int(st.ioerr))), 1},
		{"", 2},
		{pair("files", fmt.Sprintf("%d / %d", st.doneFiles, d.total)), 0},
		{pair("data", fmt.Sprintf("%s / %s", humanBytes(st.doneBytes), humanBytes(d.totalBytes))), 1},
		{pair("elapsed", fmtDur(st.elapsed)), 1},
		{pair("eta", st.eta), 0},
		{"", 2},
		{pair("throughput", c.cyan(humanBytes(st.rate)+"/s")), 0},
		{" " + c.cyan(sparkline(st.hist, w-2)), 1},
	}

	drop := len(rows) - h
	for prio := 2; prio >= 1 && drop > 0; prio-- {
		for i := len(rows) - 1; i >= 0 && drop > 0; i-- {
			if rows[i].prio == prio {
				rows = append(rows[:i], rows[i+1:]...)
				drop--
			}
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.text)
	}
	return out
}

func (d *Display) progressLine(w int, st statsSnapshot) string {
	hint := "[tab] log  [space] detail  [q] quit"
	pct := fmt.Sprintf(" %3.0f%% ", st.frac*100)
	barW := w - len(hint) - len(pct) - 3
	if barW < 4 {
		return truncVisible(" "+pct+hint, w)
	}
	return " " + d.c.cyan(bar(st.frac, barW)) + pct + d.c.dim(hint)
}
