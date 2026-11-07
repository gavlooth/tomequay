package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"terminal-pdf-reader/internal/keys"
)

// Size describes pane cells and the pixel dimensions of one cell.
type Size struct {
	Columns, Rows                     int
	CellWidthPixels, CellHeightPixels int
}

type ResizeEvent struct{ Size Size }
type KeyEvent struct{ Key keys.Key }
type ErrorEvent struct{ Err error }

// MouseKind describes a button, drag, or wheel action.
type MouseKind int

const (
	MousePress MouseKind = iota
	MouseRelease
	MouseDrag
	MouseWheelUp
	MouseWheelDown
	MouseWheelLeft
	MouseWheelRight
)

// MouseButton identifies a button; wheel actions use ButtonNone.
type MouseButton int

const (
	ButtonLeft MouseButton = iota
	ButtonMiddle
	ButtonRight
	ButtonNone
)

// MouseEvent uses zero-based cell coordinates and preserves modifier keys.
type MouseEvent struct {
	Kind             MouseKind
	Button           MouseButton
	Column, Row      int
	Shift, Alt, Ctrl bool
}

// GraphicsReplyEvent acknowledges a Kitty graphics command for an image ID.
// OK is true only for the exact success message "OK".
type GraphicsReplyEvent struct {
	ID      uint32
	OK      bool
	Message string
}

// SignalError asks the application's event loop to clean up and exit. The
// terminal writer remains available for 300 ms, or until Close is called.
type SignalError struct{ Signal os.Signal }

func (e *SignalError) Error() string { return "terminal: received " + e.Signal.String() }

// Event is a KeyEvent, MouseEvent, ResizeEvent, ErrorEvent, or GraphicsReplyEvent.
// Close closes the event stream.
type Event interface{}

const (
	escapeWait      = 25 * time.Millisecond
	sizeWait        = 300 * time.Millisecond
	signalCloseWait = 300 * time.Millisecond
	enterScreen     = "\x1b[?1049h\x1b[?25l"
	leaveScreen     = "\x1b[0m\x1b[?25h\x1b[?1049l"
	enableMouse     = "\x1b[?1000h\x1b[?1002h\x1b[?1006h"
	disableMouse    = "\x1b[?1006l\x1b[?1002l\x1b[?1000l"
)

type lockedWriter struct {
	mu     sync.Mutex
	buffer *bufio.Writer
	closed bool
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	return w.buffer.Write(p)
}

func (w *lockedWriter) flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Flush()
}

func (w *lockedWriter) command(s string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	_, err := w.buffer.WriteString(s)
	return errors.Join(err, w.buffer.Flush())
}

// Terminal owns raw mode and a single stdin reader. Immediately defer Close
// after Open succeeds; the defer also restores the tty during caller panics.
type Terminal struct {
	input    int
	original *term.State
	writer   *lockedWriter
	events   chan Event
	replies  chan cellSizeReply
	signals  chan os.Signal
	done     chan struct{}
	stopped  chan struct{}
	wake     [2]int
	workers  sync.WaitGroup
	once     sync.Once
	closeErr error
	sizeMu   sync.Mutex
	mouse    bool // protected by writer.mu
}

// SetMouse enables or disables button, button-held motion, and SGR mouse
// reporting. Open enables reporting by default. Calls do not use tmux
// passthrough: these modes belong to the pane, not the outer terminal.
func (t *Terminal) SetMouse(enabled bool) error {
	t.writer.mu.Lock()
	defer t.writer.mu.Unlock()
	if t.writer.closed {
		return os.ErrClosed
	}
	if t.mouse == enabled {
		return nil
	}
	command := disableMouse
	if enabled {
		command = enableMouse
	}
	// Remember an enable attempt so Close also resets partially written modes.
	t.mouse = enabled
	_, err := t.writer.buffer.WriteString(command)
	return errors.Join(err, t.writer.buffer.Flush())
}

