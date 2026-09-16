package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
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

// results is everything the view needs to describe a finished run.
type results struct {
	counts  *Counters
	root    string
	mode    string
	elapsed int64
	status  int
}

// runResults keeps the live view on screen and turns it into the results view,
// then waits. The frame does not change shape: the same panes carry problems
// and evidence instead of workers and a stream, so nothing the reader was
// looking at moves at the moment they have something to act on.
func runResults(d *Display, res results) {
	// Wrapped, not "defer d.Screen().Leave()": the receiver of a deferred
	// method call is evaluated when the defer is registered, which is before
	// ShowResults has created the screen. That captured nil every time.
	defer func() { d.Screen().Leave() }()
	d.ShowResults(res)
	readKeys(d)
}

// runReviewStandalone shows the same results view for a run that printed its
// verdicts to stdout, where no live view was ever on screen.
func runReviewStandalone(out *os.File, c *colors, jobs int, res results) {
	rows, cols := terminalSize(out)
	d := NewDisplay(NewRenderer(out), c, jobs, res.counts.Scanned, 0,
		res.root, res.mode, true)
	d.screen = NewScreen(out, rows, cols)
	d.screen.Enter()
	defer d.screen.Leave()
	d.ShowResults(res)
	readKeys(d)
}

// readKeys owns the terminal for the results view and returns when asked to
// quit. A blocking read is right here: nothing else is happening, so there is
// no reason to wake up and redraw a screen that cannot have changed.
func readKeys(d *Display) {
	restore, ok := rawMode(d.r.out)
	if !ok {
		return
	}
	defer restore()

	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()

	fd := int(tty.Fd())
	buf := make([]byte, 8)
	for {
		n, err := syscall.Read(fd, buf)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || n <= 0 {
			return
		}
		switch decodeKey(buf[:n]) {
		case keyQuit:
			return
		case keyUp:
			d.Move(-1)
		case keyDown:
			d.Move(1)
		case keyPageUp:
			d.Move(-10)
		case keyPageDown:
			d.Move(10)
		case keyHome:
			d.SelectFirst()
		case keyEnd:
			d.SelectLast()
		case keyBand:
			d.ToggleExpanded()
		case keyWide:
			d.ToggleWide()
		}
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
	keyPause
	keyBand
	keyWide
)

// decodeKey handles the arrow keys, which arrive as escape sequences rather
// than single bytes, alongside the vi-style and plain letters.
//
// A bare ESC is deliberately not quit: it is the first byte of every arrow
// key, and a short read would otherwise end the session on an arrow press.
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
		case 'p', 'P':
			return keyPause
		case ' ':
			return keyBand
		case '\t':
			return keyWide
		case 'q', 'Q', 3, 4:
			return keyQuit
		}
	}
	return keyNone
}
