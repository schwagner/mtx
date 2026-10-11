package version

import (
	"strings"
	"testing"
)

func TestModalFooter(t *testing.T) {
	got := ModalFooter("v1.2.3")

	// Version is interpolated.
	if !strings.Contains(got, "mtx v1.2.3") {
		t.Errorf("footer missing version: %q", got)
	}
	// Attribution to slk's author.
	if !strings.Contains(got, "built on slk") || !strings.Contains(got, "Grant Ammons") {
		t.Errorf("footer missing slk attribution: %q", got)
	}
	// URL is present and OSC-8 wrapped (clickable), with visible label.
	if !strings.Contains(got, "\x1b]8;;https://github.com/gammons/slk\x1b\\") {
		t.Errorf("footer url not OSC-8 wrapped: %q", got)
	}
	// URL is parenthesized.
	if !strings.Contains(got, "(") || !strings.Contains(got, ")") {
		t.Errorf("footer url not parenthesized: %q", got)
	}
}

func TestModalFooterUsesGivenVersion(t *testing.T) {
	got := ModalFooter("dev")
	if !strings.Contains(got, "mtx dev") {
		t.Errorf("expected 'mtx dev' in %q", got)
	}
}
