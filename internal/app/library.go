package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/catalog"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
	libraryview "github.com/blokxz/viewDI/internal/ui/views/library"
)

// libraryRun is the context of a form opened from the Library: which entry
// it came from and the host ports it proposed, for the note afterwards.
type libraryRun struct {
	Engine engine.ID
	Entry  catalog.Entry
}

// loadLocalImages asks each engine which images it already has, so the
// Library can mark entries as local. It reads the daemons' local image
// lists only; it never touches the network.
func (m Model) loadLocalImages() tea.Cmd {
	src := m.opts.Images
	if src == nil {
		return nil
	}
	ctx := m.opts.Context
	var cmds []tea.Cmd
	for _, e := range engine.IDs {
		if ev := m.store.Snapshot().Engine(e); ev.Health.State != store.StateConnected {
			continue
		}
		cmds = append(cmds, func() tea.Msg {
			imgs, err := src.Images(ctx, e)
			if err != nil {
				return nil
			}
			var refs []string
			for _, img := range imgs {
				refs = append(refs, img.Refs...)
			}
			return libraryview.LocalImagesMsg{Engine: e, Refs: refs}
		})
	}
	return tea.Batch(cmds...)
}

// runLibrary opens the create form of the entry's engine with the entry's
// defaults filled in: a free name, free host ports, generated secrets.
func (m Model) runLibrary(e catalog.Entry) (tea.Model, tea.Cmd) {
	ids, cmd := m.startFlow("create")
	if len(ids) == 0 {
		return m, cmd
	}
	if !slices.Contains(ids, e.Engine) {
		return m, toast.Show(theme.LevelWarn, textsafe.Clean(e.Name)+" needs "+e.Engine.Label()+", which is not available right now")
	}
	snap := m.store.Snapshot()
	ev := snap.Engine(e.Engine)
	var fields []form.Field
	var note string
	if e.Engine == engine.Incus {
		fields = incusFields(snap, ev)
		var err error
		if fields, err = prefillIncus(fields, e, snap, ev); err != nil {
			return m, toast.Show(theme.LevelWarn, err.Error())
		}
	} else {
		fields = dockerFields(ev)
		fields, note = prefillDocker(fields, e, snap)
	}
	title := "Run " + textsafe.Clean(e.Name)
	intro := fmt.Sprintf("from the library · on %s %s · progress in Jobs (5)", e.Engine.Label(), ev.Info.Version)
	if note != "" {
		intro += "\n" + note
	}
	f := form.New(m.keys, formCreatePrefix+string(e.Engine), title, intro, "Create", fields).WithContext(libraryRun{Engine: e.Engine, Entry: e})
	vm := e.Engine == engine.Incus && fieldValue(fields, "kind") == "vm"
	return m, tea.Batch(modal.Open(f), m.loadImages(e.Engine), m.localNote(e.Engine, e.Image, vm), m.autoSize(e.Engine, e.Image, vm))
}

func setValue(fields []form.Field, id, value string) {
	for i := range fields {
		if fields[i].ID == id {
			fields[i].Value = value
		}
	}
}

func setOn(fields []form.Field, id string, on bool) {
	for i := range fields {
		if fields[i].ID == id {
			fields[i].On = on
		}
	}
}

// freeName returns base, or base-2, base-3… when taken on that engine.
func freeName(snap *store.Snapshot, id engine.ID, project, base string) string {
	taken := map[string]bool{}
	for _, in := range snap.Instances {
		if in.Engine == id && (id == engine.Docker || in.Project == project) {
			taken[in.Name] = true
		}
	}
	name := base
	for n := 2; taken[name]; n++ {
		name = base + "-" + strconv.Itoa(n)
	}
	return name
}

// publishedPorts is every host port a Docker instance publishes, with its
// protocol.
func publishedPorts(snap *store.Snapshot) map[string]bool {
	used := map[string]bool{}
	for _, in := range snap.Instances {
		if in.Engine != engine.Docker {
			continue
		}
		for _, p := range in.Ports {
			if p.HostPort != 0 {
				used[portKey(p.HostPort, p.Protocol)] = true
			}
		}
	}
	return used
}

