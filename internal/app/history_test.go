package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tomequay/internal/history"
	"tomequay/internal/keys"
	"tomequay/internal/tabs"
	"tomequay/internal/terminal"
)

func TestRestoredHistoryClampsReplacedShorterPDF(t *testing.T) {
	m, _ := testModel(t, 100)
	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	id := addTab(m, path)
	m.tabs.UpdateDocument(path, 12)
	m.historyData.Files = []history.Entry{{Path: path, Page: 200, Pages: 400, Fit: "custom", Zoom: 2.4, ScrollX: .3, ScrollY: .8, WidthFit: true, Dark: true, Size: 9000, Mtime: 1, Opened: time.Now()}}
	m.restoreHistory(id, m.historyData.Files[0])
	view, _ := m.tabs.View(id)
	if view.Page != 12 || view.Fit != tabs.Custom || view.Zoom != 2.4 || view.ScrollX != .3 || view.ScrollY != .8 || !m.state(id).dark || !m.state(id).widthFit {
		t.Fatalf("restored view: %+v %+v", view, m.state(id))
	}
}

func TestOpenRestoresHistoryAndExplicitPageOverrides(t *testing.T) {
	m, _ := testModel(t, 100)
	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, []byte("loaded document"), 0600); err != nil {
		t.Fatal(err)
	}
	addTab(m, path)
	m.historyLast = historySnapshot{tab: 1, view: tabs.View{Page: 1, Fit: tabs.FitPage, Zoom: 1}}
	m.historyData.Files = []history.Entry{{Path: path, Page: 77, Pages: 100, Fit: "width", Zoom: 1, ScrollY: .6, Dark: true}}
	m.open(path, 0, "")
	opened, _ := m.tabs.Active()
	if opened.View.Page != 77 || opened.View.Fit != tabs.FitWidth || opened.View.ScrollY != .6 || !m.state(opened.ID).dark {
		t.Fatalf("restore: %+v", opened)
	}
	m.open(path, 3, "")
	opened, _ = m.tabs.Active()
	if opened.View.Page != 3 || opened.View.Fit != tabs.FitPage || m.state(opened.ID).dark {
		t.Fatalf("explicit page: %+v", opened)
	}
}

func TestDuplicateTabsSaveMostRecentlyActiveView(t *testing.T) {
	m, _ := testModel(t, 100)
	first := addTab(m, "/one.pdf")
	feed(m, "20G+zd")
	second := m.duplicateTab(first)
	feed(m, "80G")
	m.tabs.Focus(first)
	m.recordHistory()
	m.saveHistory(true)
	data, err := m.historyStore.Load()
	if err != nil || len(data.Files) != 1 || data.Files[0].Page != 20 || !data.Files[0].Dark || data.Files[0].Zoom != 1.2 {
		t.Fatalf("first active: %+v %v", data, err)
	}
	m.closeTab(second)
	data, err = m.historyStore.Load()
	if err != nil || len(data.Files) != 1 || data.Files[0].Page != 20 {
		t.Fatalf("inactive close overwrote view: %+v %v", data, err)
	}
}

func TestQuitSavesUnchangedLiveViewAfterAnotherReaderWrites(t *testing.T) {
	m, _ := testModel(t, 100)
	addTab(m, "/one.pdf")
	feed(m, "20G")
	m.saveHistory(true)
	if _, err := m.historyStore.Save(history.Changes{Files: []history.Entry{{Path: "/one.pdf", Page: 88}}}); err != nil {
		t.Fatal(err)
	}
	// A quit must save the local view even when the debounce already saved it.
	m.saveHistory(true)
	data, err := m.historyStore.Load()
	if err != nil || len(data.Files) != 1 || data.Files[0].Page != 20 {
		t.Fatalf("quit did not save the live view: %+v %v", data, err)
	}
}

func TestHistoryPanelMissingFilesAndForget(t *testing.T) {
	m, _ := testModel(t, 100)
	path := filepath.Join(t.TempDir(), "present.pdf")
	if err := os.WriteFile(path, []byte("not touched"), 0600); err != nil {
		t.Fatal(err)
	}
	m.historyData.Files = []history.Entry{{Path: "/missing.pdf", Page: 9, Opened: time.Now()}, {Path: path, Page: 4, Opened: time.Now().Add(-time.Hour)}}
	m.showHistory()
	m.historyKey(keys.Key{Special: keys.Enter})
	if m.historyPanel == nil || len(m.tabs.Tabs()) != 0 {
		t.Fatal("opened missing path")
	}
	m.historyKey(keys.Key{Rune: 'j'})
	m.historyKey(keys.Key{Rune: 'd'})
	if len(m.historyPanel.filtered) != 1 {
		t.Fatal("entry not removed")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "not touched" {
		t.Fatalf("file changed: %q %v", b, err)
	}
}

func TestPromptHistoryPersistsAcrossReaderModels(t *testing.T) {
	m, _ := testModel(t, 100)
	feed(m, ":history")
	m.key(keys.Key{Special: keys.Enter})
	m.historyPanel = nil
	feed(m, "/quantum")
	m.key(keys.Key{Special: keys.Enter})
	m.saveHistory(true)
	m.loadHistory()
	feed(m, ":")
	m.key(keys.Key{Special: keys.Up})
	if m.decoder.Pending() != ":history" {
		t.Fatal(m.decoder.Pending())
	}
	m.key(keys.Key{Special: keys.Esc})
	feed(m, "?")
	m.key(keys.Key{Special: keys.Up})
	if m.decoder.Pending() != "?quantum" {
		t.Fatal(m.decoder.Pending())
	}
}

func TestRelativeHistoryTime(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{{0, "just now"}, {3 * time.Minute, "3 min ago"}, {2 * time.Hour, "2 h ago"}, {48 * time.Hour, "2 d ago"}} {
		if got := relativeTime(now.Add(-tc.age), now); got != tc.want {
			t.Fatalf("age %s: %s", tc.age, got)
		}
	}
}

func TestHistoryMouseSelectsBeforeOpening(t *testing.T) {
	m, _ := testModel(t, 100)
	path := filepath.Join(t.TempDir(), "book.pdf")
	if err := os.WriteFile(path, []byte("loaded document"), 0600); err != nil {
		t.Fatal(err)
	}
	addTab(m, path)
	m.showHistory()
	click := terminal.MouseEvent{Kind: terminal.MousePress, Button: terminal.ButtonLeft, Column: 5, Row: 2}
	m.mouse(click)
	if len(m.tabs.Tabs()) != 1 || m.historyPanel == nil {
		t.Fatal("single click opened a file instead of selecting it")
	}
	m.mouse(click)
	if len(m.tabs.Tabs()) != 2 || m.historyPanel != nil {
		t.Fatal("double click did not open an independent tab")
	}
}
