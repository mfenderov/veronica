package tui_test

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/tui"
)

type mockPodService struct {
	modules []domain.ModuleSummary
	status  domain.GatewayStatus
}

func (m *mockPodService) Status(ctx context.Context) (domain.GatewayStatus, error) {
	return m.status, nil
}

func (m *mockPodService) ListModules(ctx context.Context) ([]domain.ModuleSummary, error) {
	return m.modules, nil
}

func (m *mockPodService) DeployModule(ctx context.Context, p domain.DeployParams) (domain.DeployResult, error) {
	m.modules = append(m.modules, domain.ModuleSummary{
		Name:      p.Name,
		Transport: domain.TransportType(p.Transport),
		Status:    domain.StatusActive,
		Tools:     []string{"sample_tool"},
	})
	return domain.DeployResult{Name: p.Name, Status: domain.StatusActive, Message: "deployed ok"}, nil
}

func (m *mockPodService) RecallModule(ctx context.Context, name string) (domain.RecallResult, error) {
	filtered := make([]domain.ModuleSummary, 0)
	for _, mod := range m.modules {
		if mod.Name != name {
			filtered = append(filtered, mod)
		}
	}
	m.modules = filtered
	return domain.RecallResult{Name: name, Success: true, Message: "recalled ok"}, nil
}

func (m *mockPodService) ToggleModule(ctx context.Context, name string, enable bool) (domain.ToggleResult, error) {
	status := domain.StatusInactive
	if enable {
		status = domain.StatusActive
	}
	for i := range m.modules {
		if m.modules[i].Name == name {
			m.modules[i].Status = status
		}
	}
	return domain.ToggleResult{Name: name, Enabled: enable, Status: status, Message: "toggled ok"}, nil
}

func (m *mockPodService) ReauthModule(ctx context.Context, name string) (domain.ReauthResult, error) {
	return domain.ReauthResult{Name: name, Success: true, Message: "reauth ok"}, nil
}

func (m *mockPodService) RestartDaemon(ctx context.Context) (domain.RestartResult, error) {
	return domain.RestartResult{Success: true, Message: "reloaded ok"}, nil
}

func (m *mockPodService) RecentTraces(ctx context.Context, limit int) ([]domain.ToolTrace, error) {
	return nil, nil
}

func TestTUI_Lifecycle(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{
		status: domain.GatewayStatus{
			Uptime:        "10m",
			ActiveModules: 1,
			TotalTools:    5,
			AllocMB:       2,
		},
		modules: []domain.ModuleSummary{
			{
				Name:      "mark42",
				Transport: domain.TransportStdio,
				Status:    domain.StatusActive,
				Target:    "/bin/mark42",
				Tools:     []string{"search_nodes", "open_nodes"},
			},
			{
				Name:      "slack",
				Transport: domain.TransportHTTP,
				Status:    domain.StatusInactive,
				Target:    "https://mcp.slack.com",
				Tools:     []string{"slack_send_message"},
			},
		},
	}

	model := tui.NewModel(svc)
	cmd := model.Init()
	if cmd == nil {
		t.Fatal("expected non-nil Init command")
	}

	updated := model
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				msg := subCmd()
				u, _ := updated.Update(msg)
				updated = u.(tui.Model)
			}
		}
	}

	// 1. Test Window Resize
	u, _ := updated.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	updated = u.(tui.Model)
	view := updated.View()
	if !strings.Contains(view, "VERONICA ORBITAL POD") {
		t.Fatalf("expected title in view, got: %s", view)
	}

	// 2. Test Navigation Down
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = u.(tui.Model)
	// 3. Test Navigation Up
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	updated = u.(tui.Model)

	// 4. Test Toggle
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeySpace})
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected cmd on toggle")
	}

	// 5. Test Recall Key (d)
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected cmd on recall")
	}

	// 6. Test Refresh Key (r)
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected batch cmd on refresh")
	}

	// 7. Test Enter Add Mode
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = u.(tui.Model)
	view = updated.View()
	if !strings.Contains(view, "DEPLOY NEW MCP MODULE") {
		t.Fatalf("expected add modal view, got: %s", view)
	}

	// 8. Test Form Inputs (type name, switch to command, toggle transport, submit)
	for _, ch := range "test-mod" {
		u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		updated = u.(tui.Model)
	}
	// Tab switch
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyTab})
	updated = u.(tui.Model)
	for _, ch := range "/bin/test" {
		u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		updated = u.(tui.Model)
	}
	// Ctrl+T toggle transport
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	updated = u.(tui.Model)
	// Ctrl+T toggle transport back
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	updated = u.(tui.Model)

	// Enter submit
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected deploy cmd on enter")
	}
	// Execute deploy cmd to generate actionResultMsg
	deployResMsg := cmd()
	u, cmd = updated.Update(deployResMsg)
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected batch cmd on actionResultMsg")
	}

	// 9. Test Cancel Add Mode (Esc)
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = u.(tui.Model)
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEsc})
	updated = u.(tui.Model)
	view = updated.View()
	if !strings.Contains(view, "MODULES") {
		t.Fatalf("expected dashboard view after Esc, got: %s", view)
	}

	// 10. Test Quit Key (q) and Ctrl+C
	_, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected quit cmd on q")
	}
	_, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected quit cmd on ctrl+c")
	}
}

