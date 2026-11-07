package app

import (
	"bytes"
	"context"
	"io"
	"testing"

	"terminal-pdf-reader/internal/cache"
	"terminal-pdf-reader/internal/imageout"
	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/pdf"
	"terminal-pdf-reader/internal/tabs"
	"terminal-pdf-reader/internal/terminal"
)

type fakeScreen struct {
	bytes.Buffer
	size    terminal.Size
	flushes int
}

func (f *fakeScreen) Events() <-chan terminal.Event { return nil }
func (f *fakeScreen) Size() terminal.Size           { return f.size }
func (f *fakeScreen) Writer() io.Writer             { return &f.Buffer }
func (f *fakeScreen) Flush() error                  { f.flushes++; return nil }

type fakeImages struct {
	deleted     []uint32
	sent, cells int
}

func (f *fakeImages) SendPNG(_ uint32, _ io.Reader, _, _ int) error { f.sent++; return nil }
func (f *fakeImages) Place(uint32, int, int) error                  { return nil }
func (f *fakeImages) WriteCells(uint32, int, int, int, int) error   { f.cells++; return nil }
func (f *fakeImages) Delete(id uint32) error                        { f.deleted = append(f.deleted, id); return nil }
func testModel(t *testing.T, budget int64) (*model, *fakeImages) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	screen := &fakeScreen{size: terminal.Size{Columns: 100, Rows: 42, CellWidthPixels: 10, CellHeightPixels: 20}}
	out := &fakeImages{}
	m := newModel(screen, out, pdf.Tools{}, nil, Options{CacheBytes: budget})
	m.ctx = context.Background()
	return m, out
}
func addTab(m *model, path string) tabs.TabID {
	m.docs[path] = &document{info: pdf.DocumentInfo{PageCount: 100, PageWidthPoints: 600, PageHeightPoints: 800}, mtime: 1, ready: true}
	id := m.tabs.Open(path, 100)
	m.state(id)
	return id
}
func feed(m *model, s string) {
	for _, r := range s {
		m.key(keys.Key{Rune: r})
	}
}

