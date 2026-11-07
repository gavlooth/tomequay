package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"terminal-pdf-reader/internal/keys"
)

type ptyReport struct {
	Kind     string
	Size     Size
	Key      keys.Key
	Error    string
	Graphics GraphicsReplyEvent
}

// This subprocess keeps test-runner stdin/stdout and signal handlers untouched.
func TestTerminalPTYHelper(t *testing.T) {
	if os.Getenv("TERMINAL_PTY_HELPER") != "1" {
		return
	}
	report := json.NewEncoder(os.NewFile(3, "report"))
	tty, err := Open()
	if err != nil {
		_ = report.Encode(ptyReport{Kind: "error", Error: err.Error()})
		os.Exit(2)
	}
	defer tty.Close()
	_ = report.Encode(ptyReport{Kind: "opened", Size: tty.Size()})
	ignoreSignals := false
	for event := range tty.Events() {
		switch event := event.(type) {
		case ResizeEvent:
			_ = report.Encode(ptyReport{Kind: "resize", Size: event.Size})
		case GraphicsReplyEvent:
			_ = report.Encode(ptyReport{Kind: "graphics", Graphics: event})
		case KeyEvent:
			_ = report.Encode(ptyReport{Kind: "key", Key: event.Key})
			if event.Key.Rune == 'i' {
				ignoreSignals = true
			}
			if event.Key.Rune == 'p' {
				panic("intentional caller panic")
			}
			if event.Key.Rune == 'q' {
				goto closed
			}
		case ErrorEvent:
			if _, ok := event.Err.(*SignalError); ok {
				if ignoreSignals {
					continue
				}
				if _, err := io.WriteString(tty.Writer(), "signal-cleanup"); err != nil {
					t.Fatal(err)
				}
				if err := tty.Flush(); err != nil {
					t.Fatal(err)
				}
				goto closed
			}
			_ = report.Encode(ptyReport{Kind: "error", Error: event.Err.Error()})
		}
	}
closed:
	if err := tty.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tty.Close(); err != nil {
		t.Fatal(err)
	}
	_ = report.Encode(ptyReport{Kind: "closed"})
}

type ptySession struct {
	master, slave *os.File
	cmd           *exec.Cmd
	reports       chan ptyReport
	exited        chan error
	stop          chan struct{}
	outputDone    chan struct{}
	mu            sync.Mutex
	output        []byte
	stderr        bytes.Buffer
	original      *unix.Termios
}

func startPTY(t *testing.T, ws unix.Winsize, tmux, answer bool) *ptySession {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "ptmx")
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &ws); err != nil {
		t.Fatal(err)
	}
	original, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reportRead.Close() })
	s := &ptySession{master: master, slave: slave, reports: make(chan ptyReport, 32), exited: make(chan error, 1), stop: make(chan struct{}), outputDone: make(chan struct{}), original: original}
	s.cmd = exec.Command(os.Args[0], "-test.run=^TestTerminalPTYHelper$")
	s.cmd.Env = append(os.Environ(), "TERMINAL_PTY_HELPER=1", "TMUX=")
	if tmux {
		s.cmd.Env = append(s.cmd.Env, "TMUX=test")
	}
	s.cmd.Stdin, s.cmd.Stdout, s.cmd.Stderr = slave, slave, &s.stderr
	s.cmd.ExtraFiles = []*os.File{reportWrite}
	if err := s.cmd.Start(); err != nil {
		_ = reportWrite.Close()
		t.Fatal(err)
	}
	_ = reportWrite.Close()
	go func() { s.exited <- s.cmd.Wait() }()
	go func() {
		defer close(s.reports)
		decoder := json.NewDecoder(reportRead)
		for {
			var report ptyReport
			if decoder.Decode(&report) != nil {
				return
			}
			s.reports <- report
		}
	}()
	go func() {
		defer close(s.outputDone)
		var buffer [4096]byte
		answered := 0
		for {
			polls := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			_, _ = unix.Poll(polls, 10)
			if polls[0].Revents&unix.POLLIN != 0 {
				n, _ := unix.Read(fd, buffer[:])
				if n > 0 {
					s.mu.Lock()
					s.output = append(s.output, buffer[:n]...)
					queries := bytes.Count(s.output, []byte("\x1b[16t"))
					s.mu.Unlock()
					if answer {
						for answered < queries {
							_, _ = unix.Write(fd, []byte("\x1b[6;23;11t"))
							answered++
						}
					}
				}
			}
			select {
			case <-s.stop:
				return
			default:
			}
		}
	}()
	t.Cleanup(func() { _ = s.cmd.Process.Kill(); close(s.stop); <-s.outputDone })
	return s
}

func (s *ptySession) next(t *testing.T, kind string) ptyReport {
	t.Helper()
	select {
	case report, ok := <-s.reports:
		if !ok {
			t.Fatalf("report stream closed before %s", kind)
		}
		if report.Kind != kind {
			t.Fatalf("got report %#v, want %s", report, kind)
		}
		return report
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", kind)
		return ptyReport{}
	}
}

