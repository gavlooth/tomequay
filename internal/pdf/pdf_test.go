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
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A separate test process supplies a slow renderer so cancellation is exercised
// after output begins, without depending on how quickly a machine renders PDFs.
func init() {
	if marker := os.Getenv("PDF_TEST_SLOW_RENDER"); marker != "" {
		if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(2)
		}
		fmt.Print("partial image")
		time.Sleep(time.Hour)
		os.Exit(0)
	}
}

func installedTools(t *testing.T) Tools {
	t.Helper()
	tools, err := FindTools()
	if err != nil {
		t.Skipf("Poppler integration test skipped: %v", err)
	}
	return tools
}

type testPage struct {
	width, height float64
	rotation      int
	text          string
}

func writePDF(t *testing.T, path, title string, pages []testPage, cropBoxes ...[4]float64) {
	t.Helper()
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", "<< /Title (" + pdfString(title) + ") >>"}
	var kids []string
	for i, page := range pages {
		id := len(objects) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", id))
		content := ""
		if page.text != "" {
			content = "BT /F1 12 Tf 20 40 Td (" + pdfString(page.text) + ") Tj ET\n"
		}
		crop := ""
		if i < len(cropBoxes) {
			box := cropBoxes[i]
			crop = fmt.Sprintf("/CropBox [%g %g %g %g]", box[0], box[1], box[2], box[3])
		}
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %g %g] %s /Rotate %d /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", page.width, page.height, crop, page.rotation, id+1),
			fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	var data bytes.Buffer
	data.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, object := range objects {
		offsets[i] = data.Len()
		fmt.Fprintf(&data, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := data.Len()
	fmt.Fprintf(&data, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&data, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&data, "trailer\n<< /Size %d /Root 1 0 R /Info 4 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func pdfString(text string) string {
	return strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text)
}

func samplePDF(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.pdf")
	writePDF(t, path, "Study: Qubits (notes)", []testPage{{300, 600, 0, "One qubit"}, {600, 300, 0, "Two QUBITS"}, {600, 300, 90, ""}, {612.5, 792.25, 0, "A final page"}})
	return path
}

func TestInfo(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	for _, tc := range []struct {
		name, locale string
	}{{"default locale", "C"}, {"non-English parent locale", "de_DE.UTF-8"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LC_ALL", tc.locale)
			got, err := tools.Info(context.Background(), path, "")
			want := DocumentInfo{PageCount: 4, Title: "Study: Qubits (notes)", PageWidthPoints: 300, PageHeightPoints: 600}
			if err != nil || got != want {
				t.Fatalf("Info = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestRenderDimensions(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	for _, tc := range []struct {
		name                string
		page, width, height int
		zoom                float64
		wantW, wantH        int
	}{
		{"portrait height fit", 1, 400, 300, 1, 150, 300},
		{"portrait width fit", 1, 200, 600, 1, 200, 400},
		{"landscape width fit", 2, 400, 300, 1, 400, 200},
		{"rotated height fit", 3, 400, 300, 1, 150, 300},
		{"rotated width fit", 3, 200, 600, 1, 200, 400},
		{"zoom", 1, 400, 300, 2, 300, 600},
		{"fractional points", 4, 401, 299, 1, 232, 299},
		{"one pixel", 1, 1, 1, 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := tools.RenderPage(context.Background(), RenderRequest{Path: path, Page: tc.page, WidthPixels: tc.width, HeightPixels: tc.height, Zoom: tc.zoom})
			if err != nil {
				t.Fatal(err)
			}
			image, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("invalid PNG: %v", err)
			}
			w, h := image.Bounds().Dx(), image.Bounds().Dy()
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("PNG = %dx%d; want %dx%d", w, h, tc.wantW, tc.wantH)
			}
			if w > int(float64(tc.width)*tc.zoom) || h > int(float64(tc.height)*tc.zoom) {
				t.Fatalf("PNG %dx%d exceeds fit box", w, h)
			}
		})
	}
}

func TestCroppedPageFit(t *testing.T) {
	tools := installedTools(t)
	for _, rotation := range []int{0, 90} {
		t.Run(strconv.Itoa(rotation), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cropped.pdf")
			writePDF(t, path, "", []testPage{{300, 600, rotation, "cropped"}}, [4]float64{10, 20, 210, 320})
			info, err := tools.Info(context.Background(), path, "")
			if err != nil || info.PageWidthPoints != 200 || info.PageHeightPoints != 300 {
				t.Fatalf("cropped Info = %+v, %v", info, err)
			}
			data, err := tools.RenderPage(context.Background(), RenderRequest{Path: path, Page: 1, WidthPixels: 400, HeightPixels: 300, Zoom: 1})
			if err != nil {
				t.Fatal(err)
			}
			config, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			wantW, wantH := 200, 300
			if rotation == 90 {
				wantW, wantH = 400, 267
			}
			if config.Width != wantW || config.Height != wantH {
				t.Fatalf("cropped PNG = %dx%d; want %dx%d", config.Width, config.Height, wantW, wantH)
			}
		})
	}
}

func TestPageTextAndSearch(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	for _, tc := range []struct {
		query string
		want  []int
	}{{"qUbIt", []int{1, 2}}, {"final", []int{4}}, {"absent", []int{}}, {"", []int{}}, {"qubit\fTwo", []int{}}} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := tools.SearchPages(context.Background(), path, "", tc.query)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SearchPages = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		page int
		text string
	}{{1, "One qubit"}, {2, "Two QUBITS"}, {3, ""}} {
		got, err := tools.PageText(context.Background(), path, "", tc.page)
		if err != nil || strings.TrimSpace(got) != tc.text || strings.ContainsRune(got, '\f') {
			t.Errorf("PageText(%d) = %q, %v; want %q", tc.page, got, err, tc.text)
		}
	}
	blank := filepath.Join(t.TempDir(), "image-only.pdf")
	writePDF(t, blank, "", []testPage{{300, 600, 0, ""}, {300, 600, 0, ""}})
	got, err := tools.SearchPages(context.Background(), blank, "", "qubit")
	if err != nil || len(got) != 0 {
		t.Fatalf("textless PDF search = %v, %v", got, err)
	}
}

