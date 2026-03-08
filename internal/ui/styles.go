package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Shinobi color palette - Website Green
var (
	// Base colors
	colorBackground = "#1e1e2e" // Base
	colorForeground = "#e0e0e0" // Text
	colorMuted      = "#6c7086" // Overlay1 (gray)

	// Role-based message colors
	colorUserRole      = "#7fb02a" // Website green - user
	colorAssistantRole = "#94e2d5" // Teal - calm, helpful
	colorSystemRole    = "#b4befe" // Lavender - neutral, info
	colorErrorRole     = "#f38ba8" // Red - alerts

	// Accent colors
	colorMauve    = "#cba6f7" // Purple
	colorGreen    = "#a6e3a1" // Soft green
	colorPeach    = "#fab387" // Peach/Orange
	colorSky      = "#89dceb" // Sky Blue
	colorSapphire = "#74c7ec" // Sapphire
	colorYellow   = "#f9e2af" // Yellow

	// Primary UI colors
	colorPrimary   = "#7fb02a" // Website green - primary accent
	colorSuccess   = "#7fb02a" // Website green - success/active
	colorWarning   = "#fab387" // Peach - warnings
	colorError     = "#f38ba8" // Red - errors
	colorAccent    = "#7fb02a" // Website green - accent
	colorModelName = "#7fb02a" // Website green - highlights
)

// Icons holds all the glyphs/icons used throughout the UI
type Icons struct {
	// Status indicators
	Active      string // ● or *
	Inactive    string // ○ or space
	Success     string // ✓ or +
	Thinking    string // ◌ or o

	// Navigation
	Selected   string // ▸ or >
	Unselected string // space

	// Hierarchy
	BranchStart string // ├─ or |-
	BranchCont  string // │  or |

	// Separators
	Dot string // • or *
	Bar string // │ or |

	// Special
	Cursor string // ▊ or _

	// Roles
	RoleUser      string // ▸ or >
	RoleAssistant string // ◆ or *
	RoleSystem    string // ◉ or o
	RoleError     string // ⚠ or !

	// Rounded box borders (NEW - Phase 2)
	BoxTopLeft     string // ╭ or +
	BoxTopRight    string // ╮ or +
	BoxBottomLeft  string // ╰ or +
	BoxBottomRight string // ╯ or +
	BoxHorizontal  string // ─ or -
	BoxVertical    string // │ or |
}

func defaultIcons() Icons {
	return Icons{
		Active:         "●",
		Inactive:       "○",
		Success:        "✓",
		Thinking:       "◌",
		Selected:       "▸",
		Unselected:     "  ",
		BranchStart:    "├─",
		BranchCont:     "│ ",
		Dot:            "•",
		Bar:            "│",
		Cursor:         "▊",
		RoleUser:       "",
		RoleAssistant:  "",
		RoleSystem:     "",
		RoleError:      "",
		BoxTopLeft:     "╭",
		BoxTopRight:    "╮",
		BoxBottomLeft:  "╰",
		BoxBottomRight: "╯",
		BoxHorizontal:  "─",
		BoxVertical:    "│",
	}
}

func plainIcons() Icons {
	return Icons{
		Active:         "*",
		Inactive:       " ",
		Success:        "+",
		Thinking:       "o",
		Selected:       ">",
		Unselected:     "  ",
		BranchStart:    "|-",
		BranchCont:     "| ",
		Dot:            "*",
		Bar:            "|",
		Cursor:         "_",
		RoleUser:       "",
		RoleAssistant:  "",
		RoleSystem:     "",
		RoleError:      "",
		BoxTopLeft:     "+",
		BoxTopRight:    "+",
		BoxBottomLeft:  "+",
		BoxBottomRight: "+",
		BoxHorizontal:  "-",
		BoxVertical:    "|",
	}
}

// Styles holds all the lipgloss styles for the UI
type Styles struct {
	// Message styles
	UserMessage      lipgloss.Style
	AssistantMessage lipgloss.Style
	SystemMessage    lipgloss.Style
	ErrorMessage     lipgloss.Style

	// Message box styles (NEW - Phase 2)
	UserMessageBox      lipgloss.Style
	AssistantMessageBox lipgloss.Style
	SystemMessageBox    lipgloss.Style
	ErrorMessageBox     lipgloss.Style

	// Message metadata
	Timestamp lipgloss.Style
	ModelName lipgloss.Style

	// Input styles
	Input       lipgloss.Style
	InputPrompt lipgloss.Style
	InputHint   lipgloss.Style

	// Status bar
	StatusBar        lipgloss.Style
	StatusBarItem    lipgloss.Style
	StatusBarActive  lipgloss.Style
	StatusBarDivider lipgloss.Style
	StatusBarMuted   lipgloss.Style

	// General
	Border lipgloss.Style
	Help   lipgloss.Style

	// Modal menu
	MenuBox             lipgloss.Style
	MenuTitle           lipgloss.Style
	MenuItem            lipgloss.Style
	MenuItemSelected    lipgloss.Style
	MenuItemDescription lipgloss.Style
	MenuHint            lipgloss.Style

	// Menu border variations (NEW - Lipgloss enhancements)
	MenuBoxPrimary   lipgloss.Style // Main menu - rounded border
	MenuBoxSecondary lipgloss.Style // List menus - single border
	MenuBoxInfo      lipgloss.Style // Help/settings - double border
	MenuBoxAction    lipgloss.Style // Destructive actions - thick border

	// Icon system
	Icons Icons
}

