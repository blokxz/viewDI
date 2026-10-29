// Package sparkline renders a series as one line of block characters
// (in-house, see docs/01-research.md §1.2). Series longer than the width
// are downsampled to the width, keeping each bucket's maximum so short
// spikes stay visible.
package sparkline

import "strings"

var blocks = []rune("▁▂▃▄▅▆▇█")

// Render draws values (oldest first) in width cells, right-aligned so the
// newest value is always at the right edge. top is the value drawn as a
// full block; 0 scales to the series maximum. The result is plain text,
// exactly width cells wide; callers style it.
func Render(values []float64, width int, top float64) string {
	if width <= 0 {
		return ""
	}
	vals := Downsample(values, width)
	if top <= 0 {
		for _, v := range vals {
			top = max(top, v)
		}
	}
	var b strings.Builder
	b.Grow(width * 3)
	b.WriteString(strings.Repeat(" ", width-len(vals)))
	for _, v := range vals {
		switch {
		case top <= 0 || v <= 0:
			b.WriteRune(blocks[0])
		default:
			i := int(v / top * float64(len(blocks)-1))
			b.WriteRune(blocks[min(max(i, 0), len(blocks)-1)])
		}
	}
	return b.String()
}

// Downsample reduces values to at most n buckets, keeping each bucket's
// maximum. Shorter series are returned unchanged.
func Downsample(values []float64, n int) []float64 {
	if len(values) <= n || n <= 0 {
		return values
	}
	out := make([]float64, n)
	for i := range n {
		lo, hi := i*len(values)/n, (i+1)*len(values)/n
		m := values[lo]
		for _, v := range values[lo:hi] {
			m = max(m, v)
		}
		out[i] = m
	}
	return out
}
