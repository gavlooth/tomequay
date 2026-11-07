package keys

import (
	"reflect"
	"testing"
)

func typePrompt(d *Decoder, text string) {
	for _, r := range text {
		d.Feed(Key{Rune: r})
	}
}

func TestPromptHistoryPrefixWalking(t *testing.T) {
	var d Decoder
	d.SetHistory([]string{"e first.pdf", "dark", "e second.pdf", "e third.pdf"}, []string{"quantum", "query", "other"})
	typePrompt(&d, ":e ")
	for _, step := range []struct {
		key  Key
		want string
	}{
		{Key{Special: Up}, ":e third.pdf"},
		{Key{Rune: 'p', Ctrl: true}, ":e second.pdf"},
		{Key{Special: Up}, ":e first.pdf"},
		{Key{Special: Up}, ":e first.pdf"},
		{Key{Special: Down}, ":e second.pdf"},
		{Key{Rune: 'n', Ctrl: true}, ":e third.pdf"},
		{Key{Special: Down}, ":e "},
		{Key{Special: Down}, ":e "},
	} {
		if _, emitted := d.Feed(step.key); emitted || d.Pending() != step.want {
			t.Fatalf("walk: %q, want %q", d.Pending(), step.want)
		}
	}
	d.Feed(Key{Special: Esc})
	typePrompt(&d, "?qu")
	d.Feed(Key{Special: Up})
	if d.Pending() != "?query" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Up})
	if d.Pending() != "?quantum" {
		t.Fatal(d.Pending())
	}
	cmd, ok := d.Feed(Key{Special: Enter})
	if !ok || cmd.Action != SearchBackward || cmd.Text != "quantum" {
		t.Fatalf("submission: %+v %v", cmd, ok)
	}
}

func TestPromptHistoryEmptyPrefixAndUnmatchedPrefix(t *testing.T) {
	var d Decoder
	d.SetHistory([]string{"dark", "history"}, nil)
	typePrompt(&d, ":")
	d.Feed(Key{Special: Down})
	if d.Pending() != ":" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Up})
	if d.Pending() != ":history" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Down})
	if d.Pending() != ":" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Esc})
	typePrompt(&d, ":missing")
	d.Feed(Key{Special: Up})
	d.Feed(Key{Special: Down})
	if d.Pending() != ":missing" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Esc})
	typePrompt(&d, "/")
	d.Feed(Key{Special: Up})
	d.Feed(Key{Special: Down})
	if d.Pending() != "/" {
		t.Fatal(d.Pending())
	}
}

func TestPromptEditingResetsHistoryPrefix(t *testing.T) {
	var d Decoder
	d.SetHistory(nil, []string{"alpha", "alpine", "beta"})
	typePrompt(&d, "/al")
	d.Feed(Key{Special: Up})
	d.Feed(Key{Rune: 'u', Ctrl: true})
	if d.Mode() != Search || d.Pending() != "/" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Special: Up})
	if d.Pending() != "/beta" {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Rune: 'u', Ctrl: true})
	typePrompt(&d, "量子  two words  ")
	d.Feed(Key{Rune: 'w', Ctrl: true})
	if d.Pending() != "/量子  two " {
		t.Fatal(d.Pending())
	}
	d.Feed(Key{Rune: 'w', Ctrl: true})
	d.Feed(Key{Rune: 'w', Ctrl: true})
	d.Feed(Key{Rune: 'w', Ctrl: true})
	if d.Mode() != Search || d.Pending() != "/" {
		t.Fatal(d.Pending())
	}
}

func TestPromptHistoriesSeparateCapAndCollapse(t *testing.T) {
	var d Decoder
	for range 205 {
		typePrompt(&d, ":dark")
		d.Feed(Key{Special: Enter})
	}
	for _, input := range []string{"/", "/query", "?query", "/next"} {
		typePrompt(&d, input)
		d.Feed(Key{Special: Enter})
	}
	commands, searches := d.History()
	if !reflect.DeepEqual(commands, []string{"dark"}) || !reflect.DeepEqual(searches, []string{"query", "next"}) {
		t.Fatalf("histories: %v %v", commands, searches)
	}
	entries := make([]string, 205)
	for i := range entries {
		entries[i] = string(rune(1000 + i))
	}
	d.SetHistory(entries, entries)
	commands, searches = d.History()
	if len(commands) != 200 || len(searches) != 200 || commands[0] != entries[5] {
		t.Fatalf("caps: %v %v", commands, searches)
	}
	commands[0] = "changed"
	commands, _ = d.History()
	if commands[0] == "changed" {
		t.Fatal("history slice aliases decoder")
	}
}
