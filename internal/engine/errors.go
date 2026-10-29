package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
)

// Class sorts errors for the supervisor and the UI.
type Class int

// Error classes (docs/02-architecture.md §4.2).
const (
	// ClassTransient: refused, timeout, stream EOF… — retry with backoff.
	ClassTransient Class = iota
	// ClassPermission: EACCES or HTTP 403 — needs the user to fix access.
	ClassPermission
	// ClassMissing: the socket does not exist (ENOENT) — daemon not
	// installed or not started, or stopped and removed its socket.
	ClassMissing
	// ClassNotFound: the requested object does not exist.
	ClassNotFound
	// ClassCanceled: the caller's context was canceled.
	ClassCanceled
)

func (c Class) String() string {
	switch c {
	case ClassPermission:
		return "permission"
	case ClassMissing:
		return "missing"
	case ClassNotFound:
		return "not-found"
	case ClassCanceled:
		return "canceled"
	}
	return "transient"
}

// Error is a classified engine error.
type Error struct {
	Class Class
	Op    string
	Err   error
}

func (e *Error) Error() string {
	if e.Op == "" {
		return e.Err.Error()
	}
	return e.Op + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Errorf wraps err with an explicit class.
func Errorf(class Class, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Class: class, Op: op, Err: err}
}

// ErrNotFound is the base error for missing objects.
var ErrNotFound = errors.New("not found")

// NotFound returns a ClassNotFound error for an object.
func NotFound(op, what string) error {
	return &Error{Class: ClassNotFound, Op: op, Err: fmt.Errorf("%s: %w", what, ErrNotFound)}
}

// Wrap annotates err with op, classifying it with Classify.
func Wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Class: Classify(err), Op: op, Err: err}
}

// Classify returns the class of err. Explicitly classified errors win;
// otherwise system errors are inspected (EACCES/EPERM → permission, ENOENT →
// missing, context cancellation → canceled) and everything else is
// transient.
func Classify(err error) Class {
	var e *Error
	switch {
	case err == nil:
		return ClassTransient
	case errors.As(err, &e):
		return e.Class
	case errors.Is(err, context.Canceled):
		return ClassCanceled
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return ClassPermission
	case errors.Is(err, syscall.ENOENT):
		return ClassMissing
	case errors.Is(err, ErrNotFound):
		return ClassNotFound
	}
	return ClassTransient
}

// IsTimeout reports whether err is a deadline or network timeout.
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// IsEOF reports whether err means the stream ended.
func IsEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
