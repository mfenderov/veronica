# Veronica Observability & Resilience Design Specification

**Date:** 2026-09-17  
**Status:** Approved & Implemented  
**Target Release:** v0.7.0  

---

## 1. Overview & Motivation

Veronica multiplexes downstream MCP tools (Atlassian, Slack, Mark42, Context7, MarkItDown) over a single gateway endpoint. As usage grows across multiple AI harnesses (Copilot, Cursor, Claude Code), two operational needs emerged:

1. **Downstream Transient Network / Edge Proxy Failures**:
   External downstream endpoints (such as Atlassian's Edge reverse proxy in AWS) periodically produce transient `502 Bad Gateway`, `503 Service Unavailable`, `504 Gateway Timeout`, or `429 Too Many Requests` responses.
2. **Gateway Execution Opacity**:
   When an AI agent calls a tool through Veronica, execution was previously opaque without request-level latency or outcome metrics visible to the operator.

This specification introduces:
- **Transparent Downstream Retry Middleware**: An `http.RoundTripper` that retries retryable status codes and network errors using jittered exponential backoff.
- **In-Memory Tool Call Tracing**: A memory-bounded ring buffer recording execution metrics (ID, timestamp, module, tool name, latency, outcome).
- **TUI Traces View**: A toggleable bottom pane in the Bubble Tea TUI (`[t] Traces`) showing live execution traces alongside the existing gateway event log.

---

## 2. Core Decisions & Architectural Invariants

1. **Zero External Dependencies in Domain**:
   - `domain.ToolTrace` and `domain.TraceRecorder` reside in `internal/domain` with standard library imports only.
   - Clean Architecture dependency hierarchy is strictly preserved.
2. **Transparent Standard Library Middleware**:
   - Resilience is implemented via a standard `http.RoundTripper` wrapper (`RetryRoundTripper`).
   - Works with `mcp-go` client without modifying external package code.
3. **Bounded In-Memory Footprint**:
   - `TraceRecorder` uses a fixed-capacity circular ring buffer (default 100 entries).
   - Oldest traces are dropped when capacity is reached.
4. **Non-Blocking Observability**:
   - Tracing is an auxiliary concern: any unexpected error in trace recording must never fail the underlying tool execution.
5. **Quality Gates Enforced**:
   - Every function CRAP score $\le 10$.
   - Full test suite passes with `go test -race -shuffle=on ./...`.
   - `golangci-lint` passes with zero issues.

---

## 3. Downstream HTTP Retry Transport (`internal/transport/retry.go`)

### 3.1 RetryRoundTripper Contract
- Config: `MaxRetries` (default 2, total 3 attempts), `InitialBackoff` (100ms), `MaxBackoff` (1s), `BackoffFactor` (2.0).
- Conditions: status codes `429`, `502`, `503`, `504`, or temporary network errors/resets.
- Preserves request body replay via `req.GetBody()`.
- Aborts immediately if upstream client cancels context (`req.Context().Done()`).

---

## 4. Tool Trace Domain & Ring Buffer (`internal/domain/trace.go`, `internal/registry/traces.go`)

- `domain.ToolTrace`: ID, Timestamp, ModuleName, ToolName, Duration, IsError, ErrorMsg.
- `domain.TraceRecorder` interface: `Record(trace ToolTrace)`, `Recent(limit int) []ToolTrace`.
- `internal/registry/traces.go`: Thread-safe `RingBufferTraceRecorder` with capacity 100.
- `Registry.CallTool` measures start-to-finish latency and records outcome.

---

## 5. PodService & Meta Tool

- `domain.PodService` interface extended with `RecentTraces(ctx context.Context, limit int) ([]ToolTrace, error)`.
- Implemented by local handler and `RemotePodClient`.
- Custom meta-tool `veronica_traces` registered in upstream server.

---

## 6. TUI Traces View & Toggle

- TUI bottom pane mode: `bottomPaneEvents` vs `bottomPaneTraces`.
- Toggle key `[t]` in `handleActionKey` switches view.
- Footer keys updated to include `[t] Traces`.
- Traces view renders formatted rows with duration and status badges (`[OK]` / `[ERR]`).
