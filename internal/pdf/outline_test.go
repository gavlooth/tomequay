package pdf

import (
	"context"
	"errors"
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

func TestFlattenOutline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []qpdfOutline
		want  []OutlineEntry
	}{
		{"empty", nil, []OutlineEntry{}},
		{"nested", []qpdfOutline{
			{Title: "Chapter", Page: 1, Kids: []qpdfOutline{
				{Title: "Section", Page: 2, Kids: []qpdfOutline{{Title: "Unresolved"}}},
				{Title: "Next section", Page: 3},
			}},
			{Title: "Next chapter", Page: 7},
		}, []OutlineEntry{{"Chapter", 1, 0}, {"Section", 2, 1}, {"Unresolved", 0, 2}, {"Next section", 3, 1}, {"Next chapter", 7, 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := flattenOutline(tc.items); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("flattenOutline = %+v; want %+v", got, tc.want)
			}
		})
	}
}

func outlinePDF(t *testing.T, path string) {
	t.Helper()
	writePDFObjects(t, path, []string{
		"<< /Type /Catalog /Pages 2 0 R /Outlines 11 0 R >>",
		"<< /Type /Pages /Kids [5 0 R 7 0 R 9 0 R] /Count 3 >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Title (Outline test) >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 600] /Contents 6 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 600] /Contents 8 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 600] /Contents 10 0 R >>",
		"<< /Length 0 >>\nstream\nendstream",
		"<< /Type /Outlines /First 12 0 R /Last 15 0 R /Count 5 >>",
		"<< /Title (Chapter) /Parent 11 0 R /Next 15 0 R /First 13 0 R /Last 13 0 R /Count 2 /Dest [5 0 R /Fit] >>",
		"<< /Title (Section) /Parent 12 0 R /First 14 0 R /Last 14 0 R /Count 1 /Dest [7 0 R /Fit] >>",
		"<< /Title (No destination) /Parent 13 0 R >>",
		"<< /Title (Next chapter) /Parent 11 0 R /Prev 12 0 R /First 16 0 R /Last 16 0 R /Count 1 /Dest [9 0 R /Fit] >>",
		"<< /Title (Missing destination) /Parent 15 0 R /Dest /Missing >>",
	})
}

func TestOutline(t *testing.T) {
	qpdf, err := exec.LookPath("qpdf")
	if err != nil {
		t.Skip("outline integration cases skipped: install qpdf")
	}
	tools := Tools{qpdf: qpdf}
	t.Chdir(t.TempDir())
	path := "-outline;$(touch unexpected).pdf"
	outlinePDF(t, path)
	encrypted := filepath.Join(t.TempDir(), "encrypted.pdf")
	if out, err := exec.Command(qpdf, "--encrypt", "reader-password", "owner-password", "256", "--", "./"+path, encrypted).CombinedOutput(); err != nil {
		t.Fatalf("encrypt outline PDF: %v: %s", err, out)
	}
	plain := filepath.Join(t.TempDir(), "plain.pdf")
	writePDF(t, plain, "", []testPage{{300, 600, 0, "No outline"}})
	invalid := filepath.Join(t.TempDir(), "invalid.pdf")
	if err := os.WriteFile(invalid, []byte("not a PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	want := []OutlineEntry{{"Chapter", 1, 0}, {"Section", 2, 1}, {"No destination", 0, 2}, {"Next chapter", 3, 0}, {"Missing destination", 0, 1}}
	for _, tc := range []struct {
		name, path, password string
		want                 []OutlineEntry
		err                  error
	}{
		{"nested and option-like path", path, "", want, nil},
		{"encrypted missing password", encrypted, "", nil, ErrPasswordRequired},
		{"encrypted wrong password", encrypted, "wrong", nil, ErrWrongPassword},
		{"encrypted correct password", encrypted, "reader-password", want, nil},
		{"no outline", plain, "", []OutlineEntry{}, nil},
		{"invalid PDF", invalid, "", nil, ErrInvalidPDF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tools.Outline(context.Background(), tc.path, tc.password)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Outline = %+v, %v; want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestOutlineToolMissing(t *testing.T) {
	for _, tools := range []Tools{{}, {qpdf: filepath.Join(t.TempDir(), "absent-qpdf")}} {
		got, err := tools.Outline(context.Background(), "unused.pdf", "")
		if got != nil || !errors.Is(err, ErrOutlineToolMissing) || !strings.Contains(err.Error(), "install qpdf") {
			t.Fatalf("Outline = %+v, %v; want installation message", got, err)
		}
	}
}

func TestOutlineCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Tools{}).Outline(ctx, "unused.pdf", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Outline: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("process exit observation uses Unix signals")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "qpdf-pid")
	t.Setenv("PDF_TEST_SLOW_RENDER", marker)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		entries []OutlineEntry
		err     error
	}
	done := make(chan result, 1)
	go func() {
		entries, err := (Tools{qpdf: executable}).Outline(ctx, "unused.pdf", "")
		done <- result{entries, err}
	}()
	var pid int
	deadline := time.After(5 * time.Second)
	for pid == 0 {
		if data, err := os.ReadFile(marker); err == nil {
			pid, _ = strconv.Atoi(string(data))
		}
		if pid != 0 {
			break
		}
		select {
		case got := <-done:
			t.Fatalf("qpdf exited before cancellation: %v", got.err)
		case <-deadline:
			t.Fatal("qpdf did not start within 5 seconds")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) || got.entries != nil {
			t.Fatalf("canceled Outline = %+v, %v", got.entries, got.err)
		}
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("qpdf process %d remains after cancellation: %v", pid, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled Outline did not return within 2 seconds")
	}
}
