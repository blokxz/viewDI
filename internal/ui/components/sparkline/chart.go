package sparkline

import "strings"

// Chart draws values (oldest first) as an area chart of width×height
// cells, right-aligned like Render. top is the value of the top edge (a
// limit, or 0 to scale to the series maximum). Rows are returned top
// first, as plain text exactly width cells wide.
func Chart(values []float64, width, height int, top float64) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	vals := Downsample(values, width)
	if top <= 0 {
		for _, v := range vals {
			top = max(top, v)
		}
	}
	rows := make([]strings.Builder, height)
	pad := width - len(vals)
	for r := range rows {
		rows[r].WriteString(strings.Repeat(" ", pad))
	}
	steps := float64(height * len(blocks))
	for _, v := range vals {
		level := 0 // eighths of a cell, from the bottom
		if top > 0 && v > 0 {
			level = max(1, min(int(v/top*steps+0.5), height*len(blocks)))
		}
		for r := range rows {
			fromBottom := height - 1 - r
			fill := level - fromBottom*len(blocks)
			switch {
			case fill >= len(blocks):
				rows[r].WriteRune(blocks[len(blocks)-1])
			case fill > 0:
				rows[r].WriteRune(blocks[fill-1])
			default:
				rows[r].WriteByte(' ')
			}
		}
	}
	out := make([]string, height)
	for r := range rows {
		out[r] = rows[r].String()
	}
	return out
}
