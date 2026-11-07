package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tomequay/internal/history"
	"tomequay/internal/keys"
	"tomequay/internal/tabs"
)

type historySnapshot struct {
	tab            tabs.TabID
	view           tabs.View
	widthFit, dark bool
}

type historyRow struct {
	entry   history.Entry
	missing bool
}

type historyView struct {
	entries, filtered []historyRow
	filter            string
	filtering         bool
	selected          int
}

func (m *model) loadHistory() {
	m.historyStore = history.Default()
	data, err := m.historyStore.Load()
	if err != nil {
		m.message = "history: " + err.Error()
	}
	m.historyData = data
	m.historyViews = make(map[string]history.Entry)
	m.historyLive = make(map[string]history.Entry)
	m.decoder.SetHistory(data.Commands, data.Searches)
	m.lastSearch = data.LastSearch
	m.historySaved = time.Now()
}

func (m *model) historyEntry(path string) (history.Entry, bool) {
	if e, ok := m.historyViews[path]; ok {
		return e, true
	}
	if e, ok := m.historyLive[path]; ok {
		return e, true
	}
	for _, e := range m.historyData.Files {
		if e.Path == path {
			return e, true
		}
	}
	return history.Entry{}, false
}

// recordHistory keeps only the most recently active view for each path, not the
// last tab in display order. Disk writes are combined by saveHistory's timer.
func (m *model) recordHistory() {
	t, ok := m.tabs.Active()
	if !ok {
		return
	}
	s := m.state(t.ID)
	now := historySnapshot{tab: t.ID, view: t.View, widthFit: s.widthFit, dark: s.dark}
	if now == m.historyLast {
		return
	}
	m.historyLast = now
	e, found := m.historyEntry(t.Document.Path)
	if !found {
		e.Opened = time.Now()
	}
	e.Path, e.Page, e.Pages = t.Document.Path, t.View.Page, t.Document.PageCount
	e.Fit = "page"
	if t.View.Fit == tabs.FitWidth {
		e.Fit = "width"
	}
	if t.View.Fit == tabs.Custom {
		e.Fit = "custom"
	}
	e.Zoom, e.ScrollX, e.ScrollY = t.View.Zoom, t.View.ScrollX, t.View.ScrollY
	e.WidthFit, e.Dark = s.widthFit, s.dark
	if stat, err := os.Stat(e.Path); err == nil {
		e.Size, e.Mtime = stat.Size(), stat.ModTime().UnixNano()
	}
	m.historyViews[e.Path] = e
	m.historyLive[e.Path] = e
}

func (m *model) openedHistory() {
	m.historyLast = historySnapshot{}
	m.recordHistory()
	if t, ok := m.tabs.Active(); ok {
		e := m.historyViews[t.Document.Path]
		e.Opened = time.Now()
		m.historyViews[e.Path] = e
		m.historyLive[e.Path] = e
	}
}

func (m *model) saveHistory(force bool) {
	if m.historyStore == nil {
		return
	}
	m.recordHistory()
	if !force && time.Since(m.historySaved) < 5*time.Second {
		return
	}
	if force {
		for path, entry := range m.historyLive {
			m.historyViews[path] = entry
		}
	}
	changes := m.historyChanges
	if len(m.historyViews) == 0 && len(changes.Remove) == 0 && len(changes.Commands) == 0 && len(changes.Searches) == 0 && changes.LastSearch == nil {
		return
	}
	for _, e := range m.historyViews {
		changes.Files = append(changes.Files, e)
	}
	data, err := m.historyStore.Save(changes)
	m.historySaved = time.Now()
	if err != nil {
		m.message = "history: " + err.Error()
		return
	}
	m.historyData = data
	clear(m.historyViews)
	m.historyChanges = history.Changes{}
}

func (m *model) submittedPrompt(mode keys.Mode, line string) {
	if line == "" {
		return
	}
	if mode == keys.CommandLine {
		m.historyChanges.Commands = append(m.historyChanges.Commands, line)
	}
	if mode == keys.Search {
		m.historyChanges.Searches = append(m.historyChanges.Searches, line)
	}
}

func (m *model) historyEntries() []history.Entry {
	entries := make(map[string]history.Entry, len(m.historyData.Files)+len(m.historyViews))
	for _, e := range m.historyData.Files {
		entries[e.Path] = e
	}
	for _, path := range m.historyChanges.Remove {
		delete(entries, path)
	}
	for _, e := range m.historyViews {
		entries[e.Path] = e
	}
	result := make([]history.Entry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Opened.Equal(result[j].Opened) {
			return result[i].Path < result[j].Path
		}
		return result[i].Opened.After(result[j].Opened)
	})
	return result
}

