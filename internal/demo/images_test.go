package demo

import (
	"context"
	"errors"
	"image"
	"testing"

	imgpkg "github.com/schwagner/mtx/internal/image"
)

func testImageStore(t *testing.T) imageStore {
	t.Helper()
	s, err := newImageStore()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestImageStoreServesEveryDemoImage(t *testing.T) {
	s := testImageStore(t)
	for _, id := range []string{chartFileID, memeFileID} {
		// The inline renderer keys by FileID-<thumb size>; the full-screen
		// preview by FileID-preview.
		for _, key := range []string{id + "-640", id + "-preview"} {
			if img, ok := s.Cached(key, image.Pt(320, 160)); !ok || img == nil {
				t.Errorf("Cached(%q) missed", key)
			}
			res, err := s.Fetch(context.Background(), imgpkg.FetchRequest{Key: key})
			if err != nil || res.Img == nil || res.Mime == "" {
				t.Errorf("Fetch(%q) = %+v, %v", key, res, err)
			}
		}
	}
	if img, _ := s.Cached(memeFileID+"-280", image.Pt(1, 1)); img.Bounds().Dx() != memeW {
		t.Errorf("meme key served an image %d wide, want the meme (%d)", img.Bounds().Dx(), memeW)
	}
}

func TestImageStoreMissesEverythingElse(t *testing.T) {
	s := testImageStore(t)
	for _, key := range []string{"F0OTHER-360", chartFileID + "X-640", ""} {
		if _, ok := s.Cached(key, image.Pt(320, 160)); ok {
			t.Errorf("Cached(%q) hit", key)
		}
		if _, err := s.Fetch(context.Background(), imgpkg.FetchRequest{Key: key}); !errors.Is(err, errUnavailable) {
			t.Errorf("Fetch(%q) err = %v, want errUnavailable", key, err)
		}
	}
	if _, ok := s.Prerendered(chartFileID+"-640", image.Pt(40, 10), imgpkg.ProtoHalfBlock); ok {
		t.Fatal("Prerendered must miss so the renderer encodes half-blocks itself")
	}
}
