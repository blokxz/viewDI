package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/blokxz/viewDI/internal/engine"
)

// Images implements engine.Engine.
func (a *Adapter) Images(ctx context.Context) ([]engine.Image, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	res, err := a.cli.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, classify("list images", err)
	}
	out := make([]engine.Image, 0, len(res.Items))
	for _, s := range res.Items {
		img := engine.Image{Engine: engine.Docker, ID: s.ID, Size: uint64(max(s.Size, 0)), Created: time.Unix(s.Created, 0).UTC()}
		for _, t := range s.RepoTags {
			if t != "<none>:<none>" {
				img.Refs = append(img.Refs, t)
			}
		}
		out = append(out, img)
	}
	return out, nil
}

// SearchImages implements engine.Engine with the daemon's registry search.
func (a *Adapter) SearchImages(ctx context.Context, query string) ([]engine.RemoteImage, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := a.cli.ImageSearch(ctx, query, client.ImageSearchOptions{Limit: 25})
	if err != nil {
		return nil, classify("search images", err)
	}
	out := make([]engine.RemoteImage, 0, len(res.Items))
	for _, r := range res.Items {
		out = append(out, engine.RemoteImage{Ref: r.Name, Description: r.Description, Official: r.IsOfficial})
	}
	return out, nil
}

// Create implements engine.Engine: optional pull with progress, create,
// optional start.
func (a *Adapter) Create(ctx context.Context, spec engine.CreateSpec, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	if spec.Pull {
		if err := a.pull(ctx, spec.Image, progress); err != nil {
			return "", err
		}
	}
	progress(engine.Progress{Phase: "creating"})
	cctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	cfg, host, err := containerConfig(spec)
	if err != nil {
		return "", err
	}
	res, err := a.cli.ContainerCreate(cctx, client.ContainerCreateOptions{Config: cfg, HostConfig: host, Name: spec.Name})
	if err != nil {
		return "", classify("create "+spec.Name, err)
	}
	if spec.Start {
		progress(engine.Progress{Phase: "starting"})
		if err := a.Act(ctx, res.ID, engine.Start, engine.ActionOptions{}); err != nil {
			return res.ID, err
		}
	}
	return res.ID, nil
}

// containerConfig maps a spec onto the create request.
func containerConfig(spec engine.CreateSpec) (*container.Config, *container.HostConfig, error) {
	cfg := &container.Config{Image: spec.Image, Env: spec.Env, Labels: spec.Labels, ExposedPorts: network.PortSet{}}
	host := &container.HostConfig{PortBindings: network.PortMap{}}
	if spec.RestartPolicy != "" {
		host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(spec.RestartPolicy)}
	}
	if spec.Network != "" {
		host.NetworkMode = container.NetworkMode(spec.Network)
	}
	for _, p := range spec.Ports {
		port, err := network.ParsePort(fmt.Sprintf("%d/%s", p.ContainerPort, p.Protocol))
		if err != nil {
			return nil, nil, engine.Errorf(engine.ClassConflict, "create", err)
		}
		cfg.ExposedPorts[port] = struct{}{}
		if p.HostPort == 0 && p.HostIP == "" {
			continue
		}
		b := network.PortBinding{HostPort: strconv.Itoa(int(p.HostPort))}
		if p.HostPort == 0 {
			b.HostPort = ""
		}
		if p.HostIP != "" {
			ip, err := netip.ParseAddr(p.HostIP)
			if err != nil {
				return nil, nil, engine.Errorf(engine.ClassConflict, "create", fmt.Errorf("host IP %q: %w", p.HostIP, err))
			}
			b.HostIP = ip
		}
		host.PortBindings[port] = append(host.PortBindings[port], b)
	}
	for _, m := range spec.Volumes {
		bind := m.Source + ":" + m.Destination
		if m.ReadOnly {
			bind += ":ro"
		}
		host.Binds = append(host.Binds, bind)
	}
	return cfg, host, nil
}

