package main

import (
	"fmt"
	"io"
)

// recapCap bounds how many paths a failure counter names. A count on its own
// sends the reader back through the whole log to find out which file rotted,
// which is the one question this tool exists to answer; an unbounded list on a
// badly degraded volume buries the summary instead.
const recapCap = 20

type Counters struct {
	Scanned    int
	OK         int
	Created    int
	Unverified int
	Mismatch   int
	Missing    int
	IOErr      int
	Unreached  int

	MismatchPaths []string
	IOErrPaths    []string
}

func (n *Counters) Add(v Verdict) {
	n.Scanned++
	if v.Created {
		n.Created++
	}
	switch v.Outcome {
	case OutcomeOK:
		n.OK++
	case OutcomeMismatch:
		n.Mismatch++
		if len(n.MismatchPaths) < recapCap {
			n.MismatchPaths = append(n.MismatchPaths, v.Path)
		}
	case OutcomeMissing:
		n.Missing++
	case OutcomeIOError:
		n.IOErr++
		if len(n.IOErrPaths) < recapCap {
			n.IOErrPaths = append(n.IOErrPaths, v.Path)
		}
	case OutcomeUnverified:
		n.Unverified++
	}
}

func writeRecap(w io.Writer, total int, paths []string) {
	for _, p := range paths {
		fmt.Fprintf(w, "  %s\n", displayPath(p))
	}
	if total > len(paths) {
		fmt.Fprintf(w, "  ... and %d more\n", total-len(paths))
	}
}

func (n *Counters) Write(w io.Writer, c *colors, create bool, elapsed int64) {
	if n.Scanned == 1 {
		fmt.Fprintf(w, "Scanned:         1 file\n")
	} else {
		fmt.Fprintf(w, "Scanned:         %d files\n", n.Scanned)
	}
	fmt.Fprintf(w, "Verified:        %d\n", n.OK)
	if create {
		fmt.Fprintf(w, "Created:         %d\n", n.Created)
	}
	if n.Unverified > 0 {
		fmt.Fprintf(w, "Not verified:    %d\n", n.Unverified)
	}
	if n.Mismatch > 0 {
		fmt.Fprintf(w, "Mismatched:      %d   %s\n", n.Mismatch, c.red("<- corrupt"))
	} else {
		fmt.Fprintf(w, "Mismatched:      0\n")
	}
	writeRecap(w, n.Mismatch, n.MismatchPaths)
	fmt.Fprintf(w, "Missing/empty:   %d\n", n.Missing)
	fmt.Fprintf(w, "I/O errors:      %d\n", n.IOErr)
	writeRecap(w, n.IOErr, n.IOErrPaths)
	if n.Unreached > 0 {
		fmt.Fprintf(w, "Not reached:     %d\n", n.Unreached)
	}
	fmt.Fprintf(w, "Elapsed:         %s\n", fmtDur(elapsed))
}

// ExitCode maps a finished run onto the documented interface.
//
//	0   clean
//	1   verification failed: a mismatch, an I/O error, or a failed walk
//	2   nothing corrupt, but some files have no usable sidecar
//	130 interrupted: only the files reported as scanned were checked
func (n *Counters) ExitCode(walkFailed, interrupted, strict, create bool) int {
	// A scan that did not reach every file checked less than it was asked
	// to, so it can never report the clean verdict. This is keyed on records
	// that actually came back, not only on a flag, because "checked less
	// than it claims, says nothing" is the one failure this tool must never
	// have.
	if interrupted {
		return 130
	}
	if n.Unreached > 0 {
		return 1
	}
	if walkFailed {
		return 1
	}
	if n.Mismatch > 0 || n.IOErr > 0 {
		return 1
	}
	if n.Missing > 0 && !create {
		if strict {
			return 1
		}
		return 2
	}
	return 0
}
