// Package terminal provides the Linux raw tty and input layer for the reader.
// Open uses stdin/stdout and enters the alternate screen with mouse reporting.
// Immediately defer Terminal.Close after a successful Open: Go cannot intercept
// an arbitrary caller panic, but deferred Close restores mouse modes, screen,
// and tty during unwinding.
// Close is idempotent. SIGTERM, SIGINT, and SIGHUP deliver an ErrorEvent with
// *SignalError, allowing the application's event loop to delete owned images
// before calling Close. If Close is not called within 300 ms, the terminal
// restores itself and closes Events. Exit the event loop when Events closes.
// SIGKILL and os.Exit cannot run deferred cleanup.
//
// A single goroutine reads stdin. Its pure streaming parser preserves incomplete
// UTF-8 and escape sequences across reads. A lone Escape is resolved after a
// read wait of at most 25 milliseconds. Alt uses an Escape event followed by the
// ordinary key because keys.Key has no Alt flag. Home and End use keys.Home and
// keys.End. Unsupported sequences, legacy X10 mouse reports, focus reports, and
// bracketed-paste content are ignored.
// Ctrl-^ (byte 0x1e) is represented as keys.Key{Rune: '^', Ctrl: true}.
// Cell-size replies never reach the public event stream.
// Kitty graphics acknowledgements are emitted as GraphicsReplyEvent on Events.
// The parser accepts APC G replies carrying a nonzero uint32 image ID and a
// printable ASCII message; OK means exactly "OK". Invalid replies and replies
// exceeding 4096 bytes are ignored. Upload matching, timeouts, and retries belong
// to the application event loop, not this package.
//
// Suspend pauses that same stdin reader, disables mouse reports, leaves the
// alternate screen, and restores cooked mode before a foreground tty program
// runs. Resume restores raw mode and the alternate screen before releasing the
// reader. The app must finish pending graphics replies before Suspend, then
// redraw and re-upload its image on Resume. In tmux, popup pickers do not suspend
// the reader or leave the screen; graphics replies continue through Events.
//
// Open enables xterm button reporting (1000), button-held drag motion (1002),
// and SGR mouse encoding (1006). MouseEvent reports presses, releases, drags,
// and vertical or horizontal wheel movement with zero-based cell coordinates
// and Shift, Alt, and Ctrl modifiers. Wheel events have ButtonNone. SGR reports
// are decoded across reads, including coordinates larger than legacy X10 can
// represent; malformed reports are discarded rather than emitted as keys.
// SetMouse(false) temporarily disables reporting, for example to allow text
// selection; SetMouse(true) enables it again. Repeated settings are idempotent.
// Close, signal restoration, and deferred Close during panic reset enabled
// mouse modes. In tmux, enable the tmux mouse option (set -g mouse on) so the pane
// receives SGR reports. Mouse mode changes go directly to tmux, without DCS
// passthrough; tmux forwards the mouse events to the application.
//
// Size reads the pane's TIOCGWINSZ, using its pixel dimensions when available.
// Otherwise it asks for cell pixels with CSI 16 t, wrapped in tmux passthrough
// when TMUX is set, and waits at most 300 milliseconds for the shared input
// reader. The fallback is 9x21 pixels per cell (80x24 cells if ioctl dimensions
// are absent). Queries run at startup and whenever Size is called, including
// after SIGWINCH. The query may flush buffered output; keep application drawing
// in a single event loop and call Flush exactly once after each complete frame.
package terminal
