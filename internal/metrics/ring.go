package metrics

// Ring is a fixed-capacity buffer that overwrites its oldest value.
type Ring[T any] struct {
	buf  []T
	next int
	full bool
}

// NewRing returns an empty ring of capacity n (at least 1).
func NewRing[T any](n int) *Ring[T] { return &Ring[T]{buf: make([]T, max(1, n))} }

// Push appends v, dropping the oldest value when full.
func (r *Ring[T]) Push(v T) {
	r.buf[r.next] = v
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Len is the number of values held.
func (r *Ring[T]) Len() int {
	if r.full {
		return len(r.buf)
	}
	return r.next
}

// Cap is the capacity.
func (r *Ring[T]) Cap() int { return len(r.buf) }

// Last returns the newest value.
func (r *Ring[T]) Last() (T, bool) {
	var zero T
	if r.Len() == 0 {
		return zero, false
	}
	return r.buf[(r.next-1+len(r.buf))%len(r.buf)], true
}

// Tail copies the newest n values (or fewer), oldest first, into dst.
func (r *Ring[T]) Tail(dst []T, n int) []T {
	n = min(n, r.Len())
	start := (r.next - n + len(r.buf)) % len(r.buf)
	for i := range n {
		dst = append(dst, r.buf[(start+i)%len(r.buf)])
	}
	return dst
}
