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
	return boxRowFocus(cols, content, nil, false)
}

// boxRowFocus draws a full-width row, colouring both side borders when the pane
// it belongs to has focus.
func boxRowFocus(cols int, content string, c *colors, focused bool) string {
	body := padVisible(truncVisible(content, cols-2), cols-2)
	l, r := bV, bV
	if focused && c != nil {
		l, r = c.cyan(bV), c.cyan(bV)
	}
	return l + body + r
}

// splitRow draws one row of a two-pane band, with the divider at a fixed column
// so the panes stay aligned down the whole band.
func splitRow(cols, split int, left, right string) string {
	return splitRowFocus(cols, split, left, right, nil, false, false)
}

// splitRowFocus draws a two-pane row. The outer border on each side belongs to
// the pane beside it, and the divider belongs to whichever pane has focus, so
// the highlighted region encloses exactly one pane.
func splitRowFocus(cols, split int, left, right string, c *colors, leftFocus, rightFocus bool) string {
	l := padVisible(truncVisible(left, split-1), split-1)
	r := padVisible(truncVisible(right, cols-split-2), cols-split-2)
	lb, mid, rb := bV, bV, bV
	if c != nil {
		if leftFocus {
			lb, mid = c.cyan(bV), c.cyan(bV)
		} else if rightFocus {
			mid, rb = c.cyan(bV), c.cyan(bV)
		}
	}
	return lb + l + mid + r + rb
}

// pane draws one bounded box, borders included, exactly w cells wide and h+2
// rows tall.
//
// Panes do not share borders. A shared segment belongs to two panes at once,
// so highlighting it to show focus says "one of these two", which is not what
// focus means: the divider between the stream and the counters lit up for
// either, and the rule above the worker band lit up for a pane that was not
// the worker band. Every pane owning its own outline costs one row and two
// columns, and makes the highlight unambiguous.
func pane(c *colors, title string, w, h int, content []string, focused bool) []string {
	top := hrule(bTL, bTR, w, title, nil)
	bot := hrule(bBL, bBR, w, "", nil)
	if focused {
		top, bot = c.cyan(top), c.cyan(bot)
	}
	out := make([]string, 0, h+2)
	out = append(out, top)
	for i := 0; i < h; i++ {
		out = append(out, boxRowFocus(w, rowAt(content, i), c, focused))
	}
	return append(out, bot)
}

// joinH places two blocks of rows side by side.
func joinH(a, b []string) []string {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]string, n)
	for i := range out {
		out[i] = rowAt(a, i) + rowAt(b, i)
	}
	return out
}

