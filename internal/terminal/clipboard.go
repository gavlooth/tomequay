package terminal

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// tmux reserves one byte for NUL in its 1 MiB input buffer. The stored DCS
// contents are "tmux;", the unescaped OSC prefix, the base64 text, and BEL.
const tmuxClipboardMaxBytes = ((1<<20 - 1 - len("tmux;") - len("\x1b]52;c;") - 1) / 4) * 3

// WriteClipboard sends text to clipboard selection c using OSC 52. Empty text
// is an error and produces no output, so it cannot clear the clipboard. Text
// bytes are base64 encoded, including any embedded terminal escape sequences.
//
// With tmux, the OSC is wrapped in DCS passthrough with inner ESC bytes doubled.
// Text longer than 786420 bytes is rejected before writing to fit tmux's default
// 1 MiB input buffer. Without tmux there is no imposed size limit. The receiving
// terminal may impose a smaller limit or disable clipboard writes entirely.
//
// Success means bytes reached w, not that the clipboard changed. WriteClipboard
// neither flushes nor waits for a reply. The caller must serialize it with all
// other terminal output. A write failure may leave a partial terminal sequence.
func WriteClipboard(w io.Writer, text string, tmux bool) error {
	if text == "" {
		return errors.New("terminal: empty clipboard selection")
	}
	if w == nil {
		return errors.New("terminal: nil clipboard writer")
	}
	if tmux && len(text) > tmuxClipboardMaxBytes {
		return fmt.Errorf("terminal: clipboard selection exceeds tmux limit of %d bytes", tmuxClipboardMaxBytes)
	}
	prefix, suffix := "\x1b]52;c;", "\a"
	if tmux {
		prefix, suffix = "\x1bPtmux;\x1b\x1b]52;c;", "\a\x1b\\"
	}
	var input [3072]byte
	var output [4096 + 32]byte
	frame := append(output[:0], prefix...)
	for len(text) > 0 {
		n := copy(input[:], text)
		text = text[n:]
		start := len(frame)
		frame = frame[:start+base64.StdEncoding.EncodedLen(n)]
		base64.StdEncoding.Encode(frame[start:], input[:n])
		if text == "" {
			frame = append(frame, suffix...)
		}
		written, err := w.Write(frame)
		if err != nil {
			return fmt.Errorf("terminal: write clipboard: %w", err)
		}
		if written != len(frame) {
			return io.ErrShortWrite
		}
		frame = output[:0]
	}
	return nil
}
