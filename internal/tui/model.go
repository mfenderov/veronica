// Package tui provides an interactive terminal user interface for monitoring and managing the Veronica gateway.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
)

type viewMode int

const (
	modeDashboard viewMode = iota
	modeAdd
	modeEdit

	interactiveAuthTimeout = 3 * time.Minute
)

type statusMsg struct {
	status domain.GatewayStatus
	err    error
}

type modulesMsg struct {
	modules []domain.ModuleSummary
	err     error
}

type actionResultMsg struct {
	message string
	isError bool
}

// Model represents the Bubble Tea model and state for the Veronica interactive TUI.
type Model struct {
	service        domain.PodService
	modules        []domain.ModuleSummary
	status         domain.GatewayStatus
	cursor         int
	width          int
	height         int
	mode           viewMode
	nameInput      textinput.Model
	cmdInput       textinput.Model
	transportType  domain.TransportType
	activeInputIdx int
	statusMsg      string
	statusIsError  bool
	// Live-ops refresh loop state.
	events         []uiEvent
	eventsSrc      EventSource
	autoRefresh    bool
	lastOk         time.Time
	lastErr        error
	hasGoodData    bool
	prevModules    []domain.ModuleSummary
	lastStatusErr  bool
	lastModulesErr bool
}

// NewModel creates an initialized TUI Model connected to the specified PodService.
func NewModel(service domain.PodService) Model {
	nameInput := textinput.New()
	nameInput.Placeholder = "module-name (e.g. postgres)"
	nameInput.Focus()

	cmdInput := textinput.New()
	cmdInput.Placeholder = "command (e.g. /bin/pg-mcp)"

	return Model{
		service:       service,
		modules:       make([]domain.ModuleSummary, 0),
		mode:          modeDashboard,
		nameInput:     nameInput,
		cmdInput:      cmdInput,
		transportType: domain.TransportStdio,
		width:         100,
		height:        28,
		eventsSrc:     localEventSource{},
		autoRefresh:   true,
	}
}

// Init sets up initial commands to load status and module lists upon TUI startup.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadStatusCmd(),
		m.loadModulesCmd(),
		tickCmd(),
	)
}

func (m Model) loadStatusCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		st, err := m.service.Status(ctx)
		return statusMsg{status: st, err: err}
	}
}

func (m Model) loadModulesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		mods, err := m.service.ListModules(ctx)
		return modulesMsg{modules: mods, err: err}
	}
}

func (m Model) toggleModuleCmd(name string, currentStatus domain.ModuleStatus) tea.Cmd {
	enable := currentStatus != domain.StatusActive
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		res, err := m.service.ToggleModule(ctx, name, enable)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Toggle failed: %v", err), isError: true}
		}
		return actionResultMsg{message: res.Message, isError: false}
	}
}

func (m Model) recallModuleCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		res, err := m.service.RecallModule(ctx, name)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Recall failed: %v", err), isError: true}
		}
		return actionResultMsg{message: res.Message, isError: false}
	}
}

func (m Model) deployModuleCmd() tea.Cmd {
	p := domain.DeployParams{
		Name:      strings.TrimSpace(m.nameInput.Value()),
		Transport: string(m.transportType),
	}
	target := strings.TrimSpace(m.cmdInput.Value())
	if m.transportType == domain.TransportStdio {
		p.Command = target
	} else {
		p.URL = target
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		res, err := m.service.DeployModule(ctx, p)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Deploy failed: %v", err), isError: true}
		}
		return actionResultMsg{message: res.Message, isError: false}
	}
}

func (m Model) reauthModuleCmd(name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), interactiveAuthTimeout)
		defer cancel()
		res, err := m.service.ReauthModule(ctx, name)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Reauth failed: %v", err), isError: true}
		}
		return actionResultMsg{message: res.Message, isError: false}
	}
}

func (m Model) restartDaemonCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		defer cancel()
		res, err := m.service.RestartDaemon(ctx)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Restart failed: %v", err), isError: true}
		}
		return actionResultMsg{message: res.Message, isError: false}
	}
}
