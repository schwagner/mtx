package avatar

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	imgpng "image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	imgpkg "github.com/schwagner/mtx/internal/image"
)

// pathLog records the request paths a test server saw, in order.
type pathLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *pathLog) add(p string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.paths = append(l.paths, p)
}

func (l *pathLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.paths)
}

// pngServer serves a 16x16 PNG at every path in ok and a 404
// everywhere else.
func pngServer(t *testing.T, ok ...string) (*httptest.Server, *pathLog) {
	t.Helper()
	var buf bytes.Buffer
	if err := imgpng.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	png := buf.Bytes()
	log := &pathLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r.URL.Path)
		switch {
		case slices.Contains(ok, r.URL.Path):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

func newImgCache(t *testing.T) *imgpkg.Cache {
	t.Helper()
	ic, err := imgpkg.NewCache(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	return ic
}

// launchCache builds an avatar Cache over imgCache with a fresh
// fetcher, as a new launch would against the same disk cache.
func launchCache(imgCache *imgpkg.Cache) *Cache {
	return NewCache(imgpkg.NewFetcher(imgCache, http.DefaultClient), nil, false)
}

func TestFetchKey(t *testing.T) {
	const u = "mxc://example.org/abcdef"
	sum := sha256.Sum256([]byte(u))
	if got, want := fetchKey("U1", u), "avatar-U1-"+hex.EncodeToString(sum[:])[:12]; got != want {
		t.Errorf("fetchKey = %q; want %q", got, want)
	}
	if fetchKey("U1", u) == fetchKey("U1", u+"x") {
		t.Error("different URLs for one user share a key")
	}
	if fetchKey("U1", u) == fetchKey("U2", u) {
		t.Error("different users with one URL share a key")
	}
}

// A user's avatar URL changes between launches: the new URL must be
// fetched rather than the old file served from the shared disk cache,
// and a repeat of the same URL must still be a disk hit.
func TestPreload_NewURLForSameUserIsFetchedFresh(t *testing.T) {
	srv, log := pngServer(t, "/old.png", "/new.png")
	imgCache := newImgCache(t)

	launchCache(imgCache).PreloadSync("U1", srv.URL+"/old.png")
	launchCache(imgCache).PreloadSync("U1", srv.URL+"/new.png")
	third := launchCache(imgCache)
	third.PreloadSync("U1", srv.URL+"/new.png")

	if got, want := log.get(), []string{"/old.png", "/new.png"}; !slices.Equal(got, want) {
		t.Errorf("requested %v; want %v", got, want)
	}
	if third.Get("U1") == "" {
		t.Error("avatar not rendered from the disk cache")
	}
}
