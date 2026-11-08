package app

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"

	"tomequay/internal/cache"
	"tomequay/internal/keys"
	"tomequay/internal/pagefilter"
	"tomequay/internal/pdf"
	"tomequay/internal/tabs"
	"tomequay/internal/terminal"
)

type pageSelection struct {
	tab                 tabs.TabID
	path                string
	page                int
	mtime               int64
	cancel              context.CancelFunc
	loading             bool
	ctx                 context.Context
	words               pdf.PageWords
	cursor, anchor      int
	selecting, dragging bool
	version             uint64
	highlightKey        cache.Key
	displayVersion      uint64
	highlightVersion    uint64
	highlightPNG        []byte
	highlightRunning    bool
}

type pageWordsResult struct {
	selection *pageSelection
	words     pdf.PageWords
	err       error
}

type selectionImageResult struct {
	selection *pageSelection
	key       cache.Key
	version   uint64
	png       []byte
	err       error
}

func (m *model) selectConfiguredText() {
	if m.selectionMode == "page" {
		m.showPageSelection()
	} else {
		m.showText()
	}
}

func (m *model) setSelectionMode(mode string) {
	if mode == "" {
		m.message = "Selection mode: " + m.selectionMode
		return
	}
	if mode != "text" && mode != "page" {
		m.message = "Invalid selection mode: use text or page"
		return
	}
	m.selectionMode = mode
	m.message = "Selection mode: " + mode
}
func (m *model) showPageSelection() {
	t, ok := m.tabs.Active()
	if !ok {
		return
	}
	d := m.docs[t.Document.Path]
	if d == nil || !d.ready {
		return
	}
	m.closeText()
	m.closePageSelection()
	ctx, cancel := context.WithCancel(m.ctx)
	p := &pageSelection{tab: t.ID, path: t.Document.Path, page: t.View.Page, mtime: d.mtime, ctx: ctx, cancel: cancel, loading: true}
	m.pageSelection, m.drag = p, nil
	m.message = "Loading page selection… Esc cancels"
	password := d.password
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		words, err := m.tools.PageWords(ctx, p.path, password, p.page)
		select {
		case m.pageWordsResults <- pageWordsResult{p, words, err}:
		case <-ctx.Done():
		}
	}()
}

func (m *model) closePageSelection() {
	if p := m.pageSelection; p != nil {
		p.cancel()
		m.pageSelection = nil
		m.discardPendingReplacement()
		m.restoreSelectionImages()
		m.message = ""
	}
}

func (m *model) restoreSelectionImages() {
	for key, r := range m.imageIDs {
		if r.original != nil {
			m.noteOutput(m.images.Delete(r.id))
			m.imageIDs[key] = r.original
		}
	}
}

func (m *model) pageSelectionMatches(p *pageSelection) bool {
	t, ok := m.tabs.Active()
	d := m.docs[p.path]
	return ok && t.ID == p.tab && t.View.Page == p.page && d != nil && d.ready && d.mtime == p.mtime
}

func (m *model) acceptPageWords(r pageWordsResult) {
	p := m.pageSelection
	if p == nil || p != r.selection {
		return
	}
	if !m.pageSelectionMatches(p) {
		m.closePageSelection()
		return
	}
	p.loading = false
	if r.err != nil {
		m.message = "Text extraction failed: " + r.err.Error()
		return
	}
	p.words = r.words
	if len(p.words.Words) == 0 {
		m.message = "No selectable text; this page may need OCR"
		return
	}
	m.message = "Drag words · h/j/k/l move · v select · a all · y copy · Esc finish"
	p.version++
}

func (p *pageSelection) interval() (int, int) {
	start, end := p.cursor, p.cursor
	if p.selecting {
		start, end = min(p.anchor, p.cursor), max(p.anchor, p.cursor)
	}
	return start, end
}

func (p *pageSelection) selectedText() string {
	if len(p.words.Words) == 0 {
		return ""
	}
	start, end := p.interval()
	var out strings.Builder
	for i := start; i <= end; i++ {
		if i > start {
			if p.words.Words[i].Line != p.words.Words[i-1].Line {
				out.WriteByte('\n')
			} else {
				out.WriteByte(' ')
			}
		}
		out.WriteString(p.words.Words[i].Text)
	}
	return out.String()
}

