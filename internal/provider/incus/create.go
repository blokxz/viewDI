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

// ----- image server index (simplestreams) -----

const maxIndex = 64 << 20 // bytes of images.json read (untrusted input)

// ssProduct is one product of a simplestreams index, with its newest build.
type ssProduct struct {
	Aliases []string
	Arch    string
	Desc    string
	Serial  string // newest version name ("20261028_05:24")
	CT, VM  string // fingerprints of the newest container and VM builds; "" when absent
	CTSize  uint64 // download sizes of those builds, 0 when the index does not say
	VMSize  uint64
	HasVM   bool // some version has a VM image
}

type ssIndex struct {
	at       time.Time
	products []ssProduct
}

var (
	ssMu    sync.Mutex
	ssCache = map[string]ssIndex{} // by server URL
)

// SearchImages implements engine.Engine: it reads the "images" remote's
// simplestreams index (cached for an hour) and matches aliases, OS and
// release names for this host's architecture.
func (a *Adapter) SearchImages(ctx context.Context, query string) ([]engine.RemoteImage, error) {
	products, err := fetchIndex(ctx, a.server("images"), "search images")
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var out []engine.RemoteImage
	for _, r := range remoteImages(products, hostArch()) {
		if strings.Contains(strings.ToLower(r.Ref+" "+r.Description), q) {
			out = append(out, r)
		}
	}
	return out, nil
}

// server returns the simplestreams URL of an image remote ("" when unknown).
func (a *Adapter) server(remote string) string {
	if a.imageServer != "" {
		return a.imageServer
	}
	return Remotes[remote]
}

// ImageUpdateStatus implements engine.Engine: it finds the newest build of
// c.Ref ("images:debian/bookworm/default") for c.Arch on the image server
// and compares its fingerprint (else its serial) with the instance's base
// image. The index is shared with SearchImages and cached for an hour.
func (a *Adapter) ImageUpdateStatus(ctx context.Context, c engine.ImageCheck) (engine.ImageStatus, error) {
	st := engine.ImageStatus{Check: c, Current: c.Local}
	op := "check image " + c.Ref
	if err := ctx.Err(); err != nil {
		return st, classify(ctx, op, err)
	}
	remote, alias, ok := strings.Cut(c.Ref, ":")
	if !ok || alias == "" {
		st.State = engine.ImageLocal
		return st, nil
	}
	server := a.server(remote)
	if server == "" {
		st.State = engine.ImageLocal
		return st, nil
	}
	products, err := fetchIndex(ctx, server, op)
	if err != nil {
		return st, err
	}
	var p *ssProduct
	for i := range products {
		if products[i].Arch == c.Arch && slices.Contains(products[i].Aliases, alias) {
			p = &products[i]
			break
		}
	}
	if p == nil {
		return st, engine.Errorf(engine.ClassNotFound, op, fmt.Errorf("%w: no %s build on %s", engine.ErrNotFound, c.Arch, server))
	}
	st.Available, st.NewSerial = p.CT, p.Serial
	if c.VM {
		st.Available = p.VM
	}
	st.Serial = a.serialOf(ctx, c.Local)
	switch {
	case st.Available != "" && c.Local != "":
		st.State = engine.ImageOutdated
		if strings.EqualFold(st.Available, c.Local) {
			st.State = engine.ImageUpToDate
		}
	case st.Serial != "" && st.NewSerial != "":
		st.State = engine.ImageOutdated
		if st.Serial >= st.NewSerial {
			st.State = engine.ImageUpToDate
		}
	default:
		return st, engine.Errorf(engine.ClassTransient, op, errors.New("the image server lists no fingerprint for this build"))
	}
	return st, nil
}

// serialOf returns the serial of a cached local image, or "" (cached images
// expire; the fingerprint comparison does not need it).
func (a *Adapter) serialOf(ctx context.Context, fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	img, _, err := a.client(ctx, "").GetImage(fingerprint)
	if err != nil {
		return ""
	}
	return img.Properties["serial"]
}

