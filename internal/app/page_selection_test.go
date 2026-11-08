package app

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tomequay/internal/cache"
	"tomequay/internal/keys"
	"tomequay/internal/pdf"
	"tomequay/internal/terminal"
)

func pageSelectionFixture(t *testing.T, rotation int) (string, pdf.Tools, pdf.PageWords) {
	t.Helper()
	tools, err := pdf.FindTools()
	if err != nil {
		t.Skip(err)
	}
	content := "BT /F1 14 Tf 40 540 Td (Alpha beta gamma) Tj 0 -30 Td (Second line) Tj ET\n"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [4 0 R] /Count 1 >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 600] /Rotate %d /Resources << /Font << /F1 3 0 R >> >> /Contents 5 0 R >>", rotation), fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content)}
	var data bytes.Buffer
	data.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = data.Len()
		fmt.Fprintf(&data, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := data.Len()
	fmt.Fprintf(&data, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&data, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&data, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	path := filepath.Join(t.TempDir(), "selection.pdf")
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	words, err := tools.PageWords(context.Background(), path, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	return path, tools, words
}

func TestPageSelectionCopiesWordsAndLineBreaks(t *testing.T) {
	p := &pageSelection{words: pdf.PageWords{Words: []pdf.Word{{Text: "Alpha", Line: 0}, {Text: "beta", Line: 0}, {Text: "Second", Line: 1}, {Text: "line", Line: 1}}}}
	if got := p.selectedText(); got != "Alpha" {
		t.Fatalf("cursor word=%q", got)
	}
	p.selecting, p.anchor, p.cursor = true, 3, 1
	if got := p.selectedText(); got != "beta\nSecond line" {
		t.Fatalf("reverse multiline=%q", got)
	}
	p.words.Words = nil
	if got := p.selectedText(); got != "" {
		t.Fatalf("empty=%q", got)
	}
}

func TestPageSelectionMouseTracksZoomCropAndRotation(t *testing.T) {
	for _, rotation := range []int{0, 90, 180, 270} {
		t.Run(fmt.Sprint(rotation), func(t *testing.T) {
			path, _, words := pageSelectionFixture(t, rotation)
			m, _ := testModel(t, 1024)
			id := addTab(m, path)
			p := &pageSelection{tab: id, path: path, page: 1, mtime: 1, words: words, cancel: func() {}}
			m.pageSelection = p
			request := pdf.RenderRequest{Page: 1, WidthPixels: 600, HeightPixels: 1200, Zoom: 2}
			w, h := words.PixelSize(request)
			word := words.Words[1]
			centerX := (word.XMin + word.XMax) / 2 / words.Width * float64(w)
			centerY := (word.YMin + word.YMax) / 2 / words.Height * float64(h)
			request.CropX, request.CropY = max(0, int(centerX)-100), max(0, int(centerY)-100)
			request.CropWidth, request.CropHeight = 200, 200
			m.layout = pageLayout{X: 5, Y: 2, Columns: 20, Rows: 20, FullWidth: w, FullHeight: h, VisibleWidth: 200, VisibleHeight: 200, Request: request}
			m.currentPNG = []byte("rendered page")
			column := 5 + int(math.Floor((centerX-float64(request.CropX))/10))
			row := 2 + int(math.Floor((centerY-float64(request.CropY))/10))
			m.mouse(terminal.MouseEvent{Kind: terminal.MousePress, Button: terminal.ButtonLeft, Column: column, Row: row})
			m.mouse(terminal.MouseEvent{Kind: terminal.MouseRelease, Button: terminal.ButtonLeft, Column: column, Row: row})
			if got := p.selectedText(); got != word.Text {
				t.Fatalf("hit=%q want=%q", got, word.Text)
			}
		})
	}
}

func TestSelectionModeSwitchesWithoutRemovingTextView(t *testing.T) {
	path, tools, _ := pageSelectionFixture(t, 0)
	m, _ := testModel(t, 1024)
	id := addTab(m, path)
	m.tools = tools
	t.Cleanup(m.stopWorkers)
	before, _ := m.tabs.View(id)
	feed(m, ":selection-mode page")
	m.key(keys.Key{Special: keys.Enter})
	feed(m, "v")
	if m.pageSelection == nil || m.textView != nil {
		t.Fatal("configured page mode did not retain PDF")
	}
	m.acceptPageWords(<-m.pageWordsResults)
	m.key(keys.Key{Special: keys.Esc})
	feed(m, ":text")
	m.key(keys.Key{Special: keys.Enter})
	if m.textView == nil || m.pageSelection != nil {
		t.Fatal("explicit text view unavailable in page mode")
	}
	m.acceptText(<-m.textResults)
	if !strings.Contains(m.textView.text, "Alpha beta gamma") {
		t.Fatalf("text=%q", m.textView.text)
	}
	m.key(keys.Key{Special: keys.Esc})
	m.setSelectionMode("invalid")
	if m.selectionMode != "page" {
		t.Fatal("invalid mode changed preference")
	}
	m.setSelectionMode("text")
	feed(m, "v")
	if m.textView == nil {
		t.Fatal("text mode not restored")
	}
	m.closeText()
	after, _ := m.tabs.View(id)
	if before != after {
		t.Fatal("switching selection modes changed PDF position")
	}
}

func TestSelectionImageWaitsForAcknowledgementAndRestoresBase(t *testing.T) {
	m, out := testModel(t, 1024)
	id := addTab(m, "/one.pdf")
	key := cache.Key{Path: "/one.pdf", Page: 1}
	m.currentKey, m.currentPNG = key, []byte("base")
	m.cache.Put(key, m.currentPNG)
	m.layout = pageLayout{Columns: 10, Rows: 20}
	base := m.image(key, true)
	base.sent = true
	p := &pageSelection{tab: id, path: "/one.pdf", page: 1, mtime: 1, cancel: func() {}, highlightKey: key, highlightPNG: []byte("highlight"), displayVersion: 7}
	m.pageSelection = p
	m.ensureUpload()
	u := m.pendingUpload
	if u == nil || u.replacement == nil || m.imageIDs[key] != base || !base.sent {
		t.Fatal("highlight replaced visible image before acknowledgement")
	}
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: u.id, OK: true})
	if m.imageIDs[key].id != u.id || !m.imageIDs[key].sent {
		t.Fatal("acknowledged highlight not displayed")
	}
	if data, _ := m.cache.Get(key); string(data) != "base" {
		t.Fatal("highlight contaminated shared page cache")
	}
	p.displayVersion++
	p.highlightPNG = []byte("new range")
	m.ensureUpload()
	next := m.pendingUpload
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: next.id, OK: true})
	if m.imageIDs[key].original != base || base.original != nil {
		t.Fatal("moving selection lost the original image")
	}
	m.closePageSelection()
	if m.imageIDs[key] != base || !base.sent || m.pendingUpload != nil {
		t.Fatal("closing selection did not restore the already uploaded original image")
	}
	if len(out.deleted) != 2 || out.deleted[0] != u.id || out.deleted[1] != next.id {
		t.Fatalf("closing selection deleted the wrong images: %v", out.deleted)
	}
}

