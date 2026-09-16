package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// slot is one worker's view of what it is doing right now. Bytes hashed is
// tracked per file, which is what lets a 50 GiB original show a moving bar
// instead of a row that sits unchanged for several minutes. The shell
// implementation could not do this at all: it shelled out to `shasum`, which
// reports nothing until it is finished.
type slot struct {
	active atomic.Bool
	path   atomic.Value // string
	size   atomic.Int64
	done   atomic.Int64
	start  atomic.Int64 // unix nanos
}

// Display owns the live view. Everything it reads is either an atomic or
// guarded here, so the hashing workers never block on the terminal.
type Display struct {
	r     *Renderer
	c     *colors
	slots []*slot

	total      int
	totalBytes int64

	doneFiles atomic.Int64
	doneBytes atomic.Int64
	nOK       atomic.Int64
	nMismatch atomic.Int64
	nMissing  atomic.Int64
	nIOErr    atomic.Int64

	start    time.Time
	expanded atomic.Bool
	// dash is fixed for the run. The two views are not interchangeable at
	// runtime: the full-screen one deliberately writes nothing to stdout, and
	// the scrolling one is nothing but writes to stdout.
	dash   bool
	stop   chan struct{}
	wg     sync.WaitGroup
	screen *Screen

	// The frame keeps its shape before and after the scan finishes; only what
	// the panes hold changes. Switching to a differently shaped screen at the
	// moment a run ends makes the reader re-find everything they were already
	// looking at.
	done atomic.Bool
	// showBand toggles the lower band: worker rows while running, the problem
	// list once finished.
	showBand atomic.Bool
	// focus is which pane the arrow keys act on. One set of keys doing
	// different things depending on where you are is how a two-pane view
	// stays navigable without a key per pane.
	focus atomic.Int32
	// streamOff scrolls the verdict stream back from its newest line.
	streamOff atomic.Int64
	// statSel picks a counter in the statistics pane, which filters the
	// stream and the problem list to that category.
	statSel atomic.Int32
	// Heights of the panes as last drawn, so a page key moves by what is
	// actually on screen rather than by a number picked in advance. A page
	// that is not a screenful is not a page.
	paneH atomic.Int32
	bandH atomic.Int32
	gate  *pauseGate

	// Results, set once when the scan finishes. Guarded by mu. Forensics are
	// gathered when a problem is first selected rather than during the run,
	// so a scan pays nothing for a screen it may never show.
	res       results
	sel       int
	forensics map[int]forensics

	// Descriptive fields for the dashboard header, fixed for the run.
	root string
	mode string

	// Guarded by mu: the recent-verdict ring and the throughput history. The
	// ring keeps each line's outcome so the stream can be filtered to one
	// category without re-deriving it from the rendered text.
	recent   []recentLine
	rateHist []int64
	lastB    int64
	lastT    time.Time

	// rate is smoothed over the whole run rather than sampled per tick: an
	// instantaneous rate on a mix of huge and tiny files swings hard enough
	// to make the remaining estimate useless. The sparkline is the opposite:
	// it wants the per-second variation, which is what shows a device
	// stalling, so it keeps its own short history.
	mu sync.Mutex
}

// recentCap is the ring the dashboard's stream pane draws from.
const recentCap = 200

// statsSnapshot is one consistent read of everything the panes display. Taking
// it once per frame stops a frame showing a file count from after a completion
// beside a byte count from before it.
type statsSnapshot struct {
	doneFiles, doneBytes         int64
	ok, mismatch, missing, ioerr int64
	inflight                     int64
	rate, average                int64
	elapsed, wall                int64
	paused                       bool
	frac                         float64
	eta                          string
	hist                         []int64
}