func (p *pageSelection) moveLine(direction int) {
	current := p.words.Words[p.cursor]
	targetLine := -1
	for i := p.cursor + direction; i >= 0 && i < len(p.words.Words); i += direction {
		if p.words.Words[i].Line != current.Line {
			targetLine = p.words.Words[i].Line
			break
		}
	}
	if targetLine < 0 {
		return
	}
	best, distance := p.cursor, math.Inf(1)
	x := (current.XMin + current.XMax) / 2
	for i, word := range p.words.Words {
		if word.Line != targetLine {
			continue
		}
		d := math.Abs((word.XMin+word.XMax)/2 - x)
		if d < distance {
			best, distance = i, d
		}
	}
	p.cursor = best
}

func (m *model) pageSelectionKey(k keys.Key) bool {
	p := m.pageSelection
	if k.Special == keys.Esc || k.Rune == 'q' && !k.Ctrl {
		m.closePageSelection()
		return true
	}
	if !k.Ctrl && (k.Rune == '+' || k.Rune == '-') {
		action := keys.ZoomIn
		if k.Rune == '-' {
			action = keys.ZoomOut
		}
		m.command(keys.Command{Action: action})
		return true
	}
	handled := k.Special == keys.Left || k.Special == keys.Right || k.Special == keys.Up || k.Special == keys.Down || !k.Ctrl && strings.ContainsRune("hjklvay", k.Rune)
	if !handled {
		return false
	}
	if p.loading || len(p.words.Words) == 0 {
		return true
	}
	oldCursor, oldAnchor, oldSelecting := p.cursor, p.anchor, p.selecting
	switch {
	case k.Special == keys.Left || k.Rune == 'h':
		p.cursor = max(0, p.cursor-1)
	case k.Special == keys.Right || k.Rune == 'l':
		p.cursor = min(len(p.words.Words)-1, p.cursor+1)
	case k.Special == keys.Up || k.Rune == 'k':
		p.moveLine(-1)
	case k.Special == keys.Down || k.Rune == 'j':
		p.moveLine(1)
	case k.Rune == 'v':
		p.selecting = !p.selecting
		p.anchor = p.cursor
	case k.Rune == 'a':
		p.selecting, p.anchor, p.cursor = true, 0, len(p.words.Words)-1
	case k.Rune == 'y':
		if err := terminal.WriteClipboard(m.screen.Writer(), p.selectedText(), terminal.InTmux()); err != nil {
			m.message = "Copy failed: " + err.Error()
		} else {
			m.message = "Selection sent to terminal clipboard (OSC 52)"
		}
	}
	if oldCursor != p.cursor || oldAnchor != p.anchor || oldSelecting != p.selecting {
		p.version++
		m.revealSelectedWord()
	}
	return true
}

func (m *model) revealSelectedWord() {
	p := m.pageSelection
	if p == nil || len(p.words.Words) == 0 {
		return
	}
	l := m.activeLayout()
	word := p.words.Words[p.cursor]
	w, h := p.pixelSize(l)
	x1, x2 := word.XMin/p.words.Width*float64(w), word.XMax/p.words.Width*float64(w)
	y1, y2 := word.YMin/p.words.Height*float64(h), word.YMax/p.words.Height*float64(h)
	cropX, cropY := float64(l.Request.CropX), float64(l.Request.CropY)
	if x1 < cropX {
		cropX = x1
	} else if x2 > cropX+float64(l.VisibleWidth) {
		cropX = x2 - float64(l.VisibleWidth)
	}
	if y1 < cropY {
		cropY = y1
	} else if y2 > cropY+float64(l.VisibleHeight) {
		cropY = y2 - float64(l.VisibleHeight)
	}
	view, _ := m.tabs.View(p.tab)
	x, y := view.ScrollX, view.ScrollY
	if overflow := l.FullWidth - l.VisibleWidth; overflow > 0 {
		x = cropX / float64(overflow)
	}
	if overflow := l.FullHeight - l.VisibleHeight; overflow > 0 {
		y = cropY / float64(overflow)
	}
	m.tabs.Scroll(p.tab, x-view.ScrollX, y-view.ScrollY)
}

func (p *pageSelection) pixelSize(l pageLayout) (int, int) {
	return p.words.PixelSize(l.Request)
}

