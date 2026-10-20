package tui

import "charm.land/lipgloss/v2"

// Card widths, in terminal cells.
const (
	// DeviceCardWidth frames the device-flow card.
	DeviceCardWidth = 56
	// PasswordCardWidth frames the password-flow card.
	PasswordCardWidth = 52
	// HomeCardWidth frames the session shell cards.
	HomeCardWidth = 64
	// MinWidth and MinHeight below which the TUI refuses to render a layout.
	MinWidth  = 44
	MinHeight = 14
)

// Styles is the resolved palette for one background polarity.
//
// Lip Gloss v2 colors are values and AdaptiveColor is gone, so every style is
// rebuilt from the terminal's reported background color.
type Styles struct {
	Accent     lipgloss.Style
	Error      lipgloss.Style
	Success    lipgloss.Style
	Muted      lipgloss.Style
	Foreground lipgloss.Style
	Bold       lipgloss.Style
	Title      lipgloss.Style
	Subtitle   lipgloss.Style
	UserCode   lipgloss.Style
	Hint       lipgloss.Style
	Label      lipgloss.Style
	Selected   lipgloss.Style
	Card       lipgloss.Style
	Notice     lipgloss.Style
}

// newStyles builds the palette for the given background polarity.
func newStyles(dark bool) Styles {
	ld := lipgloss.LightDark(dark)

	accent := lipgloss.Color("#7D56F4")
	errColor := lipgloss.Color("#F25F4C")
	okColor := lipgloss.Color("#2E9E5B")
	foreground := ld(lipgloss.Color("#1B1B1B"), lipgloss.Color("#EDEDED"))
	muted := ld(lipgloss.Color("#6C6C6C"), lipgloss.Color("#9B9B9B"))

	base := lipgloss.NewStyle().Foreground(foreground)

	return Styles{
		Accent:     lipgloss.NewStyle().Foreground(accent),
		Error:      lipgloss.NewStyle().Foreground(errColor),
		Success:    lipgloss.NewStyle().Foreground(okColor),
		Muted:      lipgloss.NewStyle().Foreground(muted),
		Foreground: base,
		Bold:       base.Bold(true),
		Title:      base.Foreground(accent).Bold(true),
		Subtitle:   lipgloss.NewStyle().Foreground(muted),
		UserCode:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		Hint:       lipgloss.NewStyle().Foreground(muted),
		Label:      lipgloss.NewStyle().Foreground(muted),
		Selected:   lipgloss.NewStyle().Foreground(accent).Bold(true),
		Notice:     lipgloss.NewStyle().Foreground(okColor),
		Card: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(muted).
			Padding(1, 3),
	}
}

// cardWidth clamps a card width to the available terminal width.
func cardWidth(preferred, available int) int {
	inner := available - 8 // room for border and padding
	if inner < 20 {
		inner = 20
	}
	if preferred > inner {
		return inner
	}
	return preferred
}