func TestPageOutOfRange(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	for _, page := range []int{-1, 0, 5} {
		t.Run(strconv.Itoa(page), func(t *testing.T) {
			_, err := tools.PageText(context.Background(), path, "", page)
			if !errors.Is(err, ErrPageOutOfRange) {
				t.Errorf("PageText error = %v", err)
			}
			data, err := tools.RenderPage(context.Background(), RenderRequest{Path: path, Page: page, WidthPixels: 100, HeightPixels: 100, Zoom: 1})
			if !errors.Is(err, ErrPageOutOfRange) || data != nil {
				t.Errorf("RenderPage = %d bytes, %v", len(data), err)
			}
		})
	}
}

func TestInvalidPDF(t *testing.T) {
	tools := installedTools(t)
	for _, content := range []string{"not a PDF", "%PDF-1.4\ntruncated"} {
		t.Run(content, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.pdf")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := tools.Info(context.Background(), path, "")
			if !errors.Is(err, ErrInvalidPDF) || !strings.Contains(err.Error(), "Syntax") {
				t.Fatalf("Info error = %v; want ErrInvalidPDF with Poppler details", err)
			}
		})
	}
}

func TestHostileFilename(t *testing.T) {
	tools := installedTools(t)
	t.Chdir(t.TempDir())
	path := "-help;$(touch unexpected).pdf"
	writePDF(t, path, "Hostile filename", []testPage{{300, 600, 0, "safe qubit"}})
	if got, err := tools.Info(context.Background(), path, ""); err != nil || got.Title != "Hostile filename" {
		t.Fatalf("Info = %+v, %v", got, err)
	}
	if _, err := tools.RenderPage(context.Background(), RenderRequest{Path: path, Page: 1, WidthPixels: 100, HeightPixels: 100, Zoom: 1}); err != nil {
		t.Fatal(err)
	}
	if got, err := tools.PageText(context.Background(), path, "", 1); err != nil || !strings.Contains(got, "safe qubit") {
		t.Fatalf("PageText = %q, %v", got, err)
	}
	if got, err := tools.SearchPages(context.Background(), path, "", "QUBIT"); err != nil || !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("SearchPages = %v, %v", got, err)
	}
}

func TestEncryptedPDF(t *testing.T) {
	tools := installedTools(t)
	qpdf, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("encrypted PDF cases untested: qpdf is not installed")
	}
	path := filepath.Join(t.TempDir(), "encrypted.pdf")
	command := exec.Command(qpdf, "--encrypt", "reader-password", "owner-password", "256", "--", samplePDF(t), path)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("qpdf: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name, password string
		want           error
	}{{"missing", "", ErrPasswordRequired}, {"wrong", "wrong", ErrWrongPassword}, {"correct", "reader-password", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			info, err := tools.Info(context.Background(), path, tc.password)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Info error = %v; want %v", err, tc.want)
			}
			if tc.want != nil {
				if !strings.Contains(err.Error(), "Incorrect password") {
					t.Fatalf("password error lost Poppler details: %v", err)
				}
				return
			}
			if !info.Encrypted || info.PageCount != 4 {
				t.Fatalf("encrypted Info = %+v", info)
			}
			if _, err := tools.RenderPage(context.Background(), RenderRequest{Path: path, Password: tc.password, Page: 1, WidthPixels: 100, HeightPixels: 100, Zoom: 1}); err != nil {
				t.Fatal(err)
			}
			if text, err := tools.PageText(context.Background(), path, tc.password, 1); err != nil || !strings.Contains(text, "qubit") {
				t.Fatalf("encrypted PageText = %q, %v", text, err)
			}
			if pages, err := tools.SearchPages(context.Background(), path, tc.password, "qubit"); err != nil || !reflect.DeepEqual(pages, []int{1, 2}) {
				t.Fatalf("encrypted SearchPages = %v, %v", pages, err)
			}
		})
	}
}

