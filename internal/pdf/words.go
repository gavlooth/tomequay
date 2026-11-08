package pdf

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Word is one extracted word in bbox-layout reading order. Its rectangle is in
// PDF points from the top-left of the displayed, rotated crop box. Line is a
// zero-based line identifier; separate bbox-layout lines have distinct IDs.
type Word struct {
	Text                   string
	XMin, YMin, XMax, YMax float64
	Line                   int
}

// PageWords describes the displayed, rotated crop box and its extractable words.
// Image-only pages have no words; extraction does not perform OCR.
type PageWords struct {
	Width, Height float64
	Words         []Word
	info          DocumentInfo
}

// PixelSize returns the full rendered pixel dimensions for this page and fit
// request, before pixel cropping. It follows RenderPage's actual-page scale-axis
// choice, using precise extraction dimensions for raster rounding rather than
// pdfinfo's rounded dimensions. Invalid requests return (0, 0). Scale word
// coordinates by these dimensions / Width and Height, then subtract the visible
// pixel-crop origin.
func (p PageWords) PixelSize(r RenderRequest) (w, h int) {
	size, err := scalePage(p.info, r)
	if err != nil || !positiveFinite(p.Width) || !positiveFinite(p.Height) {
		return 0, 0
	}
	pixels, fitWidth := size.scaleX, true
	if pixels < 0 {
		pixels, fitWidth = size.scaleY, false
	}
	if p.info.PageRotationDegrees%180 != 0 {
		fitWidth = !fitWidth
	}
	var other float64
	if fitWidth {
		other = math.Ceil(p.Height * float64(pixels) / p.Width)
	} else {
		other = math.Ceil(p.Width * float64(pixels) / p.Height)
	}
	if !positiveFinite(other) || other > math.MaxInt32 {
		return 0, 0
	}
	if fitWidth {
		return pixels, int(other)
	}
	return int(other), pixels
}

// PageWords extracts one 1-based page's words with Poppler's bbox-layout reading
// order and line boundaries. Crop-box offsets and rotation are already applied
// to Poppler's word boxes; only its unrotated page dimensions need swapping.
// Boxes crossing the visible page boundary are clipped to that boundary.
func (t Tools) PageWords(ctx context.Context, path, password string, page int) (PageWords, error) {
	info, err := t.checkedPage(ctx, path, password, page)
	if err != nil {
		return PageWords{}, err
	}
	if page != 1 {
		info, err = t.info(ctx, path, password, page)
		if err != nil {
			return PageWords{}, err
		}
	}
	number := strconv.Itoa(page)
	data, err := run(ctx, t.pdftotext, path, password, "-bbox-layout", "-cropbox", "-f", number, "-l", number, "-enc", "UTF-8", "-eol", "unix")
	if err != nil {
		return PageWords{}, err
	}
	words, err := parsePageWords(data, info.PageRotationDegrees)
	if err != nil {
		return PageWords{}, fmt.Errorf("%w: pdftotext word geometry: %v", ErrInvalidPDF, err)
	}
	words.info = info.DocumentInfo
	return words, nil
}

func parsePageWords(data []byte, rotation int) (PageWords, error) {
	rotation = (rotation%360 + 360) % 360
	if rotation%90 != 0 {
		return PageWords{}, fmt.Errorf("unsupported page rotation %d", rotation)
	}
	// encoding/xml is strict by default and never resolves external DTDs or
	// entities. In particular, Poppler's XHTML doctype needs no network access.
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var result PageWords
	var stack []string
	var word Word
	var text strings.Builder
	pageSeen, rootSeen := false, false
	pageDepth, lineDepth, wordDepth, line := 0, 0, 0, -1
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return PageWords{}, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			if wordDepth != 0 {
				return PageWords{}, fmt.Errorf("nested markup in word")
			}
			if len(stack) == 0 {
				if rootSeen || token.Name.Local != "html" {
					return PageWords{}, fmt.Errorf("expected one XHTML document")
				}
				rootSeen = true
			}
			parent := ""
			if len(stack) != 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, token.Name.Local)
			switch token.Name.Local {
			case "page":
				if pageSeen || parent != "doc" {
					return PageWords{}, fmt.Errorf("expected exactly one page")
				}
				pageSeen, pageDepth = true, len(stack)
				result.Width, err = wordGeometryAttribute(token, "width")
				if err != nil {
					return PageWords{}, err
				}
				result.Height, err = wordGeometryAttribute(token, "height")
				if err != nil {
					return PageWords{}, err
				}
				if !positiveFinite(result.Width) || !positiveFinite(result.Height) {
					return PageWords{}, fmt.Errorf("page dimensions must be positive")
				}
				if rotation%180 != 0 {
					result.Width, result.Height = result.Height, result.Width
				}
			case "line":
				if pageDepth == 0 || lineDepth != 0 || parent != "block" {
					return PageWords{}, fmt.Errorf("line outside page block")
				}
				line++
				lineDepth = len(stack)
			case "word":
				if lineDepth == 0 || parent != "line" {
					return PageWords{}, fmt.Errorf("word outside line")
				}
				word = Word{Line: line}
				for _, attribute := range []struct {
					name  string
					value *float64
				}{{"xMin", &word.XMin}, {"yMin", &word.YMin}, {"xMax", &word.XMax}, {"yMax", &word.YMax}} {
					*attribute.value, err = wordGeometryAttribute(token, attribute.name)
					if err != nil {
						return PageWords{}, err
					}
				}
				if word.XMax <= word.XMin || word.YMax <= word.YMin {
					return PageWords{}, fmt.Errorf("word rectangle must have positive area")
				}
				text.Reset()
				wordDepth = len(stack)
			}
		case xml.CharData:
			if wordDepth != 0 {
				text.Write(token)
			} else if len(stack) == 0 && strings.TrimSpace(string(token)) != "" {
				return PageWords{}, fmt.Errorf("text outside XHTML document")
			}
		case xml.EndElement:
			if len(stack) == wordDepth {
				word.Text = text.String()
				if strings.IndexFunc(word.Text, unicode.IsSpace) >= 0 {
					word.Text = strings.Join(strings.Fields(word.Text), " ")
				}
				word.XMin = max(0, word.XMin)
				word.YMin = max(0, word.YMin)
				word.XMax = min(result.Width, word.XMax)
				word.YMax = min(result.Height, word.YMax)
				if word.Text != "" && word.XMax > word.XMin && word.YMax > word.YMin {
					result.Words = append(result.Words, word)
				}
				wordDepth = 0
			}
			if len(stack) == lineDepth {
				lineDepth = 0
			}
			if len(stack) == pageDepth {
				pageDepth = 0
			}
			stack = stack[:len(stack)-1]
		}
	}
	if !pageSeen {
		return PageWords{}, fmt.Errorf("missing page")
	}
	return result, nil
}

func wordGeometryAttribute(element xml.StartElement, name string) (float64, error) {
	var value string
	found := false
	for _, attribute := range element.Attr {
		if attribute.Name.Local == name {
			if found {
				return 0, fmt.Errorf("duplicate %s attribute", name)
			}
			value, found = attribute.Value, true
		}
	}
	if !found {
		return 0, fmt.Errorf("missing %s attribute", name)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("invalid %s attribute", name)
	}
	return number, nil
}
