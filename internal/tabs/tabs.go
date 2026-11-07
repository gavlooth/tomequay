// Package tabs keeps ordered PDF tabs with shared document metadata and separate
// views. Set is intended for one event-loop goroutine; it is not concurrency-safe.
package tabs

import (
	"math"
	"path/filepath"
)

// TabID identifies a tab for the lifetime of its Set. Zero is not a valid ID.
type TabID uint64

// FitMode selects the page sizing rule.
type FitMode int

const (
	FitPage FitMode = iota
	FitWidth
	Custom
)

const (
	// MinZoom and MaxZoom limit the multiplier on the fitted page size.
	MinZoom = 0.1
	MaxZoom = 16.0
)

// View belongs to one tab. ScrollX and ScrollY are fractions of the available
// horizontal and vertical scroll distance: 0 is the left/top, 1 the right/bottom.
// A renderer uses zero pixels on an axis when the page fits on that axis.
type View struct {
	Page    int // 1-based
	Fit     FitMode
	Zoom    float64 // multiplier on the fitted page size; 1 is an exact fit
	ScrollX float64
	ScrollY float64
}

// Document is shared by all tabs with the same canonical path. Treat its fields
// as read-only; change the page count with Set.UpdateDocument.
type Document struct {
	Path      string
	PageCount int
}

// Tab has its own View even when Document is shared with another tab.
type Tab struct {
	ID       TabID
	Document *Document
	View     View
}

// Set keeps tabs in display order. The zero value is ready to use.
type Set struct {
	tabs      []*Tab
	documents map[string]*Document
	nextID    TabID
	active    int
}

func NewSet() *Set { return &Set{} }

// Open appends and focuses a new tab. canonicalPath must already be canonical;
// use CanonicalPath at the file-opening boundary. Page counts below 1 become 1.
// If the document is already open, pageCount is ignored; use UpdateDocument to
// reload shared metadata without changing the document's identity.
func (s *Set) Open(canonicalPath string, pageCount int) TabID {
	if s.documents == nil {
		s.documents = make(map[string]*Document)
	}
	doc := s.documents[canonicalPath]
	if doc == nil {
		doc = &Document{Path: canonicalPath, PageCount: max(1, pageCount)}
		s.documents[canonicalPath] = doc
	}
	s.nextID++
	tab := &Tab{ID: s.nextID, Document: doc, View: View{Page: 1, Fit: FitPage, Zoom: 1}}
	s.tabs = append(s.tabs, tab)
	s.active = len(s.tabs) - 1
	return tab.ID
}

// Duplicate inserts a copy immediately after id and focuses it. An unknown id
// returns zero and leaves the set unchanged.
func (s *Set) Duplicate(id TabID) TabID {
	i := s.index(id)
	if i < 0 {
		return 0
	}
	s.nextID++
	tab := *s.tabs[i]
	tab.ID = s.nextID
	s.tabs = append(s.tabs, nil)
	copy(s.tabs[i+2:], s.tabs[i+1:])
	s.tabs[i+1] = &tab
	s.active = i + 1
	return tab.ID
}

// Close removes id. Closing the focused tab focuses its right neighbor, or its
// left neighbor if there is no tab on the right. Closing another tab preserves
// focus. The document is forgotten when its last tab closes.
func (s *Set) Close(id TabID) {
	i := s.index(id)
	if i < 0 {
		return
	}
	doc := s.tabs[i].Document
	copy(s.tabs[i:], s.tabs[i+1:])
	s.tabs[len(s.tabs)-1] = nil
	s.tabs = s.tabs[:len(s.tabs)-1]
	if i < s.active {
		s.active--
	} else if s.active >= len(s.tabs) && s.active > 0 {
		s.active--
	}
	for _, tab := range s.tabs {
		if tab.Document == doc {
			return
		}
	}
	delete(s.documents, doc.Path)
}

// Next focuses the following tab, wrapping at the end.
func (s *Set) Next() {
	if len(s.tabs) > 0 {
		s.active = (s.active + 1) % len(s.tabs)
	}
}

// Previous focuses the preceding tab, wrapping at the beginning.
func (s *Set) Previous() {
	if len(s.tabs) > 0 {
		s.active = (s.active + len(s.tabs) - 1) % len(s.tabs)
	}
}

