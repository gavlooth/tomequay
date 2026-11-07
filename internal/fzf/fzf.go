// Package fzf runs the optional fuzzy picker and supplies its PDF candidates.
package fzf

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

var ErrUnavailable = errors.New("fzf is not installed; use o, gb, gO, or / instead")

// Candidate keeps machine-readable fields separate from the displayed label.
// Encoding the path lets tabs, newlines, and shell characters remain literal.
type Candidate struct {
	Path  string
	Page  int
	ID    uint64
	Label string
}

func (c Candidate) Line() string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.Path)) + "\t" + strconv.Itoa(c.Page) + "\t" + strconv.FormatUint(c.ID, 10) + "\t" + DisplayText(c.Label)
}

func Parse(line string) (Candidate, error) {
	fields := strings.SplitN(line, "\t", 4)
	if len(fields) != 4 {
		return Candidate{}, errors.New("invalid fzf selection")
	}
	path, err := base64.RawURLEncoding.DecodeString(fields[0])
	if err != nil {
		return Candidate{}, fmt.Errorf("invalid fzf path: %w", err)
	}
	page, err := strconv.Atoi(fields[1])
	if err != nil || page < 1 {
		return Candidate{}, errors.New("invalid fzf page")
	}
	id, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return Candidate{}, errors.New("invalid fzf item")
	}
	return Candidate{Path: string(path), Page: page, ID: id, Label: fields[3]}, nil
}

// DisplayText preserves leading indentation without allowing terminal escapes
// or record separators in names to become picker syntax.
func DisplayText(text string) string {
	var out strings.Builder
	for _, r := range text {
		switch r {
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}

// Request streams candidates while the user filters them. Items must stop when
// yield returns false or the context is canceled.
type Request struct {
	Header, Prompt, Query, Password, CacheDirectory string
	Popup, Multi                                    bool
	Items                                           func(context.Context, func(Candidate) bool) error
}

func Available() error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return ErrUnavailable
	}
	return nil
}

// Run starts fzf directly, never through a shell. Only fzf's preview facility
// invokes a shell, with a quoted executable and encoded placeholder arguments.
func Run(ctx context.Context, request Request) ([]Candidate, error) {
	path, err := exec.LookPath("fzf")
	if err != nil {
		return nil, ErrUnavailable
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("fzf terminal: %w", err)
	}
	defer tty.Close()
	query := base64.RawURLEncoding.EncodeToString([]byte(request.Query))
	preview := shellQuote(executable) + " --preview-page {1} {2} " + shellQuote(query)
	args := []string{"--no-ansi", "--delimiter=\t", "--with-nth=4..", "--layout=reverse", "--no-sort", "--header=" + DisplayText(request.Header), "--prompt=" + request.Prompt, "--preview=" + preview, "--preview-window=right:55%:wrap", "--with-shell=sh -c", "--bind=esc:abort"}
	if request.Popup {
		args = append(args, "--tmux=center,90%,85%")
	} else {
		args = append(args, "--no-tmux")
	}
	if request.Multi {
		args = append(args, "--multi", "--bind=tab:toggle+down,shift-tab:toggle+up")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "FZF_DEFAULT_OPTS=", "FZF_DEFAULT_OPTS_FILE=", "PDFR_PREVIEW_PASSWORD="+request.Password, "PDFR_PREVIEW_CACHE="+request.CacheDirectory)
	cmd.Stderr = tty
	var selected bytes.Buffer
	cmd.Stdout = &selected
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		return nil, fmt.Errorf("fzf: %w", err)
	}
	itemsDone := make(chan error, 1)
	go func() {
		itemsDone <- request.Items(ctx, func(item Candidate) bool {
			if ctx.Err() != nil {
				return false
			}
			_, err := io.WriteString(input, item.Line()+"\n")
			return err == nil
		})
		input.Close()
	}()
	err = cmd.Wait()
	cancel()
	itemsErr := <-itemsDone
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && (exit.ExitCode() == 130 || exit.ExitCode() == 1) {
			return nil, nil
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}
	if itemsErr != nil && !errors.Is(itemsErr, context.Canceled) {
		return nil, itemsErr
	}
	var result []Candidate
	scanner := bufio.NewScanner(&selected)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		item, err := Parse(scanner.Text())
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, scanner.Err()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// Roots returns distinct absolute scan roots in priority order. An explicit
// PDFR_ROOTS replaces the defaults; ~ and ~/ are expanded without a shell.
func Roots(value, documentDirectory, home, cwd string) []string {
	var paths []string
	if value != "" {
		paths = filepath.SplitList(value)
	} else {
		if documentDirectory == "" {
			documentDirectory = cwd
		}
		paths = append(paths, documentDirectory)
		if home != "" {
			paths = append(paths, filepath.Join(home, "Documents"), filepath.Join(home, "Downloads"), home)
		}
	}
	seen := make(map[string]bool)
	var result []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		if path == "~" {
			path = home
		} else if strings.HasPrefix(path, "~/") {
			path = filepath.Join(home, path[2:])
		}
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			result = append(result, path)
		}
	}
	return result
}

