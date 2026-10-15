package main

import (
	"image"
	"io"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"github.com/schwagner/mtx/internal/config"
	"github.com/schwagner/mtx/internal/debuglog"
	imgpkg "github.com/schwagner/mtx/internal/image"
	"github.com/schwagner/mtx/internal/matrix"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/imgrender"
)

// imageCacheDir holds downloaded images, decrypted ones included, so
// logout removes it with the rest of the local data.
func imageCacheDir() string {
	return filepath.Join(xdgCache(), "images")
}

// inlineImages is the wired inline-image pipeline.
type inlineImages struct {
	// output is the program's terminal writer: sixel frames are painted
	// after Bubble Tea's text diff, and kitty uploads are serialized
	// with it.
	output io.Writer
	// context builds the TUI's image context; send delivers fetch
	// completions to the program.
	context func(send func(tea.Msg)) imgrender.ImageContext
	// fetcher downloads Matrix media through the engine; nil when
	// images are off.
	fetcher *imgpkg.Fetcher
	proto   imgpkg.Protocol
}

// setupImages detects the terminal's image protocol and wires inline
// images into app, fetching Matrix media through b. It probes the
// terminal, so it must run before Bubble Tea takes over stdin. On any
// failure images stay off and the TUI shows link lines.
func setupImages(cfg config.Config, app *ui.App, b *matrix.Backend) inlineImages {
	sixelFrames := imgpkg.NewSixelFrameStore()
	output := imgpkg.NewFrameOutput(os.Stdout, sixelFrames)
	imgpkg.KittyOutput = output.SideChannel()
	app.SetSixelFrameStore(sixelFrames)
	off := inlineImages{output: output, context: func(func(tea.Msg)) imgrender.ImageContext {
		return imgrender.ImageContext{Protocol: imgpkg.ProtoOff}
	}}

	proto := detectImageProtocol(cfg.Appearance.ImageProtocol)
	app.SetImageProtocol(proto)
	if proto == imgpkg.ProtoOff {
		return off
	}
	cache, err := imgpkg.NewCache(imageCacheDir(), cfg.Cache.MaxImageCacheMB)
	if err != nil {
		debuglog.ImgRender("image cache: %v; images off", err)
		app.SetImageProtocol(imgpkg.ProtoOff)
		return off
	}
	fetcher := imgpkg.NewFetcher(cache, nil)
	fetcher.SetDownloader(b.FetchImage)
	fetcher.ConfigurePrerender(proto)
	if proto == imgpkg.ProtoKitty {
		fetcher.ConfigurePrerenderKitty(imgpkg.KittyRendererInstance())
	}
	app.SetImageFetcher(fetcher)

	// Sixel sizes its raster from the cell's pixel size; kitty sends
	// enough pixels for it.
	pxW, pxH := imgpkg.CellPixels(int(os.Stdout.Fd()))
	debuglog.ImgRender("cell pixels: %dx%d", pxW, pxH)
	imgpkg.SetCellPixels(pxW, pxH)

	ctx := func(send func(tea.Msg)) imgrender.ImageContext {
		return imgrender.ImageContext{
			Protocol:    proto,
			Fetcher:     fetcher,
			KittyRender: imgpkg.KittyRendererInstance(),
			CellPixels:  image.Pt(pxW, pxH),
			MaxRows:     cfg.Appearance.MaxImageRows,
			MaxCols:     cfg.Appearance.MaxImageCols,
			SendMsg:     send,
		}
	}
	app.SetImageContext(ctx(nil))
	return inlineImages{output: output, context: ctx, fetcher: fetcher, proto: proto}
}

// detectImageProtocol picks the protocol from the config and the
// environment, then confirms it with the terminal: a kitty claim that
// gets no reply drops to half-block, and in auto mode a half-block
// guess is upgraded to sixel when the terminal says it has it. This is
// slk's startup sequence.
func detectImageProtocol(cfg string) imgpkg.Protocol {
	proto := imgpkg.Detect(imgpkg.CaptureEnv(), cfg)
	debuglog.ImgRender("image protocol detect: cfg=%q result=%s", cfg, proto)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return proto
	}
	if proto == imgpkg.ProtoKitty && !probe(imgpkg.ProbeKittyGraphics) {
		debuglog.ImgRender("kitty probe failed, downgrading to halfblock")
		proto = imgpkg.ProtoHalfBlock
	}
	// Never inside a multiplexer: tmux keeps sixel off by policy and
	// zellij has none, so the probe would only cost its timeout.
	if proto == imgpkg.ProtoHalfBlock && imgpkg.IsAutoProtocol(cfg) &&
		os.Getenv("TMUX") == "" && os.Getenv("ZELLIJ") == "" &&
		probe(imgpkg.ProbeSixel) {
		debuglog.ImgRender("sixel probe succeeded, upgrading halfblock to sixel")
		proto = imgpkg.ProtoSixel
	}
	debuglog.ImgRender("image protocol: %s", proto)
	return proto
}

// probe runs a terminal query with stdin in raw mode.
func probe(query func(io.Writer, io.Reader, time.Duration) bool) bool {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		debuglog.ImgRender("probe skipped: cannot enter raw mode: %v", err)
		return false
	}
	defer func() {
		if err := term.Restore(fd, state); err != nil {
			debuglog.ImgRender("term restore after probe: %v", err)
		}
	}()
	return query(os.Stdout, os.Stdin, 200*time.Millisecond)
}
