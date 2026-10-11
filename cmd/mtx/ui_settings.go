package main

import (
	"time"

	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/styles"
	versionpkg "github.com/schwagner/mtx/internal/version"
)

// uiSettings is the display configuration run() and runDemo() both hand
// the App. It carries values rather than the config file so that demo
// mode can build one without loading any config.
type uiSettings struct {
	TimestampFormat  string
	TypingIndicators bool
	StaleAfter       time.Duration
	MouseWheelLines  int
	ColoredUsernames bool
	ThemeOverrides   core.Theme
}

// applyUISettings installs s on app. The timestamp formatter feeds the
// optimistic instant-display path on send: the App mints a placeholder
// before the chat.postMessage round-trip and needs a Timestamp string
// that renders identically to messages arriving through the load path.
func applyUISettings(app *ui.App, s uiSettings) {
	app.SetHelpFooter(versionpkg.ModalFooter(version))
	app.SetNowTimestampFormatter(func() string {
		return time.Now().Format(s.TimestampFormat)
	})
	app.SetTypingEnabled(s.TypingIndicators)
	app.SetSidebarStaleThreshold(s.StaleAfter)
	app.SetMouseWheelLines(s.MouseWheelLines)
	app.SetColoredUsernames(s.ColoredUsernames)
	app.SetThemeItems(styles.ThemeNames())
	app.SetThemeOverrides(s.ThemeOverrides)
}
