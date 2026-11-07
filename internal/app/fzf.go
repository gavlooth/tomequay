package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"terminal-pdf-reader/internal/fzf"
	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/tabs"
	"terminal-pdf-reader/internal/terminal"
)

type fuzzyRequest struct {
	action   keys.Action
	tab      tabs.TabID
	path     string
	password string
	query    string
	pages    []int
}

func (m *model) recentPaths() []string { return m.historyPaths() }

func (m *model) queueFuzzy(action keys.Action, query string) {
	if err := fzf.Available(); err != nil {
		m.message = err.Error()
		return
	}
	r := &fuzzyRequest{action: action, query: query}
	if tab, ok := m.tabs.Active(); ok {
		r.tab, r.path = tab.ID, tab.Document.Path
		if d := m.docs[r.path]; d != nil {
			r.password = d.password
		}
	} else if action != keys.FuzzyFiles {
		m.message = "open a PDF first"
		return
	}
	if action == keys.FuzzyGrep {
		if r.query == "" {
			r.query = m.state(r.tab).searchQuery
		}
		if r.query == "" {
			m.message = "enter a query with g/ or :grep QUERY"
			return
		}
	}
	if action == keys.FuzzyOutline {
		d := m.docs[r.path]
		if d == nil || !d.ready {
			return
		}
		m.requestOutline(r.path, d)
		if !d.outlineLoaded {
			m.message = "loading outline for fzf"
		}
	}
	m.fuzzyPending = r
}

// runQueuedFuzzy waits for the reader's outstanding graphics reply before a
// popup can take keyboard focus. No image upload or app drawing runs inside the
// picker; the terminal reader continues parsing replies in the tmux case.
func (m *model) runQueuedFuzzy() bool {
	r := m.fuzzyPending
	if r == nil || m.pendingUpload != nil {
		return false
	}
	if r.action == keys.FuzzyOutline {
		d := m.docs[r.path]
		if d != nil && !d.outlineLoaded {
			return false
		}
	}
	m.fuzzyPending = nil
	var err error
	if m.fuzzyCache == "" {
		m.fuzzyCache, err = os.MkdirTemp("", "pdfr-text-*")
		if err != nil {
			m.message = "fzf text cache: " + err.Error()
			return true
		}
	}
	request, err := m.fuzzyItems(r)
	if err != nil {
		m.message = err.Error()
		return true
	}
	request.CacheDirectory = m.fuzzyCache
	request.Popup = terminal.InTmux()
	var resume func() error
	if !request.Popup {
		screen, ok := m.screen.(interface {
			Suspend() error
			Resume() error
		})
		if !ok {
			m.message = "fzf needs a terminal that can suspend input"
			return true
		}
		if err := screen.Suspend(); err != nil {
			m.noteOutput(err)
			return true
		}
		resume = screen.Resume
	}
	ctx, cancel := context.WithCancel(m.ctx)
	type result struct {
		items []fzf.Candidate
		err   error
	}
	done := make(chan result, 1)
	go func() {
		items, err := fzf.Run(ctx, request)
		done <- result{items, err}
	}()
	var followingKeys []keys.Key
	defer func() {
		for _, key := range followingKeys {
			if !m.quit {
				m.key(key)
			}
		}
	}()
	var selected result
	waiting := true
	for waiting {
		select {
		case selected = <-done:
			waiting = false
		case <-ctx.Done():
			selected = <-done
			waiting = false
		case event, ok := <-m.screen.Events():
			if !ok {
				cancel()
				m.quit = true
				continue
			}
			switch event := event.(type) {
			case terminal.KeyEvent:
				// tmux may return focus before the fzf child has fully exited.
				// Preserve keys typed immediately after accepting or canceling.
				followingKeys = append(followingKeys, event.Key)
			case terminal.GraphicsReplyEvent:
				m.graphicsReply(event)
			case terminal.ResizeEvent:
				m.size = event.Size
			case terminal.ErrorEvent:
				var signal *terminal.SignalError
				if !errors.As(event.Err, &signal) {
					m.noteOutput(event.Err)
				}
				m.quit = true
				cancel()
			}
		}
	}
	cancel()
	if resume != nil {
		m.noteOutput(resume())
		// Leaving the alternate screen can discard the terminal's image data.
		// Resume therefore uploads fresh IDs instead of trusting old cells.
		for key := range m.imageIDs {
			m.deleteImage(key)
		}
	}
	m.frame = drawnFrame{}
	if selected.err != nil {
		if !errors.Is(selected.err, context.Canceled) {
			m.message = selected.err.Error()
		}
		return true
	}
	if len(selected.items) > 0 {
		m.selectFuzzy(r, selected.items)
	}
	return true
}

