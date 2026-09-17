package tui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
)

const defaultTimeout = 5 * time.Second

// Update processes incoming Bubble Tea messages and returns updated models and commands.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tickMsg:
		return m.handleTickMsg()
	case statusMsg:
		return m.handleStatusMsg(msg)
	case modulesMsg:
		return m.handleModulesMsg(msg)
	case actionResultMsg:
		return m.handleActionResult(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	default:
		return m, nil
	}
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	return m, nil
}

func (m Model) handleTickMsg() (tea.Model, tea.Cmd) {
	if m.mode == modeAdd || m.mode == modeEdit || !m.autoRefresh {
		return m, tickCmd()
	}
	return m, tea.Batch(m.loadStatusCmd(), m.loadModulesCmd(), tickCmd())
}

func (m Model) handleStatusMsg(msg statusMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.statusMsg = fmt.Sprintf("Status error: %v", msg.err)
		m.statusIsError = true
		if !m.lastStatusErr {
			m = m.appendEvent(newEvent(eventKindError, "status error: "+msg.err.Error()))
		}
		m.lastStatusErr = true
		m.lastErr = msg.err
		return m, nil
	}
	m.status = msg.status
	if m.lastStatusErr {
		m = m.appendEvent(newEvent(eventKindResult, "status recovered"))
	}
	m.lastStatusErr = false
	m.lastOk = time.Now()
	if !m.lastModulesErr {
		m.lastErr = nil
	}
	m.hasGoodData = true
	return m, nil
}

func (m Model) handleModulesMsg(msg modulesMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.withModulesError(msg.err), nil
	}
	m = m.withModulesSnapshot(msg.modules)
	m = m.markModulesOk()
	return m, nil
}

func (m Model) withModulesError(err error) Model {
	m.statusMsg = fmt.Sprintf("Modules error: %v", err)
	m.statusIsError = true
	if !m.lastModulesErr {
		m = m.appendEvent(newEvent(eventKindError, "modules error: "+err.Error()))
	}
	m.lastModulesErr = true
	m.lastErr = err
	return m
}

func (m Model) withModulesSnapshot(mods []domain.ModuleSummary) Model {
	selectedName := m.currentSelectedName()
	sorted := sortModules(mods)
	m.recordModuleDeltas(sorted)
	m.prevModules = append([]domain.ModuleSummary(nil), sorted...)
	m.modules = sorted
	m.cursor = m.findCursorForName(selectedName)
	return m
}

func (m Model) recordModuleDeltas(sorted []domain.ModuleSummary) {
	if m.prevModules == nil {
		return
	}
	src := m.eventsSrc
	if src == nil {
		src = localEventSource{}
	}
	for _, ev := range src.Deltas(m.prevModules, sorted) {
		m = m.appendEvent(ev)
	}
}

func (m Model) markModulesOk() Model {
	if m.lastModulesErr {
		m = m.appendEvent(newEvent(eventKindResult, "modules recovered"))
	}
	m.lastModulesErr = false
	m.lastOk = time.Now()
	if !m.lastStatusErr {
		m.lastErr = nil
	}
	m.hasGoodData = true
	return m
}

func (m Model) currentSelectedName() string {
	if len(m.modules) > 0 && m.cursor < len(m.modules) {
		return m.modules[m.cursor].Name
	}
	return ""
}

func sortModules(mods []domain.ModuleSummary) []domain.ModuleSummary {
	sorted := make([]domain.ModuleSummary, len(mods))
	copy(sorted, mods)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func (m Model) findCursorForName(name string) int {
	if name != "" {
		for i, mod := range m.modules {
			if mod.Name == name {
				return i
			}
		}
	}
	if m.cursor >= len(m.modules) && len(m.modules) > 0 {
		return len(m.modules) - 1
	}
	return m.cursor
}

func (m Model) handleActionResult(msg actionResultMsg) (tea.Model, tea.Cmd) {
	m.statusMsg = msg.message
	m.statusIsError = msg.isError
	m = m.appendEvent(newEvent(eventKindResult, msg.message))
	return m, tea.Batch(m.loadStatusCmd(), m.loadModulesCmd())
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	if m.mode == modeAdd || m.mode == modeEdit {
		return m.handleFormKey(msg)
	}

	return m.handleDashboardKey(msg)
}

func (m Model) handleDashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.isNavKey(key) {
		return m.handleNavKey(key), nil
	}
	return m.handleActionKey(key)
}

func (m Model) isNavKey(key string) bool {
	return key == "up" || key == "k" || key == "down" || key == "j"
}

func (m Model) handleNavKey(key string) Model {
	if key == "up" || key == "k" {
		return m.moveCursor(-1)
	}
	return m.moveCursor(1)
}

