package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func init() {
	if output := os.Getenv("PDF_TEST_WORD_XML"); output != "" {
		fmt.Print(output)
		os.Exit(0)
	}
}

func wordXML(width, height, contents string) string {
	return `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd"><html xmlns="http://www.w3.org/1999/xhtml"><head/><body><doc><page width="` + width + `" height="` + height + `"><flow><block>` + contents + `</block></flow></page></doc></body></html>`
}

func TestParsePageWordsEntitiesAndLines(t *testing.T) {
	data := wordXML("200", "300", `<line><word xMin="10" yMin="20" xMax="50" yMax="30">café &amp; &lt;量子&gt; &#x1F600;</word><word xMin="60" yMin="20" xMax="80" yMax="30">&#9; spaced&#10;word &#160;</word><word xMin="90" yMin="20" xMax="95" yMax="30"> &#9;&#10; </word></line></block></flow><flow><block><line><word xMin="5" yMin="40" xMax="30" yMax="50">next</word></line>`)
	got, err := parsePageWords([]byte(data), 0)
	want := PageWords{Width: 200, Height: 300, Words: []Word{
		{Text: "café & <量子> 😀", XMin: 10, YMin: 20, XMax: 50, YMax: 30, Line: 0},
		{Text: "spaced word", XMin: 60, YMin: 20, XMax: 80, YMax: 30, Line: 0},
		{Text: "next", XMin: 5, YMin: 40, XMax: 30, YMax: 50, Line: 1},
	}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePageWords = %+v, %v; want %+v", got, err, want)
	}
}

func TestParsePageWordsClipsVisibleBoxes(t *testing.T) {
	data := wordXML("200", "300", `<line><word xMin="-2" yMin="-3" xMax="20" yMax="30">edge</word><word xMin="190" yMin="290" xMax="210" yMax="310">corner</word><word xMin="210" yMin="10" xMax="220" yMax="20">outside</word></line>`)
	got, err := parsePageWords([]byte(data), 0)
	want := []Word{
		{Text: "edge", XMin: 0, YMin: 0, XMax: 20, YMax: 30, Line: 0},
		{Text: "corner", XMin: 190, YMin: 290, XMax: 200, YMax: 300, Line: 0},
	}
	if err != nil || !reflect.DeepEqual(got.Words, want) {
		t.Fatalf("parsePageWords = %+v, %v; want %v", got, err, want)
	}
}

func TestParsePageWordsRejectsInvalidXMLAndGeometry(t *testing.T) {
	validWord := `<word xMin="1" yMin="2" xMax="10" yMax="20">word</word>`
	valid := wordXML("200", "300", "<line>"+validWord+"</line>")
	for _, tc := range []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"truncated", valid[:len(valid)-10]},
		{"missing page", `<html><body><doc/></body></html>`},
		{"multiple pages", strings.Replace(valid, "</doc>", `<page width="200" height="300"/></doc>`, 1)},
		{"multiple roots", valid + valid},
		{"trailing text", valid + "trailing"},
		{"zero width", wordXML("0", "300", "")},
		{"negative height", wordXML("200", "-300", "")},
		{"nonfinite page", wordXML("NaN", "300", "")},
		{"infinite page", wordXML("200", "+Inf", "")},
		{"missing dimension", strings.Replace(valid, ` width="200"`, "", 1)},
		{"missing coordinate", strings.Replace(valid, ` xMin="1"`, "", 1)},
		{"duplicate coordinate", strings.Replace(valid, `xMin="1"`, `xMin="1" xMin="2"`, 1)},
		{"nonfinite coordinate", strings.Replace(valid, `xMin="1"`, `xMin="NaN"`, 1)},
		{"infinite coordinate", strings.Replace(valid, `yMax="20"`, `yMax="-Inf"`, 1)},
		{"overflow coordinate", strings.Replace(valid, `yMax="20"`, `yMax="1e999"`, 1)},
		{"reversed rectangle", strings.Replace(valid, `xMin="1"`, `xMin="11"`, 1)},
		{"empty rectangle", strings.Replace(valid, `yMax="20"`, `yMax="2"`, 1)},
		{"word outside line", wordXML("200", "300", validWord)},
		{"nested markup", strings.Replace(valid, ">word</word>", "><b>word</b></word>", 1)},
		{"unknown entity", strings.Replace(valid, ">word</word>", ">&unknown;</word>", 1)},
		{"external entity", `<!DOCTYPE html [<!ENTITY secret SYSTEM "file:///etc/passwd">]>` + strings.Replace(valid[strings.Index(valid, "<html"):], ">word</word>", ">&secret;</word>", 1)},
		{"recursive entity", `<!DOCTYPE html [<!ENTITY loop "&loop;">]>` + strings.Replace(valid[strings.Index(valid, "<html"):], ">word</word>", ">&loop;</word>", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePageWords([]byte(tc.data), 0)
			if err == nil || !reflect.DeepEqual(got, PageWords{}) {
				t.Fatalf("parsePageWords = %+v, %v; want no output and an error", got, err)
			}
		})
	}
	if _, err := parsePageWords([]byte(valid), 45); err == nil {
		t.Fatal("non-cardinal rotation accepted")
	}
}

