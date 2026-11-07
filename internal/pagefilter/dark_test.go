package pagefilter

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"sync"
	"testing"
)

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func decodeDark(t testing.TB, data []byte) image.Image {
	t.Helper()
	result, err := Dark(data)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(result))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestDarkPaperTextAndColour(t *testing.T) {
	page := image.NewNRGBA(image.Rect(0, 0, 7, 3))
	colours := []color.NRGBA{
		{255, 255, 255, 255}, {0, 0, 0, 255}, {255, 0, 0, 255},
		{30, 140, 210, 255}, {240, 200, 40, 255}, {120, 0, 0, 128},
		{10, 20, 30, 0},
	}
	for x, c := range colours {
		page.SetNRGBA(x, 0, c)
	}
	input := encodePNG(t, page)
	original := bytes.Clone(input)
	got := decodeDark(t, input)
	if got.Bounds() != page.Bounds() {
		t.Fatalf("dimensions changed: %v -> %v", page.Bounds(), got.Bounds())
	}
	if !bytes.Equal(input, original) {
		t.Fatal("input bytes changed")
	}
	paper := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA)
	wantPaper := color.NRGBA{DarkPaper >> 16, (DarkPaper >> 8) & 255, DarkPaper & 255, 255}
	for i, pair := range [][2]byte{{paper.R, wantPaper.R}, {paper.G, wantPaper.G}, {paper.B, wantPaper.B}} {
		if math.Abs(float64(pair[0])-float64(pair[1])) > 1 {
			t.Errorf("paper channel %d: got %d, want %d", i, pair[0], pair[1])
		}
	}
	text := color.NRGBAModel.Convert(got.At(1, 0)).(color.NRGBA)
	if min(text.R, text.G, text.B) < 220 {
		t.Errorf("black text did not become light: %v", text)
	}
	red := color.NRGBAModel.Convert(got.At(2, 0)).(color.NRGBA)
	if red.R < 220 || red.G > 25 || red.B > 25 {
		t.Errorf("saturated red lost its colour: %v", red)
	}
	for x := 2; x < 6; x++ {
		before := colours[x]
		after := color.NRGBAModel.Convert(got.At(x, 0)).(color.NRGBA)
		difference := math.Abs(hue(before) - hue(after))
		difference = min(difference, 360-difference)
		if difference > 2 {
			t.Errorf("pixel %d hue changed by %.2f degrees: %v -> %v", x, difference, before, after)
		}
		if after.A != before.A {
			t.Errorf("pixel %d alpha changed: %d -> %d", x, before.A, after.A)
		}
	}
	transparent := color.NRGBAModel.Convert(got.At(6, 0)).(color.NRGBA)
	if transparent.A != 0 {
		t.Errorf("transparent pixel became visible: %v", transparent)
	}
}

func hue(c color.NRGBA) float64 {
	r, g, b := float64(c.R), float64(c.G), float64(c.B)
	hi, lo := max(r, g, b), min(r, g, b)
	if hi == lo {
		return 0
	}
	var h float64
	switch hi {
	case r:
		h = (g - b) / (hi - lo)
	case g:
		h = 2 + (b-r)/(hi-lo)
	default:
		h = 4 + (r-g)/(hi-lo)
	}
	return math.Mod(h*60+360, 360)
}

func TestDarkPNGColourModels(t *testing.T) {
	rect := image.Rect(0, 0, 3, 2)
	palette := color.Palette{color.White, color.Black, color.NRGBA{255, 0, 0, 128}}
	cases := []struct {
		name string
		page interface {
			image.Image
			Set(int, int, color.Color)
		}
	}{
		{"gray", image.NewGray(rect)},
		{"palette", image.NewPaletted(rect, palette)},
		{"rgba", image.NewRGBA(rect)},
		{"rgba64", image.NewRGBA64(rect)},
		{"nrgba64", image.NewNRGBA64(rect)},
		{"gray16", image.NewGray16(rect)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			test.page.Set(0, 0, color.White)
			test.page.Set(1, 0, color.Black)
			if test.name == "palette" || test.name == "nrgba64" {
				test.page.Set(2, 0, color.NRGBA{255, 0, 0, 128})
			}
			got := decodeDark(t, encodePNG(t, test.page))
			if got.Bounds() != rect {
				t.Fatalf("dimensions changed: %v", got.Bounds())
			}
			for x, want := range []uint32{16 * 257, 239 * 257} {
				r, g, b, a := got.At(x, 0).RGBA()
				if r != want || g != want || b != want || a != 65535 {
					t.Errorf("pixel %d = (%d,%d,%d,%d), want (%d,%d,%d,65535)", x, r, g, b, a, want, want, want)
				}
			}
			if test.name == "palette" || test.name == "nrgba64" {
				c := color.NRGBAModel.Convert(got.At(2, 0)).(color.NRGBA)
				if c.R < 220 || c.G > 25 || c.B > 25 || c.A != 128 {
					t.Errorf("translucent red changed hue or alpha: %v", c)
				}
			}
		})
	}
}

func TestDarkInvalidPNG(t *testing.T) {
	valid := encodePNG(t, image.NewGray(image.Rect(0, 0, 2, 2)))
	for _, input := range [][]byte{nil, []byte("not a PNG"), valid[:len(valid)/2]} {
		if result, err := Dark(input); err == nil || result != nil {
			t.Errorf("invalid PNG returned %d bytes and error %v", len(result), err)
		}
	}
}

func TestDarkResultsRemainIndependent(t *testing.T) {
	page := image.NewRGBA(image.Rect(0, 0, 64, 64))
	page.Set(0, 0, color.White)
	input := encodePNG(t, page)
	first, err := Dark(input)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(first)
	var calls sync.WaitGroup
	for range 8 {
		calls.Go(func() {
			got, err := Dark(input)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(got, want) {
				t.Error("concurrent call returned a different PNG")
			}
		})
	}
	calls.Wait()
	if !bytes.Equal(first, want) {
		t.Fatal("a later call changed the earlier PNG")
	}
}

func BenchmarkDark(b *testing.B) {
	page := image.NewRGBA(image.Rect(0, 0, 1035, 1340))
	for y := range 1340 {
		for x := range 1035 {
			c := color.RGBA{255, 255, 255, 255}
			if x > 80 && x < 955 && y%24 > 15 && (x+y)%13 < 7 {
				c = color.RGBA{20, 20, 20, 255}
			}
			if y > 900 && y < 1200 && x%160 < 3 {
				c = color.RGBA{30, 110, 210, 255}
			}
			page.SetRGBA(x, y, c)
		}
	}
	input := encodePNG(b, page)
	b.SetBytes(1035 * 1340 * 4)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Dark(input); err != nil {
			b.Fatal(err)
		}
	}
}
