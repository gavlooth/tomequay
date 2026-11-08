package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tomequay/internal/keys"
	"tomequay/internal/terminal"
)

func selectedText(p *textView) string {
	start, end := p.selection()
	return p.text[start:end]
}

func TestTextSelectionUnicodeTabsAndReverse(t *testing.T) {
	p := &textView{}
	p.setText("A\te\u0301界\nnext")
	if got := p.atColumn(0, 3); got.glyph != 1 {
		t.Fatalf("tab hit = %+v", got)
	}
	if got := p.atColumn(0, 10); got.glyph != 3 {
		t.Fatalf("wide character hit = %+v", got)
	}
	p.selecting, p.anchor, p.cursor = true, p.lines[0].glyphs[2].start, textCursor{0, 3}
	if got := selectedText(p); got != "e\u0301界" {
		t.Fatalf("forward = %q", got)
	}
	p.anchor, p.cursor = p.lines[0].glyphs[3].start, textCursor{0, 2}
	if got := selectedText(p); got != "e\u0301界" {
		t.Fatalf("reverse = %q", got)
	}
	p.anchor, p.cursor = p.lines[0].glyphs[2].start, textCursor{1, 1}
	if got := selectedText(p); got != "e\u0301界\nne" {
		t.Fatalf("multiline = %q", got)
	}
}

func TestTextKeyboardSelectionKeepsPDFPosition(t *testing.T) {
	m, _ := testModel(t, 1024)
	id := addTab(m, "/one.pdf")
	m.tabs.SetPage(id, 12, 0.4)
	before, _ := m.tabs.View(id)
	ctx, cancel := context.WithCancel(context.Background())
	p := &textView{cancel: cancel}
	p.setText("first line\nsecond line\n")
	m.textView = p
	feed(m, "vllll")
	if got := selectedText(p); got != "first" {
		t.Fatalf("keyboard = %q", got)
	}
	feed(m, "vla")
	if got := selectedText(p); got != p.text {
		t.Fatalf("all = %q", got)
	}
	m.key(keys.Key{Special: keys.Esc})
	after, _ := m.tabs.View(id)
	if before != after || m.textView != nil {
		t.Fatalf("PDF changed: before=%+v after=%+v", before, after)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("closing text left extraction running")
	}
}

func TestTextMouseSelectionAndScroll(t *testing.T) {
	m, _ := testModel(t, 1024)
	id := addTab(m, "/one.pdf")
	before, _ := m.tabs.View(id)
	m.size.Columns, m.size.Rows = 24, 8
	p := &textView{cancel: func() {}}
	p.setText("alpha beta\nsecond\nthird\nfourth\nfifth\nsixth")
	m.textView = p
	m.mouse(terminal.MouseEvent{Kind: terminal.MousePress, Button: terminal.ButtonLeft, Column: 4, Row: 2})
	m.mouse(terminal.MouseEvent{Kind: terminal.MouseDrag, Button: terminal.ButtonLeft, Column: 0, Row: 2})
	m.mouse(terminal.MouseEvent{Kind: terminal.MouseRelease, Button: terminal.ButtonLeft, Column: 0, Row: 2})
	if got := selectedText(p); got != "alpha" || p.dragging {
		t.Fatalf("mouse = %q; dragging=%v", got, p.dragging)
	}
	m.mouse(terminal.MouseEvent{Kind: terminal.MouseWheelDown})
	if p.top != 2 {
		t.Fatalf("scroll top=%d", p.top)
	}
	after, _ := m.tabs.View(id)
	if before != after {
		t.Fatal("text wheel scrolled PDF")
	}
}

func TestTextResultsRejectClosedOrChangedPage(t *testing.T) {
	m, _ := testModel(t, 1024)
	id := addTab(m, "/one.pdf")
	old := &textView{tab: id, path: "/one.pdf", page: 1, mtime: 1, cancel: func() {}, loading: true}
	current := &textView{tab: id, path: "/one.pdf", page: 1, mtime: 1, cancel: func() {}, loading: true}
	m.textView = current
	m.acceptText(textResult{view: old, text: "old"})
	if !current.loading {
		t.Fatal("old result replaced new text view")
	}
	m.tabs.SetPage(id, 2, 0)
	m.acceptText(textResult{view: current, text: "wrong page"})
	if m.textView != nil {
		t.Fatal("changed page retained stale text")
	}
}

func TestTextEmptyErrorAndTerminalCommands(t *testing.T) {
	m, _ := testModel(t, 1024)
	id := addTab(m, "/one.pdf")
	p := &textView{tab: id, path: "/one.pdf", page: 1, mtime: 1, cancel: func() {}, loading: true}
	m.textView = p
	m.acceptText(textResult{view: p, text: " \n\t"})
	if !strings.Contains(p.message, "No selectable text") {
		t.Fatalf("empty message=%q", p.message)
	}
	m.acceptText(textResult{view: p, err: errors.New("bad PDF")})
	if !strings.Contains(p.message, "bad PDF") {
		t.Fatalf("error message=%q", p.message)
	}
	p.setText("safe\x1b]52;c;bad\a\nline\tend")
	if strings.ContainsAny(p.text, "\x1b\a") || !strings.Contains(p.text, "\nline\tend") {
		t.Fatalf("unsafe or lost whitespace: %q", p.text)
	}
}

func TestTextHorizontalScrollAndResizeKeepsWholeCharacters(t *testing.T) {
	p := &textView{}
	p.setText("0123456789界z")
	p.cursor = p.atColumn(0, 11)
	p.reveal(4, 2)
	if p.left != 8 {
		t.Fatalf("wide cursor left=%d", p.left)
	}
	p.reveal(2, 2)
	if p.left != 10 {
		t.Fatalf("resized cursor left=%d", p.left)
	}
	if p.offset(p.cursor) != 10 {
		t.Fatal("resize changed selected character")
	}
}
