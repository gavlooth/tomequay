// Package pdf reads PDF metadata, images, and text with Poppler, and outlines with qpdf.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrPasswordRequired = errors.New("PDF password required")
	ErrWrongPassword    = errors.New("incorrect PDF password")
	ErrInvalidPDF       = errors.New("invalid PDF")
	ErrPageOutOfRange   = errors.New("PDF page out of range")
)

// Tools holds executable paths resolved by FindTools. Its zero value is not usable.
// Tools can be shared between goroutines; each call starts its own processes.
type Tools struct {
	pdfinfo, pdftoppm, pdftotext string
	qpdf                         string
}

// FindTools locates the required Poppler executables and optional qpdf on PATH.
func FindTools() (Tools, error) {
	var t Tools
	var missing []string
	for _, tool := range []struct {
		name string
		path *string
	}{{"pdfinfo", &t.pdfinfo}, {"pdftoppm", &t.pdftoppm}, {"pdftotext", &t.pdftotext}} {
		path, err := exec.LookPath(tool.name)
		if err != nil {
			missing = append(missing, tool.name)
			continue
		}
		*tool.path = path
	}
	t.qpdf, _ = exec.LookPath("qpdf")
	if len(missing) != 0 {
		return Tools{}, fmt.Errorf("missing Poppler tools: %s; install poppler (Arch: pacman -S poppler; Debian/Ubuntu: apt install poppler-utils)", strings.Join(missing, ", "))
	}
	return t, nil
}

// DocumentInfo describes the document and the unrotated size of its first page.
type DocumentInfo struct {
	PageCount                         int
	Title                             string
	Encrypted                         bool
	PageWidthPoints, PageHeightPoints float64
	PageRotationDegrees               int
}

// RenderRequest selects a 1-based page and a pixel fit box. Zoom must be positive;
// 1 fits the page inside the box, and larger values enlarge that fitted image.
type RenderRequest struct {
	Path, Password            string
	Page                      int
	WidthPixels, HeightPixels int
	Zoom                      float64
	CropX, CropY              int
	CropWidth, CropHeight     int
}

type pageInfo struct {
	DocumentInfo
}

var pageSizePattern = regexp.MustCompile(`^Page\s+(?:\d+\s+)?size:\s+([0-9.eE+-]+)\s+x\s+([0-9.eE+-]+)\s+pts`)
var pageRotationPattern = regexp.MustCompile(`^Page\s+(?:\d+\s+)?rot:\s+(-?\d+)`)

// Info reads the page count, title, encryption status, and first page size.
func (t Tools) Info(ctx context.Context, path, password string) (DocumentInfo, error) {
	info, err := t.info(ctx, path, password, 1)
	if err != nil {
		return DocumentInfo{}, err
	}
	return info.DocumentInfo, nil
}

func (t Tools) info(ctx context.Context, path, password string, page int) (pageInfo, error) {
	out, err := run(ctx, t.pdfinfo, path, password, "-enc", "UTF-8", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page))
	if err != nil {
		return pageInfo{}, err
	}
	var info pageInfo
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Pages":
			info.PageCount, _ = strconv.Atoi(value)
		case "Title":
			info.Title = value
		case "Encrypted":
			info.Encrypted = strings.HasPrefix(value, "yes")
		}
		if match := pageSizePattern.FindStringSubmatch(line); match != nil {
			info.PageWidthPoints, _ = strconv.ParseFloat(match[1], 64)
			info.PageHeightPoints, _ = strconv.ParseFloat(match[2], 64)
		}
		if match := pageRotationPattern.FindStringSubmatch(line); match != nil {
			info.PageRotationDegrees, _ = strconv.Atoi(match[1])
		}
	}
	if info.PageCount < 1 || !positiveFinite(info.PageWidthPoints) || !positiveFinite(info.PageHeightPoints) {
		return pageInfo{}, fmt.Errorf("%w: pdfinfo returned missing or invalid page dimensions/count", ErrInvalidPDF)
	}
	return info, nil
}

