// Package history persists reading positions and prompt histories.
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

const (
	FileLimit   = 500
	PromptLimit = 200
)

// Entry is the last saved view of a canonical PDF path. Fit is page, width, or custom.
type Entry struct {
	Path     string    `json:"path"`
	Page     int       `json:"page"`
	Pages    int       `json:"pages"`
	Fit      string    `json:"fit"`
	Zoom     float64   `json:"zoom"`
	ScrollX  float64   `json:"scroll_x"`
	ScrollY  float64   `json:"scroll_y"`
	WidthFit bool      `json:"width_fit,omitempty"`
	Dark     bool      `json:"dark,omitempty"`
	Size     int64     `json:"size"`
	Mtime    int64     `json:"mtime"`
	Opened   time.Time `json:"opened"`
}

// Data keeps prompt histories oldest first and file entries newest first.
type Data struct {
	Version    int      `json:"version"`
	Files      []Entry  `json:"files"`
	Commands   []string `json:"commands"`
	Searches   []string `json:"searches"`
	LastSearch string   `json:"last_search,omitempty"`
}

// Changes contains only modified paths and newly submitted prompts, so saving
// from another process does not overwrite unrelated updates made since Load.
type Changes struct {
	Files      []Entry
	Remove     []string
	Commands   []string
	Searches   []string
	LastSearch *string
}

type Store struct{ directory string }

// New uses a supplied pdfr state directory. Default uses the XDG state location.
func New(directory string) *Store { return &Store{directory: directory} }

func Default() *Store {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return New("")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return New(filepath.Join(base, "pdfr"))
}

func (s *Store) locked(fn func() error) error {
	if s.directory == "" {
		return nil
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.directory, "history.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *Store) read() Data {
	var data Data
	b, err := os.ReadFile(filepath.Join(s.directory, "history.json"))
	if err != nil || json.Unmarshal(b, &data) != nil || data.Version != 1 {
		return Data{Version: 1}
	}
	files := data.Files[:0]
	seen := make(map[string]bool, len(data.Files))
	for _, e := range data.Files {
		if !filepath.IsAbs(e.Path) || seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		files = append(files, e)
	}
	data.Files = files
	normalize(&data)
	return data
}

// Load ignores corrupt or unknown-format history. The old recent file is read
// only when history.json is missing and removed only after a successful migration.
func (s *Store) Load() (Data, error) {
	data := Data{Version: 1}
	err := s.locked(func() error {
		if _, err := os.Stat(filepath.Join(s.directory, "history.json")); os.IsNotExist(err) {
			old := filepath.Join(s.directory, "recent")
			f, err := os.Open(old)
			if err == nil {
				scanner := bufio.NewScanner(f)
				now := time.Now()
				for scanner.Scan() && len(data.Files) < FileLimit {
					path := scanner.Text()
					if !filepath.IsAbs(path) {
						continue
					}
					if real, err := filepath.EvalSymlinks(path); err == nil {
						path = real
					}
					e := Entry{Path: path, Page: 1, Fit: "page", Zoom: 1, Opened: now.Add(-time.Duration(len(data.Files)) * time.Second)}
					if stat, err := os.Stat(path); err == nil {
						e.Size, e.Mtime = stat.Size(), stat.ModTime().UnixNano()
					}
					data.Files = append(data.Files, e)
				}
				f.Close()
				normalize(&data)
				if err := s.write(data); err != nil {
					return err
				}
				return os.Remove(old)
			}
		}
		data = s.read()
		return nil
	})
	return data, err
}

// Save performs a read-modify-write under flock. The last writer wins for each
// modified path; independent paths and prompt submissions from peers are kept.
func (s *Store) Save(changes Changes) (Data, error) {
	data := Data{Version: 1}
	err := s.locked(func() error {
		data = s.read()
		files := make(map[string]Entry, len(data.Files)+len(changes.Files))
		for _, e := range data.Files {
			files[e.Path] = e
		}
		for _, path := range changes.Remove {
			delete(files, path)
		}
		for _, e := range changes.Files {
			if filepath.IsAbs(e.Path) {
				if previous, ok := files[e.Path]; ok && previous.Opened.After(e.Opened) {
					e.Opened = previous.Opened
				}
				files[e.Path] = e
			}
		}
		data.Files = data.Files[:0]
		for _, e := range files {
			data.Files = append(data.Files, e)
		}
		data.Commands = append(data.Commands, changes.Commands...)
		data.Searches = append(data.Searches, changes.Searches...)
		if changes.LastSearch != nil {
			data.LastSearch = *changes.LastSearch
		}
		normalize(&data)
		return s.write(data)
	})
	return data, err
}

func normalize(data *Data) {
	data.Version = 1
	sort.Slice(data.Files, func(i, j int) bool {
		if data.Files[i].Opened.Equal(data.Files[j].Opened) {
			return data.Files[i].Path < data.Files[j].Path
		}
		return data.Files[i].Opened.After(data.Files[j].Opened)
	})
	if len(data.Files) > FileLimit {
		data.Files = data.Files[:FileLimit]
	}
	data.Commands = Prompts(data.Commands)
	data.Searches = Prompts(data.Searches)
}

// Prompts drops empty entries, collapses consecutive duplicates, and retains
// the newest 200 values. It returns its own slice.
func Prompts(entries []string) []string {
	result := make([]string, 0, min(len(entries), PromptLimit))
	for _, entry := range entries {
		if entry != "" && (len(result) == 0 || result[len(result)-1] != entry) {
			result = append(result, entry)
		}
	}
	if len(result) > PromptLimit {
		result = result[len(result)-PromptLimit:]
	}
	return result
}

func (s *Store) write(data Data) error {
	f, err := os.CreateTemp(s.directory, ".history-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	err = json.NewEncoder(f).Encode(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(s.directory, "history.json"))
}