func TestLayoutFitCenterPixels(t *testing.T) {
	size := terminal.Size{Columns: 100, Rows: 42, CellWidthPixels: 10, CellHeightPixels: 20}
	info := pdf.DocumentInfo{PageWidthPoints: 600, PageHeightPoints: 800}
	view := tabs.View{Page: 1, Fit: tabs.FitPage, Zoom: 1}
	l := layoutPage(size, info, view, false)
	if l.X != 20 || l.Y != 1 || l.Columns != 60 || l.Rows != 40 {
		t.Fatalf("fit layout: %+v", l)
	}
	if l.Request.WidthPixels != 600 || l.Request.HeightPixels != 800 || l.Request.CropWidth != 0 {
		t.Fatalf("fit pixels: %+v", l.Request)
	}
	view.Fit = tabs.FitWidth
	l = layoutPage(size, info, view, true)
	if l.Columns != 100 || l.Rows != 40 || l.Request.CropWidth != 1000 || l.Request.CropHeight != 800 {
		t.Fatalf("width crop: %+v", l)
	}
	if l.FullWidth != 1000 || l.FullHeight != 1334 {
		t.Fatalf("scaled width: %+v", l)
	}
	size.Columns = 19
	if l := layoutPage(size, info, view, false); l.Columns != 0 {
		t.Fatalf("small: %+v", l)
	}
}
func TestLayoutScrollMapsOverflow(t *testing.T) {
	l := layoutPage(terminal.Size{Columns: 100, Rows: 42, CellWidthPixels: 10, CellHeightPixels: 20}, pdf.DocumentInfo{PageWidthPoints: 600, PageHeightPoints: 800}, tabs.View{Page: 3, Fit: tabs.Custom, Zoom: 2, ScrollX: 0.25, ScrollY: 0.75}, false)
	if l.Request.CropX != 50 || l.Request.CropY != 600 || l.Request.CropWidth != 1000 || l.Request.CropHeight != 800 {
		t.Fatalf("crop: %+v", l)
	}
}
func TestScrollEdgesAndCounts(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		page, pages          int
		y, overflow, step    float64
		count, dir, wantPage int
		wantY                float64
	}{
		{"reach bottom", 2, 10, 0.9, 1000, 100, 1, 1, 2, 1},
		{"flip after bottom", 2, 10, 1, 1000, 100, 1, 1, 3, 0},
		{"previous bottom", 2, 10, 0, 1000, 100, 1, -1, 1, 1},
		{"fit count", 2, 10, 0, 0, 100, 3, 1, 5, 0},
		{"count crossing", 2, 10, 0.9, 1000, 100, 3, 1, 3, 0.1},
		{"last page", 10, 10, 1, 1000, 100, 100, 1, 10, 1},
		{"first page", 1, 10, 0, 0, 100, 100, -1, 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, y := scrollVertical(tt.page, tt.pages, tt.y, tt.overflow, tt.step, tt.count, tt.dir)
			if p != tt.wantPage || y < tt.wantY-1e-8 || y > tt.wantY+1e-8 {
				t.Fatalf("got %d %g, want %d %g", p, y, tt.wantPage, tt.wantY)
			}
		})
	}
}
func TestStaleRenderDropped(t *testing.T) {
	m, _ := testModel(t, 100)
	id := addTab(m, "/one.pdf")
	key := cache.Key{Path: "/one.pdf", Page: 1}
	m.requested, m.requestedTab, m.requestedKey, m.generation = true, id, key, 7
	for _, r := range []renderResult{
		{tab: id, generation: 6, key: key, foreground: true, png: []byte("old")},
		{tab: id + 1, generation: 7, key: key, foreground: true, png: []byte("tab")},
		{tab: id, generation: 7, key: cache.Key{Path: "/one.pdf", Page: 2}, foreground: true, png: []byte("page")},
	} {
		if m.acceptRender(r) {
			t.Fatal("accepted stale render")
		}
	}
	if m.cache.Len() != 0 || len(m.currentPNG) != 0 {
		t.Fatal("stale render changed state")
	}
	if !m.acceptRender(renderResult{tab: id, generation: 7, key: key, foreground: true, png: []byte("new")}) || string(m.currentPNG) != "new" {
		t.Fatal("current render rejected")
	}
}
func TestImageEvictionAndCachedReuse(t *testing.T) {
	m, out := testModel(t, 4)
	id := addTab(m, "/one.pdf")
	key := cache.Key{Path: "/one.pdf", Page: 1}
	m.requested, m.requestedTab, m.requestedKey, m.generation = true, id, key, 1
	l := pageLayout{Columns: 10, Rows: 10}
	m.acceptRender(renderResult{tab: id, generation: 1, key: key, foreground: true, png: []byte("one"), layout: l})
	m.ensureUpload()
	if out.cells != 0 || m.pendingUpload == nil {
		t.Fatal("cells must wait for upload acknowledgment")
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: m.pendingUpload.id, OK: true, Message: "OK"})
	m.drawPage()
	m.drawPage()
	if out.sent != 1 || out.cells != 2 {
		t.Fatalf("sent=%d cells=%d", out.sent, out.cells)
	}
	oldID := m.imageIDs[key].id
	m.acceptRender(renderResult{tab: id, generation: 1, key: cache.Key{Path: "/one.pdf", Page: 2}, png: []byte("two")})
	if len(out.deleted) != 1 || out.deleted[0] != oldID {
		t.Fatalf("deleted: %v", out.deleted)
	}
	if _, ok := m.imageIDs[key]; ok {
		t.Fatal("evicted ID retained")
	}
}
func TestCommandsCountsTabsDuplicateMarksJumps(t *testing.T) {
	m, _ := testModel(t, 100)
	original := addTab(m, "/one.pdf")
	feed(m, "12G")
	if v, _ := m.tabs.View(original); v.Page != 12 {
		t.Fatalf("counted G: %+v", v)
	}
	feed(m, "ma3J")
	feed(m, "gd2J")
	duplicate, _ := m.tabs.Active()
	if duplicate.ID == original || duplicate.View.Page != 17 {
		t.Fatalf("duplicate: %+v", duplicate)
	}
	if v, _ := m.tabs.View(original); v.Page != 15 {
		t.Fatalf("original changed: %+v", v)
	}
	feed(m, "gt")
	if active, _ := m.tabs.Active(); active.ID != original {
		t.Fatal("gt did not wrap")
	}
	feed(m, "'a")
	if v, _ := m.tabs.View(original); v.Page != 12 {
		t.Fatalf("mark: %+v", v)
	}
	feed(m, "''")
	if v, _ := m.tabs.View(original); v.Page != 15 {
		t.Fatalf("jump back: %+v", v)
	}
	feed(m, "2gt")
	if active, _ := m.tabs.Active(); active.ID != duplicate.ID {
		t.Fatal("counted gt")
	}
	feed(m, "1gt20G30G")
	m.key(keys.Key{Rune: 'o', Ctrl: true})
	if v, _ := m.tabs.View(original); v.Page != 20 {
		t.Fatalf("older: %+v", v)
	}
	m.key(keys.Key{Rune: 'i', Ctrl: true})
	if v, _ := m.tabs.View(original); v.Page != 30 {
		t.Fatalf("newer: %+v", v)
	}
	feed(m, "q")
	if m.quit {
		t.Fatal("quit with duplicate open")
	}
	feed(m, "q")
	if !m.quit {
		t.Fatal("last tab did not quit")
	}
}
func TestSearchDirectionsAndNoText(t *testing.T) {
	m, _ := testModel(t, 100)
	id := addTab(m, "/one.pdf")
	m.tabs.SetPage(id, 12, 0)
	s := m.state(id)
	s.searchGeneration = 1
	m.acceptSearch(searchResult{tab: id, generation: 1, pages: []int{2, 12, 30}})
	m.command(keys.Command{Action: keys.SearchNext, Count: 2})
	if v, _ := m.tabs.View(id); v.Page != 2 {
		t.Fatalf("counted search: %+v", v)
	}
	m.command(keys.Command{Action: keys.SearchPrevious})
	if v, _ := m.tabs.View(id); v.Page != 30 {
		t.Fatalf("previous search: %+v", v)
	}
	m.acceptSearch(searchResult{tab: id, generation: 1})
	if m.message != "no text matches (scanned PDF?)" {
		t.Fatal(m.message)
	}
}
func TestFrameFlushAndTerminalSanitization(t *testing.T) {
	m, _ := testModel(t, 100)
	addTab(m, "/one.pdf")
	m.message = "bad\x1b[2J\nname"
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	s := m.screen.(*fakeScreen)
	if s.flushes != 1 {
		t.Fatalf("flushes=%d", s.flushes)
	}
	if bytes.Contains(s.Bytes(), []byte("bad\x1b")) {
		t.Fatal("terminal injection")
	}
}

