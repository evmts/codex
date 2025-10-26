package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/cmd/codex/tui"
	"github.com/evmts/codex/codex-go/internal/client"
	"github.com/evmts/codex/codex-go/internal/client/openai"
	"github.com/evmts/codex/codex-go/internal/conversation/manager"
	"github.com/evmts/codex/codex-go/internal/protocol"
	"github.com/evmts/codex/codex-go/internal/tools"
	"github.com/evmts/codex/codex-go/internal/tools/orchestrator"
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

var (
	messageFlag = flag.String("message", "", "Send a message in non-interactive mode")
	messageFlagShort = flag.String("m", "", "Send a message in non-interactive mode (shorthand)")
	sessionFlag = flag.String("session", "", "Session ID to use (optional, generates new one if not specified)")
	sessionFlagShort = flag.String("s", "", "Session ID to use (shorthand)")
	modelFlag   = flag.String("model", "", "Model to use (overrides MODEL env var)")
)

func main() {
	flag.Parse()

	// Determine message and session from flags
	message := *messageFlag
	if message == "" {
		message = *messageFlagShort
	}
	session := *sessionFlag
	if session == "" {
		session = *sessionFlagShort
	}
	model := *modelFlag

	// Create manager
	mgr, err := createManager()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing manager: %v\n", err)
		os.Exit(1)
	}
	defer mgr.Close()

	// Check if non-interactive mode
	if message != "" {
		// Non-interactive mode
		if err := runNonInteractive(mgr, message, session, model); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Interactive TUI mode
		if err := tui.Run(mgr); err != nil {
			fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
			os.Exit(1)
		}
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

	// Create tool registry with all standard tools
	registry := tools.NewDefaultRegistry()

	// Create approval cache (auto-approve all for now)
	approvalCache := tools.NewAutoApprovalCache()

	// Create orchestrator with auto-approval handler
	autoApprovalHandler := func(ctx context.Context, req *runtime.ApprovalRequest) (runtime.ApprovalDecision, error) {
		// Auto-approve everything
		return runtime.ApprovalApproved, nil
	}

	orch := orchestrator.NewOrchestrator(registry, approvalCache, autoApprovalHandler)

	// Create manager with orchestrator
	cfg := manager.ManagerConfig{
		Client:       llmClient,
		Orchestrator: orch,
	}
	return manager.NewManager(cfg)
}

// runNonInteractive runs a single message in non-interactive mode and streams the response to stdout
func runNonInteractive(mgr manager.ConversationManager, message, sessionID, model string) error {
	ctx := context.Background()

	// Use provided model or default
	if model == "" {
		model = os.Getenv("MODEL")
		if model == "" {
			model = "claude-3-5-sonnet-20241022"
		}
	}

	// Generate session ID if not provided
	if sessionID == "" {
		sessionID = fmt.Sprintf("cli-%d", time.Now().Unix())
	}

	// Create event handler for streaming output
	var streamingText strings.Builder
	done := make(chan struct{})
	hadError := false

	eventHandler := func(ctx context.Context, event *protocol.Event) error {
		switch msg := event.Msg.(type) {
		case *protocol.EventAgentMessageDelta:
			// Print streaming text immediately
			fmt.Print(msg.Delta)
			streamingText.WriteString(msg.Delta)

		case *protocol.EventExecCommandBegin:
			// Show tool execution
			fmt.Fprintf(os.Stderr, "\n[Executing: %s]\n", strings.Join(msg.Command, " "))

		case *protocol.EventExecCommandEnd:
			// Show tool completion
			if msg.ExitCode == 0 {
				fmt.Fprintf(os.Stderr, "[Command completed successfully]\n")
			} else {
				fmt.Fprintf(os.Stderr, "[Command failed with exit code %d]\n", msg.ExitCode)
			}

		case *protocol.EventTaskComplete:
			// Task complete
			fmt.Println() // Final newline
			close(done)

		case *protocol.EventError:
			// Error occurred
			fmt.Fprintf(os.Stderr, "\nError: %s\n", msg.Message)
			hadError = true
			close(done)
		}
		return nil
	}

	// Try to get existing session or create new one
	sess, err := mgr.GetSession(sessionID)
	if err != nil || sess == nil {
		// Get absolute path for current directory
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			cwd = "." // Fallback to relative path
		}

		// Create new session with event handler
		sess, err = mgr.CreateSession(ctx, manager.SessionConfig{
			ID: sessionID,
			TurnContext: &manager.TurnContext{
				Cwd:            cwd,
				ApprovalPolicy: "auto",
				SandboxPolicy:  protocol.SandboxPolicy{Mode: "native"},
				Model:          model,
			},
			EventHandlers: []manager.EventHandler{eventHandler},
		})
		if err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
	}

	// Submit message
	op := &protocol.OpUserInput{
		Items: []protocol.UserInput{
			{
				Type: "text",
				Text: &message,
			},
		},
	}

	err = mgr.SubmitOp(ctx, sessionID, op)
	if err != nil {
		return fmt.Errorf("failed to submit message: %w", err)
	}

	// Wait for completion with timeout
	select {
	case <-done:
		if hadError {
			return fmt.Errorf("turn processing failed")
		}
		return nil
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("timeout waiting for response")
	}
}
