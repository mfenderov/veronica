package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
)

type stubPodService struct {
	status     domain.GatewayStatus
	modules    []domain.ModuleSummary
	traces     []domain.ToolTrace
	statusErr  error
	modulesErr error
}

func (s *stubPodService) Status(_ context.Context) (domain.GatewayStatus, error) {
	if s.statusErr != nil {
		return domain.GatewayStatus{}, s.statusErr
	}
	return s.status, nil
}

func (s *stubPodService) ListModules(_ context.Context) ([]domain.ModuleSummary, error) {
	if s.modulesErr != nil {
		return nil, s.modulesErr
	}
	return s.modules, nil
}

func (s *stubPodService) DeployModule(_ context.Context, p domain.DeployParams) (domain.DeployResult, error) {
	return domain.DeployResult{Name: p.Name, Status: domain.StatusActive, Message: "deployed ok"}, nil
}

func (s *stubPodService) RecallModule(_ context.Context, name string) (domain.RecallResult, error) {
	return domain.RecallResult{Name: name, Success: true, Message: "recalled ok"}, nil
}

func (s *stubPodService) ToggleModule(_ context.Context, name string, enable bool) (domain.ToggleResult, error) {
	st := domain.StatusInactive
	if enable {
		st = domain.StatusActive
	}
	return domain.ToggleResult{Name: name, Enabled: enable, Status: st, Message: "toggled ok"}, nil
}

func (s *stubPodService) ReauthModule(_ context.Context, name string) (domain.ReauthResult, error) {
	return domain.ReauthResult{Name: name, Success: true, Message: "reauth ok"}, nil
}

func (s *stubPodService) RestartDaemon(_ context.Context) (domain.RestartResult, error) {
	return domain.RestartResult{Success: true, Message: "reloaded ok"}, nil
}

func (s *stubPodService) RecentTraces(_ context.Context, _ int) ([]domain.ToolTrace, error) {
	return s.traces, nil
}

func testLiveModel() Model {
	svc := &stubPodService{
		status:  domain.GatewayStatus{Uptime: "10m", ActiveModules: 1, TotalTools: 2, AllocMB: 4},
		modules: []domain.ModuleSummary{{Name: "alpha", Transport: domain.TransportStdio, Status: domain.StatusActive, Tools: []string{"t1"}}},
	}
	m := NewModel(svc)
	m.width = 100
	m.height = 28
	return m
}

func TestLive_TickRearmsAndFetchesOnDashboard(t *testing.T) {
	old := tickInterval
	tickInterval = time.Millisecond
	defer func() { tickInterval = old }()

	m := testLiveModel()
	u, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected non-nil cmd from tick (re-arm + fetch)")
	}
	got := cmd()
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected BatchMsg with loads + re-arm, got %T", got)
	}
	if len(batch) != 4 {
		t.Fatalf("expected 4 cmds (status, modules, traces, tick), got %d", len(batch))
	}
	_ = u
}

func TestLive_TickSkipsFetchInModal(t *testing.T) {
	old := tickInterval
	tickInterval = time.Millisecond
	defer func() { tickInterval = old }()

	m := testLiveModel()
	m.mode = modeAdd
	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected re-arm cmd even in modal")
	}
	got := cmd()
	if _, ok := got.(tea.BatchMsg); ok {
		t.Fatal("modal tick must not batch loads, only re-arm")
	}

	m.mode = modeEdit
	_, cmd = m.Update(tickMsg(time.Now()))
	got = cmd()
	if _, ok := got.(tea.BatchMsg); ok {
		t.Fatal("edit modal tick must not batch loads")
	}
}

func TestLive_TickSkipsFetchWhenPaused(t *testing.T) {
	old := tickInterval
	tickInterval = time.Millisecond
	defer func() { tickInterval = old }()

	m := testLiveModel()
	m.autoRefresh = false
	_, cmd := m.Update(tickMsg(time.Now()))
	got := cmd()
	if _, ok := got.(tea.BatchMsg); ok {
		t.Fatal("paused tick must not batch loads")
	}
}

func TestLive_PToggleFlipsAutoRefresh(t *testing.T) {
	m := testLiveModel()
	if !m.autoRefresh {
		t.Fatal("expected autoRefresh default true")
	}
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	nm := u.(Model)
	if nm.autoRefresh {
		t.Fatal("expected autoRefresh false after p")
	}
	u, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	nm = u.(Model)
	if !nm.autoRefresh {
		t.Fatal("expected autoRefresh true after second p")
	}
	if !strings.Contains(nm.View(), "[p]") {
		t.Fatal("expected footer to document [p]")
	}
}

func TestLive_LocalDeltasAddRemoveStatus(t *testing.T) {
	src := localEventSource{}
	prev := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1"}},
		{Name: "beta", Status: domain.StatusActive, Tools: []string{"t1", "t2"}},
	}
	next := []domain.ModuleSummary{
		{Name: "beta", Status: domain.StatusError, Error: "boom", Tools: []string{"t1"}},
		{Name: "gamma", Status: domain.StatusActive, Tools: []string{"t1"}},
	}
	deltas := src.Deltas(prev, next)
	var sb strings.Builder
	for _, d := range deltas {
		sb.WriteString(d.text)
		sb.WriteString("|")
	}
	text := sb.String()
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "gamma") || !strings.Contains(text, "beta") {
		t.Fatalf("expected add/remove/status deltas, got %q", text)
	}

	silent := src.Deltas(prev, []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1"}},
		{Name: "beta", Status: domain.StatusActive, Tools: []string{"t1", "t2"}},
	})
	if len(silent) != 0 {
		t.Fatalf("expected no deltas for identical lists, got %d", len(silent))
	}
}

