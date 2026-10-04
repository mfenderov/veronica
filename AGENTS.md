# AGENTS.md

Autonomous agent guide and engineering instructions for Veronica.

`veronica` is a local Model Context Protocol (MCP) gateway and tool multiplexer in Go
(module `github.com/mfenderov/veronica`, Go 1.26+). It spawns/proxies downstream MCP
servers (local stdio processes, remote Streamable HTTP / legacy SSE) and re-exposes
their tools plus `veronica_*` meta-tools to upstream AI clients over a single endpoint.
Key libs: `mark3labs/mcp-go`, `spf13/cobra`, `charmbracelet/bubbletea+lipgloss`,
`gopkg.in/yaml.v3`, `stretchr/testify`.

## Essential commands

```bash
make build         # go build -o bin/veronica ./cmd/veronica (Version via -ldflags -X main.Version)
make test          # go test -v -race -shuffle=on ./...  (canonical; CI uses same)
make test-e2e      # go test -v -race -shuffle=on ./e2e/...
make test-coverage # coverprofile + HTML
make crap          # go test -coverprofile + `go tool gocrap -exclude '*_test.go' -max 10 ./...`
make lint          # go tool golangci-lint run  (NOT bare `golangci-lint`)
make fmt           # go fmt ./...
make fix           # go fix ./...
make tidy          # go mod tidy && go mod tidy --diff  (CI fails if not tidy)
make clean         # rm -rf bin/ coverage.out coverage.html
make install       # build + atomic install (cp tmp + mv) to $BINDIR
# BINDIR defaults to ~/.local/bin only if dir exists AND is on PATH, else ~/bin.
# Override: make install BINDIR=/custom/bin
cog check --from-latest-tag  # Conventional Commits check (cog.toml: tag_prefix "v", main only)
```

Runtime CLI (`cmd/veronica/main.go`, `service.go`):

```bash
veronica serve                                   # daemon on 127.0.0.1:9090 (loopback by default)
veronica serve --config ~/.config/veronica/config.yaml
veronica serve --stdio                           # single-client stdio mode, no TCP (quiet: suppresses mount logs)
veronica tui                                     # Bubble Tea dashboard (default endpoint http://localhost:9090/sse)
veronica tui --endpoint http://localhost:9090/sse
veronica list                                    # NAME/TRANSPORT/TARGET table from on-disk config (sorted)
veronica doctor                                  # read-only Diagnose over config+auth.json; exits non-zero on any error
veronica version
veronica install-service                         # systemd user unit (linux) or LaunchAgent plist (darwin), enable+start
veronica install-service --dry-run               # print unit without writing
veronica install-service --uninstall            # stop/disable/remove
```

## Architecture and data flow

```text
AI clients (Copilot/OpenCode/Pi/Claude/Cursor)
  -> UpstreamServer (:9090): GET /sse + POST /message (legacy SSE) else Streamable HTTP (/mcp, POST /sse, POST /)
  -> Registry (namespaced catalog + raw aliases + toolRoutes)
  -> DownstreamAdapter per module: stdio child OR remote HTTP/SSE (mcp-go client)
Meta-tools (veronica_*) -> meta.Handler (domain.PodService) -> Registry + AuthStore + ClientFactory + config.yaml
Supervisor goroutine (started in runServer) -> probes auto_restart modules + hotswaps changed binaries
TUI / doctor -> domain.PodService (local Handler or RemotePodClient over SSE)
```

Upstream multiplexing (`internal/transport/upstream.go:Handler`): exactly
`GET /sse` -> SSE handler, any path `/message` -> SSE message handler, everything
else -> StreamableHTTP server. Every catalog rebuild calls
`SendNotificationToAllClients(tools/list_changed)`. Daemon mode serves both HTTP
transports on one port; `--stdio` serves one client over stdin/stdout.

Layer import rules (enforced by `architecture_test.go` via `tools/goarch`):