// renderDashboard composes the whole screen, before and after the scan ends.
//
// The layout is deliberately identical in both states: same panes in the same
// places, carrying problems and evidence instead of workers and a stream once
// there is nothing left to run. A results screen with a different shape makes
// the reader re-find everything they were already looking at, at exactly the
// moment they have something to act on.
//
// It degrades by dropping panes rather than by overflowing: a terminal too
// short for the band loses the band, because a list of workers is worth less
// than knowing what the run found.
func (d *Display) renderDashboard(rows, cols int) []string {
	if cols < 40 || rows < 10 {
		return d.renderTooSmall(rows, cols)
	}
	cols = uiWidth(cols)
	st := d.snapshot()
	done := d.done.Load()
	focus := d.focus.Load()

	band := d.bandRows(cols - 2)
	showBand := d.showBand.Load() && len(band) > 0
	bandBox := 0
	if showBand {
		bandBox = len(band) + 2
	}

	// Each box costs its two rules; the status row costs one.
	bodyH := rows - bandBox - 2 - 1
	const minBody = 7
	if bodyH < minBody && showBand {
		showBand, bandBox = false, 0
		bodyH = rows - 2 - 1
	}
	if bodyH < 1 {
		bodyH = 1
	}
	bodyWithBand := rows - (len(band) + 2) - 2 - 1
	if bodyWithBand < 1 {
		bodyWithBand = 1
	}

	statsW := capAt(cols*2/5, maxStatsWidth)
	if statsW < 26 {
		statsW = 26
	}
	streamW := cols - statsW
	if streamW < 30 {
		streamW = 30
		statsW = cols - streamW
	}

	// The statistics are laid out for the height they would have with the band
	// showing, whichever way the band is actually set. Otherwise hiding the
	// worker rows hands the pane more room and it grows a counter or two back,
	// so a key about workers silently changes which statistics exist.
	left := d.leftPane(bodyH, streamW-2, done)
	right := d.statsLines(statsW-2, minInt(bodyH, bodyWithBand), st, done)

	out := make([]string, 0, rows)
	out = append(out, joinH(
		pane(d.c, d.title(done), streamW, bodyH, left, focus == focusStream),
		pane(d.c, "counters", statsW, bodyH, right, focus == focusStats))...)
	if showBand {
		out = append(out, pane(d.c, d.bandTitle(done), cols, len(band), band,
			focus == focusBand)...)
	}

	// Recorded for the page keys, which move by what is on screen.
	d.paneH.Store(int32(bodyH))
	d.bandH.Store(int32(len(band)))

	out = append(out, d.bottomLine(cols, st, done))
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
	match, filtered := d.filter()
	d.mu.Lock()
	rec := make([]string, 0, len(d.recent))
	for _, l := range d.recent {
		if filtered && !match(l.outcome) {
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
	if len(rec) == 0 && filtered {
		// A blank pane reads as something having gone wrong. Selecting a
		// category with nothing in it is a perfectly good answer, and saying
		// so is the difference between a result and an apparent fault.
		if h > 0 {
			out[0] = " " + d.c.dim("no files in this category")
		}
		return out
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
	problems := st.mismatch + st.missing + st.ioerr
	problemText := itoa(int(problems))
	if problems > 0 {
		problemText = c.yellow(problemText)
	}
	counts := []string{
		itoa(int(st.doneFiles)), itoa(int(st.ok)), problemText,
		mism, itoa(int(st.missing)), itoa(int(st.ioerr)),
	}
	rows := []row{{"", 2}}
	for i, f := range statFilters {
		// Indented by depth, so the grouping is visible rather than something
		// to be inferred from the order.
		label := strings.Repeat("  ", f.depth) + f.label
		rows = append(rows, row{mark(i, pairIn(label, counts[i], w-4)), 0})
	}
	rows = append(rows, row{"", 2})
	rows = append(rows, []row{
		{pair("files", fmt.Sprintf("%d / %d", st.doneFiles, d.total)), 1},
		{pair("data", fmt.Sprintf("%s / %s", humanBytes(st.doneBytes), humanBytes(d.totalBytes))), 1},
	}...)
	if done {
		// An estimate and an instantaneous rate describe work still to come.
		// Once there is none, the honest readings are what it took and what
		// it averaged, and a sparkline of a finished run is decoration.
		rows = append(rows,
			row{pair("elapsed", fmtDur(st.elapsed)), 0},
			row{"", 2},
			row{pair("throughput", c.cyan(humanBytes(st.average)+"/s")), 1},
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
	// The lead is sized first and the hints get what is left, rather than each
	// taking half the row. Splitting it evenly meant a bar that never wants
	// more than a couple of dozen cells reserved half a wide terminal, and the
	// hints fell back to their short forms with room to spare.
	var lead string
	if done {
		lead = d.summaryText()
	} else {
		pct := fmt.Sprintf(" %3.0f%% ", st.frac*100)
		if st.paused {
			pct = " " + d.c.yellow("PAUSED") + " "
		}
		if barW := capAt(w/3, maxBarWidth); barW >= 4 {
			lead = " " + d.c.cyan(bar(st.frac, barW)) + pct
		} else {
			lead = pct
		}
	}
	hint := d.hintText(done, st.paused, w-visibleLen(lead)-2)
	if pad := w - visibleLen(lead) - visibleLen(hint) - 1; pad > 0 {
		return lead + strings.Repeat(" ", pad) + d.c.dim(hint)
	}
	return truncVisible(lead+" "+d.c.dim(hint), w)
}

// hintText names every key that does something right now, and only those. A
// binding that works but is never offered may as well not exist; one that is
// offered but does nothing is worse.
func (d *Display) hintText(done, paused bool, w int) string {
	what := "scroll"
	switch d.focus.Load() {
	case focusStats:
		what = "filter"
	case focusBand:
		what = "problem"
	}

	// Ordered least to most worth keeping. A narrow terminal drops hints from
	// the front rather than truncating the row, which otherwise cuts a key
	// name in half and leaves something like "[q" as the last thing on screen.
	type hint struct{ full, short string }
	hints := []hint{}
	if !done {
		band := "[space] hide workers"
		if !d.showBand.Load() {
			band = "[space] show workers"
		}
		hints = append(hints, hint{band, "[space]"})
	}
	hints = append(hints, hint{"[tab] focus", "[tab]"})
	hints = append(hints, hint{"[↑↓] " + what, "[↑↓]"})
	if !done {
		if paused {
			hints = append(hints, hint{"[p] resume", "[p]"})
		} else {
			hints = append(hints, hint{"[p] pause", "[p]"})
		}
	}
	hints = append(hints, hint{"[q] quit", "[q]"})

	// Full labels if they fit, short ones if they do not, then shed from the
	// front until what is left fits. Quit is last in the list, so it is the
	// one thing that always survives.
	join := func(parts []string) string { return strings.Join(parts, "  ") }
	full := make([]string, len(hints))
	short := make([]string, len(hints))
	for i, h := range hints {
		full[i], short[i] = h.full, h.short
	}
	if visibleLen(join(full)) <= w {
		return join(full)
	}
	for i := 0; i < len(short); i++ {
		if candidate := join(short[i:]); visibleLen(candidate) <= w {
			return candidate
		}
	}
	return short[len(short)-1]
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

func rowAt(rows []string, i int) string {
	if i < len(rows) {
		return rows[i]
	}
	return ""
}

// title names the run while it is running and its outcome once it is not. It
// lives in the stream pane's own border, since panes no longer share one.
func (d *Display) title(done bool) string {
	if !done {
		workers := "workers"
		if len(d.slots) == 1 {
			workers = "worker"
		}
		return fmt.Sprintf("%s · %s · %d %s",
			truncRunes(displayPath(d.root), 32), d.mode, len(d.slots), workers)
	}
	d.mu.Lock()
	res := d.res
	d.mu.Unlock()
	if res.counts == nil {
		return "finished"
	}
	if len(res.counts.Failures) == 0 {
		return statusPhrase(res.status)
	}
	return "detail"
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
func (d *Display) bandRows(w int) []string {
	if !d.done.Load() {
		rows := make([]string, 0, len(d.slots))
		for i, s := range d.slots {
			rows = append(rows, d.slotRow(i, s, w))
		}
		return rows
	}
	d.mu.Lock()
	res, sel := d.res, d.sel
	d.mu.Unlock()
	if res.counts == nil || len(res.counts.Failures) == 0 {
		return nil
	}
	// Limited to the selected counter, so picking "mismatched" narrows the
	// list to the files that matter rather than only recolouring a number.
	fs := res.counts.Failures
	if match, filtered := d.filter(); filtered {
		kept := make([]Failure, 0, len(fs))
		for _, f := range fs {
			if match(f.Outcome) {
				kept = append(kept, f)
			}
		}
		fs = kept
		if sel >= len(fs) {
			sel = len(fs) - 1
		}
	}
	if len(fs) == 0 {
		return []string{" " + d.c.dim("no files in this category")}
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
		rows = append(rows, rowCursor(d.c, i == sel)+tok+" "+displayPath(rel))
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
	head := "every file matched its sidecar"
	switch res.status {
	case 130:
		head = d.c.yellow("stopped early; only the files below were checked")
	case 2:
		head = d.c.yellow("nothing is corrupt, but some files have no sidecar yet")
	default:
		head = d.c.green(head)
	}
	out := []string{"", " " + head, ""}
	return append(out, d.recentLines(h-len(out))...)
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