func portKey(port uint16, proto string) string {
	if proto == "" {
		proto = "tcp"
	}
	return strconv.Itoa(int(port)) + "/" + proto
}

// prefillDocker fills the Docker form from an entry and returns the note
// for the intro about any port that had to move.
func prefillDocker(fields []form.Field, e catalog.Entry, snap *store.Snapshot) ([]form.Field, string) {
	name := freeName(snap, engine.Docker, "", e.ID)
	setValue(fields, "image", e.Image)
	setOn(fields, "pull", true)
	setValue(fields, "name", name)
	used := publishedPorts(snap)
	var ports, moved []string
	for _, p := range e.Ports {
		proto := p.Protocol
		host := cmpOr(p.Host, p.Container)
		for used[portKey(host, proto)] && host < 65535 {
			host++
		}
		used[portKey(host, proto)] = true
		if host != cmpOr(p.Host, p.Container) {
			moved = append(moved, fmt.Sprintf("%d is taken, using %d", cmpOr(p.Host, p.Container), host))
		}
		spec := fmt.Sprintf("127.0.0.1:%d:%d", host, p.Container)
		if proto == "udp" {
			spec += "/udp"
		}
		ports = append(ports, spec)
	}
	setValue(fields, "ports", strings.Join(ports, ", "))
	var env []string
	for _, v := range e.Env {
		val := v.Value
		if v.Generate == "password" {
			val = generatePassword()
		}
		if val != "" {
			env = append(env, v.Name+"="+val)
		}
	}
	setValue(fields, "env", strings.Join(env, ", "))
	var vols []string
	for _, v := range e.Volumes {
		vols = append(vols, name+"-"+v.Name+":"+v.Path)
	}
	setValue(fields, "volumes", strings.Join(vols, ", "))
	if e.Restart != "" {
		setValue(fields, "restart", e.Restart)
	}
	setValue(fields, "labels", libraryview.LabelDocker+"="+e.ID)
	note := "ports are published on 127.0.0.1 only (edit to share them)"
	if len(moved) > 0 {
		note += "\n" + strings.Join(moved, "; ")
	}
	return fields, note
}

func cmpOr(a, b uint16) uint16 {
	if a != 0 {
		return a
	}
	return b
}

// prefillIncus fills the Incus form from an entry.
func prefillIncus(fields []form.Field, e catalog.Entry, snap *store.Snapshot, ev store.EngineView) ([]form.Field, error) {
	kind := e.DefaultKind
	if kind == "" && len(e.Kinds) > 0 {
		kind = e.Kinds[0]
	}
	var options, labels []string
	for _, k := range e.Kinds {
		if k == "vm" && !ev.Info.Capabilities.VMs {
			continue
		}
		options = append(options, k)
		labels = append(labels, map[string]string{"container": "container", "vm": "virtual machine"}[k])
	}
	if len(options) == 0 {
		return nil, fmt.Errorf("%s needs a virtual machine, which this Incus cannot run", textsafe.Clean(e.Name))
	}
	if !slices.Contains(options, kind) {
		kind = options[0]
	}
	project := ev.Project
	if project == "" {
		project = "default"
	}
	setValue(fields, "image", e.Image)
	setValue(fields, "name", freeName(snap, engine.Incus, project, e.ID))
	for i := range fields {
		if fields[i].ID == "kind" {
			fields[i].Options, fields[i].Labels, fields[i].Value = options, labels, kind
		}
	}
	if len(e.Profiles) > 0 {
		setValue(fields, "profiles", strings.Join(e.Profiles, ", "))
	}
	if e.CPU > 0 {
		setValue(fields, "cpu", strconv.Itoa(e.CPU))
	}
	setValue(fields, "memory", e.Memory)
	return fields, nil
}

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword returns a random 24-character secret from crypto/rand.
// The alphabet avoids look-alike characters and anything the form's
// comma-separated environment field or a shell would treat specially.
func generatePassword() string {
	out := make([]byte, 24)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("library: the system random source failed: " + err.Error())
		}
		out[i] = passwordAlphabet[n.Int64()]
	}
	return string(out)
}

