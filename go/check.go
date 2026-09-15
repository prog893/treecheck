package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const hashExt = ".sha256"

var errInterrupted = errors.New("interrupted")

// isSHA256 decides whether a sidecar holds a digest.
//
// A sidecar's contents are untrusted input as much as a filename is. Stripping
// whitespace leaves every other control byte in place, so a .sha256 file could
// carry ESC into a mismatch detail line. Anything that is not 64 hex
// characters is not a digest: refuse it as unusable rather than comparing it,
// and never render its bytes.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func readSidecar(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// progressFn is called with bytes hashed so far for the file in flight, which
// is what lets the display show progress *within* a multi-gigabyte file rather
// than a row that sits unchanged for minutes.
type progressFn func(done int64)

// hashFile computes the digest, reporting progress and honouring cancellation
// mid-file. A 50 GiB original must not have to finish before Ctrl-C is felt.
func hashFile(ctx context.Context, path string, buf []byte, prog progressFn) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	var done int64
	for {
		select {
		case <-ctx.Done():
			return "", errInterrupted
		default:
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
			done += int64(n)
			if prog != nil {
				prog(done)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type checkOpts struct {
	create   bool
	force    bool
	noVerify bool
}

// checkFile is the whole per-file decision, returning the verdict to print and
// count. It is pure with respect to the display: nothing here writes to the
// terminal, which is what lets the same function serve a pipe and a live view.
func checkFile(ctx context.Context, f File, opts checkOpts, buf []byte, prog progressFn) Verdict {
	v := Verdict{Path: f.Path, Size: f.Size}
	sidecar := f.Path + hashExt

	if opts.create {
		return createPath(ctx, f, sidecar, opts, buf, prog, v)
	}

	stored, err := readSidecar(sidecar)
	if err != nil {
		if os.IsNotExist(err) {
			v.Token, v.Outcome = TokenMissing, OutcomeMissing
			return v
		}
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"sidecar could not be read"}
		v.Err = err
		return v
	}
	if stored == "" {
		v.Token, v.Outcome = TokenMissing, OutcomeMissing
		v.Detail = []string{"sidecar is empty"}
		return v
	}
	if !isSHA256(stored) {
		v.Token, v.Outcome = TokenMissing, OutcomeMissing
		v.Detail = []string{"sidecar does not hold a SHA-256 digest"}
		return v
	}

	current, err := hashFile(ctx, f.Path, buf, prog)
	if err != nil {
		if errors.Is(err, errInterrupted) {
			return Verdict{Path: f.Path, Size: f.Size, Token: "", Outcome: OutcomeOK}
		}
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"file could not be read"}
		v.Err = err
		return v
	}
	if strings.EqualFold(stored, current) {
		v.Token, v.Outcome = TokenOK, OutcomeOK
		return v
	}
	v.Token, v.Outcome = TokenMismatch, OutcomeMismatch
	v.Detail = []string{"recorded " + stored, "now      " + current}
	v.Recorded, v.Computed = stored, current
	return v
}

func createPath(ctx context.Context, f File, sidecar string, opts checkOpts, buf []byte, prog progressFn, v Verdict) Verdict {
	// An existing sidecar is honoured unless -f was given: creating is for
	// files that have none, and overwriting one silently would destroy the
	// only record of what the file used to hash to.
	if !opts.force {
		if stored, err := readSidecar(sidecar); err == nil {
			switch {
			case stored != "" && !isSHA256(stored):
				v.Token, v.Outcome = TokenMissing, OutcomeMissing
				v.Detail = []string{"sidecar does not hold a SHA-256 digest"}
				return v
			case stored != "" && opts.noVerify:
				v.Token, v.Outcome = TokenSkipped, OutcomeUnverified
				v.Detail = []string{"sidecar already exists, not verified"}
				return v
			case stored != "":
				current, herr := hashFile(ctx, f.Path, buf, prog)
				if herr != nil {
					if errors.Is(herr, errInterrupted) {
						return Verdict{Path: f.Path, Size: f.Size}
					}
					v.Token, v.Outcome = TokenIOError, OutcomeIOError
					v.Detail = []string{"file could not be read"}
					return v
				}
				if strings.EqualFold(stored, current) {
					v.Token, v.Outcome = TokenOK, OutcomeOK
					return v
				}
				v.Token, v.Outcome = TokenMismatch, OutcomeMismatch
				v.Detail = []string{"recorded " + stored, "now      " + current}
				return v
			}
		} else if !os.IsNotExist(err) {
			v.Token, v.Outcome = TokenIOError, OutcomeIOError
			v.Detail = []string{"sidecar could not be read"}
			return v
		}
	}

	// A file that cannot be read must not get a sidecar, or an empty hash
	// would be recorded as though it were the truth.
	current, err := hashFile(ctx, f.Path, buf, prog)
	if err != nil {
		if errors.Is(err, errInterrupted) {
			return Verdict{Path: f.Path, Size: f.Size}
		}
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"file could not be read"}
		return v
	}
	if err := os.WriteFile(sidecar, []byte(current+"\n"), 0o644); err != nil {
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"sidecar could not be written"}
		return v
	}
	v.Created = true

	// Read the sidecar back and compare it against the digest still in hand.
	//
	// The digest is NOT recomputed from the source. Doing that reads every
	// file a second time, doubling the I/O of every create run, and the only
	// thing it catches beyond this comparison is media returning different
	// bytes on consecutive reads. What the read-back is for is proving the
	// sidecar landed intact, which comparing its contents does exactly.
	if opts.noVerify {
		// Written but deliberately not read back, so it has not been
		// verified and must not be counted as though it had been.
		v.Token, v.Outcome = TokenCreated, OutcomeUnverified
		v.Detail = []string{"not read back"}
		return v
	}
	back, err := readSidecar(sidecar)
	if err != nil {
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"sidecar written but could not be read back"}
		return v
	}
	if !isSHA256(back) {
		v.Token, v.Outcome = TokenIOError, OutcomeIOError
		v.Detail = []string{"sidecar read back as something other than a digest"}
		return v
	}
	if !strings.EqualFold(back, current) {
		v.Token, v.Outcome = TokenMismatch, OutcomeMismatch
		v.Detail = []string{
			fmt.Sprintf("sidecar read back as %s", back),
			fmt.Sprintf("expected             %s", current),
		}
		return v
	}
	v.Token, v.Outcome = TokenCreated, OutcomeOK
	return v
}
