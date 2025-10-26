// Package sdk provides a high-level, ergonomic API for using Codex programmatically.
// It wraps internal packages (client, conversation manager, tools orchestrator) and provides
// a simple interface for creating AI-powered coding sessions with streaming, tool execution,
// and conversation persistence.
package sdk

import (
	"context"
	"fmt"
	"sync"

	"github.com/evmts/codex/codex-go/internal/conversation/manager"
	"github.com/evmts/codex/codex-go/internal/tools/file"
	"github.com/evmts/codex/codex-go/internal/tools/orchestrator"
	"github.com/evmts/codex/codex-go/internal/tools/patch"
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
	"github.com/evmts/codex/codex-go/internal/tools/shell"
	"github.com/evmts/codex/codex-go/pkg/sdk/client"
	"github.com/spf13/afero"
)

// SDK is the main entry point for the Codex SDK.
// It manages conversation sessions and provides access to the AI model.
type SDK struct {
	client        *client.Client
	manager       manager.ConversationManager
	toolRegistry  *runtime.ToolRegistry
	orchestrator  *orchestrator.Orchestrator
	approvalCache runtime.ApprovalCache
	enableHistory bool
	historyPath   string
	mu            sync.RWMutex
	sessions      map[string]*Session
}

// New creates a new Codex SDK instance with the given options.
// The client is required; other options are optional with sensible defaults.
func New(opts Options) (*SDK, error) {
	if opts.Client == nil {
		return nil, fmt.Errorf("client is required")
	}

	// Create or use provided tool registry
	var toolRegistry *runtime.ToolRegistry
	if opts.ToolRegistry != nil {
		toolRegistry = opts.ToolRegistry
	} else {
		toolRegistry = runtime.NewToolRegistry()

		// Register tools
		if len(opts.Tools) > 0 {
			// Use provided tools
			for _, tool := range opts.Tools {
				toolRegistry.Register(tool)
			}
		} else {
			// Register default tools
			fs := afero.NewOsFs()
			toolRegistry.Register(patch.NewPatchTool(fs))
			toolRegistry.Register(file.NewReadTool(fs))
			toolRegistry.Register(file.NewWriteTool(fs))
			toolRegistry.Register(file.NewListTool(fs))
			toolRegistry.Register(file.NewGrepTool(fs))
			toolRegistry.Register(shell.NewShellTool())
		}
	}

	// Create approval cache
	approvalCache := runtime.NewMemoryApprovalCache()

	// Create orchestrator
	orch := orchestrator.NewOrchestrator(
		toolRegistry,
		approvalCache,
		nil, // Approval handler will be set per-session
	)

	// Create conversation manager
	mgr, err := manager.NewManager(manager.ManagerConfig{
		Client: opts.Client.Internal(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create conversation manager: %w", err)
	}

	return &SDK{
		client:        opts.Client,
		manager:       mgr,
		toolRegistry:  toolRegistry,
		orchestrator:  orch,
		approvalCache: approvalCache,
		enableHistory: opts.EnableHistory,
		historyPath:   opts.HistoryPath,
		sessions:      make(map[string]*Session),
	}, nil
}

// NewSession creates a new conversation session with the given options.
// Sessions are isolated and can run concurrently.
func (s *SDK) NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	// Create session
	session := &Session{
		sdk:              s,
		systemPrompt:     opts.SystemPrompt,
		streaming:        opts.Streaming,
		onToolApproval:   opts.OnToolApproval,
		approvalPolicy:   opts.ApprovalPolicy,
		sandboxPolicy:    opts.SandboxPolicy,
		workingDirectory: opts.WorkingDirectory,
		model:            opts.Model,
		conversationID:   opts.ConversationID,
		messages:         make([]*Message, 0),
	}

	// Generate session ID if not provided
	if session.conversationID == "" {
		session.conversationID = generateSessionID()
	}

	// Store session
	s.mu.Lock()
	s.sessions[session.conversationID] = session
	s.mu.Unlock()

	return session, nil
}

// GetSession retrieves an existing session by ID.
func (s *SDK) GetSession(sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return session, nil
}

// ListSessions returns the IDs of all active sessions.
func (s *SDK) ListSessions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}

	return ids
}

// CloseSession closes a specific session and removes it from the SDK.
func (s *SDK) CloseSession(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Close the session
	if err := session.close(); err != nil {
		return fmt.Errorf("failed to close session: %w", err)
	}

	// Remove from SDK
	delete(s.sessions, sessionID)

	return nil
}

// Close closes all sessions and shuts down the SDK.
// This should be called when the SDK is no longer needed to clean up resources.
func (s *SDK) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error

	// Close all sessions
	for id, session := range s.sessions {
		if err := session.close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close session %s: %w", id, err))
		}
	}

	// Close conversation manager
	if err := s.manager.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close conversation manager: %w", err))
	}

	// Clear sessions
	s.sessions = make(map[string]*Session)

	if len(errs) > 0 {
		return fmt.Errorf("errors during shutdown: %v", errs)
	}

	return nil
}

// generateSessionID generates a unique session identifier.
var sessionIDCounter int64

func generateSessionID() string {
	// Use a counter to ensure unique IDs in tests
	sessionIDCounter++
	return fmt.Sprintf("session_%d", sessionIDCounter)
}
