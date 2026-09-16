package main

import "testing"

// TestScreenNilSafe pins a crash that made the exit status depend on where
// output went. A scan that finishes before the first frame never creates a
// screen, and the teardown path dereferenced it anyway: the process died with
// Go's panic status 2 where a pipe reported 1, so the same tree gave different
// answers depending on whether stdout was a terminal.
func TestScreenNilSafe(t *testing.T) {
	var s *Screen
	// None of these may panic.
	s.Enter()
	s.Resize(24, 80)
	s.Draw([]string{"x"})
	s.Leave()
}