func TestCountedScrollAndFitWidthMarkRestore(t *testing.T) {
	m, _ := testModel(t, 100)
	id := addTab(m, "/one.pdf")
	feed(m, "+5j")
	view, _ := m.tabs.View(id)
	layout := m.activeLayout()
	if view.Page != 2 || view.ScrollY <= 0 || layout.Request.CropY == 0 {
		t.Fatalf("counted scroll did not crop: %+v %+v", view, layout)
	}
	feed(m, "zw+ma")
	saved, _ := m.tabs.View(id)
	feed(m, "zp'a")
	restored, _ := m.tabs.View(id)
	if restored != saved || !m.state(id).widthFit {
		t.Fatalf("mark lost view: got %+v, want %+v", restored, saved)
	}
}

func TestPickerFilteringAndNavigation(t *testing.T) {
	p := &picker{entries: []pickerEntry{{label: "recent: /docs/Quantum.pdf"}, {label: "notes/"}, {label: "classical.PDF"}}, filter: "quantum"}
	p.refilter()
	if len(p.filtered) != 1 || p.filtered[0].label != "recent: /docs/Quantum.pdf" {
		t.Fatalf("filter: %+v", p.filtered)
	}
	m, _ := testModel(t, 100)
	addTab(m, "/one.pdf")
	m.picker = p
	m.key(keys.Key{Rune: '/'})
	m.key(keys.Key{Special: keys.Backspace})
	if p.filter != "quantu" || !p.filtering {
		t.Fatalf("filter editing: %+v", p)
	}
	m.key(keys.Key{Special: keys.Esc})
	if p.filtering || m.picker == nil {
		t.Fatal("escape should leave filter first")
	}
	m.key(keys.Key{Special: keys.Esc})
	if m.picker != nil || m.quit {
		t.Fatal("escape should return to document")
	}
}

