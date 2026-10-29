package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/jobs"
	"github.com/blokxz/viewDI/internal/metrics"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/ui/components/files"
	"github.com/blokxz/viewDI/internal/ui/components/form"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/picker"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// ImageSource lists and searches images for the create form; main backs
// it with the engines. Calls run inside commands.
type ImageSource interface {
	Images(ctx context.Context, e engine.ID) ([]engine.Image, error)
	SearchImages(ctx context.Context, e engine.ID, query string) ([]engine.RemoteImage, error)
}

// Modal kinds of the create and import flows.
const (
	pickerCreateEngine = "create-engine"
	pickerImportKind   = "import-kind"
	formCreatePrefix   = "create-" // + engine ID
	formImport         = "import"
	filesImport        = "import"
)

// importExts are the file names each import kind offers.
var importExts = map[engine.ImportKind][]string{
	engine.ImportDockerImage:  {".tar", ".tar.gz", ".tgz"},
	engine.ImportDockerRootfs: {".tar", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.zst", ".tar.bz2"},
	engine.ImportIncusBackup:  {".tar.gz", ".tar.xz", ".tar.zst", ".tar.bz2", ".tar", ".backup"},
	engine.ImportIncusImage:   {".tar.gz", ".tar.xz", ".tar.zst", ".tar", ".squashfs"},
}

// writable returns the engines that can take a create or import, and the
// reasons the others cannot.
func (m Model) writable() ([]engine.ID, []string) {
	snap := m.store.Snapshot()
	var ok []engine.ID
	var why []string
	for _, ev := range snap.Engines {
		caps := ev.Info.Capabilities
		switch {
		case ev.Health.State != store.StateConnected || !ev.InfoKnown:
			why = append(why, ev.ID.Label()+" is not connected")
		case caps.Restricted:
			why = append(why, ev.ID.Label()+": "+caps.Reason)
		default:
			ok = append(ok, ev.ID)
		}
	}
	return ok, why
}

// startFlow checks what create and import need, and returns the usable
// engines or the command that explains why there are none.
func (m Model) startFlow(what string) ([]engine.ID, tea.Cmd) {
	if m.opts.ReadOnly {
		return nil, toast.Show(theme.LevelWarn, "Read-only mode: creating and importing are disabled")
	}
	if m.opts.Jobs == nil {
		return nil, toast.Show(theme.LevelWarn, capital(what)+" needs a connected engine")
	}
	ids, why := m.writable()
	if len(ids) == 0 {
		text := "No engine can " + what + " right now"
		if len(why) > 0 {
			text += ": " + strings.Join(why, "; ")
		}
		return nil, toast.Show(theme.LevelWarn, text)
	}
	return ids, nil
}

// ----- create -----

func (m Model) newInstance() (tea.Model, tea.Cmd) {
	ids, cmd := m.startFlow("create")
	switch len(ids) {
	case 0:
		return m, cmd
	case 1:
		return m, m.openCreate(ids[0])
	}
	items := make([]picker.Item, 0, len(ids))
	for _, id := range ids {
		detail := "container"
		if id == engine.Incus {
			detail = "container or virtual machine"
		}
		items = append(items, picker.Item{ID: string(id), Label: id.Label(), Detail: detail})
	}
	return m, modal.Open(picker.New(m.keys, pickerCreateEngine, "Create on", items, ""))
}

// openCreate opens the create form of engine e and loads its images.
func (m Model) openCreate(e engine.ID) tea.Cmd {
	snap := m.store.Snapshot()
	ev := snap.Engine(e)
	var fields []form.Field
	title := "Create a Docker container"
	if e == engine.Incus {
		title = "Create an Incus instance"
		fields = incusFields(snap, ev)
	} else {
		fields = dockerFields(ev)
	}
	intro := fmt.Sprintf("on %s %s · the job's progress shows in Jobs (5)", e.Label(), ev.Info.Version)
	f := form.New(m.keys, formCreatePrefix+string(e), title, intro, "Create", fields).WithContext(e)
	return tea.Batch(modal.Open(f), m.loadImages(e))
}

func names[T any](items []T, name func(T) string, keep func(T) bool) []string {
	var out []string
	for _, it := range items {
		if n := name(it); n != "" && (keep == nil || keep(it)) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func validator[T any](parse func(string) (T, error)) func(string) error {
	return func(s string) error {
		_, err := parse(s)
		return err
	}
}

func dockerFields(ev store.EngineView) []form.Field {
	return []form.Field{
		{
			ID: "image", Label: "Image", Required: true, Searchable: true, Validate: engine.ValidateImage,
			Placeholder: "nginx:1.27", Hint: "ctrl+n/p local images · ctrl+f searches the registry",
		},
		{ID: "pull", Label: "Pull", Kind: form.Check, Hint: "pull the image first (needed if it is not local)"},
		{
			ID: "name", Label: "Name", Placeholder: "picked by Docker",
			Validate: func(s string) error { return engine.ValidateName(engine.Docker, s) },
		},
		{
			ID: "ports", Label: "Ports", Placeholder: "8080:80, 127.0.0.1:5432:5432, 53:53/udp",
			Hint: "host:container[/udp], comma-separated", Validate: validator(engine.ParsePorts),
		},
		{
			ID: "env", Label: "Environment", Placeholder: "KEY=value, OTHER=x", MaskSecrets: true,
			Hint: "comma-separated; values cannot contain commas or spaces", Validate: validator(engine.ParseEnv),
		},
		{
			ID: "volumes", Label: "Volumes", Placeholder: "data:/var/lib/data, /srv/conf:/etc/app:ro",
			Hint: "a source starting with / is a bind mount, otherwise a named volume", Validate: validator(engine.ParseVolumes),
		},
		{
			ID: "network", Label: "Network", Placeholder: "default bridge",
			Suggestions: names(ev.Networks, func(n engine.Network) string { return n.Name }, nil),
		},
		{ID: "restart", Label: "Restart", Kind: form.Select, Options: engine.RestartPolicies},
		{ID: "labels", Label: "Labels", Placeholder: "tier=web, owner=me", Validate: validator(engine.ParseLabels)},
		{ID: "start", Label: "Start", Kind: form.Check, On: true, Hint: "start the container once created"},
	}
}

func incusFields(snap *store.Snapshot, ev store.EngineView) []form.Field {
	var profiles []string
	for _, in := range snap.Instances {
		if x, ok := in.Extra.(*engine.IncusExtra); ok && in.Engine == engine.Incus {
			for _, p := range x.Profiles {
				if !slices.Contains(profiles, p) {
					profiles = append(profiles, p)
				}
			}
		}
	}
	slices.Sort(profiles)
	kinds, labels := []string{"container"}, []string{"container"}
	if ev.Info.Capabilities.VMs {
		kinds, labels = append(kinds, "vm"), append(labels, "virtual machine")
	}
	fields := []form.Field{
		{
			ID: "image", Label: "Image", Required: true, Searchable: true, Placeholder: "images:debian/12",
			Hint: "a local alias or images:<alias> · ctrl+n/p local · ctrl+f searches images:",
		},
		{
			ID: "name", Label: "Name", Required: true, Placeholder: "my-instance",
			Validate: func(s string) error { return engine.ValidateName(engine.Incus, s) },
		},
		{ID: "kind", Label: "Type", Kind: form.Select, Options: kinds, Labels: labels},
		{ID: "profiles", Label: "Profiles", Value: "default", Suggestions: profiles, Hint: "comma-separated, applied in order"},
		{
			ID: "cpu", Label: "CPU limit", Placeholder: "no limit", Hint: "number of CPUs",
			Validate: func(s string) error {
				if n, err := strconv.Atoi(s); err != nil || n < 1 || n > 4096 {
					return errors.New("a number of CPUs, 1 or more")
				}
				return nil
			},
		},
		{ID: "memory", Label: "Memory limit", Placeholder: "no limit", Hint: "e.g. 512MiB, 2GiB, 50%", Validate: engine.ValidateMemory},
		{
			ID: "network", Label: "Network", Placeholder: "from the profile",
			Suggestions: names(ev.Networks, func(n engine.Network) string { return n.Name }, func(n engine.Network) bool { return n.Managed }),
		},
		{
			ID: "pool", Label: "Storage pool", Placeholder: "from the profile",
			Suggestions: names(ev.Volumes, func(v engine.Volume) string { return v.Pool }, nil),
		},
	}
	if p := projectField(ev); p != nil {
		fields = append(fields, *p)
	}
	return append(fields, form.Field{ID: "start", Label: "Start", Kind: form.Check, On: true, Hint: "start the instance once created"})
}

// projectField is the Incus project select, when there is a choice.
func projectField(ev store.EngineView) *form.Field {
	projects := ev.Info.Projects
	if len(projects) < 2 {
		return nil
	}
	current := cmp.Or(ev.Project, "default")
	return &form.Field{ID: "project", Label: "Project", Kind: form.Select, Options: projects, Value: current}
}

// loadImages fills the image suggestions with the engine's local images.
func (m Model) loadImages(e engine.ID) tea.Cmd {
	src := m.opts.Images
	if src == nil {
		return nil
	}
	ctx := m.opts.Context
	kind := formCreatePrefix + string(e)
	return func() tea.Msg {
		imgs, err := src.Images(ctx, e)
		if err != nil {
			return form.SuggestionsMsg{Kind: kind, Field: "image", Err: fmt.Errorf("local images: %w", err)}
		}
		var refs []string
		for _, img := range imgs {
			refs = append(refs, img.Refs...)
		}
		slices.Sort(refs)
		return form.SuggestionsMsg{Kind: kind, Field: "image", Items: slices.Compact(refs), Note: fmt.Sprintf("%d local images", len(imgs))}
	}
}

// searchImages answers a form's ctrl+f.
func (m Model) searchImages(msg form.SearchMsg) tea.Cmd {
	src := m.opts.Images
	e, ok := strings.CutPrefix(msg.Kind, formCreatePrefix)
	if src == nil || !ok || msg.Field != "image" {
		return nil
	}
	if strings.TrimSpace(msg.Query) == "" {
		return func() tea.Msg {
			return form.SuggestionsMsg{Kind: msg.Kind, Field: msg.Field, Err: errors.New("type part of a name to search")}
		}
	}
	parent := m.opts.Context
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		res, err := src.SearchImages(ctx, engine.ID(e), msg.Query)
		if err != nil {
			return form.SuggestionsMsg{Kind: msg.Kind, Field: msg.Field, Err: fmt.Errorf("search: %w", err)}
		}
		refs := make([]string, 0, len(res))
		for _, r := range res {
			refs = append(refs, r.Ref)
		}
		note := fmt.Sprintf("%d found for %q · ctrl+n to browse", len(res), msg.Query)
		if len(res) == 0 {
			note = fmt.Sprintf("nothing found for %q", msg.Query)
		}
		return form.SuggestionsMsg{Kind: msg.Kind, Field: msg.Field, Items: refs, Remote: true, Note: note}
	}
}

// createSpec turns the create form's values into a spec. The form
// validated every field, so parse errors cannot happen here.
func createSpec(e engine.ID, v form.Values) engine.CreateSpec {
	spec := engine.CreateSpec{Name: v["name"], Image: v["image"], Start: v.Bool("start"), Network: v["network"]}
	if e == engine.Docker {
		spec.Pull = v.Bool("pull")
		spec.Ports, _ = engine.ParsePorts(v["ports"])
		spec.Env, _ = engine.ParseEnv(v["env"])
		spec.Volumes, _ = engine.ParseVolumes(v["volumes"])
		spec.Labels, _ = engine.ParseLabels(v["labels"])
		spec.RestartPolicy = v["restart"]
		return spec
	}
	if v["kind"] == "vm" {
		spec.Kind = engine.KindVM
	}
	spec.Profiles = engine.ParseList(v["profiles"])
	spec.CPU, _ = strconv.Atoi(v["cpu"])
	spec.Memory = v["memory"]
	spec.Pool = v["pool"]
	spec.Project = v["project"]
	return spec
}

func (m Model) submitCreate(e engine.ID, v form.Values) (tea.Model, tea.Cmd) {
	spec := createSpec(e, v)
	if spec.Project == "" && e == engine.Incus {
		spec.Project = m.store.Snapshot().Engine(e).Project
	}
	name := cmp.Or(spec.Name, spec.Image)
	model, cmd := m.submit("create", []jobs.Request{{Key: engine.MakeKey(e, ""), Name: name, Create: &spec}})
	return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Creating "+name+"… progress in Jobs (5)"))
}

// ----- import -----

func (m Model) importFile() (tea.Model, tea.Cmd) {
	ids, cmd := m.startFlow("import")
	if len(ids) == 0 {
		return m, cmd
	}
	var items []picker.Item
	for _, k := range []engine.ImportKind{engine.ImportDockerImage, engine.ImportDockerRootfs, engine.ImportIncusBackup, engine.ImportIncusImage} {
		if !slices.Contains(ids, k.Engine()) {
			continue
		}
		items = append(items, picker.Item{ID: strconv.Itoa(int(k)), Label: k.String(), Detail: importDetail[k]})
	}
	return m, modal.Open(picker.New(m.keys, pickerImportKind, "Import", items, ""))
}

var importDetail = map[engine.ImportKind]string{
	engine.ImportDockerImage:  "docker save output → docker load",
	engine.ImportDockerRootfs: "filesystem tarball → docker import",
	engine.ImportIncusBackup:  "incus export output → new instance",
	engine.ImportIncusImage:   "unified image tarball → local image",
}

// chooseImportFile opens the file picker for kind.
func (m Model) chooseImportFile(id string) tea.Cmd {
	n, err := strconv.Atoi(id)
	if err != nil || n < 0 || n > int(engine.ImportIncusImage) {
		return nil
	}
	kind := engine.ImportKind(n)
	dir := cmp.Or(m.importDir, m.opts.WorkDir, m.opts.Home, "/")
	fp, cmd := files.New(m.keys, filesImport, "Import "+kind.String()+": choose a file", dir, m.opts.Home, importExts[kind])
	return tea.Batch(modal.Open(fp.WithContext(kind)), cmd)
}

// importForm asks for what the import still needs.
func (m Model) importForm(msg files.ChosenMsg) (tea.Model, tea.Cmd) {
	kind, ok := msg.Context.(engine.ImportKind)
	if !ok {
		return m, nil
	}
	m.importDir = filepath.Dir(msg.Path)
	ev := m.store.Snapshot().Engine(kind.Engine())
	var fields []form.Field
	switch kind {
	case engine.ImportDockerRootfs:
		fields = append(fields, form.Field{
			ID: "name", Label: "Image name", Required: true, Placeholder: "my/rootfs:1",
			Hint: "the reference the new image gets", Validate: engine.ValidateImage,
		})
	case engine.ImportIncusBackup:
		fields = append(fields,
			form.Field{
				ID: "name", Label: "Name", Placeholder: "the backup's own",
				Validate: func(s string) error { return engine.ValidateName(engine.Incus, s) },
			},
			form.Field{
				ID: "pool", Label: "Storage pool", Placeholder: "default",
				Suggestions: names(ev.Volumes, func(v engine.Volume) string { return v.Pool }, nil),
			})
	case engine.ImportIncusImage:
		fields = append(fields, form.Field{ID: "name", Label: "Alias", Placeholder: "none (fingerprint only)", Hint: "e.g. custom/app"})
	}
	if kind.Engine() == engine.Incus {
		if p := projectField(ev); p != nil {
			fields = append(fields, *p)
		}
	}
	intro := fmt.Sprintf("%s\n%s · %s", msg.Path, kind, metrics.Bytes(uint64(max(msg.Size, 0))))
	spec := engine.ImportSpec{Kind: kind, Path: msg.Path}
	f := form.New(m.keys, formImport, "Import into "+kind.Engine().Label(), intro, "Import", fields).WithContext(spec)
	return m, modal.Open(f)
}

func (m Model) submitImport(spec engine.ImportSpec, v form.Values) (tea.Model, tea.Cmd) {
	spec.Name, spec.Pool, spec.Project = v["name"], v["pool"], v["project"]
	e := spec.Kind.Engine()
	if spec.Project == "" && e == engine.Incus {
		spec.Project = m.store.Snapshot().Engine(e).Project
	}
	name := filepath.Base(spec.Path)
	model, cmd := m.submit("import", []jobs.Request{{Key: engine.MakeKey(e, ""), Name: name, Import: &spec}})
	return model, tea.Batch(cmd, toast.Show(theme.LevelInfo, "Importing "+name+"… progress in Jobs (5)"))
}

// formSubmitted routes a submitted form of the create and import flows.
func (m Model) formSubmitted(msg form.SubmittedMsg) (tea.Model, tea.Cmd) {
	if m.opts.ReadOnly {
		return m, nil
	}
	switch ctx := msg.Context.(type) {
	case engine.ID:
		if msg.Kind == formCreatePrefix+string(ctx) {
			return m.submitCreate(ctx, msg.Values)
		}
	case engine.ImportSpec:
		if msg.Kind == formImport {
			return m.submitImport(ctx, msg.Values)
		}
	}
	return m, nil
}

// searchTimeout bounds a ctrl+f search (the adapters bound theirs too).
const searchTimeout = 30 * time.Second