func (s *ptySession) wait(t *testing.T, wantFailure bool) {
	t.Helper()
	select {
	case err := <-s.exited:
		if (err != nil) != wantFailure {
			t.Fatalf("exit = %v, stderr: %s", err, &s.stderr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit")
	}
	current, err := unix.IoctlGetTermios(int(s.slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(current, s.original) {
		t.Fatalf("tty mode not restored: got %#v, want %#v", current, s.original)
	}
	// Flush the master's queued output without a timing assumption.
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		output := string(s.output)
		s.mu.Unlock()
		if strings.Contains(output, leaveScreen) {
			if !strings.Contains(output, enterScreen) {
				t.Fatalf("missing alternate-screen entry in %q", output)
			}
			return
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-deadline.C:
			t.Fatal("missing screen restoration")
		}
	}
}

func TestPTYLifecycleAndResize(t *testing.T) {
	s := startPTY(t, unix.Winsize{Col: 100, Row: 30, Xpixel: 900, Ypixel: 630}, false, false)
	want := Size{Columns: 100, Rows: 30, CellWidthPixels: 9, CellHeightPixels: 21}
	if got := s.next(t, "opened").Size; got != want {
		t.Fatalf("size = %#v, want %#v", got, want)
	}
	raw, err := unix.IoctlGetTermios(int(s.slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Lflag&(unix.ICANON|unix.ECHO) != 0 {
		t.Fatal("terminal did not enter raw mode")
	}
	ws := unix.Winsize{Col: 80, Row: 20, Xpixel: 880, Ypixel: 460}
	if err := unix.IoctlSetWinsize(int(s.slave.Fd()), unix.TIOCSWINSZ, &ws); err != nil {
		t.Fatal(err)
	}
	if err := s.cmd.Process.Signal(syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if got := s.next(t, "resize").Size; got != (Size{Columns: 80, Rows: 20, CellWidthPixels: 11, CellHeightPixels: 23}) {
		t.Fatalf("resized size = %#v", got)
	}
	_, _ = io.WriteString(s.master, "\x1b")
	if key := s.next(t, "key").Key; key.Special != keys.Esc {
		t.Fatalf("escape timeout key = %#v", key)
	}
	_, _ = io.WriteString(s.master, "\x1b_Gi=66051;OK\x1b\\")
	if got := s.next(t, "graphics").Graphics; got != (GraphicsReplyEvent{ID: 66051, OK: true, Message: "OK"}) {
		t.Fatalf("graphics reply = %#v", got)
	}
	_, _ = io.WriteString(s.master, "q")
	s.next(t, "key")
	s.next(t, "closed")
	s.wait(t, false)
}

func TestPTYCellSizeQueries(t *testing.T) {
	for _, tmux := range []bool{false, true} {
		for _, answer := range []bool{false, true} {
			t.Run(fmt.Sprintf("tmux=%t/answer=%t", tmux, answer), func(t *testing.T) {
				s := startPTY(t, unix.Winsize{Col: 90, Row: 25}, tmux, answer)
				want := Size{Columns: 90, Rows: 25, CellWidthPixels: 9, CellHeightPixels: 21}
				if answer {
					want.CellWidthPixels, want.CellHeightPixels = 11, 23
				}
				if got := s.next(t, "opened").Size; got != want {
					t.Fatalf("size = %#v, want %#v", got, want)
				}
				s.mu.Lock()
				output := string(s.output)
				s.mu.Unlock()
				query := "\x1b[16t"
				if tmux {
					query = "\x1bPtmux;\x1b\x1b[16t\x1b\\"
				}
				if !strings.Contains(output, query) {
					t.Fatalf("missing query %q in %q", query, output)
				}
				_, _ = io.WriteString(s.master, "q")
				s.next(t, "key") // Any leaked reply would precede this key.
				s.next(t, "closed")
				s.wait(t, false)
			})
		}
	}
}

func TestPTYRestoresOnPanicAndSignals(t *testing.T) {
	for _, cause := range []string{"panic", "SIGTERM", "SIGINT", "SIGHUP"} {
		t.Run(cause, func(t *testing.T) {
			s := startPTY(t, unix.Winsize{Col: 100, Row: 30, Xpixel: 900, Ypixel: 630}, false, false)
			s.next(t, "opened")
			if cause == "panic" {
				_, _ = io.WriteString(s.master, "p")
				s.next(t, "key")
			} else {
				sig := map[string]os.Signal{"SIGTERM": syscall.SIGTERM, "SIGINT": syscall.SIGINT, "SIGHUP": syscall.SIGHUP}[cause]
				if err := s.cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				s.next(t, "closed")
			}
			s.wait(t, cause == "panic")
			if cause != "panic" {
				s.mu.Lock()
				output := string(s.output)
				s.mu.Unlock()
				if !strings.Contains(output, "signal-cleanup"+leaveScreen) {
					t.Fatalf("writer unavailable before signal restoration: %q", output)
				}
			}
		})
	}
}

func TestPTYSignalFallback(t *testing.T) {
	s := startPTY(t, unix.Winsize{Col: 100, Row: 30, Xpixel: 900, Ypixel: 630}, false, false)
	s.next(t, "opened")
	_, _ = io.WriteString(s.master, "i")
	s.next(t, "key")
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	s.next(t, "closed")
	s.wait(t, false)
}