func (d *Display) snapshot() statsSnapshot {
	var st statsSnapshot
	st.doneFiles = d.doneFiles.Load()
	st.doneBytes = d.doneBytes.Load()
	st.ok = d.nOK.Load()
	st.mismatch = d.nMismatch.Load()
	st.missing = d.nMissing.Load()
	st.ioerr = d.nIOErr.Load()
	for _, s := range d.slots {
		if s.active.Load() {
			st.inflight += s.done.Load()
		}
	}
	elapsed := time.Since(d.start)
	st.wall = int64(elapsed.Seconds())
	if d.gate != nil {
		st.paused = d.gate.paused()
		// Excluded from the elapsed that feeds the rate and the estimate: a
		// run paused for ten minutes has not slowed down, and an estimate
		// that says otherwise is worse than no estimate. The wall clock is
		// kept separately, since that is what a watch shows.
		elapsed -= d.gate.pausedFor()
		if elapsed < 0 {
			elapsed = 0
		}
	}
	st.elapsed = int64(elapsed.Seconds())

	// In-flight bytes count toward progress. Without them a single very large
	// file leaves the bar frozen for minutes while real work is happening.
	seen := st.doneBytes + st.inflight
	switch {
	case d.totalBytes > 0:
		st.frac = float64(seen) / float64(d.totalBytes)
	case d.total > 0:
		st.frac = float64(st.doneFiles) / float64(d.total)
	}
	// Clamped, because the sizes are a weighting heuristic rather than a
	// measurement: a file that grew between the walk and the hash makes the
	// in-flight sum overshoot the total, and a bar reading 270% is worse
	// than one that sits at full while the last file finishes.
	if st.frac > 1 {
		st.frac = 1
	}
	if st.frac < 0 {
		st.frac = 0
	}
	if elapsed > 0 {
		st.average = int64(float64(seen) / elapsed.Seconds())
	}
	// The displayed rate is the recent one, so it falls to zero when the work
	// does. A run average cannot: it would still read 4GiB/s several minutes
	// into a pause, which is the opposite of what the number is for. The
	// estimate keeps using the average, because smoothing is what an estimate
	// wants.
	st.rate = st.average
	d.mu.Lock()
	if n := len(d.rateHist); n > 0 {
		k := n
		if k > 3 {
			k = 3
		}
		var sum int64
		for _, v := range d.rateHist[n-k:] {
			sum += v
		}
		st.rate = sum / int64(k)
	}
	d.mu.Unlock()
	if st.paused {
		st.rate = 0
	}
	st.eta = "--"
	if st.average > 0 && d.totalBytes > 0 {
		if remain := d.totalBytes - seen; remain > 0 {
			st.eta = fmtDur(remain / st.average)
		} else {
			st.eta = "0s"
		}
	}
	d.mu.Lock()
	st.hist = append([]int64(nil), d.rateHist...)
	d.mu.Unlock()
	return st
}

// sampleRate records one point of instantaneous throughput for the sparkline.
func (d *Display) sampleRate() {
	now := time.Now()
	seen := d.doneBytes.Load()
	for _, s := range d.slots {
		if s.active.Load() {
			seen += s.done.Load()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastT.IsZero() {
		d.lastT, d.lastB = now, seen
		return
	}
	dt := now.Sub(d.lastT).Seconds()
	if dt < 0.5 {
		return
	}
	if d.gate != nil && d.gate.paused() {
		// Frozen, not sampled as zero. The sparkline exists to show how the
		// device behaved, and a long pause would push every real sample off
		// the end and leave a flat line saying nothing about the run. The
		// throughput reading is reported as zero separately, which is the
		// number that should track reality moment to moment.
		d.lastT, d.lastB = now, seen
		return
	}
	// Sampled whether or not the run is paused, so the recent rate decays to
	// zero instead of freezing at whatever it was when the pause began.
	d.rateHist = append(d.rateHist, int64(float64(seen-d.lastB)/dt))
	if len(d.rateHist) > 240 {
		d.rateHist = d.rateHist[len(d.rateHist)-240:]
	}
	d.lastT, d.lastB = now, seen
}

func NewDisplay(r *Renderer, c *colors, jobs, total int, totalBytes int64, root, mode string, dash bool) *Display {
	d := &Display{
		r:          r,
		c:          c,
		slots:      make([]*slot, jobs),
		total:      total,
		totalBytes: totalBytes,
		start:      time.Now(),
		stop:       make(chan struct{}),
		root:       root,
		mode:       mode,
		dash:       dash,
	}
	for i := range d.slots {
		s := &slot{}
		s.path.Store("")
		d.slots[i] = s
	}
	d.expanded.Store(true)
	d.showBand.Store(true)
	return d
}

func (d *Display) Begin(worker int, path string, size int64) {
	s := d.slots[worker]
	s.path.Store(path)
	s.size.Store(size)
	s.done.Store(0)
	s.start.Store(time.Now().UnixNano())
	s.active.Store(true)
}

func (d *Display) Progress(worker int, done int64) { d.slots[worker].done.Store(done) }

func (d *Display) Finish(worker int, v Verdict) {
	d.slots[worker].active.Store(false)
	d.slots[worker].path.Store("")
	d.doneFiles.Add(1)
	d.doneBytes.Add(v.Size)
	switch v.Outcome {
	case OutcomeOK:
		d.nOK.Add(1)
	case OutcomeMismatch:
		d.nMismatch.Add(1)
	case OutcomeMissing:
		d.nMissing.Add(1)
	case OutcomeIOError:
		d.nIOErr.Add(1)
	}
}

// footerHeight must agree with frame() exactly. A mismatch makes the first
// SetFooter tear the scrolling region down and re-establish it at a different
// size, which scrolls the screen a second time and loses a line of output.
//
//	blank + headline + stats + counts                       = 4
//	blank + headline + blank + N slots + blank + stats + counts = N + 6
func (d *Display) footerHeight() int {
	if !d.expanded.Load() {
		return 4
	}
	return len(d.slots) + 6
}

// Run drives the frame loop until Close. 20fps is fast enough that a bar reads
// as moving and slow enough that the cost stays invisible next to hashing.
func (d *Display) Run() {
	// Started synchronously, before the caller launches anything that reads
	// the terminal. Start asks the terminal where the cursor is and waits for
	// the reply on the same input stream keystrokes arrive on, so a key
	// watcher running concurrently takes the reply and the block falls back
	// to pinning itself to the bottom of an otherwise empty screen.
	//
	// Only the log view owns a scrolling region. Starting one while the
	// dashboard is up would scroll the primary screen behind it.
	if !d.dash {
		d.r.Start(d.footerHeight(), d.repaint)
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-t.C:
				d.sampleRate()
				d.repaint()
			}
		}
	}()
}

