package fake

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/blokxz/viewDI/internal/engine"
)

// catalog is what SearchImages finds, per engine.
var catalog = map[engine.ID][]engine.RemoteImage{
	engine.Docker: {
		{Ref: "nginx", Description: "Official build of Nginx.", Official: true},
		{Ref: "postgres", Description: "The PostgreSQL object-relational database system", Official: true},
		{Ref: "redis", Description: "Redis is an open source key-value store", Official: true},
		{Ref: "grafana/grafana", Description: "The official Grafana docker container"},
		{Ref: "alpine", Description: "A minimal Docker image based on Alpine Linux", Official: true},
		{Ref: "traefik", Description: "Traefik, the cloud native edge router", Official: true},
	},
	engine.Incus: {
		{Ref: "images:debian/12", Description: "Debian bookworm amd64", VM: true},
		{Ref: "images:debian/13", Description: "Debian trixie amd64", VM: true},
		{Ref: "images:ubuntu/24.04", Description: "Ubuntu noble amd64", VM: true},
		{Ref: "images:alpine/3.20", Description: "Alpine 3.20 amd64"},
		{Ref: "images:archlinux", Description: "Archlinux current amd64", VM: true},
		{Ref: "images:fedora/41", Description: "Fedora 41 amd64", VM: true},
	},
}

// AddImage adds a local image.
func (e *Engine) AddImage(img engine.Image) {
	img.Engine = e.id
	if img.ID == "" && len(img.Refs) > 0 {
		img.ID = imageID(img.Refs[0])
	}
	if img.Created.IsZero() {
		img.Created = e.clk.Now()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.images = append(e.images, img)
}

// Images implements engine.Engine.
func (e *Engine) Images(ctx context.Context) ([]engine.Image, error) {
	if err := e.enter(ctx, "list images"); err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]engine.Image, 0, len(e.images))
	for _, img := range e.images {
		img.Refs = slices.Clone(img.Refs)
		out = append(out, img)
	}
	return out, nil
}

