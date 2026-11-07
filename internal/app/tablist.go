package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"tomequay/internal/keys"
	"tomequay/internal/tabs"
)

type tabListView struct {
	filtered  []tabs.Tab
	filter    string
	filtering bool
	selected  int
}

func (m *model) showTabList(filter string) {
	m.tabList = &tabListView{filter: filter}
	active, _ := m.tabs.Active()
	var id tabs.TabID
	if active != nil {
		id = active.ID
	}
	m.filterTabs(id)
	m.message = ""
}

func (m *model) filterTabs(prefer tabs.TabID) {
	p := m.tabList
	p.filtered = p.filtered[:0]
	query := strings.ToLower(p.filter)
	for _, tab := range m.tabs.Tabs() {
		if strings.Contains(strings.ToLower(filepath.Base(tab.Document.Path)), query) {
			p.filtered = append(p.filtered, tab)
		}
	}
	p.selected = max(0, min(p.selected, len(p.filtered)-1))
	for i, tab := range p.filtered {
		if tab.ID == prefer {
			p.selected = i
			break
		}
	}
}

func (m *model) selectTabByName(name string) {
	var matches []tabs.TabID
	for _, tab := range m.tabs.Tabs() {
		if strings.Contains(strings.ToLower(filepath.Base(tab.Document.Path)), strings.ToLower(name)) {
			matches = append(matches, tab.ID)
		}
	}
	switch len(matches) {
	case 0:
		m.message = "no tab matches " + name
	case 1:
		m.tabs.Focus(matches[0])
	default:
		m.showTabList(name)
	}
}

func (m *model) tabListKey(k keys.Key) {
	p := m.tabList
	if k.Special == keys.Esc {
		m.tabList = nil
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
			m.filterTabs(0)
		default:
			if k.Rune >= 32 && !k.Ctrl {
				p.filter += string(k.Rune)
				p.selected = 0
				m.filterTabs(0)
			}
		}
		return
	}
	switch {
	case k.Rune >= '1' && k.Rune <= '9':
		all := m.tabs.Tabs()
		i := int(k.Rune - '1')
		if i < len(all) {
			m.tabs.Focus(all[i].ID)
			m.tabList = nil
		}
	case k.Rune == '/':
		p.filtering = true
	case k.Rune == 'j' || k.Special == keys.Down:
		p.selected = max(0, min(len(p.filtered)-1, p.selected+1))
	case k.Rune == 'k' || k.Special == keys.Up:
		p.selected = max(0, p.selected-1)
	case k.Special == keys.Enter:
		if len(p.filtered) > 0 {
			m.tabs.Focus(p.filtered[p.selected].ID)
			m.tabList = nil
		}
	case k.Rune == 'x':
		if len(p.filtered) > 0 {
			m.closeTab(p.filtered[p.selected].ID)
			m.filterTabs(0)
		}
	case k.Rune == 'd':
		if len(p.filtered) > 0 {
			id := m.duplicateTab(p.filtered[p.selected].ID)
			m.filterTabs(id)
		}
	}
}

func (m *model) duplicateTab(id tabs.TabID) tabs.TabID {
	copyID := m.tabs.Duplicate(id)
	if copyID != 0 {
		s, copyState := m.state(id), m.state(copyID)
		copyState.widthFit, copyState.dark = s.widthFit, s.dark
	}
	m.openedHistory()
	return copyID
}

func (m *model) closeTab(id tabs.TabID) {
	m.recordHistory()
	m.saveHistory(true)
	var path string
	for _, tab := range m.tabs.Tabs() {
		if tab.ID == id {
			path = tab.Document.Path
			break
		}
	}
	if path == "" {
		return
	}
	if s := m.states[id]; s != nil && s.searchCancel != nil {
		s.searchCancel()
	}
	m.tabs.Close(id)
	delete(m.states, id)
	// A duplicate continues using the same document and rendered pages.
	for _, other := range m.tabs.Tabs() {
		if other.Document.Path == path {
			return
		}
	}
	delete(m.historyLive, path)
	m.dropDocument(path)
	delete(m.docs, path)
	if _, ok := m.tabs.Active(); !ok {
		m.quit = true
	}
}

func tabFit(tab tabs.Tab) string {
	switch tab.View.Fit {
	case tabs.FitWidth:
		return "width"
	case tabs.Custom:
		return fmt.Sprintf("%.0f%%", tab.View.Zoom*100)
	default:
		return "fit"
	}
}

func (m *model) drawTabList() {
	p := m.tabList
	height := max(0, m.size.Rows-5)
	start := max(0, p.selected-height+1)
	title := "OPEN TABS"
	if p.filter != "" {
		title += " /" + p.filter
	}
	box := m.panel(title, "j/k select  Enter/1-9 jump  x close  d duplicate  / filter  Esc close", start, height, len(p.filtered))
	all := m.tabs.Tabs()
	active, _ := m.tabs.Active()
	previous, _ := m.tabs.PreviousUsed()
	for i := range height {
		index := start + i
		if index >= len(p.filtered) {
			label := ""
			if i == 0 {
				label = "no matching tabs"
			}
			box.row(i, label, "", m.theme.muted())
			continue
		}
		tab := p.filtered[index]
		number := 0
		name := filepath.Base(tab.Document.Path)
		collision := false
		for j, other := range all {
			if other.ID == tab.ID {
				number = j + 1
			}
			if other.ID != tab.ID && filepath.Base(other.Document.Path) == name {
				collision = true
			}
		}
		mark := " "
		if active != nil && active.ID == tab.ID {
			mark = "*"
		} else if previous == tab.ID {
			mark = "#"
		}
		left := fmt.Sprintf("%s %d %s", mark, number, name)
		if collision {
			left += "  " + filepath.Dir(tab.Document.Path)
		}
		right := fmt.Sprintf("p. %d/%d  %s", tab.View.Page, tab.Document.PageCount, tabFit(tab))
		if m.state(tab.ID).dark {
			right += "  dark"
		}
		style := m.theme.plain()
		if index == p.selected {
			style = textStyle{fg: m.theme.Background, bg: m.theme.Blue, bold: true}
		}
		left = ellipsisText(cleanText(left), max(0, box.width-5-textWidth(right)), m.theme.NoGlyphs)
		box.row(i, left, right, style)
	}
}
