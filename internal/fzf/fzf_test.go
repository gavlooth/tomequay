package fzf

import (
	"reflect"
	"strings"
	"testing"
)

func TestCandidateRoundTrip(t *testing.T) {
	for _, path := range []string{"/tmp/日本語 λ.pdf", "/tmp/a\tb\nc.pdf", "/tmp/a'$(touch bad).pdf"} {
		item := Candidate{Path: path, Page: 17, ID: 42, Label: "  " + path}
		line := item.Line()
		if strings.Count(line, "\t") != 3 || strings.ContainsAny(line, "\r\n") {
			t.Fatalf("record contains raw separators: %q", line)
		}
		got, err := Parse(line)
		if err != nil || got.Path != path || got.Page != 17 || got.ID != 42 || got.Label != DisplayText(item.Label) {
			t.Errorf("round trip = %#v, %v", got, err)
		}
	}
	for _, line := range []string{"missing fields", "%%%\t1\t0\tx", "YQ\t0\t0\tx", "YQ\t1\t-1\tx"} {
		if _, err := Parse(line); err == nil {
			t.Errorf("accepted invalid record %q", line)
		}
	}
}

func TestOutlineIndentation(t *testing.T) {
	label := OutlineLabel("第一章", 3, 12)
	item, err := Parse((Candidate{Path: "/book.pdf", Page: 12, Label: label}).Line())
	if err != nil || item.Label != "      第一章  (p. 12)" {
		t.Fatalf("outline indentation = %q, %v", item.Label, err)
	}
	if got := OutlineLabel("Root", -1, 1); got != "Root  (p. 1)" {
		t.Errorf("negative depth = %q", got)
	}
}

func TestMatchLinesContext(t *testing.T) {
	text := "Qubit at the top\n  next line\n\n previous\nTwo QUBITS\n following\nlast qubit"
	got := MatchLines(text, "qubit", 9)
	want := []Candidate{
		{Page: 9, ID: 1, Label: "9:1  Qubit at the top · next line"},
		{Page: 9, ID: 5, Label: "9:5  previous · Two QUBITS · following"},
		{Page: 9, ID: 7, Label: "9:7  following · last qubit"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("matches = %#v; want %#v", got, want)
	}
	if len(MatchLines(text, "", 1)) != 0 || len(MatchLines(text, "absent", 1)) != 0 {
		t.Error("empty or absent query produced matches")
	}
}

func TestRoots(t *testing.T) {
	for _, tc := range []struct {
		value, document, home, cwd string
		want                       []string
	}{
		{"", "/papers", "/home/reader", "/work", []string{"/papers", "/home/reader/Documents", "/home/reader/Downloads", "/home/reader"}},
		{"", "", "/home/reader", "/work", []string{"/work", "/home/reader/Documents", "/home/reader/Downloads", "/home/reader"}},
		{"~/Books:/papers::~/Books:relative:~", "", "/home/reader", "/work", []string{"/home/reader/Books", "/papers", "/work/relative", "/home/reader"}},
		{"", "/home/reader", "/home/reader", "/work", []string{"/home/reader", "/home/reader/Documents", "/home/reader/Downloads"}},
	} {
		if got := Roots(tc.value, tc.document, tc.home, tc.cwd); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Roots(%q) = %q; want %q", tc.value, got, tc.want)
		}
	}
}

func TestHighlightLiteralUnicode(t *testing.T) {
	got := Highlight("Kelvin kELVIN .+\n\x1b[2J", "kelvin")
	if strings.Count(got, "\x1b[1;30;43m") != 2 || strings.Contains(got, "\x1b[2J") {
		t.Errorf("unicode highlight or terminal cleanup = %q", got)
	}
	if got := Highlight("literal .+ text", ".+"); got != "literal \x1b[1;30;43m.+\x1b[0m text" {
		t.Errorf("query was not literal: %q", got)
	}
}
