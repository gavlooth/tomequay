package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"tomequay/internal/keys"
	"tomequay/internal/tabs"
	"tomequay/internal/terminal"
)

type textGlyph struct {
	start, end, column, width int
}

type textLine struct {
	start, end, width int
	glyphs            []textGlyph
}

type textCursor struct{ line, glyph int }

type textView struct {
	tab                 tabs.TabID
	path                string
	page                int
	mtime               int64
	cancel              context.CancelFunc
	loading             bool
	empty               bool
	text                string
	lines               []textLine
	cursor              textCursor
	anchor              int
	selecting, dragging bool
	top, left           int
	message             string
}

type textResult struct {
	view *textView
	text string
	err  error
}

func (m *model) showText() {
	t, ok := m.tabs.Active()
	if !ok {
		return
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		return
	}
	m.closePageSelection()
	m.closeText()
	ctx, cancel := context.WithCancel(m.ctx)
	p := &textView{tab: t.ID, path: t.Document.Path, page: t.View.Page, mtime: d.mtime, cancel: cancel, loading: true}
	m.textView, m.drag, m.message = p, nil, ""
	password := d.password
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		text, err := m.tools.PageText(ctx, p.path, password, p.page)
		select {
		case m.textResults <- textResult{p, text, err}:
		case <-ctx.Done():
		}
	}()
}

func (m *model) closeText() {
	if p := m.textView; p != nil {
		p.cancel()
		m.textView = nil
	}
}

func (m *model) acceptText(r textResult) {
	p := m.textView
	if p == nil || p != r.view {
		return
	}
	t, ok := m.tabs.Active()
	d := m.docs[p.path]
	if !ok || t.ID != p.tab || t.View.Page != p.page || d == nil || !d.ready || d.mtime != p.mtime {
		m.closeText()
		return
	}
	p.loading = false
	if r.err != nil {
		p.message = "Text extraction failed: " + r.err.Error()
		return
	}
	p.setText(r.text)
	if p.empty {
		p.message = "No selectable text; this page may need OCR"
	}
}

func (p *textView) setText(text string) {
	p.text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	p.lines = nil
	p.empty = strings.TrimSpace(p.text) == ""
	line := textLine{}
	for offset, r := range p.text {
		if r == '\n' {
			line.end = offset
			p.lines = append(p.lines, line)
			line = textLine{start: offset + 1}
			continue
		}
		width := runeWidth(r)
		if r == '\t' {
			width = 8 - line.width%8
		}
		end := offset + utf8.RuneLen(r)
		if width == 0 && len(line.glyphs) > 0 {
			line.glyphs[len(line.glyphs)-1].end = end
			continue
		}
		line.glyphs = append(line.glyphs, textGlyph{offset, end, line.width, width})
		line.width += width
	}
	line.end = len(p.text)
	p.lines = append(p.lines, line)
}

func (p *textView) offset(c textCursor) int {
	line := p.lines[c.line]
	if c.glyph == len(line.glyphs) {
		return line.end
	}
	return line.glyphs[c.glyph].start
}

func (p *textView) selection() (int, int) {
	if !p.selecting || len(p.lines) == 0 {
		return 0, 0
	}
	start, end := p.anchor, p.offset(p.cursor)
	if start > end {
		start, end = end, start
	}
	// Include the character at the far end, including a newline at line end.
	if end < len(p.text) {
		for _, line := range p.lines {
			if end == line.end {
				return start, end + 1
			}
			if end < line.start || end > line.end {
				continue
			}
			for _, glyph := range line.glyphs {
				if glyph.start == end {
					return start, glyph.end
				}
			}
		}
	}
	return start, end
}

func (p *textView) column() int {
	line := p.lines[p.cursor.line]
	if p.cursor.glyph == len(line.glyphs) {
		return line.width
	}
	return line.glyphs[p.cursor.glyph].column
}

func (p *textView) atColumn(line, column int) textCursor {
	line = max(0, min(line, len(p.lines)-1))
	for i, g := range p.lines[line].glyphs {
		if column < g.column+g.width {
			return textCursor{line, i}
		}
	}
	return textCursor{line, len(p.lines[line].glyphs)}
}

func (p *textView) reveal(width, height int) {
	if len(p.lines) == 0 {
		return
	}
	width, height = max(1, width), max(1, height)
	p.top = min(p.top, p.cursor.line)
	p.top = max(p.top, p.cursor.line-height+1)
	p.top = max(0, min(p.top, max(0, len(p.lines)-height)))
	column := p.column()
	cursorWidth := 1
	if p.cursor.glyph < len(p.lines[p.cursor.line].glyphs) {
		cursorWidth = max(1, p.lines[p.cursor.line].glyphs[p.cursor.glyph].width)
	}
	p.left = max(0, min(p.left, column))
	p.left = max(p.left, column+cursorWidth-width)
}

