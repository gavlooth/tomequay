package app

import (
	"context"
	"fmt"
	"math"
	"strings"

	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/tabs"
)

func (m *model) key(k keys.Key) {
	if m.passwordFor != nil {
		switch k.Special {
		case keys.Esc:
			m.passwordFor = nil
			m.password = nil
			m.nextPassword()
		case keys.Backspace:
			if len(m.password) > 0 {
				m.password = m.password[:len(m.password)-1]
			}
		case keys.Enter:
			a, password := *m.passwordFor, string(m.password)
			m.passwordFor = nil
			m.password = nil
			m.open(a.path, a.page, password)
			if m.passwordFor == nil {
				m.nextPassword()
			}
		default:
			if k.Rune >= 32 && !k.Ctrl {
				m.password = append(m.password, k.Rune)
			}
		}
		return
	}
	if m.help {
		switch {
		case k.Rune == 'j' || k.Special == keys.Down:
			m.helpOffset++
		case k.Rune == 'k' || k.Special == keys.Up:
			m.helpOffset = max(0, m.helpOffset-1)
		default:
			m.help = false
		}
		return
	}
	if m.picker != nil {
		m.pickerKey(k)
		return
	}
	if m.outline != nil {
		m.outlineKey(k)
		return
	}
	cmd, ok := m.decoder.Feed(k)
	if ok {
		m.command(cmd)
	} else if k.Special == keys.Enter && m.decoder.Mode() == keys.CommandLine {
		if _, err := keys.ParseCommandLine(m.decoder.Pending()); err != nil {
			m.message = err.Error()
		}
	}
}
func (m *model) nextPassword() {
	if len(m.passwordQueue) > 0 {
		a := m.passwordQueue[0]
		m.passwordQueue = m.passwordQueue[1:]
		m.passwordFor = &a
	}
}

func (m *model) command(cmd keys.Command) {
	n := max(1, cmd.Count)
	switch cmd.Action {
	case keys.QuitAll:
		m.quit = true
		return
	case keys.OpenFile:
		if cmd.Text != "" {
			m.open(cmd.Text, 1, "")
		} else {
			m.showPicker()
		}
		return
	case keys.NewTab:
		m.showPicker()
		return
	case keys.HelpAction:
		m.help = true
		m.helpOffset = 0
		return
	case keys.Outline:
		m.showOutline()
		return
	}
	t, ok := m.tabs.Active()
	if !ok {
		if cmd.Action == keys.CloseTab {
			m.quit = true
		}
		return
	}
	s := m.state(t.ID)
	old := position{Tab: t.ID, View: t.View, widthFit: s.widthFit}
	jump := false
	switch cmd.Action {
	case keys.NextPage:
		m.tabs.NextPage(t.ID, n)
		m.tabs.SetPage(t.ID, t.View.Page, 0)
		jump = true
	case keys.PreviousPage:
		m.tabs.PreviousPage(t.ID, n)
		m.tabs.SetPage(t.ID, t.View.Page, 0)
		jump = true
	case keys.FirstPage:
		m.tabs.SetPage(t.ID, 1, 0)
		jump = true
	case keys.LastPage:
		m.tabs.SetPage(t.ID, t.Document.PageCount, 0)
		jump = true
	case keys.GoToPage:
		m.tabs.SetPage(t.ID, n, 0)
		jump = true
	case keys.ScrollToTop:
		m.tabs.SetPage(t.ID, t.View.Page, 0)
	case keys.ScrollToMiddle:
		m.tabs.SetPage(t.ID, t.View.Page, 0.5)
	case keys.ScrollToBottom:
		m.tabs.SetPage(t.ID, t.View.Page, 1)
	case keys.ScrollDown, keys.ScrollUp, keys.HalfPageDown, keys.HalfPageUp, keys.FullPageDown, keys.FullPageUp:
		l := m.activeLayout()
		step := float64(max(1, m.size.CellHeightPixels)) * 3
		if cmd.Action == keys.HalfPageDown || cmd.Action == keys.HalfPageUp {
			step = float64(l.VisibleHeight) / 2
		}
		if cmd.Action == keys.FullPageDown || cmd.Action == keys.FullPageUp {
			step = float64(l.VisibleHeight)
		}
		dir := 1
		if cmd.Action == keys.ScrollUp || cmd.Action == keys.HalfPageUp || cmd.Action == keys.FullPageUp {
			dir = -1
		}
		page, y := scrollVertical(t.View.Page, t.Document.PageCount, t.View.ScrollY, float64(max(0, l.FullHeight-l.VisibleHeight)), math.Max(1, step), n, dir)
		m.tabs.SetPage(t.ID, page, y)
	case keys.ScrollLeft, keys.ScrollRight:
		l := m.activeLayout()
		if overflow := l.FullWidth - l.VisibleWidth; overflow > 0 {
			dx := float64(n) * float64(max(1, m.size.CellWidthPixels)) * 3 / float64(overflow)
			if cmd.Action == keys.ScrollLeft {
				dx = -dx
			}
			m.tabs.Scroll(t.ID, dx, 0)
		}
	case keys.ZoomIn, keys.ZoomOut:
		if t.View.Fit != tabs.Custom {
			s.widthFit = t.View.Fit == tabs.FitWidth
		}
		factor := math.Pow(1.2, float64(min(n, 100)))
		if cmd.Action == keys.ZoomOut {
			factor = 1 / factor
		}
		m.tabs.ZoomBy(t.ID, factor)
	case keys.FitWidth:
		s.widthFit = true
		m.tabs.SetFit(t.ID, tabs.FitWidth)
	case keys.FitPage, keys.ResetZoom:
		s.widthFit = false
		m.tabs.SetFit(t.ID, tabs.FitPage)
	case keys.NextTab:
		for range n % len(m.tabs.Tabs()) {
			m.tabs.Next()
		}
	case keys.PreviousTab:
		for range n % len(m.tabs.Tabs()) {
			m.tabs.Previous()
		}
	case keys.GoToTab:
		tt := m.tabs.Tabs()
		if n <= len(tt) {
			m.tabs.Focus(tt[n-1].ID)
		} else {
			m.message = "no such tab"
		}
	case keys.DuplicateTab:
		id := m.tabs.Duplicate(t.ID)
		m.state(id).widthFit = s.widthFit
	case keys.CloseTab:
		path := t.Document.Path
		if s.searchCancel != nil {
			s.searchCancel()
		}
		m.tabs.Close(t.ID)
		delete(m.states, t.ID)
		// Closing one duplicate must not invalidate the other duplicate's cache.
		inUse := false
		for _, other := range m.tabs.Tabs() {
			if other.Document.Path == path {
				inUse = true
				break
			}
		}
		if !inUse {
			m.dropDocument(path)
			delete(m.docs, path)
		}
		if _, ok := m.tabs.Active(); !ok {
			m.quit = true
		}
	case keys.SetMark:
		s.marks[cmd.Arg] = old
		m.message = fmt.Sprintf("mark %c set", cmd.Arg)
	case keys.JumpToMark:
		if p, ok := s.marks[cmd.Arg]; ok {
			m.restore(p)
			jump = true
		} else {
			m.message = fmt.Sprintf("mark %c is not set", cmd.Arg)
		}
	case keys.JumpBack:
		if s.previous != nil {
			p := *s.previous
			m.restore(p)
			s.previous = &old
		}
	case keys.JumpOlder, keys.JumpNewer:
		if len(s.jumps) == 0 {
			break
		}
		if cmd.Action == keys.JumpOlder {
			if s.jumpIndex == len(s.jumps)-1 && s.jumps[s.jumpIndex] != old {
				s.jumps = append(s.jumps, old)
				s.jumpIndex++
			}
			s.jumpIndex = max(0, s.jumpIndex-n)
		} else {
			s.jumpIndex += min(n, len(s.jumps)-1-s.jumpIndex)
		}
		m.restore(s.jumps[s.jumpIndex])
	case keys.Reload:
		m.reload(t.Document.Path)
	case keys.Redraw:
		m.forceUpload()
	case keys.SearchForward, keys.SearchBackward:
		query := cmd.Text
		if query == "" {
			query = s.searchQuery
		}
		if query == "" {
			m.message = "enter a search query"
			break
		}
		m.startSearch(t, query, cmd.Action == keys.SearchBackward)
	case keys.SearchNext, keys.SearchPrevious:
		direction := 1
		if s.searchBackward {
			direction = -1
		}
		if cmd.Action == keys.SearchPrevious {
			direction = -direction
		}
		m.nextMatch(t.ID, direction, n)
		jump = true
	}
	if jump {
		if now, ok := m.snapshot(); ok && now != old {
			m.rememberJump(old)
		}
	}
}