// repaint draws whichever view is current. The two are mutually exclusive
// owners of the terminal: the log view holds a scrolling region with a pinned
// footer, the dashboard holds the alternate buffer, and switching hands the
// terminal from one to the other rather than layering them.
func (d *Display) repaint() {
	if d.dash {
		rows, cols := d.r.Size()
		d.mu.Lock()
		sc := d.screen
		fresh := sc == nil
		if fresh {
			sc = NewScreen(d.r.out, rows, cols)
			d.screen = sc
		}
		d.mu.Unlock()
		// Claiming the alternate buffer is what makes the screen drawable;
		// Draw is a no-op until then. Enter is idempotent, so a repaint
		// after a view switch re-enters rather than needing its own path.
		sc.Enter()
		if !fresh {
			sc.Resize(rows, cols)
		}
		sc.Draw(d.renderDashboard(rows, cols))
		return
	}
	d.r.SetFooter(d.frame())
}

// Close stops the frame loop. In full-screen mode the screen itself is left
// alone, because the caller goes on using it for the results view.
func (d *Display) Close() {
	close(d.stop)
	d.wg.Wait()
	if !d.dash {
		d.r.Stop()
	}
}

// Screen is the alternate-screen buffer the full-screen view draws into, so the
// results view can carry on using it rather than tearing it down and opening
// another, which flashes the primary screen in between.
func (d *Display) Screen() *Screen {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.screen
}

// Commit records finished verdicts. In the scrolling view they are written to
// stdout above the status block. In the full-screen view they feed the stream
// pane and go nowhere else: that view owns the terminal and leaves it as it
// found it, so writing to stdout would leave behind output nobody asked to keep.
func (d *Display) Commit(lines []string) { d.commit(OutcomeOK, lines) }

// CommitVerdict records a verdict's lines along with the outcome they carry, so
// the stream can be filtered later.
func (d *Display) CommitVerdict(v Verdict, lines []string) { d.commit(v.Outcome, lines) }

func (d *Display) commit(o Outcome, lines []string) {
	d.mu.Lock()
	for _, l := range lines {
		d.recent = append(d.recent, recentLine{outcome: o, text: l})
	}
	if len(d.recent) > recentCap {
		d.recent = d.recent[len(d.recent)-recentCap:]
	}
	d.mu.Unlock()
	if !d.dash {
		d.r.Commit(lines)
	}
}

// ToggleExpanded shows or hides the worker rows, and does nothing once the
// scan has finished: the band then holds the problems, which are what the
// screen exists to show.
func (d *Display) ToggleExpanded() {
	if d.done.Load() {
		return
	}
	d.expanded.Store(!d.expanded.Load())
	d.showBand.Store(!d.showBand.Load())
	// Focus cannot stay on a pane that is no longer drawn.
	if !d.showBand.Load() && d.focus.Load() == focusBand {
		d.focus.Store(focusStream)
	}
	d.repaint()
}

// ShowResults moves the view into its results state without changing its shape.
func (d *Display) ShowResults(res results) {
	d.mu.Lock()
	d.res = res
	d.sel = 0
	d.mu.Unlock()
	d.done.Store(true)
	d.repaint()
}

// Move steps the problem selection, and does nothing while the scan is running.
func (d *Display) Move(n int) { d.selectBy(n, false) }

