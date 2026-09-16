//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import "syscall"

// The BSDs name the termios ioctls TIOCGETA and TIOCSETA.
const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
