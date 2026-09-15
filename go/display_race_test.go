package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestDisplayConcurrency drives the display the way a real run does: workers
// reporting progress while the frame loop reads, and a view switch happening
// underneath both. Run under -race this is the only thing that exercises the
// display's sharing, since the contract tests all use pipes and never build
// one.
func TestDisplayConcurrency(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	const jobs = 8
	d := NewDisplay(NewRenderer(f), newColors(false), jobs, 400, 400<<20,
		filepath.Join(t.TempDir(), "root"), "Verify only", true)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Workers: claim a slot, report progress, finish, repeat.
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				size := int64(1<<20) * int64(1+i%7)
				d.Begin(worker, "file-"+itoa(i)+".bin", size)
				for b := int64(0); b < size; b += size/4 + 1 {
					d.Progress(worker, b)
				}
				v := Verdict{Path: "file-" + itoa(i) + ".bin", Size: size}
				switch i % 4 {
				case 0:
					v.Outcome, v.Token = OutcomeOK, TokenOK
				case 1:
					v.Outcome, v.Token = OutcomeMismatch, TokenMismatch
				case 2:
					v.Outcome, v.Token = OutcomeMissing, TokenMissing
				case 3:
					v.Outcome, v.Token = OutcomeIOError, TokenIOError
				}
				d.Finish(worker, v)
				d.Commit(v.Render(d.c))
			}
		}(w)
	}

	// Readers: the frame loop's work, plus both renderers, concurrently.
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				d.sampleRate()
				_ = d.snapshot()
				_ = d.frame()
				_ = d.renderDashboard(30, 100)
				_ = d.recentLines(10)
			}
		}()
	}

	// A view switch while all of that is in flight.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			select {
			case <-stop:
				return
			default:
			}
			d.expanded.Store(i%2 == 0)
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stop)
	wg.Wait()

	if got := d.doneFiles.Load(); got == 0 {
		t.Fatal("no files were recorded; the test did not exercise anything")
	}
	st := d.snapshot()
	if st.ok+st.mismatch+st.missing+st.ioerr != st.doneFiles {
		t.Errorf("outcome counters sum to %d but %d files finished",
			st.ok+st.mismatch+st.missing+st.ioerr, st.doneFiles)
	}
	if st.frac < 0 || st.frac > 1 {
		t.Errorf("progress fraction escaped [0,1]: %v", st.frac)
	}
}
