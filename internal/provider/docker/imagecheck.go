package docker

import (
	"context"
	"time"

	"github.com/distribution/reference"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// registryTimeout bounds one registry query: the daemon contacts the
// registry, which may be slow, but never longer than this.
const registryTimeout = 20 * time.Second

// ImageUpdateStatus implements engine.Engine. It compares the repo digests
// of the local image c.Ref resolves to with the manifest digest the
// registry serves for c.Ref (GET /distribution/{ref}/json, resolved by the
// daemon). An image without a repo digest for that repository (built,
// loaded or retagged locally) is ImageLocal. The registry is queried
// without credentials, so a private repository answers ClassPermission.
func (a *Adapter) ImageUpdateStatus(ctx context.Context, c engine.ImageCheck) (engine.ImageStatus, error) {
	st := engine.ImageStatus{Check: c}
	named, err := reference.ParseNormalizedNamed(c.Ref)
	if err != nil {
		st.State = engine.ImageLocal
		return st, nil //nolint:nilerr // an unparsable reference has no registry to ask
	}
	ictx, cancel := engine.WithDefaultTimeout(ctx)
	img, err := a.cli.ImageInspect(ictx, c.Ref)
	cancel()
	if err != nil {
		return st, classify("inspect image "+c.Ref, err)
	}
	st.Current = repoDigest(named, img.RepoDigests)
	if st.Current == "" {
		st.State = engine.ImageLocal
		return st, nil
	}
	rctx, cancel := context.WithTimeout(ctx, registryTimeout)
	defer cancel()
	res, err := a.cli.DistributionInspect(rctx, c.Ref, client.DistributionInspectOptions{})
	if err != nil {
		return st, classify("check image "+c.Ref, err)
	}
	st.Available = res.Descriptor.Digest.String()
	st.State = engine.ImageUpToDate
	if st.Available != st.Current && !hasDigest(named, img.RepoDigests, st.Available) {
		st.State = engine.ImageOutdated
	}
	return st, nil
}

// repoDigest returns the digest of the first repo digest ("nginx@sha256:…")
// that belongs to named's repository, or "".
func repoDigest(named reference.Named, digests []string) string {
	for _, d := range digests {
		if cn, ok := canonical(d); ok && cn.Name() == named.Name() {
			return cn.Digest().String()
		}
	}
	return ""
}

// hasDigest reports whether any repo digest of named's repository is dg: an
// image pulled under several digests (an index and a platform manifest)
// is current when any of them matches.
func hasDigest(named reference.Named, digests []string, dg string) bool {
	for _, d := range digests {
		if cn, ok := canonical(d); ok && cn.Name() == named.Name() && cn.Digest().String() == dg {
			return true
		}
	}
	return false
}

func canonical(s string) (reference.Canonical, bool) {
	r, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		return nil, false
	}
	cn, ok := r.(reference.Canonical)
	return cn, ok
}
