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
	stop     chan struct{}
	wg       sync.WaitGroup

	// rate is smoothed over the whole run rather than sampled per tick: an
	// instantaneous rate on a mix of huge and tiny files swings hard enough
	// to make the remaining estimate useless.
	mu sync.Mutex
}

func NewDisplay(r *Renderer, c *colors, jobs, total int, totalBytes int64) *Display {
	d := &Display{
		r:          r,
		c:          c,
		slots:      make([]*slot, jobs),
		total:      total,
		totalBytes: totalBytes,
		start:      time.Now(),
		stop:       make(chan struct{}),
	}
	for i := range d.slots {
		s := &slot{}
		s.path.Store("")
		d.slots[i] = s
	}
	d.expanded.Store(true)
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
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.r.Start(d.footerHeight(), func() { d.r.SetFooter(d.frame()) })
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-d.stop:
				return
			case <-t.C:
				d.r.SetFooter(d.frame())
			}
		}
	}()
}

func (d *Display) Close() {
	close(d.stop)
	d.wg.Wait()
	d.r.Stop()
}

// Commit prints finished verdicts above the pinned footer.
func (d *Display) Commit(lines []string) { d.r.Commit(lines) }

func (d *Display) ToggleExpanded() {
	d.expanded.Store(!d.expanded.Load())
	d.r.SetFooter(d.frame())
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

func (d *Display) frame() []string {
	_, cols := d.r.Size()
	if cols < 20 {
		cols = 20
	}
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
	nameW := cols - 1 - fixed
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