func TestClosingLastDocumentDeletesItsIDs(t *testing.T) {
	m, out := testModel(t, 100)
	id := addTab(m, "/one.pdf")
	key := cache.Key{Path: "/one.pdf", Page: 1}
	m.cache.Put(key, []byte("png"))
	record := m.image(key, true)
	record.sent = true
	copyID := m.tabs.Duplicate(id)
	m.state(copyID)
	m.command(keys.Command{Action: keys.CloseTab})
	if len(out.deleted) != 0 || !m.cache.Contains(key) {
		t.Fatal("closing duplicate deleted shared image")
	}
	m.command(keys.Command{Action: keys.CloseTab})
	if len(out.deleted) != 1 || out.deleted[0] != record.id || m.cache.Contains(key) {
		t.Fatal("closing document did not delete its image")
	}
}

func TestRotatedLayoutAndTextWidth(t *testing.T) {
	info := pdf.DocumentInfo{PageWidthPoints: 800, PageHeightPoints: 600, PageRotationDegrees: 90}
	layout := layoutPage(terminal.Size{Columns: 100, Rows: 42, CellWidthPixels: 10, CellHeightPixels: 20}, info, tabs.View{Page: 1, Fit: tabs.FitPage, Zoom: 1}, false)
	if layout.Columns != 60 || layout.Rows != 40 || layout.X != 20 {
		t.Fatalf("rotated layout: %+v", layout)
	}
	if got := clipText("A界B", 3); got != "A界" {
		t.Fatalf("wide clipping: %q", got)
	}
	if got := cleanText("a\x1b[2J\nb"); got != "a [2J b" {
		t.Fatalf("control sanitization: %q", got)
	}
}

func TestLargePaneUsesSupportedPlaceholderGrid(t *testing.T) {
	size := terminal.Size{Columns: 600, Rows: 700, CellWidthPixels: 9, CellHeightPixels: 21}
	layout := layoutPage(size, pdf.DocumentInfo{PageWidthPoints: 600, PageHeightPoints: 800}, tabs.View{Page: 1, Fit: tabs.FitWidth, Zoom: 2}, true)
	if layout.Columns > imageout.MaxGridSize || layout.Rows > imageout.MaxGridSize {
		t.Fatalf("unsupported placeholder grid: %+v", layout)
	}
	if layout.X != (size.Columns-layout.Columns)/2 || layout.Y != 1+(size.Rows-2-layout.Rows)/2 {
		t.Fatalf("large grid not centered: %+v", layout)
	}
}

func readyPage(m *model, key cache.Key) {
	m.cache.Put(key, []byte("png"))
	m.currentKey, m.currentPNG = key, []byte("png")
	m.layout = pageLayout{Columns: 10, Rows: 10}
}

func TestConfirmedUploadsAreSingleFlightAndBounded(t *testing.T) {
	m, out := testModel(t, 100)
	addTab(m, "/one.pdf")
	first, second := cache.Key{Path: "/one.pdf", Page: 1}, cache.Key{Path: "/one.pdf", Page: 2}
	readyPage(m, first)
	m.ensureUpload()
	id := m.pendingUpload.id
	m.drawPage()
	if out.sent != 1 || out.cells != 0 {
		t.Fatal("unacknowledged image was drawn")
	}
	readyPage(m, second)
	m.ensureUpload()
	if out.sent != 1 {
		t.Fatal("second image uploaded before first reply")
	}
	if m.graphicsReply(terminal.GraphicsReplyEvent{ID: id + 100, OK: true}) {
		t.Fatal("accepted unrelated reply")
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: id, OK: true})
	if !m.imageIDs[first].sent {
		t.Fatal("reply did not confirm cached data")
	}
	m.ensureUpload()
	for range 2 {
		m.uploadExpired(m.pendingUpload.deadline)
	}
	if out.sent != 4 || m.pendingUpload.attempts != 3 {
		t.Fatalf("resends: %+v, sent=%d", m.pendingUpload, out.sent)
	}
	m.uploadExpired(m.pendingUpload.deadline)
	if m.pendingUpload != nil || !m.imageIDs[second].failed {
		t.Fatal("missing replies did not terminate")
	}
	m.ensureUpload()
	if out.sent != 4 {
		t.Fatal("failed upload retried without explicit action")
	}
	m.command(keys.Command{Action: keys.Redraw})
	m.ensureUpload()
	if out.sent != 5 || m.pendingUpload == nil {
		t.Fatal("forced redraw did not re-upload")
	}
}