func (m *model) textKey(k keys.Key) {
	p := m.textView
	if k.Special == keys.Esc || k.Rune == 'q' && !k.Ctrl {
		m.closeText()
		return
	}
	if p.loading || len(p.lines) == 0 || p.empty {
		return
	}
	height := max(1, m.size.Rows-4)
	column := p.column()
	switch {
	case k.Ctrl && (k.Rune == 'd' || k.Rune == 'u'):
		delta := max(1, height/2)
		if k.Rune == 'u' {
			delta = -delta
		}
		p.cursor = p.atColumn(p.cursor.line+delta, column)
	case k.Special == keys.Down || !k.Ctrl && k.Rune == 'j':
		p.cursor = p.atColumn(p.cursor.line+1, column)
	case k.Special == keys.Up || !k.Ctrl && k.Rune == 'k':
		p.cursor = p.atColumn(p.cursor.line-1, column)
	case k.Special == keys.Left || !k.Ctrl && k.Rune == 'h':
		if p.cursor.glyph > 0 {
			p.cursor.glyph--
		} else if p.cursor.line > 0 {
			p.cursor.line--
			p.cursor.glyph = len(p.lines[p.cursor.line].glyphs)
		}
	case k.Special == keys.Right || !k.Ctrl && k.Rune == 'l':
		if p.cursor.glyph < len(p.lines[p.cursor.line].glyphs) {
			p.cursor.glyph++
		} else if p.cursor.line+1 < len(p.lines) {
			p.cursor.line++
			p.cursor.glyph = 0
		}
	case k.Special == keys.Home || !k.Ctrl && k.Rune == '0':
		p.cursor.glyph = 0
	case k.Special == keys.End || !k.Ctrl && k.Rune == '$':
		p.cursor.glyph = len(p.lines[p.cursor.line].glyphs)
	case k.Special == keys.PageDown || k.Special == keys.PageUp:
		delta := height
		if k.Special == keys.PageUp {
			delta = -delta
		}
		p.cursor = p.atColumn(p.cursor.line+delta, column)
	case !k.Ctrl && k.Rune == 'v':
		p.selecting = !p.selecting
		p.anchor = p.offset(p.cursor)
	case !k.Ctrl && k.Rune == 'a':
		p.selecting, p.anchor = true, 0
		last := len(p.lines) - 1
		p.cursor = textCursor{last, len(p.lines[last].glyphs)}
	case !k.Ctrl && k.Rune == 'y':
		start, end := p.selection()
		if start == end {
			p.message = "Select text with v or mouse-drag first"
			return
		}
		if err := terminal.WriteClipboard(m.screen.Writer(), p.text[start:end], terminal.InTmux()); err != nil {
			p.message = "Copy failed: " + err.Error()
		} else {
			p.message = "Selection sent to terminal clipboard (OSC 52)"
		}
	}
	p.reveal(m.size.Columns, height)
}

func (m *model) textMouse(e terminal.MouseEvent) {
	p := m.textView
	if p.loading || len(p.lines) == 0 || p.empty {
		return
	}
	height := max(1, m.size.Rows-4)
	if e.Kind == terminal.MouseWheelUp || e.Kind == terminal.MouseWheelDown {
		delta := -3
		if e.Kind == terminal.MouseWheelDown {
			delta = 3
		}
		p.top = max(0, min(p.top+delta, max(0, len(p.lines)-height)))
		return
	}
	if e.Kind == terminal.MouseRelease {
		p.dragging = false
		return
	}
	if e.Button != terminal.ButtonLeft || e.Kind != terminal.MousePress && e.Kind != terminal.MouseDrag {
		return
	}
	if e.Kind == terminal.MousePress {
		if e.Row < 2 || e.Row >= m.size.Rows-2 || e.Column < 0 || e.Column >= m.size.Columns {
			return
		}
	} else if !p.dragging {
		return
	}
	if e.Row < 2 {
		p.top = max(0, p.top-1)
	}
	if e.Row >= m.size.Rows-2 {
		p.top = min(max(0, len(p.lines)-height), p.top+1)
	}
	line := p.top + max(0, min(e.Row-2, height-1))
	p.cursor = p.atColumn(line, max(0, min(e.Column, m.size.Columns-1))+p.left)
	if e.Kind == terminal.MousePress {
		p.selecting, p.dragging, p.anchor = true, true, p.offset(p.cursor)
	}
}

func (m *model) drawText() {
	p := m.textView
	t := m.theme
	header := m.row(1)
	header.add(fmt.Sprintf(" TEXT: %s · page %d ", filepath.Base(p.path), p.page), textStyle{fg: t.Cyan, bg: t.Background, bold: true})
	header.finish()
	height := max(1, m.size.Rows-4)
	p.top = max(0, min(p.top, max(0, len(p.lines)-height)))
	start, end := p.selection()
	for row := range height {
		r := m.row(row + 2)
		index := p.top + row
		if index < len(p.lines) {
			line := p.lines[index]
			for i, g := range line.glyphs {
				if g.column+g.width <= p.left {
					continue
				}
				if g.column >= p.left+m.size.Columns {
					break
				}
				style := t.plain()
				if g.start >= start && g.start < end {
					style = textStyle{fg: t.Background, bg: t.Blue}
				}
				if index == p.cursor.line && i == p.cursor.glyph {
					style = textStyle{fg: t.Background, bg: t.Yellow}
				}
				visibleWidth := min(g.column+g.width, p.left+m.size.Columns) - max(g.column, p.left)
				s := p.text[g.start:g.end]
				if s[0] == '\t' || g.column < p.left || visibleWidth < g.width {
					s = strings.Repeat(" ", visibleWidth)
				}
				r.add(s, style)
			}
			if line.width >= p.left && line.width < p.left+m.size.Columns {
				style := t.plain()
				if line.end >= start && line.end < end {
					style = textStyle{fg: t.Background, bg: t.Blue}
				}
				if index == p.cursor.line && p.cursor.glyph == len(line.glyphs) {
					style = textStyle{fg: t.Background, bg: t.Yellow}
				}
				r.add(" ", style)
			}
		}
		r.finish()
	}
	footer := m.row(m.size.Rows - 2)
	message := p.message
	if p.loading {
		message = "Extracting page text… Esc cancels"
	}
	if message == "" {
		message = "h/j/k/l move · v select · drag select · a all · y copy · Esc PDF"
	}
	footer.add(message, t.muted())
	footer.finish()
}
