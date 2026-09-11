package main

import (
	"testing"

	"github.com/mfenderov/veronica/tools/goarch"
)

func TestArchitecture(t *testing.T) {
	t.Parallel()

	goarch.Check(t,
		// Domain layer has no dependencies on other internal layers
		goarch.Layer("domain", "internal/domain").
			MayOnlyImport(),

		// Registry manages domain modules and tools
		goarch.Layer("registry", "internal/registry").
			MayOnlyImport("internal/domain"),

		// Auth manages credentials and tokens
		goarch.Layer("auth", "internal/auth").
			MayOnlyImport("internal/domain"),

		// Meta-tools interact with domain, registry, and auth
		goarch.Layer("meta", "internal/meta").
			MayOnlyImport("internal/domain", "internal/registry", "internal/auth"),

		// Transports connect downstream and upstream MCPs
		goarch.Layer("transport", "internal/transport").
			MayOnlyImport("internal/domain", "internal/registry", "internal/auth"),

		// Config persistence
		goarch.Layer("config", "internal/config").
			MayOnlyImport("internal/domain", "internal/registry", "internal/auth"),

		// Client adapter connects to Veronica gateway
		goarch.Layer("client", "internal/client").
			MayOnlyImport("internal/domain"),

		// TUI presentation layer
		goarch.Layer("tui", "internal/tui").
			MayOnlyImport("internal/domain"),
	)
}
