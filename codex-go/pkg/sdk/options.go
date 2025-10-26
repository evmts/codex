package sdk

import (
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
	"github.com/evmts/codex/codex-go/pkg/sdk/client"
)

// Options configures the SDK.
type Options struct {
	// Client is the OpenAI-compatible client for API requests.
	// Use client.New(), client.FromEnv(), or client.FromConfig() to create one.
	Client *client.Client

	// Tools is the list of tools available to the agent.
	// If nil, a default set of tools will be registered.
	Tools []runtime.ToolRuntime

	// ToolRegistry allows providing a pre-configured tool registry.
	// If provided, Tools will be ignored.
	ToolRegistry *runtime.ToolRegistry

	// EnableHistory enables conversation history persistence.
	// When true, conversations will be saved to disk.
	EnableHistory bool

	// HistoryPath is the path where conversation history is stored.
	// Only used when EnableHistory is true.
	// Defaults to ~/.codex/history.jsonl
	HistoryPath string
}

// SessionOptions configures a conversation session.
type SessionOptions struct {
	// SystemPrompt is the initial system message for the session.
	SystemPrompt string

	// Streaming enables streaming responses.
	// When true, use SubmitStream() instead of Submit().
	Streaming bool

	// OnToolApproval is called when a tool requires user approval.
	// If nil, tools will be auto-approved based on the approval policy.
	// Return true to approve, false to deny.
	OnToolApproval func(toolName, operation string) bool

	// ApprovalPolicy controls when tools require approval.
	// Values: "auto" (default), "always", "never"
	ApprovalPolicy string

	// SandboxPolicy controls tool sandboxing.
	// Values: "read_only", "workspace_write", "full_access"
	SandboxPolicy string

	// WorkingDirectory is the initial working directory for the session.
	WorkingDirectory string

	// Model overrides the default model for this session.
	Model string

	// ConversationID for resuming existing conversations.
	// If empty, a new conversation is started.
	ConversationID string
}
