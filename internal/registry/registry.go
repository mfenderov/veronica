// Package registry provides module registration, tool catalog aggregation, namespacing, and routing for Veronica.
package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mfenderov/veronica/internal/domain"
)

type toolRoute struct {
	moduleName     string
	downstreamName string
}

// Registry maintains active downstream modules, aggregates their tools into a unified catalog, and routes tool calls.
type Registry struct {
	mu         sync.RWMutex
	modules    map[string]*domain.Module
	clients    map[string]domain.DownstreamClient
	toolRoutes map[string]toolRoute
	aggregated []domain.Tool
	listeners  []func()
}

// New creates an initialized empty Registry.
func New() *Registry {
	return &Registry{
		modules:    make(map[string]*domain.Module),
		clients:    make(map[string]domain.DownstreamClient),
		toolRoutes: make(map[string]toolRoute),
		aggregated: make([]domain.Tool, 0),
		listeners:  make([]func(), 0),
	}
}

// OnToolsChanged registers a listener callback to be invoked whenever the aggregated tool catalog changes.
func (r *Registry) OnToolsChanged(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listeners = append(r.listeners, fn)
}

func (r *Registry) notifyListeners() {
	for _, fn := range r.listeners {
		fn()
	}
}

// Register registers a downstream module and client, discovers its tools, and updates the aggregated catalog.
func (r *Registry) Register(mod *domain.Module, client domain.DownstreamClient) error {
	tools, err := client.ListTools(context.Background())
	if err != nil {
		return fmt.Errorf("failed to list tools for module %s: %w", mod.Name, err)
	}

	r.mu.Lock()
	r.modules[mod.Name] = mod
	r.clients[mod.Name] = client
	mod.MarkActive(tools)
	r.rebuildCatalogLocked()
	r.mu.Unlock()

	r.notifyListeners()
	return nil
}

// RegisterError registers a module that failed to start, preserving its presence in the registry with error status.
func (r *Registry) RegisterError(mod *domain.Module, err error) {
	r.mu.Lock()
	r.modules[mod.Name] = mod
	delete(r.clients, mod.Name)
	mod.MarkError(err.Error())
	r.rebuildCatalogLocked()
	r.mu.Unlock()

	r.notifyListeners()
}

// Unregister removes a module and its client from the registry, stopping the client process.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	_, exists := r.modules[name]
	if !exists {
		r.mu.Unlock()
		return domain.ErrModuleNotFound
	}

	client := r.clients[name]
	delete(r.modules, name)
	delete(r.clients, name)
	r.rebuildCatalogLocked()
	r.mu.Unlock()

	if client != nil {
		_ = client.Stop(context.Background())
	}
	r.notifyListeners()
	return nil
}

// Deactivate transitions an active module to inactive, stops its client, and removes its tools from the catalog.
func (r *Registry) Deactivate(name string) error {
	r.mu.Lock()
	mod, exists := r.modules[name]
	if !exists {
		r.mu.Unlock()
		return domain.ErrModuleNotFound
	}

	client := r.clients[name]
	delete(r.clients, name)
	mod.MarkInactive()
	r.rebuildCatalogLocked()
	r.mu.Unlock()

	if client != nil {
		_ = client.Stop(context.Background())
	}
	r.notifyListeners()
	return nil
}

func (r *Registry) rebuildCatalogLocked() {
	r.toolRoutes = make(map[string]toolRoute)
	r.aggregated = make([]domain.Tool, 0)

	modNames := make([]string, 0, len(r.modules))
	for name := range r.modules {
		modNames = append(modNames, name)
	}
	sort.Strings(modNames)

	for _, modName := range modNames {
		mod := r.modules[modName]
		if mod.Status != domain.StatusActive {
			continue
		}
		for _, t := range mod.Tools {
			exposedName := FormatExposedToolName(modName, t.Name)
			exposedDesc := FormatExposedDescription(modName, t.Description)

			exposedTool := domain.Tool{
				Name:         exposedName,
				Description:  exposedDesc,
				InputSchema:  t.InputSchema,
				OriginModule: modName,
			}

			route := toolRoute{
				moduleName:     modName,
				downstreamName: t.Name,
			}

			r.toolRoutes[exposedName] = route
			r.aggregated = append(r.aggregated, exposedTool)

			// If exposedName differs from raw tool name (e.g. mark42_search_nodes vs search_nodes),
			// also register the raw tool name as an alias so existing sessions and un-prefixed
			// calls succeed seamlessly.
			if exposedName != t.Name {
				r.toolRoutes[t.Name] = route
				aliasTool := domain.Tool{
					Name:         t.Name,
					Description:  exposedDesc,
					InputSchema:  t.InputSchema,
					OriginModule: modName,
				}
				r.aggregated = append(r.aggregated, aliasTool)
			}
		}
	}
}

// ListModules returns a sorted list of all modules currently in the registry.
func (r *Registry) ListModules() []*domain.Module {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*domain.Module, 0, len(r.modules))
	for _, m := range r.modules {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// GetModule retrieves a module by name from the registry, returning false if not found.
func (r *Registry) GetModule(name string) (*domain.Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m, ok := r.modules[name]
	return m, ok
}

// ListTools returns a snapshot of all aggregated tools currently available across active modules.
func (r *Registry) ListTools() []domain.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]domain.Tool, len(r.aggregated))
	copy(out, r.aggregated)
	return out
}

// CallTool routes a tool call to the owning downstream module client.
func (r *Registry) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	r.mu.RLock()
	route, ok := r.toolRoutes[call.ToolName]
	if !ok {
		r.mu.RUnlock()
		return domain.ToolResult{}, domain.ErrToolNotFound
	}
	client, clientOk := r.clients[route.moduleName]
	r.mu.RUnlock()

	if !clientOk {
		return domain.ToolResult{}, domain.ErrModuleNotFound
	}

	downstreamCall := domain.ToolCall{
		ToolName:  route.downstreamName,
		Arguments: call.Arguments,
	}

	return client.CallTool(ctx, downstreamCall)
}

// FormatExposedToolName ensures a tool name is namespaced with its module prefix.
func FormatExposedToolName(modName, toolName string) string {
	prefix := modName + "_"
	if strings.HasPrefix(toolName, prefix) || strings.HasPrefix(toolName, modName+"-") {
		return toolName
	}
	return prefix + toolName
}

// FormatExposedDescription prepends a [module] tag to the tool description if not already present.
func FormatExposedDescription(modName, desc string) string {
	tag := "[" + modName + "]"
	if strings.HasPrefix(desc, tag) {
		return desc
	}
	if desc == "" {
		return tag
	}
	return tag + " " + desc
}