// RenderPage returns one PNG, preserving the page's aspect ratio and rotation.
// Positive CropWidth and CropHeight select a window in the scaled page's pixels.
// The window is intersected with the page; an empty intersection is an error.
// On cancellation or any other error, it returns no image bytes.
func (t Tools) RenderPage(ctx context.Context, r RenderRequest) ([]byte, error) {
	size, err := t.pageScale(ctx, r)
	if err != nil {
		return nil, err
	}
	page := strconv.Itoa(r.Page)
	options := []string{"-png", "-singlefile", "-cropbox", "-f", page, "-l", page, "-scale-to-x", strconv.Itoa(size.scaleX), "-scale-to-y", strconv.Itoa(size.scaleY)}
	if r.CropWidth > 0 && r.CropHeight > 0 {
		x, width := cropInterval(r.CropX, r.CropWidth, size.width)
		y, height := cropInterval(r.CropY, r.CropHeight, size.height)
		if width == 0 || height == 0 {
			return nil, errors.New("render crop does not intersect the scaled page")
		}
		options = append(options, "-x", strconv.Itoa(x), "-y", strconv.Itoa(y), "-W", strconv.Itoa(width), "-H", strconv.Itoa(height))
	}
	// With no output prefix, -singlefile writes the PNG directly to stdout.
	return run(ctx, t.pdftoppm, r.Path, r.Password, options...)
}

// ScaledPageSize computes the full scaled image size without reading the PDF.
// It ignores cropping and uses the supplied page dimensions and rotation.
// Invalid dimensions or zoom return (0, 0).
func ScaledPageSize(info DocumentInfo, r RenderRequest) (w, h int) {
	size, err := scalePage(info, r)
	if err != nil {
		return 0, 0
	}
	return size.width, size.height
}

type pageScale struct {
	width, height  int
	scaleX, scaleY int
}

func (t Tools) pageScale(ctx context.Context, r RenderRequest) (pageScale, error) {
	if r.WidthPixels <= 0 || r.HeightPixels <= 0 || !positiveFinite(r.Zoom) {
		return pageScale{}, errors.New("render requires positive pixel dimensions and finite positive zoom")
	}
	info, err := t.checkedPage(ctx, r.Path, r.Password, r.Page)
	if err != nil {
		return pageScale{}, err
	}
	if r.Page != 1 {
		info, err = t.info(ctx, r.Path, r.Password, r.Page)
		if err != nil {
			return pageScale{}, err
		}
	}
	return scalePage(info.DocumentInfo, r)
}

func scalePage(info DocumentInfo, r RenderRequest) (pageScale, error) {
	if r.WidthPixels <= 0 || r.HeightPixels <= 0 || !positiveFinite(r.Zoom) {
		return pageScale{}, errors.New("render requires positive pixel dimensions and finite positive zoom")
	}
	if !positiveFinite(info.PageWidthPoints) || !positiveFinite(info.PageHeightPoints) {
		return pageScale{}, errors.New("render requires finite positive page dimensions")
	}
	width, height := float64(r.WidthPixels)*r.Zoom, float64(r.HeightPixels)*r.Zoom
	// Poppler's scale axes refer to the page before its rotation is applied.
	rotated := info.PageRotationDegrees%180 != 0
	if rotated {
		width, height = height, width
	}
	size := pageScale{scaleX: -1, scaleY: -1}
	var pixels, other float64
	fitWidth := width/info.PageWidthPoints <= height/info.PageHeightPoints
	if fitWidth {
		pixels = math.Floor(width)
		other = math.Ceil(info.PageHeightPoints * pixels / info.PageWidthPoints)
	} else {
		pixels = math.Floor(height)
		other = math.Ceil(info.PageWidthPoints * pixels / info.PageHeightPoints)
	}
	// Poppler parses these arguments as signed 32-bit integers.
	if !positiveFinite(pixels) || pixels > math.MaxInt32 || !positiveFinite(other) || other > math.MaxInt32 {
		return pageScale{}, errors.New("rendered dimensions must be between 1 and 2147483647 pixels")
	}
	if fitWidth {
		size.scaleX = int(pixels)
		size.width, size.height = int(pixels), int(other)
	} else {
		size.scaleY = int(pixels)
		size.width, size.height = int(other), int(pixels)
	}
	if rotated {
		size.width, size.height = size.height, size.width
	}
	return size, nil
}

