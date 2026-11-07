package app

import (
	"fmt"
	"strings"
	"testing"

	"terminal-pdf-reader/internal/keys"
)

func TestEllipsisRetainsNameSuffixByCells(t *testing.T) {
	for _, tt := range []struct {
		name    string
		columns int
		ascii   bool
		want    string
	}{
		{"notes.pdf", 9, false, "notes.pdf"},
		{"long-notes.pdf", 10, false, "…notes.pdf"},
		{"long-notes.pdf", 10, true, "...tes.pdf"},
		{"界界.pdf", 7, false, "…界.pdf"},
		{"cafe\u0301.pdf", 6, false, "…e\u0301.pdf"},
		{"a\ue0b0\uf1c1.pdf", 7, false, "a\ue0b0\uf1c1.pdf"},
		{"a\U000f02d1.pdf", 5, false, "….pdf"},
		{"界界", 2, false, "…"},
		{"notes.pdf", 1, false, "…"},
		{"notes.pdf", 2, true, ".."},
		{"notes.pdf", 0, false, ""},
	} {
		t.Run(fmt.Sprintf("%s/%d/%t", tt.name, tt.columns, tt.ascii), func(t *testing.T) {
			got := ellipsisText(tt.name, tt.columns, tt.ascii)
			if got != tt.want || textWidth(got) > tt.columns {
				t.Fatalf("truncated = %q (%d cells), want %q within %d cells", got, textWidth(got), tt.want, tt.columns)
			}
		})
	}
	for _, tt := range []struct {
		text string
		want int
	}{
		{"界A", 3}, {"e\u0301", 1}, {"\ue0b0\ue0b2\uf1c1\U000f02d1", 4}, {"█▉░", 3}, {"😀", 2},
	} {
		if got := textWidth(tt.text); got != tt.want {
			t.Errorf("width(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
}

func TestProgressCellsAndPartialBars(t *testing.T) {
	for _, tt := range []struct {
		page, pages, width, full, part int
	}{
		{0, 0, 8, 0, 0}, {0, 10, 8, 0, 0}, {1, 10, 8, 0, 6},
		{5, 10, 8, 4, 0}, {10, 10, 8, 8, 0}, {11, 10, 16, 16, 0},
		{-1, 10, 8, 0, 0}, {5, 10, 0, 0, 0}, {1, 128, 16, 0, 1},
	} {
		full, part := progressCells(tt.page, tt.pages, tt.width)
		if full != tt.full || part != tt.part {
			t.Errorf("progress(%d/%d, %d) = %d,%d; want %d,%d", tt.page, tt.pages, tt.width, full, part, tt.full, tt.part)
		}
	}
	if got := progressBar(1, 10, 8, false); got != "▊░░░░░░░" {
		t.Errorf("partial bar = %q", got)
	}
	if got := progressBar(1, 10, 8, true); got != "+-------" {
		t.Errorf("ASCII partial bar = %q", got)
	}
}

func TestTabWindowKeepsActiveVisible(t *testing.T) {
	for _, tt := range []struct {
		widths                      []int
		active, columns, start, end int
	}{
		{nil, 0, 20, 0, 0},
		{[]int{10, 10, 10}, 1, 30, 0, 3},
		{[]int{10, 10, 10, 10}, 3, 20, 2, 4},
		{[]int{10, 10, 10, 10}, 0, 20, 0, 2},
		{[]int{10, 30, 10}, 1, 20, 1, 2},
		{[]int{10, 10}, 1, 0, 0, 0},
		{[]int{9, 12, 10, 8}, 2, 22, 1, 3},
	} {
		start, end := tabWindow(tt.widths, tt.active, tt.columns)
		if start != tt.start || end != tt.end {
			t.Errorf("window(%v, %d, %d) = [%d,%d), want [%d,%d)", tt.widths, tt.active, tt.columns, start, end, tt.start, tt.end)
		}
	}
}

func TestPromptCursorFollowsVisibleWideInput(t *testing.T) {
	m, _ := testModel(t, 100)
	addTab(m, "/one.pdf")
	m.size.Columns = 30
	feed(m, "/a界e\u0301")
	m.drawStatus()
	out := m.screen.(*fakeScreen).String()
	if !strings.HasSuffix(out, "\x1b[42;16H\x1b[?25h") {
		t.Fatalf("prompt cursor not after display cells: %q", out)
	}
	m.screen.(*fakeScreen).Reset()
	feed(m, strings.Repeat("界", 30))
	m.drawStatus()
	out = m.screen.(*fakeScreen).String()
	if !strings.HasSuffix(out, "\x1b[42;30H\x1b[?25h") && !strings.HasSuffix(out, "\x1b[42;29H\x1b[?25h") {
		t.Fatalf("long prompt cursor not on its visible tail: %q", out)
	}
	m.key(keys.Key{Special: keys.Esc})
	m.screen.(*fakeScreen).Reset()
	m.drawStatus()
	if !strings.HasSuffix(m.screen.(*fakeScreen).String(), "\x1b[?25l") {
		t.Fatal("normal mode left the prompt cursor visible")
	}
	m.screen.(*fakeScreen).Reset()
	feed(m, ":a界e\u0301")
	m.drawStatus()
	if out := m.screen.(*fakeScreen).String(); !strings.HasSuffix(out, "\x1b[42;17H\x1b[?25h") {
		t.Fatalf("command cursor not after display cells: %q", out)
	}
}

func TestASCIIPanelAndDuplicateTabLabels(t *testing.T) {
	m, _ := testModel(t, 100)
	m.theme.NoGlyphs = true
	first := addTab(m, "/long-file.pdf")
	m.tabs.SetPage(first, 12, 0)
	second := m.tabs.Duplicate(first)
	m.tabs.SetPage(second, 20, 0)
	m.drawTabs()
	out := m.screen.(*fakeScreen).String()
	if !strings.Contains(out, "long-file.pdf:12") || !strings.Contains(out, "long-file.pdf:20") {
		t.Fatalf("duplicate tabs lack independent pages: %q", out)
	}
	m.screen.(*fakeScreen).Reset()
	long := addTab(m, "/a-very-long-distinctive-file-name.pdf")
	m.tabs.SetPage(long, 12, 0)
	copy := m.tabs.Duplicate(long)
	m.tabs.SetPage(copy, 20, 0)
	m.size.Columns = 36
	m.drawTabs()
	out = m.screen.(*fakeScreen).String()
	if !strings.Contains(out, "4 ...") || !strings.Contains(out, ".pdf:20") || !strings.Contains(out, "<4/4>") {
		t.Fatalf("narrow tab lost its number, suffix, or overflow count: %q", out)
	}
	m.size.Columns = 100
	m.screen.(*fakeScreen).Reset()
	m.drawHelp()
	out = m.screen.(*fakeScreen).String()
	for _, r := range out {
		if r > 127 {
			t.Fatalf("ASCII help contains decorative glyph %U", r)
		}
	}
	if !strings.Contains(out, "Navigation") || !strings.Contains(out, "Zoom") || !strings.Contains(out, "\x1b[0m\x1b[K") {
		t.Fatalf("help lacks grouped rows or clean row endings: %q", out)
	}
}
