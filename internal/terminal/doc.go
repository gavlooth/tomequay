// Package terminal provides the Linux raw tty and input layer for the reader.
// Open uses stdin/stdout and enters the alternate screen. Immediately defer
// Terminal.Close after a successful Open: Go cannot intercept an arbitrary
// caller panic, but deferred Close restores the screen and tty during unwinding.
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
// keys.End. Unsupported control sequences, mouse/focus reports, and
// bracketed-paste content are ignored.
// Cell-size replies never reach the public event stream.
// Kitty graphics acknowledgements are emitted as GraphicsReplyEvent on Events.
// The parser accepts APC G replies carrying a nonzero uint32 image ID and a
// printable ASCII message; OK means exactly "OK". Invalid replies and replies
// exceeding 4096 bytes are ignored. Upload matching, timeouts, and retries belong
// to the application event loop, not this package.
//
// Size reads the pane's TIOCGWINSZ, using its pixel dimensions when available.
// Otherwise it asks for cell pixels with CSI 16 t, wrapped in tmux passthrough
// when TMUX is set, and waits at most 300 milliseconds for the shared input
// reader. The fallback is 9x21 pixels per cell (80x24 cells if ioctl dimensions
// are absent). Queries run at startup and whenever Size is called, including
// after SIGWINCH. The query may flush buffered output; keep application drawing
// in a single event loop and call Flush exactly once after each complete frame.
package terminal
