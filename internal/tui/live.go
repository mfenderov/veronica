package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/domain"
)

// tickInterval is the auto-refresh period for the live-ops refresh loop.
var tickInterval = 2 * time.Second

// tickMsg fires on every refresh-loop tick to re-poll daemon status.
type tickMsg time.Time

// tickCmd schedules the next refresh-loop tick.
func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// uiEvent is a single entry in the live-ops event stream.
type uiEvent struct {
	at   time.Time
	kind string
	text string
}

// Event kinds recorded in the event stream pane.
const (
	eventKindResult  = "result"
	eventKindAdded   = "added"
	eventKindRemoved = "removed"
	eventKindStatus  = "status"
	eventKindError   = "error"
)

// maxEvents caps the retained event stream; oldest entries are dropped.
const maxEvents = 50

// maxVisibleEvents caps how many stream rows the EVENTS pane shows.
const maxVisibleEvents = 3

// appendEvent records ev, keeping at most maxEvents entries (drops oldest).
func (m Model) appendEvent(ev uiEvent) Model {
	m.events = append(m.events, ev)
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
	return m
}

// EventSource computes the module deltas observed between two snapshots.
type EventSource interface {
	Deltas(prev, next []domain.ModuleSummary) []uiEvent
}

// localEventSource diffs module snapshots in the TUI process.
type localEventSource struct{}

// Deltas reports added/removed modules plus status and tool-count transitions.
func (localEventSource) Deltas(prev, next []domain.ModuleSummary) []uiEvent {
	prevBy := indexByName(prev)
	nextBy := indexByName(next)
	var out []uiEvent
	for _, n := range next {
		p, ok := prevBy[n.Name]
		if !ok {
			out = append(out, newEvent(eventKindAdded, "module added: "+n.Name))
			continue
		}
		if p.Status != n.Status {
			out = append(out, newEvent(eventKindStatus,
				fmt.Sprintf("module %s status: %s -> %s", n.Name, p.Status, n.Status)))
			continue
		}
		if len(p.Tools) != len(n.Tools) {
			out = append(out, newEvent(eventKindStatus,
				fmt.Sprintf("module %s tools: %d -> %d", n.Name, len(p.Tools), len(n.Tools))))
		}
	}
	for _, p := range prev {
		if _, ok := nextBy[p.Name]; !ok {
			out = append(out, newEvent(eventKindRemoved, "module removed: "+p.Name))
		}
	}
	return out
}

func indexByName(mods []domain.ModuleSummary) map[string]domain.ModuleSummary {
	out := make(map[string]domain.ModuleSummary, len(mods))
	for _, m := range mods {
		out[m.Name] = m
	}
	return out
}

func newEvent(kind, text string) uiEvent {
	return uiEvent{at: time.Now(), kind: kind, text: text}
}

// Health states shown in the live-ops health strip.
const (
	healthHealthy  = "HEALTHY"
	healthDegraded = "DEGRADED"
	healthStale    = "STALE"
	healthOffline  = "OFFLINE"
)

// deriveHealth reports the gateway health snapshot for the strip.
func deriveHealth(modules []domain.ModuleSummary, lastErr error, hasGoodData bool) string {
	if lastErr != nil && !hasGoodData {
		return healthOffline
	}
	if lastErr != nil {
		return healthStale
	}
	for _, m := range modules {
		if m.Status == domain.StatusError {
			return healthDegraded
		}
	}
	return healthHealthy
}

// renderHealthStrip renders the one-line health summary below the header.
func renderHealthStrip(modules []domain.ModuleSummary, lastErr error, hasGoodData bool) string {
	healthy := 0
	for _, m := range modules {
		if m.Status == domain.StatusActive {
			healthy++
		}
	}
	label := "Health: " + deriveHealth(modules, lastErr, hasGoodData)
	if lastErr != nil {
		label += " | " + lastErr.Error()
	}
	label += fmt.Sprintf(" | %d/%d healthy", healthy, len(modules))
	return headerInfo.Render(label)
}

// renderEventsPane renders the capped event stream (newest last).
func renderEventsPane(events []uiEvent) string {
	var b strings.Builder
	b.WriteString(paneTitle.Render("EVENTS") + "\n")
	if len(events) == 0 {
		b.WriteString(textSubtle.Render("no events yet"))
		return b.String()
	}
	visible := events
	if len(visible) > maxVisibleEvents {
		visible = visible[len(visible)-maxVisibleEvents:]
	}
	for _, ev := range visible {
		fmt.Fprintf(&b, "%s %s\n", ev.at.Format("15:04:05"), ev.text)
	}
	return strings.TrimRight(b.String(), "\n")
}