func cropInterval(start, length, total int) (int, int) {
	if start < 0 {
		if start <= -length {
			return 0, 0
		}
		length += start
		start = 0
	}
	if start >= total {
		return 0, 0
	}
	return start, min(length, total-start)
}

// PageText returns UTF-8 text from one 1-based page, without a page separator.
func (t Tools) PageText(ctx context.Context, path, password string, page int) (string, error) {
	if _, err := t.checkedPage(ctx, path, password, page); err != nil {
		return "", err
	}
	out, err := run(ctx, t.pdftotext, path, password, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-enc", "UTF-8", "-eol", "unix", "-nopgbrk")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SearchPages returns the 1-based pages containing a case-insensitive substring.
// Empty queries and image-only PDFs return an empty result. Matches do not span pages.
func (t Tools) SearchPages(ctx context.Context, path, password, query string) ([]int, error) {
	if _, err := t.Info(ctx, path, password); err != nil {
		return nil, err
	}
	if query == "" {
		return []int{}, nil
	}
	out, err := run(ctx, t.pdftotext, path, password, "-enc", "UTF-8", "-eol", "unix")
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	pages := make([]int, 0)
	page := 1
	for text := range strings.SplitSeq(string(out), "\f") {
		if strings.Contains(strings.ToLower(text), query) {
			pages = append(pages, page)
		}
		page++
	}
	return pages, nil
}

func (t Tools) checkedPage(ctx context.Context, path, password string, page int) (pageInfo, error) {
	if page < 1 {
		return pageInfo{}, fmt.Errorf("%w: page %d is less than 1", ErrPageOutOfRange, page)
	}
	info, err := t.info(ctx, path, password, 1)
	if err != nil {
		return pageInfo{}, err
	}
	if page > info.PageCount {
		return pageInfo{}, fmt.Errorf("%w: page %d, document has %d pages", ErrPageOutOfRange, page, info.PageCount)
	}
	return info, nil
}

func positiveFinite(n float64) bool {
	return n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n)
}

func run(ctx context.Context, tool, path, password string, options ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tool == "" {
		return nil, errors.New("Poppler tool path is empty; call pdf.FindTools first")
	}
	// Absolute paths cannot be mistaken for options or Poppler's stdin marker.
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve PDF path: %w", err)
	}
	args := options
	if password != "" {
		// Poppler offers -upw, but no password file or descriptor option.
		args = append(args, "-upw", password)
	}
	args = append(args, absolute)
	if filepath.Base(tool) == "pdftotext" {
		args = append(args, "-")
	}
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run() // Run waits for the process, including after CommandContext kills it.
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(tool), ctx.Err())
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		kind := err
		switch {
		case strings.Contains(message, "Incorrect password"):
			kind = ErrWrongPassword
			if password == "" {
				kind = ErrPasswordRequired
			}
		case strings.Contains(message, "May not be a PDF file"), strings.Contains(message, "Couldn't find trailer dictionary"), strings.Contains(message, "Couldn't read xref table"):
			kind = ErrInvalidPDF
		}
		if message == "" {
			return nil, fmt.Errorf("%s: %w", filepath.Base(tool), kind)
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(tool), kind, message)
	}
	// Successful commands may emit Syntax Warning lines; they are not failures.
	return stdout.Bytes(), nil
}