func (m *model) historyPaths() []string {
	entries := m.historyEntries()
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths
}

func (m *model) restoreHistory(id tabs.TabID, e history.Entry) {
	fit := tabs.FitPage
	if e.Fit == "width" {
		fit = tabs.FitWidth
	}
	if e.Fit == "custom" {
		fit = tabs.Custom
	}
	m.tabs.SetFit(id, fit)
	if fit == tabs.Custom {
		m.tabs.ZoomBy(id, e.Zoom)
	}
	// SetPage always clamps against freshly loaded metadata, even if a file
	// changed identity or now has fewer pages than the saved entry.
	m.tabs.SetPage(id, e.Page, e.ScrollY)
	m.tabs.Scroll(id, e.ScrollX, 0)
	s := m.state(id)
	s.widthFit, s.dark = e.WidthFit || fit == tabs.FitWidth, e.Dark
}

func (m *model) showHistory() {
	m.recordHistory()
	p := &historyView{}
	for _, e := range m.historyEntries() {
		stat, err := os.Stat(e.Path)
		p.entries = append(p.entries, historyRow{entry: e, missing: err != nil || stat.IsDir()})
	}
	m.historyPanel = p
	p.refilter()
	m.message = ""
}

func (p *historyView) refilter() {
	p.filtered = p.filtered[:0]
	query := strings.ToLower(p.filter)
	for _, row := range p.entries {
		if strings.Contains(strings.ToLower(row.entry.Path), query) {
			p.filtered = append(p.filtered, row)
		}
	}
	p.selected = max(0, min(p.selected, len(p.filtered)-1))
}

func (m *model) historyKey(k keys.Key) {
	p := m.historyPanel
	if k.Special == keys.Esc {
		m.historyPanel = nil
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
				p.selected = 0
				p.refilter()
			}
		}
		return
	}
	switch {
	case k.Rune == '/':
		p.filtering = true
	case k.Rune == 'j' || k.Special == keys.Down:
		p.selected = max(0, min(len(p.filtered)-1, p.selected+1))
	case k.Rune == 'k' || k.Special == keys.Up:
		p.selected = max(0, p.selected-1)
	case k.Special == keys.Enter:
		if len(p.filtered) == 0 {
			return
		}
		row := p.filtered[p.selected]
		if row.missing {
			m.message = "missing file: " + row.entry.Path
			return
		}
		m.open(row.entry.Path, 0, "")
	case k.Rune == 'd':
		if len(p.filtered) == 0 {
			return
		}
		path := p.filtered[p.selected].entry.Path
		delete(m.historyViews, path)
		delete(m.historyLive, path)
		m.historyChanges.Remove = append(m.historyChanges.Remove, path)
		for i, row := range p.entries {
			if row.entry.Path == path {
				p.entries = append(p.entries[:i], p.entries[i+1:]...)
				break
			}
		}
		p.refilter()
		m.saveHistory(true)
	}
}

func relativeTime(opened, now time.Time) string {
	d := now.Sub(opened)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	if d < 30*24*time.Hour {
		return fmt.Sprintf("%d d ago", int(d/(24*time.Hour)))
	}
	return opened.Format("2006-01-02")
}

func (m *model) drawHistory() {
	p := m.historyPanel
	height := max(0, m.size.Rows-5)
	start := max(0, p.selected-height+1)
	title := "READING HISTORY"
	if p.filter != "" {
		title += " /" + p.filter
	}
	box := m.panel(title, "j/k select  Enter open  d forget  / filter  Esc close", start, height, len(p.filtered))
	now := time.Now()
	for i := range height {
		index := start + i
		if index >= len(p.filtered) {
			label := ""
			if i == 0 {
				label = "no matching history"
			}
			box.row(i, label, "", m.theme.muted())
			continue
		}
		row := p.filtered[index]
		e := row.entry
		pages := "?"
		if e.Pages > 0 {
			pages = fmt.Sprint(e.Pages)
		}
		right := fmt.Sprintf("p. %d/%s  %s", max(1, e.Page), pages, relativeTime(e.Opened, now))
		left := filepath.Base(e.Path) + "  " + filepath.Dir(e.Path)
		style := m.theme.plain()
		if index == p.selected {
			style = textStyle{fg: m.theme.Background, bg: m.theme.Blue, bold: true}
		}
		if row.missing {
			style = m.theme.muted()
			style.dim = true
			left = "[missing] " + left
		}
		left = ellipsisText(cleanText(left), max(0, box.width-5-textWidth(right)), m.theme.NoGlyphs)
		box.row(i, left, right, style)
	}
}
