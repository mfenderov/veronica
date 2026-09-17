# TUI Live Ops View — Design (2026-09-16)

Status: **draft for review** — iteration-1 of the Veronica TUI revamp.
No code until this spec is approved. Implementation follows via `writing-plans` + TDD.

## 1. Context

The current TUI (`internal/tui/`, ~470 lines UI + ~503 lines tests) is a static
snapshot dashboard: header (uptime/modules/tools/RAM), left module list
(name/transport pill/tool count/status dot), right inspector (status/transport/
target/tool names), deploy/edit modal, 8 keybindings. Data loads once on `Init`
and again only after an action or manual `r`. Gaps: no auto-refresh, no
health-at-a-glance, no event/log stream, no search, no tool schemas, no help
overlay, no confirm, no scrolling/mouse.

Iteration-1 priority (locked): **Live ops view — auto-refresh, event stream,
health at a glance.** Everything else (sparklines, logs, search, schemas, help,
confirm, mouse) is explicitly deferred.

## 2. Locked decisions

- Refresh: **2s tick, paused on modal/form** (tick re-arms but skips fetch while
  `modeAdd`/`modeEdit` open).
- Approach: **A — TUI-local live view with EventSource seam** (zero
  backend/protocol change, remote-safe over `RemotePodClient`, mock-testable).
  - B (new Diagnose/Supervisor/Events tools) deferred: 2–3x scope, domain-interface change.
  - C (tail `daemon.log`) rejected: same-host-only, brittle.
- Scope: `internal/tui` + `internal/tui/tui_test.go` only. **No `domain`
  changes** in iteration-1 (clean-arch preserved).

## 3. Architecture (Approach A)

Poll the existing `domain.PodService` (`Status` + `ListModules`) on a Bubble Tea
tick. Derive health client-side. Build the event pane from TUI-local
observations (`actionResultMsg` + module-list deltas + fetch errors). Hide the
delta logic behind a small `EventSource` interface so a future daemon-side feed
can plug in without touching `Update`/`View`.

```
tick(2s) → skip if modal/paused → Status + ListModules (existing cmds)
  → statusMsg/modulesMsg → update state, lastOk, deltas → events ring
  → View: header + health strip + modules/inspector + events pane + footer
```

Single-threaded `Update` serializes all messages: no races. In-flight overlap
(slow daemon > 2s) resolves to last-writer-wins; accepted for iteration-1.

## 4. Design §1 — Tick loop & state (locked)

- `type tickMsg time.Time`; `tickCmd()` re-arms via `tea.Tick(2*time.Second, …)`.
- `Init()` batches existing loads **plus** first `tickCmd()`.
- `Update` on `tickMsg`: always re-arm; skip fetch when `mode == modeAdd ||
  mode == modeEdit` or `!autoRefresh`.
- New `Model` fields:
  - `events []uiEvent` — ring buffer, cap 50.
  - `eventsSrc EventSource` — default `localEventSource{}`, injectable in tests.
  - `autoRefresh bool` — default `true`, toggled by `p`.
  - `lastOk time.Time` — last successful fetch; drives STALE display.
  - `lastErr error` — last fetch error (nil when healthy); drives OFFLINE/STALE.
- `NewModel` wires `eventsSrc: localEventSource{}` and `autoRefresh: true`.
- Stale-overwrite between overlapping polls accepted (documented, tested).

## 5. Design §2 — Health strip + event semantics

### 5.1 `uiEvent`

```go
type uiEvent struct {
    at      time.Time
    kind    string // "action" | "module" | "status"
    text    string
    isError bool
}
```

Helpers (kept small for CRAP ≤ 10): `appendEvent(e uiEvent)` (cap 50, drop
oldest), `diffModuleEvents(prev, next []domain.ModuleSummary) []uiEvent`.

### 5.2 `EventSource` seam

```go
// Deltas returns display-worthy events comparing previous and fresh lists.
// Iter-1: localEventSource diffs name/status/tool-count changes.
// Future: daemonEventSource can serve a server-side log/diagnose feed.
type EventSource interface {
    Deltas(prev, next []domain.ModuleSummary) []uiEvent
}
type localEventSource struct{}
```

`Model.eventsSrc` defaults to `localEventSource{}`. Tests inject a stub.

### 5.3 What gets recorded (spam-safe)

- `actionResultMsg` (deploy/recall/toggle/reauth/restart outcomes): **always**.
- Module deltas via `Deltas`: added / removed / `status` transition
  (`active→error` i.e. `StatusActive→StatusError`, with error text) / tool-count change. Successful polls
  with **no delta record nothing**.
