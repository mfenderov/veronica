package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors (Clean modern slate & sky tech palette)
	colorPrimary  = lipgloss.Color("#38BDF8") // Sky Blue Accent
	colorSuccess  = lipgloss.Color("#34D399") // Mint Green
	colorDanger   = lipgloss.Color("#F87171") // Coral Red
	colorMuted    = lipgloss.Color("#64748B") // Muted Slate
	colorSubtle   = lipgloss.Color("#94A3B8") // Light Slate
	colorBorder   = lipgloss.Color("#334155") // Border Slate
	colorSelectBg = lipgloss.Color("#1E3A5F") // High-contrast Deep Blue Highlight
	colorBgDark   = lipgloss.Color("#0F172A") // Deep Slate Background

	// Header Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	headerBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(colorPrimary).
			Padding(0, 1)

	headerInfo = lipgloss.NewStyle().
			Foreground(colorSubtle)

	// Panel Styles
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	activePaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(0, 1)

	paneTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	// Helper Text Styles
	textSubtle  = lipgloss.NewStyle().Foreground(colorSubtle)
	textSuccess = lipgloss.NewStyle().Foreground(colorSuccess)
	textMuted   = lipgloss.NewStyle().Foreground(colorMuted)
	textDanger  = lipgloss.NewStyle().Foreground(colorDanger)

	// List Item Styles
	selectedItemStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(colorSelectBg)

	normalItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CBD5E1"))

	activeDot   = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
	disabledDot = lipgloss.NewStyle().Foreground(colorMuted).Render("○")
	errorDot    = lipgloss.NewStyle().Foreground(colorDanger).Render("✖")

	badgeStdio = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0")).
			Background(lipgloss.Color("#334155")).
			Padding(0, 1)

	badgeHTTP = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#0284C7")).
			Padding(0, 1)

	// Footer Styles
	footerStyle = lipgloss.NewStyle().
			Foreground(colorSubtle).
			Padding(0, 1)

	footerKey = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)

	// Form Modal Styles
	modalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Background(colorBgDark).
			Padding(1, 2)

	modalTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary)
)
