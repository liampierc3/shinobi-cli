package ui

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Comprehensive pattern to strip ONLY mouse tracking ANSI codes while preserving all other codes
// Matches:
// - SGR mouse events: [<64;99;28M or [<64; 99; 28M (with/without spaces)
// - Mouse tracking mode switches: [?1000h, [?1000l, [?1002h, [?1002l, [?1003h, [?1003l, [?1006h, [?1006l
// Does NOT match other important codes like cursor control ([?25h/l) or positioning
var (
	// Only match mouse tracking modes: 1000 (X10), 1002 (button), 1003 (any), 1006 (SGR)
	unwantedANSI  = regexp.MustCompile(`\x1b\[<[0-9; ]+[mMhlH]|\x1b\[\?(?:1000|1002|1003|1006)[hl]`)
	catchAllANSI  = regexp.MustCompile(`\x1b\[<[^\x1b]*?[mMhlH]`)
	bareMouseANSI = regexp.MustCompile(`\[<[0-9; ]+[mM]`)
)

// StripUnwantedANSI removes mouse tracking codes but keeps styling codes
// Exported so it can be used throughout the ui package for defense-in-depth
func StripUnwantedANSI(s string) string {
	// First pass: remove known unwanted codes
	cleaned := unwantedANSI.ReplaceAllString(s, "")

	// Second pass: remove any remaining [< sequences (catch-all for mouse codes)
	// This is more aggressive but necessary for malformed codes with spaces
	cleaned = catchAllANSI.ReplaceAllString(cleaned, "")
	cleaned = bareMouseANSI.ReplaceAllString(cleaned, "")

	if debugANSILoggingEnabled() && cleaned != s {
		logANSI("sanitized", []string{s, cleaned})
	}

	return cleaned
}

func debugANSILoggingEnabled() bool {
	return os.Getenv(ansiLogEnv) != ""
}

func logANSI(event string, payload []string) {
	path := ansiLogTarget
	if path == "" {
		path = os.ExpandEnv("${HOME}/.shinobi/ansi-debug.log")
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	_, _ = f.WriteString("=== " + event + " ===\n")
	for _, item := range payload {
		_, _ = f.WriteString(item)
		_, _ = f.WriteString("\n---\n")
	}
	_, _ = f.WriteString("\n")
}

// MarkdownRenderer handles markdown rendering (plain text during streaming, rendered when complete)
type MarkdownRenderer struct {
	buffer   strings.Builder       // Accumulated content
	renderer *glamour.TermRenderer // Glamour renderer for final markdown
	width    int                   // Terminal width
}

const ansiLogEnv = "OLLAMA_TUI_DEBUG_ANSI"

var ansiLogTarget = os.Getenv("OLLAMA_TUI_ANSI_LOG")

func logGlamourDebug(rendered string) {
	if !debugANSILoggingEnabled() {
		return
	}

	hasANSI := strings.Contains(rendered, "\x1b[") || strings.Contains(rendered, "\x1b]")
	sample := rendered
	if len(sample) > 400 {
		sample = sample[:400]
	}

	payload := []string{
		fmt.Sprintf("lipgloss_profile=%s", lipgloss.ColorProfile().Name()),
		fmt.Sprintf("env_profile=%s", termenv.EnvColorProfile().Name()),
		fmt.Sprintf("detected_profile=%s", termenv.ColorProfile().Name()),
		"has_ansi=" + strconv.FormatBool(hasANSI),
		"rendered_len=" + strconv.Itoa(len(rendered)),
		"rendered_sample=" + sample,
	}
	logANSI("glamour", payload)
}

func newGlamourRenderer(width int) (*glamour.TermRenderer, error) {
	// Match Lip Gloss's color profile so markdown aligns with the UI renderer.
	profile := lipgloss.ColorProfile()
	formatter := "terminal256"
	switch profile {
	case termenv.TrueColor:
		formatter = "terminal16m"
	case termenv.ANSI:
		formatter = "terminal16"
	case termenv.ANSI256:
		formatter = "terminal256"
	}
	return glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width-4), // Leave room for message box borders
		glamour.WithColorProfile(profile),
		glamour.WithChromaFormatter(formatter),
	)
}

// NewMarkdownRenderer creates a new markdown renderer
func NewMarkdownRenderer(width int) (*MarkdownRenderer, error) {
	// Ensure a sane minimum width so Glamour's word wrap never receives
	// a zero/negative value (which can cause render errors).
	if width < minContentWidth {
		width = minContentWidth
	}

	// Create Glamour renderer with explicit dark style
	// Don't use WithAutoStyle() - it can auto-detect wrong terminal mode
	renderer, err := newGlamourRenderer(width)
	if err != nil {
		return nil, err
	}

	return &MarkdownRenderer{
		renderer: renderer,
		width:    width,
	}, nil
}

// AppendToken adds a token to the buffer
// PERFORMANCE: We don't render during streaming, only accumulate
func (r *MarkdownRenderer) AppendToken(token string) {
	if token == "" {
		return
	}

	// CRITICAL: Strip any ANSI codes from incoming tokens
	// This prevents them from getting into the buffer in the first place
	cleaned := StripUnwantedANSI(token)

	r.buffer.WriteString(cleaned)

	// No need to track code block state anymore since we don't render during streaming
	// Rendering only happens once at the end via GetFinalContent()
}

// GetCurrentPlainText returns the current accumulated plain text (no rendering)
func (r *MarkdownRenderer) GetCurrentPlainText() string {
	return r.buffer.String()
}

// GetFinalContent returns the final rendered markdown
func (r *MarkdownRenderer) GetFinalContent() string {
	content := r.buffer.String()

	// Always render as complete markdown at the end
	rendered, err := r.renderer.Render(content)
	if err != nil {
		// Fallback to plain text if rendering fails
		return content
	}
	logGlamourDebug(rendered)

	// Strip mouse tracking and other unwanted ANSI codes
	// Keep styling codes (colors, bold, etc.) but remove mouse events
	cleaned := StripUnwantedANSI(rendered)

	return cleaned
}

// Reset clears the renderer state
func (r *MarkdownRenderer) Reset() {
	r.buffer.Reset()
}

// SetWidth updates the renderer width (for terminal resize)
func (r *MarkdownRenderer) SetWidth(width int) error {
	if width < minContentWidth {
		width = minContentWidth
	}

	r.width = width
	// Recreate renderer with new width
	renderer, err := newGlamourRenderer(width)
	if err != nil {
		return err
	}
	r.renderer = renderer
	return nil
}
