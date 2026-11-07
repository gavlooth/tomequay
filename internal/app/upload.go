package app

import (
	"bytes"
	"fmt"
	"time"

	"terminal-pdf-reader/internal/cache"
	"terminal-pdf-reader/internal/terminal"
)

const uploadTimeout = 2 * time.Second
const maxUploadAttempts = 3 // initial transmission and at most two resends

type pendingUpload struct {
	key           cache.Key
	id            uint32
	png           []byte
	columns, rows int
	attempts      int
	deadline      time.Time
	lastError     string
}

// Only one image transmission may await acknowledgment. tmux can discard raw
// passthrough under output backpressure while still rebuilding ordinary text.
// An acknowledged upload, not a successful pipe write, makes cells safe to draw.
func (m *model) ensureUpload() {
	if len(m.currentPNG) == 0 || m.pendingUpload != nil {
		return
	}
	r := m.image(m.currentKey, m.cache.Contains(m.currentKey))
	if r.sent || r.failed {
		return
	}
	l := m.layout
	if err := m.images.SendPNG(r.id, bytes.NewReader(m.currentPNG), l.Columns, l.Rows); err != nil {
		m.noteOutput(err)
		return
	}
	m.pendingUpload = &pendingUpload{key: m.currentKey, id: r.id, png: m.currentPNG, columns: l.Columns, rows: l.Rows, attempts: 1, deadline: time.Now().Add(uploadTimeout)}
}

func (m *model) graphicsReply(reply terminal.GraphicsReplyEvent) bool {
	u := m.pendingUpload
	if u == nil || u.id != reply.ID {
		return false
	}
	r := m.imageIDs[u.key]
	if reply.OK {
		if r != nil && r.id == u.id {
			r.sent = true
			r.failed = false
			r.columns, r.rows = u.columns, u.rows
		}
		m.pendingUpload = nil
	} else {
		u.lastError = cleanText(reply.Message)
		u.deadline = time.Now()
	}
	return true
}

func (m *model) uploadExpired(now time.Time) {
	u := m.pendingUpload
	if u == nil || now.Before(u.deadline) {
		return
	}
	r := m.imageIDs[u.key]
	if r == nil || r.id != u.id || len(m.currentPNG) == 0 || m.currentKey != u.key || m.help || m.picker != nil || m.passwordFor != nil {
		// An obsolete transfer is never resent. Clear any incomplete terminal
		// data before this cache entry can be transmitted again.
		if r != nil && r.id == u.id {
			m.noteOutput(m.images.Delete(u.id))
		}
		m.pendingUpload = nil
		return
	}
	if u.attempts >= maxUploadAttempts {
		r.failed = true
		m.message = "image upload was not acknowledged after 3 attempts; Ctrl-l to redraw"
		if u.lastError != "" {
			m.message = fmt.Sprintf("image upload failed: %s; Ctrl-l to redraw", u.lastError)
		}
		m.noteOutput(m.images.Delete(u.id))
		m.pendingUpload = nil
		return
	}
	if err := m.images.SendPNG(u.id, bytes.NewReader(u.png), u.columns, u.rows); err != nil {
		m.noteOutput(err)
		return
	}
	u.attempts++
	u.deadline = time.Now().Add(uploadTimeout)
}

func (m *model) forceUpload() {
	if len(m.currentPNG) == 0 {
		return
	}
	m.deleteImage(m.currentKey)
	m.message = "redrawing page"
}
