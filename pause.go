package main

import (
	"context"
	"sync"
	"time"
)

// pauseGate lets the operator stop the hashing without stopping the run.
//
// A long verify saturates the device it is reading, which is a problem when
// that device is also the one an edit is playing back from. Pausing gives it
// back without losing the scan's progress, which the alternative, killing the
// run and starting again, does not.
//
// Paused time is tracked so the rate and the estimate stay honest: a run
// paused for ten minutes has not slowed down, and an estimate that says
// otherwise is worse than no estimate.
type pauseGate struct {
	mu     sync.Mutex
	resume chan struct{} // non-nil and open while paused
	since  time.Time
	total  time.Duration
}

// wait blocks while paused, and returns as soon as the run is cancelled so a
// paused scan still stops promptly on a signal or a quit.
func (g *pauseGate) wait(ctx context.Context) error {
	g.mu.Lock()
	ch := g.resume
	g.mu.Unlock()
	if ch == nil {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *pauseGate) toggle() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resume == nil {
		g.resume = make(chan struct{})
		g.since = time.Now()
		return
	}
	close(g.resume)
	g.resume = nil
	g.total += time.Since(g.since)
}

func (g *pauseGate) paused() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.resume != nil
}

// pausedFor is how long the run has spent paused, including the pause it is in
// right now.
func (g *pauseGate) pausedFor() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.total
	if g.resume != nil {
		t += time.Since(g.since)
	}
	return t
}

// release wakes anything waiting, for shutdown. A paused run that is cancelled
// must not sit blocked while the rest of the program waits for its workers.
func (g *pauseGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resume != nil {
		close(g.resume)
		g.resume = nil
		g.total += time.Since(g.since)
	}
}
