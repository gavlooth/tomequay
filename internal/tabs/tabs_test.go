package tabs_test

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"terminal-pdf-reader/internal/tabs"
)

func active(t *testing.T, s *tabs.Set, id tabs.TabID) *tabs.Tab {
	t.Helper()
	tab, ok := s.Active()
	if !ok || tab.ID != id {
		t.Fatalf("active = %v, %v; want tab %d", tab, ok, id)
	}
	return tab
}

func TestDuplicateViewsAreIndependent(t *testing.T) {
	s := tabs.NewSet()
	first := s.Open("/book.pdf", 100)
	s.GoToPage(first, 23)
	s.ZoomBy(first, 2)
	s.Scroll(first, 0.3, 0.6)
	source := *active(t, s, first)
	last := s.Open("/other.pdf", 10)
	copyID := s.Duplicate(first)
	duplicate := active(t, s, copyID)
	if duplicate.View != source.View || duplicate.Document != source.Document {
		t.Fatalf("duplicate = %+v; want copied view and shared document %+v", duplicate, source)
	}
	ordered := s.Tabs()
	if len(ordered) != 3 || ordered[0].ID != first || ordered[1].ID != copyID || ordered[2].ID != last {
		t.Fatalf("duplicate order = %+v", ordered)
	}
	s.NextPage(copyID, 4)
	s.ZoomBy(copyID, 1.5)
	s.Scroll(copyID, 0.1, 0.2)
	if got := s.Tabs()[0].View; got != source.View {
		t.Fatalf("source changed after duplicate navigation: %+v, want %+v", got, source.View)
	}
	if got := duplicate.View; got.Page != 27 || got.Zoom != 3 || got.ScrollX != 0.1 || got.ScrollY != 0.2 {
		t.Fatalf("duplicate view = %+v", got)
	}
	// Editing the snapshot must not leak back into either live view.
	ordered[0].View.Page = 99
	if got := s.Tabs()[0].View; got != source.View {
		t.Fatalf("snapshot mutation changed source: %+v", got)
	}
}

func TestCanonicalSpellingsShareOnlyDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "book.pdf")
	if err := os.WriteFile(path, []byte("test document"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias.pdf")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	original, err := tabs.CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := tabs.CanonicalPath("./alias.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if original != linked || !filepath.IsAbs(linked) {
		t.Fatalf("canonical paths: %q, %q", original, linked)
	}
	s := tabs.NewSet()
	first := s.Open(original, 10)
	s.GoToPage(first, 4)
	s.ZoomBy(first, 2)
	s.Scroll(first, 0.2, 0.7)
	second := s.Open(linked, 99)
	all := s.Tabs()
	if all[0].Document != all[1].Document || all[0].Document.PageCount != 10 {
		t.Fatalf("same path did not reuse existing document: %+v", all)
	}
	if got := active(t, s, second).View; got != (tabs.View{Page: 1, Fit: tabs.FitPage, Zoom: 1}) {
		t.Fatalf("new view = %+v", got)
	}
	s.GoToPage(second, 9)
	s.Scroll(second, 1, 1)
	if got := s.Tabs()[0].View; got.Page != 4 || got.Zoom != 2 || got.ScrollY != 0.7 {
		t.Fatalf("second opening changed first view: %+v", got)
	}
	if _, err := tabs.CanonicalPath("missing.pdf"); err == nil {
		t.Fatal("missing file did not return an error")
	}
}

func TestCloseFocusAndDocumentLifetime(t *testing.T) {
	s := tabs.NewSet()
	left := s.Open("/book.pdf", 20)
	middle := s.Open("/book.pdf", 20)
	right := s.Open("/other.pdf", 10)
	doc := s.Tabs()[0].Document
	s.Focus(middle)
	s.Close(middle)
	active(t, s, right)
	if s.Tabs()[0].Document != doc {
		t.Fatal("closing one tab replaced another tab's document")
	}
	s.Close(right)
	active(t, s, left)
	// Closing an unfocused tab on either side preserves the focused tab.
	right = s.Open("/right.pdf", 1)
	s.Focus(left)
	s.Close(right)
	active(t, s, left)
	right = s.Open("/right.pdf", 1)
	s.Close(left)
	active(t, s, right)
	s.Close(right)
	if tab, ok := s.Active(); ok || tab != nil || len(s.Tabs()) != 0 {
		t.Fatalf("last close left state: %v, %v, %+v", tab, ok, s.Tabs())
	}
	reopened := s.Open("/book.pdf", 7)
	if got := active(t, s, reopened).Document; got == doc || got.PageCount != 7 {
		t.Fatalf("last close retained document: %+v", got)
	}
	if reopened == left || reopened == middle || reopened == right {
		t.Fatal("a closed tab ID was reused")
	}
}

func TestTabFocusWrapsAndUnknownIDsDoNothing(t *testing.T) {
	var s tabs.Set
	s.Next()
	s.Previous()
	if s.Focus(42) || s.Duplicate(42) != 0 {
		t.Fatal("unknown ID was accepted")
	}
	s.Close(42)
	s.GoToPage(42, 2)
	s.NextPage(42, 1)
	s.PreviousPage(42, 1)
	s.ZoomBy(42, 2)
	s.SetFit(42, tabs.FitWidth)
	s.Scroll(42, 1, 1)
	s.UpdateDocument("/absent.pdf", 3)
	first := s.Open("/a.pdf", 3)
	second := s.Open("/b.pdf", 3)
	s.Next()
	active(t, &s, first)
	s.Previous()
	active(t, &s, second)
	if !s.Focus(first) {
		t.Fatal("existing ID was not focused")
	}
	s.Next()
	active(t, &s, second)
	s.Previous()
	active(t, &s, first)
	s.Close(second)
	s.Next()
	s.Previous()
	active(t, &s, first)
}

func TestPageLimitsAndReload(t *testing.T) {
	s := tabs.NewSet()
	first := s.Open("/book.pdf", 100)
	second := s.Open("/book.pdf", 100)
	other := s.Open("/other.pdf", 30)
	s.GoToPage(first, -5)
	if got := s.Tabs()[0].View.Page; got != 1 {
		t.Fatalf("lower page limit = %d", got)
	}
	s.GoToPage(first, 1000)
	s.GoToPage(second, 80)
	s.GoToPage(other, 25)
	if got := s.Tabs()[0].View.Page; got != 100 {
		t.Fatalf("upper page limit = %d", got)
	}
	s.Scroll(first, 1, 1)
	s.UpdateDocument("/book.pdf", 12)
	all := s.Tabs()
	if all[0].View.Page != 12 || all[1].View.Page != 12 || all[2].View.Page != 25 || all[0].Document.PageCount != 12 {
		t.Fatalf("reload did not clamp only shared document: %+v", all)
	}
	if all[0].View.ScrollX != 0 || all[0].View.ScrollY != 0 {
		t.Fatal("clamping to a different page did not reset scrolling")
	}
	maxInt := int(^uint(0) >> 1)
	s.NextPage(first, maxInt)
	s.PreviousPage(second, maxInt)
	if got := s.Tabs(); got[0].View.Page != 12 || got[1].View.Page != 1 {
		t.Fatalf("large step overflowed: %+v", got)
	}
	s.PreviousPage(first, 2)
	s.NextPage(first, 1)
	s.NextPage(first, -1)
	s.PreviousPage(first, 0)
	if got := s.Tabs()[0].View.Page; got != 11 {
		t.Fatalf("page steps = %d; want 11", got)
	}
	s.UpdateDocument("/book.pdf", 0)
	if got := s.Tabs(); got[0].View.Page != 1 || got[1].View.Page != 1 || got[0].Document.PageCount != 1 {
		t.Fatalf("nonpositive page count = %+v", got)
	}
	id := s.Open("/empty.pdf", -10)
	if doc := active(t, s, id).Document; doc.PageCount != 1 {
		t.Fatalf("new document page count = %d", doc.PageCount)
	}
}

func TestSetPageRestoresPositionAndViewSnapshot(t *testing.T) {
	s := tabs.NewSet()
	source := s.Open("/book.pdf", 10)
	s.ZoomBy(source, 2)
	s.Scroll(source, 0.4, 0.2)
	duplicate := s.Duplicate(source)
	s.SetPage(source, 6, 0.75)
	view, ok := s.View(source)
	if !ok || view != (tabs.View{Page: 6, Fit: tabs.Custom, Zoom: 2, ScrollY: 0.75}) {
		t.Fatalf("page jump = %+v, %v", view, ok)
	}
	active(t, s, duplicate)
	if got, ok := s.View(duplicate); !ok || got != (tabs.View{Page: 1, Fit: tabs.Custom, Zoom: 2, ScrollX: 0.4, ScrollY: 0.2}) {
		t.Fatalf("page jump changed duplicate = %+v, %v", got, ok)
	}
	view.Page = 9
	if got, _ := s.View(source); got.Page != 6 {
		t.Fatal("changing returned view changed the tab")
	}
	s.Scroll(source, 0.3, 0)
	s.SetPage(source, 6, 0.5)
	if got, _ := s.View(source); got.ScrollX != 0.3 || got.ScrollY != 0.5 {
		t.Fatalf("same-page jump = %+v", got)
	}
	for _, test := range []struct {
		page       int
		scroll     float64
		wantPage   int
		wantScroll float64
	}{
		{-3, -0.5, 1, 0},
		{20, 2, 10, 1},
		{4, math.Inf(-1), 4, 0},
		{4, math.Inf(1), 4, 1},
		{4, math.NaN(), 4, 0},
	} {
		s.SetPage(source, test.page, test.scroll)
		if got, _ := s.View(source); got.Page != test.wantPage || got.ScrollY != test.wantScroll {
			t.Fatalf("SetPage(%d, %g) = %+v", test.page, test.scroll, got)
		}
	}
	before, _ := s.View(source)
	s.SetPage(999, 2, 1)
	if got, _ := s.View(source); got != before {
		t.Fatal("unknown tab jump changed another tab")
	}
	if got, ok := s.View(999); ok || got != (tabs.View{}) {
		t.Fatalf("unknown tab view = %+v, %v", got, ok)
	}
}

func TestZoomFitAndScrollLimits(t *testing.T) {
	s := tabs.NewSet()
	id := s.Open("/book.pdf", 10)
	tab := active(t, s, id)
	s.ZoomBy(id, math.MaxFloat64)
	if tab.View.Zoom != tabs.MaxZoom || tab.View.Fit != tabs.Custom {
		t.Fatalf("upper zoom limit = %+v", tab.View)
	}
	s.ZoomBy(id, math.SmallestNonzeroFloat64)
	if tab.View.Zoom != tabs.MinZoom {
		t.Fatalf("lower zoom limit = %g", tab.View.Zoom)
	}
	before := tab.View
	for _, factor := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		s.ZoomBy(id, factor)
		if tab.View != before {
			t.Fatalf("invalid factor %g changed view: %+v", factor, tab.View)
		}
	}
	s.Scroll(id, -2, 3)
	if tab.View.ScrollX != 0 || tab.View.ScrollY != 1 {
		t.Fatalf("scroll limits = %+v", tab.View)
	}
	s.Scroll(id, 0.25, -0.5)
	s.Scroll(id, math.NaN(), math.NaN())
	if tab.View.ScrollX != 0.25 || tab.View.ScrollY != 0.5 {
		t.Fatalf("scroll fractions = %+v", tab.View)
	}
	for _, mode := range []tabs.FitMode{tabs.FitWidth, tabs.FitPage, tabs.Custom} {
		s.SetFit(id, mode)
		if got := tab.View; got.Fit != mode || got.Zoom != 1 || got.ScrollX != 0 || got.ScrollY != 0 {
			t.Fatalf("fit reset = %+v", got)
		}
		s.ZoomBy(id, 2)
		s.Scroll(id, 0.5, 0.5)
	}
	before = tab.View
	s.SetFit(id, tabs.FitMode(100))
	if tab.View != before {
		t.Fatal("unknown fit mode changed view")
	}
}