- Fetch failures (`statusMsg`/`modulesMsg` with `err`): record only on
  **transition into failure and on recovery**, not every 2s tick.
- Timestamp display `HH:MM:SS` (local).

### 5.4 Health strip derivation (client-side, pure function for tests)

`deriveHealth(modules, lastErr, hasGoodData)`: `OFFLINE` (last fetch failed and no good data
yet) → `STALE last ok HH:MM:SS` (failure after good data; `lastOk` passed separately for display) → `DEGRADED n error`
(any `StatusError`) → `HEALTHY` otherwise. Strip format:

```
● HEALTHY • refreshed 12:04:02 • auto ON (p to pause)
● DEGRADED 1 error • refreshed 12:04:02 • auto ON
● STALE last ok 12:03:10 • auto ON
```

Reuse existing `styles.go` slate/sky palette (`textSuccess`/`textDanger`/
`textMuted`); no new colors.

## 6. Design §3 — Layout & keys

- Keep the two-pane modules/inspector layout untouched. Add two thin rows:
  1. Health strip directly under the header (one line, truncates on narrow).
  2. `EVENTS (last N)` pane above the footer, height 5 total (border + up to 3
     rows + title). Newest at bottom (tail-like). Oldest drops past cap 50;
     only the newest visible rows render. **No scrolling in iteration-1.**
- Small-terminal behavior: if `height < 15`, events pane collapses to 1 row;
  if `height < 10`, existing "too small" guard wins.
- New key: **`p` toggles `autoRefresh`** (dashboard mode only). Footer gains
  `[p] Pause/Resume`; `r` stays as manual refresh (same delta rules, no extra
  event). No other key changes — `e`/`a`/`d`/`A`/`R`/`Space`/`q` untouched.
- Modal behavior: tick re-arms but issues no fetch; form input focus/keys
  unchanged; on submit/esc the next tick resumes normally.

## 7. Design §4 — Error handling & testing

- Fetch error: keep last good modules/status on screen, show existing footer
  error line, health strip flips to `STALE`/`OFFLINE`. No modal, no crash.
- Overlapping polls: last-writer-wins, accepted; a sequence guard is a
  named future, not iteration-1.
- Tests (TDD, extend `mockPodService` + stub `EventSource`):
  - tick re-arms and triggers loads when dashboard + auto on;
  - tick skips fetch in `modeAdd`/`modeEdit` and when paused;
  - `p` toggles `autoRefresh` and footer label;
  - deltas recorded (add/remove/status change), no-delta polls silent;
  - ring cap: 60 appends → 50 kept, newest last;
  - error transition/recovery events (no per-tick spam);
  - `deriveHealth` cases (healthy/degraded/stale/offline);
  - `View` contains `EVENTS` + health label; small-terminal guard intact.
- Gates per AGENTS.md: `go test -race -shuffle=on ./...`, `make crap`
  (CRAP ≤ 10 — keep `Update`/`View` helpers small: `appendEvent`,
  `diffModuleEvents`, `deriveHealth`, `renderEventsPane`, `renderHealthStrip`),
  `make lint`. No domain changes, so `goarch` unaffected. Conventional commit.

## 8. Files touched

- `internal/tui/model.go` — `uiEvent`, `EventSource`, new `Model` fields, `tickCmd`.
- `internal/tui/update.go` — `tickMsg` routing, modal/pause skip, `p` toggle,
  delta + error-transition recording.
- `internal/tui/views.go` — health strip + events pane + footer key.
- `internal/tui/tui_test.go` — cases above.
- Explicitly **not** touched: `internal/domain/*`, transport, meta, CLI wiring.

## 9. Acceptance criteria

1. Idle TUI refreshes modules/status every ~2s without keypress.
2. Opening deploy/edit modal freezes fetches; closing resumes.
3. `p` pauses/resumes; footer + strip reflect state.
4. Deploy/toggle/recall/reauth outcomes appear in EVENTS within one tick.
5. Module crash (status → ERROR) surfaces as DEGRADED + event without action.
6. Daemon down shows STALE/OFFLINE with last good data, recovers cleanly.
7. All repo gates green; no backend/protocol change.

## 10. Deferred (not iteration-1)

Sparklines/history graphs, daemon-log tail, search/filter, tool JSON schemas,
help overlay, destructive-action confirm, mouse/scroll, sequence guards,
backend `Diagnose`/`Events` tools (Approach B).