func TestTUI_SmallWindow(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{}
	model := tui.NewModel(svc)

	updated, _ := model.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	view := updated.View()

	if !strings.Contains(view, "too small") {
		t.Fatalf("expected small window warning, got: %s", view)
	}
}

func TestTUI_DeterministicSortAndCursorPreservation(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{
		status: domain.GatewayStatus{Uptime: "1h", ActiveModules: 3},
		modules: []domain.ModuleSummary{
			{Name: "zeta", Transport: domain.TransportStdio, Status: domain.StatusActive},
			{Name: "alpha", Transport: domain.TransportStdio, Status: domain.StatusActive},
			{Name: "gamma", Transport: domain.TransportStdio, Status: domain.StatusActive},
		},
	}

	model := tui.NewModel(svc)
	cmd := model.Init()

	updated := model
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				u, _ := updated.Update(subCmd())
				updated = u.(tui.Model)
			}
		}
	}

	// Move to index 1 (which should be "gamma" after sorting: alpha, gamma, zeta)
	u, _ := updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated = u.(tui.Model)

	view := updated.View()
	if !strings.Contains(view, "INSPECTOR: gamma") {
		t.Fatalf("expected cursor on gamma after sort, view: %s", view)
	}

	// Simulate refresh (r) where service returns items in different order: gamma, zeta, alpha
	svc.modules = []domain.ModuleSummary{
		{Name: "gamma", Transport: domain.TransportStdio, Status: domain.StatusActive},
		{Name: "zeta", Transport: domain.TransportStdio, Status: domain.StatusActive},
		{Name: "alpha", Transport: domain.TransportStdio, Status: domain.StatusActive},
	}
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	updated = u.(tui.Model)
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				u, _ = updated.Update(subCmd())
				updated = u.(tui.Model)
			}
		}
	}

	// Verify cursor is still on gamma!
	view = updated.View()
	if !strings.Contains(view, "INSPECTOR: gamma") {
		t.Fatalf("expected cursor preserved on gamma after refresh, got view: %s", view)
	}
}

func TestTUI_NavigationBounds(t *testing.T) {
	t.Parallel()

	// Test 1: Empty list navigation does not panic
	svc := &mockPodService{modules: []domain.ModuleSummary{}}
	model := tui.NewModel(svc)
	u, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyUp})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeySpace})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	view := u.View()
	if !strings.Contains(view, "No module selected") {
		t.Fatalf("expected 'No module selected' in empty list view: %s", view)
	}

	// Test 2: Boundary clamping with populated list
	svc2 := &mockPodService{
		modules: []domain.ModuleSummary{
			{Name: "mod-a", Transport: domain.TransportStdio, Status: domain.StatusActive},
			{Name: "mod-b", Transport: domain.TransportStdio, Status: domain.StatusActive},
		},
	}
	model2 := tui.NewModel(svc2)
	cmd := model2.Init()
	updated := model2
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				u2, _ := updated.Update(subCmd())
				updated = u2.(tui.Model)
			}
		}
	}

	// Press Up at 0 -> stays at 0 (mod-a)
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if !strings.Contains(u.View(), "INSPECTOR: mod-a") {
		t.Fatal("expected cursor clamped at 0")
	}

	// Press Down multiple times -> clamps at last item (mod-b)
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyDown})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyDown})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyDown})
	if !strings.Contains(u.View(), "INSPECTOR: mod-b") {
		t.Fatal("expected cursor clamped at last index")
	}
}

func TestTUI_FormModalValidation(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{}
	model := tui.NewModel(svc)

	// Enter add mode
	u, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	// Press Enter with empty fields -> should NOT dispatch deploy command
	u, cmd := u.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected nil cmd when submitting empty form")
	}

	// Cycle Tab back and forth
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyTab})
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyTab})

	// Toggle transport with Ctrl+T and verify
	u, _ = u.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	view := u.View()
	if !strings.Contains(view, "http") {
		t.Fatalf("expected http transport in view after Ctrl+T: %s", view)
	}
}

