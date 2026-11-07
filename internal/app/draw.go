package app

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"terminal-pdf-reader/internal/keys"
	"terminal-pdf-reader/internal/tabs"
)

func (m *model) text(x, y int, s string) {
	if y < 0 || y >= m.size.Rows || x < 0 || x >= m.size.Columns {
		return
	}
	_, err := fmt.Fprintf(m.screen.Writer(), "\x1b[%d;%dH%s", y+1, x+1, clipText(cleanText(s), m.size.Columns-x))
	m.noteOutput(err)
}
func (m *model) line(y int, s string) {
	if y < 0 || y >= m.size.Rows || m.size.Columns < 1 {
		return
	}
	_, err := fmt.Fprintf(m.screen.Writer(), "\x1b[%d;1H\x1b[0m%s\x1b[K", y+1, clipText(cleanText(s), m.size.Columns))
	m.noteOutput(err)
}
func clipText(s string, columns int) string {
	width := 0
	for i, r := range s {
		w := 1
		// CJK and emoji are normally two cells; combining marks do not advance.
		if r >= 0x300 && r <= 0x36f {
			w = 0
		} else if r >= 0x1100 && (r <= 0x115f || r >= 0x2e80 && r <= 0xa4cf || r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff || r >= 0xfe10 && r <= 0xfe6f || r >= 0xff01 && r <= 0xff60 || r >= 0x1f300) {
			w = 2
		}
		if width+w > columns {
			return s[:i]
		}
		width += w
	}
	return s
}
func (m *model) draw() error {
	w := m.screen.Writer()
	var bar strings.Builder
	active, _ := m.tabs.Active()
	if m.size.Columns >= 20 && m.size.Rows >= 8 && !m.help && m.picker == nil && m.passwordFor == nil {
		m.ensureUpload()
	}
	for i, t := range m.tabs.Tabs() {
		star := ""
		if active != nil && t.ID == active.ID {
			star = "*"
		}
		fmt.Fprintf(&bar, " %d%s:%s ", i+1, star, filepath.Base(t.Document.Path))
	}
	if bar.Len() == 0 {
		bar.WriteString(" pdfreader")
	}
	m.line(0, bar.String())
	next := drawnFrame{size: m.size}
	if active != nil {
		next.tab = active.ID
	}
	switch {
	case m.size.Columns < 20 || m.size.Rows < 8:
		next.kind = smallFrame
	case m.help || m.picker != nil:
		next.kind = overlayFrame
	case m.passwordFor == nil && len(m.currentPNG) > 0:
		r := m.image(m.currentKey, m.cache.Contains(m.currentKey))
		if r.sent {
			next.kind = pageFrame
			next.key = m.currentKey
			next.id = r.id
			next.x, next.y, next.columns, next.rows = m.layout.X, m.layout.Y, m.layout.Columns, m.layout.Rows
		}
	}
	// Count/prompt/status changes must not rewrite the placeholder grid. Apart
	// from wasted work, repeated grids can fill tmux's tty queue, which discards
	// graphics passthrough bytes even though it can reconstruct ordinary text.
	if next != m.frame || next.kind == overlayFrame {
		blank := strings.Repeat(" ", max(0, m.size.Columns))
		for row := 1; row < m.size.Rows-1; row++ {
			_, err := fmt.Fprintf(w, "\x1b[%d;1H\x1b[0m%s", row+1, blank)
			m.noteOutput(err)
		}
		switch next.kind {
		case smallFrame:
			m.text(0, max(1, m.size.Rows/2), "terminal too small")
		case pageFrame:
			m.drawPage()
		case overlayFrame:
			if m.help {
				m.drawHelp()
			} else {
				m.drawPicker()
			}
		}
		m.frame = next
	}
	m.line(m.size.Rows-1, m.status())
	_, err := io.WriteString(w, "\x1b[0m")
	m.noteOutput(err)
	// Exactly one flush belongs to each complete frame.
	m.noteOutput(m.screen.Flush())
	return m.outputErr
}
func (m *model) status() string {
	if m.passwordFor != nil {
		return "Password for " + filepath.Base(m.passwordFor.path) + ": " + strings.Repeat("*", len(m.password))
	}
	var parts []string
	if t, ok := m.tabs.Active(); ok {
		fit := "fit page"
		if t.View.Fit == tabs.FitWidth {
			fit = "fit width"
		} else if t.View.Fit == tabs.Custom {
			fit = fmt.Sprintf("zoom %.0f%%", t.View.Zoom*100)
		}
		parts = append(parts, fmt.Sprintf("page %d/%d | %s", t.View.Page, t.Document.PageCount, fit))
		s := m.state(t.ID)
		if len(s.searchPages) > 0 && s.searchIndex >= 0 {
			parts = append(parts, fmt.Sprintf("match %d/%d", s.searchIndex+1, len(s.searchPages)))
		}
	}
	if u := m.pendingUpload; u != nil && u.key == m.currentKey {
		if u.attempts == 1 {
			parts = append(parts, "sending image")
		} else {
			parts = append(parts, fmt.Sprintf("image retry %d/2", u.attempts-1))
		}
	}
	if m.picker != nil {
		p := m.picker
		if p.filtering {
			return "/" + p.filter
		}
		parts = append(parts, "j/k select  Enter open  h parent  / filter  Esc close")
	}
	if m.help {
		parts = append(parts, "help: j/k scroll; any other key closes")
	}
	if pending := m.decoder.Pending(); pending != "" {
		if m.decoder.Mode() != keys.Normal {
			return strings.Join(append(parts, pending), " | ")
		}
		parts = append(parts, pending)
	}
	if m.message != "" {
		parts = append(parts, m.message)
	}
	if len(parts) == 0 {
		return "o: open PDF | F1: help | q: quit"
	}
	return strings.Join(parts, " | ")
}
func (m *model) drawHelp() {
	help := keys.Help()
	height := m.size.Rows - 3
	m.helpOffset = min(m.helpOffset, max(0, len(help)-height))
	m.text(0, 1, "KEYS / COMMANDS")
	for i := range height {
		index := i + m.helpOffset
		if index >= len(help) {
			break
		}
		e := help[index]
		m.text(1, i+2, fmt.Sprintf("%-18s %s", e.Keys, e.Description))
	}
}
func (m *model) drawPicker() {
	p := m.picker
	m.text(0, 1, "OPEN: "+p.directory)
	height := m.size.Rows - 3
	start := max(0, p.selected-height+1)
	for i := range height {
		index := start + i
		if index >= len(p.filtered) {
			break
		}
		prefix := "  "
		if index == p.selected {
			prefix = "> "
		}
		m.text(0, i+2, prefix+p.filtered[index].label)
	}
	if len(p.filtered) == 0 {
		m.text(1, 2, "no matching directories or PDFs")
	}
}
