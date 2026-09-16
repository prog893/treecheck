package main

// Outcome is what a single file's check concluded. It drives both the summary
// counters and the exit status, and is deliberately separate from the token
// printed on the verdict line: "ok" and "created" are different words for the
// same outcome, and a creation that was never read back shares its token with
// one that was.
type Outcome int

const (
	OutcomeOK Outcome = iota
	OutcomeMismatch
	OutcomeMissing
	OutcomeIOError
	// OutcomeUnverified covers a sidecar this run wrote but did not read
	// back, and an existing sidecar -n declined to check. Neither is corrupt
	// and neither has been verified, so it counts as neither.
	OutcomeUnverified
)

// Verdict tokens. Fixed set, fixed column: an outcome token in a known place
// is what makes the output greppable and diffable between runs.
const (
	TokenOK       = "ok"
	TokenCreated  = "created"
	TokenMismatch = "MISMATCH"
	TokenMissing  = "missing"
	TokenIOError  = "io-error"
	TokenSkipped  = "skipped"
)

const verdictWidth = 8

// Verdict is one file's result. Index is its position in the walk, which is
// what lets results computed out of order be emitted in order.
type Verdict struct {
	Index   int
	Path    string
	Token   string
	Detail  []string
	Outcome Outcome
	// Created records that this run wrote a sidecar, independently of the
	// outcome: a creation that failed its read-back is still a creation.
	Created bool
	Size    int64

	// Kept for the review screen rather than for the verdict line. Err is the
	// underlying failure, which the printed line deliberately does not carry:
	// "file could not be read" reads the same for a permissions problem and a
	// failing disk, and telling those apart is the whole question when
	// something is wrong.
	Err                error
	Recorded, Computed string
}

// Render lays out the verdict line and any detail beneath it. Detail is
// indented to the path column so it reads as belonging to the line above.
func (v Verdict) Render(c *colors) []string {
	var tok string
	switch v.Token {
	case TokenMismatch:
		tok = c.red(padRight(v.Token, verdictWidth))
	case TokenIOError:
		tok = c.yellow(padRight(v.Token, verdictWidth))
	case TokenOK, TokenCreated:
		tok = c.green(padRight(v.Token, verdictWidth))
	default:
		tok = padRight(v.Token, verdictWidth)
	}
	lines := []string{tok + " " + displayPath(v.Path)}
	for _, d := range v.Detail {
		if d == "" {
			continue
		}
		lines = append(lines, padRight("", verdictWidth)+" "+d)
	}
	return lines
}
