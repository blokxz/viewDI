package clock

import "time"

// Eventually polls cond on the real clock until it holds or within passes,
// and reports whether it held. It is for tests that wait on goroutines
// they cannot observe otherwise (no sleeps with a guessed duration).
func Eventually(within time.Duration, cond func() bool) bool {
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	for !cond() {
		select {
		case <-tick.C:
		case <-deadline.C:
			return cond()
		}
	}
	return true
}
