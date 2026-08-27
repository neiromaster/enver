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
	IconInherited  = "↳"
	IconUnset      = "⊘"
)

type theme struct {
	title       lipgloss.Style
	cursor      string
	rowActive   lipgloss.Style
	selected    lipgloss.Style
	normal      lipgloss.Style
	dim         lipgloss.Style
	separator   lipgloss.Style
	help        lipgloss.Style
	filter      lipgloss.Style
	fieldActive lipgloss.Style
	fieldIdle   lipgloss.Style
	checkOn     string
	checkOff    string
	iconStyles  map[string]lipgloss.Style
}

func defaultTheme() *theme {
	faint := lipgloss.NewStyle().Faint(true)
	green := lipgloss.NewStyle().Foreground(lipgloss.Green)
	red := lipgloss.NewStyle().Foreground(lipgloss.Red)
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	blue := lipgloss.NewStyle().Foreground(lipgloss.Blue)
	magenta := lipgloss.NewStyle().Foreground(lipgloss.Magenta)
	cyan := lipgloss.NewStyle().Foreground(lipgloss.Cyan)

	return &theme{
		title:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Cyan),
		cursor:      cyan.Render("▸"),
		rowActive:   cyan.Bold(true),
		selected:    cyan.Bold(true),
		normal:      lipgloss.NewStyle(),
		dim:         faint,
		separator:   faint,
		help:        faint,
		filter:      yellow,
		fieldActive: lipgloss.NewStyle().BorderStyle(lipgloss.ThickBorder()).BorderLeft(true).BorderForeground(lipgloss.Blue).PaddingLeft(1),
		fieldIdle:   lipgloss.NewStyle().Faint(true).PaddingLeft(2),
		checkOn:     green.Render("●"),
		checkOff:    faint.Render("○"),
		iconStyles: map[string]lipgloss.Style{
			IconAdd:        green,
			IconDone:       green,
			IconExtends:    blue,
			IconInherited:  blue,
			IconDefault:    yellow,
			IconOverride:   magenta,
			IconDeleteVar:  red,
			IconDeleteProf: red,
			IconUnset:      red,
			IconBack:       cyan,
		},
	}
}

// icon renders an icon glyph in its semantic color. Unknown glyphs pass through
// uncolored so the menu still renders if a caller passes an ad-hoc icon.
func (t *theme) icon(glyph string) string {
	if style, ok := t.iconStyles[glyph]; ok {
		return style.Bold(true).Render(glyph)
	}
	return glyph
}
