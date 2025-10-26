package orchestrator

import (
	"fmt"
	"sort"

	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

// RegistryHelper provides convenience methods for working with tool registries.
// The core ToolRegistry is defined in runtime package; this adds orchestrator-specific helpers.
type RegistryHelper struct {
	registry *runtime.ToolRegistry
}

// NewRegistryHelper wraps a tool registry with helper methods.
func NewRegistryHelper(registry *runtime.ToolRegistry) *RegistryHelper {
	return &RegistryHelper{
		registry: registry,
	}
}

// GetOrError retrieves a tool by name or returns a structured error.
func (h *RegistryHelper) GetOrError(name string) (runtime.ToolRuntime, error) {
	tool := h.registry.Get(name)
	if tool == nil {
		return nil, &runtime.ToolError{
			Kind:    runtime.ErrorInternal,
			Message: fmt.Sprintf("tool not found: %s", name),
		}
	}
	return tool, nil
}

// ListSorted returns all tool names sorted alphabetically.
func (h *RegistryHelper) ListSorted() []string {
	names := h.registry.List()
	sort.Strings(names)
	return names
}

// CountTools returns the number of registered tools.
func (h *RegistryHelper) CountTools() int {
	return len(h.registry.List())
}

// HasTool checks if a tool with the given name is registered.
func (h *RegistryHelper) HasTool(name string) bool {
	return h.registry.Get(name) != nil
}

// GetToolsByCapability returns all tools matching a specific capability.
func (h *RegistryHelper) GetToolsByCapability(check func(runtime.ToolRuntime) bool) []runtime.ToolRuntime {
	tools := []runtime.ToolRuntime{}
	for _, name := range h.registry.List() {
		tool := h.registry.Get(name)
		if tool != nil && check(tool) {
			tools = append(tools, tool)
		}
	}
	return tools
}

// GetParallelTools returns all tools that support parallel execution.
func (h *RegistryHelper) GetParallelTools() []runtime.ToolRuntime {
	return h.GetToolsByCapability(func(t runtime.ToolRuntime) bool {
		return t.SupportsParallel()
	})
}

// GetSequentialTools returns all tools that don't support parallel execution.
func (h *RegistryHelper) GetSequentialTools() []runtime.ToolRuntime {
	return h.GetToolsByCapability(func(t runtime.ToolRuntime) bool {
		return !t.SupportsParallel()
	})
}

// GetToolsRequiringSandbox returns all tools that require sandboxing.
func (h *RegistryHelper) GetToolsRequiringSandbox() []runtime.ToolRuntime {
	return h.GetToolsByCapability(func(t runtime.ToolRuntime) bool {
		return t.SandboxPreference() == runtime.SandboxRequire
	})
}

// GetToolsForbiddingSandbox returns all tools that forbid sandboxing.
func (h *RegistryHelper) GetToolsForbiddingSandbox() []runtime.ToolRuntime {
	return h.GetToolsByCapability(func(t runtime.ToolRuntime) bool {
		return t.SandboxPreference() == runtime.SandboxForbid
	})
}

// ToolInfo provides metadata about a registered tool.
type ToolInfo struct {
	Name              string
	SupportsParallel  bool
	SandboxPreference runtime.SandboxPreference
	EscalateOnFailure bool
}

// GetToolInfo returns metadata about a tool.
func (h *RegistryHelper) GetToolInfo(name string) (*ToolInfo, error) {
	tool, err := h.GetOrError(name)
	if err != nil {
		return nil, err
	}

	return &ToolInfo{
		Name:              tool.Name(),
		SupportsParallel:  tool.SupportsParallel(),
		SandboxPreference: tool.SandboxPreference(),
		EscalateOnFailure: tool.EscalateOnFailure(),
	}, nil
}

// GetAllToolInfo returns metadata for all registered tools.
func (h *RegistryHelper) GetAllToolInfo() []*ToolInfo {
	infos := []*ToolInfo{}
	for _, name := range h.ListSorted() {
		info, err := h.GetToolInfo(name)
		if err == nil {
			infos = append(infos, info)
		}
	}
	return infos
}

// ValidateToolRequests validates a batch of tool requests.
// Returns the first invalid request or nil if all are valid.
func (h *RegistryHelper) ValidateToolRequests(requests []*runtime.ToolRequest) error {
	for _, req := range requests {
		if err := h.ValidateToolRequest(req); err != nil {
			return err
		}
	}
	return nil
}

// ValidateToolRequest validates a single tool request.
func (h *RegistryHelper) ValidateToolRequest(req *runtime.ToolRequest) error {
	if req == nil {
		return &runtime.ToolError{
			Kind:    runtime.ErrorInvalidArguments,
			Message: "request is nil",
		}
	}

	if req.CallID == "" {
		return &runtime.ToolError{
			Kind:    runtime.ErrorInvalidArguments,
			Message: "request missing CallID",
		}
	}

	if req.ToolName == "" {
		return &runtime.ToolError{
			Kind:    runtime.ErrorInvalidArguments,
			Message: "request missing ToolName",
		}
	}

	if !h.HasTool(req.ToolName) {
		return &runtime.ToolError{
			Kind:    runtime.ErrorInternal,
			Message: fmt.Sprintf("tool not found: %s", req.ToolName),
		}
	}

	return nil
}

// GroupRequestsByParallelism groups tool requests into parallel and sequential batches.
// This helps the execution engine optimize scheduling.
func (h *RegistryHelper) GroupRequestsByParallelism(
	requests []*runtime.ToolRequest,
) (parallel []*runtime.ToolRequest, sequential []*runtime.ToolRequest) {
	parallel = []*runtime.ToolRequest{}
	sequential = []*runtime.ToolRequest{}

	for _, req := range requests {
		tool := h.registry.Get(req.ToolName)
		if tool == nil {
			// If tool not found, treat as sequential to be safe
			sequential = append(sequential, req)
			continue
		}

		if tool.SupportsParallel() {
			parallel = append(parallel, req)
		} else {
			sequential = append(sequential, req)
		}
	}

	return parallel, sequential
}

// FilterRequestsByTool filters requests by tool name.
func (h *RegistryHelper) FilterRequestsByTool(
	requests []*runtime.ToolRequest,
	toolName string,
) []*runtime.ToolRequest {
	filtered := []*runtime.ToolRequest{}
	for _, req := range requests {
		if req.ToolName == toolName {
			filtered = append(filtered, req)
		}
	}
	return filtered
}

// DeduplicateRequests removes duplicate requests based on CallID.
// Returns deduplicated requests maintaining original order.
func (h *RegistryHelper) DeduplicateRequests(
	requests []*runtime.ToolRequest,
) []*runtime.ToolRequest {
	seen := make(map[string]bool)
	deduplicated := []*runtime.ToolRequest{}

	for _, req := range requests {
		if !seen[req.CallID] {
			seen[req.CallID] = true
			deduplicated = append(deduplicated, req)
		}
	}

	return deduplicated
}

// CreateRegistrySnapshot captures the current state of the registry.
// Useful for testing and debugging.
type RegistrySnapshot struct {
	ToolNames   []string
	ToolCount   int
	Parallel    int
	Sequential  int
	Sandboxed   int
	Unsandboxed int
}

// GetSnapshot returns a snapshot of the current registry state.
func (h *RegistryHelper) GetSnapshot() *RegistrySnapshot {
	snapshot := &RegistrySnapshot{
		ToolNames: h.ListSorted(),
		ToolCount: h.CountTools(),
	}

	for _, name := range snapshot.ToolNames {
		tool := h.registry.Get(name)
		if tool == nil {
			continue
		}

		if tool.SupportsParallel() {
			snapshot.Parallel++
		} else {
			snapshot.Sequential++
		}

		pref := tool.SandboxPreference()
		if pref == runtime.SandboxRequire || pref == runtime.SandboxAuto {
			snapshot.Sandboxed++
		} else {
			snapshot.Unsandboxed++
		}
	}

	return snapshot
}
