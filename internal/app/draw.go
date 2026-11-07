package app

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"tomequay/internal/keys"
	"tomequay/internal/pagefilter"
	"tomequay/internal/tabs"
)

func (m *model) text(x, y int, s string) {
	r := m.row(y)
	r.padTo(x, m.theme.plain())
	r.add(s, m.theme.plain())
	r.finish()
}
func (m *model) draw() error {
	w := m.screen.Writer()
	active, _ := m.tabs.Active()
	if m.size.Columns >= 20 && m.size.Rows >= 8 && !m.help && m.picker == nil && m.outline == nil && m.tabList == nil && m.historyPanel == nil && m.passwordFor == nil {
		m.ensureUpload()
	}
	m.drawTabs()
	next := drawnFrame{size: m.size}
	if active != nil {
		next.tab = active.ID
		next.dark = m.state(active.ID).dark
		next.sidebar = m.sidebarWidth()
	}
	switch {
	case m.size.Columns < 20 || m.size.Rows < 8:
		next.kind = smallFrame
	case m.help || m.picker != nil || m.outline != nil || m.tabList != nil || m.historyPanel != nil:
		next.kind = overlayFrame
		next.sidebar = 0
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
		background := m.theme.Background
		if next.dark && next.kind != overlayFrame {
			background = RGB(pagefilter.DarkPaper)
		}
		for row := 1; row < m.size.Rows-1; row++ {
			_, err := fmt.Fprintf(w, "\x1b[%d;1H\x1b[0m\x1b[48;2;%d;%d;%dm%s\x1b[0m\x1b[K", row+1, background>>16&255, background>>8&255, background&255, blank)
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
			} else if m.outline != nil {
				m.drawOutline()
			} else if m.tabList != nil {
				m.drawTabList()
			} else if m.historyPanel != nil {
				m.drawHistory()
			} else {
				m.drawPicker()
			}
		}
		m.frame = next
	}
	if next.kind != overlayFrame && next.kind != smallFrame {
		m.drawSidebar()
	}
	m.drawStatus()
	// Exactly one flush belongs to each complete frame.
	m.noteOutput(m.screen.Flush())
	return m.outputErr
}
func (m *model) drawTabs() {
	t := m.theme
	r := m.row(0)
	all := m.tabs.Tabs()
	m.tabHits = m.tabHits[:0]
	if len(all) == 0 {
		r.add(t.glyph(" \uf1c1 pdfr", " pdfr"), textStyle{fg: t.Cyan, bg: t.Background, bold: true})
		r.finish()
		return
	}
	active, _ := m.tabs.Active()
	labels := make([]string, len(all))
	widths := make([]int, len(all))
	total, selected := 0, 0
	for i, tab := range all {
		name := cleanText(filepath.Base(tab.Document.Path))
		for j, other := range all {
			if i != j && tab.Document.Path == other.Document.Path {
				name += fmt.Sprintf(":%d", tab.View.Page)
				break
			}
		}
		labels[i] = fmt.Sprintf("%d %s", i+1, name)
		widths[i] = min(30, textWidth(labels[i])+3)
		total += widths[i]
		if tab.ID == active.ID {
			selected = i
		}
	}
	marker := ""
	available := m.size.Columns
	if total > available {
		marker = fmt.Sprintf(t.glyph(" ‹%d/%d› ", " <%d/%d> "), selected+1, len(all))
		available = max(1, available-textWidth(marker))
	}
	start, end := tabWindow(widths, selected, available)
	for i := start; i < end; i++ {
		width := min(widths[i], available-r.x)
		split := strings.IndexByte(labels[i], ' ') + 1
		prefix := clipText(labels[i][:split], max(0, width-3))
		label := prefix + ellipsisText(labels[i][split:], max(0, width-3-textWidth(prefix)), t.NoGlyphs)
		x := r.x
		if i == selected {
			r.add(" "+label+" ", textStyle{fg: t.Background, bg: t.Blue, bold: true})
			r.add(t.glyph("\ue0b0", ">"), textStyle{fg: t.Blue, bg: t.Background})
		} else {
			r.add(" "+label+" ", t.muted())
			r.add(t.glyph("│", "|"), t.muted())
		}
		m.tabHits = append(m.tabHits, tabHit{id: all[i].ID, start: x, end: r.x})
	}
	if marker != "" {
		r.padTo(m.size.Columns-textWidth(marker), t.plain())
		r.add(marker, textStyle{fg: t.Cyan, bg: t.Background})
	}
	r.finish()
}

