package app

import (
	"time"

	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/tabs"
	"terminal-pdf-reader/internal/terminal"
)

type tabHit struct {
	id         tabs.TabID
	start, end int
}

type pageDrag struct {
	tab                  tabs.TabID
	x, y                 int
	scrollX, scrollY     float64
	overflowX, overflowY int
}

type rowClick struct {
	picker *picker
	index  int
	at     time.Time
}

func (m *model) mouse(e terminal.MouseEvent) {
	if e.Kind == terminal.MouseRelease {
		m.drag = nil
		return
	}
	if e.Shift && e.Kind != terminal.MouseWheelUp && e.Kind != terminal.MouseWheelDown {
		return
	}
	if m.passwordFor != nil {
		return
	}
	if e.Kind >= terminal.MouseWheelUp {
		down := e.Kind == terminal.MouseWheelDown || e.Kind == terminal.MouseWheelRight
		if m.help {
			if down {
				m.helpOffset++
			} else {
				m.helpOffset = max(0, m.helpOffset-1)
			}
			return
		}
		if m.picker != nil || m.outline != nil || m.tabList != nil {
			key := keys.Key{Special: keys.Up}
			if down {
				key.Special = keys.Down
			}
			m.key(key)
			return
		}
		action := keys.ScrollUp
		if down {
			action = keys.ScrollDown
		}
		if e.Ctrl {
			action = keys.ZoomIn
			if down {
				action = keys.ZoomOut
			}
		} else if e.Shift || e.Kind == terminal.MouseWheelLeft || e.Kind == terminal.MouseWheelRight {
			action = keys.ScrollLeft
			if down {
				action = keys.ScrollRight
			}
		}
		m.command(keys.Command{Action: action})
		return
	}
	if e.Kind == terminal.MousePress && e.Row == 0 {
		for _, hit := range m.tabHits {
			if e.Column < hit.start || e.Column >= hit.end {
				continue
			}
			switch e.Button {
			case terminal.ButtonLeft:
				m.tabs.Focus(hit.id)
				m.picker, m.outline, m.tabList, m.help = nil, nil, nil, false
			case terminal.ButtonMiddle:
				m.closeTab(hit.id)
				if m.tabList != nil {
					m.filterTabs(0)
				}
			}
			return
		}
	}
	if e.Button != terminal.ButtonLeft {
		return
	}
	if m.help {
		return
	}
	if m.tabList != nil || m.outline != nil || m.picker != nil {
		if e.Kind != terminal.MousePress {
			return
		}
		x := 0
		if m.size.Columns >= 40 {
			x = 2
		}
		height := max(0, m.size.Rows-5)
		if e.Column <= x || e.Column >= m.size.Columns-x-1 || e.Row < 2 || e.Row >= height+2 {
			return
		}
		switch {
		case m.tabList != nil:
			p := m.tabList
			index := max(0, p.selected-height+1) + e.Row - 2
			if index < len(p.filtered) {
				p.selected = index
				p.filtering = false
				m.tabListKey(keys.Key{Special: keys.Enter})
			}
		case m.outline != nil:
			p := m.outline
			index := max(0, p.selected-height+1) + e.Row - 2
			if index < len(p.filtered) {
				p.selected = index
				p.filtering = false
				m.outlineKey(keys.Key{Special: keys.Enter})
			}
		case m.picker != nil:
			p := m.picker
			index := max(0, p.selected-height+1) + e.Row - 2
			if index >= len(p.filtered) {
				return
			}
			p.selected = index
			now := time.Now()
			if c := m.lastClick; c != nil && c.picker == p && c.index == index && now.Sub(c.at) <= 400*time.Millisecond {
				p.filtering = false
				m.pickerKey(keys.Key{Special: keys.Enter})
				m.lastClick = nil
			} else {
				m.lastClick = &rowClick{picker: p, index: index, at: now}
			}
		}
		return
	}
	if e.Kind == terminal.MousePress && e.Column < m.sidebarWidth() {
		m.sidebarClick(e.Row)
		return
	}
	if e.Kind == terminal.MousePress {
		t, ok := m.tabs.Active()
		if !ok {
			return
		}
		l := m.activeLayout()
		if e.Column < l.X || e.Column >= l.X+l.Columns || e.Row < l.Y || e.Row >= l.Y+l.Rows {
			return
		}
		m.drag = &pageDrag{tab: t.ID, x: e.Column, y: e.Row, scrollX: t.View.ScrollX, scrollY: t.View.ScrollY,
			overflowX: max(0, l.FullWidth-l.VisibleWidth), overflowY: max(0, l.FullHeight-l.VisibleHeight)}
	} else if e.Kind == terminal.MouseDrag && m.drag != nil {
		d := m.drag
		t, ok := m.tabs.Active()
		if !ok || t.ID != d.tab {
			m.drag = nil
			return
		}
		x, y := t.View.ScrollX, t.View.ScrollY
		if d.overflowX > 0 {
			x = d.scrollX - float64((e.Column-d.x)*max(1, m.size.CellWidthPixels))/float64(d.overflowX)
		}
		if d.overflowY > 0 {
			y = d.scrollY - float64((e.Row-d.y)*max(1, m.size.CellHeightPixels))/float64(d.overflowY)
		}
		m.tabs.Scroll(d.tab, x-t.View.ScrollX, y-t.View.ScrollY)
	}
}
