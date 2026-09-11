# CLAUDE.md

Guidelines and architecture reference for working on Veronica.

---

## Overview

`veronica` is a lightweight, autonomous local Model Context Protocol (MCP) gateway and tool multiplexer written in Go (Go 1.26+). It provides a unified gateway on `:9090/sse` that harmonizes tool access across Copilot, OpenCode, Pi, Claude Code, and Cursor without Docker Desktop overhead.

---

## Core Architecture (Clean Architecture / Hexagonal)

Veronica enforces a strict dependency rule verified at build-time by `tools/goarch`:

- **`internal/domain`**: Pure business domain models, ports, and errors (`Module`, `Tool`, `AuthToken`, `DownstreamClient`, `AuthStore`, `TokenProvider`). **Zero external dependencies.**
- **`internal/registry`**: Thread-safe catalog router managing active and errored modules, tool aggregation, and list-changed change notifications.
- **`internal/auth`**: OAuth 2.0 lifecycle manager, RFC 7636 PKCE S256 code verifier/challenge generator, local callback server (`:9091/oauth/callback` or custom ports/paths), file token store (`auth.json`), and OpenCode token importer.
- **`internal/transport`**:
  - `DownstreamAdapter`: Connects to downstream stdio processes or remote HTTP/SSE endpoints with bearer token injection.
  - `UpstreamServer`: Multiplexes legacy SSE (`/sse`, `/message`) and modern Streamable HTTP (`/mcp`, `/sse`) for upstream AI clients. Exposes full JSON input schemas for all tools.
- **`internal/meta`**: Meta-tools implementation (`veronica_list_modules`, `veronica_deploy_module`, `veronica_recall_module`, `veronica_toggle_module`, `veronica_reauth_module`, `veronica_status`).
- **`internal/tui`**: Interactive terminal dashboard powered by Bubble Tea & Lipgloss.
- **`cmd/veronica`**: CLI entrypoint with Cobra (`serve`, `list`, `tui`, `version`).

---

## Development & Quality Commands

```bash
# Test execution (race detector + randomized test execution order)
make test

# Full End-to-End lifecycle test suite
make test-e2e

# Strict CRAP score quality gate (max 10 across all non-test functions)
make crap

# GolangCI-Lint (forbidigo, errorlint, bodyclose, perfsprint, recvcheck)
make lint

# Auto-fixers & formatting
make fmt
make fix

# Dependency hygiene
make tidy

# Build and install to ~/bin/veronica
make install
```

---

## Engineering Rules & Quality Gates

1. **Test-Driven Development (TDD)**:
   - Always write failing tests first before writing implementation code.
   - Use `t.Context()` for all context-aware tests.
   - Run the full test suite with `-race -shuffle=on`.

2. **CRAP Score Gate ($\le 10$)**:
   - Every function's CRAP score ($C = \text{complexity}^2 \times (1 - \text{coverage})^3 + \text{complexity}$) must be $\le 10$.
   - Keep functions small, focused, and well-covered.

3. **No Hardcoded Vendor Secrets or Provider-Specific Logic**:
   - All OAuth credentials, scopes, and custom query parameters (`audience`, `prompt`) must be injected via `domain.OAuthClientConfig.AuthParams` and `config.yaml`.
   - Never hardcode vendor-specific URLs or client secrets in `internal/` packages.

4. **Conventional Commits & Automated Releases**:
   - All commits must follow Conventional Commits (`feat:`, `fix:`, `chore:`, etc.).
   - Verified via `cog check --from-latest-tag`.
   - Pushes to `main` automatically run CI, bump version tags via Cocogitto, and publish releases on GitHub.
