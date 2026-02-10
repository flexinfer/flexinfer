// Package theme defines the color palette and reusable styles for the Loom TUI
// dashboard. Both the tui root package and sub-packages (widgets, panels) import
// this package to avoid import cycles.
package theme

import "github.com/charmbracelet/lipgloss"

// Colors from the web HUD deep teal palette.
var (
	ColorBgPrimary   = lipgloss.Color("#00171A")
	ColorBgSecondary = lipgloss.Color("#002227")
	ColorBgTertiary  = lipgloss.Color("#002E34")
	ColorFgPrimary   = lipgloss.Color("#81F0FE")
	ColorFgSecondary = lipgloss.Color("#5EBDC9")
	ColorFgMuted     = lipgloss.Color("#2A7A87")
	ColorBorder      = lipgloss.Color("#035964")
	ColorSuccess     = lipgloss.Color("#22B255")
	ColorWarning     = lipgloss.Color("#E7B312")
	ColorError       = lipgloss.Color("#E61E3F")
	ColorAccent      = lipgloss.Color("#E95D74")
	ColorInfo        = lipgloss.Color("#018799")
	ColorTierWorking = lipgloss.Color("#4EEAFE")
	ColorTierShort   = lipgloss.Color("#9B5CD0")
	ColorTierLong    = lipgloss.Color("#22B255")
)

// Styles contains reusable lipgloss styles for the TUI.
var Styles = struct {
	// Panel styles
	PanelBorder lipgloss.Style
	PanelTitle  lipgloss.Style
	ActiveTab   lipgloss.Style
	InactiveTab lipgloss.Style

	// Content styles
	SectionTitle lipgloss.Style
	Label        lipgloss.Style
	Value        lipgloss.Style
	MutedText    lipgloss.Style

	// Status styles
	StatusOK    lipgloss.Style
	StatusWarn  lipgloss.Style
	StatusError lipgloss.Style
	StatusMuted lipgloss.Style

	// Table styles
	TableHeader lipgloss.Style
	TableRow    lipgloss.Style
	TableRowAlt lipgloss.Style

	// Header bar
	HeaderBar lipgloss.Style
	Logo      lipgloss.Style
	Clock     lipgloss.Style

	// Help bar
	HelpBar  lipgloss.Style
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
}{}

func init() {
	// Panel styles
	Styles.PanelBorder = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(0, 1)

	Styles.PanelTitle = lipgloss.NewStyle().
		Foreground(ColorFgPrimary).
		Bold(true).
		Padding(0, 1)

	Styles.ActiveTab = lipgloss.NewStyle().
		Foreground(ColorAccent).
		Bold(true).
		Padding(0, 1).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(ColorAccent)

	Styles.InactiveTab = lipgloss.NewStyle().
		Foreground(ColorFgMuted).
		Padding(0, 1)

	// Content styles
	Styles.SectionTitle = lipgloss.NewStyle().
		Foreground(ColorFgSecondary).
		Bold(true).
		MarginBottom(1)

	Styles.Label = lipgloss.NewStyle().
		Foreground(ColorFgMuted)

	Styles.Value = lipgloss.NewStyle().
		Foreground(ColorFgPrimary)

	Styles.MutedText = lipgloss.NewStyle().
		Foreground(ColorFgMuted)

	// Status styles
	Styles.StatusOK = lipgloss.NewStyle().
		Foreground(ColorSuccess)

	Styles.StatusWarn = lipgloss.NewStyle().
		Foreground(ColorWarning)

	Styles.StatusError = lipgloss.NewStyle().
		Foreground(ColorError)

	Styles.StatusMuted = lipgloss.NewStyle().
		Foreground(ColorFgMuted)

	// Table styles
	Styles.TableHeader = lipgloss.NewStyle().
		Foreground(ColorFgSecondary).
		Bold(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderBottom(true).
		BorderForeground(ColorBorder).
		Padding(0, 1)

	Styles.TableRow = lipgloss.NewStyle().
		Foreground(ColorFgPrimary).
		Padding(0, 1)

	Styles.TableRowAlt = lipgloss.NewStyle().
		Foreground(ColorFgPrimary).
		Background(ColorBgTertiary).
		Padding(0, 1)

	// Header bar
	Styles.HeaderBar = lipgloss.NewStyle().
		Background(ColorBgSecondary).
		Foreground(ColorFgPrimary).
		Padding(0, 1)

	Styles.Logo = lipgloss.NewStyle().
		Foreground(ColorFgPrimary).
		Bold(true).
		Background(ColorBgSecondary).
		Padding(0, 1)

	Styles.Clock = lipgloss.NewStyle().
		Foreground(ColorFgMuted).
		Background(ColorBgSecondary).
		Padding(0, 1)

	// Help bar
	Styles.HelpBar = lipgloss.NewStyle().
		Background(ColorBgSecondary).
		Padding(0, 1)

	Styles.HelpKey = lipgloss.NewStyle().
		Foreground(ColorFgMuted).
		Bold(true)

	Styles.HelpDesc = lipgloss.NewStyle().
		Foreground(ColorFgSecondary)
}
