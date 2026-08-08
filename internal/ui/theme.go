package ui

import "charm.land/lipgloss/v2"

// Icon glyphs for menu actions — single source of truth, so the whole UI can be
// re-themed by editing these. Keep them single-cell-wide; the icon column is
// padded to a fixed width with lipgloss.Width so any glyph aligns the label.
const (
	IconAdd        = "+"
	IconExtends    = "↗"
	IconDefault    = "★"
	IconDeleteVar  = "✕"
	IconDeleteProf = "⚠"
	IconDone       = "✓"
	IconBack       = "←"
	IconOverride   = "↻"
)

type theme struct {
	title       lipgloss.Style
	cursor      string
	rowActive   lipgloss.Style
	selected    lipgloss.Style
	normal      lipgloss.Style
	separator   lipgloss.Style
	help        lipgloss.Style
	filter      lipgloss.Style
	fieldActive lipgloss.Style
	fieldIdle   lipgloss.Style
	checkOn     string
	checkOff    string
}

func defaultTheme() *theme {
	faint := lipgloss.NewStyle().Faint(true)
	return &theme{
		title:       lipgloss.NewStyle().Bold(true),
		cursor:      "▸",
		rowActive:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("237")),
		selected:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")),
		normal:      lipgloss.NewStyle(),
		separator:   faint,
		help:        faint,
		filter:      lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		fieldActive: lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderForeground(lipgloss.Color("4")).PaddingLeft(1),
		fieldIdle:   lipgloss.NewStyle().Faint(true).PaddingLeft(2),
		checkOn:     "●",
		checkOff:    "○",
	}
}
