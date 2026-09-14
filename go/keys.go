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
func rawMode(f *os.File) (func(), bool) {
	var old syscall.Termios
	if err := ioctlTermios(f.Fd(), syscall.TIOCGETA, &old); err != nil {
		return func() {}, false
	}
	raw := old
	raw.Lflag &^= syscall.ICANON | syscall.ECHO
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
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
func watchKeys(ctx context.Context, d *Display, stop func()) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	restore, ok := rawMode(tty)
	if !ok {
		return
	}
	defer restore()

	go func() {
		<-ctx.Done()
		restore()
		tty.Close()
	}()

	buf := make([]byte, 1)
	for {
		n, err := tty.Read(buf)
		if err != nil || n == 0 {
			return
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
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}
