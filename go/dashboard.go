package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
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
	// Joins first, so the title can be measured against them. A join dropped
	// on top of the title afterwards lands in the middle of a word:
	// "0 no usable┬sidecar" is what that looks like.
	limit := inner - 2
	for at, glyph := range joins {
		if at > 0 && at < inner {
			cells[at] = glyph
			if at-1 < limit {
				limit = at - 1
			}
		}
	}
	if title != "" {
		// Indexed by rune, not by byte. Ranging a string yields byte offsets,
		// so a multi-byte glyph in the title used to skip cells and leave
		// border segments stranded inside the text.
		t := []rune(" " + title + " ")
		if len(t) > limit && limit > 3 {
			t = append(t[:limit-2:limit-2], '…', ' ')
		}
		if len(t) <= limit {
			for i, r := range t {
				cells[i+1] = string(r)
			}
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
// renderDashboard composes the whole screen, before and after the scan ends.
//
// The frame is deliberately identical in both states: same border, same panes,
// same places. Only what the panes hold changes, workers becoming problems and
// the progress bar becoming the summary. A results screen with a different
// shape makes the reader re-find everything they were already looking at, at
// exactly the moment they have something to act on.
//
// It degrades by dropping panes rather than by overflowing: a terminal too
// short for the band loses the band, and one too short for both loses the
// stream, because the statistics are the part that cannot be reconstructed by
// looking elsewhere.
func (d *Display) renderDashboard(rows, cols int) []string {
	if cols < 40 || rows < 8 {
		return d.renderTooSmall(rows, cols)
	}
	cols = uiWidth(cols)
	st := d.snapshot()
	done := d.done.Load()

	band := d.bandRows(cols)
	showBand := d.showBand.Load() && len(band) > 0
	bandH := 0
	if showBand {
		bandH = len(band) + 1 // rows plus its rule
	}

	bodyH := rows - (1 + 1 + bandH + 1 + 1)
	if bodyH < 3 && showBand {
		showBand, bandH = false, 0
		bodyH = rows - (1 + 1 + 1 + 1)
	}
	if bodyH < 1 {
		bodyH = 1
	}
	// What the body height would be with the band showing, which is what the
	// statistics pane is laid out against so it does not change when the band
	// is toggled.
	bodyWithBand := rows - (1 + 1 + len(band) + 1 + 1 + 1)
	if bodyWithBand < 1 {
		bodyWithBand = 1
	}

	statsW := capAt(cols*2/5, maxStatsWidth)
	if statsW < 24 {
		statsW = 24
	}
	split := cols - statsW - 2
	if split < 24 {
		split = 24
		statsW = cols - split - 2
	}

	focus := d.focus.Load()
	out := make([]string, 0, rows)
	out = append(out, colorRuleHalves(
		hrule(bTL, bTR, cols, d.title(done), map[int]string{split - 1: bTT}),
		split-1, d.c, focus == focusStream, focus == focusStats))

	// The statistics are laid out for the height they would have with the band
	// showing, whichever way the band is actually set. Otherwise hiding the
	// worker rows hands the pane more room and it grows a couple of counters
	// back, so a key about workers silently changes which statistics exist.
	// Only the stream takes the slack.
	left := d.leftPane(bodyH, split-1, done)
	right := d.statsLines(statsW, minInt(bodyH, bodyWithBand), st, done)
	for i := 0; i < bodyH; i++ {
		out = append(out, splitRow(cols, split, rowAt(left, i), rowAt(right, i)))
	}

	bandJoins := map[int]string{split - 1: bBT}
	if showBand {
		rule := hrule(bLT, bRT, cols, d.bandTitle(done), bandJoins)
		if focus == focusBand {
			rule = d.c.cyan(rule)
		}
		out = append(out, rule)
		for _, row := range band {
			out = append(out, boxRow(cols, row))
		}
		out = append(out, hrule(bLT, bRT, cols, "", nil))
	} else {
		out = append(out, hrule(bLT, bRT, cols, "", bandJoins))
	}

	// Recorded for the page keys, which move by what is on screen.
	d.paneH.Store(int32(bodyH))
	d.bandH.Store(int32(len(band)))

	out = append(out, boxRow(cols, d.bottomLine(cols-2, st, done)))
	out = append(out, hrule(bBL, bBR, cols, "", nil))
	return out
}

func rowAt(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return ""
}

func (d *Display) title(done bool) string {
	if !done {
		workers := "workers"
		if len(d.slots) == 1 {
			workers = "worker"
		}
		return fmt.Sprintf("treecheck · %s · %s · %d %s",
			truncRunes(displayPath(d.root), 40), d.mode, len(d.slots), workers)
	}
	d.mu.Lock()
	res := d.res
	d.mu.Unlock()
	n := res.counts
	if n == nil {
		return "treecheck · finished"
	}
	if len(n.Failures) == 0 {
		return fmt.Sprintf("treecheck · %s · %s",
			truncRunes(displayPath(d.root), 40), statusPhrase(res.status))
	}
	return fmt.Sprintf("treecheck · %d mismatched · %d io · %d no usable sidecar",
		n.Mismatch, n.IOErr, n.Missing)
}

func statusPhrase(status int) string {
	switch status {
	case 0:
		return "nothing wrong"
	case 2:
		return "no usable sidecar"
	case 130:
		return "interrupted"
	default:
		return "failed"
	}
}

func (d *Display) bandTitle(done bool) string {
	if done {
		return "problems"
	}
	return "workers"
}

// bandRows is the lower band: one row per worker while scanning, one row per
// problem once finished.
func (d *Display) bandRows(cols int) []string {
	if !d.done.Load() {
		rows := make([]string, 0, len(d.slots))
		for i, s := range d.slots {
			rows = append(rows, d.slotRow(i, s, cols-2))
		}
		return rows
	}
	d.mu.Lock()
	res, sel := d.res, d.sel
	d.mu.Unlock()
	if res.counts == nil || len(res.counts.Failures) == 0 {
		return nil
	}
	// Limited to the selected counter, so picking "mismatched" in the
	// statistics pane narrows the list to the files that matter rather than
	// only recolouring a number.
	fs := res.counts.Failures
	if want, filtered := d.filter(); filtered {
		kept := make([]Failure, 0, len(fs))
		for _, f := range fs {
			if f.Outcome == want {
				kept = append(kept, f)
			}
		}
		if len(kept) > 0 {
			fs = kept
			if sel >= len(fs) {
				sel = len(fs) - 1
			}
		}
	}

	// A window that keeps the selection visible, so the keys move something
	// the reader can see.
	const maxRows = 10
	h := len(fs)
	if h > maxRows {
		h = maxRows
	}
	start := sel - h/2
	if start > len(fs)-h {
		start = len(fs) - h
	}
	if start < 0 {
		start = 0
	}

	rows := make([]string, 0, h)
	for i := start; i < start+h; i++ {
		f := fs[i]
		marker := rowCursor(d.c, i == sel)
		tok := padRight(f.Token, verdictWidth)
		switch f.Outcome {
		case OutcomeMismatch:
			tok = d.c.red(tok)
		case OutcomeIOError:
			tok = d.c.yellow(tok)
		}
		rel := f.Path
		if p, err := filepath.Rel(res.root, f.Path); err == nil && !strings.HasPrefix(p, "..") {
			rel = p
		}
		rows = append(rows, marker+tok+" "+displayPath(rel))
	}
	return rows
}

// leftPane is the verdict stream while scanning, and the selected problem's
// evidence once finished.
func (d *Display) leftPane(h, w int, done bool) []string {
	if !done {
		return d.recentLines(h)
	}
	d.mu.Lock()
	res, sel := d.res, d.sel
	d.mu.Unlock()
	if res.counts == nil || len(res.counts.Failures) == 0 {
		return d.cleanPane(h, w, res)
	}
	return d.detailPane(h, w, res, sel)
}

func (d *Display) cleanPane(h, w int, res results) []string {
	out := []string{""}
	head := "every file matched its sidecar"
	switch res.status {
	case 130:
		head = d.c.yellow("stopped early; only the files below were checked")
	case 2:
		head = d.c.yellow("nothing is corrupt, but some files have no sidecar yet")
	default:
		head = d.c.green(head)
	}
	out = append(out, " "+head, "")
	for _, l := range d.recentLines(h - len(out)) {
		out = append(out, l)
	}
	return out
}

func (d *Display) detailPane(h, w int, res results, sel int) []string {
	fs := res.counts.Failures
	if sel < 0 || sel >= len(fs) {
		return nil
	}
	f := fs[sel]
	fo := d.forensicsFor(sel, f)

	var out []string
	add := func(format string, args ...any) {
		out = append(out, truncVisible(" "+fmt.Sprintf(format, args...), w))
	}
	add("%s", d.c.cyan(displayPath(f.Path)))
	out = append(out, "")
	if fo.fileOK {
		add("file      %s   mode %v", exactBytes(fo.fileSize), fo.fileMode.Perm())
		add("          %s", fo.fileMod.Format(time.RFC3339))
	} else {
		add("file      %s", d.c.yellow("not present"))
	}
	if fo.sideOK {
		add("sidecar   %s   %s", exactBytes(fo.sideSize), fo.sideMod.Format(time.RFC3339))
	} else {
		add("sidecar   %s", d.c.yellow("not present"))
	}
	if f.Recorded != "" {
		out = append(out, "")
		add("recorded  %s", f.Recorded)
		add("computed  %s", d.c.red(f.Computed))
	}
	if len(fo.assessment) > 0 {
		out = append(out, "")
		for i, a := range fo.assessment {
			if i == 0 {
				add("%s", d.c.yellow(a))
			} else {
				add("%s", a)
			}
		}
	}
	return out
}

func (d *Display) forensicsFor(i int, f Failure) forensics {
	d.mu.Lock()
	if d.forensics == nil {
		d.forensics = map[int]forensics{}
	}
	if fo, ok := d.forensics[i]; ok {
		d.mu.Unlock()
		return fo
	}
	d.mu.Unlock()

	fo := gather(f)
	d.mu.Lock()
	d.forensics[i] = fo
	d.mu.Unlock()
	return fo
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
	want, filtered := d.filter()
	d.mu.Lock()
	rec := make([]string, 0, len(d.recent))
	for _, l := range d.recent {
		if filtered && l.outcome != want {
			continue
		}
		rec = append(rec, l.text)
	}
	d.mu.Unlock()

	// Scrolled back from the newest line, so the stream can be read rather
	// than only watched.
	off := int(d.streamOff.Load())
	if off > len(rec) {
		off = len(rec)
	}
	end := len(rec) - off
	if end < 0 {
		end = 0
	}
	start := end - h
	if start < 0 {
		start = 0
	}
	rec = rec[start:end]

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
func (d *Display) statsLines(w, h int, st statsSnapshot, done bool) []string {
	c := d.c
	pair := func(label, value string) string { return "  " + pairIn(label, value, w-4) }
	mism := itoa(int(st.mismatch))
	if st.mismatch > 0 {
		mism = c.red(mism + " corrupt")
	}

	// prio 0 is kept as long as there is any room at all; 2 goes first.
	type row struct {
		text string
		prio int
	}
	focused := d.focus.Load() == focusStats
	sel := int(d.statSel.Load())
	// Marked rather than merely coloured: the selected filter has to be
	// readable at a glance from across the pane, including when the pane does
	// not have focus and nothing is highlighted.
	mark := func(i int, text string) string {
		if i == sel && !focused {
			return c.dim(cursorMark) + cursorGap + text
		}
		return rowCursor(c, i == sel) + text
	}
	counts := []string{
		"", itoa(int(st.ok)), mism, itoa(int(st.missing)), itoa(int(st.ioerr)),
	}
	rows := []row{{"", 2}}
	for i, f := range statFilters {
		label, value := f.label, counts[i]
		if f.all {
			value = itoa(int(st.doneFiles))
		}
		rows = append(rows, row{mark(i, pairIn(label, value, w-4)), 0})
	}
	rows = append(rows, row{"", 2})
	rows = append(rows, []row{
		{pair("files", fmt.Sprintf("%d / %d", st.doneFiles, d.total)), 0},
		{pair("data", fmt.Sprintf("%s / %s", humanBytes(st.doneBytes), humanBytes(d.totalBytes))), 1},
	}...)
	if done {
		// An estimate and an instantaneous rate describe work still to come.
		// Once there is none, the honest readings are what it took and what
		// it averaged, and a sparkline of a finished run is decoration.
		rows = append(rows,
			row{pair("elapsed", fmtDur(st.elapsed)), 0},
			row{"", 2},
			row{pair("average", c.cyan(humanBytes(st.rate)+"/s")), 1},
		)
	} else {
		rows = append(rows,
			row{pair("elapsed", fmtDur(st.elapsed)), 1},
			row{pair("eta", st.eta), 0},
			row{"", 2},
			row{pair("throughput", c.cyan(humanBytes(st.rate)+"/s")), 0},
			row{" " + c.cyan(sparkline(st.hist, w-2)), 1},
		)
	}

	// Spacers go first, then the least load-bearing numbers, so a genuinely
	// short terminal still shows the counters that cannot be reconstructed
	// from anywhere else.
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

// bottomLine is the progress bar while scanning and the summary afterwards,
// each with the keys that apply in that state.
func (d *Display) bottomLine(w int, st statsSnapshot, done bool) string {
	hint := d.hintText(done, st.paused)
	var lead string
	if done {
		lead = d.summaryText()
	} else {
		pct := fmt.Sprintf(" %3.0f%% ", st.frac*100)
		if st.paused {
			pct = " " + d.c.yellow("PAUSED") + " "
		}
		barW := capAt(w-visibleLen(hint)-visibleLen(pct)-3, maxBarWidth)
		if barW < 4 {
			return truncVisible(" "+pct+hint, w)
		}
		lead = " " + d.c.cyan(bar(st.frac, barW)) + pct
	}
	if pad := w - visibleLen(lead) - visibleLen(hint); pad > 0 {
		return lead + strings.Repeat(" ", pad) + d.c.dim(hint)
	}
	return truncVisible(lead+" "+d.c.dim(hint), w)
}

// hintText names every key that does something right now, and only those. A
// binding that works but is never offered may as well not exist; one that is
// offered but does nothing is worse.
func (d *Display) hintText(done, paused bool) string {
	what := "scroll"
	switch d.focus.Load() {
	case focusStats:
		what = "filter"
	case focusBand:
		if done {
			what = "problem"
		} else {
			what = "worker"
		}
	}
	keys := "[tab] focus  [↑↓] " + what + "  "
	if !done {
		// Only offered while there are workers to hide. After the scan the
		// band holds the problems, which are the point of the screen.
		if d.showBand.Load() {
			keys += "[space] hide workers  "
		} else {
			keys += "[space] show workers  "
		}
		if paused {
			keys += "[p] resume  "
		} else {
			keys += "[p] pause  "
		}
	}
	return keys + "[q] quit"
}

// Marks, one role each, so a reader never has to work out which is which:
//
//	border segment highlighted   this pane has the keys
//	cursor (▸)                   this row is where the keys are pointing
//	gutter (reserved)            selection, once there is something to select
//
// A future multi-select needs a mark of its own, and it cannot be either of
// the two above, so the row gutter is laid out with room for it already.
const (
	cursorMark = "▸"
	cursorGap  = " "
	// gutterWidth is the cursor plus its gap. Every row in a selectable list
	// reserves it, selected or not, so nothing shifts as the cursor moves.
	gutterWidth = 2
)

// rowCursor renders a list row's gutter.
func rowCursor(c *colors, isCursor bool) string {
	if isCursor {
		return c.cyan(cursorMark) + cursorGap
	}
	return " " + cursorGap[:1]
}

// summaryLine is the whole run in one row. Nothing is written to stdout in
// this mode, so this is the only place the counters appear.
func (d *Display) summaryText() string {
	d.mu.Lock()
	res := d.res
	sel := d.sel
	d.mu.Unlock()
	n := res.counts
	if n == nil {
		return ""
	}
	left := fmt.Sprintf(" %d scanned · %s verified", n.Scanned, d.c.green(itoa(n.OK)))
	if n.Created > 0 {
		left += fmt.Sprintf(" · %d created", n.Created)
	}
	if n.Unverified > 0 {
		left += fmt.Sprintf(" · %d not verified", n.Unverified)
	}
	if n.Unreached > 0 {
		left += " · " + d.c.yellow(itoa(n.Unreached)+" not reached")
	}
	left += " · " + fmtDur(res.elapsed)
	if total := len(n.Failures); total > 0 {
		left += fmt.Sprintf(" · %d/%d", sel+1, total)
	}
	return left
}

// pairIn lays a label against a right-aligned value inside a given width.
func pairIn(label, value string, w int) string {
	gap := w - 2 - len(label) - visibleLen(value)
	if gap < 1 {
		gap = 1
	}
	return " " + label + strings.Repeat(" ", gap) + value
}

// colorRuleHalves highlights the half of a shared rule that belongs to the
// focused pane. The rule spans both panes, so colouring the whole thing would
// say nothing about which one has the keys.
func colorRuleHalves(rule string, at int, c *colors, leftFocused, rightFocused bool) string {
	if !leftFocused && !rightFocused {
		return rule
	}
	r := []rune(rule)
	if at < 1 || at >= len(r)-1 {
		return rule
	}
	left, right := string(r[:at+1]), string(r[at+1:])
	if leftFocused {
		return c.cyan(left) + right
	}
	return left + c.cyan(right)
}