func fetchIndex(ctx context.Context, server, op string) ([]ssProduct, error) {
	ssMu.Lock()
	c, ok := ssCache[server]
	ssMu.Unlock()
	if ok && time.Since(c.at) < time.Hour {
		return c.products, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(server, "/")+"/streams/v1/images.json", nil)
	if err != nil {
		return nil, engine.Wrap(op, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, engine.Wrap(op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, engine.Errorf(engine.ClassTransient, op, fmt.Errorf("%s: HTTP %d", server, resp.StatusCode))
	}
	products, err := parseProducts(io.LimitReader(resp.Body, maxIndex))
	if err != nil {
		return nil, engine.Wrap(op, err)
	}
	ssMu.Lock()
	ssCache[server] = ssIndex{at: time.Now(), products: products}
	ssMu.Unlock()
	return products, nil
}

func hostArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return runtime.GOARCH
}

type ssItem struct {
	Ftype    string `json:"ftype"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	SquashFS string `json:"combined_squashfs_sha256"`
	RootXz   string `json:"combined_rootxz_sha256"`
	Combined string `json:"combined_sha256"`
	DiskKvm  string `json:"combined_disk-kvm-img_sha256"`
	DiskImg  string `json:"combined_disk1-img_sha256"`
	DiskUefi string `json:"combined_uefi1-img_sha256"`
}

// parseProducts reads a simplestreams images.json. Fingerprints follow
// the Incus client (shared/simplestreams): the metadata item carries the
// combined hash of metadata and root image.
func parseProducts(r io.Reader) ([]ssProduct, error) {
	var doc struct {
		Products map[string]struct {
			Aliases  string `json:"aliases"`
			Arch     string `json:"arch"`
			OS       string `json:"os"`
			Release  string `json:"release_title"`
			Variant  string `json:"variant"`
			Versions map[string]struct {
				Items map[string]ssItem `json:"items"`
			} `json:"versions"`
		} `json:"products"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	out := make([]ssProduct, 0, len(doc.Products))
	for _, p := range doc.Products {
		if p.Aliases == "" {
			continue
		}
		sp := ssProduct{Arch: p.Arch, Aliases: strings.Split(p.Aliases, ",")}
		slices.SortFunc(sp.Aliases, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b)) })
		sp.Desc = strings.TrimSpace(p.OS + " " + p.Release + " " + p.Arch)
		if p.Variant != "" && p.Variant != "default" {
			sp.Desc += " (" + p.Variant + ")"
		}
		for name, v := range p.Versions {
			ct, vm := fingerprints(v.Items)
			sp.HasVM = sp.HasVM || hasFtype(v.Items, "disk-kvm.img")
			if name > sp.Serial {
				sp.Serial, sp.CT, sp.VM = name, ct, vm
				sp.CTSize, sp.VMSize = downloadSizes(v.Items)
			}
		}
		out = append(out, sp)
	}
	slices.SortFunc(out, func(a, b ssProduct) int {
		return cmp.Or(cmp.Compare(a.Aliases[0], b.Aliases[0]), cmp.Compare(a.Arch, b.Arch))
	})
	return out, nil
}

// fingerprints returns the container and VM fingerprints of one version.
func fingerprints(items map[string]ssItem) (ct, vm string) {
	var meta *ssItem
	for _, it := range items {
		if it.Ftype == "incus.tar.xz" || it.Ftype == "lxd.tar.xz" {
			meta = &it
			break
		}
	}
	if meta == nil {
		return "", ""
	}
	switch {
	case hasFtype(items, "squashfs"):
		ct = meta.SquashFS
	case hasFtype(items, "root.tar.xz"):
		ct = cmp.Or(meta.RootXz, meta.Combined)
	}
	switch {
	case hasFtype(items, "disk-kvm.img"):
		vm = meta.DiskKvm
	case hasFtype(items, "disk1.img"):
		vm = meta.DiskImg
	case hasFtype(items, "uefi1.img"):
		vm = meta.DiskUefi
	}
	return ct, vm
}

