package main

import (
	"bytes"
	"os"
	"strings"
)

// Screen is a line-addressed double buffer for full-screen drawing.
//
// A frame is composed in full, then compared row by row against the frame
// already on the terminal, and only the rows that differ are written. Both
// halves matter: composing first means the whole update lands in one write, so
// the terminal cannot paint a half-finished frame, and diffing means a
// dashboard whose worker rows change while its statistics do not costs a
// handful of rows rather than a full repaint.
//
// It draws into the alternate screen buffer, which is why it is not the default
// view: the alternate buffer is discarded on exit, taking the verdict stream
// with it, and that stream is the product of this tool. The scrolling view
// keeps it in scrollback. This is for watching a long run, not for recording
// one.
type Screen struct {
	out        *os.File
	rows, cols int
	prev       []string
	entered    bool
}

func NewScreen(out *os.File, rows, cols int) *Screen {
	return &Screen{out: out, rows: rows, cols: cols}
}

func (s *Screen) Enter() {
	if s.entered {
		return
	}
	// Alternate buffer, cursor hidden, cleared.
	s.out.WriteString("\033[?1049h\033[?25l\033[2J")
	s.entered = true
	s.prev = nil
}

func (s *Screen) Leave() {
	if !s.entered {
		return
	}
	// Restore the primary buffer and the cursor. Both, on every exit path,
	// or the terminal is left in the alternate buffer with no cursor.
	s.out.WriteString("\033[?25h\033[?1049l")
	s.entered = false
	s.prev = nil
}

// Resize invalidates the cached frame: the old one describes a screen that no
// longer exists, so every row has to be laid down again.
func (s *Screen) Resize(rows, cols int) {
	// Only a real size change invalidates the frame. Clearing unconditionally
	// would blank the screen on every repaint, which is a full repaint per
	// frame and the flicker this buffer exists to avoid.
	if rows == s.rows && cols == s.cols {
		return
	}
	s.rows, s.cols = rows, cols
	s.prev = nil
	if s.entered {
		s.out.WriteString("\033[2J")
	}
}

func (s *Screen) Draw(frame []string) {
	if !s.entered {
		return
	}
	if len(frame) > s.rows {
		frame = frame[:s.rows]
	}
	var b bytes.Buffer
	for i, row := range frame {
		if i < len(s.prev) && s.prev[i] == row {
			continue
		}
		b.WriteString(sprintfCSI(i+1, 1))
		b.WriteString(truncVisible(row, s.cols))
		b.WriteString("\033[K")
	}
	// Clear any rows the previous frame used and this one does not.
	for i := len(frame); i < len(s.prev); i++ {
		b.WriteString(sprintfCSI(i+1, 1))
		b.WriteString("\033[K")
	}
	if b.Len() > 0 {
		s.out.Write(b.Bytes())
	}
	s.prev = append(s.prev[:0], frame...)
}

func sprintfCSI(row, col int) string {
	var b strings.Builder
	b.WriteString("\033[")
	b.WriteString(itoa(row))
	b.WriteByte(';')
	b.WriteString(itoa(col))
	b.WriteByte('H')
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
