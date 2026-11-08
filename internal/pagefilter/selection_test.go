package pagefilter

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"slices"
	"testing"
)

func decodeSelection(t testing.TB, input []byte, rectangles []image.Rectangle) image.Image {
	t.Helper()
	result, err := Selection(input, rectangles)
	if err != nil {
		t.Fatal(err)
	}
	page, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestSelectionPixels(t *testing.T) {
	page := image.NewNRGBA(image.Rect(0, 0, 6, 5))
	for y := range page.Bounds().Dy() {
		for x := range page.Bounds().Dx() {
			page.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}
	page.SetNRGBA(2, 2, color.NRGBA{A: 255})
	input := encodePNG(t, page)
	original := bytes.Clone(input)
	rectangles := []image.Rectangle{image.Rect(1, 1, 5, 4)}
	originalRectangles := slices.Clone(rectangles)
	got := decodeSelection(t, input, rectangles)
	if got.Bounds() != page.Bounds() {
		t.Fatalf("dimensions changed: %v -> %v", page.Bounds(), got.Bounds())
	}
	if !bytes.Equal(input, original) {
		t.Fatal("input bytes changed")
	}
	if !slices.Equal(rectangles, originalRectangles) {
		t.Fatal("input rectangles changed")
	}
	for y := range page.Bounds().Dy() {
		for x := range page.Bounds().Dx() {
			want := page.NRGBAAt(x, y)
			if image.Pt(x, y).In(rectangles[0]) {
				want = color.NRGBA{183, 207, 255, 255}
				if x == 2 && y == 2 {
					want = color.NRGBA{24, 48, 96, 255}
				}
			}
			if actual := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA); actual != want {
				t.Errorf("pixel (%d,%d) = %v, want %v", x, y, actual, want)
			}
		}
	}
}

func TestSelectionRectangleUnion(t *testing.T) {
	page := image.NewGray(image.Rect(0, 0, 6, 6))
	for i := range page.Pix {
		page.Pix[i] = 255
	}
	input := encodePNG(t, page)
	tests := []struct {
		name       string
		rectangles []image.Rectangle
		selected   [6]string
	}{
		{
			name: "clipping and empty rectangles",
			rectangles: []image.Rectangle{
				image.Rect(-2, -2, 2, 2), image.Rect(4, 4, 9, 9),
				image.Rect(-5, 1, -1, 4), image.Rect(7, 0, 8, 3),
				image.Rect(1, -5, 4, -1), image.Rect(1, 8, 4, 9),
				image.Rect(3, 2, 3, 5),
				{Min: image.Pt(4, 2), Max: image.Pt(2, 4)},
			},
			selected: [6]string{"##....", "##....", "......", "......", "....##", "....##"},
		},
		{
			name: "overlaps and duplicates tint once",
			rectangles: []image.Rectangle{
				image.Rect(2, 2, 5, 5), image.Rect(1, 1, 4, 4), image.Rect(1, 1, 4, 4),
			},
			selected: [6]string{"......", ".###..", ".####.", ".####.", "..###.", "......"},
		},
		{
			name: "unsorted disjoint and touching rectangles",
			rectangles: []image.Rectangle{
				image.Rect(4, 4, 6, 5), image.Rect(3, 0, 5, 1),
				image.Rect(0, 4, 1, 5), image.Rect(1, 0, 3, 1),
			},
			selected: [6]string{".####.", "......", "......", "......", "#...##", "......"},
		},
		{
			name: "entirely off image",
			rectangles: []image.Rectangle{
				image.Rect(-5, -5, -1, -1), image.Rect(6, 0, 9, 6), image.Rect(0, 6, 6, 9),
			},
			selected: [6]string{"......", "......", "......", "......", "......", "......"},
		},
		{
			name:     "no rectangles",
			selected: [6]string{"......", "......", "......", "......", "......", "......"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			originalRectangles := slices.Clone(test.rectangles)
			got := decodeSelection(t, input, test.rectangles)
			if !slices.Equal(test.rectangles, originalRectangles) {
				t.Fatal("input rectangles changed")
			}
			for y, row := range test.selected {
				for x := range len(row) {
					want := color.NRGBA{255, 255, 255, 255}
					if row[x] == '#' {
						want = color.NRGBA{183, 207, 255, 255}
					}
					if actual := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA); actual != want {
						t.Errorf("pixel (%d,%d) = %v, want %v", x, y, actual, want)
					}
				}
			}
		})
	}
}

