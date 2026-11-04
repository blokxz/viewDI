package app

import (
	"context"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/config"
	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/hub"
	"github.com/blokxz/viewDI/internal/textsafe"
	"github.com/blokxz/viewDI/internal/ui/components/hubbrowser"
	"github.com/blokxz/viewDI/internal/ui/components/modal"
	"github.com/blokxz/viewDI/internal/ui/components/toast"
	"github.com/blokxz/viewDI/internal/ui/theme"
)

// HubSource lists images on Docker Hub. Nil in Options means the network is
// off ([updates] enabled = false): the browser then searches through the
// Docker daemon and has no Most downloaded tab.
type HubSource interface {
	Search(ctx context.Context, query string, limit int) ([]hub.Repo, error)
	Top(ctx context.Context, limit int) ([]hub.Repo, error)
}

// hubResults is how many images a tab lists.
const hubResults = 30

// librarySearch opens the Docker Hub browser (S in the Library).
func (m Model) librarySearch() (tea.Model, tea.Cmd) {
	ids, cmd := m.startFlow("create")
	if len(ids) == 0 {
		return m, cmd
	}
	if !slices.Contains(ids, engine.Docker) {
		return m, toast.Show(theme.LevelWarn, "Browsing Docker Hub needs Docker, which is not available right now")
	}
	cfg := hubbrowser.Config{Favourites: m.hubFavourites()}
	if m.opts.Hub == nil {
		cfg.TopNote = "Docker Hub is off here: [updates] enabled = false stops every call to the network. Search still asks your Docker daemon."
		if m.opts.Images == nil {
			cfg.SearchNote = "Searching needs a connected Docker engine."
		}
	}
	return m, modal.Open(hubbrowser.New(m.keys, cfg))
}

// hubFavourites are the saved favourites that are valid image names.
func (m Model) hubFavourites() []string {
	var out []string
	for _, f := range m.state.Hub.Favourites {
		if hub.ValidRef(f) {
			out = append(out, f)
		}
	}
	return out
}

// hubSearch answers the browser's SearchMsg: the Hub when there is one,
// otherwise the Docker daemon's own registry search.
func (m Model) hubSearch(msg hubbrowser.SearchMsg) tea.Cmd {
	src, imgs, parent := m.opts.Hub, m.opts.Images, m.opts.Context
	if src == nil && imgs == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		res := hubbrowser.ResultsMsg{Tab: hubbrowser.TabSearch, Query: msg.Query}
		if src != nil {
			repos, err := src.Search(ctx, msg.Query, hubResults)
			res.Items, res.Err = hubItems(repos), errText(err)
			return res
		}
		found, err := imgs.SearchImages(ctx, engine.Docker, msg.Query)
		for _, r := range found {
			if hub.ValidRef(r.Ref) {
				res.Items = append(res.Items, hubbrowser.Item{
					Ref: r.Ref, Description: textsafe.Clean(r.Description), Stars: uint64(max(0, r.Stars)), Official: r.Official,
				})
			}
		}
		res.Err = errText(err)
		return res
	}
}

// hubTop answers the browser's LoadMsg for the Most downloaded tab.
func (m Model) hubTop() tea.Cmd {
	src, parent := m.opts.Hub, m.opts.Context
	if src == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, searchTimeout)
		defer cancel()
		repos, err := src.Top(ctx, hubResults)
		return hubbrowser.ResultsMsg{Tab: hubbrowser.TabTop, Items: hubItems(repos), Err: errText(err)}
	}
}

func hubItems(repos []hub.Repo) []hubbrowser.Item {
	out := make([]hubbrowser.Item, 0, len(repos))
	for _, r := range repos {
		out = append(out, hubbrowser.Item{Ref: r.Ref, Description: r.Description, Stars: r.Stars, Pulls: r.Pulls, Official: r.Official})
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return textsafe.Clean(strings.TrimSpace(err.Error()))
}

// hubFavourited saves the favourites the browser reports.
func (m Model) hubFavourited(msg hubbrowser.FavouritesMsg) (tea.Model, tea.Cmd) {
	refs := make([]string, 0, len(msg.Refs))
	for _, r := range msg.Refs {
		if hub.ValidRef(r) && len(refs) < config.MaxHubFavourites {
			refs = append(refs, r)
		}
	}
	m.state.Hub.Favourites = refs
	return m, m.saveState()
}
