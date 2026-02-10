package tui

import "github.com/charmbracelet/lipgloss"

// Layout computes panel dimensions based on terminal size.
type Layout struct {
	Width    int
	Height   int
	HeaderH  int // height of top header bar (1 line)
	HelpH    int // height of bottom help bar (1 line)
	ContentH int // remaining height for panel content
	ContentW int // available width for panel content
}

// NewLayout creates a layout for the given terminal dimensions.
func NewLayout(width, height int) Layout {
	headerH := 1
	helpH := 1
	contentH := height - headerH - helpH
	if contentH < 1 {
		contentH = 1
	}
	return Layout{
		Width:    width,
		Height:   height,
		HeaderH:  headerH,
		HelpH:    helpH,
		ContentH: contentH,
		ContentW: width,
	}
}

// PanelStyle returns a lipgloss style for the main content panel area.
func (l Layout) PanelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Width(l.ContentW).
		Height(l.ContentH)
}
