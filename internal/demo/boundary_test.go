package demo

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Demo mode must never reach the user's real data or the network.
var demoBannedImports = []string{
	"net", "net/http", "os", "os/exec", "database/sql",
	"github.com/gorilla/websocket",
	"modernc.org/sqlite",
	"github.com/schwagner/mtx/internal/avatar",
	"github.com/schwagner/mtx/internal/bootstrap",
	"github.com/schwagner/mtx/internal/cache",
	"github.com/schwagner/mtx/internal/config",
	"github.com/schwagner/mtx/internal/editor",
	"github.com/schwagner/mtx/internal/export",
	"github.com/schwagner/mtx/internal/filedl",
	"github.com/schwagner/mtx/internal/notify",
	"github.com/schwagner/mtx/internal/service",
	"github.com/schwagner/mtx/internal/wake",
}

func demoImportBanned(path string) bool {
	// mrkdwn is pure text conversion, the same one internal/ui uses to
	// render its optimistic send.
	if path == "github.com/schwagner/mtx/internal/slack/mrkdwn" {
		return false
	}
	if strings.HasPrefix(path, "github.com/schwagner/mtx/internal/slack") {
		return true
	}
	return slices.Contains(demoBannedImports, path)
}

func TestDemoStaysOffTheNetworkAndDisk(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if demoImportBanned(path) {
				t.Errorf("%s imports %s; demo mode must not touch real data or the network", name, path)
			}
		}
	}
}

func TestDemoImportBanCoversSlackSubpackages(t *testing.T) {
	for path, want := range map[string]bool{
		"github.com/schwagner/mtx/internal/slack":        true,
		"github.com/schwagner/mtx/internal/slackhttp":    true,
		"github.com/schwagner/mtx/internal/slackdesktop": true,
		"github.com/schwagner/mtx/internal/slack/mrkdwn": false,
		"github.com/schwagner/mtx/internal/core":         false,
	} {
		if got := demoImportBanned(path); got != want {
			t.Errorf("demoImportBanned(%s) = %v, want %v", path, got, want)
		}
	}
}
