package pdf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrOutlineToolMissing means outline navigation needs the optional qpdf tool.
var ErrOutlineToolMissing = errors.New("install qpdf for PDF outlines (Arch: pacman -S qpdf; Debian/Ubuntu: apt install qpdf)")

// OutlineEntry is a document-order outline item. Page is 1-based, or 0 when
// the destination has no resolvable page. Depth starts at 0 for top-level items.
type OutlineEntry struct {
	Title string
	Page  int
	Depth int
}

type qpdfOutline struct {
	Title string        `json:"title"`
	Page  int           `json:"destpageposfrom1"`
	Kids  []qpdfOutline `json:"kids"`
}

func flattenOutline(items []qpdfOutline) []OutlineEntry {
	entries := make([]OutlineEntry, 0, len(items))
	var appendItems func([]qpdfOutline, int)
	appendItems = func(items []qpdfOutline, depth int) {
		for _, item := range items {
			entries = append(entries, OutlineEntry{Title: item.Title, Page: max(0, item.Page), Depth: depth})
			appendItems(item.Kids, depth+1)
		}
	}
	appendItems(items, 0)
	return entries
}

// Outline reads bookmarks with qpdf and flattens them depth-first in document
// order. Documents without bookmarks return an empty slice. qpdf is optional
// for other operations; a missing executable returns ErrOutlineToolMissing.
func (t Tools) Outline(ctx context.Context, path, password string) ([]OutlineEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.qpdf == "" {
		return nil, ErrOutlineToolMissing
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve PDF path: %w", err)
	}
	args := []string{"--json", "--json-key=outlines"}
	if password != "" {
		args = append(args, "--password="+password)
	}
	args = append(args, absolute)
	cmd := exec.CommandContext(ctx, t.qpdf, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("qpdf: %w", ctx.Err())
	}
	var exit *exec.ExitError
	// qpdf's exit status 3 means usable output with warnings, not failure.
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 3) {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, ErrOutlineToolMissing
		}
		message := strings.TrimSpace(stderr.String())
		kind := err
		switch {
		case strings.Contains(message, "invalid password"):
			kind = ErrWrongPassword
			if password == "" {
				kind = ErrPasswordRequired
			}
		case strings.Contains(message, "not a PDF file"),
			strings.Contains(message, "unable to find trailer dictionary"),
			strings.Contains(message, "can't find startxref"),
			strings.Contains(message, "unable to find /Root dictionary"),
			strings.Contains(message, "unable to find /Pages dictionary"):
			kind = ErrInvalidPDF
		}
		if message == "" {
			return nil, fmt.Errorf("qpdf: %w", kind)
		}
		return nil, fmt.Errorf("qpdf: %w: %s", kind, message)
	}
	var result struct {
		Outlines []qpdfOutline `json:"outlines"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("read qpdf outlines: %w", err)
	}
	return flattenOutline(result.Outlines), nil
}
