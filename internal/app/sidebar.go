package app

import (
	"fmt"
	"io"
	"strings"
)

func (m *model) sidebarWidth() int {
	if !m.sidebar || m.size.Columns < 72 || m.size.Rows < 8 {
		return 0
	}
	return min(30, m.size.Columns/3)
}

func (m *model) prepareSidebar() {
	if m.sidebarWidth() == 0 {
		return
	}
	t, ok := m.tabs.Active()
	if !ok {
		m.sidebarView = nil
		return
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		m.sidebarView = nil
		return
	}
	p := m.sidebarView
	if p == nil || p.tab != t.ID || p.path != t.Document.Path {
		p = &outlineView{path: t.Document.Path, tab: t.ID, page: t.View.Page, selected: -1, loading: true}
		m.sidebarView = p
	}
	if d.outlineLoaded && p.loading {
		p.entries, p.loading = d.outline, false
		p.refilter()
	}
	if p.page != t.View.Page {
		p.page = t.View.Page
		p.selected = outlinePreselect(p.entries, p.filtered, p.page)
	}
	m.requestOutline(t.Document.Path, d)
}

func (m *model) drawSidebar() {
	width := m.sidebarWidth()
	p := m.sidebarView
	if width == 0 || p == nil {
		return
	}
	height := max(0, m.size.Rows-3)
	start := max(0, p.selected-height+1)
	for row := 1; row < m.size.Rows-1; row++ {
		label, style := "", m.theme.plain()
		if row == 1 {
			label = "OUTLINE"
			style = textStyle{fg: m.theme.Cyan, bg: m.theme.Background, bold: true}
		} else if index := start + row - 2; index < len(p.filtered) {
			entry := p.entries[p.filtered[index]]
			page := ""
			if entry.Page > 0 {
				page = fmt.Sprintf(" %d", entry.Page)
			}
			indent := strings.Repeat(" ", min(max(0, entry.Depth)*2, width/3))
			label = indent + ellipsisText(cleanText(entry.Title), max(0, width-3-len(indent)-textWidth(page)), m.theme.NoGlyphs) + page
			if entry.Page == 0 {
				style = m.theme.muted()
				style.dim = true
			} else if index == p.selected {
				style = textStyle{fg: m.theme.Background, bg: m.theme.Blue, bold: true}
			}
		} else if row == 2 {
			label = "no outline"
			if p.loading {
				label = "loading outline"
			}
			if d := m.docs[p.path]; d != nil && d.outlineError != "" {
				label = "outline unavailable"
			}
			style = m.theme.muted()
			style.dim = true
		}
		r := m.row(row)
		r.add(" "+clipText(label, width-3), style)
		r.padTo(width-1, style)
		r.add(m.theme.glyph("│", "|"), m.theme.muted())
		// Do not erase to the right: those cells hold the page placeholders.
		r.out.WriteString("\x1b[0m")
		_, err := io.WriteString(m.screen.Writer(), r.out.String())
		m.noteOutput(err)
	}
}

func (m *model) sidebarClick(row int) {
	p := m.sidebarView
	if p == nil || row < 2 {
		return
	}
	height := max(0, m.size.Rows-3)
	index := max(0, p.selected-height+1) + row - 2
	if index >= len(p.filtered) {
		return
	}
	entry := p.entries[p.filtered[index]]
	if entry.Page == 0 {
		return
	}
	old, ok := m.snapshot()
	if !ok || old.Tab != p.tab {
		return
	}
	m.tabs.SetPage(p.tab, entry.Page, 0)
	if now, ok := m.snapshot(); ok && now != old {
		m.rememberJump(old)
	}
	m.message = entry.Title
}