// DefaultStyles returns the default style set
func DefaultStyles() Styles {
	if isPlainTerminal() {
		return plainStyles()
	}

	return Styles{
		// User messages - blue, bold
		UserMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			Bold(true).
			PaddingLeft(2),

		// Assistant messages - off white for readability
		AssistantMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorForeground)).
			PaddingLeft(2),

		// System messages - gray, italic
		SystemMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true).
			PaddingLeft(2),

		// Error messages - red
		ErrorMessage: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorError)).
			Bold(true).
			PaddingLeft(2),

		// Message boxes with role-specific colors (NEW - Phase 2)
		UserMessageBox: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorForeground)).
			Padding(0, 1).
			MarginBottom(1),

		AssistantMessageBox: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorForeground)).
			BorderLeft(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colorMuted)).
			MarginBottom(1),

		SystemMessageBox: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Padding(0, 1).
			MarginBottom(1),

		ErrorMessageBox: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colorErrorRole)).
			Background(lipgloss.AdaptiveColor{
				Light: "#f38ba8", // Red in light mode
				Dark:  "#2d1e25", // Very dark red tint
			}).
			Foreground(lipgloss.Color(colorForeground)).
			Padding(0, 1).
			MarginBottom(1),

		// Timestamp - muted
		Timestamp: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true),

		// Model name - orange highlight
		ModelName: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorModelName)).
			Italic(true),

		// Input field
		Input: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorForeground)).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colorMuted)).
			Padding(0, 1),

		// Input prompt ("> ")
		InputPrompt: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			Bold(true),

		// Input hint text
		InputHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true),

		// Status bar
		StatusBar: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Background(lipgloss.Color(colorBackground)),

		StatusBarItem: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			PaddingLeft(1).
			PaddingRight(1),

		StatusBarActive: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorPrimary)).
			PaddingLeft(1).
			PaddingRight(1),

		StatusBarDivider: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			PaddingLeft(1).
			PaddingRight(1),

		StatusBarMuted: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true).
			PaddingLeft(1).
			PaddingRight(1),

		// Border style
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colorMuted)),

		// Help text
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Italic(true),

		// Menu — single muted border, no color noise
		MenuBox: lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colorMuted)).
			Padding(0, 1),

		MenuTitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)).
			Bold(true).
			MarginBottom(1),

		MenuItem: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorForeground)).
			PaddingLeft(1),

		MenuItemSelected: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorBackground)).
			Background(lipgloss.Color(colorForeground)).
			Bold(true).
			PaddingLeft(1),

		MenuItemDescription: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)),

		MenuHint: lipgloss.NewStyle().
			Foreground(lipgloss.Color(colorMuted)),

		// Keep variants as aliases to the same simple style
		MenuBoxPrimary:   lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(colorMuted)).Padding(0, 1),
		MenuBoxSecondary: lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(colorMuted)).Padding(0, 1),
		MenuBoxInfo:      lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(colorMuted)).Padding(0, 1),
		MenuBoxAction:    lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color(colorMuted)).Padding(0, 1),

		// Icon system
		Icons: defaultIcons(),
	}
}

func isPlainTerminal() bool {
	return termenv.EnvColorProfile() == termenv.Ascii
}

func plainStyles() Styles {
	return Styles{
		UserMessage:         lipgloss.NewStyle().PaddingLeft(2),
		AssistantMessage:    lipgloss.NewStyle().PaddingLeft(2),
		SystemMessage:       lipgloss.NewStyle().PaddingLeft(2),
		ErrorMessage:        lipgloss.NewStyle().PaddingLeft(2),
		UserMessageBox:      lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(0, 1).MarginBottom(1),
		AssistantMessageBox: lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(0, 1).MarginBottom(1),
		SystemMessageBox:    lipgloss.NewStyle().Padding(0, 1).MarginBottom(1),
		ErrorMessageBox:     lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(0, 1).MarginBottom(1),
		Timestamp:           lipgloss.NewStyle(),
		ModelName:           lipgloss.NewStyle(),
		Input:               lipgloss.NewStyle().Padding(0, 1).BorderStyle(lipgloss.NormalBorder()),
		InputPrompt:         lipgloss.NewStyle(),
		InputHint:           lipgloss.NewStyle(),
		StatusBar:           lipgloss.NewStyle(),
		StatusBarItem:       lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1),
		StatusBarActive:     lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1),
		StatusBarDivider:    lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1),
		StatusBarMuted:      lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1),
		Border:              lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()),
		Help:                lipgloss.NewStyle(),
		MenuBox:             lipgloss.NewStyle().BorderStyle(lipgloss.DoubleBorder()).Padding(1, 2),
		MenuTitle:           lipgloss.NewStyle().Bold(true),
		MenuItem:            lipgloss.NewStyle().PaddingLeft(1),
		MenuItemSelected:    lipgloss.NewStyle().PaddingLeft(1).Bold(true),
		MenuItemDescription: lipgloss.NewStyle(),
		MenuHint:            lipgloss.NewStyle().MarginTop(1),
		MenuBoxPrimary:      lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(1, 2),
		MenuBoxSecondary:    lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(1, 2),
		MenuBoxInfo:         lipgloss.NewStyle().BorderStyle(lipgloss.DoubleBorder()).Padding(1, 2),
		MenuBoxAction:       lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(1, 2),
		Icons:               plainIcons(),
	}
}
