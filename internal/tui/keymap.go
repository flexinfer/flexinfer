package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines the key bindings for the TUI dashboard.
type KeyMap struct {
	Overview key.Binding
	Fleet    key.Binding
	Health   key.Binding
	Tasks    key.Binding
	Memory   key.Binding
	Stream   key.Binding
	Presence key.Binding
	Cost     key.Binding
	RBAC     key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Quit     key.Binding

	// Panel interaction keys
	Enter  key.Binding
	Escape key.Binding
	Filter key.Binding
}

// Keys is the default set of key bindings for the TUI dashboard.
var Keys = KeyMap{
	Overview: key.NewBinding(
		key.WithKeys("1"),
		key.WithHelp("1", "overview"),
	),
	Fleet: key.NewBinding(
		key.WithKeys("2"),
		key.WithHelp("2", "fleet"),
	),
	Health: key.NewBinding(
		key.WithKeys("3"),
		key.WithHelp("3", "health"),
	),
	Tasks: key.NewBinding(
		key.WithKeys("4"),
		key.WithHelp("4", "tasks"),
	),
	Memory: key.NewBinding(
		key.WithKeys("5"),
		key.WithHelp("5", "memory"),
	),
	Stream: key.NewBinding(
		key.WithKeys("6"),
		key.WithHelp("6", "stream"),
	),
	Presence: key.NewBinding(
		key.WithKeys("7"),
		key.WithHelp("7", "presence"),
	),
	Cost: key.NewBinding(
		key.WithKeys("8"),
		key.WithHelp("8", "cost"),
	),
	RBAC: key.NewBinding(
		key.WithKeys("9"),
		key.WithHelp("9", "rbac"),
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
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("⏎", "expand/action"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "collapse"),
	),
	Filter: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "filter"),
	),
}

// ShortHelp returns key bindings shown in the compact help view.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Enter, k.Filter, k.Help, k.Quit}
}

// FullHelp returns key bindings shown in the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Overview, k.Fleet, k.Health, k.Tasks, k.Memory, k.Stream, k.Presence, k.Cost, k.RBAC},
		{k.Refresh, k.Enter, k.Escape, k.Filter},
		{k.Help, k.Quit},
	}
}
