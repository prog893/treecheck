package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Failure is one problem, kept in full for the review screen. The printed recap
// caps how many paths it names; this does not, because the whole point of the
// screen is to go through them.
type Failure struct {
	Path               string
	Token              string
	Outcome            Outcome
	Detail             []string
	Err                error
	Recorded, Computed string
	Size               int64
}

// forensics is what the review screen can tell you about a failure beyond the
// fact that it failed. Gathered when a row is selected rather than during the
// run, so a scan pays nothing for it.
type forensics struct {
	fileOK     bool
	fileSize   int64
	fileMode   os.FileMode
	fileMod    time.Time
	sideOK     bool
	sideSize   int64
	sideMod    time.Time
	sideBody   string
	sideErr    error
	fileErr    error
	assessment []string
}

func gather(f Failure) forensics {
	var fo forensics
	side := f.Path + hashExt

	if fi, err := os.Lstat(f.Path); err == nil {
		fo.fileOK = true
		fo.fileSize, fo.fileMode, fo.fileMod = fi.Size(), fi.Mode(), fi.ModTime()
	} else {
		fo.fileErr = err
	}
	if fi, err := os.Lstat(side); err == nil {
		fo.sideOK = true
		fo.sideSize, fo.sideMod = fi.Size(), fi.ModTime()
		if b, rerr := os.ReadFile(side); rerr == nil {
			fo.sideBody = strings.TrimSpace(string(b))
		} else {
			fo.sideErr = rerr
		}
	} else {
		fo.sideErr = err
	}

	fo.assessment = assess(f, fo)
	return fo
}

// assess is the reason this screen exists. A mismatch alone does not say
// whether the data rotted or somebody edited the file, and those call for
// opposite responses: restore from backup, or re-create the sidecar.
//
// The timestamps separate them. A file whose contents changed *and* whose mtime
// moved past the sidecar was rewritten, which is what an edit, a re-encode or a
// re-export looks like. A file whose contents changed while its mtime did not
// was never rewritten by anything that updates metadata, and that is the
// signature of corruption: bad blocks, a failing controller, a bad cable.
//
// Stated as evidence and its likely reading, never as a verdict. The mtime can
// be preserved deliberately (rsync -t, a restore from archive), so this narrows
// the question rather than answering it.
func assess(f Failure, fo forensics) []string {
	switch f.Outcome {
	case OutcomeMismatch:
		if !fo.fileOK || !fo.sideOK {
			return []string{"the file or its sidecar has gone since the scan"}
		}
		// Compared with a tolerance, not for equality. Timestamps carry
		// sub-second precision, so a file written microseconds before its
		// sidecar is not "newer" in any sense a reader cares about, and
		// reporting it as "0s newer" is worse than saying nothing.
		const tol = time.Second
		gap := fo.fileMod.Sub(fo.sideMod)
		switch {
		case gap > tol:
			return []string{
				fmt.Sprintf("the file was modified %s after its sidecar was written",
					gap.Round(time.Second)),
				"consistent with an edit, a re-encode or a restore, rather than with decay",
				"if the change was intended, re-create the sidecar with -c -f",
			}
		case gap < -tol:
			return []string{
				fmt.Sprintf("the sidecar is %s newer than the file",
					(-gap).Round(time.Second)),
				"the contents changed without the file being rewritten",
				"treat as corruption unless the timestamp was preserved deliberately",
			}
		default:
			return []string{
				"the file's contents changed but its timestamp did not",
				"nothing rewrote this file through the filesystem",
				"this is what corruption looks like: restore from a known-good copy",
			}
		}
	case OutcomeIOError:
		if fo.fileErr != nil {
			return []string{"the file could not be examined: " + fo.fileErr.Error()}
		}
		if !fo.fileOK {
			return []string{"the file is gone"}
		}
		if fo.fileMode.Perm()&0o400 == 0 {
			return []string{
				fmt.Sprintf("mode is %v, so it is not readable by this user", fo.fileMode.Perm()),
				"a permissions problem, not a data problem",
			}
		}
		if f.Err != nil {
			return []string{
				"the read failed: " + f.Err.Error(),
				"readable by permission but not in fact readable: suspect the device",
			}
		}
		return []string{"the read failed for a reason this tool could not narrow down"}
	case OutcomeMissing:
		switch {
		case !fo.sideOK:
			return []string{
				"no sidecar exists for this file",
				"nothing is known to be wrong with it; create one with -c",
			}
		case fo.sideSize == 0:
			return []string{
				"the sidecar exists but is empty",
				"a write that did not complete; re-create it with -c -f",
			}
		default:
			return []string{
				"the sidecar holds something that is not a SHA-256 digest",
				fmt.Sprintf("%d bytes, refused rather than compared", fo.sideSize),
				"re-create it with -c -f once the file is known to be good",
			}
		}
	}
	return nil
}

// review is the post-run browser. It owns the terminal for as long as it runs
// and restores it on every exit path.
type review struct {
	failures []Failure
	sel      int
	scroll   int
	sc       *Screen
	c        *colors
	out      *os.File
	root     string
	cache    map[int]forensics
}

func runReview(out *os.File, c *colors, root string, failures []Failure) {
	if len(failures) == 0 {
		return
	}
	restore, ok := rawMode(out)
	if !ok {
		return
	}
	defer restore()

	rows, cols := terminalSize(out)
	r := &review{
		failures: failures, c: c, out: out, root: root,
		sc:    NewScreen(out, rows, cols),
		cache: map[int]forensics{},
	}
	r.sc.Enter()
	defer r.sc.Leave()

	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()

	r.draw()
	buf := make([]byte, 8)
	for {
		n, err := tty.Read(buf)
		if err != nil || n == 0 {
			return
		}
		switch decodeKey(buf[:n]) {
		case keyUp:
			r.move(-1)
		case keyDown:
			r.move(1)
		case keyPageUp:
			r.move(-10)
		case keyPageDown:
			r.move(10)
		case keyHome:
			r.sel = 0
		case keyEnd:
			r.sel = len(r.failures) - 1
		case keyQuit:
			return
		}
		rows, cols := terminalSize(out)
		r.sc.Resize(rows, cols)
		r.draw()
	}
}