func (s *Set) Focus(id TabID) bool {
	if i := s.index(id); i >= 0 {
		s.active = i
		return true
	}
	return false
}

// Active returns the focused tab, or nil and false for an empty set. The pointer
// remains attached to that tab through insertions and removals of other tabs.
// Prefer the Set methods to keep page, zoom, and scroll values within range.
func (s *Set) Active() (*Tab, bool) {
	if len(s.tabs) == 0 {
		return nil, false
	}
	return s.tabs[s.active], true
}

// Tabs returns an ordered snapshot. Changing its tabs or views does not change
// the set. Document pointers deliberately remain shared and are read-only.
func (s *Set) Tabs() []Tab {
	tabs := make([]Tab, len(s.tabs))
	for i, tab := range s.tabs {
		tabs[i] = *tab
	}
	return tabs
}

// GoToPage clamps page to the document's range. A page change resets scrolling.
// Methods accepting an unknown tab ID have no effect.
func (s *Set) GoToPage(id TabID, page int) {
	if i := s.index(id); i >= 0 {
		setPage(s.tabs[i], page)
	}
}

func setPage(tab *Tab, page int) {
	page = max(1, min(page, tab.Document.PageCount))
	if page != tab.View.Page {
		tab.View.Page = page
		tab.View.ScrollX, tab.View.ScrollY = 0, 0
	}
}

// NextPage advances by n pages, stopping at the final page. Nonpositive n does
// nothing. The distance is checked before addition to avoid integer overflow.
func (s *Set) NextPage(id TabID, n int) {
	if i := s.index(id); i >= 0 && n > 0 {
		tab := s.tabs[i]
		setPage(tab, tab.View.Page+min(n, tab.Document.PageCount-tab.View.Page))
	}
}

// PreviousPage moves back by n pages, stopping at page 1. Nonpositive n does
// nothing.
func (s *Set) PreviousPage(id TabID, n int) {
	if i := s.index(id); i >= 0 && n > 0 {
		tab := s.tabs[i]
		setPage(tab, tab.View.Page-min(n, tab.View.Page-1))
	}
}

// ZoomBy multiplies zoom, clamps it to [MinZoom, MaxZoom], and selects Custom.
// Nonpositive and nonfinite factors have no effect. Scroll fractions stay put.
func (s *Set) ZoomBy(id TabID, factor float64) {
	if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return
	}
	if i := s.index(id); i >= 0 {
		view := &s.tabs[i].View
		view.Zoom = max(MinZoom, min(MaxZoom, view.Zoom*factor))
		view.Fit = Custom
	}
}

// SetFit selects m and resets zoom and scrolling. Unknown modes do nothing.
func (s *Set) SetFit(id TabID, m FitMode) {
	if m != FitPage && m != FitWidth && m != Custom {
		return
	}
	if i := s.index(id); i >= 0 {
		view := &s.tabs[i].View
		view.Fit, view.Zoom = m, 1
		view.ScrollX, view.ScrollY = 0, 0
	}
}

// Scroll adds fractions of the available scroll distance and clamps each axis
// to [0, 1]. A NaN delta is ignored for that axis; infinities reach the endpoint.
func (s *Set) Scroll(id TabID, dx, dy float64) {
	if i := s.index(id); i >= 0 {
		view := &s.tabs[i].View
		if !math.IsNaN(dx) {
			view.ScrollX = max(0, min(1, view.ScrollX+dx))
		}
		if !math.IsNaN(dy) {
			view.ScrollY = max(0, min(1, view.ScrollY+dy))
		}
	}
}

// UpdateDocument replaces the shared page count and re-clamps every matching
// tab's page. Counts below 1 become 1; an unopened path has no effect.
func (s *Set) UpdateDocument(canonicalPath string, pageCount int) {
	doc := s.documents[canonicalPath]
	if doc == nil {
		return
	}
	doc.PageCount = max(1, pageCount)
	for _, tab := range s.tabs {
		if tab.Document == doc {
			setPage(tab, tab.View.Page)
		}
	}
}

func (s *Set) index(id TabID) int {
	for i, tab := range s.tabs {
		if tab.ID == id {
			return i
		}
	}
	return -1
}

// CanonicalPath returns an absolute path with symlinks resolved. Unlike the tab
// model, this helper accesses the filesystem and fails for nonexistent paths.
func CanonicalPath(p string) (string, error) {
	absolute, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
