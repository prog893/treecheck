package main

import "os"

// colors is a no-op when output is not a terminal or NO_COLOR is set, so a
// pipe gets plain bytes and a redirected log stays greppable.
type colors struct{ on bool }

func newColors(isTTY bool) *colors {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return &colors{on: false}
	}
	return &colors{on: isTTY}
}

func (c *colors) wrap(code, s string) string {
	if !c.on {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (c *colors) red(s string) string    { return c.wrap("0;31", s) }
func (c *colors) green(s string) string  { return c.wrap("0;32", s) }
func (c *colors) yellow(s string) string { return c.wrap("1;33", s) }
func (c *colors) dim(s string) string    { return c.wrap("2", s) }
func (c *colors) cyan(s string) string   { return c.wrap("0;36", s) }
