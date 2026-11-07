package imageout

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
)

func wrapAPC(apc string) string {
	return "\x1bPtmux;" + strings.ReplaceAll(apc, "\x1b", "\x1b\x1b") + "\x1b\\"
}

func parseAPCs(t *testing.T, data string, tmux bool) []string {
	t.Helper()
	var apcs []string
	for data != "" {
		if tmux {
			if !strings.HasPrefix(data, "\x1bPtmux;") {
				t.Fatalf("missing per-APC wrapper: %q", data[:min(80, len(data))])
			}
			data = data[len("\x1bPtmux;"):]
			var inner strings.Builder
			for {
				i := strings.IndexByte(data, '\x1b')
				if i < 0 || i+1 == len(data) {
					t.Fatal("unterminated tmux wrapper")
				}
				inner.WriteString(data[:i])
				data = data[i+1:]
				if data[0] == '\x1b' {
					inner.WriteByte('\x1b')
					data = data[1:]
					continue
				}
				if data[0] != '\\' {
					t.Fatal("inner ESC not doubled")
				}
				data = data[1:]
				break
			}
			apc := inner.String()
			if !strings.HasPrefix(apc, "\x1b_G") || !strings.HasSuffix(apc, "\x1b\\") || strings.Count(apc, "\x1b_G") != 1 {
				t.Fatal("wrapper does not contain exactly one complete APC")
			}
			apcs = append(apcs, strings.TrimSuffix(strings.TrimPrefix(apc, "\x1b_G"), "\x1b\\"))
			continue
		}
		if !strings.HasPrefix(data, "\x1b_G") {
			t.Fatal("missing APC prefix")
		}
		i := strings.Index(data, "\x1b\\")
		if i < 0 {
			t.Fatal("unterminated APC")
		}
		apcs = append(apcs, data[3:i])
		data = data[i+2:]
	}
	return apcs
}

func TestSendPNGRoundTrip(t *testing.T) {
	// A real PNG, deliberately not compressible into a single transport chunk.
	img := image.NewNRGBA(image.Rect(0, 0, 100, 100))
	state := uint32(17)
	for y := range 100 {
		for x := range 100 {
			state = state*1664525 + 1013904223
			img.SetNRGBA(x, y, color.NRGBA{R: byte(state), G: byte(state >> 8), B: byte(state >> 16), A: 255})
		}
	}
	var original bytes.Buffer
	if err := png.Encode(&original, img); err != nil {
		t.Fatal(err)
	}
	if original.Len() <= pngChunkBytes {
		t.Fatal("fixture must exceed one chunk")
	}
	for _, tmux := range []bool{false, true} {
		t.Run(fmt.Sprint(tmux), func(t *testing.T) {
			var output bytes.Buffer
			if err := New(&output, tmux).SendPNG(66051, bytes.NewReader(original.Bytes()), 30, 18); err != nil {
				t.Fatal(err)
			}
			apcs := parseAPCs(t, output.String(), tmux)
			if len(apcs) != (original.Len()+pngChunkBytes-1)/pngChunkBytes {
				t.Fatalf("unexpected chunk count %d", len(apcs))
			}
			var decoded bytes.Buffer
			for i, apc := range apcs {
				header, payload, ok := strings.Cut(apc, ";")
				if !ok || len(payload) > 4096 || (i < len(apcs)-1 && len(payload)%4 != 0) {
					t.Fatalf("invalid payload in APC %d", i)
				}
				wantHeader := ""
				if i == 0 {
					wantHeader = "a=T,f=100,t=d,q=0,i=66051,c=30,r=18,C=1,U=1,"
				}
				if i < len(apcs)-1 {
					wantHeader += "m=1"
				} else {
					wantHeader += "m=0,q=0"
				}
				if header != wantHeader {
					t.Fatalf("header = %q, want %q", header, wantHeader)
				}
				chunk, err := base64.StdEncoding.DecodeString(payload)
				if err != nil {
					t.Fatal(err)
				}
				decoded.Write(chunk)
			}
			if !bytes.Equal(decoded.Bytes(), original.Bytes()) {
				t.Fatal("PNG changed during streaming")
			}
		})
	}
}

