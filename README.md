# 🛰️ Veronica

**Autonomous Local MCP Gateway & Dynamic Tool Pod**

[![CI](https://github.com/mfenderov/veronica/actions/workflows/ci.yml/badge.svg)](https://github.com/mfenderov/veronica/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.26-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

`veronica` is a lightweight, agent-driven local Model Context Protocol (MCP) gateway and tool multiplexer written in Go. It eliminates tool inflation and fragmented configuration across multiple AI coding harnesses (**Copilot / VS Code**, **OpenCode**, **Pi**, **Claude Code**, and **Cursor**) by serving as a single, unified point of tool administration.

---

## Why Veronica?

1. **Autonomous Tool Pod (Agent-Managed)**:
   Veronica exposes built-in **meta-tools** (`veronica_list_modules`, `veronica_deploy_module`, `veronica_recall_module`, `veronica_toggle_module`, `veronica_reauth_module`, `veronica_status`). Connected AI assistants can discover, mount, retire, and refresh tools on demand without human configuration edits.
2. **Harmonized Harnesses**:
   Copilot, VS Code, OpenCode, Pi, Claude Code, and Cursor connect to a single SSE/Streamable HTTP endpoint (`http://localhost:9090/sse`). Any tool mounted in Veronica is immediately available across all harnesses.
3. **No Docker Desktop**:
   Zero container overhead. Spawns local CLI MCP processes natively and proxies remote HTTP/SSE servers with sub-millisecond dispatch and ~2MB RAM footprint.
4. **Centralized OAuth, PKCE & Secrets**:
   Includes an automated local OAuth callback listener (`:9091/oauth/callback` or custom callback ports such as `:3118/callback`), native RFC 7636 PKCE S256 support, RFC 7591 Dynamic Client Registration (DCR), and automatic token import from OpenCode (`~/.local/share/opencode/mcp-auth.json`). Absorbs ad-hoc shell and Node bridge scripts into a single Go binary.
5. **Interactive Charm TUI**:
   Full terminal dashboard (`veronica tui`) powered by Bubble Tea & Lipgloss with instant module toggling, in-place editing, and 1-key force re-authentication.
6. **Merciless Simplification & Quality**:
   - $\text{CRAP} \le 10$ enforced across the entire codebase with zero directory exclusions.
   - Automated compile-time DDD architecture verification (`goarch`).
   - Strict race detector and linter compliance.

---

## Installation

### 1. Homebrew (macOS)
```bash
brew install mfenderov/tap/veronica
# Or manually tap first:
# brew tap mfenderov/tap && brew install veronica
```

### 2. Go Toolchain (Requires Go 1.26+)
```bash
go install github.com/mfenderov/veronica/cmd/veronica@latest
```
*Ensure `$(go env GOPATH)/bin` (typically `~/go/bin`) is in your `$PATH`.*

### 3. One-Liner Script (macOS / Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/mfenderov/veronica/main/install.sh | bash
```
*Automatically detects OS (`darwin` / `linux`) and architecture (`arm64` / `amd64`), downloads the latest release tarball, and installs `veronica` to `~/.local/bin` or `~/bin`.*

### 4. GitHub Releases (Pre-built Binaries)
Download pre-built universal binary archives and `checksums.txt` for macOS (`darwin/arm64`, `darwin/amd64`) and Linux (`linux/amd64`, `linux/arm64`) from the [Releases](https://github.com/mfenderov/veronica/releases) page.

### 5. Build from Source
```bash
git clone https://github.com/mfenderov/veronica.git
cd veronica
make install  # Compiles binary and installs to ~/bin/veronica
```

---

## Architecture

```text
┌────────────────────────────────────────────────────────┐
│ AI Clients (Copilot, OpenCode, Pi, Claude Code, Cursor)│
└───────────────────────────┬────────────────────────────┘
                            │ Single Endpoint: http://localhost:9090/sse
                            ▼
┌────────────────────────────────────────────────────────┐
│ Veronica Gateway Daemon (:9090)                        │
│                                                        │
│  - 🌟 Meta-Tools (`veronica_deploy_module`, etc.)      │
│  - 🔐 Central Auth & OAuth Engine (:9091)              │
│  - 🔀 Thread-Safe Router & Dynamic Catalog             │
│  - 🔌 Stdio Supervisor & Remote SSE/HTTP Client        │
└──────┬────────────────────────────┬────────────────────┘
       ▼                            ▼
 [Local Stdio MCPs]         [Remote HTTP/SSE MCPs]
 - Node CLIs (npx)          - Streamable HTTP
 - Python CLIs (uvx)        - Server-Sent Events (SSE)
 - Native Go/Rust binaries  - OAuth 2.0 / Bearer Auth
```

---

## Universal MCP Support

Veronica is completely agnostic to downstream implementations and supports any MCP-compliant server:

- **Local Stdio MCPs**: Any executable command (`npx`, `uvx`, custom binaries, scripts) communicating over standard JSON-RPC. Veronica supervises processes, monitors memory, and restarts failed instances.
- **Remote HTTP & SSE MCPs**: Connects to remote endpoints over both modern Streamable HTTP (POST) and legacy Server-Sent Events (GET), automatically injecting dynamic OAuth Bearer tokens and custom headers.
- **On-Demand Toggling**: Keep modules configured but paused (`disabled: true`). Activate them in seconds via the TUI or when your agent calls `veronica_toggle_module`, keeping your prompt context small and fast.

---

## Interactive TUI Dashboard

Launch the interactive terminal user interface anytime:

```bash
veronica tui
```

### Keyboard Shortcuts

#### Dashboard View

| Shortcut | Action | Description |
| :--- | :--- | :--- |
| **`↑` / `↓`** or **`j` / `k`** | **Navigate** | Move cursor between configured MCP modules |
| **`Space`** | **Toggle** | Enable or disable the selected module on the fly |
| **`e`** | **Edit** | Edit target command/URL or transport in-place |
| **`A`** or **`Ctrl+A`** | **Re-auth** | Force immediate OAuth token refresh and reconnect client |
| **`a`** | **Deploy** | Open modal dialog to deploy a new MCP module |
| **`d`** | **Recall** | Gracefully unmount and remove the selected module |
| **`r`** | **Refresh** | Re-query daemon metrics and reload module catalog |
| **`q`** or **`Ctrl+C`** | **Quit** | Exit the TUI |

#### Modal Dialog Controls (Deploy & Edit)

| Shortcut | Action | Description |
| :--- | :--- | :--- |
| **`Tab`** | **Switch Field** | Toggle focus between Module Name and Command / URL input fields |
| **`Ctrl+T`** | **Toggle Transport** | Flip transport mode between `stdio` and `http` |
| **`Enter`** | **Submit** | Confirm and deploy the module or save configuration edits |
| **`Esc`** | **Cancel** | Close the modal dialog and return to the main dashboard |

---

## Meta-Tools (Agent Self-Administration)

Veronica equips connected AI agents with tools to manage their own tool environment:

| Meta-Tool | Purpose | Parameters |
| :--- | :--- | :--- |
| `veronica_list_modules` | List all active and mounted modules and their tool schemas | None |
| `veronica_deploy_module` | Mount a new stdio CLI or remote HTTP/SSE MCP on demand | `name`, `transport`, `command`/`url`, `args`, `env`, `headers` |
| `veronica_recall_module` | Gracefully stop and unmount an active module | `name` |
| `veronica_toggle_module` | Enable or disable an MCP module live | `name`, `enable` |
| `veronica_reauth_module` | Force immediate OAuth token renewal and header injection | `name` |
| `veronica_status` | Gateway health, memory footprint, uptime, active tool counts | None |

---

## CLI Usage

```bash
# Start the gateway daemon (SSE / HTTP server on :9090)
veronica serve

# Start with a custom configuration file path
veronica serve --config ~/.config/veronica/config.yaml

# Run directly as a stdio MCP server (for single-client subprocess mode)
veronica serve --stdio

# Launch the interactive Charm TUI dashboard
veronica tui

# Connect TUI to a custom gateway endpoint
veronica tui --endpoint http://localhost:9090/sse

# List configured modules and target endpoints
veronica list

# Diagnose config, credentials, health, and missing local binaries
veronica doctor

# Show Veronica version
veronica version
```

### Protocols: Streamable (preferred) vs SSE (legacy) vs stdio

- **Streamable HTTP (preferred):** modern MCP transport served by the daemon at `/mcp` (also answers `POST /sse` and `POST /`). Point new clients here.
- **SSE legacy:** `GET /sse` event stream + `POST /message` for older clients (OpenCode remote, existing configs). Kept for backward compatibility.
- **stdio:** `veronica serve --stdio` runs as a single-client subprocess over stdin/stdout (no TCP port). Use for Claude Code / CLI `mcp add` local modes.

### First-run prerequisites

On first `serve`, Veronica warns (to stderr, never fails the daemon) when an enabled
`stdio` module's `command` is not found in `$PATH`, with an install hint:

```text
Missing prerequisites:
- markitdown: command "uvx" not found. Install with: brew install uv (then uvx markitdown-mcp).
```

Run `veronica doctor` anytime to re-check config, credentials, health, and binaries.

## End-to-End Walkthrough: OpenCode + Mark42 via Veronica

Here is a complete, real-world example showing how to mount a memory tool (`mark42`) into Veronica and query it from **OpenCode** with zero manual tool configuration in OpenCode:

### 1. Launch the Veronica Daemon
```bash
veronica serve
# [veronica] 🛰️ Veronica gateway listening on :9090/sse
```
*(Or keep it running 24/7 in the background as a macOS LaunchAgent).*

### 2. Mount Mark42 into Veronica
Mount your MCP server dynamically using the TUI (`veronica tui` -> press `a`), ask your AI assistant to call `veronica_deploy_module`, or add it to `~/.config/veronica/config.yaml` before starting the daemon:

```yaml
modules:
  mark42:
    name: mark42
    transport: stdio
    command: /opt/homebrew/bin/mark42-server
```

Veronica immediately spawns Mark42, introspects its capabilities, and mounts its tools (`mark42_search_nodes`, `mark42_read_graph`, etc.) into the live catalog. It also registers un-prefixed aliases (`search_nodes`) for seamless backward compatibility and broadcasts an MCP `notifications/tools/list_changed` event to all connected clients.

### 3. Connect OpenCode to Veronica (Once)
In `~/.config/opencode/opencode.jsonc`, point OpenCode to Veronica:

```jsonc
{
  "mcp": {
    "veronica": {
      "type": "remote",
      "url": "http://localhost:9090/sse"
    }
  }
}
```
*You never have to edit OpenCode's configuration again when adding, updating, or removing tools.*

### 4. Query Mark42 from OpenCode
Open your OpenCode chat and ask:

> *"What architecture decisions do we have recorded for our Go microservices?"*

**Behind the Scenes:**
1. OpenCode issues an MCP tool call: `mark42_search_nodes(query="Go microservices")` (or `search_nodes(...)`).
2. Veronica catches the request on `:9090/sse` and routes it over stdio JSON-RPC to the supervised `mark42-server` process.
3. Mark42 queries its local knowledge graph and returns the entities.
4. Veronica delivers the payload back to OpenCode's context window with sub-millisecond dispatch.

---

## Client Configurations

Veronica supports **Streamable HTTP** (preferred, `/mcp`), **SSE legacy** (`GET /sse` + `POST /message`),
and **stdio** (`veronica serve --stdio`, direct subprocess, no TCP). Daemon mode (`veronica serve`) exposes
both HTTP transports on the same port; stdio mode serves one client over stdin/stdout.

### 1. VS Code

In workspace `.vscode/mcp.json` or user profile `mcp.json` (`~/Library/Application Support/Code/User/mcp.json` on macOS, `~/.config/Code/User/mcp.json` on Linux):

```json
{
  "servers": {
    "veronica": {
      "type": "http",
      "url": "http://localhost:9090/sse"
    }
  }
}
```

*Or via local stdio:*
```json
{
  "servers": {
    "veronica": {
      "type": "stdio",
      "command": "veronica",
      "args": ["serve", "--stdio"]
    }
  }
}
```

### 2. GitHub Copilot CLI

In `~/.copilot/mcp-config.json` (or workspace `.mcp.json`):

```json
{
  "mcpServers": {
    "veronica": {
      "type": "http",
      "url": "http://localhost:9090/sse",
      "tools": ["*"]
    }
  }
}
```

*Or via local stdio:*
```json
{
  "mcpServers": {
    "veronica": {
      "type": "stdio",
      "command": "veronica",
      "args": ["serve", "--stdio"],
      "tools": ["*"]
    }
  }
}
```

### 3. OpenCode

In `~/.config/opencode/opencode.jsonc`:

```jsonc
{
  "mcp": {
    "veronica": {
      "type": "remote",
      "url": "http://localhost:9090/sse"
    }
  }
}
```

*Or via local stdio:*
```jsonc
{
  "mcp": {
    "veronica": {
      "type": "local",
      "command": ["veronica", "serve", "--stdio"]
    }
  }
}
```

### 4. Pi

In `~/.pi/agent/mcp.json`:

```json
{
  "mcpServers": {
    "veronica": {
      "url": "http://localhost:9090/sse"
    }
  }
}
```

*Or via local stdio:*
```json
{
  "mcpServers": {
    "veronica": {
      "command": "veronica",
      "args": ["serve", "--stdio"]
    }
  }
}
```

### 5. Claude Code

Connect to Veronica with the `claude mcp` CLI:

```bash
# Connect to background daemon via SSE
claude mcp add --transport sse veronica http://localhost:9090/sse

# Or run directly via local stdio
claude mcp add veronica -- veronica serve --stdio
```

*Or via project `.mcp.json` / `~/.claude.json`:*
```json
{
  "mcpServers": {
    "veronica": {
      "type": "sse",
      "url": "http://localhost:9090/sse"
    }
  }
}
```

### 6. Cursor

In `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (workspace):

```json
{
  "mcpServers": {
    "veronica": {
      "url": "http://localhost:9090/sse"
    }
  }
}
```

*Or via local stdio:*
```json
{
  "mcpServers": {
    "veronica": {
      "type": "stdio",
      "command": "veronica",
      "args": ["serve", "--stdio"]
    }
  }
}
```

---

## Zero-Boilerplate Configuration (`~/.config/veronica/config.yaml`)

Veronica manages tool definitions cleanly. You only need to define your downstream modules:

```yaml
server:
  addr: :9090

modules:
  # Local stdio MCP
  mark42:
    transport: stdio
    command: /opt/homebrew/bin/mark42-server

  # Remote MCPs
  atlassian:
    transport: http
    url: https://mcp.atlassian.com/v1/mcp

  slack:
    transport: http
    url: https://mcp.slack.com/mcp
```

### Automated OAuth & Secrets Lifecycle
- **Automatic Defaults & Discovery**: Endpoints, Dynamic Client Registration (RFC 7591), and required scopes are discovered from downstream metadata (`/.well-known/oauth-protected-resource`) or seeded by Veronica defaults.
- **Interactive Consent**: Triggering re-auth in the TUI (or calling `veronica_reauth_module`) automatically opens your browser for OAuth 2.0 PKCE consent and captures the callback on a local listener.
- **Transparent Token Management**: Tokens, refresh cycles, and header injections are persisted in `~/.config/veronica/auth.json`. You never have to manually author raw credentials, scopes, or tokens.

---

## Quality & Testing Gates

```bash
# Run unit and integration tests with race detector and test shuffle
make test

# Run full end-to-end integration test suite
make test-e2e

# Run strict CRAP score quality gate (max 10 across all functions)
make crap

# Run golangci-lint
make lint

# Run code formatters and modern Go fixers
make fmt
make fix

# Verify module dependencies
make tidy

# Compile binary and install to ~/bin/veronica
make install
```
