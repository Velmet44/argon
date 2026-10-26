package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	colArgon   = lipgloss.Color("6")  // cyan
	colCmd     = lipgloss.Color("11") // bright yellow
	colErr     = lipgloss.Color("9")  // red
	colDim     = lipgloss.Color("8")  // grey
	colHeader  = lipgloss.Color("12") // bright blue
)

func styleArgon(color bool, s string) string {
	if !color {
		return "◈ " + s
	}
	return lipgloss.NewStyle().Foreground(colArgon).Faint(true).Render("◈ " + s)
}

func styleCmd(color bool, s string) string {
	if !color {
		return "$ " + s
	}
	return lipgloss.NewStyle().Foreground(colCmd).Bold(true).Render("$ " + s)
}

func styleErr(color bool, s string) string {
	if !color {
		return s
	}
	return lipgloss.NewStyle().Foreground(colErr).Bold(true).Render(s)
}

func styleDim(color bool, s string) string {
	if !color {
		return s
	}
	return lipgloss.NewStyle().Foreground(colDim).Faint(true).Render(s)
}

func styleHeader(color bool, s string) string {
	if !color {
		return s
	}
	return lipgloss.NewStyle().Foreground(colHeader).Bold(true).Render(s)
}
