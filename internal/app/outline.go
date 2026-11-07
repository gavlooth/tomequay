package app

import (
	"context"
	"strings"

	"tomequay/internal/keys"
	"tomequay/internal/pdf"
	"tomequay/internal/tabs"
)

type outlineView struct {
	path      string
	tab       tabs.TabID
	entries   []pdf.OutlineEntry
	filtered  []int
	filter    string
	filtering bool
	selected  int
	page      int
	loading   bool
}

type outlineResult struct {
	path       string
	document   *document
	mtime      int64
	generation uint64
	entries    []pdf.OutlineEntry
	err        error
}

func (p *outlineView) refilter() {
	p.filtered = p.filtered[:0]
	query := strings.ToLower(p.filter)
	for i, e := range p.entries {
		if strings.Contains(strings.ToLower(e.Title), query) {
			p.filtered = append(p.filtered, i)
		}
	}
	p.selected = outlinePreselect(p.entries, p.filtered, p.page)
}

// Prefer the nearest preceding section, with the deepest item at that page.
// Before the first destination, select the first usable item instead.
func outlinePreselect(entries []pdf.OutlineEntry, filtered []int, page int) int {
	selected, first := -1, -1
	for i, index := range filtered {
		e := entries[index]
		if e.Page == 0 {
			continue
		}
		if first == -1 {
			first = i
		}
		if e.Page > page {
			continue
		}
		if selected == -1 {
			selected = i
			continue
		}
		previous := entries[filtered[selected]]
		if e.Page > previous.Page || e.Page == previous.Page && e.Depth >= previous.Depth {
			selected = i
		}
	}
	if selected == -1 {
		return first
	}
	return selected
}

func (p *outlineView) move(direction int) {
	start := p.selected
	if start < 0 {
		if direction < 0 {
			start = len(p.filtered)
		} else {
			start = -1
		}
	}
	for i := start + direction; i >= 0 && i < len(p.filtered); i += direction {
		if p.entries[p.filtered[i]].Page != 0 {
			p.selected = i
			return
		}
	}
}

func (m *model) showOutline() {
	t, ok := m.tabs.Active()
	if !ok {
		return
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		return
	}
	p := &outlineView{path: t.Document.Path, tab: t.ID, page: t.View.Page, selected: -1}
	m.outline, m.message = p, ""
	if d.outlineLoaded {
		if d.outlineError != "" {
			m.outline = nil
			m.message = d.outlineError
		} else {
			m.fillOutline(p, d.outline)
		}
		return
	}
	p.loading = true
	m.requestOutline(t.Document.Path, d)
}

func (m *model) requestOutline(path string, d *document) {
	if d.outlineLoaded || d.outlineCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	d.outlineCancel = cancel
	d.outlineGeneration++
	password, mtime, generation := d.password, d.mtime, d.outlineGeneration
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		entries, err := m.tools.Outline(ctx, path, password)
		select {
		case m.outlineResults <- outlineResult{path, d, mtime, generation, entries, err}:
		case <-ctx.Done():
		}
	}()
}

func (m *model) fillOutline(p *outlineView, entries []pdf.OutlineEntry) {
	if len(entries) == 0 {
		m.outline = nil
		m.message = "document has no outline"
		return
	}
	p.entries, p.loading = entries, false
	p.refilter()
}

func (m *model) acceptOutline(r outlineResult) {
	d := m.docs[r.path]
	if d == nil || d != r.document || !d.ready || d.mtime != r.mtime || d.outlineGeneration != r.generation {
		return
	}
	if d.outlineCancel != nil {
		d.outlineCancel()
		d.outlineCancel = nil
	}
	d.outline, d.outlineLoaded, d.outlineError = r.entries, true, ""
	if r.err != nil {
		d.outlineError = r.err.Error()
		m.message = d.outlineError
	}
	p := m.outline
	if p == nil || p.path != r.path {
		return
	}
	if r.err != nil {
		m.outline = nil
		m.message = r.err.Error()
		return
	}
	m.fillOutline(p, r.entries)
}

func (m *model) dropOutline(path string) {
	if d := m.docs[path]; d != nil {
		if d.outlineCancel != nil {
			d.outlineCancel()
			d.outlineCancel = nil
		}
		d.outline, d.outlineLoaded = nil, false
		d.outlineError = ""
		d.outlineGeneration++
	}
	if m.outline != nil && m.outline.path == path {
		m.outline = nil
	}
	if m.sidebarView != nil && m.sidebarView.path == path {
		m.sidebarView = nil
	}
}

func (m *model) outlineKey(k keys.Key) {
	p := m.outline
	if k.Special == keys.Esc {
		if p.filtering {
			p.filtering = false
		} else {
			m.outline = nil
		}
		return
	}
	if p.filtering {
		switch k.Special {
		case keys.Enter:
			p.filtering = false
		case keys.Backspace:
			r := []rune(p.filter)
			if len(r) > 0 {
				p.filter = string(r[:len(r)-1])
			}
			p.refilter()
		default:
			if k.Rune >= 32 && !k.Ctrl {
				p.filter += string(k.Rune)
				p.refilter()
			}
		}
		return
	}
	switch {
	case k.Rune == 'q' || k.Rune == 'Q':
		m.outline = nil
	case k.Rune == '/':
		p.filtering = true
	case k.Rune == 'j' || k.Special == keys.Down:
		p.move(1)
	case k.Rune == 'k' || k.Special == keys.Up:
		p.move(-1)
	case k.Special == keys.Enter:
		if p.selected < 0 || p.selected >= len(p.filtered) {
			return
		}
		e := p.entries[p.filtered[p.selected]]
		if e.Page == 0 {
			return
		}
		t, ok := m.tabs.Active()
		if !ok || t.ID != p.tab || t.Document.Path != p.path {
			return
		}
		old, _ := m.snapshot()
		m.tabs.SetPage(t.ID, e.Page, 0)
		if now, ok := m.snapshot(); ok && now != old {
			m.rememberJump(old)
		}
		m.outline = nil
		m.message = e.Title
	}
}
