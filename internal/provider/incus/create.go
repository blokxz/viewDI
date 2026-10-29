package incus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"

	"github.com/blokxz/viewDI/internal/engine"
)

// Remotes maps image remote names to simplestreams servers.
var Remotes = map[string]string{"images": "https://images.linuxcontainers.org"}

// Images implements engine.Engine.
func (a *Adapter) Images(ctx context.Context) ([]engine.Image, error) {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	imgs, err := a.client(ctx, "").GetImages()
	if err != nil {
		return nil, classify(ctx, "list images", err)
	}
	out := make([]engine.Image, 0, len(imgs))
	for _, i := range imgs {
		img := engine.Image{
			Engine: engine.Incus, ID: i.Fingerprint[:min(12, len(i.Fingerprint))], Size: uint64(max(i.Size, 0)),
			Created: i.CreatedAt.UTC(), Description: i.Properties["description"], VM: i.Type == string(api.InstanceTypeVM),
		}
		for _, al := range i.Aliases {
			img.Refs = append(img.Refs, al.Name)
		}
		out = append(out, img)
	}
	return out, nil
}

// ----- image server search (simplestreams) -----

const maxIndex = 64 << 20 // bytes of images.json read (untrusted input)

type ssIndex struct {
	at     time.Time
	images []engine.RemoteImage
}

var (
	ssMu    sync.Mutex
	ssCache = map[string]ssIndex{} // by server URL
)

// SearchImages implements engine.Engine: it reads the "images" remote's
// simplestreams index (cached for an hour) and matches aliases, OS and
// release names for this host's architecture.
func (a *Adapter) SearchImages(ctx context.Context, query string) ([]engine.RemoteImage, error) {
	server := a.imageServer
	if server == "" {
		server = Remotes["images"]
	}
	all, err := fetchIndex(ctx, server)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []engine.RemoteImage
	for _, r := range all {
		if strings.Contains(strings.ToLower(r.Ref+" "+r.Description), q) {
			out = append(out, r)
		}
	}
	return out, nil
}

