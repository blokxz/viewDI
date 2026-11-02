package fake

import (
	"context"
	"fmt"
	"slices"

	"github.com/blokxz/viewDI/internal/engine"
)

// remoteImage is what the fake image source offers for one reference.
type remoteImage struct {
	digest string // "" = the reference has no remote source (ImageLocal)
	serial string // Incus build serial
}

// SetRemoteImage makes the image source offer digest (and, for Incus, the
// build serial) for ref. An empty digest marks ref as local only. A ref
// never set is up to date: the source offers what runs.
func (e *Engine) SetRemoteImage(ref, digest, serial string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.remote[ref] = remoteImage{digest: digest, serial: serial}
}

// HangImageChecks makes image checks block until their context ends (an
// unreachable registry), or answer again with false. Other calls are not
// affected.
func (e *Engine) HangImageChecks(hang bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.imageHang = hang
}

// FailImageChecks makes image checks fail with err (an offline host), or
// answer again with nil. Other calls are not affected.
func (e *Engine) FailImageChecks(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.imageErr = err
}

// ImageUpdateStatus implements engine.Engine. Docker compares the local
// image ID of c.Ref with the source's digest; Incus compares c.Local.
func (e *Engine) ImageUpdateStatus(ctx context.Context, c engine.ImageCheck) (engine.ImageStatus, error) {
	op := "check image " + c.Ref
	if err := e.enter(ctx, op); err != nil {
		return engine.ImageStatus{Check: c}, err
	}
	e.mu.Lock()
	hang, failure := e.imageHang, e.imageErr
	r, set := e.remote[c.Ref]
	current := c.Local
	if e.id == engine.Docker {
		current = ""
		for _, img := range e.images {
			if slices.Contains(img.Refs, c.Ref) {
				current = img.ID
			}
		}
	}
	e.mu.Unlock()
	if hang {
		<-ctx.Done()
		return engine.ImageStatus{Check: c}, engine.Wrap(op, ctx.Err())
	}
	if failure != nil {
		return engine.ImageStatus{Check: c}, engine.Wrap(op, failure)
	}
	st := engine.ImageStatus{Check: c, Current: current}
	if e.id == engine.Docker && current == "" {
		return st, engine.Errorf(engine.ClassNotFound, op, fmt.Errorf("%w: no local image %s", engine.ErrNotFound, c.Ref))
	}
	switch {
	case set && r.digest == "":
		st.State = engine.ImageLocal
	case set && r.digest != current:
		st.State, st.Available, st.NewSerial = engine.ImageOutdated, r.digest, r.serial
	default:
		st.State, st.Available = engine.ImageUpToDate, current
	}
	if e.id == engine.Incus {
		if in := e.bySerialOf(c.Local); in != "" {
			st.Serial = in
		}
		if st.NewSerial == "" {
			st.NewSerial = st.Serial
		}
	}
	return st, nil
}

// bySerialOf returns the build serial of an instance created from the base
// image fingerprint, or "".
func (e *Engine) bySerialOf(base string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, in := range e.instances {
		if x, ok := in.Extra.(*engine.IncusExtra); ok && x.BaseImage == base && x.ImageSerial != "" {
			return x.ImageSerial
		}
	}
	return ""
}