// InTmux reports whether output must use tmux DCS passthrough.
func InTmux() bool { return os.Getenv("TMUX") != "" }

// Open uses stdin/stdout, enables raw mode, the alternate screen, and mouse
// reporting, and starts input and signal handling. Startup failure restores state.
func Open() (_ *Terminal, err error) {
	input, output := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	if !term.IsTerminal(input) || !term.IsTerminal(output) {
		return nil, errors.New("terminal: stdin and stdout must be terminals")
	}
	if _, err := unix.IoctlGetWinsize(input, unix.TIOCGWINSZ); err != nil {
		return nil, fmt.Errorf("terminal: get window size: %w", err)
	}
	original, err := term.MakeRaw(input)
	if err != nil {
		return nil, fmt.Errorf("terminal: raw mode: %w", err)
	}
	t := &Terminal{
		input: input, original: original,
		writer: &lockedWriter{buffer: bufio.NewWriter(os.Stdout)},
		events: make(chan Event, 128), replies: make(chan cellSizeReply, 1),
		signals: make(chan os.Signal, 8), done: make(chan struct{}),
		stopped: make(chan struct{}), wake: [2]int{-1, -1},
	}
	started := false
	defer func() {
		if !started {
			_ = t.writer.command(disableMouse + leaveScreen)
			_ = term.Restore(input, original)
			for _, fd := range t.wake {
				if fd >= 0 {
					_ = unix.Close(fd)
				}
			}
		}
	}()
	if err := unix.Pipe2(t.wake[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		return nil, fmt.Errorf("terminal: wake pipe: %w", err)
	}
	if err := t.writer.command(enterScreen); err != nil {
		return nil, fmt.Errorf("terminal: enter screen: %w", err)
	}
	if err := t.SetMouse(true); err != nil {
		return nil, fmt.Errorf("terminal: enable mouse reporting: %w", err)
	}
	signal.Notify(t.signals, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	t.workers.Add(2)
	go t.readInput()
	go t.readSignals()
	go func() {
		t.workers.Wait()
		for _, fd := range t.wake {
			_ = unix.Close(fd)
		}
		close(t.events)
		close(t.stopped)
	}()
	started = true
	t.Size()
	return t, nil
}

// Close restores mouse reporting, screen, cursor, and tty mode, and stops input
// and signal handling. It is safe to call repeatedly, including from a defer.
func (t *Terminal) Close() error {
	t.shutdown()
	<-t.stopped
	return t.closeErr
}

func (t *Terminal) shutdown() {
	t.once.Do(func() {
		close(t.done)
		signal.Stop(t.signals)
		_, _ = unix.Write(t.wake[1], []byte{1})
		t.writer.mu.Lock()
		var mouseErr error
		if t.mouse {
			_, mouseErr = t.writer.buffer.WriteString(disableMouse)
			t.mouse = false
		}
		_, writeErr := t.writer.buffer.WriteString(leaveScreen)
		flushErr := t.writer.buffer.Flush()
		t.writer.closed = true
		t.writer.mu.Unlock()
		t.closeErr = errors.Join(mouseErr, writeErr, flushErr, term.Restore(t.input, t.original))
	})
}

func (t *Terminal) Events() <-chan Event { return t.events }
func (t *Terminal) Writer() io.Writer    { return t.writer }
func (t *Terminal) Flush() error         { return t.writer.flush() }

// Size prefers TIOCGWINSZ pixel geometry. Missing pixel dimensions trigger a
// cell-size query, sharing the input reader and waiting no more than 300 ms.
// Unanswered queries use 9x21 pixels; zero row/column counts use 80x24 cells.
func (t *Terminal) Size() Size {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()
	size := Size{Columns: 80, Rows: 24, CellWidthPixels: 9, CellHeightPixels: 21}
	ws, err := unix.IoctlGetWinsize(t.input, unix.TIOCGWINSZ)
	if err == nil {
		if ws.Col > 0 {
			size.Columns = int(ws.Col)
		}
		if ws.Row > 0 {
			size.Rows = int(ws.Row)
		}
		if ws.Col > 0 && ws.Row > 0 && ws.Xpixel >= ws.Col && ws.Ypixel >= ws.Row {
			size.CellWidthPixels = int(ws.Xpixel / ws.Col)
			size.CellHeightPixels = int(ws.Ypixel / ws.Row)
			return size
		}
	}
	select {
	case <-t.replies:
	default:
	}
	query := "\x1b[16t"
	if InTmux() {
		query = "\x1bPtmux;\x1b\x1b[16t\x1b\\"
	}
	if t.writer.command(query) == nil {
		timer := time.NewTimer(sizeWait)
		defer timer.Stop()
		select {
		case reply := <-t.replies:
			size.CellWidthPixels, size.CellHeightPixels = reply.width, reply.height
		case <-timer.C:
		case <-t.done:
		}
	}
	return size
}

func (t *Terminal) emit(event Event) bool {
	select {
	case t.events <- event:
		return true
	case <-t.done:
		return false
	}
}

func (t *Terminal) readSignals() {
	defer t.workers.Done()
	for {
		select {
		case <-t.done:
			return
		case sig := <-t.signals:
			if sig != syscall.SIGWINCH {
				timer := time.NewTimer(signalCloseWait)
				defer timer.Stop()
				// Include delivery time in the bound: a full event queue must
				// not prevent restoration when the caller is unresponsive.
				select {
				case t.events <- ErrorEvent{Err: &SignalError{Signal: sig}}:
					select {
					case <-t.done:
					case <-timer.C:
					}
				case <-t.done:
				case <-timer.C:
				}
				t.shutdown()
				return
			}
			if !t.emit(ResizeEvent{Size: t.Size()}) {
				return
			}
		}
	}
}

func (t *Terminal) readInput() {
	defer t.workers.Done()
	state := parserState{}
	var escapeDeadline time.Time
	var buffer [4096]byte
	polls := []unix.PollFd{{Fd: int32(t.input), Events: unix.POLLIN}, {Fd: int32(t.wake[0]), Events: unix.POLLIN}}
	for {
		wait := -1
		if state.pending == "\x1b" && !state.control && !state.paste {
			if escapeDeadline.IsZero() {
				escapeDeadline = time.Now().Add(escapeWait)
			}
			// Keep the original deadline across EINTR, rather than restarting
			// the 25 ms wait whenever a signal interrupts poll.
			wait = max(0, int(time.Until(escapeDeadline)/time.Millisecond))
		} else {
			escapeDeadline = time.Time{}
		}
		n, err := unix.Poll(polls, wait)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			t.emit(ErrorEvent{Err: fmt.Errorf("terminal: poll input: %w", err)})
			t.shutdown()
			return
		}
		if polls[1].Revents != 0 {
			return
		}
		var data []byte
		if n > 0 {
			count, err := unix.Read(t.input, buffer[:])
			if err == unix.EINTR || err == unix.EAGAIN {
				continue
			}
			if err != nil || count == 0 {
				if err == nil {
					err = io.EOF
				}
				t.emit(ErrorEvent{Err: fmt.Errorf("terminal: read input: %w", err)})
				t.shutdown()
				return
			}
			data = buffer[:count]
		}
		var events []Event
		state, events = parse(state, data, n == 0)
		if len(data) > 0 {
			escapeDeadline = time.Time{}
			if state.pending == "\x1b" && !state.control && !state.paste {
				escapeDeadline = time.Now().Add(escapeWait)
			}
		}
		for _, event := range events {
			if reply, ok := event.(cellSizeReply); ok {
				select {
				case t.replies <- reply:
				default:
				}
				continue
			}
			if !t.emit(event) {
				return
			}
		}
	}
}
