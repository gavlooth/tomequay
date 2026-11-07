package cache_test

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"tomequay/internal/cache"
)

func key(page int) cache.Key {
	return cache.Key{Path: "/book.pdf", Page: page, WidthPixels: 800, HeightPixels: 1000, ZoomMilli: 1000}
}

func assertSize(t *testing.T, c *cache.Cache, count int, size int64) {
	t.Helper()
	if gotCount, gotSize := c.Len(), c.Bytes(); gotCount != count || gotSize != size {
		t.Fatalf("cache size = %d entries, %d bytes; want %d entries, %d bytes", gotCount, gotSize, count, size)
	}
}

func TestGetRefreshesEvictionOrder(t *testing.T) {
	c := cache.New(6)
	c.Put(key(1), []byte("one"))
	c.Put(key(2), []byte("two"))
	if got, ok := c.Get(key(1)); !ok || string(got) != "one" {
		t.Fatalf("Get = %q, %v", got, ok)
	}
	c.Put(key(3), []byte("tri"))
	if _, ok := c.Get(key(2)); ok {
		t.Fatal("Get did not refresh page 1; page 2 should have been evicted")
	}
	if _, ok := c.Get(key(1)); !ok {
		t.Fatal("recently read page 1 was evicted")
	}
	assertSize(t, c, 2, 6)
	// A larger render must remove multiple entries, not just one.
	c.Put(key(4), []byte("large!"))
	assertSize(t, c, 1, 6)
	if got, ok := c.Get(key(4)); !ok || string(got) != "large!" {
		t.Fatalf("large entry = %q, %v", got, ok)
	}
}

func TestContainsDoesNotRefreshRecency(t *testing.T) {
	c := cache.New(6)
	if c.Contains(key(1)) {
		t.Fatal("empty cache contains page 1")
	}
	c.Put(key(1), []byte("one"))
	c.Put(key(2), []byte("two"))
	if !c.Contains(key(1)) {
		t.Fatal("stored page 1 is missing")
	}
	c.Put(key(3), []byte("tri"))
	if c.Contains(key(1)) || !c.Contains(key(2)) || !c.Contains(key(3)) {
		t.Fatal("Contains changed eviction order")
	}
	c.DropPath("/book.pdf")
	if c.Contains(key(2)) || c.Contains(key(3)) {
		t.Fatal("dropped pages are still present")
	}
}

func TestOversizePutChangesNothing(t *testing.T) {
	c := cache.New(6)
	c.Put(key(1), []byte("one"))
	c.Put(key(2), []byte("two"))
	c.Put(key(3), []byte("too big"))
	c.Put(key(1), []byte("also too big"))
	assertSize(t, c, 2, 6)
	if _, ok := c.Get(key(3)); ok {
		t.Fatal("oversize item was stored")
	}
	// An ignored replacement must not refresh the original key's recency.
	c.Put(key(4), []byte("new"))
	if _, ok := c.Get(key(1)); ok {
		t.Fatal("oversize replacement changed LRU order")
	}
	if _, ok := c.Get(key(2)); !ok {
		t.Fatal("oversize insertion removed an existing page")
	}
	assertSize(t, c, 2, 6)

	c = cache.New(6)
	c.Put(key(1), []byte("one"))
	c.Put(key(1), []byte("too big"))
	if got, ok := c.Get(key(1)); !ok || string(got) != "one" {
		t.Fatalf("oversize replacement removed original: %q, %v", got, ok)
	}
}

func TestReplacementCountsBytesAndRefreshesRecency(t *testing.T) {
	c := cache.New(10)
	c.Put(key(1), []byte("1234"))
	c.Put(key(2), []byte("5678"))
	c.Put(key(1), []byte("x"))
	assertSize(t, c, 2, 5)
	c.Put(key(3), []byte("abcdef"))
	assertSize(t, c, 2, 7)
	if _, ok := c.Get(key(2)); ok {
		t.Fatal("replacement did not refresh page 1's recency")
	}
	c.Put(key(1), []byte("123456789"))
	assertSize(t, c, 1, 9)
	if got, ok := c.Get(key(1)); !ok || string(got) != "123456789" {
		t.Fatalf("larger replacement = %q, %v", got, ok)
	}
	c.Put(key(1), nil)
	assertSize(t, c, 1, 0)
	if got, ok := c.Get(key(1)); !ok || got != nil {
		t.Fatalf("empty replacement = %v, %v", got, ok)
	}
}

func TestDropPathRemovesEveryRenderAndVersion(t *testing.T) {
	c := cache.New(100)
	for i := range 4 {
		k := key(i + 1)
		k.ModTimeUnixNano = int64(i)
		k.WidthPixels += i
		k.HeightPixels += i
		k.ZoomMilli += i
		c.Put(k, []byte("abc"))
	}
	other := key(1)
	other.Path = "/other.pdf"
	c.Put(other, []byte("other"))
	c.DropPath("/absent.pdf")
	assertSize(t, c, 5, 17)
	c.DropPath("/book.pdf")
	assertSize(t, c, 1, 5)
	if got, ok := c.Get(other); !ok || string(got) != "other" {
		t.Fatalf("DropPath removed unrelated path: %q, %v", got, ok)
	}
	c.DropPath(other.Path)
	assertSize(t, c, 0, 0)
	// The list must remain usable after its last entry is removed.
	c.Put(key(1), []byte("new"))
	assertSize(t, c, 1, 3)
}

