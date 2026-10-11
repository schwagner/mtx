// Package demo is slk's recording mode: two made-up workspaces served
// through the same internal/core ports the real app wires, plus a
// director that plays scripted live activity. It exists to record the
// README GIFs (docs/superpowers/specs/2026-09-27-demo-mode-design.md)
// and must never reach Slack, the cache, the config or the network;
// boundary_test.go enforces that.
package demo

import (
	"errors"
	"image"
	"image/color"
	"image/draw"

	"github.com/schwagner/mtx/internal/core"
)

// errUnavailable answers every operation the demo does not fake. The App
// shows it through its normal failure toast.
var errUnavailable = errors.New("not available in demo mode")

const (
	chartFileID = "FDEMOCHART"
	chartW      = 640
	chartH      = 320
)

// chartAttachment is the one inline image in the demo, posted in #design.
func chartAttachment() core.Attachment {
	return core.Attachment{
		Kind:   "image",
		Name:   "signups-by-week.png",
		URL:    "https://example.com/lumen/signups-by-week.png",
		FileID: chartFileID,
		Mime:   "image/png",
		Thumbs: []core.ThumbSpec{{URL: "demo://signups-by-week.png", W: chartW, H: chartH}},
	}
}

// drawChart draws eight weeks of rising signups, the latest week
// highlighted.
func drawChart() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, chartW, chartH))
	fill(img, img.Bounds(), color.RGBA{0x1a, 0x1b, 0x26, 0xff})
	weeks := []int{30, 36, 34, 48, 57, 55, 71, 88} // percent of the plot height
	const base = chartH - 24
	slot := chartW / (2*len(weeks) + 1)
	for i, pct := range weeks {
		x := slot * (2*i + 1)
		top := base - pct*(base-24)/100
		c := color.RGBA{0x7a, 0xa2, 0xf7, 0xff}
		if i == len(weeks)-1 {
			c = color.RGBA{0x9e, 0xce, 0x6a, 0xff}
		}
		fill(img, image.Rect(x, top, x+slot, base), c)
	}
	fill(img, image.Rect(slot/2, base, chartW-slot/2, base+3), color.RGBA{0xa9, 0xb1, 0xd6, 0xff})
	return img
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}
