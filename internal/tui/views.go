package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mfenderov/veronica/internal/domain"
)

// View renders the visual TUI interface string based on current model state.
func (m Model) View() string {
	if m.width < 40 || m.height < 10 {
		return "Terminal window too small for Veronica TUI"
	}

	if m.mode == modeAdd || m.mode == modeEdit {
		return m.renderFormModal()
	}

	header := m.renderHeader()
	health := renderHealthStrip(m.modules, m.lastErr, m.hasGoodData)
	main := m.renderMainDashboard()
	var bottomContent string
	if m.bottomPane == bottomPaneTraces {
		bottomContent = renderTracesPane(m.traces)
	} else {
		bottomContent = renderEventsPane(m.events)
	}
	bottom := paneStyle.Width(m.width - 4).Render(bottomContent)
	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, header, health, main, bottom, footer)
}

func (m Model) renderHeader() string {
	title := titleStyle.Render("🛰️  VERONICA ORBITAL POD")
	badge := headerBadge.Render("MCP GATEWAY")

	info := headerInfo.Render(fmt.Sprintf(
		"Uptime: %s  •  Modules: %d active  •  Tools: %d  •  RAM: %dMB",
		m.status.Uptime,
		m.status.ActiveModules,
		m.status.TotalTools,
		m.status.AllocMB,
	))

	gap := m.width - lipgloss.Width(title) - lipgloss.Width(badge) - lipgloss.Width(info) - 4
	if gap < 1 {
		gap = 1
	}

	left := lipgloss.JoinHorizontal(lipgloss.Center, title, badge)
	return lipgloss.JoinHorizontal(lipgloss.Center, left, strings.Repeat(" ", gap), info)
}

func (m Model) renderMainDashboard() string {
	paneHeight := m.height - 14
	if paneHeight < 5 {
		paneHeight = 5
	}

	leftWidth := m.width/3 - 2
	if leftWidth < 28 {
		leftWidth = 28
	}
	rightWidth := m.width - leftWidth - 6
	if rightWidth < 30 {
		rightWidth = 30
	}

	left := m.renderLeftPane(leftWidth, paneHeight)
	right := m.renderRightPane(rightWidth, paneHeight)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) renderLeftPane(width, height int) string {
	var b strings.Builder
	b.WriteString(paneTitle.Render("MODULES") + "\n\n")

	if m.statusIsError && len(m.modules) == 0 {
		b.WriteString(textDanger.Render(m.statusMsg))
	} else if len(m.modules) == 0 {
		b.WriteString(textSubtle.Render("No modules configured\nPress [a] to deploy an MCP"))
	} else {
		for i, mod := range m.modules {
			item := renderModuleListItem(mod, i == m.cursor, width-4)
			b.WriteString(item + "\n")
		}
	}

	return activePaneStyle.
		Width(width).
		Height(height).
		Render(b.String())
}

func renderModuleListItem(mod domain.ModuleSummary, selected bool, width int) string {
	dot := activeDot
	switch mod.Status {
	case domain.StatusInactive:
		dot = disabledDot
	case domain.StatusError:
		dot = errorDot
	}

	transportPill := badgeStdio.Render("stdio")
	if mod.Transport == domain.TransportHTTP || mod.Transport == domain.TransportSSE {
		transportPill = badgeHTTP.Render("http ")
	}

	toolCount := fmt.Sprintf("%2d tools", len(mod.Tools))
	if mod.Status == domain.StatusInactive {
		toolCount = "disabled"
	}

	name := mod.Name
	if len(name) > 12 {
		name = name[:10] + ".."
	}

	if selected {
		row := fmt.Sprintf(" ❯ %s %-12s %s %s", dot, name, transportPill, toolCount)
		return selectedItemStyle.Width(width).Render(row)
	}

	row := fmt.Sprintf("   %s %-12s %s %s", dot, name, transportPill, toolCount)
	return normalItemStyle.Width(width).Render(row)
}