func (m *model) fuzzyItems(r *fuzzyRequest) (fzf.Request, error) {
	request := fzf.Request{Password: r.password}
	switch r.action {
	case keys.FuzzyFiles:
		home, _ := os.UserHomeDir()
		cwd, _ := os.Getwd()
		dir := ""
		if r.path != "" {
			dir = filepath.Dir(r.path)
		}
		roots := fzf.Roots(os.Getenv("PDFR_ROOTS"), dir, home, cwd)
		recent := append([]string(nil), m.recentPaths()...)
		request.Header, request.Prompt, request.Multi = "PDFs · Tab selects several · Enter opens new tabs · scan: depth 3, at most 5000 files", "Files> ", true
		request.Password = ""
		request.Items = func(ctx context.Context, yield func(fzf.Candidate) bool) error {
			return fzf.Files(ctx, recent, roots, yield)
		}
	case keys.FuzzyTabs:
		var items []fzf.Candidate
		passwords := make(map[string]string)
		for i, tab := range m.tabs.Tabs() {
			items = append(items, fzf.Candidate{Path: tab.Document.Path, Page: tab.View.Page, ID: uint64(tab.ID), Label: fmt.Sprintf("%d  p. %d  %s", i+1, tab.View.Page, tab.Document.Path)})
			if d := m.docs[tab.Document.Path]; d != nil && d.password != "" {
				passwords[tab.Document.Path] = d.password
			}
		}
		request.Header, request.Prompt = "Open tabs · Enter switches", "Tabs> "
		tools, directory := m.tools, m.fuzzyCache
		request.Password = ""
		request.Items = func(ctx context.Context, yield func(fzf.Candidate) bool) error {
			for _, item := range items {
				if password := passwords[item.Path]; password != "" {
					if _, err := fzf.PageText(ctx, tools, item.Path, password, item.Page, directory); err != nil {
						return err
					}
				}
				if ctx.Err() != nil || !yield(item) {
					return ctx.Err()
				}
			}
			return nil
		}
	case keys.FuzzyOutline:
		d := m.docs[r.path]
		if d == nil || !d.ready {
			return request, errors.New("document is not ready")
		}
		if d.outlineError != "" {
			return request, errors.New(d.outlineError)
		}
		var items []fzf.Candidate
		for i, entry := range d.outline {
			if entry.Page > 0 {
				items = append(items, fzf.Candidate{Path: r.path, Page: entry.Page, ID: uint64(i), Label: fzf.OutlineLabel(entry.Title, entry.Depth, entry.Page)})
			}
		}
		if len(items) == 0 {
			return request, errors.New("document has no usable outline")
		}
		request.Header, request.Prompt = "Outline · Enter jumps · indentation shows section depth", "Outline> "
		request.Items = candidateItems(items)
	case keys.FuzzyGrep:
		request.Header = "Text: " + r.query + " · first 200 matching pages only · Enter jumps; n/N continues the query"
		request.Prompt, request.Query = "Matches> ", r.query
		tools, directory := m.tools, m.fuzzyCache
		request.Items = func(ctx context.Context, yield func(fzf.Candidate) bool) error {
			pages, err := tools.SearchPages(ctx, r.path, r.password, r.query)
			if err != nil {
				return err
			}
			r.pages = pages
			for _, page := range pages[:min(200, len(pages))] {
				text, err := fzf.PageText(ctx, tools, r.path, r.password, page, directory)
				if err != nil {
					return err
				}
				for _, item := range fzf.MatchLines(text, r.query, page) {
					item.Path = r.path
					if !yield(item) {
						return ctx.Err()
					}
				}
			}
			return nil
		}
	}
	return request, nil
}

func candidateItems(items []fzf.Candidate) func(context.Context, func(fzf.Candidate) bool) error {
	return func(ctx context.Context, yield func(fzf.Candidate) bool) error {
		for _, item := range items {
			if ctx.Err() != nil || !yield(item) {
				return ctx.Err()
			}
		}
		return nil
	}
}

func (m *model) selectFuzzy(r *fuzzyRequest, items []fzf.Candidate) {
	defer m.recordHistory()
	if r.action == keys.FuzzyFiles {
		for _, item := range items {
			m.open(item.Path, 0, "")
		}
		return
	}
	item := items[0]
	if r.action == keys.FuzzyTabs {
		m.tabs.Focus(tabs.TabID(item.ID))
		return
	}
	tab, ok := m.tabs.Active()
	if !ok || tab.ID != r.tab || tab.Document.Path != r.path {
		return
	}
	old, _ := m.snapshot()
	m.tabs.SetPage(tab.ID, item.Page, 0)
	if now, ok := m.snapshot(); ok && now != old {
		m.rememberJump(old)
	}
	m.message = strings.TrimSpace(item.Label)
	if r.action == keys.FuzzyGrep {
		s := m.state(tab.ID)
		if s.searchCancel != nil {
			s.searchCancel()
			s.searchCancel = nil
		}
		s.searchGeneration++
		s.searchQuery, s.searchPages, s.searchBackward = r.query, r.pages, false
		m.lastSearch = r.query
		m.historyChanges.LastSearch = &m.lastSearch
		s.searchIndex = -1
		for i, page := range r.pages {
			if page == item.Page {
				s.searchIndex = i
				break
			}
		}
		if len(r.pages) > 200 {
			m.message = fmt.Sprintf("showing first 200 of %d matching pages; n/N searches all matches", len(r.pages))
		}
	}
}

func (m *model) closeFuzzy() {
	if m.fuzzyCache != "" {
		_ = os.RemoveAll(m.fuzzyCache)
	}
}
