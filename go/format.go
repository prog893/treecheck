package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// displayPath neutralizes control bytes in a filename.
//
// A filename is untrusted input. Printed straight to a terminal it can carry
// ESC and rewrite the report about itself, and a name that erases its own
// MISMATCH line is precisely the silent failure this tool exists to prevent.
// Quoting the whole path with %q instead would wrap every ordinary path
// containing a space, which on a media tree is most of them.
func displayPath(p string) string {
	if !strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || r == utf8.RuneError }) {
		return p
	}
	var b strings.Builder
	b.Grow(len(p))
	for _, r := range p {
		if unicode.IsControl(r) || r == utf8.RuneError {
			b.WriteByte('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// humanBytes renders a byte count the way the shell implementation did, to
// one decimal place and in binary units, so output stays comparable.
func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k*k:
		return fmt.Sprintf("%dKiB", n/k)
	case n < k*k*k:
		return fmt.Sprintf("%d.%dMiB", n/(k*k), (n%(k*k))*10/(k*k))
	case n < k*k*k*k:
		return fmt.Sprintf("%d.%dGiB", n/(k*k*k), (n%(k*k*k))*10/(k*k*k))
	default:
		return fmt.Sprintf("%d.%dTiB", n/(k*k*k*k), (n%(k*k*k*k))*10/(k*k*k*k))
	}
}

// fmtDur reads as a duration rather than a raw second count, which stops being
// legible somewhere around the four-digit mark on a large volume.
func fmtDur(seconds int64) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm%02ds", seconds/60, seconds%60)
	default:
		return fmt.Sprintf("%dh%02dm", seconds/3600, seconds%3600/60)
	}
}

// truncRunes cuts to a rune count, never mid-rune, and marks the cut. The tail
// of a path is where the filename is, so an over-long path keeps its end.
func truncRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w <= 3 {
		return string(r[:w])
	}
	return string(r[:w-1]) + "…"
}

func tailRunes(s string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 3 {
		return string(r[len(r)-w:])
	}
	return "…" + string(r[len(r)-w+1:])
}

func padRight(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// visibleLen counts printable cells, skipping ANSI escape sequences.
//
// Truncating a colored row by raw rune count cuts inside an escape sequence or
// discards the tail of a row that was never too wide to begin with: the escapes
// occupy no cells but count as runes. Every width decision in the display goes
// through this instead of len() or RuneCountInString.
func visibleLen(s string) int {
	n, inEsc := 0, false
	for _, r := range s {
		switch {
		case inEsc:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc = true
		default:
			n++
		}
	}
	return n
}

// truncVisible cuts to a visible width while carrying escape sequences through,
// and closes any style it cut in the middle of so the colour cannot bleed into
// the rest of the terminal.
func truncVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visibleLen(s) <= w {
		return s
	}
	var b strings.Builder
	n, inEsc, sawEsc := 0, false, false
	for _, r := range s {
		switch {
		case inEsc:
			b.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
		case r == 0x1b:
			inEsc, sawEsc = true, true
			b.WriteRune(r)
		default:
			if n >= w {
				if sawEsc {
					b.WriteString("\033[0m")
				}
				return b.String()
			}
			b.WriteRune(r)
			n++
		}
	}
	return b.String()
}

// padVisible pads to a visible width, ignoring escape sequences.
func padVisible(s string, w int) string {
	if n := visibleLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// exactBytes is for the review screen, where the exact count is the reading
// that matters: humanBytes rounds to KiB, so every small file showed as "0KiB",
// and a file truncated to a handful of bytes is diagnostic on its own.
func exactBytes(n int64) string {
	if n < 1<<20 {
		if n == 1 {
			return "1 byte"
		}
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%s (%d bytes)", humanBytes(n), n)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