// applyEntry adds what the entry carries but the form cannot show: the
// library label and, for Incus, the instance config (cloud-init).
func applyEntry(spec engine.CreateSpec, run libraryRun) engine.CreateSpec {
	e := run.Entry
	if spec.Labels == nil {
		spec.Labels = map[string]string{}
	}
	if run.Engine == engine.Incus {
		spec.Labels[libraryview.LabelIncus] = e.ID
		spec.Config = map[string]string{}
		for k, v := range e.Config {
			spec.Config[k] = v
		}
		if e.CloudInit != "" {
			spec.Config["cloud-init.user-data"] = e.CloudInit
		}
		return spec
	}
	spec.Labels[libraryview.LabelDocker] = e.ID
	return spec
}

// submitLibrary creates the instance of a library form and remembers the
// note to show once the job succeeds.
func (m Model) submitLibrary(run libraryRun, v form.Values) (tea.Model, tea.Cmd) {
	spec := applyEntry(createSpec(run.Engine, v), run)
	if spec.Project == "" && run.Engine == engine.Incus {
		spec.Project = m.store.Snapshot().Engine(run.Engine).Project
	}
	name := spec.Name
	if name == "" {
		name = spec.Image
	}
	hostPorts := map[uint16]uint16{}
	for _, p := range spec.Ports {
		hostPorts[p.ContainerPort] = p.HostPort
	}
	model, cmd := m.submit("create", []jobs.Request{{Key: engine.MakeKey(run.Engine, ""), Name: name, Create: &spec}})
	mm := model.(Model)
	if b, ok := mm.batches[mm.nextBatch]; ok {
		b.noteTitle = textsafe.Clean(name) + " is ready"
		b.note = catalog.RenderNote(run.Entry.Note, spec.Name, hostPorts)
	}
	return mm, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Creating "+textsafe.Clean(name)+"… progress in Jobs (5)"))
}

// showNote opens the entry's note in a dialog.
func (m Model) showNote(title, note string) tea.Cmd {
	if strings.TrimSpace(note) == "" {
		return nil
	}
	return modal.Open(modal.NewConfirm(m.keys, textsafe.Clean(title), textsafe.Clean(note), "Close", false, nil))
}

func fieldValue(fields []form.Field, id string) string {
	for _, f := range fields {
		if f.ID == id {
			return f.Value
		}
	}
	return ""
}

// ----- download size -----

// sizeText is the note under the image field.
func sizeText(sz engine.ImageSize) string {
	switch {
	case sz.Local:
		return "already local (" + metrics.Bytes(sz.Bytes) + " on disk): no download"
	case sz.Bytes > 0:
		return "download of about " + metrics.Bytes(sz.Bytes)
	}
	return "size unknown: the source did not say"
}

// localNote tells whether the image is already local, from the daemon's own
// list (no network), so the form can say "no download" before asking.
func (m Model) localNote(e engine.ID, image string, vm bool) tea.Cmd {
	src := m.opts.Images
	if src == nil || image == "" {
		return nil
	}
	ctx, kind := m.opts.Context, formCreatePrefix+string(e)
	return func() tea.Msg {
		imgs, err := src.Images(ctx, e)
		if err != nil {
			return nil
		}
		bare := image
		if _, alias, ok := strings.Cut(image, ":"); ok && e == engine.Incus {
			bare = alias
		}
		for _, img := range imgs {
			if e == engine.Incus && img.VM != vm {
				continue
			}
			if slices.Contains(img.Refs, image) || slices.Contains(img.Refs, image+":latest") || slices.Contains(img.Refs, bare) {
				return form.NoteMsg{Kind: kind, Field: "image", Text: sizeText(engine.ImageSize{Local: true, Bytes: img.Size})}
			}
		}
		return nil
	}
}

// autoSize asks an Incus image server how big the download is when the form
// opens (Docker waits for ctrl+d, so browsing and running stay offline).
func (m Model) autoSize(e engine.ID, image string, vm bool) tea.Cmd {
	if e != engine.Incus {
		return nil
	}
	return m.sizeCmd(e, image, vm)
}