func TestInvalidRenderSize(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		zoom          float64
	}{{0, 100, 1}, {100, -1, 1}, {100, 100, 0}, {100, 100, -1}, {100, 100, math.NaN()}, {100, 100, math.Inf(1)}} {
		data, err := (Tools{}).RenderPage(context.Background(), RenderRequest{WidthPixels: tc.width, HeightPixels: tc.height, Zoom: tc.zoom})
		if err == nil || data != nil {
			t.Fatalf("invalid render size returned %d bytes, %v", len(data), err)
		}
	}
}

func TestRenderCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process exit observation uses Unix signals")
	}
	tools := installedTools(t)
	path := samplePDF(t)
	marker := filepath.Join(t.TempDir(), "renderer-pid")
	t.Setenv("PDF_TEST_SLOW_RENDER", marker)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tools.pdftoppm = executable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := tools.RenderPage(ctx, RenderRequest{Path: path, Page: 1, WidthPixels: 100, HeightPixels: 100, Zoom: 1})
		done <- result{data, err}
	}()
	var pid int
	deadline := time.After(5 * time.Second)
	for pid == 0 {
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, _ = strconv.Atoi(string(data))
		}
		if pid != 0 {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("renderer exited before cancellation: %v", got.err)
		case <-deadline:
			t.Fatal("renderer did not start within 5 seconds")
		case <-time.After(5 * time.Millisecond):
		}
	}
	start := time.Now()
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.data != nil {
			t.Fatalf("canceled render = %d bytes, %v", len(got.data), got.err)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("renderer process %d remains after cancellation: %v", pid, err)
		}
		t.Logf("renderer killed and reaped in %s", time.Since(start))
	case <-time.After(2 * time.Second):
		t.Fatal("canceled render did not return within 2 seconds")
	}
}

func TestCanceledOperations(t *testing.T) {
	tools := installedTools(t)
	path := samplePDF(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, operation := range map[string]func() error{
		"info":   func() error { _, err := tools.Info(ctx, path, ""); return err },
		"text":   func() error { _, err := tools.PageText(ctx, path, "", 1); return err },
		"search": func() error { _, err := tools.SearchPages(ctx, path, "", "qubit"); return err },
		"render": func() error {
			_, err := tools.RenderPage(ctx, RenderRequest{Path: path, Page: 1, WidthPixels: 100, HeightPixels: 100, Zoom: 1})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v; want context.Canceled", err)
			}
		})
	}
}

func TestFindToolsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := FindTools()
	if err == nil {
		t.Fatal("FindTools succeeded with an empty PATH")
	}
	for _, part := range []string{"pdfinfo", "pdftoppm", "pdftotext", "poppler", "poppler-utils"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("missing %q in error: %v", part, err)
		}
	}
}

func TestNielsenChuangSmoke(t *testing.T) {
	if os.Getenv("PDF_REAL_SMOKE") != "1" {
		t.Skip("set PDF_REAL_SMOKE=1 to run the Nielsen–Chuang real-document smoke test")
	}
	tools := installedTools(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "Documents", "code", "Nielsen_Chuang.pdf")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	info, err := tools.Info(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Info: %s; pages=%d title=%q first-page=%gx%g pt", time.Since(start), info.PageCount, info.Title, info.PageWidthPoints, info.PageHeightPoints)
	start = time.Now()
	data, err := tools.RenderPage(ctx, RenderRequest{Path: path, Page: 300, WidthPixels: 1200, HeightPixels: 1600, Zoom: 1})
	if err != nil {
		t.Fatal(err)
	}
	renderTime := time.Since(start)
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if config.Width > 1200 || config.Height > 1600 {
		t.Fatalf("render exceeds fit box: %dx%d", config.Width, config.Height)
	}
	t.Logf("RenderPage(300): %s; PNG=%dx%d bytes=%d", renderTime, config.Width, config.Height, len(data))
	start = time.Now()
	pages, err := tools.SearchPages(ctx, path, "", "qubit")
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("SearchPages(qubit) found no pages")
	}
	t.Logf("SearchPages(qubit): %s; matches=%d first=%d last=%d", time.Since(start), len(pages), pages[0], pages[len(pages)-1])
}
