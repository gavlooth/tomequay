package terminal

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func decodeClipboardOutput(t *testing.T, data string, tmux bool) string {
	t.Helper()
	if tmux {
		if !strings.HasPrefix(data, "\x1bPtmux;") {
			t.Fatal("missing tmux DCS prefix")
		}
		data = data[len("\x1bPtmux;"):]
		var inner strings.Builder
		for {
			i := strings.IndexByte(data, '\x1b')
			if i < 0 || i+1 == len(data) {
				t.Fatal("unterminated tmux DCS")
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
			if len(data) != 1 {
				t.Fatal("unexpected output after tmux DCS")
			}
			break
		}
		data = inner.String()
	}
	if !strings.HasPrefix(data, "\x1b]52;c;") || !strings.HasSuffix(data, "\a") {
		t.Fatal("expected OSC 52 clipboard selection c terminated by BEL")
	}
	payload := data[len("\x1b]52;c;") : len(data)-1]
	for _, ch := range []byte(payload) {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '+' || ch == '/' || ch == '=') {
			t.Fatalf("unexpected byte %#x in base64 payload", ch)
		}
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func TestWriteClipboardRoundTrip(t *testing.T) {
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, text := range []string{
		"Hello, 世界! Ελληνικά 👩🏽‍💻 e\u0301\n\t",
		"\x1b]52;c;ZXZpbA==\a\x1b\\\x1b[2J\x1bPtmux;\x00\r\n\x7f\u009b\u009d",
		string(allBytes),
	} {
		for _, tmux := range []bool{false, true} {
			t.Run(fmt.Sprintf("tmux=%t/bytes=%d", tmux, len(text)), func(t *testing.T) {
				var output bytes.Buffer
				if err := WriteClipboard(&output, text, tmux); err != nil {
					t.Fatal(err)
				}
				if got := decodeClipboardOutput(t, output.String(), tmux); got != text {
					t.Fatalf("clipboard text = %q, want %q", got, text)
				}
			})
		}
	}
}

func TestWriteClipboardChunkSizes(t *testing.T) {
	for _, size := range []int{1, 2, 3, 3071, 3072, 3073, 6143, 6144, 6145} {
		text := strings.Repeat("\xff\x1b\x00", (size+2)/3)[:size]
		for _, tmux := range []bool{false, true} {
			t.Run(fmt.Sprintf("tmux=%t/bytes=%d", tmux, size), func(t *testing.T) {
				var output bytes.Buffer
				if err := WriteClipboard(&output, text, tmux); err != nil {
					t.Fatal(err)
				}
				if got := decodeClipboardOutput(t, output.String(), tmux); got != text {
					t.Fatal("text changed across base64 chunks")
				}
			})
		}
	}
}

func TestWriteClipboardTmuxSizeLimit(t *testing.T) {
	// Independent of the implementation's size constant: tmux stores its five
	// prefix bytes and the unescaped OSC, and needs a final NUL byte as well.
	const maxTextBytes = 786420
	if tmuxClipboardMaxBytes != maxTextBytes {
		t.Fatalf("tmux text limit = %d, want %d", tmuxClipboardMaxBytes, maxTextBytes)
	}
	for _, size := range []int{maxTextBytes - 2, maxTextBytes - 1, maxTextBytes, maxTextBytes + 1, maxTextBytes + 2, maxTextBytes + 3} {
		text := strings.Repeat("x", size)
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var output bytes.Buffer
			err := WriteClipboard(&output, text, true)
			if size > maxTextBytes {
				if err == nil || output.Len() != 0 {
					t.Fatal("oversize tmux selection must fail before writing")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := decodeClipboardOutput(t, output.String(), true); got != text {
				t.Fatal("tmux text was truncated")
			}
			storedBytes := len("tmux;") + len("\x1b]52;c;") + base64.StdEncoding.EncodedLen(size) + 1
			if storedBytes+1 > 1<<20 {
				t.Fatalf("tmux input requires %d bytes including NUL", storedBytes+1)
			}
		})
	}
	var output bytes.Buffer
	text := strings.Repeat("x", maxTextBytes+3)
	if err := WriteClipboard(&output, text, false); err != nil {
		t.Fatal(err)
	}
	if got := decodeClipboardOutput(t, output.String(), false); got != text {
		t.Fatal("direct output must not inherit the tmux size limit")
	}
}

func TestWriteClipboardInvalidArguments(t *testing.T) {
	for _, tmux := range []bool{false, true} {
		var output bytes.Buffer
		if err := WriteClipboard(&output, "", tmux); err == nil {
			t.Fatal("empty selection must return an error")
		}
		if output.Len() != 0 {
			t.Fatal("empty selection must not clear the clipboard")
		}
		if err := WriteClipboard(nil, "text", tmux); err == nil {
			t.Fatal("nil writer must return an error")
		}
	}
}

type clipboardFailWriter struct {
	output             bytes.Buffer
	remaining          int
	err                error
	failed             bool
	writesAfterFailure int
}

func (w *clipboardFailWriter) Write(p []byte) (int, error) {
	if w.failed {
		w.writesAfterFailure++
	}
	n := min(len(p), w.remaining)
	_, _ = w.output.Write(p[:n])
	w.remaining -= n
	if n != len(p) || w.remaining == 0 && w.err != nil {
		w.failed = true
		return n, w.err
	}
	return n, nil
}

func TestWriteClipboardWriteFailures(t *testing.T) {
	failure := errors.New("clipboard writer failure")
	text := strings.Repeat("abc", 3073)
	for _, tmux := range []bool{false, true} {
		var complete bytes.Buffer
		if err := WriteClipboard(&complete, text, tmux); err != nil {
			t.Fatal(err)
		}
		for _, offset := range []int{0, 1, 7, 14, 15, 100, 4103, 4111, complete.Len() - 3, complete.Len() - 2, complete.Len() - 1} {
			for _, writeErr := range []error{failure, nil} {
				t.Run(fmt.Sprintf("tmux=%t/offset=%d/error=%v", tmux, offset, writeErr), func(t *testing.T) {
					writer := &clipboardFailWriter{remaining: offset, err: writeErr}
					want := writeErr
					if want == nil {
						want = io.ErrShortWrite
					}
					if err := WriteClipboard(writer, text, tmux); !errors.Is(err, want) {
						t.Fatalf("write error = %v, want %v", err, want)
					}
					if writer.writesAfterFailure != 0 {
						t.Fatal("continued writing after a failed or short write")
					}
					if !bytes.Equal(writer.output.Bytes(), complete.Bytes()[:offset]) {
						t.Fatal("failure output differs from the expected prefix")
					}
				})
			}
		}
	}
}

type clipboardFlushWriter struct {
	bytes.Buffer
	flushes int
}

func (w *clipboardFlushWriter) Flush() error {
	w.flushes++
	return nil
}

func TestWriteClipboardDoesNotFlush(t *testing.T) {
	writer := &clipboardFlushWriter{}
	if err := WriteClipboard(writer, "text", true); err != nil {
		t.Fatal(err)
	}
	if writer.flushes != 0 {
		t.Fatal("clipboard output must leave flushing to its caller")
	}
	if got := decodeClipboardOutput(t, writer.String(), true); got != "text" {
		t.Fatalf("clipboard text = %q, want text", got)
	}
}