func TestKeySeparatesRenderSizesAndDocumentVersions(t *testing.T) {
	c := cache.New(100)
	base := key(1)
	variants := []cache.Key{base, base, base, base, base, base, base, base, base, base, base}
	variants[1].Path = "/other.pdf"
	variants[2].ModTimeUnixNano = 1
	variants[3].Page = 2
	variants[4].WidthPixels++
	variants[5].HeightPixels++
	variants[6].ZoomMilli++
	variants[7].CropX++
	variants[8].CropY++
	variants[9].CropWidth++
	variants[10].CropHeight++
	for i, k := range variants {
		c.Put(k, []byte{byte(i)})
	}
	for i, k := range variants {
		if got, ok := c.Get(k); !ok || len(got) != 1 || got[0] != byte(i) {
			t.Fatalf("render %d collided: %v, %v", i, got, ok)
		}
	}
	assertSize(t, c, len(variants), int64(len(variants)))
}

func TestDisabledCacheAndReturnedSliceLifetime(t *testing.T) {
	for _, c := range []*cache.Cache{cache.New(0), cache.New(-1), {}} {
		c.Put(key(1), []byte("page"))
		c.Put(key(2), nil)
		if _, ok := c.Get(key(1)); ok {
			t.Fatal("disabled cache stored a page")
		}
		c.DropPath("/book.pdf")
		assertSize(t, c, 0, 0)
	}
	c := cache.New(4)
	input := []byte("page")
	c.Put(key(1), input)
	got, ok := c.Get(key(1))
	if !ok || &got[0] != &input[0] {
		t.Fatal("cache did not retain the supplied slice")
	}
	c.Put(key(2), []byte("next"))
	if string(got) != "page" {
		t.Fatalf("eviction changed a previously returned slice: %q", got)
	}
}

func TestConcurrentGetPutAndDrop(t *testing.T) {
	const limit = 256
	c := cache.New(limit)
	payload := []byte("immutable rendered page")
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := range 500 {
				k := key((i + worker) % 20)
				k.Path = fmt.Sprintf("/book-%d.pdf", worker%2)
				c.Put(k, payload)
				if got, ok := c.Get(k); ok && !bytes.Equal(got, payload) {
					t.Errorf("concurrent read = %q", got)
				}
				if i%17 == 0 {
					c.DropPath(k.Path)
				}
				if size := c.Bytes(); size < 0 || size > limit {
					t.Errorf("concurrent byte count = %d", size)
				}
				_ = c.Len()
				_ = c.Contains(k)
			}
		}(worker)
	}
	wg.Wait()
	if c.Bytes() != int64(c.Len()*len(payload)) {
		t.Fatalf("final bytes = %d, entries = %d", c.Bytes(), c.Len())
	}
}

// TestRealPDFSmoke uses PDFREADER_TEST_PDF and the installed pdftoppm program to
// check cache sharing with actual PNG page bytes, rather than synthetic data.
func TestRealPDFSmoke(t *testing.T) {
	path := os.Getenv("PDFREADER_TEST_PDF")
	if path == "" {
		t.Skip("set PDFREADER_TEST_PDF for the real-PDF smoke test")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(t.TempDir(), "page")
	output, err := exec.Command("pdftoppm", "-f", "1", "-l", "1", "-singlefile", "-scale-to", "64", "-png", path, prefix).CombinedOutput()
	if err != nil {
		t.Fatalf("pdftoppm: %v: %s", err, output)
	}
	data, err := os.ReadFile(prefix + ".png")
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	k := cache.Key{Path: path, ModTimeUnixNano: info.ModTime().UnixNano(), Page: 1, WidthPixels: config.Width, HeightPixels: config.Height, ZoomMilli: 1000}
	c := cache.New(int64(len(data)))
	c.Put(k, data)
	got, ok := c.Get(k)
	if !ok || !bytes.Equal(got, data) {
		t.Fatal("real PDF page was not returned intact")
	}
	c.DropPath(path)
	assertSize(t, c, 0, 0)
	t.Logf("cached and dropped real PDF page: %dx%d PNG, %d bytes", config.Width, config.Height, len(data))
}

func TestLightAndDarkPagesHaveSeparateEntries(t *testing.T) {
	c := cache.New(64)
	light, dark := key(1), key(1)
	dark.Dark = true
	c.Put(light, []byte("light"))
	c.Put(dark, []byte("dark"))
	for k, want := range map[cache.Key]string{light: "light", dark: "dark"} {
		if got, ok := c.Get(k); !ok || string(got) != want {
			t.Fatalf("page dark=%v: %q, %v; want %q", k.Dark, got, ok, want)
		}
	}
	c.DropPath(light.Path)
	if c.Contains(light) || c.Contains(dark) {
		t.Fatal("reload retained a color variant")
	}
}