func hasFtype(items map[string]ssItem, ftype string) bool {
	for _, it := range items {
		if it.Ftype == ftype {
			return true
		}
	}
	return false
}

// remoteImages lists the products of arch as search results named
// "images:<shortest alias>".
func remoteImages(products []ssProduct, arch string) []engine.RemoteImage {
	var out []engine.RemoteImage
	for _, p := range products {
		if p.Arch == arch {
			out = append(out, engine.RemoteImage{Ref: "images:" + p.Aliases[0], Description: p.Desc, VM: p.HasVM})
		}
	}
	slices.SortFunc(out, func(a, b engine.RemoteImage) int { return cmp.Compare(a.Ref, b.Ref) })
	return out
}

// parseIndex reads a simplestreams images.json into one search entry per
// product of arch.
func parseIndex(r io.Reader, arch string) ([]engine.RemoteImage, error) {
	products, err := parseProducts(r)
	if err != nil {
		return nil, err
	}
	return remoteImages(products, arch), nil
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
	for k, v := range spec.Config {
		post.Config[k] = v
	}
	for k, v := range spec.Labels { // Incus keeps labels as user.* keys
		post.Config["user."+k] = v
	}
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

// downloadSizes sums the files a container image and a VM image of one
// version download: the metadata plus the root filesystem or disk.
func downloadSizes(items map[string]ssItem) (ct, vm uint64) {
	var meta int64
	var root, disk int64
	for _, it := range items {
		switch it.Ftype {
		case "incus.tar.xz", "lxd.tar.xz":
			meta = it.Size
		case "squashfs", "root.tar.xz":
			root = it.Size
		case "disk-kvm.img", "disk1.img", "uefi1.img":
			disk = it.Size
		}
	}
	if root > 0 {
		ct = uint64(meta + root)
	}
	if disk > 0 {
		vm = uint64(meta + disk)
	}
	return ct, vm
}

// ImageSize implements engine.Engine: a local alias is answered from the
// daemon; "images:debian/12" is looked up in the image server's index (the
// one SearchImages caches), which says what the download weighs.
func (a *Adapter) ImageSize(ctx context.Context, ref string, vm bool) (engine.ImageSize, error) {
	local, err := a.Images(ctx)
	if err != nil {
		return engine.ImageSize{}, err
	}
	bare := ref
	if _, alias, ok := strings.Cut(ref, ":"); ok {
		bare = alias
	}
	for _, img := range local {
		if img.VM == vm && (slices.Contains(img.Refs, ref) || slices.Contains(img.Refs, bare)) {
			return engine.ImageSize{Local: true, Bytes: img.Size}, nil
		}
	}
	remote, alias, ok := strings.Cut(ref, ":")
	if !ok || a.server(remote) == "" {
		return engine.ImageSize{}, nil
	}
	op := "size of " + ref
	products, err := fetchIndex(ctx, a.server(remote), op)
	if err != nil {
		return engine.ImageSize{}, err
	}
	for _, p := range products {
		if p.Arch == hostArch() && slices.Contains(p.Aliases, alias) {
			if vm {
				return engine.ImageSize{Bytes: p.VMSize}, nil
			}
			return engine.ImageSize{Bytes: p.CTSize}, nil
		}
	}
	return engine.ImageSize{}, engine.Errorf(engine.ClassNotFound, op, fmt.Errorf("%w: no %s build on %s", engine.ErrNotFound, hostArch(), a.server(remote)))
}

// RemoveImage implements engine.Engine: id is the fingerprint prefix that
// Images reports.
func (a *Adapter) RemoveImage(ctx context.Context, id string) error {
	ctx, cancel := engine.WithDefaultTimeout(ctx)
	defer cancel()
	op := "remove image " + id
	o, err := a.client(ctx, "").DeleteImage(id)
	if err != nil {
		return classify(ctx, op, err)
	}
	return a.wait(ctx, op, o)
}