// TestRealPDFSmoke can be included in the normal test run by setting
// PDFREADER_TEST_PDF to a real PDF. It requires the installed pdfinfo program.
func TestRealPDFSmoke(t *testing.T) {
	path := os.Getenv("PDFREADER_TEST_PDF")
	if path == "" {
		t.Skip("set PDFREADER_TEST_PDF for the real-PDF smoke test")
	}
	canonical, err := tabs.CanonicalPath(path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("pdfinfo", canonical).Output()
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?m)^Pages:\s+(\d+)`).FindSubmatch(output)
	if len(match) != 2 {
		t.Fatalf("pdfinfo has no page count: %s", output)
	}
	count, err := strconv.Atoi(string(match[1]))
	if err != nil || count < 1 {
		t.Fatalf("invalid page count %q: %v", match[1], err)
	}
	s := tabs.NewSet()
	first := s.Open(canonical, count)
	s.GoToPage(first, count)
	duplicate := s.Duplicate(first)
	s.GoToPage(duplicate, 1)
	if got := s.Tabs(); got[0].View.Page != count || got[1].View.Page != 1 || got[0].Document != got[1].Document {
		t.Fatalf("real PDF tabs = %+v", got)
	}
	t.Logf("opened real PDF with %d pages; duplicate navigation preserved source page %d", count, count)
}
