package main

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"
)

// Renderer keeps a block of status rows directly beneath the output that has
// scrolled past, and pins it to the bottom of the screen once the output has
// grown to fill it.
//
// The two-phase behavior is the whole point. A naive pinned block jumps to the
// bottom of the terminal immediately, so a run started on an otherwise empty
// screen shows its header at the top, the status block at the bottom, and a
// screenful of nothing in between. Here the block starts wherever the cursor
// already was and walks down as lines are committed, reaching the bottom only
// when the screen is genuinely full.
//
// Once pinned, DECSTBM confines scrolling to the rows above the block, so the
// block is never erased and never redrawn wholesale. The top margin stays at
// row 1 so that lines scrolled off the top still reach the terminal's
// scrollback, which is where the verdict log lives.
type Renderer struct {
	mu         sync.Mutex
	out        *os.File
	rows, cols int
	footerH    int
	// footTop is the first row of the status block, 1-based. Committed lines
	// are written at footTop and push it down until it reaches the bottom.
	footTop int
	prev    []string
	active  bool
	winch   chan os.Signal
	onResiz func()
}

func NewRenderer(out *os.File) *Renderer {
	r := &Renderer{out: out}
	r.rows, r.cols = terminalSize(out)
	return r
}

func (r *Renderer) Size() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rows, r.cols
}

// pinnedLocked reports whether the block has reached the bottom of the screen,
// after which committed lines scroll the region above it rather than pushing
// it further down.
func (r *Renderer) pinnedLocked() bool {
	return r.footTop+r.footerH-1 >= r.rows
}

func (r *Renderer) Start(h int, onResize func()) {
	r.mu.Lock()
	r.onResiz = onResize

	// Where the cursor already is, so the block lands under the existing
	// output instead of at the bottom of an empty screen. A terminal that
	// does not answer leaves row 0, and the fallback below pins immediately,
	// which is the old behavior and still correct.
	row, ok := queryCursorRow(r.out)
	if !ok || row < 1 || row > r.rows {
		row = r.rows
	}
	r.footTop = row
	r.footerH = clampFooter(h, r.rows)
	r.makeRoomLocked()

	var b bytes.Buffer
	b.WriteString("\033[?25l")
	writeCSI(&b, "\033[1;%dr", r.rows-r.footerH)
	r.out.Write(b.Bytes())
	r.prev = nil
	r.active = true
	r.mu.Unlock()

	r.winch = make(chan os.Signal, 1)
	signal.Notify(r.winch, syscall.SIGWINCH)
	go func() {
		for range r.winch {
			r.mu.Lock()
			rows, cols := terminalSize(r.out)
			changed := rows != r.rows || cols != r.cols
			if changed {
				// Terminals drop the margins on resize, so the region has to
				// be re-established rather than trusted. The block is put at
				// the bottom of the new screen, since the old position
				// describes a screen that no longer exists.
				r.rows, r.cols = rows, cols
				r.footerH = clampFooter(r.footerH, r.rows)
				r.footTop = r.rows - r.footerH + 1
				var b bytes.Buffer
				writeCSI(&b, "\033[1;%dr", r.rows-r.footerH)
				r.out.Write(b.Bytes())
				r.prev = nil
			}
			cb := r.onResiz
			r.mu.Unlock()
			if changed && cb != nil {
				cb()
			}
		}
	}()
}

func clampFooter(h, rows int) int {
	if h >= rows {
		h = rows - 1
	}
	if h < 1 {
		h = 1
	}
	return h
}

// makeRoomLocked scrolls the screen only by however much the block overhangs
// the bottom, never by a whole block height.
//
// Scrolling by the full height on every change is what made toggling the
// detail block throw the verdict log off the top of the screen: each toggle
// pushed the screen up by the new block's height whether or not any room was
// needed.
func (r *Renderer) makeRoomLocked() {
	over := r.footTop + r.footerH - 1 - r.rows
	if over <= 0 {
		return
	}
	var b bytes.Buffer
	for i := 0; i < over; i++ {
		b.WriteByte('\n')
	}
	r.out.Write(b.Bytes())
	r.footTop -= over
	if r.footTop < 1 {
		r.footTop = 1
	}
}