// pull pulls ref, summing the layers' download progress.
func (a *Adapter) pull(ctx context.Context, ref string, progress engine.ProgressFunc) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	resp, err := a.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return classify("pull "+ref, err)
	}
	defer resp.Close()
	type layer struct{ cur, total int64 }
	layers := map[string]layer{}
	var order []string
	for msg, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return classify("pull "+ref, err)
		}
		if msg.Error != nil {
			return engine.Errorf(engine.ClassConflict, "pull "+ref, errors.New(msg.Error.Message))
		}
		if msg.ID == "" || msg.Progress == nil || msg.Progress.Total <= 0 || !strings.HasPrefix(msg.Status, "Downloading") {
			continue
		}
		if _, ok := layers[msg.ID]; !ok {
			order = append(order, msg.ID)
		}
		layers[msg.ID] = layer{msg.Progress.Current, msg.Progress.Total}
		var p engine.Progress
		p.Phase = "pulling"
		for _, id := range order {
			p.Current += layers[id].cur
			p.Total += layers[id].total
		}
		progress(p)
	}
	return nil
}

// counter reports how much of a file was read.
type counter struct {
	r        io.Reader
	n, total int64
	progress engine.ProgressFunc
	phase    string
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	c.progress(engine.Progress{Phase: c.phase, Current: c.n, Total: c.total})
	return n, err
}

// openUpload opens a local file for an upload with progress.
func openUpload(path string, progress engine.ProgressFunc) (*os.File, *counter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, engine.Wrap("import", err)
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("%s is not a regular file", path))
	}
	return f, &counter{r: f, total: fi.Size(), progress: progress, phase: "uploading"}, nil
}

// Import implements engine.Engine: docker load or docker import.
func (a *Adapter) Import(ctx context.Context, spec engine.ImportSpec, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	if spec.Kind.Engine() != engine.Docker {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("%s files go to Incus", spec.Kind))
	}
	f, src, err := openUpload(spec.Path, progress)
	if err != nil {
		return "", err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	var body io.ReadCloser
	switch spec.Kind {
	case engine.ImportDockerRootfs:
		if err := engine.ValidateImage(spec.Name); err != nil {
			return "", engine.Errorf(engine.ClassConflict, "import", err)
		}
		body, err = a.cli.ImageImport(ctx, client.ImageImportSource{Source: src, SourceName: "-"}, spec.Name, client.ImageImportOptions{})
	default:
		body, err = a.cli.ImageLoad(ctx, src)
	}
	if err != nil {
		return "", classify("import", err)
	}
	defer body.Close()
	result, err := loadResult(body)
	if err != nil {
		return "", err
	}
	if spec.Kind == engine.ImportDockerRootfs {
		return spec.Name, nil
	}
	return result, nil
}

// loadResult reads a load/import message stream: the loaded image (from
// "Loaded image: …" lines or an import's status digest), or the error.
func loadResult(r io.Reader) (string, error) {
	dec := json.NewDecoder(io.LimitReader(r, 4<<20))
	var refs []string
	for {
		var m jsonstream.Message
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", engine.Wrap("import", err)
		}
		if m.Error != nil {
			return "", engine.Errorf(engine.ClassConflict, "import", errors.New(m.Error.Message))
		}
		for _, prefix := range []string{"Loaded image: ", "Loaded image ID: "} {
			if ref, ok := strings.CutPrefix(strings.TrimSpace(m.Stream), prefix); ok {
				refs = append(refs, ref)
			}
		}
		if strings.HasPrefix(m.Status, "sha256:") {
			refs = append(refs, m.Status)
		}
	}
	refs = slices.Compact(refs)
	if len(refs) == 0 {
		return "", engine.Errorf(engine.ClassTransient, "import", errors.New("the daemon reported no image"))
	}
	return strings.Join(refs, ", "), nil
}
