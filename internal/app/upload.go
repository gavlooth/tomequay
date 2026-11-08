package app

import (
	"bytes"
	"fmt"
	"time"

	"tomequay/internal/cache"
	"tomequay/internal/terminal"
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
	replacement   *imageRecord
	previousID    uint32
}

// Only one image transmission may await acknowledgment. tmux can discard raw
// passthrough under output backpressure while still rebuilding ordinary text.
// An acknowledged upload, not a successful pipe write, makes cells safe to draw.
func (m *model) ensureUpload() {
	if len(m.currentPNG) == 0 || m.pendingUpload != nil {
		return
	}
	data, version := m.displayedPNG()
	r := m.image(m.currentKey, m.cache.Contains(m.currentKey))
	if version == 0 && r.original != nil {
		m.noteOutput(m.images.Delete(r.id))
		r = r.original
		m.imageIDs[m.currentKey] = r
	}
	if r.failed {
		return
	}
	if version != 0 && r.visualVersion == 0 && !r.sent {
		data, version = m.currentPNG, 0
	}
	var replacement *imageRecord
	var previousID uint32
	if r.visualVersion != version {
		if r.sent {
			previousID = r.id
			replacement = m.newImageRecord(r.cached)
			replacement.visualVersion = version
			r = replacement
		} else {
			m.deleteImage(m.currentKey)
			r = m.image(m.currentKey, m.cache.Contains(m.currentKey))
			r.visualVersion = version
		}
	}
	if r.sent || r.failed {
		return
	}
	l := m.layout
	if err := m.images.SendPNG(r.id, bytes.NewReader(data), l.Columns, l.Rows); err != nil {
		if replacement != nil {
			m.noteOutput(m.images.Delete(r.id))
		}
		m.noteOutput(err)
		return
	}
	m.pendingUpload = &pendingUpload{key: m.currentKey, id: r.id, png: data, columns: l.Columns, rows: l.Rows, attempts: 1,
		deadline: time.Now().Add(uploadTimeout), replacement: replacement, previousID: previousID}
}

func (m *model) graphicsReply(reply terminal.GraphicsReplyEvent) bool {
	u := m.pendingUpload
	if u == nil || u.id != reply.ID {
		return false
	}
	r := m.imageIDs[u.key]
	if u.replacement != nil {
		_, version := m.displayedPNG()
		if m.currentKey != u.key || r == nil || r.id != u.previousID || version != u.replacement.visualVersion {
			m.discardPendingReplacement()
			return true
		}
		if reply.OK {
			if r.visualVersion == 0 {
				u.replacement.original = r
			} else {
				u.replacement.original = r.original
				m.noteOutput(m.images.Delete(r.id))
			}
			r = u.replacement
			m.imageIDs[u.key] = r
		}
	}
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
	if u.replacement != nil {
		_, version := m.displayedPNG()
		if version != u.replacement.visualVersion {
			m.discardPendingReplacement()
			return
		}
	}
	r := m.imageIDs[u.key]
	if r == nil || u.replacement == nil && r.id != u.id || u.replacement != nil && r.id != u.previousID || len(m.currentPNG) == 0 || m.currentKey != u.key || m.help || m.picker != nil || m.passwordFor != nil {
		// An obsolete transfer is never resent. Clear any incomplete terminal
		// data before this cache entry can be transmitted again.
		if u.replacement != nil {
			m.noteOutput(m.images.Delete(u.id))
		} else if r != nil && r.id == u.id {
			m.noteOutput(m.images.Delete(u.id))
		}
		m.pendingUpload = nil
		return
	}
	if u.attempts >= maxUploadAttempts {
		if u.replacement != nil {
			r.failed = true
			m.noteOutput(m.images.Delete(u.id))
			m.pendingUpload = nil
			m.message = "Selection image upload was not acknowledged; Ctrl-l to redraw"
			return
		}
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

func (m *model) discardPendingReplacement() {
	if u := m.pendingUpload; u != nil && u.replacement != nil {
		m.noteOutput(m.images.Delete(u.id))
		m.pendingUpload = nil
	}
}
