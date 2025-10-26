// Package tools provides helper functions for initializing the tool registry
// with all available tools.
package tools

import (
	"github.com/evmts/codex/codex-go/internal/tools/file"
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
	"github.com/evmts/codex/codex-go/internal/tools/shell"
	"github.com/spf13/afero"
)

// NewDefaultRegistry creates and populates a tool registry with all standard tools.
// This includes:
//   - shell: Execute shell commands
//   - read_file: Read file contents
//   - write_file: Write file contents
//   - list_dir: List directory contents
//   - grep_files: Search files with regex patterns
func NewDefaultRegistry() *runtime.ToolRegistry {
	registry := runtime.NewToolRegistry()

	// Use OS filesystem for all file operations
	fs := afero.NewOsFs()

	// Register shell tool
	registry.Register(shell.NewShellTool())

	// Register file tools
	registry.Register(file.NewReadTool(fs))
	registry.Register(file.NewWriteTool(fs))
	registry.Register(file.NewListTool(fs))
	registry.Register(file.NewGrepTool(fs))

	return registry
}

// NewAutoApprovalCache creates an approval cache that auto-approves everything.
// This is suitable for trusted environments where user approval is not required.
func NewAutoApprovalCache() runtime.ApprovalCache {
	return &autoApprovalCache{
		cache: make(map[string]runtime.ApprovalDecision),
	}
}

// autoApprovalCache implements ApprovalCache with auto-approval behavior.
type autoApprovalCache struct {
	cache map[string]runtime.ApprovalDecision
}

// Get retrieves a cached approval decision.
func (c *autoApprovalCache) Get(key string) *runtime.ApprovalDecision {
	if decision, ok := c.cache[key]; ok {
		return &decision
	}
	return nil
}

// Put stores an approval decision.
func (c *autoApprovalCache) Put(key string, decision runtime.ApprovalDecision) {
	c.cache[key] = decision
}