// Files emits recent paths first, then streams fd (or find) output. At most
// 5000 distinct PDFs are offered, and only three directory levels are scanned.
func Files(ctx context.Context, recent, roots []string, yield func(Candidate) bool) error {
	seen := make(map[string]bool)
	emit := func(path string) bool {
		path = filepath.Clean(path)
		if seen[path] || !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return true
		}
		seen[path] = true
		return yield(Candidate{Path: path, Page: 1, Label: path}) && len(seen) < 5000
	}
	for _, path := range recent {
		if !emit(path) {
			return nil
		}
	}
	for _, root := range roots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stat, err := os.Stat(root); err != nil || !stat.IsDir() {
			continue
		}
		path, err := exec.LookPath("fd")
		args := []string{"--type", "f", "--extension", "pdf", "--ignore-case", "--max-depth", "3", "--print0", "--absolute-path", ".", root}
		if err != nil {
			path, err = exec.LookPath("find")
			args = []string{root, "-maxdepth", "3", "-type", "f", "-iname", "*.pdf", "-print0"}
		}
		if err != nil {
			return errors.New("PDF discovery needs fd or find; recent files remain available")
		}
		scanCtx, stop := context.WithCancel(ctx)
		cmd := exec.CommandContext(scanCtx, path, args...)
		output, err := cmd.StdoutPipe()
		if err != nil {
			stop()
			return err
		}
		if err := cmd.Start(); err != nil {
			stop()
			return err
		}
		reader := bufio.NewReader(output)
		finished := false
		for {
			path, err := reader.ReadString(0)
			if err != nil {
				break
			}
			if !emit(strings.TrimSuffix(path, "\x00")) {
				finished = true
				stop()
				break
			}
		}
		_ = cmd.Wait() // Unreadable directories do not discard usable candidates.
		stop()
		if finished {
			return nil
		}
	}
	return ctx.Err()
}

// OutlineLabel keeps hierarchy whitespace visible in fzf's fourth column.
func OutlineLabel(title string, depth, page int) string {
	return strings.Repeat("  ", max(0, depth)) + title + fmt.Sprintf("  (p. %d)", page)
}

// MatchLines returns one item per matching line, with adjacent non-empty lines
// as context. Line numbers refer to the original extracted page text.
func MatchLines(text, query string, page int) []Candidate {
	if query == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	query = strings.ToLower(query)
	var items []Candidate
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), query) {
			continue
		}
		context := make([]string, 0, 3)
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			if text := strings.TrimSpace(lines[j]); text != "" {
				context = append(context, text)
			}
		}
		items = append(items, Candidate{Page: page, ID: uint64(i + 1), Label: fmt.Sprintf("%d:%d  %s", page, i+1, strings.Join(context, " · "))})
	}
	return items
}