| Layer (`goarch.Layer`) | Path | May only import |
|---|---|---|
| `domain` | `internal/domain` | nothing (zero internal deps) |
| `registry` | `internal/registry` | `internal/domain` |
| `auth` | `internal/auth` | `internal/domain` |
| `client` | `internal/client` | `internal/domain` |
| `tui` | `internal/tui` | `internal/domain` |
| `meta` | `internal/meta` | `domain`, `registry`, `auth` |
| `transport` | `internal/transport` | `domain`, `registry`, `auth` |
| `config` | `internal/config` | `domain`, `registry`, `auth` |

`cmd/veronica` wires everything (`setupApp`: Load config -> seed defaults ->
`NewCommandPolicy` -> `NewFileStore` + `ImportFromOpenCode` -> `NewOAuthManager` ->
`registry.New` + `downstreamFactory` + `meta.NewHandler`). `tools/goarch` itself is a
test-only helper (`Check`, `Layer(...).MayOnlyImport/MustNotImport`), not runtime code.

## Code organization

- `cmd/veronica/main.go`: Cobra root (`serve/list/version/tui/doctor/install-service`),
  `setupApp`, `mountAllModules/mountSingleModule`, `registerMetaTools`,
  `populateDefaultModules`, gateway-auth wrapping (`buildGatewayHandler`).
- `cmd/veronica/service.go`: `install-service` rendering (`renderSystemdUnit`,
  `renderLaunchAgent`), `quotePath`, `serviceFilePath` (linux
  `~/.config/systemd/user/veronica.service`, darwin
  `~/Library/LaunchAgents/com.mfenderov.veronica.plist`).
- `internal/domain/`: pure types, no internal imports. `module.go` (ModuleConfig,
  Module + `MarkStarting/MarkActive/MarkError/MarkInactive`, sentinel errors),
  `auth.go` (AuthToken `IsExpired/WillExpireSoon`, OAuthClientConfig with `AuthParams`),
  `interfaces.go` (DownstreamClient, AuthStore, TokenProvider, content types
  `text/image/audio/resource_link/resource`), `service.go` (DeployParams/Result,
  Recall/Toggle/Reauth/Restart results, ModuleSummary, GatewayStatus, PodService iface),
  `trace.go` (ToolTrace, TraceRecorder).
- `internal/registry/registry.go`: `Registry` (`modules/clients/toolRoutes/aggregated/listeners`),
  `Register/RegisterError/Unregister/Deactivate`, `rebuildCatalogLocked`,
  `FormatExposedToolName/FormatExposedDescription`, `OnToolsChanged`.
- `internal/registry/traces.go`: `RingBufferTraceRecorder(100)`.
- `internal/transport/downstream.go`: `DownstreamAdapter`, `NewDownstreamClient`
  (validates only, no connection), `Start/Stop/ListTools/CallTool`.
- `internal/transport/upstream.go`: `UpstreamServer`, `RegisterCustomTool`, `syncTools`.
- `internal/transport/auth.go`: `WithBearerAuth` (constant-time compare, `WWW-Authenticate: Bearer`).
- `internal/transport/retry.go`: `RetryRoundTripper` + `DefaultRetryConfig`.
- `internal/auth/`: `oauth.go` (OAuthManager: Get/EnsureValid/Refresh/StartInteractiveFlow,
  PKCE S256, DCR, browser open via `open`/`xdg-open`/`rundll32`), `callback.go`
  (local callback listener), `store.go` (FileAuthStore + `ImportFromOpenCode`),
  `gateway_token.go` (`LoadGatewayToken`, `VERONICA_GATEWAY_TOKEN`).
- `internal/config/config.go`: `ServerConfig` (`Addr`, `OAuthCallbackAddr`,
  `AuthTokenFile`, `AllowedCommands`), `Config` (mutex + `Modules` map),
  `Load/Save/AddModule/RemoveModule/GetModule`, `RequiresGatewayAuth`,
  `DefaultConfig` (`127.0.0.1:9090`, `127.0.0.1:9091`).
- `internal/meta/tools.go`: `Handler` (implements `domain.PodService`),
  `DeployModule/RecallModule/ToggleModule/ReauthModule/RestartDaemon/Status/ListModules/RecentTraces/SetWatchBinary`.
