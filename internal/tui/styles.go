package tui

import (
	"charm.land/lipgloss/v2"
)

type styles struct {
	app                lipgloss.Style
	header             lipgloss.Style
	activeTab          lipgloss.Style
	inactiveTab        lipgloss.Style
	panel              lipgloss.Style
	focusedPanel       lipgloss.Style
	status             lipgloss.Style
	dim                lipgloss.Style
	accent             lipgloss.Style
	error              lipgloss.Style
	warning            lipgloss.Style
	modal              lipgloss.Style
	selected           lipgloss.Style
	keyword            lipgloss.Style
	function           lipgloss.Style
	string             lipgloss.Style
	number             lipgloss.Style
	operator           lipgloss.Style
	comment            lipgloss.Style
	schema             lipgloss.Style
	table              lipgloss.Style
	alias              lipgloss.Style
	parameter          lipgloss.Style
	identifier         lipgloss.Style
	info               lipgloss.Style
	autocomplete       lipgloss.Style
	completionSelected lipgloss.Style
	completionMatch    lipgloss.Style
}

func defaultStyles() styles {
	accent := lipgloss.Color("39")
	muted := lipgloss.Color("242")
	return styles{
		app:         lipgloss.NewStyle().Padding(0, 1),
		header:      lipgloss.NewStyle().Bold(true).Foreground(accent),
		activeTab:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(accent).Padding(0, 1),
		inactiveTab: lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("237")).Padding(0, 1),
		panel: lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), true).
			BorderForeground(lipgloss.Color("238")),
		focusedPanel: lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), true).
			BorderForeground(accent),
		status:     lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236")).Padding(0, 1),
		dim:        lipgloss.NewStyle().Foreground(muted),
		accent:     lipgloss.NewStyle().Foreground(accent).Bold(true),
		error:      lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		warning:    lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
		modal:      lipgloss.NewStyle().Border(lipgloss.DoubleBorder(), true).BorderForeground(accent).Padding(1, 2),
		selected:   lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")),
		keyword:    lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		function:   lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
		string:     lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
		number:     lipgloss.NewStyle().Foreground(lipgloss.Color("215")),
		operator:   lipgloss.NewStyle().Foreground(lipgloss.Color("222")),
		comment:    lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true),
		schema:     lipgloss.NewStyle().Foreground(lipgloss.Color("117")),
		table:      lipgloss.NewStyle().Foreground(lipgloss.Color("159")),
		alias:      lipgloss.NewStyle().Foreground(lipgloss.Color("151")),
		parameter:  lipgloss.NewStyle().Foreground(lipgloss.Color("213")),
		identifier: lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		info:       lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		autocomplete: lipgloss.NewStyle().Border(lipgloss.RoundedBorder(), true).
			BorderForeground(accent).Background(lipgloss.Color("235")).Padding(0, 1),
		completionSelected: lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")),
		completionMatch:    lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true).Underline(true),
	}
}
