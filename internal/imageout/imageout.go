// Package imageout emits kitty graphics with Unicode placeholder placements.
// Graphics APCs can be wrapped individually in tmux DCS passthrough; placeholder
// cells always travel as ordinary pane-local text. The published protocol table
// has 297 entries, so grids are limited to 297 columns and 297 rows. No clipping
// is performed. Callers own image IDs, terminal bounds, flushing, and deletion
// on cache eviction or exit. This package neither reads the terminal nor flushes
// its writer.
package imageout

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const pngChunkBytes = 3072

// Output writes image data and placeholder text to a terminal writer. It must
// be used from one goroutine only, serialized with all other terminal drawing.
// New must be used to initialize an Output.
type Output struct {
	w     io.Writer
	tmux  bool
	input [pngChunkBytes]byte
	frame [4096 + 256]byte
	row   [len(rowColumnDiacritics)*16 + 128]byte
}

// New creates an output using w. When tmux is true, every graphics APC is
// separately wrapped in tmux passthrough, with all inner ESC bytes doubled.
func New(w io.Writer, tmux bool) *Output {
	return &Output{w: w, tmux: tmux}
}

// SendPNG streams PNG bytes in base64 payloads of at most 4096 bytes and creates
// a virtual placement. It does not decode or validate the PNG. Empty input is an
// error. A failed upload may have sent partial data; the caller should Delete
// the ID when abandoning it. The reader is never closed by SendPNG.
func (o *Output) SendPNG(id uint32, png io.Reader, columns, rows int) error {
	if err := validateGrid(id, columns, rows); err != nil {
		return err
	}
	if png == nil {
		return errors.New("imageout: nil PNG reader")
	}
	first := true
	prefix := 0
	var next [1]byte
	for {
		n, err := io.ReadFull(png, o.input[prefix:])
		n += prefix
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("imageout: read PNG: %w", err)
		}
		if n == 0 {
			return errors.New("imageout: empty PNG")
		}
		more := false
		if err == nil {
			_, err = io.ReadFull(png, next[:])
			if err != nil && err != io.EOF {
				return fmt.Errorf("imageout: read PNG: %w", err)
			}
			more = err == nil
		}
		b := o.beginAPC()
		if first {
			b = append(b, "a=T,f=100,t=d,q=2,i="...)
			b = strconv.AppendUint(b, uint64(id), 10)
			b = append(b, ",c="...)
			b = strconv.AppendInt(b, int64(columns), 10)
			b = append(b, ",r="...)
			b = strconv.AppendInt(b, int64(rows), 10)
			b = append(b, ",C=1,U=1,"...)
		}
		b = append(b, "m="...)
		if more {
			b = append(b, '1', ';')
		} else {
			b = append(b, '0', ';')
		}
		start := len(b)
		b = b[:start+base64.StdEncoding.EncodedLen(n)]
		base64.StdEncoding.Encode(b[start:], o.input[:n])
		if err := o.endAPC(b); err != nil {
			return err
		}
		if !more {
			return nil
		}
		o.input[0] = next[0]
		prefix = 1
		first = false
	}
}

// Place creates a virtual placement of cached image data at the new grid size.
// It does not draw cells or retransmit the image.
func (o *Output) Place(id uint32, columns, rows int) error {
	if err := validateGrid(id, columns, rows); err != nil {
		return err
	}
	b := append(o.beginAPC(), "a=p,U=1,i="...)
	b = strconv.AppendUint(b, uint64(id), 10)
	b = append(b, ",c="...)
	b = strconv.AppendInt(b, int64(columns), 10)
	b = append(b, ",r="...)
	b = strconv.AppendInt(b, int64(rows), 10)
	b = append(b, ",q=2;"...)
	return o.endAPC(b)
}

// WriteCells draws a placeholder grid at pane-local, zero-based (x, y), then
// resets text attributes. Coordinates must be nonnegative and the whole grid
// must fit the caller's pane. Rows and columns index the published diacritic
// table; values beyond its 297 entries are errors, not silently clipped.
// The foreground encodes the low 24 ID bits as truecolor. A third diacritic
// explicitly encodes the high byte, including zero, to prevent inheritance of
// a neighboring placeholder's high ID byte. Cells never use tmux passthrough.
func (o *Output) WriteCells(id uint32, x, y, columns, rows int) error {
	if err := validateGrid(id, columns, rows); err != nil {
		return err
	}
	maxInt := int(^uint(0) >> 1)
	if x < 0 || y < 0 || x > maxInt-columns || y > maxInt-rows {
		return errors.New("imageout: invalid cell coordinates")
	}
	for row := range rows {
		b := append(o.row[:0], "\x1b["...)
		b = strconv.AppendInt(b, int64(y+row+1), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(x+1), 10)
		b = append(b, 'H')
		if row == 0 {
			b = append(b, "\x1b[38;2;"...)
			b = strconv.AppendUint(b, uint64((id>>16)&255), 10)
			b = append(b, ';')
			b = strconv.AppendUint(b, uint64((id>>8)&255), 10)
			b = append(b, ';')
			b = strconv.AppendUint(b, uint64(id&255), 10)
			b = append(b, 'm')
		}
		for column := range columns {
			b = utf8.AppendRune(b, '\U0010EEEE')
			b = utf8.AppendRune(b, rowColumnDiacritics[row])
			b = utf8.AppendRune(b, rowColumnDiacritics[column])
			b = utf8.AppendRune(b, rowColumnDiacritics[id>>24])
		}
		if row == rows-1 {
			b = append(b, "\x1b[0m"...)
		}
		if err := o.write(b); err != nil {
			return err
		}
	}
	return nil
}

// Delete removes only the given image ID's placements and stored image data.
// It does not erase placeholder text. ID zero is rejected to avoid an implicit
// terminal-selected or broad deletion.
func (o *Output) Delete(id uint32) error {
	if id == 0 {
		return errors.New("imageout: image ID must be nonzero")
	}
	b := append(o.beginAPC(), "a=d,d=I,i="...)
	b = strconv.AppendUint(b, uint64(id), 10)
	b = append(b, ",q=2;"...)
	return o.endAPC(b)
}

func validateGrid(id uint32, columns, rows int) error {
	if id == 0 {
		return errors.New("imageout: image ID must be nonzero")
	}
	if columns <= 0 || rows <= 0 || columns > len(rowColumnDiacritics) || rows > len(rowColumnDiacritics) {
		return fmt.Errorf("imageout: grid must be between 1 and %d cells in each dimension", len(rowColumnDiacritics))
	}
	return nil
}

func (o *Output) beginAPC() []byte {
	b := o.frame[:0]
	if o.tmux {
		b = append(b, "\x1bPtmux;\x1b"...)
	}
	return append(b, "\x1b_G"...)
}

func (o *Output) endAPC(b []byte) error {
	// All APC contents between the delimiters are ASCII control keys or base64,
	// so the opening and closing delimiters are the only inner ESC bytes.
	if o.tmux {
		b = append(b, '\x1b')
	}
	b = append(b, "\x1b\\"...)
	if o.tmux {
		b = append(b, "\x1b\\"...)
	}
	return o.write(b)
}

func (o *Output) write(b []byte) error {
	if o.w == nil {
		return errors.New("imageout: nil writer")
	}
	n, err := o.w.Write(b)
	if err != nil {
		return fmt.Errorf("imageout: write: %w", err)
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}