func TestObsoleteUploadIsNotResent(t *testing.T) {
	m, out := testModel(t, 100)
	addTab(m, "/one.pdf")
	readyPage(m, cache.Key{Path: "/one.pdf", Page: 1})
	m.ensureUpload()
	oldID := m.pendingUpload.id
	readyPage(m, cache.Key{Path: "/one.pdf", Page: 2})
	m.uploadExpired(m.pendingUpload.deadline)
	if m.pendingUpload != nil || out.sent != 1 || len(out.deleted) != 1 || out.deleted[0] != oldID {
		t.Fatal("obsolete upload was resent or retained")
	}
	m.ensureUpload()
	if out.sent != 2 {
		t.Fatal("next visible upload did not start")
	}
}

func TestPendingKeysOnlyRedrawStatus(t *testing.T) {
	m, out := testModel(t, 100)
	addTab(m, "/one.pdf")
	readyPage(m, cache.Key{Path: "/one.pdf", Page: 1})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	if out.cells != 0 {
		t.Fatal("cells drawn before reply")
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: m.pendingUpload.id, OK: true})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	screen := m.screen.(*fakeScreen)
	screen.Reset()
	feed(m, "12")
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	if out.sent != 1 || out.cells != 1 {
		t.Fatalf("pending count retransmitted page: sent=%d cells=%d", out.sent, out.cells)
	}
	if screen.Len() > 512 {
		t.Fatalf("status-only update emitted %d bytes", screen.Len())
	}
	m.key(keys.Key{Rune: 'l', Ctrl: true})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	if out.sent != 2 || out.cells != 1 {
		t.Fatal("Ctrl-l failed to re-upload or drew before acknowledgment")
	}
}

func TestTerminalUploadErrorCanRecover(t *testing.T) {
	m, out := testModel(t, 100)
	addTab(m, "/one.pdf")
	readyPage(m, cache.Key{Path: "/one.pdf", Page: 1})
	m.ensureUpload()
	id := m.pendingUpload.id
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: id, Message: "EINVAL: incomplete image"})
	m.uploadExpired(m.pendingUpload.deadline)
	if out.sent != 2 || m.pendingUpload.attempts != 2 || out.cells != 0 {
		t.Fatal("terminal error did not retry without drawing cells")
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: id, OK: true, Message: "OK"})
	m.drawPage()
	m.ensureUpload()
	if m.pendingUpload != nil || out.sent != 2 || out.cells != 1 {
		t.Fatal("successful retry did not confirm data")
	}
}

func TestDuplicateSharesConfirmedTerminalImage(t *testing.T) {
	m, out := testModel(t, 100)
	addTab(m, "/one.pdf")
	readyPage(m, cache.Key{Path: "/one.pdf", Page: 1})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: m.pendingUpload.id, OK: true})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	m.command(keys.Command{Action: keys.DuplicateTab})
	if err := m.draw(); err != nil {
		t.Fatal(err)
	}
	if out.sent != 1 || out.cells != 2 {
		t.Fatalf("duplicate failed to share ID: sent=%d cells=%d", out.sent, out.cells)
	}
}

func TestResizeRecentersReusableRender(t *testing.T) {
	m, _ := testModel(t, 100)
	id := addTab(m, "/one.pdf")
	m.layout = m.activeLayout()
	m.requested, m.requestedTab, m.requestedKey = true, id, renderKey(m.layout.Request, m.docs["/one.pdf"].mtime)
	oldX := m.layout.X
	m.size.Columns += 2
	m.prepareRender()
	if m.layout.X != oldX+1 || m.generation != 0 {
		t.Fatalf("resize failed to reposition reusable render: %+v generation=%d", m.layout, m.generation)
	}
}