func (m Model) sizeCmd(e engine.ID, image string, vm bool) tea.Cmd {
	src := m.opts.Images
	if src == nil || image == "" {
		return nil
	}
	parent, kind := m.opts.Context, formCreatePrefix+string(e)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		sz, err := src.ImageSize(ctx, e, image, vm)
		if err != nil {
			return form.NoteMsg{Kind: kind, Field: "image", Text: "size: " + err.Error(), Err: true}
		}
		return form.NoteMsg{Kind: kind, Field: "image", Text: sizeText(sz)}
	}
}

// imageSize answers a form's ctrl+d.
func (m Model) imageSize(msg form.SizeMsg) tea.Cmd {
	e, ok := strings.CutPrefix(msg.Kind, formCreatePrefix)
	if !ok || msg.Field != "image" {
		return nil
	}
	vm := msg.Form != nil && msg.Form.Value("kind") == "vm"
	return m.sizeCmd(engine.ID(e), msg.Value, vm)
}

// ----- Docker Hub search -----

const (
	formLibrarySearch   = "library-search"
	pickerLibrarySearch = "library-search-results"
	pickerUnused        = "library-unused"
)

// librarySearchedMsg carries the results of a Docker Hub search.
type librarySearchedMsg struct {
	query   string
	results []engine.RemoteImage
	err     error
}

func (m Model) librarySearch() (tea.Model, tea.Cmd) {
	ids, cmd := m.startFlow("create")
	if len(ids) == 0 {
		return m, cmd
	}
	if !slices.Contains(ids, engine.Docker) {
		return m, toast.Show(theme.LevelWarn, "Searching Docker Hub needs Docker, which is not available right now")
	}
	f := form.New(m.keys, formLibrarySearch, "Search Docker Hub", "asks the registry through your Docker daemon · the only network call of the Library", "Search", []form.Field{
		{ID: "query", Label: "Search", Required: true, Placeholder: "grafana, redis, immich…"},
	})
	return m, modal.Open(f)
}

func (m Model) submitLibrarySearch(q string) (tea.Model, tea.Cmd) {
	src := m.opts.Images
	if src == nil {
		return m, nil
	}
	parent := m.opts.Context
	return m, tea.Batch(toast.Show(theme.LevelInfo, "Searching Docker Hub for "+textsafe.Clean(q)+"…"), func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		res, err := src.SearchImages(ctx, engine.Docker, q)
		return librarySearchedMsg{query: q, results: res, err: err}
	})
}

func (m Model) searched(msg librarySearchedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		return m, toast.Show(theme.LevelWarn, "Search failed: "+textsafe.Clean(msg.err.Error()))
	case len(msg.results) == 0:
		return m, toast.Show(theme.LevelInfo, "Nothing found for "+textsafe.Clean(msg.query))
	}
	items := make([]picker.Item, 0, len(msg.results))
	for _, r := range msg.results {
		var tags []string
		if r.Official {
			tags = append(tags, "official")
		}
		if r.Stars > 0 {
			tags = append(tags, fmt.Sprintf("★ %d", r.Stars))
		}
		detail := strings.Join(tags, " · ")
		if r.Description != "" {
			detail += "  " + r.Description
		}
		items = append(items, picker.Item{ID: r.Ref, Label: textsafe.Clean(r.Ref), Detail: textsafe.Clean(strings.TrimSpace(detail))})
	}
	return m, modal.Open(picker.New(m.keys, pickerLibrarySearch, fmt.Sprintf("%d images for %q", len(items), textsafe.Clean(msg.query)), items, ""))
}

// ----- unused images -----

// unusedImage is a local image no instance uses.
type unusedImage struct {
	engine engine.ID
	id     string
	label  string
	size   uint64
}

// unusedImagesMsg lists them.
type unusedImagesMsg struct {
	images []unusedImage
	err    error
}

// removeImageMsg is the confirmed removal of one image.
type removeImageMsg struct{ unusedImage }

// imageRemovedMsg reports it.
type imageRemovedMsg struct {
	img unusedImage
	err error
}

// usedImages is what the instances in the snapshot run from.
func usedImages(snap *store.Snapshot) (docker map[string]bool, incus []string) {
	docker = map[string]bool{}
	for _, in := range snap.Instances {
		switch x := in.Extra.(type) {
		case *engine.DockerExtra:
			docker[x.ImageID] = true
		case *engine.IncusExtra:
			if x.BaseImage != "" {
				incus = append(incus, x.BaseImage)
			}
		}
	}
	return docker, incus
}