func TestPageWordsRotatedCropBoxesAndPixelSize(t *testing.T) {
	tools := installedTools(t)
	path := filepath.Join(t.TempDir(), "mixed-crops.pdf")
	pages := []testPage{{612, 792, 0, "first page"}}
	crops := [][4]float64{{0, 0, 612, 792}}
	for _, rotation := range []int{0, 90, 180, 270} {
		pages = append(pages, testPage{300, 600, rotation, "Alpha beta"})
		crops = append(crops, [4]float64{10, 20, 210, 320})
	}
	writePDF(t, path, "", pages, crops...)
	for i, tc := range []struct {
		rotation      int
		width, height float64
		alpha, beta   [4]float64
	}{
		{0, 200, 300, [4]float64{10, 271.384, 40.684, 282.484}, [4]float64{44.020, 271.384, 67.372, 282.484}},
		{90, 300, 200, [4]float64{17.516, 10, 28.616, 40.684}, [4]float64{17.516, 44.020, 28.616, 67.372}},
		{180, 200, 300, [4]float64{159.316, 17.516, 190, 28.616}, [4]float64{132.628, 17.516, 155.980, 28.616}},
		{270, 300, 200, [4]float64{271.384, 159.316, 282.484, 190}, [4]float64{271.384, 132.628, 282.484, 155.980}},
	} {
		t.Run(strconv.Itoa(tc.rotation), func(t *testing.T) {
			page := i + 2
			got, err := tools.PageWords(context.Background(), path, "", page)
			if err != nil {
				t.Fatal(err)
			}
			if got.Width != tc.width || got.Height != tc.height || len(got.Words) != 2 {
				t.Fatalf("PageWords = %+v; want %gx%g and two words", got, tc.width, tc.height)
			}
			for j, want := range []struct {
				text string
				box  [4]float64
			}{{"Alpha", tc.alpha}, {"beta", tc.beta}} {
				word := got.Words[j]
				if word.Text != want.text || word.Line != 0 {
					t.Errorf("word %d = %+v; want %q on line 0", j, word, want.text)
				}
				for k, coordinate := range [4]float64{word.XMin, word.YMin, word.XMax, word.YMax} {
					if math.Abs(coordinate-want.box[k]) > 0.00001 {
						t.Errorf("word %d coordinate %d = %g; want %g", j, k, coordinate, want.box[k])
					}
				}
			}
			for _, zoom := range []float64{1, 2.5} {
				request := RenderRequest{Path: path, Page: page, WidthPixels: 401, HeightPixels: 299, Zoom: zoom}
				data, err := tools.RenderPage(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				config, err := png.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				width, height := got.PixelSize(request)
				if width != config.Width || height != config.Height {
					t.Errorf("PixelSize = %dx%d; rendered PNG = %dx%d", width, height, config.Width, config.Height)
				}
				request.CropX, request.CropY, request.CropWidth, request.CropHeight = 11, 17, 50, 60
				if w, h := got.PixelSize(request); w != width || h != height {
					t.Errorf("pixel crop changed full PixelSize to %dx%d", w, h)
				}
			}
			if w, h := got.PixelSize(RenderRequest{}); w != 0 || h != 0 {
				t.Errorf("invalid PixelSize = %dx%d", w, h)
			}
		})
	}
}

func TestPageWordsPreciseRasterRounding(t *testing.T) {
	tools := installedTools(t)
	for _, rotation := range []int{0, 90, 180, 270} {
		t.Run(strconv.Itoa(rotation), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fractional-crop.pdf")
			// pdfinfo rounds this crop box to 300 x 400 points. The
			// rasterizer still sees its true, slightly taller aspect ratio.
			writePDF(t, path, "", []testPage{{400, 600, rotation, ""}}, [4]float64{40, 50, 340.0001, 450.0004})
			got, err := tools.PageWords(context.Background(), path, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			request := RenderRequest{Path: path, Page: 1, WidthPixels: 300, HeightPixels: 500, Zoom: 1}
			wantW, wantH := 300, 401
			if rotation%180 != 0 {
				request.WidthPixels, request.HeightPixels = request.HeightPixels, request.WidthPixels
				wantW, wantH = wantH, wantW
			}
			if w, h := got.PixelSize(request); w != wantW || h != wantH {
				t.Errorf("PixelSize = %dx%d; want %dx%d", w, h, wantW, wantH)
			}
			data, err := tools.RenderPage(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if config.Width != wantW || config.Height != wantH {
				t.Errorf("PNG = %dx%d; want %dx%d", config.Width, config.Height, wantW, wantH)
			}
		})
	}
}

func TestPageWordsLayoutLineOrder(t *testing.T) {
	tools := installedTools(t)
	for _, rotation := range []int{0, 90, 180, 270} {
		t.Run(strconv.Itoa(rotation), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lines.pdf")
			content := "BT /F1 12 Tf 60 100 Td (Alpha beta) Tj 0 -25 Td (Second line) Tj ET\n"
			writePDFObjects(t, path, []string{
				"<< /Type /Catalog /Pages 2 0 R >>",
				"<< /Type /Pages /Kids [5 0 R] /Count 1 >>",
				"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
				"<< >>",
				fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 400 600] /CropBox [40 50 340 450] /Rotate %d /Resources << /Font << /F1 3 0 R >> >> /Contents 6 0 R >>", rotation),
				fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
			})
			got, err := tools.PageWords(context.Background(), path, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"Alpha", "beta", "Second", "line"}
			// bbox-layout orders the two vertical text blocks differently at
			// 270 degrees. Do not re-sort by X/Y or unrotated content order.
			if rotation == 270 {
				want = []string{"Second", "line", "Alpha", "beta"}
			}
			if len(got.Words) != len(want) {
				t.Fatalf("PageWords = %+v; want four words", got)
			}
			for i, word := range got.Words {
				if word.Text != want[i] || word.Line != i/2 {
					t.Errorf("word %d = %+v; want %q on line %d", i, word, want[i], i/2)
				}
			}
		})
	}
}

