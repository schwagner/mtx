package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rs/zerolog"
	"golang.design/x/clipboard"

	"github.com/schwagner/mtx/internal/config"
	"github.com/schwagner/mtx/internal/core"
	"github.com/schwagner/mtx/internal/debuglog"
	"github.com/schwagner/mtx/internal/editor"
	emojiwidth "github.com/schwagner/mtx/internal/emoji"
	"github.com/schwagner/mtx/internal/export"
	imgpkg "github.com/schwagner/mtx/internal/image"
	"github.com/schwagner/mtx/internal/matrix"
	"github.com/schwagner/mtx/internal/notify"
	"github.com/schwagner/mtx/internal/ui"
	"github.com/schwagner/mtx/internal/ui/styles"
	"github.com/schwagner/mtx/internal/ui/workspace"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if debugFile, err := debuglog.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "mtx: could not open debug log: %v\n", err)
	} else if debugFile != nil {
		defer debugFile.Close()
	}
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "--version", "-v", "version":
		fmt.Printf("mtx %s (commit %s, built %s)\n", version, commit, date)
		return
	case "--help", "-h", "help":
		printHelp()
		return
	case "logout":
		err = logout()
	case "":
		err = run()
	default:
		err = fmt.Errorf("unknown command %q (see mtx --help)", cmd)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`mtx - a terminal Matrix client

Usage:
  mtx            Start (logs in and verifies this device on first run)
  mtx logout     Log out this device and delete its local data
  mtx --version  Print the version

Files:
  ~/.config/mtx/config.toml   Settings and themes (same format as slk)
  ~/.local/share/mtx/         Account, encryption keys and message cache
  ~/.cache/mtx/mtx.log        Client log
`)
}

// pickleKey returns the key that encrypts the Olm/Megolm secrets in the
// database, creating it on first run.
func pickleKey(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, "pickle.key")
	if key, err := os.ReadFile(path); err == nil && len(key) == 32 {
		return key, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, key, 0o600)
}

func openBackend() (*matrix.Backend, error) {
	dataDir := xdgData()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating data dir: %w", err)
	}
	cacheDir := xdgCache()
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(cacheDir, "mtx.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening log: %w", err)
	}
	level := zerolog.InfoLevel
	if os.Getenv("MTX_DEBUG") != "" {
		level = zerolog.DebugLevel
	}
	logger := zerolog.New(logFile).Level(level).With().Timestamp().Logger()
	// The standard logger (used by the shared TUI packages) must never
	// write to the terminal under the TUI.
	log.SetOutput(logFile)

	key, err := pickleKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("loading pickle key: %w", err)
	}
	return matrix.Open(filepath.Join(dataDir, "mtx.db"), key, logger)
}

func logout() error {
	b, err := openBackend()
	if err != nil {
		return err
	}
	ctx := context.Background()
	userID, err := b.Load(ctx)
	if err != nil {
		return err
	}
	if userID != "" {
		if _, err := b.Client().Client.Logout(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: server logout failed: %v\n", err)
		}
	}
	b.Stop()
	dataDir := xdgData()
	for _, name := range []string{"mtx.db", "mtx.db-wal", "mtx.db-shm", "pickle.key"} {
		_ = os.Remove(filepath.Join(dataDir, name))
	}
	fmt.Println("Logged out and removed local data.")
	return nil
}

func run() error {
	configPath := filepath.Join(xdgConfig(), "config.toml")
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	styles.LoadCustomThemes(os.DirFS(filepath.Join(xdgConfig(), "themes")))
	styles.Apply(cfg.Appearance.Theme, cfg.Theme)
	emojiwidth.SetImageMode(false, cfg.Appearance.EmojiCells)

	b, err := openBackend()
	if err != nil {
		return err
	}
	defer b.Stop()
	b.TimestampFormat = cfg.Appearance.TimestampFormat
	b.DownloadDir = cfg.General.DownloadDir
	if cfg.Notifications.Enabled {
		b.Notifier = notify.New(true, cfg.Notifications.NotifyCommand)
	}

	ctx := context.Background()
	userID, err := b.Load(ctx)
	if err != nil {
		return fmt.Errorf("loading account: %w", err)
	}
	p := newPrompter()
	password := ""
	if userID == "" {
		if password, err = login(ctx, b, p); err != nil {
			return err
		}
	}
	if err := verify(ctx, b, p, password); err != nil {
		return err
	}

	clipboardOK := true
	useWayland := false
	if IsWayland() {
		useWayland = HasWlPaste()
		clipboardOK = useWayland
	} else if err := clipboard.Init(); err != nil {
		log.Printf("clipboard init failed: %v", err)
		clipboardOK = false
	}

	app := ui.NewApp()
	applyUISettings(app, uiSettings{
		TimestampFormat:  cfg.Appearance.TimestampFormat,
		TypingIndicators: cfg.Animations.TypingIndicators,
		StaleAfter:       time.Duration(cfg.Sidebar.HideInactiveAfterDays) * 24 * time.Hour,
		MouseWheelLines:  cfg.Appearance.MouseWheelLines,
		ColoredUsernames: cfg.Appearance.ColoredUsernames,
		ThemeOverrides:   cfg.Theme,
	})
	app.SetImageProtocol(imgpkg.ProtoOff)
	app.SetClipboardAvailable(clipboardOK)
	app.SetClipboardWriter(newClipboardWriter(runtime.GOOS, os.Getenv, writeMacOSClipboard))
	desktop := core.DesktopServiceFuncs{
		Open:          launchOS,
		ReadClipboard: nativeClipboardRead,
		Stat:          os.Stat,
		SaveThread:    export.SaveThread,
	}
	if useWayland {
		desktop.ReadClipboard = WaylandClipboardReader()
	}
	app.SetDesktopService(core.NewDesktopService(desktop))
	app.SetEditorService(core.NewEditorService(editor.WriteDraft, editor.Edit, editor.TakeDraft))
	if ed, ok := ui.ResolveEditor(cfg.Compose.Editor); ok {
		app.SetComposeEditor(ed)
	}
	app.SetSettingsService(core.NewSettingsService(func(string, core.ThemeScope) {}, func(int) {}))
	app.SetWorkspaceService(core.NewWorkspaceService(func(string) core.Msg { return nil }))
	app.SetAvatarService(core.NewAvatarService(func(string) string { return "" }))
	b.Wire(app)

	me := b.UserID()
	_, server, _ := me.Parse()
	app.SetLoadingWorkspaces([]string{server})
	app.SetWorkspaces([]workspace.WorkspaceItem{{
		ID:       string(me),
		Name:     server,
		Initials: workspace.WorkspaceInitials(server),
	}})

	prog := tea.NewProgram(app)
	b.SetSender(prog.Send)
	go func() {
		// The first sync after login can take a while on big accounts;
		// rooms already in the database show immediately on restarts.
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_ = b.WaitSynced(waitCtx)
		cancel()
		ready, err := b.Workspace(ctx)
		if err != nil {
			log.Printf("building workspace: %v", err)
			prog.Send(ui.WorkspaceFailedMsg{TeamName: server})
			return
		}
		prog.Send(ready)
	}()
	_, err = prog.Run()
	return err
}

