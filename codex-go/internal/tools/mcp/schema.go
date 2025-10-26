package mcp

import (
	"fmt"

	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

// convertMCPSchema converts an MCP tool input schema to the runtime tool schema format.
// MCP schemas follow JSON Schema format which is directly compatible with runtime expectations.
func convertMCPSchema(mcpSchema map[string]interface{}) interface{} {
	// MCP schemas are already in JSON Schema format
	// We can pass them through directly
	return mcpSchema
}

// generateToolSpec creates a runtime.ToolSpec from an MCP tool definition.
// The tool name is prefixed with "mcp__<server-name>__" to avoid conflicts.
func generateToolSpec(serverName string, tool MCPTool) runtime.ToolSpec {
	// Generate unique tool name with MCP prefix
	toolName := fmt.Sprintf("mcp__%s__%s", serverName, tool.Name)

	// Build description with server context
	description := fmt.Sprintf("[MCP: %s] %s", serverName, tool.Description)
	if tool.Description == "" {
		description = fmt.Sprintf("[MCP: %s] Tool: %s", serverName, tool.Name)
	}

	// Convert schema
	var schema interface{}
	if tool.InputSchema != nil {
		schema = convertMCPSchema(tool.InputSchema)
	} else {
		// Default empty object schema
		schema = map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		}
	}

	return runtime.ToolSpec{
		Name:             toolName,
		Description:      description,
		ParametersSchema: schema,
		Strict:           false, // MCP tools may have flexible schemas
		SupportsParallel: true,  // MCP tools can run in parallel
	}
}

// validateMCPTool checks if an MCP tool definition is valid
func validateMCPTool(tool MCPTool) error {
	if tool.Name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	// Check for valid schema structure if present
	if tool.InputSchema != nil {
		// Verify it has a type field
		if schemaType, ok := tool.InputSchema["type"]; ok {
			if schemaType != "object" {
				return fmt.Errorf("tool schema must be of type 'object', got: %v", schemaType)
			}
		}
	}

	return nil
}

// formatToolError formats an MCP tool error for display to the AI model
func formatToolError(serverName, toolName string, err error) string {
	return fmt.Sprintf("Error calling MCP tool '%s' on server '%s': %v", toolName, serverName, err)
}

// formatToolResult formats the successful result from an MCP tool call
func formatToolResult(serverName, toolName, result string) string {
	// For now, just return the result directly
	// In the future, we could add metadata or formatting
	return result
}
