package fzf

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"tomequay/internal/pdf"
)

// PageText shares extracted pages between grep and preview helper processes.
// The caller creates a private session directory and removes it on exit.
func PageText(ctx context.Context, tools pdf.Tools, path, password string, page int, directory string) (string, error) {
	var name string
	if directory != "" {
		if stat, err := os.Stat(path); err == nil {
			key := fmt.Sprintf("%s\x00%d\x00%d\x00%d", path, stat.ModTime().UnixNano(), stat.Size(), page)
			name = filepath.Join(directory, fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
			if text, err := os.ReadFile(name); err == nil {
				return string(text), nil
			}
		}
	}
	text, err := tools.PageText(ctx, path, password, page)
	if err != nil {
		return "", err
	}
	if name != "" {
		file, err := os.CreateTemp(directory, ".text-*")
		if err == nil {
			temporary := file.Name()
			_, writeErr := io.WriteString(file, text)
			closeErr := file.Close()
			if writeErr == nil && closeErr == nil {
				_ = os.Rename(temporary, name)
			}
			_ = os.Remove(temporary)
		}
	}
	return text, nil
}

// Preview is the same-binary helper used by fzf. Encoded paths and queries are
// data, not shell syntax. Passwords travel only in the helper's environment.
func Preview(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 3 {
		return fmt.Errorf("--preview-page needs encoded-file page encoded-query")
	}
	path, err := base64.RawURLEncoding.DecodeString(args[0])
	if err != nil {
		return fmt.Errorf("preview path: %w", err)
	}
	page, err := strconv.Atoi(args[1])
	if err != nil || page < 1 {
		return fmt.Errorf("preview page must be positive")
	}
	query, err := base64.RawURLEncoding.DecodeString(args[2])
	if err != nil {
		return fmt.Errorf("preview query: %w", err)
	}
	tools, err := pdf.FindTools()
	if err != nil {
		return err
	}
	text, err := PageText(ctx, tools, string(path), os.Getenv("PDFR_PREVIEW_PASSWORD"), page, os.Getenv("PDFR_PREVIEW_CACHE"))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Page %d · %s\n\n%s", page, DisplayText(filepath.Base(string(path))), Highlight(text, string(query)))
	return err
}

// Highlight strips terminal commands from extracted text and highlights literal
// case-insensitive matches. Unicode case folding never slices inside a rune.
func Highlight(text, query string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, text)
	if query == "" {
		return text
	}
	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		return "\x1b[1;30;43m" + match + "\x1b[0m"
	})
}
