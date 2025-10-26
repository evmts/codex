package main

import (
	"context"
	"fmt"
	"os"

	"github.com/evmts/codex/codex-go/cmd/codex/tui"
	"github.com/evmts/codex/codex-go/internal/client"
	"github.com/evmts/codex/codex-go/internal/conversation/manager"
)

func main() {
	// Create a mock conversation manager for now
	// In production, this would be initialized with a real OpenAI client
	mgr, err := createManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing manager: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	// Start the TUI
	if err := tui.Run(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// createManager creates a conversation manager
// This is a stub that would be replaced with real initialization
func createManager() (manager.ConversationManager, error) {
	// For now, use a mock client
	// In production, this would create a real OpenAI client:
	// import "github.com/evmts/codex/codex-go/internal/client/openai"
	// client := openai.NewClient(openai.Config{APIKey: os.Getenv("OPENAI_API_KEY"), ...})
	cfg := manager.ManagerConfig{
		Client: &mockClient{},
	}
	return manager.NewManager(cfg)
}

// mockClient is a stub client for development
type mockClient struct{}

func (m *mockClient) Stream(ctx context.Context, req *client.ChatCompletionRequest) (<-chan client.StreamEvent, error) {
	ch := make(chan client.StreamEvent, 10)
	close(ch)
	return ch, nil
}

func (m *mockClient) Complete(ctx context.Context, req *client.ChatCompletionRequest) (*client.ChatCompletionResponse, error) {
	return nil, fmt.Errorf("mock client not yet implemented")
}

func (m *mockClient) GetModelContextWindow() int64 {
	return 200000
}

func (m *mockClient) GetAutoCompactTokenLimit() int64 {
	return 0
}
