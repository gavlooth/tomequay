package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

// RGB is a 24-bit terminal colour, written as 0xRRGGBB.
type RGB uint32

// Theme describes the reader's chrome. NoGlyphs replaces decorative glyphs with
// ASCII; it does not transliterate document titles or file names.
type Theme struct {
	Background, Foreground, Muted RGB
	Blue, Cyan, Green, Red        RGB
	Yellow, Magenta               RGB
	NoGlyphs                      bool
}

// DefaultTheme uses the owner's dark Ghostty palette. PDFR_ASCII=1 selects
// separators, borders, icons, and progress cells that need no patched font.
func DefaultTheme() Theme {
	return Theme{
		Background: 0x000000, Foreground: 0xdfdfdf, Muted: 0x808080,
		Blue: 0x0066ff, Cyan: 0x00ffff, Green: 0x00a600, Red: 0xfe0100,
		Yellow: 0xfeff00, Magenta: 0xcc00ff, NoGlyphs: os.Getenv("PDFR_ASCII") == "1",
	}
}

type textStyle struct {
	fg, bg    RGB
	bold, dim bool
}

func (t Theme) plain() textStyle { return textStyle{fg: t.Foreground, bg: t.Background} }
func (t Theme) muted() textStyle { return textStyle{fg: t.Muted, bg: t.Background} }
func (t Theme) glyph(normal, ascii string) string {
	if t.NoGlyphs {
		return ascii
	}
	return normal
}

// textRow tracks SGR changes rather than resetting between every span. The
// final reset precedes EL so neither the next row nor its empty cells inherit
// a highlight. Text is always sanitized before it reaches the terminal.
type textRow struct {
	m      *model
	out    strings.Builder
	x      int
	style  textStyle
	styled bool
}

func (m *model) row(y int) *textRow {
	r := &textRow{m: m}
	if y < 0 || y >= m.size.Rows || m.size.Columns < 1 {
		return r
	}
	fmt.Fprintf(&r.out, "\x1b[%d;1H\x1b[0m", y+1)
	return r
}

func (r *textRow) set(s textStyle) {
	if r.styled && r.style == s {
		return
	}
	r.out.WriteString("\x1b[")
	sep := ""
	if r.styled && (r.style.bold != s.bold || r.style.dim != s.dim) {
		r.out.WriteString("22")
		sep = ";"
	}
	if s.bold && (!r.styled || !r.style.bold || r.style.dim != s.dim) {
		r.out.WriteString(sep + "1")
		sep = ";"
	}
	if s.dim && (!r.styled || !r.style.dim || r.style.bold != s.bold) {
		r.out.WriteString(sep + "2")
		sep = ";"
	}
	if !r.styled || r.style.fg != s.fg {
		fmt.Fprintf(&r.out, "%s38;2;%d;%d;%d", sep, s.fg>>16&255, s.fg>>8&255, s.fg&255)
		sep = ";"
	}
	if !r.styled || r.style.bg != s.bg {
		fmt.Fprintf(&r.out, "%s48;2;%d;%d;%d", sep, s.bg>>16&255, s.bg>>8&255, s.bg&255)
	}
	r.out.WriteByte('m')
	r.style, r.styled = s, true
}

func (r *textRow) add(s string, style textStyle) {
	if r.out.Len() == 0 || r.x >= r.m.size.Columns || s == "" {
		return
	}
	s = clipText(cleanText(s), r.m.size.Columns-r.x)
	if s == "" {
		return
	}
	r.set(style)
	r.out.WriteString(s)
	r.x += textWidth(s)
}

func (r *textRow) padTo(x int, style textStyle) {
	if n := min(x, r.m.size.Columns) - r.x; n > 0 {
		r.add(strings.Repeat(" ", n), style)
	}
}

func (r *textRow) finish() {
	if r.out.Len() == 0 {
		return
	}
	r.out.WriteString("\x1b[0m\x1b[K")
	_, err := io.WriteString(r.m.screen.Writer(), r.out.String())
	r.m.noteOutput(err)
}

// Nerd Font private-use glyphs, including supplementary-plane icons, occupy one
// cell. Only the actual wide Unicode ranges occupy two; combining/format marks
// never advance the cursor.
func runeWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f ||
		r >= 0xac00 && r <= 0xd7a3 || r >= 0xf900 && r <= 0xfaff ||
		r >= 0xfe10 && r <= 0xfe19 || r >= 0xfe30 && r <= 0xfe6f ||
		r >= 0xff01 && r <= 0xff60 || r >= 0xffe0 && r <= 0xffe6 ||
		r >= 0x1f300 && r <= 0x1faff || r >= 0x20000 && r <= 0x3fffd) {
		return 2
	}
	return 1
}

func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func clipText(s string, columns int) string {
	if columns <= 0 {
		return ""
	}
	width := 0
	for i, r := range s {
		width += runeWidth(r)
		if width > columns {
			return s[:i]
		}
	}
	return s
}

// ellipsisText retains the end of a name, where its distinguishing suffix and
// extension usually live. It never splits a wide cell or starts on a mark.
func ellipsisText(s string, columns int, ascii bool) string {
	if columns <= 0 {
		return ""
	}
	width := textWidth(s)
	if width <= columns {
		return s
	}
	mark := "…"
	if ascii {
		mark = "..."
	}
	if columns <= len(mark) && ascii || columns == 1 {
		return clipText(mark, columns)
	}
	remaining := columns - textWidth(mark)
	for i, r := range s {
		if width <= remaining && runeWidth(r) > 0 {
			return mark + s[i:]
		}
		width -= runeWidth(r)
	}
	return mark
}

// progressCells reports full cells and eighths of the next cell. The last page
// fills the bar; unknown or empty documents leave it empty.
func progressCells(page, pages, width int) (full, eighths int) {
	if pages <= 0 || width <= 0 {
		return 0, 0
	}
	units := int(float64(min(max(page, 0), pages)) / float64(pages) * float64(width*8))
	return units / 8, units % 8
}

func progressBar(page, pages, width int, ascii bool) string {
	full, part := progressCells(page, pages, width)
	var b strings.Builder
	for i := range max(0, width) {
		switch {
		case i < full:
			if ascii {
				b.WriteByte('#')
			} else {
				b.WriteRune('█')
			}
		case i == full && part > 0:
			if ascii {
				b.WriteByte('+')
			} else {
				b.WriteRune([...]rune{'▏', '▎', '▍', '▌', '▋', '▊', '▉'}[part-1])
			}
		default:
			if ascii {
				b.WriteByte('-')
			} else {
				b.WriteRune('░')
			}
		}
	}
	return b.String()
}

// tabWindow chooses a contiguous range containing the active tab. If that tab
// alone is too wide, the caller truncates it; it must never disappear behind
// the overflow marker.
func tabWindow(widths []int, active, columns int) (start, end int) {
	if len(widths) == 0 || columns <= 0 {
		return 0, 0
	}
	active = min(max(active, 0), len(widths)-1)
	start, end = active, active+1
	used := widths[active]
	for start > 0 && used+widths[start-1] <= columns {
		start--
		used += widths[start]
	}
	for end < len(widths) && used+widths[end] <= columns {
		used += widths[end]
		end++
	}
	return start, end
}
