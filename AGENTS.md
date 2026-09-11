# AGENTS.md

Autonomous agent guide and engineering instructions for Veronica.

---

## Agent Identity & Mission

You are working on **`veronica`**, an autonomous local Model Context Protocol (MCP) gateway and tool multiplexer written in Go. Veronica serves as a single unified tool supervisor across all harnesses (Copilot, OpenCode, Pi, Claude Code, Cursor) over `:9090/sse`.

---

## Core Rules & Invariants

1. **Test-Driven Development (TDD)**:
   - Always write failing tests first before implementation.
   - Run tests with `-race -shuffle=on`. Use `t.Context()` for context-aware tests.
   - Run the full test suite (`go test -v -race -shuffle=on ./...` or `make test`).

2. **CRAP Score Gate ($\le 10$)**:
   - Every function's CRAP score must be $\le 10$. Enforced via `make crap`.
   - Never skip or exclude business packages from CRAP checks.

3. **Clean Architecture / Hexagonal Ports**:
   - `internal/domain` has **zero external dependencies**. Verified by `goarch` test.
   - Adapters in `internal/transport`, `internal/auth`, and `internal/meta` depend only inward on `internal/domain`.

4. **No Hardcoded Vendor Secrets or Provider-Specific Logic**:
   - All OAuth credentials, scopes, and custom query parameters (`audience`, `prompt`) must be injected via `domain.OAuthClientConfig.AuthParams` and `config.yaml`.
   - Never hardcode vendor-specific URLs or client secrets in `internal/` packages.

5. **Conventional Commits & Automated Release Pipeline**:
   - All commits must follow Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, etc.).
   - Verified via `cog check --from-latest-tag`.
   - Every push to `main` runs CI and automatically releases on GitHub via Cocogitto.

---

## Essential Commands

```bash
make test        # Run unit and integration tests with race detector and test shuffle
make test-e2e    # Run end-to-end integration test suite
make crap        # Check CRAP complexity and coverage score (max 10)
make lint        # Run golangci-lint
make fmt         # Format Go code
make fix         # Modernize Go idioms (go fix)
make tidy        # Check dependency cleanliness
make install     # Compile binary and install to ~/bin/veronica
```

---

## Key File Locations

- `cmd/veronica/main.go`: CLI entrypoint (Cobra commands: `serve`, `list`, `tui`, `version`).
- `internal/domain/`: Pure business entities (`module.go`, `auth.go`, `interfaces.go`).
- `internal/transport/`: Downstream MCP client (`downstream.go`) and upstream gateway server (`upstream.go`).
- `internal/auth/`: OAuth manager, RFC 7636 PKCE S256, callback server, token store (`oauth.go`, `callback.go`, `store.go`).
- `internal/registry/`: Dynamic module and tool registry (`registry.go`).
- `internal/meta/`: Veronica meta-tools (`tools.go`).
- `internal/tui/`: Bubble Tea TUI dashboard (`model.go`, `update.go`, `views.go`, `styles.go`).
- `e2e/gateway_e2e_test.go`: Full lifecycle end-to-end test suite.
- `~/.config/veronica/config.yaml`: User configuration file.
- `~/.config/veronica/auth.json`: Token persistence file.