- `internal/meta/policy.go`: `CommandPolicy` allowlist. `supervisor.go`: health+hotswap
  loop (`DefaultSupervisorInterval 30s`, `ProbeTimeout 5s`, `RestartTimeout 30s`).
  `fingerprint.go`: `resolveModuleBinary/hashFile`, `shimCommands`.
  `diagnose.go`: `Diagnose` + `HasErrors`, `Diagnostic{Module,Check,Severity,Message}`.
- `internal/client/client.go`: `RemotePodClient` (SSE + `VERONICA_GATEWAY_TOKEN` header func).
- `internal/tui/`: Bubble Tea `Model` over `domain.PodService`
  (`model.go/update.go/views.go/styles.go/live.go`); bottom pane events/traces.
- `e2e/gateway_e2e_test.go`: full lifecycle (mock stdio binary built with `go build`,
  mock remote enforcing `Bearer valid-token`, mock OAuth server).
- `tools/goarch/`: architecture-test helper only.
- `.github/workflows/ci.yml`: jobs `lint` (golangci + tidy check) -> `test`
  (race+shuffle + `make crap`) -> `build`; `release` job on main push does
  `cog bump --auto` (falls back to `--patch` if no feat/fix detected), pushes tag,
  triggers `release.yml` (goreleaser, exact-tag check).
- Runtime files: `~/.config/veronica/config.yaml`, `~/.config/veronica/auth.json`
  (both 0600 files / 0700 dirs, atomic tmp+rename writes).

## Conventions and patterns

- Errors: sentinel vars in `domain` (`ErrEmptyModuleName`, `ErrInvalidTransport`,
  `ErrMissingCommand/URL`, `ErrModuleNotFound`, `ErrToolNotFound`,
  `ErrToolExecutionFail`, `ErrTokenNotFound`); wrap with `%w`, match with `errors.Is`.
  `auth.ErrTokenNotFound` is an alias of `domain.ErrTokenNotFound` so either matches.
- Validation: `ModuleConfig.Validate()` (stdio needs `command`, sse/http needs `url`);
  `NewDownstreamClient` validates but does not connect; `Start` connects.
- Tool namespacing (`FormatExposedToolName`): exposed name is `<module>_<tool>`
  unless tool already starts with `<module>_` or `<module>-` (then unchanged).
  The raw downstream name is also registered as a convenience alias; on collision
  the first module in sort order wins deterministically, others stay reachable only
  via namespaced names, collision logged via `slog.Warn`. Descriptions get a
  `[module]` prefix unless already present. `toolRoutes` maps both names to
  `{moduleName, downstreamName}`; `CallTool` rewrites to `downstreamName`.
- Config/auth persistence: `Load` returns `DefaultConfig()` when file is missing
  (never errors); `Save`/token-store save do `MkdirAll(dir, 0700)` + write tmp file
  `0600` + `os.Rename`. `Config` methods are mutex-guarded; `AddModule` upserts.
- `WatchBinary *bool` is nil-means-enabled (`WatchBinaryEnabled()` false only when
  explicitly false). It is runtime-only: `veronica_set_watch_binary` flips it via
  `SetWatchBinary` without touching config.yaml; deploy/toggle/recall persist to yaml.
- Gateway auth: `RequiresGatewayAuth(addr)` returns false for loopback
  (`localhost`, `127.x`, `::1`, empty host means loopback-wildcard) and true for
  non-loopback hostnames/IPs; invalid addr/port errors. When required,
  `LoadGatewayToken` prefers env `VERONICA_GATEWAY_TOKEN` over
  `ServerConfig.AuthTokenFile`; missing/empty is `ErrGatewayTokenMissing`.
- OAuth resolution in `DownstreamAdapter.resolveToken`: if `OAuth != nil`,
  default empty `ServerName` to module name and call `EnsureValidToken` (auto
  refresh near expiry); else `GetToken(moduleName)`. Failure returns nil (no token),
  headers still sent. `setupApp` imports OpenCode tokens from
  `~/.local/share/opencode/mcp-auth.json` (best-effort, error ignored).
