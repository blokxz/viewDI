// Package openmetrics parses the OpenMetrics / Prometheus text exposition
// format, as served by Incus at /1.0/metrics. The input is untrusted: sizes
// are capped and malformed lines are skipped, never fatal.
package openmetrics

import (
	"bufio"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

// Limits on untrusted input.
const (
	MaxBytes   = 16 << 20 // whole document
	MaxLine    = 64 << 10 // one line
	MaxSamples = 1 << 20
	maxLabels  = 64
)

// ErrTooLarge reports a document over MaxBytes or MaxSamples.
var ErrTooLarge = errors.New("openmetrics: document too large")

// Sample is one metric sample.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Label returns a label value ("" when absent).
func (s Sample) Label(k string) string { return s.Labels[k] }

// Parse reads every sample of a text exposition document. Comments
// (# HELP, # TYPE, # EOF) are skipped, as are lines that do not parse.
func Parse(r io.Reader) ([]Sample, error) {
	lr := &io.LimitedReader{R: r, N: MaxBytes + 1}
	sc := bufio.NewScanner(lr)
	sc.Buffer(make([]byte, 0, 4096), MaxLine)
	var out []Sample
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		s, ok := parseLine(line)
		if !ok {
			continue
		}
		if len(out) >= MaxSamples {
			return out, ErrTooLarge
		}
		out = append(out, s)
	}
	if lr.N <= 0 {
		return out, ErrTooLarge
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return out, err
	} else if err != nil {
		return out, ErrTooLarge
	}
	return out, nil
}

// parseLine parses `name{l="v",…} value [timestamp]`.
func parseLine(line string) (Sample, bool) {
	var s Sample
	i := strings.IndexAny(line, "{ \t")
	if i <= 0 {
		return s, false
	}
	s.Name = line[:i]
	if !validName(s.Name) {
		return s, false
	}
	rest := line[i:]
	if rest[0] == '{' {
		labels, n, ok := parseLabels(rest)
		if !ok {
			return s, false
		}
		s.Labels = labels
		rest = rest[n:]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || len(fields) > 2 {
		return s, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(v) {
		return s, false
	}
	s.Value = v
	return s, true
}

func validName(n string) bool {
	for i, r := range n {
		switch {
		case r == '_' || r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return n != ""
}

// parseLabels parses `{k="v",…}` at the start of s and returns the labels
// and the number of bytes consumed.
func parseLabels(s string) (map[string]string, int, bool) {
	labels := map[string]string{}
	i := 1
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			return nil, 0, false
		}
		if s[i] == '}' {
			return labels, i + 1, true
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq <= 0 || i+eq+1 >= len(s) || s[i+eq+1] != '"' {
			return nil, 0, false
		}
		key := strings.TrimSpace(s[i : i+eq])
		if !validName(key) {
			return nil, 0, false
		}
		i += eq + 2
		var val strings.Builder
		for {
			if i >= len(s) {
				return nil, 0, false
			}
			c := s[i]
			if c == '"' {
				i++
				break
			}
			if c == '\\' && i+1 < len(s) {
				i++
				switch s[i] {
				case 'n':
					val.WriteByte('\n')
				default:
					val.WriteByte(s[i])
				}
				i++
				continue
			}
			val.WriteByte(c)
			i++
		}
		if len(labels) >= maxLabels {
			return nil, 0, false
		}
		labels[key] = val.String()
	}
}