func (d *Display) SelectFirst() { d.selectBy(0, true) }
func (d *Display) SelectLast()  { d.selectBy(1<<30, true) }

func (d *Display) selectBy(i int, absolute bool) {
	if !d.done.Load() {
		return
	}
	d.mu.Lock()
	if total := len(d.res.counts.Failures); total > 0 {
		if absolute {
			d.sel = i
		} else {
			d.sel += i
		}
		if d.sel >= total {
			d.sel = total - 1
		}
		if d.sel < 0 {
			d.sel = 0
		}
	}
	d.mu.Unlock()
	d.repaint()
}

// TogglePause is only meaningful while the scan is running.
func (d *Display) TogglePause() {
	if d.done.Load() || d.gate == nil {
		return
	}
	d.gate.toggle()
	d.repaint()
}

// bar renders a proportional bar in block characters. Eighth-blocks give the
// bar sub-cell resolution, so it advances smoothly on a narrow terminal
// instead of jumping a whole cell at a time.
func bar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	eighths := int(frac * float64(width) * 8)
	full := eighths / 8
	rem := eighths % 8
	var b strings.Builder
	b.WriteString(strings.Repeat("█", full))
	if full < width {
		if rem > 0 {
			b.WriteRune([]rune(" ▏▎▍▌▋▊▉")[rem])
			full++
		}
		if full < width {
			b.WriteString(strings.Repeat("·", width-full))
		}
	}
	return b.String()
}

// The frame fills the terminal. What gets capped is the elements inside it: a
// 300-column terminal is not a reason to draw a 100-cell progress bar, or to
// push a size column three hundred cells from the bar it belongs to, because
// the eye cannot associate them across that gap. Capping the frame instead
// leaves a band of dead terminal down one side, which is worse.
const (
	maxBarWidth   = 48
	maxStatsWidth = 38
	maxPathWidth  = 72
)

func capAt(v, max int) int {
	if v > max {
		return max
	}
	return v
}

func uiWidth(cols int) int {
	if cols < 20 {
		return 20
	}
	return cols
}

func (d *Display) frame() []string {
	_, cols := d.r.Size()
	cols = uiWidth(cols)
	done := d.doneFiles.Load()
	dBytes := d.doneBytes.Load()
	elapsed := time.Since(d.start)
	secs := int64(elapsed.Seconds())

	// In-flight bytes count toward the bar. Without them a single very large
	// file leaves the bar frozen for minutes while real work is happening.
	var inflight int64
	for _, s := range d.slots {
		if s.active.Load() {
			inflight += s.done.Load()
		}
	}

	frac := 0.0
	if d.totalBytes > 0 {
		frac = float64(dBytes+inflight) / float64(d.totalBytes)
	} else if d.total > 0 {
		frac = float64(done) / float64(d.total)
	}

	var rate int64
	if elapsed > 0 {
		rate = int64(float64(dBytes+inflight) / elapsed.Seconds())
	}
	eta := "--"
	if rate > 0 && d.totalBytes > 0 {
		remain := d.totalBytes - dBytes - inflight
		if remain > 0 {
			eta = fmtDur(remain / rate)
		} else {
			eta = "0s"
		}
	}

	var rows []string
	rows = append(rows, "")

	// Headline bar.
	head := fmt.Sprintf(" %s %3.0f%%  %s / %s",
		bar(frac, maxInt(10, cols/3)), frac*100,
		humanBytes(dBytes+inflight), humanBytes(d.totalBytes))
	rows = append(rows, truncVisible(head, cols-1))

	if d.expanded.Load() {
		rows = append(rows, "")
		for i, s := range d.slots {
			rows = append(rows, truncVisible(d.slotRow(i, s, cols), cols-1))
		}
		rows = append(rows, "")
	}

	stats := fmt.Sprintf(" %s/s · %d/%d files · %s elapsed · eta %s",
		humanBytes(rate), done, d.total, fmtDur(secs), eta)
	rows = append(rows, truncVisible(stats, cols-1))

	counts := fmt.Sprintf(" %s %d   %s %d   %s %d   %s %d",
		d.c.green("ok"), d.nOK.Load(),
		d.c.red("mismatch"), d.nMismatch.Load(),
		d.c.yellow("missing"), d.nMissing.Load(),
		d.c.yellow("io"), d.nIOErr.Load())
	hint := "[space] detail  [q] quit"
	if pad := cols - 1 - visibleLen(counts) - len(hint); pad > 0 {
		counts += strings.Repeat(" ", pad) + d.c.dim(hint)
	}
	rows = append(rows, truncVisible(counts, cols-1))
	return rows
}