func (m *model) modeLabel() (string, RGB) {
	switch {
	case m.passwordFor != nil:
		return "PASSWORD", m.theme.Red
	case m.help:
		return "HELP", m.theme.Cyan
	case m.outline != nil:
		return "OUTLINE", m.theme.Blue
	case m.tabList != nil:
		return "TABS", m.theme.Blue
	case m.historyPanel != nil:
		return "HISTORY", m.theme.Blue
	case m.picker != nil:
		return "PICKER", m.theme.Green
	case m.decoder.Mode() == keys.Search:
		return "SEARCH", m.theme.Yellow
	case m.decoder.Mode() == keys.CommandLine:
		return "COMMAND", m.theme.Magenta
	default:
		return "NORMAL", m.theme.Foreground
	}
}

func (m *model) drawMode(r *textRow) {
	label, colour := m.modeLabel()
	r.add(" "+label+" ", textStyle{fg: m.theme.Background, bg: colour, bold: true})
	r.add(m.theme.glyph("\ue0b0", ">"), textStyle{fg: colour, bg: m.theme.Background})
}

func (m *model) promptText() (string, bool) {
	switch {
	case m.passwordFor != nil:
		return "Password for " + filepath.Base(m.passwordFor.path) + ": " + strings.Repeat("*", len(m.password)), true
	case m.picker != nil && m.picker.filtering:
		return "/" + m.picker.filter, true
	case m.outline != nil && m.outline.filtering:
		return "/" + m.outline.filter, true
	case m.tabList != nil && m.tabList.filtering:
		return "/" + m.tabList.filter, true
	case m.historyPanel != nil && m.historyPanel.filtering:
		return "/" + m.historyPanel.filter, true
	case m.decoder.Mode() != keys.Normal && !m.help && m.picker == nil && m.outline == nil && m.tabList == nil && m.historyPanel == nil:
		return m.decoder.Pending(), true
	default:
		return "", false
	}
}

