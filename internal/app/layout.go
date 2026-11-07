package app

import (
	"math"

	"tomequay/internal/imageout"
	"tomequay/internal/pdf"
	"tomequay/internal/tabs"
	"tomequay/internal/terminal"
)

// pageLayout describes the visible window of a scaled page, in pane cells and
// Poppler pixels. Scroll positions are fractions of the *overflow*, not the page.
type pageLayout struct {
	X, Y, Columns, Rows         int
	FullWidth, FullHeight       int
	VisibleWidth, VisibleHeight int
	Request                     pdf.RenderRequest
}

func layoutPage(size terminal.Size, info pdf.DocumentInfo, view tabs.View, widthFit bool) pageLayout {
	if size.Columns < 20 || size.Rows < 8 || info.PageWidthPoints <= 0 || info.PageHeightPoints <= 0 {
		return pageLayout{}
	}
	cw, ch := max(1, size.CellWidthPixels), max(1, size.CellHeightPixels)
	areaColumns, areaRows := min(size.Columns, imageout.MaxGridSize), min(size.Rows-2, imageout.MaxGridSize)
	aw, ah := areaColumns*cw, areaRows*ch
	pageW, pageH := info.PageWidthPoints, info.PageHeightPoints
	if info.PageRotationDegrees%180 != 0 {
		pageW, pageH = pageH, pageW
	}
	aspect := pageW / pageH
	widthFit = view.Fit == tabs.FitWidth || view.Fit == tabs.Custom && widthFit
	var boxW, boxH int
	if widthFit {
		boxW, boxH = aw, int(math.Ceil(float64(aw)/aspect))
	} else {
		scale := math.Min(float64(aw)/pageW, float64(ah)/pageH)
		cols := max(1, min(areaColumns, int(math.Floor(pageW*scale/float64(cw)))))
		rows := max(1, min(areaRows, int(math.Floor(pageH*scale/float64(ch)))))
		boxW, boxH = cols*cw, rows*ch
	}
	r := pdf.RenderRequest{Page: view.Page, WidthPixels: boxW, HeightPixels: boxH, Zoom: math.Round(view.Zoom*1000) / 1000}
	w, h := pdf.ScaledPageSize(info, r)
	visibleW, visibleH := min(w, aw), min(h, ah)
	cols := max(1, min(areaColumns, (visibleW+cw-1)/cw))
	rows := max(1, min(areaRows, (visibleH+ch-1)/ch))
	if w > aw || h > ah {
		r.CropX = int(math.Round(float64(max(0, w-aw)) * view.ScrollX))
		r.CropY = int(math.Round(float64(max(0, h-ah)) * view.ScrollY))
		r.CropWidth, r.CropHeight = visibleW, visibleH
	}
	return pageLayout{X: (size.Columns - cols) / 2, Y: 1 + (size.Rows-2-rows)/2,
		Columns: cols, Rows: rows, FullWidth: w, FullHeight: h,
		VisibleWidth: visibleW, VisibleHeight: visibleH, Request: r}
}

// scrollVertical consumes counted scroll steps without losing the edge: reaching
// the bottom stops there; the following step enters the next page at its top.
func scrollVertical(page, pages int, position float64, overflow, stepPixels float64, count, direction int) (int, float64) {
	if count < 1 {
		count = 1
	}
	for count > 0 {
		edge := position
		if direction > 0 {
			edge = 1 - position
		}
		if overflow > 0 && edge > 1e-9 {
			steps := max(1, int(math.Ceil(edge*overflow/stepPixels-1e-9)))
			n := min(count, steps)
			position = max(0, min(1, position+float64(direction*n)*stepPixels/overflow))
			count -= n
			continue
		}
		if direction > 0 {
			if page >= pages {
				break
			}
			page++
			position = 0
		} else {
			if page <= 1 {
				break
			}
			page--
			position = 1
		}
		count--
	}
	return page, position
}
