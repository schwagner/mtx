package demo

import (
	"bytes"
	_ "embed"
	"image"
	"image/jpeg"

	"github.com/schwagner/mtx/internal/core"
)

// memeJPEG is KC Green's "This is fine" (see assets/NOTICE.md). It is
// embedded because demo code may not read from disk.
//
//go:embed assets/this-is-fine.jpg
var memeJPEG []byte

const (
	memeFileID = "FDEMOFINE"
	memeW      = 280
	memeH      = 133
)

// memeAttachment is the image Priya posts in the hero scenario.
func memeAttachment() core.Attachment {
	return core.Attachment{
		Kind:   "image",
		Name:   "this-is-fine.jpg",
		URL:    "https://kcgreendotcom.com",
		FileID: memeFileID,
		Mime:   "image/jpeg",
		Thumbs: []core.ThumbSpec{{URL: "demo://this-is-fine.jpg", W: memeW, H: memeH}},
	}
}

func decodeMeme() (image.Image, error) {
	return jpeg.Decode(bytes.NewReader(memeJPEG))
}