type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keyPageUp
	keyPageDown
	keyHome
	keyEnd
	keyQuit
)

// decodeKey handles the arrow keys, which arrive as escape sequences rather
// than single bytes, alongside the vi-style and plain letters.
func decodeKey(b []byte) key {
	if len(b) >= 3 && b[0] == 0x1b && b[1] == '[' {
		switch b[2] {
		case 'A':
			return keyUp
		case 'B':
			return keyDown
		case '5':
			return keyPageUp
		case '6':
			return keyPageDown
		case 'H':
			return keyHome
		case 'F':
			return keyEnd
		}
		return keyNone
	}
	if len(b) == 1 {
		switch b[0] {
		case 'k':
			return keyUp
		case 'j':
			return keyDown
		case 'g':
			return keyHome
		case 'G':
			return keyEnd
		case 'q', 'Q', 0x1b, 3, 4:
			return keyQuit
		}
	}
	return keyNone
}

func (r *review) move(n int) {
	r.sel += n
	if r.sel < 0 {
		r.sel = 0
	}
	if r.sel >= len(r.failures) {
		r.sel = len(r.failures) - 1
	}
}

func (r *review) forensicsFor(i int) forensics {
	if f, ok := r.cache[i]; ok {
		return f
	}
	f := gather(r.failures[i])
	r.cache[i] = f
	return f
}

func (r *review) draw() { r.sc.Draw(r.render()) }

func (r *review) render() []string {
	rows, cols := r.sc.rows, r.sc.cols
	if cols < 40 || rows < 10 {
		return []string{truncVisible(fmt.Sprintf(" %d problems; terminal too small to review",
			len(r.failures)), cols)}
	}

	var mism, ioerr, missing int
	for _, f := range r.failures {
		switch f.Outcome {
		case OutcomeMismatch:
			mism++
		case OutcomeIOError:
			ioerr++
		case OutcomeMissing:
			missing++
		}
	}

	listH := (rows - 4) / 2
	if listH < 3 {
		listH = 3
	}
	if listH > 12 {
		listH = 12
	}
	// Keep the selection on screen.
	if r.sel < r.scroll {
		r.scroll = r.sel
	}
	if r.sel >= r.scroll+listH {
		r.scroll = r.sel - listH + 1
	}

	out := make([]string, 0, rows)
	title := fmt.Sprintf("problems · %d mismatched · %d io · %d no usable sidecar",
		mism, ioerr, missing)
	out = append(out, hrule(bTL, bTR, cols, title, nil))

	for i := 0; i < listH; i++ {
		idx := r.scroll + i
		if idx >= len(r.failures) {
			out = append(out, boxRow(cols, ""))
			continue
		}
		f := r.failures[idx]
		marker := "  "
		if idx == r.sel {
			marker = r.c.cyan("▸ ")
		}
		tok := padRight(f.Token, verdictWidth)
		switch f.Outcome {
		case OutcomeMismatch:
			tok = r.c.red(tok)
		case OutcomeIOError:
			tok = r.c.yellow(tok)
		}
		rel := f.Path
		if p, err := filepath.Rel(r.root, f.Path); err == nil && !strings.HasPrefix(p, "..") {
			rel = p
		}
		line := fmt.Sprintf("%s%s %s", marker, tok, displayPath(rel))
		if idx == r.sel {
			line = padVisible(truncVisible(line, cols-2), cols-2)
		}
		out = append(out, boxRow(cols, line))
	}

	out = append(out, hrule(bLT, bRT, cols, "detail", nil))
	for _, l := range r.detailLines(cols-2, rows-len(out)-3) {
		out = append(out, boxRow(cols, l))
	}
	out = append(out, hrule(bLT, bRT, cols, "", nil))
	out = append(out, boxRow(cols, r.c.dim(fmt.Sprintf(
		" %d/%d   [↑↓ jk] move  [g G] first last  [q] quit",
		r.sel+1, len(r.failures)))))
	out = append(out, hrule(bBL, bBR, cols, "", nil))
	return out
}

func (r *review) detailLines(w, h int) []string {
	f := r.failures[r.sel]
	fo := r.forensicsFor(r.sel)
	var out []string
	add := func(format string, args ...any) {
		out = append(out, truncVisible(" "+fmt.Sprintf(format, args...), w))
	}

	add("%s", r.c.cyan(displayPath(f.Path)))
	out = append(out, "")

	if fo.fileOK {
		add("file      %s   mode %v   %s",
			exactBytes(fo.fileSize), fo.fileMode.Perm(), fo.fileMod.Format(time.RFC3339))
	} else {
		add("file      %s", r.c.yellow("not present"))
	}
	if fo.sideOK {
		add("sidecar   %s   %s", exactBytes(fo.sideSize), fo.sideMod.Format(time.RFC3339))
	} else {
		add("sidecar   %s", r.c.yellow("not present"))
	}
	if f.Recorded != "" {
		out = append(out, "")
		add("recorded  %s", f.Recorded)
		add("computed  %s", r.c.red(f.Computed))
	}
	if len(fo.assessment) > 0 {
		out = append(out, "")
		for i, a := range fo.assessment {
			if i == 0 {
				add("%s", r.c.yellow(a))
			} else {
				add("%s", a)
			}
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	if len(out) > h {
		out = out[:h]
	}
	return out
}