// SearchImages implements engine.Engine with a small built-in catalog.
func (e *Engine) SearchImages(ctx context.Context, query string) ([]engine.RemoteImage, error) {
	if err := e.enter(ctx, "search images"); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []engine.RemoteImage
	for _, r := range catalog[e.id] {
		if strings.Contains(strings.ToLower(r.Ref+" "+r.Description), q) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (e *Engine) hasImage(ref string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, img := range e.images {
		if slices.Contains(img.Refs, ref) || slices.Contains(img.Refs, ref+":latest") || img.ID == ref {
			return true
		}
	}
	return false
}

// step waits share × the action delay, then reports progress i of n.
func (e *Engine) step(ctx context.Context, op, phase string, i, n int64, share float64, progress engine.ProgressFunc) error {
	e.mu.Lock()
	d := time.Duration(float64(e.actionDelay) * share)
	e.mu.Unlock()
	if d > 0 {
		select {
		case <-e.clk.After(d):
		case <-ctx.Done():
			return engine.Wrap(op, ctx.Err())
		}
	}
	if err := e.enter(ctx, op); err != nil {
		return err
	}
	if progress != nil {
		progress(engine.Progress{Phase: phase, Current: i, Total: n})
	}
	return nil
}

// Create implements engine.Engine. A missing image is pulled (Docker with
// Pull, Incus remote images) in four steps of half the action delay each
// that report progress; creating takes the whole delay.
func (e *Engine) Create(ctx context.Context, spec engine.CreateSpec, progress engine.ProgressFunc) (string, error) {
	if err := e.enter(ctx, "create"); err != nil {
		return "", err
	}
	if err := engine.ValidateName(e.id, spec.Name); err != nil {
		return "", engine.Errorf(engine.ClassConflict, "create", err)
	}
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("viewdi-%d", e.clk.Now().UnixNano()%100000)
	}
	if _, exists := e.Instance(spec.Name); exists {
		return "", engine.Errorf(engine.ClassConflict, "create", fmt.Errorf("an instance named %q already exists", spec.Name))
	}
	remote := e.id == engine.Incus && strings.Contains(spec.Image, ":")
	if !e.hasImage(spec.Image) {
		switch {
		case e.id == engine.Docker && !spec.Pull:
			return "", engine.NotFound("create", "image "+spec.Image+" (tick pull)")
		case e.id == engine.Incus && !remote:
			return "", engine.NotFound("create", "image alias "+spec.Image)
		}
		phase := "pulling"
		if remote {
			phase = "downloading"
		}
		const steps = 4
		for i := range int64(steps) {
			if err := e.step(ctx, "create", phase, (i+1)*25<<20, steps*25<<20, 0.5, progress); err != nil {
				return "", err
			}
		}
		e.AddImage(engine.Image{Refs: []string{spec.Image}, Size: steps * 25 << 20, VM: spec.Kind == engine.KindVM})
	}
	if err := e.step(ctx, "create", "creating", 1, 1, 1, progress); err != nil {
		return "", err
	}
	in := engine.Instance{
		Name: spec.Name, Project: cmp.Or(spec.Project, map[engine.ID]string{engine.Incus: "default"}[e.id]),
		Image: spec.Image, Kind: spec.Kind, State: engine.StateCreated, Labels: spec.Labels, Env: spec.Env,
		Ports: spec.Ports, Mounts: spec.Volumes,
	}
	if e.id == engine.Incus {
		in.State = engine.StateStopped
		profiles := spec.Profiles
		if len(profiles) == 0 {
			profiles = []string{"default"}
		}
		in.Extra = &engine.IncusExtra{Profiles: profiles, Architecture: "x86_64", Location: "none"}
		if spec.Network != "" {
			in.Networks = []engine.Attachment{{Network: spec.Network}}
		}
	} else {
		in.Kind = engine.KindContainer
		in.Extra = &engine.DockerExtra{RestartPolicy: cmp.Or(spec.RestartPolicy, "no"), ImageID: imageID(spec.Image)}
		if spec.Network != "" {
			in.Networks = []engine.Attachment{{Network: spec.Network}}
		}
	}
	in = e.Add(in)
	if spec.Start {
		e.SetState(in.Name, engine.StateRunning)
	}
	return in.ID, nil
}

// Import implements engine.Engine. The file must exist; its content is not
// parsed.
func (e *Engine) Import(ctx context.Context, spec engine.ImportSpec, progress engine.ProgressFunc) (string, error) {
	if err := e.enter(ctx, "import"); err != nil {
		return "", err
	}
	if spec.Kind.Engine() != e.id {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("%s files go to %s", spec.Kind, spec.Kind.Engine().Label()))
	}
	fi, err := os.Stat(spec.Path)
	switch {
	case err != nil:
		return "", engine.Errorf(engine.ClassNotFound, "import", err)
	case !fi.Mode().IsRegular():
		return "", engine.Errorf(engine.ClassConflict, "import", errors.New(spec.Path+" is not a regular file"))
	}
	size := max(fi.Size(), 1)
	for i := range int64(4) {
		if err := e.step(ctx, "import", "uploading", (i+1)*size/4, size, 0.5, progress); err != nil {
			return "", err
		}
	}
	base := strings.TrimSuffix(filepath.Base(spec.Path), filepath.Ext(spec.Path))
	base = strings.TrimSuffix(base, ".tar")
	switch spec.Kind {
	case engine.ImportDockerImage:
		ref := base + ":imported"
		e.AddImage(engine.Image{Refs: []string{ref}, Size: uint64(fi.Size())})
		return ref, nil
	case engine.ImportDockerRootfs:
		if err := engine.ValidateImage(spec.Name); err != nil {
			return "", engine.Errorf(engine.ClassConflict, "import", err)
		}
		e.AddImage(engine.Image{Refs: []string{spec.Name}, Size: uint64(fi.Size())})
		return spec.Name, nil
	case engine.ImportIncusImage:
		img := engine.Image{Size: uint64(fi.Size()), Description: base}
		if spec.Name != "" {
			img.Refs = []string{spec.Name}
		}
		img.ID = imageID("file:" + spec.Path)[7:19]
		e.AddImage(img)
		return cmp.Or(spec.Name, img.ID), nil
	}
	name := cmp.Or(spec.Name, base)
	if err := engine.ValidateName(engine.Incus, name); err != nil {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("instance name %q: %w", name, err))
	}
	if _, exists := e.Instance(name); exists {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("an instance named %q already exists", name))
	}
	in := e.Add(engine.Instance{
		Name: name, Project: cmp.Or(spec.Project, "default"), Image: "(backup) " + base, State: engine.StateStopped,
		Extra: &engine.IncusExtra{Profiles: []string{"default"}, Architecture: "x86_64", Location: "none"},
	})
	return in.ID, nil
}
