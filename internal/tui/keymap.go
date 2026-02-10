package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines the key bindings for the TUI dashboard.
type KeyMap struct {
	Fleet   key.Binding
	Health  key.Binding
	Tasks   key.Binding
	Memory  key.Binding
	Stream  key.Binding
	Refresh key.Binding
	Help    key.Binding
	Quit    key.Binding
}

// Keys is the default set of key bindings for the TUI dashboard.
var Keys = KeyMap{
	Fleet: key.NewBinding(
		key.WithKeys("1"),
		key.WithHelp("1", "fleet"),
	),
	Health: key.NewBinding(
		key.WithKeys("2"),
		key.WithHelp("2", "health"),
	),
	Tasks: key.NewBinding(
		key.WithKeys("3"),
		key.WithHelp("3", "tasks"),
	),
	Memory: key.NewBinding(
		key.WithKeys("4"),
		key.WithHelp("4", "memory"),
	),
	Stream: key.NewBinding(
		key.WithKeys("5"),
		key.WithHelp("5", "stream"),
	),
	Refresh: key.NewBinding(
		key.WithKeys("r"),
		key.WithHelp("r", "refresh"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q"),
		key.WithHelp("q", "quit"),
	),
}

// ShortHelp returns key bindings shown in the compact help view.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Help, k.Quit}
}

// FullHelp returns key bindings shown in the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Fleet, k.Health, k.Tasks, k.Memory, k.Stream},
		{k.Refresh, k.Help, k.Quit},
	}
}