func (m *model) drawStatus() {
	t := m.theme
	r := m.row(m.size.Rows - 1)
	m.drawMode(r)
	if prompt, ok := m.promptText(); ok {
		r.add(" ", t.plain())
		prompt = cleanText(prompt)
		columns := max(0, m.size.Columns-r.x-1)
		if strings.HasPrefix(prompt, ":") || strings.HasPrefix(prompt, "/") || strings.HasPrefix(prompt, "?") {
			prompt = clipText(prompt[:1], columns) + ellipsisText(prompt[1:], max(0, columns-1), t.NoGlyphs)
		} else {
			prompt = ellipsisText(prompt, columns, t.NoGlyphs)
		}
		r.add(prompt, t.plain())
		cursor := min(r.x+1, m.size.Columns)
		r.finish()
		_, err := fmt.Fprintf(m.screen.Writer(), "\x1b[%d;%dH\x1b[?25h", m.size.Rows, max(1, cursor))
		m.noteOutput(err)
		return
	}
	pending := cleanText(m.decoder.Pending())
	pending = ellipsisText(pending, max(0, m.size.Columns/4), t.NoGlyphs)
	right := 0
	if pending != "" {
		right = textWidth(pending) + 2
	}
	space := max(0, m.size.Columns-r.x-right)
	active, ok := m.tabs.Active()
	if !ok {
		if m.message != "" {
			r.add(clipText(" "+cleanText(m.message), space), m.messageStyle(m.message))
		} else {
			r.add(clipText(" o: open PDF  F1: help  q: quit", space), t.muted())
		}
	} else {
		page := fmt.Sprintf(" %d/%d ", active.View.Page, active.Document.PageCount)
		fit := "fit"
		if active.View.Fit == tabs.FitWidth {
			fit = "width"
		} else if active.View.Fit == tabs.Custom {
			fit = fmt.Sprintf("%.0f%%", active.View.Zoom*100)
		}
		fit = " " + fit + " "
		s := m.state(active.ID)
		if s.dark {
			fit += "dark "
		}
		match := ""
		if s.searchQuery != "" || len(s.searchPages) > 0 {
			match = fmt.Sprintf(" match %d/%d ", max(0, s.searchIndex+1), len(s.searchPages))
		}
		barWidth := min(16, max(8, m.size.Columns/10))
		if space < textWidth(page+fit+match)+barWidth+3 {
			barWidth = 0
		}
		message := m.message
		if message == strings.TrimSpace(match) {
			message = ""
		}
		if u := m.pendingUpload; u != nil && u.key == m.currentKey {
			if u.attempts == 1 {
				message = "sending image"
			} else {
				message = fmt.Sprintf("image retry %d/2", u.attempts-1)
			}
		}
		free := max(0, space-textWidth(page+fit+match)-barWidth-1)
		name := cleanText(filepath.Base(active.Document.Path))
		nameWidth := min(32, free)
		if message != "" {
			nameWidth = min(nameWidth, max(0, free/3))
		}
		if nameWidth > 1 {
			r.add(" "+ellipsisText(name, nameWidth-1, t.NoGlyphs), t.plain())
		}
		r.add(page, t.plain())
		if barWidth > 0 {
			r.add(progressBar(active.View.Page, active.Document.PageCount, barWidth, t.NoGlyphs), textStyle{fg: t.Blue, bg: t.Background})
			r.add(" ", t.plain())
		}
		r.add(fit, textStyle{fg: t.Cyan, bg: t.Background})
		r.add(match, textStyle{fg: t.Yellow, bg: t.Background})
		if message == "" && space > 72 {
			message = "gb tabs  gd copy  zd dark  gs sidebar  F1 help"
		}
		if message != "" {
			r.add(clipText(" "+cleanText(message), max(0, m.size.Columns-right-r.x)), m.messageStyle(message))
		}
	}
	if pending != "" {
		r.padTo(m.size.Columns-right, t.plain())
		r.add(" "+pending+" ", textStyle{fg: t.Yellow, bg: t.Background, bold: true})
	}
	r.finish()
	_, err := io.WriteString(m.screen.Writer(), "\x1b[?25l")
	m.noteOutput(err)
}

func (m *model) messageStyle(message string) textStyle {
	colour := m.theme.Yellow
	lower := strings.ToLower(message)
	for _, word := range [...]string{"error", "failed", "not acknowledged", "no such", "not found", "cannot", "permission", "unsupported", "invalid", "unknown", "requires", "missing"} {
		if strings.Contains(lower, word) {
			colour = m.theme.Red
			break
		}
	}
	return textStyle{fg: colour, bg: m.theme.Background}
}

type panel struct {
	m                   *model
	x, width, y, height int
}

