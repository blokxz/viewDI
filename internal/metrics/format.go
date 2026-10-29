package metrics

import (
	"fmt"
	"strconv"
)

// Bytes formats a byte count with binary units ("1.2G", "340M", "512K").
func Bytes(n uint64) string { return human(float64(n), "") }

// BytesRate formats a byte rate ("2.4M/s").
func BytesRate(v float64) string { return human(v, "/s") }

func human(v float64, suffix string) string {
	const unit = 1024
	if v < unit {
		return strconv.Itoa(int(v)) + "B" + suffix
	}
	units := "KMGTPE"
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f%c%s", v, units[i], suffix)
	}
	return fmt.Sprintf("%.1f%c%s", v, units[i], suffix)
}

// Percent formats a percentage with one decimal under 100 ("7.8", "214").
func Percent(v float64) string {
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}
