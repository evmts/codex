package main

import (
	"fmt"
	"os"

	"github.com/evmts/codex/codex-go/cmd/codex/tui"
	"github.com/evmts/codex/codex-go/internal/client"
	"github.com/evmts/codex/codex-go/internal/client/openai"
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

// createManager creates a conversation manager with a real OpenAI client
func createManager() (manager.ConversationManager, error) {
	// Get API key from environment
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		// Also try ANTHROPIC_API_KEY for Claude models
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key required: set OPENAI_API_KEY or ANTHROPIC_API_KEY environment variable")
	}

	// Get model from environment or use default
	model := os.Getenv("MODEL")
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	// Get base URL from environment or use default
	baseURL := os.Getenv("API_BASE_URL")
	if baseURL == "" {
		// Default to Anthropic API
		baseURL = "https://api.anthropic.com/v1"
	}

	// Create OpenAI-compatible client
	clientCfg := client.ClientConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}

	llmClient, err := openai.NewClient(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	// Create manager
	cfg := manager.ManagerConfig{
		Client: llmClient,
	}
	return manager.NewManager(cfg)
}