func TestTUI_WindowDimensions(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{
		modules: []domain.ModuleSummary{
			{Name: "mod-1", Transport: domain.TransportStdio, Status: domain.StatusActive},
		},
	}
	model := tui.NewModel(svc)

	// Minimum viable dimensions 40x10 -> does not panic
	u, _ := model.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	view := u.View()
	if strings.Contains(view, "too small") {
		t.Fatal("40x10 should not report too small")
	}

	// Ultrawide dimensions 200x60 -> does not panic
	u, _ = model.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	view = u.View()
	if !strings.Contains(view, "VERONICA ORBITAL POD") {
		t.Fatal("expected header in ultrawide layout")
	}
}

func TestTUI_EditAndReauth(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{
		status: domain.GatewayStatus{Uptime: "1h", ActiveModules: 1},
		modules: []domain.ModuleSummary{
			{Name: "slack", Transport: domain.TransportHTTP, Status: domain.StatusActive, Target: "https://mcp.slack.com"},
		},
	}

	model := tui.NewModel(svc)
	cmd := model.Init()
	updated := model
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				u, _ := updated.Update(subCmd())
				updated = u.(tui.Model)
			}
		}
	}

	// 1. Test Re-auth key (A)
	u, cmd := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	updated = u.(tui.Model)
	if cmd == nil {
		t.Fatal("expected reauth command on 'A'")
	}
	reauthRes := cmd()
	u, _ = updated.Update(reauthRes)
	updated = u.(tui.Model)

	// 2. Test Edit key (e)
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	updated = u.(tui.Model)
	view := updated.View()
	if !strings.Contains(view, "EDIT MODULE: slack") {
		t.Fatalf("expected edit modal for slack, got: %s", view)
	}

	// 3. Test submitting edit form
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected deploy command on enter in edit mode")
	}
	updated = u.(tui.Model)

	// 4. Test Reload Daemon key (R)
	u, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	if cmd == nil {
		t.Fatal("expected restart command on 'R'")
	}
	u, _ = u.Update(cmd())
	view = u.View()
	if !strings.Contains(view, "reloaded ok") {
		t.Fatalf("expected reload message in status, got view: %s", view)
	}
}

func TestTUI_FooterKeysAndPlaceholders(t *testing.T) {
	t.Parallel()

	svc := &mockPodService{
		status: domain.GatewayStatus{Uptime: "1h", ActiveModules: 1},
		modules: []domain.ModuleSummary{
			{Name: "mark42", Transport: domain.TransportStdio, Status: domain.StatusActive, Target: "/bin/mark42"},
		},
	}

	model := tui.NewModel(svc)
	cmd := model.Init()
	updated := model
	if batchMsg, ok := cmd().(tea.BatchMsg); ok {
		for _, subCmd := range batchMsg {
			if subCmd != nil {
				u, _ := updated.Update(subCmd())
				updated = u.(tui.Model)
			}
		}
	}

	// 1. Verify all footer keys are documented in dashboard view
	view := updated.View()
	for _, key := range []string{"[Space]", "[e]", "[A]", "[a]", "[d]", "[r]", "[q]"} {
		if !strings.Contains(view, key) {
			t.Errorf("expected footer to document key %s, got view: %s", key, view)
		}
	}

	// 2. Test Refresh (r) updates status message
	u, _ := updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	updated = u.(tui.Model)
	if !strings.Contains(updated.View(), "Catalog refreshed") {
		t.Fatalf("expected 'Catalog refreshed' in view after pressing 'r', got: %s", updated.View())
	}

	// 3. Test Add mode (a) input placeholders and dynamic labels
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated = u.(tui.Model)
	addModal := updated.View()
	if !strings.Contains(addModal, "Command:") {
		t.Fatalf("expected 'Command:' label for stdio transport in add modal, got: %s", addModal)
	}

	// Toggle to HTTP with Ctrl+T
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	updated = u.(tui.Model)
	httpModal := updated.View()
	if !strings.Contains(httpModal, "Endpoint URL:") {
		t.Fatalf("expected 'Endpoint URL:' label for http transport after toggle, got: %s", httpModal)
	}

	// Toggle back to stdio with Ctrl+T
	u, _ = updated.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	updated = u.(tui.Model)
	stdioModal := updated.View()
	if !strings.Contains(stdioModal, "Command:") {
		t.Fatalf("expected 'Command:' label after toggling back to stdio, got: %s", stdioModal)
	}
}
