package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/blokxz/viewDI/internal/selfupdate"
)

// runUpdate is `viewdi --update`: it updates this installation and exits.
func runUpdate(f flags, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := selfupdate.Run(ctx, selfupdate.Options{
		Current: version, Yes: f.Yes, Force: f.Force,
		In: os.Stdin, Out: stdout,
	})
	switch {
	case err == nil:
		return 0
	case errors.Is(err, selfupdate.ErrUpToDate):
		_, _ = fmt.Fprintln(stdout, "viewdi:", err)
		return 0
	}
	_, _ = fmt.Fprintln(stderr, "viewdi: update:", err)
	return 1
}
