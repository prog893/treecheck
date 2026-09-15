package main

import (
	"context"
	"os"
	"syscall"
	"unsafe"
)

// rawMode puts the terminal into non-canonical, non-echoing mode so a keypress
// arrives immediately instead of on the next newline, and returns a restore
// function. ISIG is deliberately left on, so Ctrl-C still raises SIGINT and the
// interrupt path stays exactly as it is for a non-interactive run.
func rawMode(f *os.File) (func(), bool) { return rawModeTimed(f, 0) }

// rawModeTimed is rawMode with a read timeout, expressed the way a terminal
// expresses one: VMIN=0 with VTIME in tenths of a second, so a read returns
// empty once the time is up.
//
// This is the only portable way to bound a read on a terminal.
// os.File.SetReadDeadline does not work here: /dev/tty is not registered with
// Go's poller, so it fails outright with "file type does not support deadline"
// and any code that treats that as fatal never performs the read at all.
//
// deciseconds of 0 means block until a byte arrives, which is what the key
// watcher wants.
func rawModeTimed(f *os.File, deciseconds uint8) (func(), bool) {
	var old syscall.Termios
	if err := ioctlTermios(f.Fd(), syscall.TIOCGETA, &old); err != nil {
		return func() {}, false
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	if deciseconds > 0 {
		raw.Cc[syscall.VMIN] = 0
		raw.Cc[syscall.VTIME] = deciseconds
	} else {
		raw.Cc[syscall.VMIN] = 1
		raw.Cc[syscall.VTIME] = 0
	}
	if err := ioctlTermios(f.Fd(), syscall.TIOCSETA, &raw); err != nil {
		return func() {}, false
	}
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		_ = ioctlTermios(f.Fd(), syscall.TIOCSETA, &old)
	}, true
}

func ioctlTermios(fd uintptr, req uintptr, t *syscall.Termios) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(t)))
	if e != 0 {
		return e
	}
	return nil
}

// watchKeys reads the controlling terminal directly rather than stdin, which
// may be a pipe, and never touches the hashing path: a key can only switch the
// view or ask the scan to stop.
func watchKeys(ctx context.Context, d *Display, stop func(), done chan<- struct{}) {
	defer close(done)

	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()

	// A timed read, not a blocking one. Closing a descriptor does not
	// interrupt a read already blocked on it, so a watcher parked in a
	// blocking read cannot be shut down at all: waiting for it deadlocks, and
	// not waiting for it leaves a second reader on the terminal that steals
	// the next keystroke from whoever owns the screen. VTIME makes the read
	// return empty every tenth of a second, which is often enough to notice
	// cancellation and rare enough to cost nothing.
	restore, ok := rawModeTimed(tty, 1)
	if !ok {
		return
	}
	defer restore()

	buf := make([]byte, 8)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := tty.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		switch buf[0] {
		case '\t':
			d.ToggleView()
		case ' ':
			d.ToggleExpanded()
		case 'q', 'Q':
			// The same path a signal takes: the run is stopped, not failed.
			stop()
			return
		}
	}
}
