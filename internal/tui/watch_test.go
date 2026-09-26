package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
)

func TestLive_WTogglesWatchFlag(t *testing.T) {
	svc := &stubPodService{
		modules: []domain.ModuleSummary{
			{Name: "alpha", Transport: domain.TransportStdio, Status: domain.StatusActive, Tools: []string{"t1"}},
		},
	}
	m := NewModel(svc)
	m.width, m.height = 100, 28
	u, _ := m.Update(modulesMsg{modules: svc.modules})
	m = u.(Model)

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m = u.(Model)
	if cmd == nil {
		t.Fatal("expected cmd on W")
	}
	res := cmd()
	if len(svc.watchCalls) != 1 || svc.watchCalls[0] != (watchCall{name: "alpha", enable: false}) {
		t.Fatalf("expected W to disable watch for alpha, got %+v", svc.watchCalls)
	}

	u, _ = m.Update(res)
	m = u.(Model)
	u, _ = m.Update(modulesMsg{modules: svc.modules})
	m = u.(Model)
	if !strings.Contains(m.View(), "off") {
		t.Fatalf("expected watch status off in view after W, got:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "Watch disabled for alpha") {
		t.Fatalf("expected watch action message in view, got:\n%s", m.View())
	}

	// W again flips the flag back on.
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m = u.(Model)
	res = cmd()
	if len(svc.watchCalls) != 2 || svc.watchCalls[1] != (watchCall{name: "alpha", enable: true}) {
		t.Fatalf("expected W to re-enable watch for alpha, got %+v", svc.watchCalls)
	}
	_ = res
}

func TestLive_PPausesAndResumesWatch(t *testing.T) {
	off := false
	svc := &stubPodService{
		modules: []domain.ModuleSummary{
			{Name: "alpha", Transport: domain.TransportStdio, Status: domain.StatusActive, Tools: []string{"t1"}},
			{Name: "beta", Transport: domain.TransportStdio, Status: domain.StatusActive, Tools: []string{"t2"}, WatchBinary: &off},
		},
	}
	m := NewModel(svc)
	m.width, m.height = 100, 28
	u, _ := m.Update(modulesMsg{modules: svc.modules})
	m = u.(Model)

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = u.(Model)
	if !m.watchPaused {
		t.Fatal("expected watchPaused after P")
	}
	res := cmd()
	if len(svc.watchCalls) != 2 {
		t.Fatalf("expected pause to disable watch for every module, got %+v", svc.watchCalls)
	}
	for _, name := range []string{"alpha", "beta"} {
		found := false
		for _, c := range svc.watchCalls {
			if c == (watchCall{name: name, enable: false}) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected pause to disable watch for %s, got %+v", name, svc.watchCalls)
		}
	}

	u, _ = m.Update(res)
	m = u.(Model)
	if !strings.Contains(m.View(), "off") {
		t.Fatalf("expected watch status off while paused, got:\n%s", m.View())
	}

	// W is inert while watching is paused.
	before := len(svc.watchCalls)
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'W'}})
	m = u.(Model)
	if cmd != nil {
		t.Fatal("expected no watch toggle cmd while paused")
	}
	if len(svc.watchCalls) != before {
		t.Fatal("expected no service call from W while paused")
	}

	// Resume restores each per-module flag from before the pause.
	svc.watchCalls = nil
	u, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	m = u.(Model)
	if m.watchPaused {
		t.Fatal("expected watching resumed after second P")
	}
	res = cmd()
	if len(svc.watchCalls) != 2 {
		t.Fatalf("expected resume to restore every module flag, got %+v", svc.watchCalls)
	}
	for _, want := range []watchCall{
		{name: "alpha", enable: true},
		{name: "beta", enable: false},
	} {
		found := false
		for _, c := range svc.watchCalls {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected resume to restore %+v, got %+v", want, svc.watchCalls)
		}
	}
	_ = res
}

func TestLive_SwapEventLineFallsBackToFingerprintPrefix(t *testing.T) {
	m := testLiveModel()
	prev := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1"}, Fingerprint: "aaaa1111aaaa1111"},
	}
	next := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1", "t2"}, Fingerprint: "bbbb2222bbbb2222"},
	}
	u, _ := m.Update(modulesMsg{modules: prev})
	m = u.(Model)
	u, _ = m.Update(modulesMsg{modules: next})
	m = u.(Model)

	want := "hotswapped alpha: aaaa1111 → bbbb2222 (2 tools)"
	if len(m.events) == 0 || m.events[len(m.events)-1].text != want {
		t.Fatalf("expected swap event %q as last event, got %+v", want, m.events)
	}
	if !strings.Contains(m.View(), want) {
		t.Fatalf("expected swap event line in view, got:\n%s", m.View())
	}
}

func TestLive_SwapEventLineUsesVersionsWhenReported(t *testing.T) {
	m := testLiveModel()
	prev := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1"}, Version: "3.4.1", Fingerprint: "aaaa1111aaaa1111"},
	}
	next := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1", "t2"}, Version: "3.5.0", Fingerprint: "bbbb2222bbbb2222"},
	}
	u, _ := m.Update(modulesMsg{modules: prev})
	m = u.(Model)
	u, _ = m.Update(modulesMsg{modules: next})
	m = u.(Model)

	want := "hotswapped alpha: 3.4.1 → 3.5.0 (2 tools)"
	if len(m.events) == 0 || m.events[len(m.events)-1].text != want {
		t.Fatalf("expected swap event %q as last event, got %+v", want, m.events)
	}
}

func TestLive_WatchStatusColumn(t *testing.T) {
	off := false
	on := true
	mods := []domain.ModuleSummary{
		{Name: "alpha", Status: domain.StatusActive, Tools: []string{"t1"}},
		{Name: "beta", Status: domain.StatusActive, Tools: []string{"t1"}, WatchStale: true},
		{Name: "gamma", Status: domain.StatusActive, Tools: []string{"t1"}, WatchBinary: &off},
		{Name: "delta", Status: domain.StatusActive, Tools: []string{"t1"}, WatchBinary: &on, WatchStale: true},
	}
	if got := watchLabel(mods[0], false); got != watchClean {
		t.Fatalf("expected clean, got %q", got)
	}
	if got := watchLabel(mods[1], false); got != watchStale {
		t.Fatalf("expected stale, got %q", got)
	}
	if got := watchLabel(mods[2], false); got != watchOff {
		t.Fatalf("expected off, got %q", got)
	}
	if got := watchLabel(mods[3], false); got != watchStale {
		t.Fatalf("expected stale for enabled module with mismatch, got %q", got)
	}
	// Global pause wins over per-module state.
	if got := watchLabel(mods[0], true); got != watchOff {
		t.Fatalf("expected off while paused, got %q", got)
	}

	m := testLiveModel()
	u, _ := m.Update(modulesMsg{modules: mods})
	m = u.(Model)
	view := m.View()
	for _, want := range []string{watchClean, watchStale, watchOff} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected watch column label %q in view, got:\n%s", want, view)
		}
	}
}