func fetchIndex(ctx context.Context, server string) ([]engine.RemoteImage, error) {
	ssMu.Lock()
	c, ok := ssCache[server]
	ssMu.Unlock()
	if ok && time.Since(c.at) < time.Hour {
		return c.images, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(server, "/")+"/streams/v1/images.json", nil)
	if err != nil {
		return nil, engine.Wrap("search images", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, engine.Wrap("search images", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, engine.Errorf(engine.ClassTransient, "search images", fmt.Errorf("%s: HTTP %d", server, resp.StatusCode))
	}
	images, err := parseIndex(io.LimitReader(resp.Body, maxIndex), hostArch())
	if err != nil {
		return nil, engine.Wrap("search images", err)
	}
	ssMu.Lock()
	ssCache[server] = ssIndex{at: time.Now(), images: images}
	ssMu.Unlock()
	return images, nil
}

func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return runtime.GOARCH
}

// parseIndex reads a simplestreams images.json into one entry per product
// of arch, named "images:<shortest alias>".
func parseIndex(r io.Reader, arch string) ([]engine.RemoteImage, error) {
	var doc struct {
		Products map[string]struct {
			Aliases  string `json:"aliases"`
			Arch     string `json:"arch"`
			OS       string `json:"os"`
			Release  string `json:"release_title"`
			Variant  string `json:"variant"`
			Versions map[string]struct {
				Items map[string]struct {
					Ftype string `json:"ftype"`
				} `json:"items"`
			} `json:"versions"`
		} `json:"products"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	var out []engine.RemoteImage
	for _, p := range doc.Products {
		if p.Arch != arch || p.Aliases == "" {
			continue
		}
		aliases := strings.Split(p.Aliases, ",")
		slices.SortFunc(aliases, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b)) })
		vm := false
		for _, v := range p.Versions {
			for _, it := range v.Items {
				vm = vm || it.Ftype == "disk-kvm.img"
			}
		}
		desc := strings.TrimSpace(p.OS + " " + p.Release + " " + p.Arch)
		if p.Variant != "" && p.Variant != "default" {
			desc += " (" + p.Variant + ")"
		}
		out = append(out, engine.RemoteImage{Ref: "images:" + aliases[0], Description: desc, VM: vm})
	}
	slices.SortFunc(out, func(a, b engine.RemoteImage) int { return cmp.Compare(a.Ref, b.Ref) })
	return out, nil
}

// ----- create and import -----

// Create implements engine.Engine. Remote images ("images:debian/12") are
// downloaded by the daemon; the operation's progress is reported.
func (a *Adapter) Create(ctx context.Context, spec engine.CreateSpec, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	if err := engine.ValidateName(engine.Incus, spec.Name); err != nil {
		return "", engine.Errorf(engine.ClassConflict, "create", err)
	}
	project := cmp.Or(spec.Project, a.defaultProject())
	post, err := instancePost(spec)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	progress(engine.Progress{Phase: "creating"})
	op, err := a.client(ctx, project).CreateInstance(post)
	if err != nil {
		return "", classify(ctx, "create "+spec.Name, err)
	}
	if _, err := a.waitProgress(ctx, "create "+spec.Name, op, progress); err != nil {
		return "", err
	}
	id := project + "/" + spec.Name
	if spec.Start {
		progress(engine.Progress{Phase: "starting"})
		if err := a.Act(ctx, id, engine.Start, engine.ActionOptions{}); err != nil {
			return id, err
		}
	}
	return id, nil
}

// instancePost maps a spec onto the create request.
func instancePost(spec engine.CreateSpec) (api.InstancesPost, error) {
	src := api.InstanceSource{Type: "image", Alias: spec.Image}
	if remote, alias, ok := strings.Cut(spec.Image, ":"); ok {
		server, known := Remotes[remote]
		if !known {
			return api.InstancesPost{}, engine.Errorf(engine.ClassConflict, "create", fmt.Errorf("unknown image remote %q", remote))
		}
		src = api.InstanceSource{Type: "image", Alias: alias, Server: server, Protocol: "simplestreams"}
	}
	post := api.InstancesPost{Name: spec.Name, Source: src, Type: api.InstanceTypeContainer}
	if spec.Kind == engine.KindVM {
		post.Type = api.InstanceTypeVM
	}
	post.Profiles = spec.Profiles
	post.Config = map[string]string{}
	if spec.CPU > 0 {
		post.Config["limits.cpu"] = strconv.Itoa(spec.CPU)
	}
	if spec.Memory != "" {
		post.Config["limits.memory"] = spec.Memory
	}
	post.Devices = map[string]map[string]string{}
	if spec.Pool != "" {
		post.Devices["root"] = map[string]string{"type": "disk", "path": "/", "pool": spec.Pool}
	}
	if spec.Network != "" {
		post.Devices["eth0"] = map[string]string{"type": "nic", "network": spec.Network, "name": "eth0"}
	}
	return post, nil
}

// Import implements engine.Engine: instance backups and unified images.
func (a *Adapter) Import(ctx context.Context, spec engine.ImportSpec, progress engine.ProgressFunc) (string, error) {
	if progress == nil {
		progress = func(engine.Progress) {}
	}
	if spec.Kind.Engine() != engine.Incus {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("%s files go to Docker", spec.Kind))
	}
	f, err := os.Open(spec.Path)
	if err != nil {
		return "", engine.Wrap("import", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return "", engine.Errorf(engine.ClassConflict, "import", fmt.Errorf("%s is not a regular file", spec.Path))
	}
	src := &reader{r: f, total: fi.Size(), progress: progress}
	project := cmp.Or(spec.Project, a.defaultProject())
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	c := a.client(ctx, project)
	var op incus.Operation
	if spec.Kind == engine.ImportIncusBackup {
		if spec.Name != "" {
			if err := engine.ValidateName(engine.Incus, spec.Name); err != nil {
				return "", engine.Errorf(engine.ClassConflict, "import", err)
			}
		}
		op, err = c.CreateInstanceFromBackup(incus.InstanceBackupArgs{BackupFile: src, PoolName: spec.Pool, Name: spec.Name})
	} else {
		post := api.ImagesPost{}
		if spec.Name != "" {
			post.Aliases = []api.ImageAlias{{Name: spec.Name}}
		}
		op, err = c.CreateImage(post, &incus.ImageCreateArgs{MetaFile: src, MetaName: filepath.Base(spec.Path)})
	}
	if err != nil {
		return "", classify(ctx, "import", err)
	}
	done, err := a.waitProgress(ctx, "import", op, progress)
	if err != nil {
		return "", err
	}
	if spec.Kind == engine.ImportIncusImage {
		fp, _ := done.Metadata["fingerprint"].(string)
		return cmp.Or(spec.Name, fp[:min(12, len(fp))]), nil
	}
	name := spec.Name
	if insts := done.Resources["instances"]; name == "" && len(insts) > 0 {
		name = lastSegment(insts[0])
	}
	return project + "/" + name, nil
}

// reader reports upload progress.
type reader struct {
	r        io.Reader
	n, total int64
	progress engine.ProgressFunc
}

func (r *reader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	r.progress(engine.Progress{Phase: "uploading", Current: r.n, Total: r.total})
	return n, err
}

// waitProgress waits for an operation one second at a time, reporting the
// daemon's progress text ("rootfs: 45% (12.3MB/s)") as a percentage.
func (a *Adapter) waitProgress(ctx context.Context, opName string, o incus.Operation, progress engine.ProgressFunc) (api.Operation, error) {
	id := o.Get().ID
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix.socket/1.0/operations/"+url.PathEscape(id)+"/wait?timeout=1", nil)
		if err != nil {
			return api.Operation{}, engine.Wrap(opName, err)
		}
		resp, err := a.srv.DoHTTP(req)
		if err != nil {
			return api.Operation{}, classify(ctx, opName, err)
		}
		var env struct {
			Type     string        `json:"type"`
			Error    string        `json:"error"`
			Code     int           `json:"error_code"`
			Metadata api.Operation `json:"metadata"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env)
		resp.Body.Close()
		switch {
		case err != nil:
			return api.Operation{}, classify(ctx, opName, err)
		case env.Type == "error":
			return api.Operation{}, engine.Errorf(engine.ClassTransient, opName, fmt.Errorf("HTTP %d: %s", env.Code, env.Error))
		}
		op := env.Metadata
		switch op.StatusCode {
		case api.Success:
			return op, nil
		case api.Cancelled:
			return op, engine.Errorf(engine.ClassCanceled, opName, errors.New("operation canceled"))
		case api.Failure:
			return op, engine.Errorf(engine.ClassConflict, opName, errors.New(op.Err))
		}
		if p, ok := opProgress(op.Metadata); ok {
			progress(p)
		}
		if ctx.Err() != nil {
			return op, engine.Wrap(opName, ctx.Err())
		}
	}
}

// opProgress reads "download_progress" style metadata: "rootfs: 45% (…)".
func opProgress(meta map[string]any) (engine.Progress, bool) {
	for key, v := range meta {
		s, ok := v.(string)
		if !ok || !strings.HasSuffix(key, "_progress") {
			continue
		}
		_, rest, found := strings.Cut(s, ": ")
		if !found {
			rest = s
		}
		pct, _, ok := strings.Cut(rest, "%")
		n, err := strconv.Atoi(strings.TrimSpace(pct))
		if !ok || err != nil {
			continue
		}
		phase := strings.TrimSuffix(key, "_progress")
		if phase == "download" {
			phase = "downloading"
		}
		return engine.Progress{Phase: phase, Current: int64(n), Total: 100}, true
	}
	return engine.Progress{}, false
}
