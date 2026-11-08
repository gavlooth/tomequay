package app

import (
	"context"
	"errors"
	"time"

	"tomequay/internal/imageout"
	"tomequay/internal/pdf"
	"tomequay/internal/terminal"
)

// Run opens the controlling terminal and runs until the last tab is closed or
// ctx is canceled. All model mutations and terminal writes happen in this loop.
func Run(ctx context.Context, opts Options) (err error) {
	if opts.SelectionMode != "" && opts.SelectionMode != "text" && opts.SelectionMode != "page" {
		return errors.New("selection mode must be text or page")
	}
	t, err := terminal.Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, t.Close()) }()
	tools, toolErr := pdf.FindTools()
	m := newModel(t, imageout.New(t.Writer(), terminal.InTmux()), tools, toolErr, opts)
	m.ctx = ctx
	defer m.closeFuzzy()
	defer func() {
		m.saveHistory(true)
		m.stopWorkers()
		// Delete only this reader's IDs, before the terminal leaves alternate screen.
		for k := range m.imageIDs {
			m.deleteImage(k)
		}
		_, writeErr := t.Writer().Write([]byte("\x1b[0m\x1b[2J\x1b[H"))
		err = errors.Join(err, m.outputErr, writeErr, t.Flush())
	}()
	for _, path := range opts.Paths {
		m.open(path, opts.Page, "")
	}
	if _, ok := m.tabs.Active(); !ok && m.passwordFor == nil {
		m.showPicker()
	}
	if toolErr != nil {
		m.message = toolErr.Error()
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	uploadTimer := time.NewTimer(time.Hour)
	uploadTimer.Stop()
	defer uploadTimer.Stop()
	for !m.quit {
		m.prepareRender()
		m.prepareSelectionImage()
		if err := m.draw(); err != nil {
			return err
		}
		if m.runQueuedFuzzy() {
			continue
		}
		redraw := false
		for !redraw && !m.quit {
			var uploadDeadline <-chan time.Time
			if u := m.pendingUpload; u != nil {
				uploadTimer.Reset(max(0, time.Until(u.deadline)))
				uploadDeadline = uploadTimer.C
			} else {
				uploadTimer.Stop()
			}
			select {
			case <-ctx.Done():
				return nil
			case event, ok := <-t.Events():
				if !ok {
					return nil
				}
				switch event := event.(type) {
				case terminal.KeyEvent:
					m.key(event.Key)
					redraw = true
				case terminal.MouseEvent:
					m.mouse(event)
					redraw = true
				case terminal.ResizeEvent:
					redraw = m.size != event.Size
					m.size = event.Size
					if p := m.textView; p != nil {
						p.reveal(m.size.Columns, m.size.Rows-4)
					}
				case terminal.GraphicsReplyEvent:
					redraw = m.graphicsReply(event)
				case terminal.ErrorEvent:
					var signalErr *terminal.SignalError
					if errors.As(event.Err, &signalErr) {
						return nil
					}
					return event.Err
				}
			case result := <-m.results:
				accepted := m.acceptRender(result)
				redraw = accepted && (result.foreground || len(m.currentPNG) > 0 && m.imageIDs[m.currentKey] == nil)
				if !redraw {
					// A prefetch eviction may queue ID deletions without changing
					// the visible page. Flush that graphics-only frame once.
					m.noteOutput(t.Flush())
					if m.outputErr != nil {
						return m.outputErr
					}
				}
			case result := <-m.searchResults:
				m.acceptSearch(result)
				redraw = true
			case result := <-m.outlineResults:
				m.acceptOutline(result)
				redraw = true
			case result := <-m.textResults:
				m.acceptText(result)
				redraw = true
			case result := <-m.pageWordsResults:
				m.acceptPageWords(result)
				redraw = true
			case result := <-m.selectionImageResults:
				m.acceptSelectionImage(result)
				redraw = true
			case now := <-uploadDeadline:
				m.uploadExpired(now)
				redraw = true
			case <-ticker.C:
				m.saveHistory(false)
				redraw = m.pollFiles()
			}
		}
	}
	return nil
}
