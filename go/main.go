// treecheck verifies files against SHA-256 sidecars to catch silent corruption.
//
// When it reports clean, the data is intact. Every bug worth fixing here is one
// that breaks that promise without being visible in the output, so the design
// rule throughout is that a run which checked less than it claims must never be
// able to reach the clean verdict by any path.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "2.0.0-draft"

type options struct {
	dir      string
	create   bool
	force    bool
	noVerify bool
	strict   bool
	verbose  bool
	quiet    bool
	jobs     int
	maxDepth int
	exclude  []string
}

func usage(w *os.File) {
	fmt.Fprint(w, `Usage: treecheck [OPTIONS] <directory>

Walks <directory> and checks every file against its .sha256 sidecar,
reporting anything whose contents no longer match. Recursive by default.
Never modifies, moves or deletes your data.

OPTIONS:
  -c              Create missing sidecars, and verify the ones that exist
  -f              Overwrite existing sidecars (use with -c)
  -n              Skip verification (create only; requires -c)
  -e DIRS         Exclude directories, comma-separated
  -j N, --jobs N  Hash across N parallel workers (default: one per CPU)
  -v              Verbose (report skipped files)
  --no-recurse    Only the named directory (same as --max-depth 1)
  --max-depth N   Descend at most N levels
  --strict        Treat missing sidecars as a failure too
  -V, --version   Print version and exit
  -h              Show this help

EXIT STATUS:
  0   clean
  1   verification failed: a mismatch, an I/O error, or a failed walk
  2   nothing corrupt, but some files have no usable sidecar yet
  130 interrupted: only the files reported as scanned were checked

KEYS (interactive runs):
  space           Toggle the per-worker detail block
  q               Stop the scan

EXAMPLES:
  treecheck /Volumes/Media                   Verify the whole tree
  treecheck -c /Volumes/Media                Create missing sidecars, verify rest
  treecheck -cn /Volumes/Media               Create only, skip verification
  treecheck --no-recurse /Volumes/Media      Top level only
  treecheck -e "temp,cache" /Volumes/Media   Skip those directories
  treecheck --strict /Volumes/Media          Missing sidecars are failures too
`)
}

func parseArgs(argv []string, stdout, stderr *os.File) (*options, int) {
	o := &options{jobs: -1}
	fail := func(format string, args ...any) {
		fmt.Fprintf(stderr, "ERROR: "+format+"\n", args...)
	}
	i := 0
	needInt := func(flag string) (int, bool) {
		i++
		if i >= len(argv) {
			fail("%s needs a positive integer, got: <missing>", flag)
			return 0, false
		}
		n, err := strconv.Atoi(argv[i])
		if err != nil || n < 1 {
			fail("%s needs a positive integer, got: %s", flag, argv[i])
			return 0, false
		}
		return n, true
	}
	for ; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "-h" || a == "--help":
			usage(stdout)
			return nil, 0
		case a == "-V" || a == "--version":
			fmt.Fprintf(stdout, "treecheck %s\n", version)
			return nil, 0
		case a == "--strict":
			o.strict = true
		case a == "--no-recurse":
			o.maxDepth = 1
		case a == "--max-depth":
			n, ok := needInt("--max-depth")
			if !ok {
				return nil, 1
			}
			o.maxDepth = n
		case a == "-j" || a == "--jobs":
			n, ok := needInt(a)
			if !ok {
				return nil, 1
			}
			o.jobs = n
		case a == "-e":
			i++
			if i >= len(argv) {
				fail("-e needs a comma-separated list of directories")
				return nil, 1
			}
			o.exclude = append(o.exclude, strings.Split(argv[i], ",")...)
		case strings.HasPrefix(a, "--"):
			fail("unknown option: %s", a)
			usage(stdout)
			return nil, 1
		case strings.HasPrefix(a, "-") && len(a) > 1:
			// Clustered short flags, so -cn works like -c -n.
			for _, r := range a[1:] {
				switch r {
				case 'c':
					o.create = true
				case 'f':
					o.force = true
				case 'n':
					o.noVerify = true
				case 'v':
					o.verbose = true
				default:
					fail("unknown option: -%c", r)
					usage(stdout)
					return nil, 1
				}
			}
		default:
			if o.dir != "" {
				fail("more than one directory given: %s and %s", o.dir, a)
				return nil, 1
			}
			o.dir = a
		}
	}
	if o.dir == "" {
		fail("no directory given")
		usage(stdout)
		return nil, 1
	}
	if o.noVerify && !o.create {
		fail("-n requires -c")
		return nil, 1
	}
	fi, err := os.Stat(o.dir)
	if err != nil || !fi.IsDir() {
		fail("not a directory: %s", o.dir)
		return nil, 1
	}
	if o.jobs < 1 {
		o.jobs = defaultJobs()
	}
	return o, -1
}