func (m *model) pageSelectionMouse(e terminal.MouseEvent) bool {
	p := m.pageSelection
	if e.Kind == terminal.MouseRelease {
		p.dragging = false
		return true
	}
	if e.Shift || e.Kind >= terminal.MouseWheelUp {
		return false
	}
	if e.Button != terminal.ButtonLeft || e.Kind != terminal.MousePress && e.Kind != terminal.MouseDrag {
		return false
	}
	l := m.layout
	inside := e.Column >= l.X && e.Column < l.X+l.Columns && e.Row >= l.Y && e.Row < l.Y+l.Rows
	if !inside && (e.Kind != terminal.MouseDrag || !p.dragging) {
		return false
	}
	if p.loading || len(p.words.Words) == 0 || len(m.currentPNG) == 0 {
		return true
	}
	w, h := p.pixelSize(l)
	if w <= 0 || h <= 0 || l.Columns <= 0 || l.Rows <= 0 {
		return true
	}
	// Images fill their placement cells, including the final partial cell.
	x := (float64(e.Column-l.X)+0.5)*float64(l.VisibleWidth)/float64(l.Columns) + float64(l.Request.CropX)
	y := (float64(e.Row-l.Y)+0.5)*float64(l.VisibleHeight)/float64(l.Rows) + float64(l.Request.CropY)
	x, y = x/float64(w)*p.words.Width, y/float64(h)*p.words.Height
	best, distance := 0, math.Inf(1)
	for i, word := range p.words.Words {
		dx := max(word.XMin-x, 0, x-word.XMax)
		dy := max(word.YMin-y, 0, y-word.YMax)
		d := dx*dx + dy*dy
		if d < distance {
			best, distance = i, d
		}
	}
	if e.Kind == terminal.MousePress {
		p.anchor, p.selecting, p.dragging = best, true, true
	} else if !p.dragging {
		return true
	}
	if p.cursor != best || e.Kind == terminal.MousePress {
		p.cursor = best
		p.version++
	}
	return true
}

func (m *model) prepareSelectionImage() {
	p := m.pageSelection
	if p == nil {
		return
	}
	if !m.pageSelectionMatches(p) {
		m.closePageSelection()
		return
	}
	if p.loading || len(p.words.Words) == 0 || p.highlightRunning || len(m.currentPNG) == 0 {
		return
	}
	if p.highlightKey == m.currentKey && p.highlightVersion == p.version {
		return
	}
	start, end := p.interval()
	l := m.layout
	w, h := p.pixelSize(l)
	rectangles := make([]image.Rectangle, 0, end-start+1)
	for _, word := range p.words.Words[start : end+1] {
		x1 := int(math.Floor(word.XMin/p.words.Width*float64(w))) - l.Request.CropX
		y1 := int(math.Floor(word.YMin/p.words.Height*float64(h))) - l.Request.CropY
		x2 := int(math.Ceil(word.XMax/p.words.Width*float64(w))) - l.Request.CropX
		y2 := int(math.Ceil(word.YMax/p.words.Height*float64(h))) - l.Request.CropY
		rectangles = append(rectangles, image.Rect(x1, y1, x2, y2))
	}
	p.highlightRunning = true
	key, version, data := m.currentKey, p.version, m.currentPNG
	ctx := p.ctx
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		png, err := pagefilter.Selection(data, rectangles)
		select {
		case m.selectionImageResults <- selectionImageResult{p, key, version, png, err}:
		case <-ctx.Done():
		}
	}()
}

func (m *model) acceptSelectionImage(r selectionImageResult) {
	p := m.pageSelection
	if p == nil || p != r.selection {
		return
	}
	p.highlightRunning = false
	if r.key != m.currentKey || r.version != p.version {
		return
	}
	p.highlightKey, p.highlightVersion = r.key, r.version
	if r.err != nil {
		p.highlightPNG = nil
		m.message = "Selection highlight failed: " + r.err.Error()
		return
	}
	p.highlightPNG = r.png
	m.selectionImageVersion++
	p.displayVersion = m.selectionImageVersion
}

func (m *model) displayedPNG() ([]byte, uint64) {
	p := m.pageSelection
	if p != nil && p.highlightKey == m.currentKey && len(p.highlightPNG) > 0 {
		return p.highlightPNG, p.displayVersion
	}
	return m.currentPNG, 0
}

func (m *model) pageSelectionStatus() string {
	p := m.pageSelection
	if p.loading {
		return " Loading page selection… Esc cancels"
	}
	if len(p.words.Words) == 0 {
		return " " + m.message
	}
	start, end := p.interval()
	return fmt.Sprintf(" words %d-%d/%d · %s", start+1, end+1, len(p.words.Words), m.message)
}
