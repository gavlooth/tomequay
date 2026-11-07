// Package cache stores rendered PNG pages in a shared, byte-limited LRU cache.
// All Cache methods are safe for concurrent use.
package cache

import "sync"

// Key distinguishes a document version, page, render size, and crop. Path must
// be canonical so different spellings of one PDF share rendered pages.
type Key struct {
	Path            string
	ModTimeUnixNano int64
	Page            int
	WidthPixels     int
	HeightPixels    int
	ZoomMilli       int
	// Crop coordinates and size are pixels of the zoomed page. All zeros mean
	// the whole page.
	CropX      int
	CropY      int
	CropWidth  int
	CropHeight int
}

type entry struct {
	key   Key
	png   []byte
	older *entry
	newer *entry
}

// Cache counts PNG payload bytes, not map or entry overhead. The caller must not
// mutate a slice after Put, or mutate a slice returned by Get: both refer to the
// same stored bytes. A returned slice stays valid after eviction. The zero value
// is a disabled cache; use New to choose a positive byte limit.
type Cache struct {
	mu       sync.Mutex
	maxBytes int64
	bytes    int64
	entries  map[Key]*entry
	oldest   *entry
	newest   *entry
}

// New creates a cache. A nonpositive maximum disables storage.
func New(maxBytes int64) *Cache {
	return &Cache{maxBytes: maxBytes, entries: make(map[Key]*entry)}
}

// Get returns the stored slice without copying and refreshes its recency.
func (c *Cache) Get(k Key) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[k]
	if e == nil {
		return nil, false
	}
	c.makeNewest(e)
	return e.png, true
}

// Contains reports whether k is stored without changing its recency.
func (c *Cache) Contains(k Key) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[k]
	return ok
}

// Put stores png without copying. An item larger than the byte limit is ignored
// without removing any existing entry, including an older value for the same
// key. Otherwise the least recently used entries are removed until it fits.
func (c *Cache) Put(k Key, png []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size := int64(len(png))
	if c.maxBytes <= 0 || size > c.maxBytes {
		return
	}
	e := c.entries[k]
	if e != nil {
		c.bytes -= int64(len(e.png))
		c.makeNewest(e)
	}
	// Subtract before comparing so the addition cannot overflow int64.
	for c.bytes > c.maxBytes-size {
		c.remove(c.oldest)
	}
	if e == nil {
		e = &entry{key: k}
		c.entries[k] = e
		c.appendNewest(e)
	}
	e.png = png
	c.bytes += size
}

// DropPath removes every rendered size, page, and version of path.
func (c *Cache) DropPath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if k.Path == path {
			c.remove(e)
		}
	}
}

// Bytes returns the total number of stored PNG payload bytes.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Len returns the number of stored renders.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *Cache) makeNewest(e *entry) {
	if c.newest == e {
		return
	}
	c.unlink(e)
	c.appendNewest(e)
}

func (c *Cache) appendNewest(e *entry) {
	e.older = c.newest
	e.newer = nil
	if c.newest != nil {
		c.newest.newer = e
	} else {
		c.oldest = e
	}
	c.newest = e
}

func (c *Cache) unlink(e *entry) {
	if e.older != nil {
		e.older.newer = e.newer
	} else {
		c.oldest = e.newer
	}
	if e.newer != nil {
		e.newer.older = e.older
	} else {
		c.newest = e.older
	}
}

func (c *Cache) remove(e *entry) {
	c.unlink(e)
	delete(c.entries, e.key)
	c.bytes -= int64(len(e.png))
}