func TestSendPNGChunkBoundaries(t *testing.T) {
	for _, size := range []int{1, 2, 3, 3071, 3072, 3073, 6144, 6145} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			input := bytes.Repeat([]byte{0x1b, 0xff, 0x00}, (size+2)/3)[:size]
			var output bytes.Buffer
			if err := New(&output, true).SendPNG(0xffffffff, &smallReader{data: input}, 1, 1); err != nil {
				t.Fatal(err)
			}
			apcs := parseAPCs(t, output.String(), true)
			var payload strings.Builder
			for i, apc := range apcs {
				header, chunk, _ := strings.Cut(apc, ";")
				wantMore := "m=1"
				if i == len(apcs)-1 {
					wantMore = "m=0"
					if i > 0 {
						wantMore += ",q=0"
					}
				}
				if !strings.HasSuffix(header, wantMore) || len(chunk) > 4096 || len(chunk) == 0 {
					t.Fatalf("invalid chunk %d: %q (%d bytes)", i, header, len(chunk))
				}
				if i == 0 && !strings.Contains(header, "q=0,") {
					t.Fatal("first upload APC must request acknowledgements")
				}
				payload.WriteString(chunk)
			}
			decoded, err := base64.StdEncoding.DecodeString(payload.String())
			if err != nil || !bytes.Equal(decoded, input) {
				t.Fatalf("round trip failed: %v", err)
			}
		})
	}
}

type smallReader struct{ data []byte }

func (r *smallReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(7, len(p))], r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestWriteCells(t *testing.T) {
	for _, id := range []uint32{66051, 0x02010203, 0x01000000, 0xff010203} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			var output bytes.Buffer
			if err := New(&output, true).WriteCells(id, 2, 3, 3, 2); err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for row := range 2 {
				fmt.Fprintf(&want, "\x1b[%d;3H", 4+row)
				if row == 0 {
					if id == 0x01000000 {
						want.WriteString("\x1b[38;2;0;0;0m")
					} else {
						want.WriteString("\x1b[38;2;1;2;3m")
					}
				}
				for column := range 3 {
					want.WriteRune('\U0010EEEE')
					want.WriteRune([]rune{'\u0305', '\u030D'}[row])
					want.WriteRune([]rune{'\u0305', '\u030D', '\u030E'}[column])
					want.WriteRune(rowColumnDiacritics[id>>24])
				}
			}
			want.WriteString("\x1b[0m")
			if output.String() != want.String() {
				t.Fatalf("cells = %q, want %q", output.String(), want.String())
			}
			if strings.Contains(output.String(), "\x1bPtmux;") || strings.Count(output.String(), "\U0010EEEE") != 6 {
				t.Fatal("placeholders must be ordinary text, one per cell")
			}
		})
	}
}

func TestPublishedTableBoundaries(t *testing.T) {
	if MaxGridSize != 297 {
		t.Fatalf("MaxGridSize = %d, want 297", MaxGridSize)
	}
	if len(rowColumnDiacritics) != 297 || rowColumnDiacritics[0] != '\u0305' || rowColumnDiacritics[1] != '\u030D' || rowColumnDiacritics[296] != '\U0001D244' {
		t.Fatal("published kitty table differs")
	}
	for _, dimensions := range [][2]int{{297, 1}, {1, 297}} {
		var output bytes.Buffer
		if err := New(&output, false).WriteCells(1, 0, 0, dimensions[0], dimensions[1]); err != nil {
			t.Fatal(err)
		}
		if strings.Count(output.String(), "\U0010EEEE") != 297 || !strings.ContainsRune(output.String(), '\U0001D244') {
			t.Fatal("last supported diacritic was not emitted")
		}
	}
}