func (m *model) panel(title, footer string, start, count, total int) panel {
	x := 0
	if m.size.Columns >= 40 {
		x = 2
	}
	p := panel{m: m, x: x, width: max(2, m.size.Columns-x*2), y: 1, height: max(0, m.size.Rows-5)}
	t := m.theme
	top := m.row(p.y)
	top.padTo(x, t.plain())
	top.add(t.glyph("╭", "+"), t.muted())
	title = " " + title + " "
	indicator := ""
	if total > 0 {
		indicator = fmt.Sprintf(" %d-%d/%d ", min(start+1, total), min(start+count, total), total)
	}
	title = ellipsisText(cleanText(title), max(0, p.width-2-textWidth(indicator)), t.NoGlyphs)
	top.add(title, textStyle{fg: t.Cyan, bg: t.Background, bold: true})
	top.add(strings.Repeat(t.glyph("─", "-"), max(0, x+p.width-1-textWidth(indicator)-top.x)), t.muted())
	top.add(indicator+t.glyph("╮", "+"), t.muted())
	top.finish()
	p.row(p.height, footer, "", t.muted())
	bottom := m.row(p.y + p.height + 2)
	bottom.padTo(x, t.plain())
	bottom.add(t.glyph("╰", "+")+strings.Repeat(t.glyph("─", "-"), max(0, p.width-2))+t.glyph("╯", "+"), t.muted())
	bottom.finish()
	return p
}

// row fills the entire interior, including the selection background. Clearing
// only to EL would not erase old Kitty placeholder cells within the box.
func (p panel) row(index int, left, right string, style textStyle) {
	t := p.m.theme
	r := p.m.row(p.y + index + 1)
	r.padTo(p.x, t.plain())
	r.add(t.glyph("│", "|"), t.muted())
	end := p.x + p.width - 1
	right = clipText(cleanText(right), max(0, p.width-4))
	left = clipText(cleanText(left), max(0, p.width-4-textWidth(right)))
	r.add(" "+left, style)
	r.padTo(end-1-textWidth(right), style)
	r.add(right, style)
	r.padTo(end, style)
	r.add(t.glyph("│", "|"), t.muted())
	r.finish()
}

type helpRow struct {
	section, keys, description string
}

var helpSections = [...]string{"Navigation", "Zoom", "Tabs", "Search", "Marks", "Files"}

// The decoder owns bindings; this table only chooses their presentation group.
var helpGroups = map[keys.Action]string{
	keys.ScrollDown: "Navigation", keys.ScrollUp: "Navigation", keys.ScrollLeft: "Navigation", keys.ScrollRight: "Navigation",
	keys.HalfPageDown: "Navigation", keys.HalfPageUp: "Navigation", keys.FullPageDown: "Navigation", keys.FullPageUp: "Navigation",
	keys.NextPage: "Navigation", keys.PreviousPage: "Navigation", keys.FirstPage: "Navigation", keys.LastPage: "Navigation",
	keys.GoToPage: "Navigation", keys.ScrollToTop: "Navigation", keys.ScrollToMiddle: "Navigation", keys.ScrollToBottom: "Navigation",
	keys.ZoomIn: "Zoom", keys.ZoomOut: "Zoom", keys.FitWidth: "Zoom", keys.FitPage: "Zoom", keys.ResetZoom: "Zoom",
	keys.NextTab: "Tabs", keys.PreviousTab: "Tabs", keys.GoToTab: "Tabs", keys.DuplicateTab: "Tabs", keys.CloseTab: "Tabs", keys.NewTab: "Tabs",
	keys.TabList: "Tabs", keys.PreviousUsedTab: "Tabs", keys.GoToTabByName: "Tabs", keys.MoveTab: "Tabs", keys.CloseOtherTabs: "Tabs",
	keys.ToggleDark: "Zoom", keys.ToggleSidebar: "Files",
	keys.SearchForward: "Search", keys.SearchBackward: "Search", keys.SearchNext: "Search", keys.SearchPrevious: "Search",
	keys.SetMark: "Marks", keys.JumpToMark: "Marks", keys.JumpBack: "Marks", keys.JumpOlder: "Marks", keys.JumpNewer: "Marks",
	keys.OpenFile: "Files", keys.QuitAll: "Files", keys.Reload: "Files", keys.Redraw: "Files", keys.Outline: "Files",
	keys.HelpAction: "Files", keys.OpenCommandLine: "Files",
	keys.History: "Files",
}