func TestLive_RingCapKeepsNewestLast(t *testing.T) {
	m := testLiveModel()
	for i := 0; i < 60; i++ {
		m = m.appendEvent(uiEvent{at: time.Now(), kind: "action", text: strings.Repeat("x", 1) + string(rune('a'+i%26))})
	}
	if len(m.events) != 50 {
		t.Fatalf("expected 50 events after 60 appends, got %d", len(m.events))
	}
}

func TestLive_ErrorTransitionNoSpam(t *testing.T) {
	m := testLiveModel()
	before := len(m.events)

	u, _ := m.Update(statusMsg{err: errors.New("down")})
	m = u.(Model)
	if len(m.events) != before+1 {
		t.Fatalf("expected 1 event on entering failure, got %d", len(m.events)-before)
	}
	u, _ = m.Update(statusMsg{err: errors.New("down")})
	m = u.(Model)
	if len(m.events) != before+1 {
		t.Fatalf("expected no extra event on repeated failure, got %d total new", len(m.events)-before)
	}
	u, _ = m.Update(statusMsg{status: domain.GatewayStatus{Uptime: "1m"}})
	m = u.(Model)
	if len(m.events) != before+2 {
		t.Fatalf("expected recovery event, got %d total new", len(m.events)-before)
	}
}

func TestLive_DeriveHealthCases(t *testing.T) {
	healthy := deriveHealth([]domain.ModuleSummary{{Name: "a", Status: domain.StatusActive}}, nil, true)
	if healthy != "HEALTHY" {
		t.Fatalf("expected HEALTHY, got %q", healthy)
	}
	degraded := deriveHealth([]domain.ModuleSummary{{Name: "a", Status: domain.StatusError}}, nil, true)
	if degraded != "DEGRADED" {
		t.Fatalf("expected DEGRADED, got %q", degraded)
	}
	stale := deriveHealth([]domain.ModuleSummary{{Name: "a", Status: domain.StatusActive}}, errors.New("x"), true)
	if stale != "STALE" {
		t.Fatalf("expected STALE, got %q", stale)
	}
	offline := deriveHealth(nil, errors.New("x"), false)
	if offline != "OFFLINE" {
		t.Fatalf("expected OFFLINE, got %q", offline)
	}
}

func TestLive_ViewContainsEventsAndHealth(t *testing.T) {
	m := testLiveModel()
	m = m.appendEvent(uiEvent{at: time.Date(2026, 9, 16, 12, 4, 2, 0, time.Local), kind: "action", text: "deployed ok"})
	view := m.View()
	if !strings.Contains(view, "EVENTS") {
		t.Fatalf("expected EVENTS pane, got:\n%s", view)
	}
	if !strings.Contains(view, "HEALTHY") {
		t.Fatalf("expected health label, got:\n%s", view)
	}
	if !strings.Contains(view, "deployed ok") {
		t.Fatalf("expected event text in view, got:\n%s", view)
	}

	m.width = 100
	m.height = 5
	if !strings.Contains(m.View(), "too small") {
		t.Fatal("small-terminal guard must win")
	}
}

func TestLive_TToggleFlipsBottomPane(t *testing.T) {
	m := testLiveModel()
	if m.bottomPane != bottomPaneEvents {
		t.Fatal("expected bottomPaneEvents default")
	}

	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	nm := u.(Model)
	if nm.bottomPane != bottomPaneTraces {
		t.Fatal("expected bottomPaneTraces after t")
	}
	if !strings.Contains(nm.View(), "TOOL TRACES") {
		t.Fatalf("expected TOOL TRACES in view, got:\n%s", nm.View())
	}

	u, _ = nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	nm = u.(Model)
	if nm.bottomPane != bottomPaneEvents {
		t.Fatal("expected bottomPaneEvents after second t")
	}
	if !strings.Contains(nm.View(), "EVENTS") {
		t.Fatalf("expected EVENTS in view, got:\n%s", nm.View())
	}
	if !strings.Contains(nm.View(), "[t]") {
		t.Fatal("expected footer to document [t]")
	}
}

func TestLive_TracesMsgUpdatesModel(t *testing.T) {
	m := testLiveModel()
	traceList := []domain.ToolTrace{
		{
			ID:         "t1",
			Timestamp:  time.Now(),
			ModuleName: "atlassian",
			ToolName:   "getJiraIssue",
			Duration:   120 * time.Millisecond,
			IsError:    false,
		},
		{
			ID:         "t2",
			Timestamp:  time.Now(),
			ModuleName: "slack",
			ToolName:   "slack_send_message",
			Duration:   2500 * time.Millisecond,
			IsError:    true,
			ErrorMsg:   "timeout",
		},
	}

	u, _ := m.Update(tracesMsg{traces: traceList})
	nm := u.(Model)
	if len(nm.traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(nm.traces))
	}

	nm.bottomPane = bottomPaneTraces
	view := nm.View()
	if !strings.Contains(view, "getJiraIssue") || !strings.Contains(view, "slack_send_message") {
		t.Fatalf("expected traces visible in view, got:\n%s", view)
	}
	if !strings.Contains(view, "OK") || !strings.Contains(view, "ERR") {
		t.Fatalf("expected OK and ERR status in view, got:\n%s", view)
	}
}

func TestLive_RenderTracesPane_Empty(t *testing.T) {
	view := renderTracesPane(nil)
	if !strings.Contains(view, "No tool calls recorded yet") {
		t.Fatalf("expected empty placeholder, got:\n%s", view)
	}
}
