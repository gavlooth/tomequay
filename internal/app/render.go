package app

import (
	"context"

	"terminal-pdf-reader/internal/cache"
	"terminal-pdf-reader/internal/pdf"
)

func (m *model) activeLayout() pageLayout {
	t, ok := m.tabs.Active()
	if !ok {
		return pageLayout{}
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		return pageLayout{}
	}
	l := layoutPage(m.size, d.info, t.View, m.state(t.ID).widthFit)
	l.Request.Path, l.Request.Password = t.Document.Path, d.password
	return l
}
func renderKey(r pdf.RenderRequest, mtime int64) cache.Key {
	return cache.Key{Path: r.Path, ModTimeUnixNano: mtime, Page: r.Page, WidthPixels: r.WidthPixels, HeightPixels: r.HeightPixels,
		ZoomMilli: int(r.Zoom*1000 + 0.5), CropX: r.CropX, CropY: r.CropY, CropWidth: r.CropWidth, CropHeight: r.CropHeight}
}

type renderJob struct {
	layout     pageLayout
	foreground bool
}

func (m *model) prepareRender() {
	t, ok := m.tabs.Active()
	if !ok || m.picker != nil || m.outline != nil || m.help || m.passwordFor != nil || m.size.Columns < 20 || m.size.Rows < 8 {
		if m.requested {
			m.invalidate()
		}
		return
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		if m.requested {
			m.invalidate()
		}
		return
	}
	l := m.activeLayout()
	if l.Columns < 1 || l.Rows < 1 {
		return
	}
	key := renderKey(l.Request, d.mtime)
	if m.requested && m.requestedTab == t.ID && m.requestedKey == key {
		// A wider pane can change centering without changing the render box.
		m.layout = l
		return
	}
	m.invalidate()
	m.requested, m.requestedTab, m.requestedKey, m.layout = true, t.ID, key, l
	if record := m.imageIDs[key]; record != nil {
		record.failed = false
	}
	foreground := true
	if data, ok := m.cache.Get(key); ok {
		m.currentPNG, m.currentKey = data, key
		foreground = false
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.renderCancel = cancel
	generation, id, mtime := m.generation, t.ID, d.mtime
	requests := make([]renderJob, 0, 3)
	if foreground {
		requests = append(requests, renderJob{l, true})
	}
	// Neighbor requests use precisely the same size/zoom/crop as the visible page.
	for _, page := range []int{t.View.Page + 1, t.View.Page - 1} {
		if page < 1 || page > d.info.PageCount {
			continue
		}
		next := l
		next.Request.Page = page
		if !m.cache.Contains(renderKey(next.Request, mtime)) {
			requests = append(requests, renderJob{next, false})
		}
	}
	if len(requests) == 0 {
		return
	}
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		for _, request := range requests {
			if ctx.Err() != nil {
				return
			}
			data, err := m.tools.RenderPage(ctx, request.layout.Request)
			r := renderResult{tab: id, generation: generation, key: renderKey(request.layout.Request, mtime), layout: request.layout, png: data, err: err, foreground: request.foreground}
			select {
			case m.results <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *model) acceptRender(r renderResult) bool {
	// The full key guards resize/zoom/crop/mtime; generation also rejects an old
	// worker that happens to revisit the same view after a tab switch.
	if r.generation != m.generation || !m.requested || r.tab != m.requestedTab {
		return false
	}
	if r.foreground && r.key != m.requestedKey {
		return false
	}
	if r.err != nil {
		if r.foreground {
			m.message = r.err.Error()
			m.currentPNG = nil
		}
		return true
	}
	m.cache.Put(r.key, r.png)
	m.sweepImages()
	cached := m.cache.Contains(r.key)
	if cached {
		m.image(r.key, true)
	}
	if r.foreground {
		m.currentPNG, m.currentKey, m.layout = r.png, r.key, r.layout
	}
	return true
}

func (m *model) drawPage() {
	if len(m.currentPNG) == 0 {
		return
	}
	cached := m.cache.Contains(m.currentKey)
	r := m.image(m.currentKey, cached)
	l := m.layout
	if !r.sent {
		return
	}
	if r.columns != l.Columns || r.rows != l.Rows {
		if err := m.images.Place(r.id, l.Columns, l.Rows); err != nil {
			m.noteOutput(err)
			return
		}
		r.columns, r.rows = l.Columns, l.Rows
	}
	m.noteOutput(m.images.WriteCells(r.id, l.X, l.Y, l.Columns, l.Rows))
}

func (m *model) stopWorkers() {
	if m.renderCancel != nil {
		m.renderCancel()
	}
	for _, d := range m.docs {
		if d.outlineCancel != nil {
			d.outlineCancel()
		}
	}
	for _, s := range m.states {
		if s.searchCancel != nil {
			s.searchCancel()
		}
	}
	m.workers.Wait()
}
