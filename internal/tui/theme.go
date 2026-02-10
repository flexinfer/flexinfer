package tui

import "github.com/crb2nu/loom/internal/tui/theme"

// Re-export color constants from the theme sub-package so that existing code
// referencing tui.ColorXxx continues to compile without changes.
var (
	ColorBgPrimary   = theme.ColorBgPrimary
	ColorBgSecondary = theme.ColorBgSecondary
	ColorBgTertiary  = theme.ColorBgTertiary
	ColorFgPrimary   = theme.ColorFgPrimary
	ColorFgSecondary = theme.ColorFgSecondary
	ColorFgMuted     = theme.ColorFgMuted
	ColorBorder      = theme.ColorBorder
	ColorSuccess     = theme.ColorSuccess
	ColorWarning     = theme.ColorWarning
	ColorError       = theme.ColorError
	ColorAccent      = theme.ColorAccent
	ColorInfo        = theme.ColorInfo
	ColorTierWorking = theme.ColorTierWorking
	ColorTierShort   = theme.ColorTierShort
	ColorTierLong    = theme.ColorTierLong
)

// Styles re-exports the shared styles struct from the theme sub-package.
var Styles = &theme.Styles