func (m Model) renderRightPane(width, height int) string {
	var b strings.Builder

	if len(m.modules) == 0 || m.cursor >= len(m.modules) {
		b.WriteString(textSubtle.Render("No module selected"))
		return paneStyle.Width(width).Height(height).Render(b.String())
	}

	mod := m.modules[m.cursor]
	b.WriteString(paneTitle.Render("INSPECTOR: "+mod.Name) + "\n\n")

	var statusStr string
	switch mod.Status {
	case domain.StatusActive:
		statusStr = textSuccess.Render("ACTIVE")
	case domain.StatusInactive:
		statusStr = textMuted.Render("DISABLED")
	default:
		statusStr = textDanger.Render("ERROR: " + mod.Error)
	}

	fmt.Fprintf(&b, "Status:    %s\n", statusStr)
	fmt.Fprintf(&b, "Transport: %s\n", mod.Transport)
	fmt.Fprintf(&b, "Target:    %s\n\n", mod.Target)

	b.WriteString(paneTitle.Render(fmt.Sprintf("TOOLS EXPOSED (%d)", len(mod.Tools))) + "\n")
	if len(mod.Tools) == 0 {
		b.WriteString(textSubtle.Render("  (No tools registered)\n"))
	} else {
		for i, toolName := range mod.Tools {
			if i >= height-8 {
				b.WriteString(textSubtle.Render(fmt.Sprintf("  ... and %d more\n", len(mod.Tools)-i)))
				break
			}
			fmt.Fprintf(&b, "  • %s\n", toolName)
		}
	}

	return paneStyle.Width(width).Height(height).Render(b.String())
}

func (m Model) renderFooter() string {
	keys := fmt.Sprintf(
		"%s Toggle  •  %s Edit  •  %s Re-auth  •  %s Reload  •  %s Deploy  •  %s Recall  •  %s Refresh  •  %s Traces  •  %s Pause  •  %s Quit",
		footerKey.Render("[Space]"),
		footerKey.Render("[e]"),
		footerKey.Render("[A]"),
		footerKey.Render("[R]"),
		footerKey.Render("[a]"),
		footerKey.Render("[d]"),
		footerKey.Render("[r]"),
		footerKey.Render("[t]"),
		footerKey.Render("[p]"),
		footerKey.Render("[q]"),
	)

	statusLine := m.statusMsg
	if m.statusIsError {
		statusLine = textDanger.Render("✖ " + m.statusMsg)
	} else if m.statusMsg != "" {
		statusLine = textSuccess.Render("✓ " + m.statusMsg)
	}

	return lipgloss.JoinVertical(lipgloss.Left, footerStyle.Render(keys), statusLine)
}

func (m Model) renderFormModal() string {
	var b strings.Builder
	title := "🛰️  DEPLOY NEW MCP MODULE"
	if m.mode == modeEdit {
		title = "✏️  EDIT MODULE: " + m.nameInput.Value()
	}
	b.WriteString(modalTitle.Render(title) + "\n\n")

	b.WriteString("Module Name:\n")
	b.WriteString(m.nameInput.View() + "\n\n")

	transportBadge := badgeStdio.Render("stdio")
	if m.transportType == domain.TransportHTTP {
		transportBadge = badgeHTTP.Render("http")
	}
	fmt.Fprintf(&b, "Transport: %s  %s\n\n", transportBadge, textSubtle.Render("(Press [Ctrl+T] to toggle)"))

	targetLabel := "Command:"
	if m.transportType == domain.TransportHTTP {
		targetLabel = "Endpoint URL:"
	}
	b.WriteString(targetLabel + "\n")
	b.WriteString(m.cmdInput.View() + "\n\n")

	actionLabel := "[Enter] Deploy"
	if m.mode == modeEdit {
		actionLabel = "[Enter] Save Changes"
	}
	b.WriteString(textSubtle.Render(actionLabel + "  •  [Tab] Switch input  •  [Esc] Cancel"))

	content := modalBox.Width(50).Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}