func (d *Display) slotRow(i int, s *slot, cols int) string {
	label := fmt.Sprintf(" %2d ", i+1)
	if !s.active.Load() {
		return label + d.c.dim("idle")
	}
	p, _ := s.path.Load().(string)
	size := s.size.Load()
	dn := s.done.Load()
	frac := 0.0
	if size > 0 {
		frac = float64(dn) / float64(size)
	}
	const bw, sizeW = 10, 9
	sizeStr := humanBytes(size)
	// Fixed columns first, filename gets whatever is left.
	fixed := len([]rune(label)) + bw + len(" 100%  ") + sizeW + 2
	nameW := capAt(cols-1-fixed, maxPathWidth)
	if nameW < 8 {
		nameW = 8
	}
	return fmt.Sprintf("%s%s %3.0f%%  %s  %s",
		label, d.c.cyan(bar(frac, bw)), frac*100,
		padRight(tailRunes(displayPath(p), nameW), nameW),
		d.c.dim(padRight(sizeStr, sizeW)))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// recentLine is one rendered output line plus the outcome it belongs to.
type recentLine struct {
	outcome Outcome
	text    string
}

// Panes the arrow keys can act on.
const (
	focusStream int32 = iota
	focusStats
	focusBand
	focusCount
)

// statFilters are the counters the statistics pane offers, in the order they
// are drawn. The first is "everything", so there is always a way back.
var statFilters = []struct {
	label string
	all   bool
	out   Outcome
}{
	{label: "everything", all: true},
	{label: "verified", out: OutcomeOK},
	{label: "mismatched", out: OutcomeMismatch},
	{label: "missing", out: OutcomeMissing},
	{label: "io errors", out: OutcomeIOError},
}

// CycleFocus moves the arrow keys to the next pane. The band is skipped while
// it is hidden, so tab never parks focus somewhere invisible.
func (d *Display) CycleFocus() {
	next := d.focus.Load()
	// At most one full cycle: if nothing else is focusable, focus stays put.
	for i := int32(0); i < focusCount; i++ {
		next = (next + 1) % focusCount
		if d.focusable(next) {
			break
		}
	}
	d.focus.Store(next)
	d.repaint()
}

// focusable reports whether a pane is drawn and has something to point at. A
// clean run's band holds no problems, so tab must not offer it: a focus you
// cannot see and keys that do nothing is worse than two panes.
func (d *Display) focusable(f int32) bool {
	switch f {
	case focusBand:
		if !d.showBand.Load() {
			return false
		}
		if !d.done.Load() {
			return len(d.slots) > 0
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.res.counts != nil && len(d.res.counts.Failures) > 0
	default:
		return true
	}
}

// pageSize is one screenful of whatever has focus.
func (d *Display) pageSize() int {
	h := int(d.paneH.Load())
	if d.focus.Load() == focusBand {
		h = int(d.bandH.Load())
	}
	if h < 1 {
		h = 1
	}
	return h
}

// Scroll moves whatever has focus.
func (d *Display) Scroll(n int) {
	switch d.focus.Load() {
	case focusStats:
		sel := d.statSel.Load() + int32(n)
		if sel < 0 {
			sel = 0
		}
		if int(sel) >= len(statFilters) {
			sel = int32(len(statFilters) - 1)
		}
		d.statSel.Store(sel)
	case focusBand:
		d.selectBy(n, false)
		return
	default:
		off := d.streamOff.Load() - int64(n)
		if off < 0 {
			off = 0
		}
		d.mu.Lock()
		max := int64(len(d.recent))
		d.mu.Unlock()
		if off > max {
			off = max
		}
		d.streamOff.Store(off)
	}
	d.repaint()
}

// ScrollHome and ScrollEnd jump the focused pane to its ends.
func (d *Display) ScrollHome() {
	switch d.focus.Load() {
	case focusStats:
		d.statSel.Store(0)
	case focusBand:
		d.selectBy(0, true)
		return
	default:
		d.mu.Lock()
		max := int64(len(d.recent))
		d.mu.Unlock()
		d.streamOff.Store(max)
	}
	d.repaint()
}

func (d *Display) ScrollEnd() {
	switch d.focus.Load() {
	case focusStats:
		d.statSel.Store(int32(len(statFilters) - 1))
	case focusBand:
		d.selectBy(1<<30, true)
		return
	default:
		d.streamOff.Store(0)
	}
	d.repaint()
}

// filter is the outcome the stream and the problem list are limited to, and
// whether any limit applies at all.
func (d *Display) filter() (Outcome, bool) {
	f := statFilters[int(d.statSel.Load())%len(statFilters)]
	return f.out, !f.all
}
