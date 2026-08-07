package ui

import "charm.land/lipgloss/v2"

type theme struct {
	title     lipgloss.Style
	cursor    string
	selected  lipgloss.Style
	normal    lipgloss.Style
	separator lipgloss.Style
	help      lipgloss.Style
	filter    lipgloss.Style
	checkOn   string
	checkOff  string
}

func defaultTheme() *theme {
	faint := lipgloss.NewStyle().Faint(true)
	return &theme{
		title:     lipgloss.NewStyle().Bold(true),
		cursor:    "▸",
		selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")),
		normal:    lipgloss.NewStyle(),
		separator: faint,
		help:      faint,
		filter:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		checkOn:   "✔",
		checkOff:  " ",
	}
}
