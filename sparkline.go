package main

import "strings"

// sparkChars are the eighth-height blocks, lowest to highest. A sparkline of a
// throughput history says in one row what a number cannot: whether the device
// is holding a steady rate, ramping, or stalling on a run of small files.
var sparkChars = []rune("▁▂▃▄▅▆▇█")

// sparkline scales from zero to the series maximum, not from its minimum.
//
// Zero is meaningful for throughput: a run that stalls should visibly drop to
// the floor, which a min-to-max scale would hide by rescaling around the stall.
// The cost is that a perfectly steady rate renders at full height, since it is
// genuinely at its own maximum throughout.
func sparkline(vals []int64, width int) string {
	if width <= 0 || len(vals) == 0 {
		return strings.Repeat(" ", maxInt(width, 0))
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	var max int64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for i := 0; i < width-len(vals); i++ {
		b.WriteByte(' ')
	}
	for _, v := range vals {
		if max <= 0 {
			b.WriteRune(sparkChars[0])
			continue
		}
		idx := int(v * int64(len(sparkChars)-1) / max)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		b.WriteRune(sparkChars[idx])
	}
	return b.String()
}