func (m *model) startSearch(t *tabs.Tab, query string, backward bool) {
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		return
	}
	s := m.state(t.ID)
	if s.searchCancel != nil {
		s.searchCancel()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	s.searchCancel = cancel
	s.searchGeneration++
	s.searchQuery = query
	s.searchBackward = backward
	s.searchPages = nil
	s.searchIndex = -1
	m.message = "searching for " + query
	generation, id, path, password := s.searchGeneration, t.ID, t.Document.Path, d.password
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		pages, err := m.tools.SearchPages(ctx, path, password, query)
		select {
		case m.searchResults <- searchResult{id, generation, pages, err}:
		case <-ctx.Done():
		}
	}()
}
func (m *model) acceptSearch(r searchResult) {
	s := m.states[r.tab]
	if s == nil || s.searchGeneration != r.generation {
		return
	}
	if r.err != nil {
		m.message = r.err.Error()
		return
	}
	s.searchPages = r.pages
	s.searchIndex = -1
	if len(r.pages) == 0 {
		m.message = "no text matches (scanned PDF?)"
		return
	}
	view, ok := m.tabs.View(r.tab)
	if !ok {
		return
	}
	index := 0
	if s.searchBackward {
		index = len(r.pages) - 1
		for i := len(r.pages) - 1; i >= 0; i-- {
			if r.pages[i] <= view.Page {
				index = i
				break
			}
		}
	} else {
		for i, page := range r.pages {
			if page >= view.Page {
				index = i
				break
			}
		}
	}
	old := position{Tab: r.tab, View: view, widthFit: s.widthFit}
	s.searchIndex = index
	m.tabs.SetPage(r.tab, r.pages[index], 0)
	if v, _ := m.tabs.View(r.tab); v != view {
		m.rememberJump(old)
	}
	m.message = fmt.Sprintf("match %d/%d", index+1, len(r.pages))
}
func (m *model) nextMatch(id tabs.TabID, direction, count int) {
	s := m.state(id)
	if len(s.searchPages) == 0 {
		m.message = "no text matches (scanned PDF?)"
		return
	}
	n := len(s.searchPages)
	index := s.searchIndex
	if index < 0 {
		index = 0
	}
	index = (index + direction*(count%n) + n) % n
	s.searchIndex = index
	m.tabs.SetPage(id, s.searchPages[index], 0)
	m.message = fmt.Sprintf("match %d/%d", index+1, n)
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r >= 0x80 && r <= 0x9f {
			return ' '
		}
		return r
	}, s)
}