func TestPlaceAndDeleteExactBytes(t *testing.T) {
	for _, tmux := range []bool{false, true} {
		var output bytes.Buffer
		out := New(&output, tmux)
		if err := out.Place(66051, 30, 18); err != nil {
			t.Fatal(err)
		}
		want := "\x1b_Ga=p,U=1,i=66051,c=30,r=18,q=2;\x1b\\"
		if tmux {
			want = wrapAPC(want)
		}
		if output.String() != want {
			t.Fatalf("place = %q, want %q", output.String(), want)
		}
		output.Reset()
		if err := out.Delete(66051); err != nil {
			t.Fatal(err)
		}
		want = "\x1b_Ga=d,d=I,i=66051,q=2;\x1b\\"
		if tmux {
			want = wrapAPC(want)
		}
		if output.String() != want {
			t.Fatalf("delete = %q, want %q", output.String(), want)
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	var output bytes.Buffer
	out := New(&output, true)
	for _, grid := range []struct {
		id            uint32
		columns, rows int
	}{{0, 1, 1}, {1, 0, 1}, {1, 1, 0}, {1, -1, 1}, {1, 1, -1}, {1, 298, 1}, {1, 1, 298}} {
		if out.SendPNG(grid.id, strings.NewReader("PNG"), grid.columns, grid.rows) == nil ||
			out.Place(grid.id, grid.columns, grid.rows) == nil ||
			out.WriteCells(grid.id, 0, 0, grid.columns, grid.rows) == nil {
			t.Fatalf("accepted invalid grid %+v", grid)
		}
	}
	maxInt := int(^uint(0) >> 1)
	for _, point := range [][2]int{{-1, 0}, {0, -1}, {maxInt, 0}, {0, maxInt}} {
		if out.WriteCells(1, point[0], point[1], 1, 1) == nil {
			t.Fatalf("accepted invalid coordinate %v", point)
		}
	}
	if out.Delete(0) == nil || out.SendPNG(1, nil, 1, 1) == nil || out.SendPNG(1, strings.NewReader(""), 1, 1) == nil {
		t.Fatal("accepted zero ID, nil reader, or empty PNG")
	}
	if output.Len() != 0 {
		t.Fatal("invalid arguments must not produce output")
	}
	if New(nil, false).Delete(1) == nil {
		t.Fatal("nil writer must return an error")
	}
}

var errFixture = errors.New("fixture I/O failure")

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errFixture }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errFixture }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestIOErrors(t *testing.T) {
	var output bytes.Buffer
	if err := New(&output, false).SendPNG(1, errorReader{}, 1, 1); !errors.Is(err, errFixture) {
		t.Fatalf("reader error = %v", err)
	}
	reader := io.MultiReader(bytes.NewReader(make([]byte, 3072)), errorReader{})
	if err := New(&output, false).SendPNG(1, reader, 1, 1); !errors.Is(err, errFixture) {
		t.Fatalf("lookahead reader error = %v", err)
	}
	for _, writer := range []io.Writer{errorWriter{}, shortWriter{}} {
		want := errFixture
		if _, ok := writer.(shortWriter); ok {
			want = io.ErrShortWrite
		}
		out := New(writer, true)
		for _, err := range []error{
			out.SendPNG(1, strings.NewReader("PNG"), 1, 1),
			out.Place(1, 1, 1), out.WriteCells(1, 0, 0, 1, 1), out.Delete(1),
		} {
			if !errors.Is(err, want) {
				t.Fatalf("writer error = %v, want %v", err, want)
			}
		}
	}
}

type streamingReader struct {
	t      *testing.T
	source *bytes.Reader
	output *bytes.Buffer
	read   int
}

func (r *streamingReader) Read(p []byte) (int, error) {
	if len(p) > 3072 {
		r.t.Fatalf("read buffer = %d bytes, want at most 3072", len(p))
	}
	if r.read >= 3073 && r.output.Len() == 0 {
		r.t.Fatal("reader consumed more than one chunk and lookahead before emitting output")
	}
	n, err := r.source.Read(p)
	r.read += n
	return n, err
}

func TestSendPNGStreams(t *testing.T) {
	var output bytes.Buffer
	input := bytes.Repeat([]byte("streamed PNG bytes"), 1000)
	reader := &streamingReader{t: t, source: bytes.NewReader(input), output: &output}
	if err := New(&output, true).SendPNG(1, reader, 1, 1); err != nil {
		t.Fatal(err)
	}
	if reader.read != len(input) {
		t.Fatalf("read %d bytes, want %d", reader.read, len(input))
	}
}