func (r *Renderer) closeLocked() {
	if !r.active {
		return
	}
	var b bytes.Buffer
	// Drop the margins, park below the block, wipe what is left of it, and
	// restore the cursor. All of it on every exit path, or the terminal is
	// left with a scrolling region and no cursor.
	b.WriteString("\033[r")
	writeCSI(&b, "\033[%d;1H", r.footTop)
	b.WriteString("\033[J\033[?25h")
	r.out.Write(b.Bytes())
	r.active = false
	r.prev = nil
}

// Stop releases the terminal. Safe to call more than once.
func (r *Renderer) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.winch != nil {
		signal.Stop(r.winch)
		close(r.winch)
		r.winch = nil
	}
	r.closeLocked()
}

// Commit writes finished lines above the status block. Before the block
// reaches the bottom each line takes the block's top row and pushes it down;
// after that each line scrolls the region and the block stays put.
func (r *Renderer) Commit(lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		for _, l := range lines {
			r.out.WriteString(l + "\n")
		}
		return
	}
	var b bytes.Buffer
	for _, l := range lines {
		if r.pinnedLocked() {
			writeCSI(&b, "\033[%d;1H", r.rows-r.footerH)
			b.WriteString("\n")
		} else {
			writeCSI(&b, "\033[%d;1H", r.footTop)
			r.footTop++
		}
		writeRow(&b, l, r.cols)
	}
	r.out.Write(b.Bytes())
	// The block moved, so the cached rows describe rows that now hold output.
	r.prev = nil
}

// SetFooter repaints the block, writing only rows whose text changed and
// emitting the whole frame in one write. A write per row lets the terminal
// paint a partial frame, which is what the eye reads as flicker.
func (r *Renderer) SetFooter(rows []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	var b bytes.Buffer
	if n := clampFooter(len(rows), r.rows); n != r.footerH {
		old := r.footerH
		r.footerH = n
		// Growing may need room at the bottom; shrinking never does, and must
		// not scroll, or a toggle would throw away output for no reason.
		r.makeRoomLocked()
		for i := n; i < old; i++ {
			writeCSI(&b, "\033[%d;1H", r.footTop+i)
			b.WriteString("\033[K")
		}
		writeCSI(&b, "\033[1;%dr", r.rows-r.footerH)
		r.prev = nil
	}
	for i, row := range rows {
		if i >= r.footerH {
			break
		}
		if i < len(r.prev) && r.prev[i] == row {
			continue
		}
		writeCSI(&b, "\033[%d;1H", r.footTop+i)
		writeRow(&b, row, r.cols)
	}
	if b.Len() > 0 {
		r.out.Write(b.Bytes())
	}
	r.prev = append(r.prev[:0], rows...)
}

func writeCSI(b *bytes.Buffer, format string, args ...any) {
	fmt.Fprintf(b, format, args...)
}

// queryCursorRow asks the terminal where the cursor is (DSR, ESC[6n) and reads
// the ESC[row;colR reply.
//
// It must run before anything else claims the terminal for reading, since the
// reply arrives on the same input stream as keystrokes and whoever reads first
// takes it. A terminal that does not reply costs the deadline once, at startup.
func queryCursorRow(out *os.File) (int, bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0, false
	}
	defer tty.Close()

	// Bounded by the terminal itself: two tenths of a second, after which a
	// read returns empty. A terminal that never answers costs that once.
	restore, ok := rawModeTimed(tty, 2)
	if !ok {
		return 0, false
	}
	defer restore()

	if _, err := out.WriteString("\033[6n"); err != nil {
		return 0, false
	}

	var buf []byte
	tmp := make([]byte, 32)
	for len(buf) < 32 {
		n, err := tty.Read(tmp)
		if n == 0 || err != nil {
			break
		}
		buf = append(buf, tmp[:n]...)
		if bytes.IndexByte(buf, 'R') >= 0 {
			break
		}
	}
	var row, col int
	if _, err := fmt.Sscanf(string(buf), "\033[%d;%dR", &row, &col); err != nil {
		return 0, false
	}
	return row, true
}

// terminalSize asks the terminal itself rather than the environment, which goes
// stale the moment the window is resized.
func terminalSize(f *os.File) (rows, cols int) {
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	if err != 0 || ws.Row == 0 || ws.Col == 0 {
		return 24, 80
	}
	return int(ws.Row), int(ws.Col)
}

func isTerminal(f *os.File) bool {
	var ws struct{ Row, Col, X, Y uint16 }
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		uintptr(syscall.TIOCGWINSZ), uintptr(unsafe.Pointer(&ws)))
	return err == 0
}
