package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/schwagner/mtx/internal/demo"
	imgpkg "github.com/schwagner/mtx/internal/image"
	"github.com/schwagner/mtx/internal/ui"
)

// deliverDemo feeds msg to app, then runs the commands it returns,
// recursively, as Bubble Tea would. A command that does not answer
// within 500ms is a timer (typing expiry, loading timeout) and is
// skipped; ui.SpinnerTickMsg is skipped explicitly (see runDemoCmd)
// rather than by timeout, since it answers within 100ms and would
// otherwise be delivered and re-armed recursively while loading.
func deliverDemo(t *testing.T, app *ui.App, msg tea.Msg, depth int) {
	t.Helper()
	if msg == nil || depth > 12 {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			runDemoCmd(t, app, cmd, depth+1)
		}
		return
	}
	_, cmd := app.Update(msg)
	runDemoCmd(t, app, cmd, depth+1)
}

func runDemoCmd(t *testing.T, app *ui.App, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if _, ok := msg.(ui.SpinnerTickMsg); ok {
			return
		}
		deliverDemo(t, app, msg, depth)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestDemoAppRendersItsFirstFrame(t *testing.T) {
	d, err := demo.New("hero", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	app := newDemoApp(d)
	deliverDemo(t, app, tea.WindowSizeMsg{Width: 120, Height: 40}, 0)
	for _, msg := range d.StartupMsgs() {
		deliverDemo(t, app, msg, 0)
	}
	frame := ansi.Strip(app.View().Content)
	for _, want := range []string{"general", "engineering", "Lunch order goes out"} {
		if !strings.Contains(frame, want) {
			t.Errorf("first demo frame is missing %q:\n%s", want, frame)
		}
	}
}

func TestRunDemoRejectsUnknownScenario(t *testing.T) {
	err := runDemo("nope")
	if err == nil || !strings.Contains(err.Error(), "hero") {
		t.Fatalf("runDemo(nope) = %v, want an error listing the scenarios", err)
	}
}

// Opening a demo image full-screen (`v`) must work too. This drives the
// chart that #design starts with: inline render, then the preview, whose
// fetch key (FileID + "-preview") differs from the inline renderer's and
// must also be served by the demo's in-memory images.
func TestDemoImageOpensFullScreen(t *testing.T) {
	d, err := demo.New("hero", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	app := newDemoApp(d)
	deliverDemo(t, app, tea.WindowSizeMsg{Width: 120, Height: 40}, 0)
	for _, msg := range d.StartupMsgs() {
		deliverDemo(t, app, msg, 0)
	}
	// C0DESIGN is #design in internal/demo's fixtures; its newest message
	// carries the chart, and opening a channel selects the newest message.
	deliverDemo(t, app, ui.ChannelSelectedMsg{ID: "C0DESIGN", Name: "design", Type: "channel"}, 0)
	inline := app.View().Content
	if strings.Count(inline, "▀") < 200 {
		t.Fatalf("#design shows no inline half-block image:\n%s", ansi.Strip(inline))
	}

	deliverDemo(t, app, tea.KeyPressMsg{Code: 'v', Text: "v"}, 0)
	preview := app.View().Content
	if got := strings.Count(preview, "▀"); got <= strings.Count(inline, "▀") {
		t.Fatalf("preview drew %d half-blocks, no more than the inline view's %d:\n%s",
			got, strings.Count(inline, "▀"), ansi.Strip(preview))
	}
}

func TestDemoImageProtocolFromEnv(t *testing.T) {
	for value, want := range map[string]imgpkg.Protocol{
		"":          imgpkg.ProtoHalfBlock,
		"halfblock": imgpkg.ProtoHalfBlock,
		"sixel":     imgpkg.ProtoSixel,
	} {
		got, err := demoImageProtocol(value)
		if err != nil || got != want {
			t.Errorf("demoImageProtocol(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	_, err := demoImageProtocol("kitty")
	if err == nil || !strings.Contains(err.Error(), "halfblock") || !strings.Contains(err.Error(), "sixel") {
		t.Errorf("demoImageProtocol(kitty) err = %v, want one naming the valid values", err)
	}
}

func TestRunDemoRejectsUnknownImageProtocol(t *testing.T) {
	t.Setenv("SLK_DEMO_IMAGES", "kitty")
	if err := runDemo("hero"); err == nil || !strings.Contains(err.Error(), "SLK_DEMO_IMAGES") {
		t.Fatalf("runDemo with SLK_DEMO_IMAGES=kitty = %v, want an error naming the variable", err)
	}
}
