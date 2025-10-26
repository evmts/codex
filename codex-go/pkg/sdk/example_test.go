package sdk_test

import (
	"context"
	"fmt"
	"log"

	"github.com/evmts/codex/codex-go/pkg/sdk"
	"github.com/evmts/codex/codex-go/pkg/sdk/client"
)

// ExampleNew demonstrates creating a new SDK instance with a client.
func ExampleNew() {
	// Create a client with direct options
	c, err := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	if err != nil {
		log.Fatal(err)
	}

	// Create the SDK
	codex, err := sdk.New(sdk.Options{
		Client: c,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer codex.Close()

	fmt.Println("SDK created successfully")
	// Output: SDK created successfully
}

// ExampleSDK_NewSession demonstrates creating a basic conversation session.
func ExampleSDK_NewSession() {
	// Setup (in real usage, use actual credentials)
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	// Create a session
	ctx := context.Background()
	_, err := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful coding assistant.",
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Session created successfully")
	// Output: Session created successfully
}

// ExampleSDK_NewSession_streaming demonstrates creating a streaming session.
func ExampleSDK_NewSession_streaming() {
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()
	_, err := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful coding assistant.",
		Streaming:    true, // Enable streaming
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Streaming session created successfully")
	// Output: Streaming session created successfully
}

// ExampleSession_Submit demonstrates sending a message and receiving a response.
func ExampleSession_Submit() {
	// Setup
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()
	session, _ := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful assistant.",
		Streaming:    false,
	})

	// Submit a message
	response, err := session.Submit(ctx, "What is 2+2?")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Response received: %d characters\n", len(response.Content))
	fmt.Printf("Tokens used: %d\n", response.TokenUsage.TotalTokens)
	// Output:
	// Response received: 60 characters
	// Tokens used: 30
}

// ExampleSession_SubmitStream demonstrates streaming responses.
func ExampleSession_SubmitStream() {
	// Setup
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()
	session, _ := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful assistant.",
		Streaming:    true,
	})

	// Submit and stream
	eventCh, err := session.SubmitStream(ctx, "Tell me a story")
	if err != nil {
		log.Fatal(err)
	}

	// Process stream events
	for event := range eventCh {
		if event.Error != nil {
			log.Fatal(event.Error)
		}
		if event.Done {
			fmt.Println("Stream complete")
			break
		}
		// In real usage, you would print event.Delta
	}
	// Output: Stream complete
}

// ExampleSession_History demonstrates accessing conversation history.
func ExampleSession_History() {
	// Setup
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()
	session, _ := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful assistant.",
	})

	// Send some messages
	session.Submit(ctx, "First message")
	session.Submit(ctx, "Second message")

	// Access history
	history := session.History()
	fmt.Printf("Conversation has %d messages\n", len(history))
	fmt.Printf("First message role: %s\n", history[0].Role)
	// Output:
	// Conversation has 4 messages
	// First message role: user
}

// Example_basicWorkflow demonstrates a complete basic workflow.
func Example_basicWorkflow() {
	// 1. Create client from environment
	c, err := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	if err != nil {
		log.Fatal(err)
	}

	// 2. Create SDK instance
	codex, err := sdk.New(sdk.Options{
		Client: c,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer codex.Close()

	// 3. Create a session
	ctx := context.Background()
	session, err := codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful coding assistant.",
		Streaming:    false,
	})
	if err != nil {
		log.Fatal(err)
	}

	// 4. Have a conversation
	response, err := session.Submit(ctx, "Hello!")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Workflow completed successfully")
	fmt.Printf("Got response with %d tokens\n", response.TokenUsage.TotalTokens)
	// Output:
	// Workflow completed successfully
	// Got response with 30 tokens
}

// Example_multipleSessions demonstrates managing multiple sessions.
func Example_multipleSessions() {
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()

	// Create multiple sessions
	_, _ = codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a Python expert.",
	})
	_, _ = codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a Go expert.",
	})

	// List all sessions
	sessions := codex.ListSessions()
	fmt.Printf("Active sessions: %d\n", len(sessions))
	fmt.Println("Sessions created successfully")
	// Output:
	// Active sessions: 2
	// Sessions created successfully
}

// Example_toolApproval demonstrates using tool approval callbacks.
func Example_toolApproval() {
	c, _ := client.New(client.Options{
		BaseURL: "https://api.openai.com/v1",
		APIKey:  "test-key",
		Model:   "gpt-4",
	})
	codex, _ := sdk.New(sdk.Options{Client: c})
	defer codex.Close()

	ctx := context.Background()
	_, _ = codex.NewSession(ctx, sdk.SessionOptions{
		SystemPrompt: "You are a helpful assistant.",
		// Provide approval callback
		OnToolApproval: func(toolName, operation string) bool {
			// In real usage, prompt user or check policy
			return true
		},
		ApprovalPolicy: "always",
	})

	fmt.Println("Session with approval callback created")
	// Output: Session with approval callback created
}