func TestSelectionPNGColourModels(t *testing.T) {
	bounds := image.Rect(0, 0, 3, 2)
	palette := color.Palette{color.White, color.NRGBA{40, 80, 120, 128}}
	tests := []struct {
		name string
		page interface {
			image.Image
			Set(int, int, color.Color)
		}
	}{
		{"gray", image.NewGray(bounds)},
		{"palette", image.NewPaletted(bounds, palette)},
		{"rgba", image.NewRGBA(bounds)},
		{"nrgba", image.NewNRGBA(bounds)},
		{"rgba64", image.NewRGBA64(bounds)},
		{"nrgba64", image.NewNRGBA64(bounds)},
		{"gray16", image.NewGray16(bounds)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.page.Set(0, 0, color.NRGBA64{10001, 20002, 30003, 40004})
			test.page.Set(2, 1, color.NRGBA64{60005, 50006, 40007, 65535})
			test.page.Set(1, 0, color.White)
			input := encodePNG(t, test.page)
			before, err := png.Decode(bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			got := decodeSelection(t, input, []image.Rectangle{image.Rect(1, 0, 2, 1)})
			if got.Bounds() != bounds {
				t.Fatalf("dimensions changed: %v", got.Bounds())
			}
			for y := range bounds.Dy() {
				for x := range bounds.Dx() {
					if x == 1 && y == 0 {
						want := color.NRGBA{183, 207, 255, 255}
						if actual := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA); actual != want {
							t.Errorf("selected pixel = %v, want %v", actual, want)
						}
						continue
					}
					r, g, b, a := before.At(x, y).RGBA()
					r2, g2, b2, a2 := got.At(x, y).RGBA()
					if [4]uint32{r, g, b, a} != [4]uint32{r2, g2, b2, a2} {
						t.Errorf("outside pixel (%d,%d) changed: (%d,%d,%d,%d) -> (%d,%d,%d,%d)", x, y, r, g, b, a, r2, g2, b2, a2)
					}
				}
			}
		})
	}
}

func TestSelectionPaletteTransparency(t *testing.T) {
	palette := color.Palette{
		color.White, color.NRGBA{1, 2, 3, 1}, color.NRGBA{10, 20, 30, 0},
	}
	page := image.NewPaletted(image.Rect(0, 0, 3, 1), palette)
	page.SetColorIndex(1, 0, 1)
	page.SetColorIndex(2, 0, 2)
	got := decodeSelection(t, encodePNG(t, page), []image.Rectangle{image.Rect(0, 0, 1, 1)})
	for x := 1; x < 3; x++ {
		want := color.NRGBAModel.Convert(page.At(x, 0)).(color.NRGBA)
		if actual := color.NRGBAModel.Convert(got.At(x, 0)).(color.NRGBA); actual != want {
			t.Errorf("outside palette pixel %d = %v, want %v", x, actual, want)
		}
	}
}

func TestSelectionTransparentPixels(t *testing.T) {
	page := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	page.SetNRGBA(0, 0, color.NRGBA{10, 20, 30, 0})
	page.SetNRGBA(1, 0, color.NRGBA{A: 128})
	page.SetNRGBA(2, 0, color.NRGBA{10, 20, 30, 0})
	got := decodeSelection(t, encodePNG(t, page), []image.Rectangle{image.Rect(0, 0, 2, 1)})
	for x, wantAlpha := range []uint8{96, 176, 0} {
		actual := color.NRGBAModel.Convert(got.At(x, 0)).(color.NRGBA)
		if actual.A != wantAlpha {
			t.Errorf("pixel %d alpha = %d, want %d", x, actual.A, wantAlpha)
		}
		if x < 2 && !(actual.B > actual.G && actual.G > actual.R) {
			t.Errorf("pixel %d did not receive blue tint: %v", x, actual)
		}
	}
	if actual := color.NRGBAModel.Convert(got.At(2, 0)).(color.NRGBA); actual != page.NRGBAAt(2, 0) {
		t.Errorf("unselected transparent pixel changed: %v", actual)
	}
}

func TestSelectionOwnsResultBytes(t *testing.T) {
	page := image.NewGray(image.Rect(0, 0, 2, 2))
	for _, rectangles := range [][]image.Rectangle{nil, {image.Rect(0, 0, 1, 1)}, {image.Rect(3, 3, 4, 4)}} {
		input := encodePNG(t, page)
		original := bytes.Clone(input)
		result, err := Selection(input, rectangles)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(input, original) {
			t.Fatal("input bytes changed")
		}
		want := bytes.Clone(result)
		input[0] ^= 0xff
		if !bytes.Equal(result, want) {
			t.Fatal("result shares storage with input bytes")
		}
		if _, err := Selection(original, []image.Rectangle{image.Rect(0, 0, 2, 2)}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(result, want) {
			t.Fatal("a later call changed the earlier PNG")
		}
	}
}

func TestSelectionInvalidPNG(t *testing.T) {
	valid := encodePNG(t, image.NewGray(image.Rect(0, 0, 2, 2)))
	for _, input := range [][]byte{nil, []byte("not a PNG"), valid[:len(valid)/2]} {
		for _, rectangles := range [][]image.Rectangle{nil, {image.Rect(0, 0, 1, 1)}} {
			if result, err := Selection(input, rectangles); err == nil || result != nil {
				t.Errorf("invalid PNG returned %d bytes and error %v", len(result), err)
			}
		}
	}
}