func TestPageWordsTextlessAndErrors(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	got, err := tools.PageWords(context.Background(), path, "", 3)
	if err != nil || got.Width != 300 || got.Height != 600 || len(got.Words) != 0 {
		t.Fatalf("textless PageWords = %+v, %v", got, err)
	}
	for _, page := range []int{-1, 0, 5} {
		got, err := tools.PageWords(context.Background(), path, "", page)
		if !errors.Is(err, ErrPageOutOfRange) || !reflect.DeepEqual(got, PageWords{}) {
			t.Errorf("PageWords(%d) = %+v, %v", page, got, err)
		}
	}
	invalid := filepath.Join(t.TempDir(), "invalid.pdf")
	if err := os.WriteFile(invalid, []byte("not a PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := tools.PageWords(context.Background(), invalid, "", 1); !errors.Is(err, ErrInvalidPDF) || !reflect.DeepEqual(got, PageWords{}) {
		t.Errorf("invalid PageWords = %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := tools.PageWords(ctx, path, "", 1); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, PageWords{}) {
		t.Errorf("canceled PageWords = %+v, %v", got, err)
	}
}

func TestPageWordsMalformedOutput(t *testing.T) {
	tools := installedTools(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tools.pdftotext = executable
	t.Setenv("PDF_TEST_WORD_XML", wordXML("200", "NaN", ""))
	got, err := tools.PageWords(context.Background(), samplePDF(t), "", 1)
	if !errors.Is(err, ErrInvalidPDF) || !reflect.DeepEqual(got, PageWords{}) {
		t.Fatalf("malformed PageWords = %+v, %v", got, err)
	}
}

func TestPageWordsHostileFilename(t *testing.T) {
	tools := installedTools(t)
	t.Chdir(t.TempDir())
	path := "-help;$(touch unexpected).pdf"
	writePDF(t, path, "", []testPage{{300, 600, 0, "safe text"}})
	got, err := tools.PageWords(context.Background(), path, "", 1)
	if err != nil || len(got.Words) != 2 || got.Words[0].Text != "safe" || got.Words[1].Text != "text" {
		t.Fatalf("hostile filename PageWords = %+v, %v", got, err)
	}
}

func TestPageWordsEncrypted(t *testing.T) {
	tools := installedTools(t)
	qpdf, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("encrypted PDF cases untested: qpdf is not installed")
	}
	path := filepath.Join(t.TempDir(), "encrypted.pdf")
	if out, err := exec.Command(qpdf, "--encrypt", "reader-password", "owner-password", "256", "--", samplePDF(t), path).CombinedOutput(); err != nil {
		t.Fatalf("qpdf: %v: %s", err, out)
	}
	for _, tc := range []struct {
		password string
		want     error
	}{{"", ErrPasswordRequired}, {"wrong", ErrWrongPassword}, {"reader-password", nil}} {
		got, err := tools.PageWords(context.Background(), path, tc.password, 1)
		if !errors.Is(err, tc.want) {
			t.Fatalf("PageWords password %q: %v; want %v", tc.password, err, tc.want)
		}
		if tc.want != nil {
			if !reflect.DeepEqual(got, PageWords{}) || !strings.Contains(err.Error(), "Incorrect password") {
				t.Errorf("password failure = %+v, %v", got, err)
			}
		} else if len(got.Words) != 2 || got.Words[0].Text != "One" || got.Words[1].Text != "qubit" {
			t.Errorf("decrypted PageWords = %+v", got)
		}
	}
}
