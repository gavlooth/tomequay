package history

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	query := "quantum"
	e := Entry{Path: "/books/a\nbook.pdf", Page: 200, Pages: 400, Fit: "custom", Zoom: 2.4, ScrollX: .3, ScrollY: .8, WidthFit: true, Dark: true, Size: 12000, Mtime: 123, Opened: time.Unix(1730000000, 123).UTC()}
	want, err := s.Save(Changes{Files: []Entry{e}, Commands: []string{"200", "dark"}, Searches: []string{"quantum"}, LastSearch: &query})
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(s.directory).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(got.Files, []Entry{e}) || got.LastSearch != query {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestCapsAndConsecutivePromptDuplicates(t *testing.T) {
	s := New(t.TempDir())
	var c Changes
	for i := range 510 {
		c.Files = append(c.Files, Entry{Path: fmt.Sprintf("/book-%03d.pdf", i), Opened: time.Unix(int64(i), 0)})
		text := fmt.Sprintf("query %d", i)
		c.Commands = append(c.Commands, text, text)
		c.Searches = append(c.Searches, "", text, text)
	}
	data, err := s.Save(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Files) != 500 || data.Files[0].Path != "/book-509.pdf" || data.Files[499].Path != "/book-010.pdf" {
		t.Fatalf("file cap: %d %+v", len(data.Files), data.Files)
	}
	if len(data.Commands) != 200 || data.Commands[0] != "query 310" || !reflect.DeepEqual(data.Commands, data.Searches) {
		t.Fatalf("prompt caps: %+v %+v", data.Commands, data.Searches)
	}
}

func TestCorruptAndUnknownFilesIgnored(t *testing.T) {
	for _, text := range []string{"{broken", `{"version":99,"files":[{"path":"/unknown.pdf"}]}`, `{"version":1,"files":[{"path":"relative.pdf"}]}`} {
		t.Run(text, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "history.json"), []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			s := New(dir)
			data, err := s.Load()
			if err != nil || len(data.Files) != 0 {
				t.Fatalf("load: %+v, %v", data, err)
			}
			data, err = s.Save(Changes{Files: []Entry{{Path: "/new.pdf", Page: 2}}})
			if err != nil || len(data.Files) != 1 || data.Files[0].Path != "/new.pdf" {
				t.Fatalf("recovery: %+v, %v", data, err)
			}
		})
	}
}

func TestConcurrentWritersMergeUnderFlock(t *testing.T) {
	dir := t.TempDir()
	var workers sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan error, 24)
	for i := range 24 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := New(dir).Save(Changes{Files: []Entry{{Path: fmt.Sprintf("/book-%d.pdf", i), Page: i + 1}}, Commands: []string{fmt.Sprint(i)}})
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := New(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Files) != 24 || len(data.Commands) != 24 {
		t.Fatalf("lost concurrent update: %+v", data)
	}
	// A later writer updates only its path; all peer paths survive.
	data, err = New(dir).Save(Changes{Files: []Entry{{Path: "/book-0.pdf", Page: 99}}})
	if err != nil || len(data.Files) != 24 {
		t.Fatalf("last writer: %+v %v", data, err)
	}
	for _, e := range data.Files {
		if e.Path == "/book-0.pdf" && e.Page != 99 {
			t.Fatal("last writer did not win")
		}
	}
}

func TestRecentMigrationRunsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "recent")
	if err := os.WriteFile(old, []byte("/newest.pdf\n/older.pdf\nrelative.pdf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	data, err := s.Load()
	if err != nil || len(data.Files) != 2 || data.Files[0].Path != "/newest.pdf" {
		t.Fatalf("migration: %+v %v", data, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old recent was not removed: %v", err)
	}
	if err := os.WriteFile(old, []byte("/not-migrated.pdf\n"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = s.Load()
	if err != nil || len(data.Files) != 2 {
		t.Fatalf("second migration: %+v %v", data, err)
	}
}

func TestOlderReaderKeepsMostRecentOpenedTime(t *testing.T) {
	s := New(t.TempDir())
	newer := time.Unix(200, 0)
	if _, err := s.Save(Changes{Files: []Entry{{Path: "/one.pdf", Page: 10, Opened: newer}}}); err != nil {
		t.Fatal(err)
	}
	data, err := s.Save(Changes{Files: []Entry{{Path: "/one.pdf", Page: 20, Opened: time.Unix(100, 0)}}})
	if err != nil || len(data.Files) != 1 || data.Files[0].Page != 20 || !data.Files[0].Opened.Equal(newer) {
		t.Fatalf("older reader changed last-opened time: %+v %v", data, err)
	}
}