- `diagnoseAuth` checks `client_id` first (error naming `oauth.client_id` in
  config.yaml), then the token store; the missing-token hint points at the TUI
  (`A`) / `veronica_reauth_module` meta-tool, never a CLI subcommand.
- Stdio: env = `os.Environ()` + `cfg.Env`; client built with
  `NewStdioWithOptions(command, env, args)` + `WithLegacyProtocolOnly()` (required
  for Python FastMCP servers that reject `server/discover`; keep the existing
  `//nolint:staticcheck` on that call).
- HTTP: `http.Client{Transport: RetryRoundTripper(Default), Timeout: 30s}`.
  `DefaultRetryConfig`: MaxRetries 2, 100ms initial, 1s max, factor 2.0 + jitter.
  Retries only transient network errors (not `Canceled`/`DeadlineExceeded`) and
  statuses 429/502/503/504; request body buffered/rewound via `GetBody`.
- Registry threading: `Register` calls `client.ListTools(context.Background())`
  (not the caller ctx) before taking the lock; `notifyListeners()` (which triggers
  `UpstreamServer.syncTools`) runs after unlock. `RegisterError` keeps the module
  visible with error status and stops+drops any still-registered client so failed
  restarts cannot orphan it. `rebuildCatalogLocked` aggregates only `StatusActive`
  modules, sorted by module name.
- Meta-tools actually registered in `registerMetaTools` (9): `veronica_status`,
  `veronica_list_modules`, `veronica_deploy_module`, `veronica_recall_module`,
  `veronica_toggle_module`, `veronica_set_watch_binary`, `veronica_reauth_module`,
  `veronica_restart_daemon`, `veronica_traces`. Deploy/toggle/recall handlers in
  `main.go` persist to config.yaml (`AddModule` / `RemoveModule` / flip `Disabled`)
  and `Save` (error ignored with `_ =`). `parseTracesLimit` defaults to 20.
- `CommandPolicy`: empty `AllowedCommands` means allow-all (`allowed == nil`).
  Non-empty resolves each entry via `exec.LookPath` at construction (unknown command
  fails setup) and `Validate` compares resolved paths exactly. `setupApp` fails fast
  on bad policy.
- Supervisor: `Run` ticks `Interval`, `CheckOnce` hotswaps first (binary fingerprint
  changed, including error-state modules) then health-probes `auto_restart` modules.
  `resolveModuleBinary` returns not-ok for non-stdio, empty command, shim basenames
  (`npx bunx npm uvx pnpm yarn deno` — shims never watched), unresolvable paths,
  dirs. `Diagnose` is read-only (mutates no state); disabled modules short-circuit to
  a single `ok` diagnostic; token-expiry warning window is 15m (`DiagnosticExpiryWindow`).
- First-run: `populateDefaultModules` seeds mark42/markitdown/honeycomb(disabled)/
  context7 plus atlassian/slack endpoint+scaffold OAuth metadata (auth/token URLs,
  redirect, scopes, auth params) only when `len(cfg.Modules) == 0`, then `Save`
  (error ignored). Defaults contain NO credentials: `client_id`/`client_secret`
  are empty and the user fills them in config.yaml (`doctor` errors on missing
  client_id; `StartInteractiveFlow` refuses without one).
  `runServer` warns to stderr (never fails) for enabled stdio modules whose command
  is not in `PATH`, with install hint; `veronica doctor` re-checks on demand.
- TUI/remote: `RemotePodClient` defaults empty endpoint to `http://localhost:9090/sse`,
  injects `Authorization: Bearer $VERONICA_GATEWAY_TOKEN` only when env non-empty,
  and `Connect` uses `context.Background()` for the session lifetime (values kept,
  cancel on `Close`). Dashboard keys: `↑/↓` or `j/k` navigate, `Space` toggle,
  `e` edit, `A`/`Ctrl+A` re-auth, `a` deploy, `d` recall, `r` refresh, `q`/`Ctrl+C`
  quit; modal: `Tab` field, `Ctrl+T` transport, `Enter` submit, `Esc` cancel.
  `interactiveAuthTimeout` is 3m. Trace ring holds 100 entries.

## Testing approach

