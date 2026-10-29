package app

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/blokxz/viewDI/internal/engine"
	"github.com/blokxz/viewDI/internal/store"
	"github.com/blokxz/viewDI/internal/streams"
	"github.com/blokxz/viewDI/internal/ui/views"
	"github.com/blokxz/viewDI/internal/ui/views/detail"
)

// Streamer starts an instance's live streams for the Detail view;
// *streams.Registry implements it.
type Streamer interface {
	Subscribe(key engine.Key, req streams.Request) *streams.Subscription
}

// detailSubscribedMsg hands the app the subscription of detail seq.
type detailSubscribedMsg struct {
	seq uint64
	sub *streams.Subscription
}

// streamBatchMsg carries one batch of subscription seq.
type streamBatchMsg struct {
	seq   uint64
	batch streams.Batch
}

// current returns the view that has the screen: the Detail view when one
// is open, else the active main view.
func (m Model) current() views.View {
	if m.detail != nil {
		return m.detail
	}
	return m.views[m.active]
}

// openDetail shows the Detail view of in and subscribes to its streams.
func (m Model) openDetail(in engine.Instance) (tea.Model, tea.Cmd) {
	closeCmd := m.closeDetail()
	tab, _ := detail.TabByName(m.state.DetailTabs[string(in.Engine)])
	d := detail.New(m.keys, in, detail.Options{Tab: tab, LogCapacity: m.opts.LogCapacity})
	d.Update(views.DataMsg{Snapshot: m.store.Snapshot()})
	d.Update(m.resizeMsg())
	m.detail = d
	m.detailFrom = m.views[m.active].Title()
	m.detailSeq++
	src := m.opts.Streams
	if src == nil {
		return m, closeCmd
	}
	seq, key := m.detailSeq, in.Key()
	subscribe := func() tea.Msg {
		return detailSubscribedMsg{seq: seq, sub: src.Subscribe(key, streams.Request{Logs: true, Stats: true, Inspect: true})}
	}
	return m, tea.Batch(closeCmd, subscribe)
}

// closeDetail closes the Detail view; the returned command cancels its
// subscription.
func (m *Model) closeDetail() tea.Cmd {
	if m.detail == nil {
		return nil
	}
	m.detail = nil
	m.detailSeq++
	sub := m.sub
	m.sub = nil
	if sub == nil {
		return nil
	}
	return func() tea.Msg { sub.Close(); return nil }
}

// follow waits for the next batch of the subscription.
func follow(ctx context.Context, seq uint64, sub *streams.Subscription) tea.Cmd {
	return func() tea.Msg {
		b, ok := sub.Next(ctx)
		if !ok {
			return nil
		}
		return streamBatchMsg{seq: seq, batch: b}
	}
}

func (m Model) detailSubscribed(msg detailSubscribedMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.detailSeq || m.detail == nil { // closed before the subscription started
		return m, func() tea.Msg { msg.sub.Close(); return nil }
	}
	m.sub = msg.sub
	return m, follow(m.opts.Context, msg.seq, msg.sub)
}

func (m Model) streamBatch(msg streamBatchMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.detailSeq || m.detail == nil || m.sub == nil {
		return m, nil
	}
	b := msg.batch
	out := detail.BatchMsg{Key: m.detail.Key(), Lines: b.Lines, Dropped: b.Dropped, Samples: b.Samples}
	if b.Inspect != nil {
		out.Inspect = &detail.Inspected{Instance: b.Inspect.Instance, Raw: b.Inspect.Raw, Err: b.Inspect.Err}
	}
	out.Logs, out.Stats = status(b.Logs), status(b.Stats)
	m.detail.Update(out)
	return m, follow(m.opts.Context, msg.seq, m.sub)
}

func status(st *streams.Status) *detail.StreamStatus {
	if st == nil {
		return nil
	}
	return &detail.StreamStatus{State: detail.StreamState(st.State), Err: st.Err, Retry: st.Retry}
}

// refreshDetail asks the subscription to re-inspect and reconnect when the
// instance changed or its engine came back.
func (m Model) refreshDetail(before, after *store.Snapshot) tea.Cmd {
	if m.detail == nil || m.sub == nil {
		return nil
	}
	k := m.detail.Key()
	a, okA := before.Lookup(k)
	b, okB := after.Lookup(k)
	changed := okA != okB || (okB && (a.State != b.State || a.Health != b.Health || !a.Started.Equal(b.Started)))
	id := k.Engine()
	if before.Engine(id).Health.State != store.StateConnected && after.Engine(id).Health.State == store.StateConnected {
		changed = true
	}
	if !changed {
		return nil
	}
	sub := m.sub
	return func() tea.Msg { sub.Refresh(); return nil }
}
