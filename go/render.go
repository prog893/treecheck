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

// Renderer pins a block of rows to the bottom of the terminal and lets
// finished output scroll past above it.
//
// The mechanism is DECSTBM: setting the scrolling region to rows 1..(H-footer)
// confines every subsequent line feed to that range, so the footer is simply
// never scrolled and never has to be erased. The shell implementation could
// not do this safely and had to erase and redraw its whole status block after
// every completed line, which is what made it flicker.
//
// The top margin stays at row 1 deliberately: terminals that preserve
// scrolled-off lines in scrollback only do so when the region starts at the
// top, and the verdict list is the actual product of this tool.
type Renderer struct {
	mu      sync.Mutex
	out     *os.File
	rows    int
	cols    int
	footerH int
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

// Start claims h rows at the bottom. The screen is scrolled up by h first, so
// nothing already on it is overwritten.
func (r *Renderer) Start(h int, onResize func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onResiz = onResize
	r.openLocked(h)

	r.winch = make(chan os.Signal, 1)
	signal.Notify(r.winch, syscall.SIGWINCH)
	go func() {
		for range r.winch {
			r.mu.Lock()
			rows, cols := terminalSize(r.out)
			changed := rows != r.rows || cols != r.cols
			if changed {
				// Terminals drop the margins on resize, so the region
				// has to be re-established rather than trusted.
				r.closeLocked()
				r.rows, r.cols = rows, cols
				r.openLocked(r.footerH)
			}
			cb := r.onResiz
			r.mu.Unlock()
			if changed && cb != nil {
				cb()
			}
		}
	}()
}

func (r *Renderer) openLocked(h int) {
	if h >= r.rows {
		h = r.rows - 1
	}
	if h < 1 {
		h = 1
	}
	var b bytes.Buffer
	for i := 0; i < h; i++ {
		b.WriteByte('\n')
	}
	b.WriteString("\033[?25l")
	writeCSI(&b, "\033[1;%dr", r.rows-h)
	writeCSI(&b, "\033[%d;1H", r.rows-h)
	r.out.Write(b.Bytes())
	r.footerH = h
	r.prev = nil
	r.active = true
}

func (r *Renderer) closeLocked() {
	if !r.active {
		return
	}
	var b bytes.Buffer
	// Drop the margins, park below the footer, wipe it, restore the cursor.
	// The cursor must come back on every exit path, the interrupt one
	// included, or the terminal is left without one.
	b.WriteString("\033[r")
	writeCSI(&b, "\033[%d;1H", r.rows-r.footerH+1)
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

// Commit writes finished lines into the scrolling region above the footer.
// Each one scrolls that region by a row; the footer is untouched.
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
	b.WriteString("\0337") // save cursor
	for _, l := range lines {
		writeCSI(&b, "\033[%d;1H", r.rows-r.footerH)
		b.WriteString("\n")
		b.WriteString(truncRunes(l, r.cols))
		b.WriteString("\033[K")
	}
	b.WriteString("\0338") // restore cursor
	r.out.Write(b.Bytes())
}

// SetFooter repaints the pinned block, writing only rows whose text changed
// and emitting the whole frame in one write. A write per row lets the terminal
// paint a partial frame, which is what the eye reads as flicker.
func (r *Renderer) SetFooter(rows []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return
	}
	if len(rows) != r.footerH {
		r.closeLocked()
		r.openLocked(len(rows))
	}
	var b bytes.Buffer
	for i, row := range rows {
		if i < len(r.prev) && r.prev[i] == row {
			continue
		}
		writeCSI(&b, "\033[%d;1H", r.rows-r.footerH+1+i)
		b.WriteString(row)
		b.WriteString("\033[K")
	}
	if b.Len() > 0 {
		r.out.Write(b.Bytes())
	}
	r.prev = append(r.prev[:0], rows...)
}

func writeCSI(b *bytes.Buffer, format string, args ...any) {
	fmt.Fprintf(b, format, args...)
}

// terminalSize asks the terminal itself rather than the environment, which
// goes stale the moment the window is resized.
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
