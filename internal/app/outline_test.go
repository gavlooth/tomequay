package app

import (
	"reflect"
	"testing"

	"terminal-pdf-reader/internal/pdf"
)

func TestOutlineFilter(t *testing.T) {
	entries := []pdf.OutlineEntry{
		{Title: "Part One", Page: 0},
		{Title: "Quantum Bits", Page: 12, Depth: 1},
		{Title: "Quantum Gates", Page: 20, Depth: 2},
		{Title: "Classical Bits", Page: 40},
		{Title: "ÉTUDE", Page: 50},
	}
	for _, tc := range []struct {
		name, query string
		want        []int
		selected    int
	}{
		{"all titles", "", []int{0, 1, 2, 3, 4}, 1},
		{"case insensitive", "qUaNtUm", []int{1, 2}, 0},
		{"substring", "bits", []int{1, 3}, 0},
		{"unresolved title remains visible", "part", []int{0}, -1},
		{"Unicode case", "étude", []int{4}, 0},
		{"no titles", "missing", []int{}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outlineView{entries: entries, filter: tc.query, page: 15}
			// Reuse an existing result slice just as incremental filter edits do.
			p.filtered = []int{4, 3, 2, 1, 0}
			p.refilter()
			if !reflect.DeepEqual(p.filtered, tc.want) || p.selected != tc.selected {
				t.Fatalf("filter = %v, selected %d; want %v, %d", p.filtered, p.selected, tc.want, tc.selected)
			}
		})
	}
}

func TestOutlinePreselect(t *testing.T) {
	entries := []pdf.OutlineEntry{
		{Title: "Unavailable", Page: 0},
		{Title: "Part", Page: 10},
		{Title: "Chapter", Page: 10, Depth: 1},
		{Title: "Section", Page: 10, Depth: 2},
		{Title: "Next chapter", Page: 20, Depth: 1},
		{Title: "Unavailable section", Page: 0, Depth: 2},
		{Title: "Later section", Page: 30, Depth: 2},
		{Title: "Earlier destination", Page: 15, Depth: 1},
	}
	for _, tc := range []struct {
		name     string
		filtered []int
		page     int
		want     int
	}{
		{"no entries", nil, 1, -1},
		{"unresolved only", []int{0, 5}, 20, -1},
		{"before first destination", []int{0, 1, 2, 3, 4}, 1, 1},
		{"deepest on same page", []int{0, 1, 2, 3, 4}, 10, 3},
		{"within section", []int{0, 1, 2, 3, 4}, 19, 3},
		{"next chapter", []int{0, 1, 2, 3, 4, 5, 6}, 24, 4},
		{"after last destination", []int{0, 1, 2, 3, 4, 5, 6}, 100, 6},
		{"destinations not sorted", []int{1, 4, 6, 7}, 25, 1},
		{"filtered current section", []int{2, 4, 6}, 35, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outlinePreselect(entries, tc.filtered, tc.page); got != tc.want {
				t.Fatalf("preselect = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestOutlineSelectionSkipsMissingPages(t *testing.T) {
	entries := []pdf.OutlineEntry{{Title: "Unavailable"}, {Title: "First", Page: 2}, {Title: "Unavailable section"}, {Title: "Last", Page: 5}, {Title: "Unavailable end"}}
	for _, tc := range []struct {
		name                string
		filtered            []int
		selected, dir, want int
	}{
		{"down skips missing page", []int{0, 1, 2, 3, 4}, 1, 1, 3},
		{"up skips missing page", []int{0, 1, 2, 3, 4}, 3, -1, 1},
		{"stays at last usable page", []int{0, 1, 2, 3, 4}, 3, 1, 3},
		{"stays at first usable page", []int{0, 1, 2, 3, 4}, 1, -1, 1},
		{"initial down", []int{0, 1, 2, 3, 4}, -1, 1, 1},
		{"initial up", []int{0, 1, 2, 3, 4}, -1, -1, 3},
		{"all unresolved down", []int{0, 2, 4}, -1, 1, -1},
		{"all unresolved up", []int{0, 2, 4}, -1, -1, -1},
		{"empty", nil, -1, 1, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outlineView{entries: entries, filtered: tc.filtered, selected: tc.selected}
			p.move(tc.dir)
			if p.selected != tc.want {
				t.Fatalf("selected = %d; want %d", p.selected, tc.want)
			}
		})
	}
}