func TestObsoleteSelectionUploadCannotReturnAfterEscape(t *testing.T) {
	m, _ := testModel(t, 1024)
	addTab(m, "/one.pdf")
	key := cache.Key{Path: "/one.pdf", Page: 1}
	m.currentKey, m.currentPNG = key, []byte("base")
	base := m.image(key, false)
	base.sent = true
	m.pageSelection = &pageSelection{cancel: func() {}, highlightKey: key, highlightPNG: []byte("selected"), displayVersion: 3}
	m.ensureUpload()
	id := m.pendingUpload.id
	m.closePageSelection()
	m.graphicsReply(terminal.GraphicsReplyEvent{ID: id, OK: true})
	if m.pendingUpload != nil || m.imageIDs[key] != base {
		t.Fatal("late highlight returned after Escape")
	}
}

func TestStaleSelectionImageCannotReplaceNewRange(t *testing.T) {
	m, _ := testModel(t, 1024)
	p := &pageSelection{version: 5, highlightRunning: true}
	m.pageSelection = p
	m.currentKey = cache.Key{Path: "/one.pdf", Page: 1}
	m.acceptSelectionImage(selectionImageResult{selection: p, key: m.currentKey, version: 4, png: []byte("old range")})
	if p.highlightRunning || len(p.highlightPNG) != 0 {
		t.Fatal("obsolete range became visible")
	}
}