// defaultJobs caps the worker count well below the core count on purpose.
//
// One worker hashes at roughly 2 GiB/s here, so eight of them muster more
// hashing capacity than any single NVMe can feed, and past that point the
// workers contend for the disk rather than sharing it. Measured on a 24-core
// M2 Ultra across three workload shapes, wall clock against worker count:
//
//	-j       2000 tiny   6x400MiB   300x384KiB
//	 1          0.514      1.261        0.179
//	 8          0.074      0.228        0.041
//	24          0.132      0.229        0.040
//	32          0.136      0.230        0.042
//
// Eight is at or within noise of the best time in every column, and the tiny
// file case degrades by 80% by the time the count reaches the core count. The
// shell implementation defaulted to one worker per core because it was paying
// a process spawn per file and needed the concurrency to hide it; nothing here
// pays that, so the default follows the measurement instead.
//
// -j overrides this, and is the right knob for a device that genuinely wants
// more in flight.
func defaultJobs() int {
	if n := runtime.NumCPU(); n < 8 {
		return n
	}
	return 8
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(argv []string, stdout, stderr *os.File) int {
	o, code := parseArgs(argv, stdout, stderr)
	if o == nil {
		return code
	}

	fail := func(format string, args ...any) {
		fmt.Fprintf(stderr, "ERROR: "+format+"\n", args...)
	}
	tty := isTerminal(stdout)
	c := newColors(tty)
	start := time.Now()

	mode := "Verify only"
	switch {
	case o.create && o.noVerify:
		mode = "Create only"
	case o.create:
		mode = "Create + Verify"
	}
	scope := "Depth: unlimited"
	if o.maxDepth > 0 {
		scope = fmt.Sprintf("Depth: %d", o.maxDepth)
	}
	fmt.Fprintf(stdout, "Mode: %s | Dir: %s | %s\n", mode, displayPath(o.dir), scope)
	if len(o.exclude) > 0 {
		fmt.Fprintf(stdout, "Excluding: %s\n", strings.Join(o.exclude, " "))
	}
	if o.jobs > 1 {
		fmt.Fprintf(stdout, "Workers: %d (parallel hashing)\n", o.jobs)
	}
	fmt.Fprintln(stdout)

	w := walkTree(o.dir, o.maxDepth, o.exclude, hashExt)
	if w.Err != nil {
		// A failed traversal is never reported as an empty directory: the
		// run examined an unknown subset and cannot speak for the rest.
		fail("could not walk %s: %v", displayPath(o.dir), w.Err)
	}
	if o.verbose {
		if skipped := w.Total - len(w.Files); skipped > 0 {
			fmt.Fprintf(stdout, "Skipping %d file(s): sidecars and excluded paths\n\n", skipped)
		}
	}

	if o.jobs > len(w.Files) && len(w.Files) > 0 {
		o.jobs = len(w.Files)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signals are handled by cancelling a context the workers observe, so an
	// interrupt stops the scan rather than merely being remembered by it. The
	// shell implementation could not do this reliably: a trap firing inside a
	// command substitution runs in that subshell and its assignment is lost.
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	var sawSignal bool
	var sigMu sync.Mutex
	go func() {
		<-sigc
		sigMu.Lock()
		sawSignal = true
		sigMu.Unlock()
		cancel()
	}()

	var disp *Display
	if tty && !o.quiet && len(w.Files) > 0 {
		disp = NewDisplay(NewRenderer(stdout), c, o.jobs, len(w.Files), w.Bytes)
		disp.Run()
		go watchKeys(ctx, disp, cancel)
	}

	if o.jobs > 1 && !o.quiet {
		line := fmt.Sprintf("Hashing with %d parallel workers...", o.jobs)
		if disp != nil {
			disp.Commit([]string{line})
		} else {
			fmt.Fprintln(stdout, line)
		}
	}

	results := runWorkers(ctx, w.Files, o, disp)

	// Emitted in walk order, always, whether the destination is a terminal or
	// a pipe. Results arrive in completion order; a reorder buffer holds the
	// early ones so two runs over one tree produce diffable output without
	// giving up liveness.
	var counts Counters
	emit := func(lines []string) {
		if disp != nil {
			disp.Commit(lines)
			return
		}
		for _, l := range lines {
			fmt.Fprintln(stdout, l)
		}
	}
	pending := map[int]Verdict{}
	next := 0
	for v := range results {
		pending[v.Index] = v
		for {
			nv, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			next++
			if nv.Token == "" {
				continue // never reached; counted below
			}
			counts.Add(nv)
			if !o.quiet {
				emit(nv.Render(c))
			}
		}
	}
	// Anything still buffered belongs to an interrupted run: its index never
	// completed, so the records after it were never drained.
	ids := make([]int, 0, len(pending))
	for k := range pending {
		ids = append(ids, k)
	}
	sort.Ints(ids)
	for _, k := range ids {
		v := pending[k]
		if v.Token == "" {
			continue
		}
		counts.Add(v)
		if !o.quiet {
			emit(v.Render(c))
		}
	}
	counts.Unreached = len(w.Files) - counts.Scanned

	if disp != nil {
		disp.Close()
	}
	signal.Stop(sigc)
	sigMu.Lock()
	wasInterrupted := sawSignal
	sigMu.Unlock()

	fmt.Fprintln(stdout)
	counts.Write(stdout, c, o.create, int64(time.Since(start).Seconds()))

	rc := counts.ExitCode(w.Err != nil, wasInterrupted, o.strict, o.create)
	switch rc {
	case 0:
		fmt.Fprintln(stdout, c.green("✓ Completed successfully"))
	case 2:
		fmt.Fprintln(stdout, c.yellow("Completed: nothing corrupt, but some files have no sidecar"))
	case 130:
		fmt.Fprintln(stdout, c.yellow("Interrupted: the counters above cover only the files reached"))
	default:
		fail("Completed with errors")
	}
	return rc
}

// runWorkers dispatches the walk across goroutines. Every cross-goroutine fact
// travels on a channel: there is no scratch directory, no per-record result
// file and no event log, because workers here share an address space. The shell
// implementation needed thirteen temporary files to carry exactly this.
func runWorkers(ctx context.Context, files []File, o *options, disp *Display) <-chan Verdict {
	out := make(chan Verdict, o.jobs*4)
	type job struct {
		idx int
		f   File
	}
	jobs := make(chan job, o.jobs*2)

	var wg sync.WaitGroup
	for wi := 0; wi < o.jobs; wi++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			buf := make([]byte, 1<<20)
			opts := checkOpts{create: o.create, force: o.force, noVerify: o.noVerify}
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				var prog progressFn
				if disp != nil {
					disp.Begin(worker, j.f.Path, j.f.Size)
					prog = func(n int64) { disp.Progress(worker, n) }
				}
				v := checkFile(ctx, j.f, opts, buf, prog)
				v.Index = j.idx
				if disp != nil {
					disp.Finish(worker, v)
				}
				out <- v
			}
		}(wi)
	}

	go func() {
		defer close(jobs)
		for i, f := range files {
			select {
			case <-ctx.Done():
				return
			case jobs <- job{idx: i, f: f}:
			}
		}
	}()
	go func() { wg.Wait(); close(out) }()
	return out
}