func (m Model) unusedImages() (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly {
		return m, toast.Show(theme.LevelWarn, "Read-only mode: removing images is disabled")
	}
	src := m.opts.Images
	if src == nil {
		return m, toast.Show(theme.LevelWarn, "Listing images needs a connected engine")
	}
	snap := m.store.Snapshot()
	dockerUsed, incusUsed := usedImages(snap)
	ctx := m.opts.Context
	var ids []engine.ID
	for _, e := range engine.IDs {
		if snap.Engine(e).Health.State == store.StateConnected {
			ids = append(ids, e)
		}
	}
	return m, func() tea.Msg {
		var out []unusedImage
		for _, e := range ids {
			imgs, err := src.Images(ctx, e)
			if err != nil {
				return unusedImagesMsg{err: fmt.Errorf("%s images: %w", e.Label(), err)}
			}
			for _, img := range imgs {
				used := dockerUsed[img.ID]
				if e == engine.Incus {
					used = slices.ContainsFunc(incusUsed, func(fp string) bool { return strings.HasPrefix(fp, img.ID) })
				}
				if used {
					continue
				}
				label := img.ID[:min(14, len(img.ID))]
				if len(img.Refs) > 0 {
					label = img.Refs[0]
				}
				out = append(out, unusedImage{engine: e, id: img.ID, label: label, size: img.Size})
			}
		}
		slices.SortFunc(out, func(a, b unusedImage) int { return cmp.Compare(b.size, a.size) })
		return unusedImagesMsg{images: out}
	}
}

func (m Model) unusedListed(msg unusedImagesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, toast.Show(theme.LevelWarn, textsafe.Clean(msg.err.Error()))
	}
	if len(msg.images) == 0 {
		return m, toast.Show(theme.LevelInfo, "No unused images: every local image is used by an instance")
	}
	var total uint64
	items := make([]picker.Item, 0, len(msg.images))
	for _, u := range msg.images {
		total += u.size
		items = append(items, picker.Item{ID: string(u.engine) + "|" + u.id, Label: textsafe.Clean(u.label), Detail: u.engine.Label() + " · " + metrics.Bytes(u.size)})
	}
	title := fmt.Sprintf("Unused images · %d · %s can be reclaimed", len(items), metrics.Bytes(total))
	return m, modal.Open(picker.New(m.keys, pickerUnused, title, items, ""))
}

// confirmRemoveImage asks before removing the image the picker chose.
func (m Model) confirmRemoveImage(choice string) tea.Cmd {
	e, id, ok := strings.Cut(choice, "|")
	if !ok || m.opts.ReadOnly {
		return nil
	}
	imgs := m.unusedCache
	i := slices.IndexFunc(imgs, func(u unusedImage) bool { return string(u.engine) == e && u.id == id })
	if i < 0 {
		return nil
	}
	u := imgs[i]
	body := fmt.Sprintf("%s image %s (%s)\\n\\nNo instance uses it. It is downloaded again the next time something needs it.", u.engine.Label(), textsafe.Clean(u.label), metrics.Bytes(u.size))
	return modal.Open(modal.NewConfirm(m.keys, "Remove image", strings.ReplaceAll(body, "\\n", "\n"), "Remove", true, removeImageMsg{u}))
}

func (m Model) removeImage(u unusedImage) tea.Cmd {
	src, parent := m.opts.Images, m.opts.Context
	if src == nil || m.opts.ReadOnly {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		return imageRemovedMsg{img: u, err: src.RemoveImage(ctx, u.engine, u.id)}
	}
}

func (m Model) imageRemoved(msg imageRemovedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m, toast.Show(theme.LevelWarn, "Could not remove "+textsafe.Clean(msg.img.label)+": "+textsafe.Clean(msg.err.Error()))
	}
	return m, tea.Batch(toast.Show(theme.LevelOK, "Removed "+textsafe.Clean(msg.img.label)+" · "+metrics.Bytes(msg.img.size)+" freed"), m.loadLocalImages())
}