func groupedHelp() []helpRow {
	entries := keys.Help()
	rows := make([]helpRow, 0, len(entries)+len(helpSections))
	for _, section := range helpSections {
		rows = append(rows, helpRow{section: section})
		start := len(rows)
		for _, entry := range entries {
			group := helpGroups[entry.Action]
			if group == "" {
				group = "Files"
			}
			if group != section {
				continue
			}
			found := false
			for i := start; i < len(rows); i++ {
				if rows[i].description == entry.Description {
					rows[i].keys += " / " + entry.Keys
					found = true
					break
				}
			}
			if !found {
				rows = append(rows, helpRow{keys: entry.Keys, description: entry.Description})
			}
		}
	}
	return rows
}

var readerHelp = groupedHelp()

func (m *model) drawHelp() {
	rows := readerHelp
	height := max(0, m.size.Rows-5)
	m.helpOffset = min(m.helpOffset, max(0, len(rows)-height))
	p := m.panel("HELP", m.theme.glyph("j/k scroll  ·  any other key closes", "j/k scroll  |  any other key closes"), m.helpOffset, height, len(rows))
	for i := range height {
		index := i + m.helpOffset
		if index >= len(rows) {
			p.row(i, "", "", m.theme.plain())
			continue
		}
		e := rows[index]
		if e.section != "" {
			p.row(i, e.section, "", textStyle{fg: m.theme.Cyan, bg: m.theme.Background, bold: true})
			continue
		}
		keyWidth := min(24, max(12, (p.width-4)/3))
		key := ellipsisText(e.keys, keyWidth, m.theme.NoGlyphs)
		p.row(i, key+strings.Repeat(" ", max(1, keyWidth-textWidth(key)+2))+e.description, "", m.theme.plain())
	}
}

func (m *model) drawPicker() {
	p := m.picker
	height := max(0, m.size.Rows-5)
	start := max(0, p.selected-height+1)
	title := m.theme.glyph("\uf07c OPEN: ", "OPEN: ") + p.directory
	if p.filter != "" {
		title += " /" + p.filter
	}
	box := m.panel(title, "j/k select  Enter open  h parent  / filter  Esc close", start, height, len(p.filtered))
	for i := range height {
		index := start + i
		if index >= len(p.filtered) {
			label := ""
			if i == 0 {
				label = "no matching directories or PDFs"
			}
			box.row(i, label, "", m.theme.muted())
			continue
		}
		entry := p.filtered[index]
		icon := m.theme.glyph("\uf1c1 ", "  ")
		if entry.dir {
			icon = m.theme.glyph("\uf07b ", "/ ")
		}
		style := m.theme.plain()
		if index == p.selected {
			style = textStyle{fg: m.theme.Background, bg: m.theme.Blue, bold: true}
		}
		box.row(i, icon+entry.label, "", style)
	}
}

func (m *model) drawOutline() {
	p := m.outline
	height := max(0, m.size.Rows-5)
	start := max(0, p.selected-height+1)
	title := "OUTLINE: " + filepath.Base(p.path)
	if p.filter != "" {
		title += " /" + p.filter
	}
	box := m.panel(title, "j/k select  Enter jump  / filter  Esc close", start, height, len(p.filtered))
	for i := range height {
		index := start + i
		if p.loading || index >= len(p.filtered) {
			label := ""
			if i == 0 {
				label = "no matching outline titles"
				if p.loading {
					label = "loading outline"
				}
			}
			box.row(i, label, "", m.theme.muted())
			continue
		}
		entry := p.entries[p.filtered[index]]
		style := m.theme.plain()
		page := fmt.Sprintf("p. %d", entry.Page)
		if entry.Page == 0 {
			style = m.theme.muted()
			style.dim = true
			page = "no page"
		} else if index == p.selected {
			style = textStyle{fg: m.theme.Background, bg: m.theme.Blue, bold: true}
		}
		depth := min(max(0, entry.Depth), max(0, box.width/4-2))
		indent := strings.Repeat(m.theme.glyph("│ ", "| "), depth)
		box.row(i, indent+entry.Title, page, style)
	}
}
