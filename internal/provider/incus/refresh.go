package incus

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// PullImage implements engine.Engine: Incus images are refreshed.
func (a *Adapter) PullImage(context.Context, engine.ImageCheck, engine.ProgressFunc) (string, error) {
	return "", engine.Unsupported("pull image", "Incus images are refreshed (f)")
}

// Recreate implements engine.Engine: Incus instances are rebuilt with
// `incus rebuild`, which viewDI only suggests (it wipes the root
// filesystem).
func (a *Adapter) Recreate(_ context.Context, id string, _ engine.RecreateOptions, _ engine.ProgressFunc) (string, error) {
	return "", engine.Unsupported("recreate "+id, "Incus instances are rebuilt with incus rebuild")
}

// RefreshImage implements engine.Engine: the daemon downloads the newest
// build of c.Ref ("images:debian/12") from its simplestreams server into
// the image store, cached and auto-updating like an image an instance
// creation downloads, so the next instance created from the alias starts
// from it.
func (a *Adapter) RefreshImage(ctx context.Context, c engine.ImageCheck, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	op := "refresh image " + c.Ref
	remote, alias, ok := strings.Cut(c.Ref, ":")
	server := a.server(remote)
	if !ok || alias == "" || server == "" {
		return "", engine.Unsupported(op, "the image has no remote source to refresh from")
	}
	kind := string(api.InstanceTypeContainer)
	if c.VM {
		kind = string(api.InstanceTypeVM)
	}
	post := api.ImagesPost{
		ImagePut: api.ImagePut{AutoUpdate: true},
		Source: &api.ImagesPostSource{
			ImageSource: api.ImageSource{Alias: alias, Server: server, Protocol: "simplestreams", ImageType: kind},
			Type:        "image", Mode: "pull",
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progress(engine.Progress{Phase: "downloading"})
	o, err := a.client(ctx, "").CreateImage(post, nil)
	if err != nil {
		return "", classify(ctx, op, err)
	}
	done, err := a.waitProgress(ctx, op, o, progress)
	if err != nil {
		return "", err
	}
	fp, _ := done.Metadata["fingerprint"].(string)
	if fp == "" {
		return "", engine.Errorf(engine.ClassTransient, op, errors.New("the daemon reported no fingerprint"))
	}
	return fp[:min(12, len(fp))], nil
}
