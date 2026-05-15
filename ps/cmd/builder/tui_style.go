package main

import "github.com/charmbracelet/lipgloss"

// Split-flap palette: saturated yellow on the terminal's native
// background. Classic Solari-style departure-board yellow — reflective,
// not emissive. We deliberately don't set background colours; most users
// run dark terminals already, and forcing black would clash with
// light-themed terminals.
var (
	clrYellow     = lipgloss.Color("#FFD700") // primary saturated yellow
	clrYellowHi   = lipgloss.Color("#FFF200") // bright yellow for cursor / active state
	clrYellowDim  = lipgloss.Color("#7A6308") // dim yellow for borders, faded text
	clrYellowDeep = lipgloss.Color("#4A3C00") // deepest yellow for very-quiet faded text
	clrRed        = lipgloss.Color("#FF5F5F") // failures stay red — universal
	clrGray       = lipgloss.Color("#5F5F5F") // skipped / cancelled
)

// Shared styles. Defined once so the run and composer screens stay
// consistent.
var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrYellowHi).
			Padding(0, 1)

	styleHeading = lipgloss.NewStyle().
			Bold(true).
			Foreground(clrYellow)

	styleHint = lipgloss.NewStyle().
			Foreground(clrYellowDeep)

	styleYellow = lipgloss.NewStyle().Foreground(clrYellow)
	styleDim    = lipgloss.NewStyle().Foreground(clrYellowDim)
	styleBad    = lipgloss.NewStyle().Foreground(clrRed).Bold(true)
	styleGray   = lipgloss.NewStyle().Foreground(clrGray)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrYellowDim).
			Padding(0, 1)

	styleCheckedMark   = lipgloss.NewStyle().Foreground(clrYellowHi).Bold(true).Render("[x]")
	styleUncheckedMark = lipgloss.NewStyle().Foreground(clrYellowDim).Render("[ ]")
	styleCursor        = lipgloss.NewStyle().Foreground(clrYellowHi).Bold(true).Render(">")
	styleArrow         = lipgloss.NewStyle().Foreground(clrYellowDim).Render(" → ")
)
