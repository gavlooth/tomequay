package pagefilter

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"slices"
)

var selectionTint = image.NewUniform(color.NRGBA{R: 0x40, G: 0x80, B: 0xff, A: 0x60})

// Selection returns a new PNG with the union of rectangles tinted translucent
// blue (#4080ff at 96/255 opacity) using source-over compositing. Rectangles use
// image pixel coordinates and are clipped to the page; overlaps are tinted only
// once. Dimensions and pixels outside the selection are unchanged. Calls may run
// together without changing the input bytes, rectangles, or previous results.
func Selection(data []byte, rectangles []image.Rectangle) ([]byte, error) {
	page, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode page PNG: %w", err)
	}

	clipped := make([]image.Rectangle, 0, len(rectangles))
	for _, rectangle := range rectangles {
		if rectangle = rectangle.Intersect(page.Bounds()); !rectangle.Empty() {
			clipped = append(clipped, rectangle)
		}
	}
	if len(clipped) == 0 {
		return bytes.Clone(data), nil
	}
	slices.SortFunc(clipped, func(a, b image.Rectangle) int {
		return a.Min.Y - b.Min.Y
	})

	var target draw.Image
	switch page := page.(type) {
	case *image.RGBA, *image.NRGBA, *image.RGBA64, *image.NRGBA64:
		target = page.(draw.Image)
	case *image.Gray16:
		target = image.NewRGBA64(page.Bounds())
		draw.Draw(target, page.Bounds(), page, page.Bounds().Min, draw.Src)
	case *image.Paletted:
		converted := image.NewNRGBA(page.Bounds())
		var palette [256]color.NRGBA
		for i, entry := range page.Palette {
			palette[i] = color.NRGBAModel.Convert(entry).(color.NRGBA)
		}
		// Expand palette colours directly to preserve even RGB values hidden
		// by zero or very low alpha outside the selection.
		for y := page.Bounds().Min.Y; y < page.Bounds().Max.Y; y++ {
			source := page.Pix[page.PixOffset(page.Bounds().Min.X, y):][:page.Bounds().Dx()]
			destination := converted.Pix[converted.PixOffset(page.Bounds().Min.X, y):][:page.Bounds().Dx()*4]
			for x, index := range source {
				c := palette[index]
				i := x * 4
				destination[i], destination[i+1], destination[i+2], destination[i+3] = c.R, c.G, c.B, c.A
			}
		}
		target = converted
	default:
		// Grayscale pages need RGB storage for the blue tint.
		target = image.NewNRGBA(page.Bounds())
		draw.Draw(target, page.Bounds(), page, page.Bounds().Min, draw.Src)
	}

	// Sweep only rows containing rectangles. The active list is reused, so
	// overlap handling needs rectangle-sized storage, not a page-sized mask.
	active := make([]image.Rectangle, 0, len(clipped))
	next := 0
	for y := clipped[0].Min.Y; next < len(clipped) || len(active) > 0; y++ {
		remaining := active[:0]
		for _, rectangle := range active {
			if rectangle.Max.Y > y {
				remaining = append(remaining, rectangle)
			}
		}
		active = remaining
		if len(active) == 0 && next < len(clipped) {
			y = max(y, clipped[next].Min.Y)
		}
		for next < len(clipped) && clipped[next].Min.Y <= y {
			active = append(active, clipped[next])
			next++
		}
		if len(active) == 0 {
			break
		}
		slices.SortFunc(active, func(a, b image.Rectangle) int {
			return a.Min.X - b.Min.X
		})
		left, right := active[0].Min.X, active[0].Max.X
		for _, rectangle := range active[1:] {
			if rectangle.Min.X <= right {
				right = max(right, rectangle.Max.X)
				continue
			}
			draw.Draw(target, image.Rect(left, y, right, y+1), selectionTint, image.Point{}, draw.Over)
			left, right = rectangle.Min.X, rectangle.Max.X
		}
		draw.Draw(target, image.Rect(left, y, right, y+1), selectionTint, image.Point{}, draw.Over)
	}

	var output bytes.Buffer
	output.Grow(len(data))
	if err := pngEncoder.Encode(&output, target); err != nil {
		return nil, fmt.Errorf("encode selection page PNG: %w", err)
	}
	return output.Bytes(), nil
}
