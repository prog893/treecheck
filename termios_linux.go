//go:build linux

package main

import "syscall"

// Linux names the same requests TCGETS and TCSETS; the BSD spellings do not
// exist there, and referring to them fails the build rather than the run.
const (
	ioctlGetTermios = syscall.TCGETS
	ioctlSetTermios = syscall.TCSETS
)
