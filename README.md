# 🛰️ Veronica

**Autonomous Local MCP Gateway & Dynamic Tool Pod**

[![CI](https://github.com/mfenderov/veronica/actions/workflows/ci.yml/badge.svg)](https://github.com/mfenderov/veronica/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.24-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

> *"Veronica, give me a hand!"* — Tony Stark

`veronica` is a lightweight, agent-driven local Model Context Protocol (MCP) gateway and tool multiplexer written in Go. It eliminates tool inflation and fragmented configuration across multiple AI coding harnesses (**Copilot / VS Code**, **OpenCode**, and **Pi**) by serving as a single, unified point of tool administration.

---

## Why Veronica?

1. **Autonomous Tool Pod (Agent-Managed)**:
   Veronica exposes built-in **meta-tools** (`veronica_list_modules`, `veronica_deploy_module`, `veronica_recall_module`, `veronica_toggle_module`, `veronica_reauth_module`, `veronica_status`). Connected AI assistants can discover, mount, retire, and refresh tools on demand without human configuration edits.
2. **Harmonized Trio**:
   Copilot, OpenCode, and Pi connect to a single SSE/Streamable HTTP endpoint (`http://localhost:9090/sse`). Any tool mounted in Veronica is immediately available across all harnesses.
3. **No Docker Desktop**:
   Zero container overhead. Spawns local CLI MCP processes natively and proxies remote HTTP/SSE servers with sub-millisecond dispatch and ~2MB RAM footprint.
4. **Centralized OAuth & Secrets**:
   Includes a local OAuth callback listener (`:9091/oauth/callback`) and an automated token refresh worker, absorbing ad-hoc shell and Node bridge scripts.
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
```

### 2. Go Toolchain
```bash
go install github.com/mfenderov/veronica/cmd/veronica@latest
```

### 3. One-Liner Script (macOS / Linux)
```bash
curl -fsSL https://raw.githubusercontent.com/mfenderov/veronica/main/install.sh | bash
```

### 4. GitHub Releases (Pre-built Binaries)
Download pre-built universal binary archives for macOS (`darwin/arm64`, `darwin/amd64`) and Linux (`linux/amd64`, `linux/arm64`) from the [Releases](https://github.com/mfenderov/veronica/releases) page.

---

## Architecture

```text
┌────────────────────────────────────────────────────────┐
│ AI Clients (Copilot / VS Code, OpenCode, Pi)           │
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
└──────┬────────────┬─────────────┬─────────────┬────────┘
       ▼            ▼             ▼             ▼
   [mark42]    [markitdown]    [atlassian]   [slack]
   [context7]  [honeycomb (on-demand)]
```

---

## Interactive TUI Dashboard

Launch the interactive terminal user interface anytime:

```bash
veronica tui
```

### Keyboard Shortcuts

| Shortcut | Action | Description |
| :--- | :--- | :--- |
| **`↑` / `↓`** or **`j` / `k`** | **Navigate** | Move cursor between configured MCP modules |
| **`Space`** | **Toggle** | Enable or disable the selected module on the fly |
| **`e`** | **Edit** | Edit target command/URL or transport in-place |
| **`A`** or **`Ctrl+A`** | **Re-auth** | Force immediate OAuth token refresh and reconnect client |
| **`a`** | **Deploy** | Open modal dialog to deploy a new MCP module (`Ctrl+T` to flip transport) |
| **`d`** | **Recall** | Gracefully unmount and remove the selected module |
| **`r`** | **Refresh** | Re-query daemon metrics and reload module catalog |
| **`q`** or **`Ctrl+C`** | **Quit** | Exit the TUI |

---

## Active & Managed Modules

| Module | Transport | Source | Status | Tools Exposed |
| :--- | :--- | :--- | :--- | :--- |
| **`mark42`** | stdio | `/opt/homebrew/bin/mark42-server` | Active | Persistent memory graph, session recall (`mark42_*`) |
| **`atlassian`** | http | `https://mcp.atlassian.com/v2/mcp` | Active | Jira issues, JQL, Confluence pages & search (`atlassian_*`) |
| **`slack`** | http | `https://mcp.slack.com/mcp` | Active | Channels, threads, messages, user profiles (`slack_*`) |
| **`markitdown`** | stdio | `uvx markitdown-mcp==0.0.1a4` | Active | Document/media to markdown conversion (`convert_to_markdown`) |
| **`context7`** | stdio | `npx @upstash/context7-mcp` | Active | Framework & library documentation (`context7_*`) |
| **`honeycomb`** | stdio | `npx mcp-remote https://mcp.honeycomb.io/mcp` | On-Demand | Distributed tracing (deployable via `veronica_deploy_module`) |

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
# Start the gateway daemon
veronica serve

# List configured modules
veronica list

# Show version
veronica version
```

## Client Configurations

Veronica supports both **HTTP/SSE** (daemon mode via LaunchAgent) and **Stdio** (direct subprocess mode).

### 1. VS Code & Copilot

In `~/Library/Application Support/Code/User/mcp.json` and `~/.copilot/mcp-config.json`:

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
      "type": "local",
      "command": "/Users/mark.fenderov/bin/veronica",
      "args": ["serve", "--stdio"],
      "tools": ["*"]
    }
  }
}
```

### 2. OpenCode

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

### 3. Pi

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

---

## Quality & Testing Gates

```bash
# Run tests with race detector
make test

# Run strict CRAP score quality gate (max 10)
make crap

# Run linter
make lint

# Compile and install to ~/bin
make install
```