- Unit tests live next to code (`*_test.go`); e2e in `e2e/` (package `e2e_test`).
  Always run `make test` (or `go test -v -race -shuffle=on ./...`) and `make crap`
  after changes; run `make test-e2e` for transport/registry/meta changes.
- Test style enforced by `.golangci.yml` `forbidigo` (applies to `_test.go` only
  via path-except inversion — i.e. these are *required* in tests): use `t.Context()`
  (never `context.Background()/TODO()`), `t.TempDir()` (never `os.MkdirTemp`),
  `t.Setenv()` (never `os.Setenv`), `t.Chdir()` (never `os.Chdir`); no
  `fmt.Print*`, no `time.Sleep` — synchronize on channels/events. `t.Parallel()`
  is used (see `TestArchitecture`).
- Mocks: hand-written fakes (`mockClient`, `testClientFactory`,
  `supervisedClient`) implementing `domain.DownstreamClient` / `ClientFactory`;
  `SetOpenBrowserFnForTesting` to stub browser launch. E2e builds a real mock stdio
  MCP binary (`go build -o tmp mock main.go` using `server.ServeStdio`) and a mock
  remote behind `httptest.Server` that rejects anything but `Bearer valid-token`.
- Architecture: `TestArchitecture` fails the build on layer violations — check it
  before adding imports across `internal/` packages.
- Quality gates: CRAP ≤ 10 for every non-test function (`make crap` needs coverage,
  so untested branches fail the gate); `errcheck` with `check-type-assertions: false`,
  `check-blank: false`, `Close`/`ResponseWriter.Write` excluded; `revive exported`
  with `checkPrivateReceivers`; full `staticcheck all`.

## Gotchas (non-obvious, do not re-derive)

- `DownstreamAdapter.Start` must spawn with `context.WithoutCancel(ctx)`: mcp-go
  stdio uses `exec.CommandContext`, so passing the request ctx kills the child when
  the RPC returns. On handshake failure `Start` closes the client itself because
  callers (deploy/toggle) do not `Stop` a failed `Start` — skipping the `Close`
  leaks the child process (see `spawn_lifetime_test.go`).
- `Register` ignores the caller ctx for tool discovery (`context.Background()`),
  so cancellation during deploy does not abort `ListTools`.
- `syncToolsLocked` re-registers custom tools before downstream tools and always
  broadcasts `tools/list_changed`, even when the catalog is empty.
- `NewDownstreamClient` never dials; forgetting `Start` yields `ErrModuleNotFound`
  from `ListTools`/`CallTool` (nil `mcpClient`), which is misleading.
- `updateConfigModuleDisabled` silently no-ops if the name is absent from yaml;
  toggle of an unpersisted module changes runtime only.
- `config.Load` on corrupt yaml returns an error, but on missing file returns
  defaults — do not treat "no config" as failure.
- `RequiresGatewayAuth`: empty host (`:9090`, i.e. wildcard bind) returns
  `requiresAuth=true` (not exempt); `localhost`/loopback IPs are exempt; any
  unparseable hostname (not an IP) counts as remote (auth required).
- `LoadGatewayToken` trims whitespace; env wins even when a file path is set.
- `RetryRoundTripper` with `MaxRetries <= 0` silently becomes 2; `BackoffFactor <= 1`
  becomes 2.0 — passing 0 does not disable retries.
- Hotswap never fires for `npx/uvx/...` shims or remote transports by design;
  to watch those you must change the module to point at a real binary.
- `veronica doctor` uses a throwaway `registry.New()` + `Handler` with nil factory:
  it reports config/credential problems but cannot probe unmounted modules' liveness.
- `install-service` on unsupported `GOOS` errors; systemd unit embeds current `$PATH`
  at install time (`Environment=PATH=...`, `Restart=on-failure RestartSec=2`).
- CI release quirk: `cog bump --auto` on main falls back to `--patch` when no
  feat/fix is detected, then pushes main + tag and dispatches `release.yml`, which
  requires HEAD to be exactly the tag.
- No `fmt.Print*` in tests, no `time.Sleep` in tests, no `context.Background()` in
  tests — all are lint errors, not suggestions.