func (m Model) handleActionKey(key string) (tea.Model, tea.Cmd) {
	if res, cmd, ok := m.handleRefreshKey(key); ok {
		return res, cmd
	}
	switch key {
	case "q":
		return m, tea.Quit
	case " ":
		return m.handleToggleKey()
	case "e":
		return m.enterEditMode(), nil
	case "A", "ctrl+a":
		return m.handleReauthKey()
	case "R", "ctrl+r":
		return m, m.restartDaemonCmd()
	case "d":
		return m.handleRecallKey()
	case "a":
		return m.enterAddMode(), nil
	default:
		return m, nil
	}
}

func (m Model) handleRefreshKey(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "r":
		m.statusMsg = "Catalog refreshed"
		m.statusIsError = false
		return m, tea.Batch(m.loadStatusCmd(), m.loadModulesCmd()), true
	case "p":
		m.autoRefresh = !m.autoRefresh
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) moveCursor(delta int) Model {
	newIdx := m.cursor + delta
	if newIdx >= 0 && newIdx < len(m.modules) {
		m.cursor = newIdx
	}
	return m
}

func (m Model) handleToggleKey() (tea.Model, tea.Cmd) {
	if len(m.modules) == 0 || m.cursor >= len(m.modules) {
		return m, nil
	}
	mod := m.modules[m.cursor]
	return m, m.toggleModuleCmd(mod.Name, mod.Status)
}

func (m Model) handleRecallKey() (tea.Model, tea.Cmd) {
	if len(m.modules) == 0 || m.cursor >= len(m.modules) {
		return m, nil
	}
	mod := m.modules[m.cursor]
	return m, m.recallModuleCmd(mod.Name)
}

func (m Model) handleReauthKey() (tea.Model, tea.Cmd) {
	if len(m.modules) == 0 || m.cursor >= len(m.modules) {
		return m, nil
	}
	mod := m.modules[m.cursor]
	return m, m.reauthModuleCmd(mod.Name)
}

func (m Model) enterAddMode() Model {
	m.mode = modeAdd
	m.nameInput.SetValue("")
	m.cmdInput.SetValue("")
	m.transportType = domain.TransportStdio
	m.cmdInput.Placeholder = "command (e.g. /bin/pg-mcp)"
	m.activeInputIdx = 0
	m.nameInput.Focus()
	m.cmdInput.Blur()
	return m
}

func (m Model) enterEditMode() Model {
	if len(m.modules) == 0 || m.cursor >= len(m.modules) {
		return m
	}
	mod := m.modules[m.cursor]
	m.mode = modeEdit
	m.nameInput.SetValue(mod.Name)
	m.cmdInput.SetValue(mod.Target)
	m.transportType = mod.Transport
	if m.transportType == domain.TransportHTTP {
		m.cmdInput.Placeholder = "https://mcp.example.com/sse"
	} else {
		m.cmdInput.Placeholder = "command (e.g. /bin/pg-mcp)"
	}
	m.activeInputIdx = 1
	m.nameInput.Blur()
	m.cmdInput.Focus()
	return m
}

func (m Model) handleFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeDashboard
		return m, nil
	case "tab":
		return m.switchActiveInput(), nil
	case "ctrl+t":
		return m.toggleFormTransport(), nil
	case "enter":
		return m.submitForm()
	default:
		return m.updateFormInputs(msg)
	}
}

func (m Model) switchActiveInput() Model {
	if m.activeInputIdx == 0 {
		m.activeInputIdx = 1
		m.nameInput.Blur()
		m.cmdInput.Focus()
	} else {
		m.activeInputIdx = 0
		m.cmdInput.Blur()
		m.nameInput.Focus()
	}
	return m
}

func (m Model) toggleFormTransport() Model {
	if m.transportType == domain.TransportStdio {
		m.transportType = domain.TransportHTTP
		m.cmdInput.Placeholder = "https://mcp.example.com/sse"
	} else {
		m.transportType = domain.TransportStdio
		m.cmdInput.Placeholder = "command (e.g. /bin/pg-mcp)"
	}
	return m
}

func (m Model) submitForm() (tea.Model, tea.Cmd) {
	if m.nameInput.Value() == "" || m.cmdInput.Value() == "" {
		return m, nil
	}
	m.mode = modeDashboard
	return m, m.deployModuleCmd()
}

func (m Model) updateFormInputs(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.activeInputIdx == 0 {
		m.nameInput, cmd = m.nameInput.Update(msg)
	} else {
		m.cmdInput, cmd = m.cmdInput.Update(msg)
	}
	return m, cmd
}
