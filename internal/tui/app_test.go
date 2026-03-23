package tui

import (
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("café-世界", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
	if lipgloss.Width(got) > 7 {
		t.Fatalf("truncate width = %d, want <= 7", lipgloss.Width(got))
	}
}
