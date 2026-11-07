package app

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/pdf"
	"terminal-pdf-reader/internal/tabs"
)

type pickerEntry struct {
	path, label string
	dir         bool
}
type picker struct {
	directory string
	entries   []pickerEntry
	filtered  []pickerEntry
	filter    string
	filtering bool
	selected  int
}

func (m *model) open(path string, page int, password string) {
	m.recordHistory()
	canonical, err := tabs.CanonicalPath(path)
	if err != nil {
		m.message = err.Error()
		return
	}
	if m.toolErr != nil {
		m.message = m.toolErr.Error()
		return
	}
	d := m.docs[canonical]
	if d == nil || !d.ready {
		info, infoErr := m.tools.Info(m.ctx, canonical, password)
		if infoErr != nil {
			if errors.Is(infoErr, pdf.ErrPasswordRequired) || errors.Is(infoErr, pdf.ErrWrongPassword) {
				attempt := openAttempt{canonical, page}
				if m.passwordFor == nil {
					m.passwordFor = &attempt
					m.password = nil
				} else {
					m.passwordQueue = append(m.passwordQueue, attempt)
				}
				m.message = "PDF password required (Esc cancels)"
			} else {
				m.message = infoErr.Error()
			}
			return
		}
		stat, statErr := os.Stat(canonical)
		if statErr != nil {
			m.message = statErr.Error()
			return
		}
		d = &document{info: info, mtime: stat.ModTime().UnixNano(), password: password, ready: true}
		m.docs[canonical] = d
	}
	id := m.tabs.Open(canonical, d.info.PageCount)
	if page > 0 {
		m.tabs.SetPage(id, page, 0)
	} else if entry, ok := m.historyEntry(canonical); ok {
		m.restoreHistory(id, entry)
	}
	m.state(id).searchQuery = m.lastSearch
	m.picker = nil
	m.historyPanel = nil
	m.message = ""
	m.openedHistory()
}

func (m *model) reload(path string) {
	d := m.docs[path]
	if d == nil || m.toolErr != nil {
		return
	}
	m.invalidate()
	m.dropDocument(path)
	// Failed reloads must not keep displaying a stale document version.
	d.ready = false
	for _, t := range m.tabs.Tabs() {
		if t.Document.Path == path {
			s := m.state(t.ID)
			if s.searchCancel != nil {
				s.searchCancel()
			}
			s.searchGeneration++
			s.searchPages = nil
			s.searchIndex = -1
		}
	}
	stat, err := os.Stat(path)
	if err != nil {
		m.message = err.Error()
		return
	}
	d.mtime = stat.ModTime().UnixNano()
	info, err := m.tools.Info(m.ctx, path, d.password)
	if err != nil {
		m.message = err.Error()
		return
	}
	d.info, d.ready = info, true
	m.tabs.UpdateDocument(path, info.PageCount)
	m.message = "reloaded " + filepath.Base(path)
}

func (m *model) pollFiles() bool {
	changed := false
	for path, d := range m.docs {
		stat, err := os.Stat(path)
		if err != nil {
			if d.ready {
				m.reload(path)
				changed = true
			}
			continue
		}
		if stat.ModTime().UnixNano() != d.mtime {
			m.reload(path)
			changed = true
		}
	}
	return changed
}

func (m *model) showPicker() {
	dir, _ := os.Getwd()
	if t, ok := m.tabs.Active(); ok {
		dir = filepath.Dir(t.Document.Path)
	}
	m.picker = &picker{}
	m.readDirectory(dir)
}

func (m *model) readDirectory(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		m.message = err.Error()
		return
	}
	p := m.picker
	p.directory, p.entries, p.filter, p.selected = dir, nil, "", 0
	for _, path := range m.historyPaths() {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			p.entries = append(p.entries, pickerEntry{path: path, label: "recent: " + path})
		}
	}
	var local []pickerEntry
	for _, e := range entries {
		isDir := e.IsDir()
		if !isDir && !strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
			continue
		}
		label := e.Name()
		if isDir {
			label += "/"
		}
		local = append(local, pickerEntry{path: filepath.Join(dir, e.Name()), label: label, dir: isDir})
	}
	sort.SliceStable(local, func(i, j int) bool {
		if local[i].dir != local[j].dir {
			return local[i].dir
		}
		return strings.ToLower(local[i].label) < strings.ToLower(local[j].label)
	})
	p.entries = append(p.entries, local...)
	p.refilter()
}
func (p *picker) refilter() {
	p.filtered = p.filtered[:0]
	for _, e := range p.entries {
		if strings.Contains(strings.ToLower(e.label), strings.ToLower(p.filter)) {
			p.filtered = append(p.filtered, e)
		}
	}
	p.selected = max(0, min(p.selected, len(p.filtered)-1))
}
func (m *model) pickerKey(k keys.Key) {
	p := m.picker
	if k.Special == keys.Esc {
		if p.filtering {
			p.filtering = false
		} else {
			m.picker = nil
			if _, ok := m.tabs.Active(); !ok {
				m.quit = true
			}
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
				p.selected = 0
				p.refilter()
			}
		}
		return
	}
	switch {
	case k.Rune == 'q' || k.Rune == 'Q':
		m.picker = nil
		if _, ok := m.tabs.Active(); !ok {
			m.quit = true
		}
	case k.Rune == '/':
		p.filtering = true
	case k.Rune == 'j' || k.Special == keys.Down:
		p.selected = max(0, min(len(p.filtered)-1, p.selected+1))
	case k.Rune == 'k' || k.Special == keys.Up:
		p.selected = max(0, p.selected-1)
	case k.Rune == 'h' || k.Special == keys.Left:
		m.readDirectory(filepath.Dir(p.directory))
	case k.Special == keys.Enter || k.Rune == 'l':
		if p.selected < 0 || p.selected >= len(p.filtered) {
			return
		}
		e := p.filtered[p.selected]
		if e.dir {
			m.readDirectory(e.path)
		} else {
			m.open(e.path, 0, "")
		}
	}
}
