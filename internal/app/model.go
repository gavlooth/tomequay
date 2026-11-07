// Package app implements the reader's single-owner event loop and render pipeline.
package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"sync"

	"terminal-pdf-reader/internal/cache"
	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/pdf"
	"terminal-pdf-reader/internal/tabs"
	"terminal-pdf-reader/internal/terminal"
)

// Options contains the deliberately small command-line surface.
type Options struct {
	Paths      []string
	CacheBytes int64
	Page       int
}

type screen interface {
	Events() <-chan terminal.Event
	Size() terminal.Size
	Writer() io.Writer
	Flush() error
}

type images interface {
	SendPNG(uint32, io.Reader, int, int) error
	Place(uint32, int, int) error
	WriteCells(uint32, int, int, int, int) error
	Delete(uint32) error
}

type document struct {
	info              pdf.DocumentInfo
	mtime             int64
	password          string
	ready             bool
	outline           []pdf.OutlineEntry
	outlineLoaded     bool
	outlineGeneration uint64
	outlineCancel     context.CancelFunc
}

type position struct {
	Tab      tabs.TabID
	View     tabs.View
	widthFit bool
}
type tabState struct {
	widthFit         bool
	marks            map[rune]position
	previous         *position
	jumps            []position
	jumpIndex        int
	searchQuery      string
	searchPages      []int
	searchIndex      int
	searchBackward   bool
	searchGeneration uint64
	searchCancel     context.CancelFunc
}

type renderResult struct {
	tab        tabs.TabID
	generation uint64
	key        cache.Key
	layout     pageLayout
	png        []byte
	err        error
	foreground bool
}
type searchResult struct {
	tab        tabs.TabID
	generation uint64
	pages      []int
	err        error
}
type imageRecord struct {
	id            uint32
	sent, cached  bool
	failed        bool
	columns, rows int
}
type openAttempt struct {
	path string
	page int
}

type frameKind uint8

const (
	blankFrame frameKind = iota
	pageFrame
	overlayFrame
	smallFrame
)

type drawnFrame struct {
	size                terminal.Size
	tab                 tabs.TabID
	key                 cache.Key
	id                  uint32
	x, y, columns, rows int
	kind                frameKind
}

type model struct {
	tabs           *tabs.Set
	states         map[tabs.TabID]*tabState
	docs           map[string]*document
	decoder        keys.Decoder
	tools          pdf.Tools
	toolErr        error
	cache          *cache.Cache
	images         images
	screen         screen
	size           terminal.Size
	message        string
	quit, help     bool
	helpOffset     int
	picker         *picker
	outline        *outlineView
	recent         []string
	recentFile     string
	password       []rune
	passwordFor    *openAttempt
	passwordQueue  []openAttempt
	results        chan renderResult
	searchResults  chan searchResult
	outlineResults chan outlineResult
	generation     uint64
	renderCancel   context.CancelFunc
	workers        sync.WaitGroup
	requested      bool
	requestedTab   tabs.TabID
	requestedKey   cache.Key
	layout         pageLayout
	currentPNG     []byte
	currentKey     cache.Key
	imageIDs       map[cache.Key]*imageRecord
	nextID         uint32
	outputErr      error
	frame          drawnFrame
	pendingUpload  *pendingUpload
	ctx            context.Context
}

func newModel(s screen, out images, tools pdf.Tools, toolErr error, opts Options) *model {
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	id := binary.LittleEndian.Uint32(seed[:]) & 0xffffff
	if id == 0 {
		id = 1
	}
	m := &model{tabs: tabs.NewSet(), states: make(map[tabs.TabID]*tabState), docs: make(map[string]*document),
		tools: tools, toolErr: toolErr, cache: cache.New(opts.CacheBytes),
		screen: s, images: out, size: s.Size(), results: make(chan renderResult, 8), searchResults: make(chan searchResult, 8),
		outlineResults: make(chan outlineResult, 8),
		imageIDs:       make(map[cache.Key]*imageRecord), nextID: id, ctx: context.Background()}
	m.recentFile, m.recent = loadRecent()
	return m
}

func (m *model) state(id tabs.TabID) *tabState {
	s := m.states[id]
	if s == nil {
		s = &tabState{marks: make(map[rune]position), jumpIndex: -1, searchIndex: -1}
		m.states[id] = s
	}
	return s
}

func (m *model) snapshot() (position, bool) {
	t, ok := m.tabs.Active()
	if !ok {
		return position{}, false
	}
	return position{Tab: t.ID, View: t.View, widthFit: m.state(t.ID).widthFit}, true
}

func (m *model) rememberJump(old position) {
	s := m.state(old.Tab)
	s.previous = &old
	if s.jumpIndex >= 0 && s.jumpIndex < len(s.jumps)-1 {
		s.jumps = s.jumps[:s.jumpIndex+1]
	}
	if len(s.jumps) == 0 || s.jumps[len(s.jumps)-1] != old {
		s.jumps = append(s.jumps, old)
	}
	if len(s.jumps) > 100 {
		s.jumps = s.jumps[len(s.jumps)-100:]
	}
	s.jumpIndex = len(s.jumps) - 1
}

func (m *model) restore(p position) {
	if !m.tabs.Focus(p.Tab) {
		m.message = "position's tab is closed"
		return
	}
	m.state(p.Tab).widthFit = p.widthFit
	m.tabs.SetFit(p.Tab, p.View.Fit)
	if p.View.Fit == tabs.Custom {
		m.tabs.ZoomBy(p.Tab, p.View.Zoom)
	}
	m.tabs.SetPage(p.Tab, p.View.Page, p.View.ScrollY)
	m.tabs.Scroll(p.Tab, p.View.ScrollX, 0)
}

func (m *model) invalidate() {
	if m.renderCancel != nil {
		m.renderCancel()
		m.renderCancel = nil
	}
	m.generation++
	m.requested = false
	m.currentPNG = nil
	for k, r := range m.imageIDs {
		if !r.cached {
			m.deleteImage(k)
		}
	}
}

func (m *model) deleteImage(k cache.Key) {
	if r := m.imageIDs[k]; r != nil {
		m.noteOutput(m.images.Delete(r.id))
		delete(m.imageIDs, k)
	}
}
func (m *model) noteOutput(err error) {
	if err != nil && m.outputErr == nil {
		m.outputErr = err
	}
}
func (m *model) sweepImages() {
	for k, r := range m.imageIDs {
		if r.cached && !m.cache.Contains(k) {
			m.deleteImage(k)
		}
	}
}
func (m *model) dropDocument(path string) {
	m.dropOutline(path)
	m.cache.DropPath(path)
	for k := range m.imageIDs {
		if k.Path == path {
			m.deleteImage(k)
		}
	}
}
func (m *model) image(k cache.Key, cached bool) *imageRecord {
	if r := m.imageIDs[k]; r != nil {
		return r
	}
	// Monotonic allocation never reuses an ID while this process owns it.
	id := m.nextID
	m.nextID++
	if m.nextID == 0 {
		m.nextID = 1
	}
	r := &imageRecord{id: id, cached: cached}
	m.imageIDs[k] = r
	return r
}
