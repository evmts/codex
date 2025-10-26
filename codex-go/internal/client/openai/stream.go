package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/internal/client"
)

// streamParser handles SSE (Server-Sent Events) parsing for streaming responses.
type streamParser struct {
	config client.StreamConfig
}

// newStreamParser creates a new stream parser.
func newStreamParser(config client.StreamConfig) *streamParser {
	return &streamParser{
		config: config,
	}
}

// parse reads and parses an SSE stream, emitting events to the channel.
func (p *streamParser) parse(ctx context.Context, r io.Reader, eventCh chan<- client.StreamEvent) error {
	scanner := bufio.NewScanner(r)

	// Configure idle timeout if set
	var idleTimer *time.Timer
	var idleTimerCh <-chan time.Time
	if p.config.IdleTimeout > 0 {
		idleTimer = time.NewTimer(p.config.IdleTimeout)
		idleTimerCh = idleTimer.C
		defer idleTimer.Stop()
	}

	// Track accumulated tool calls for streaming
	toolCallAccumulator := newToolCallAccumulator()

	for {
		// Reset idle timer
		if idleTimer != nil {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(p.config.IdleTimeout)
		}

		// Wait for next line or timeout
		lineCh := make(chan bool, 1)
		go func() {
			lineCh <- scanner.Scan()
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-idleTimerCh:
			eventCh <- client.StreamEvent{
				Type:  client.EventTypeError,
				Error: client.NewIdleTimeoutError(p.config.IdleTimeout),
			}
			return client.NewIdleTimeoutError(p.config.IdleTimeout)

		case ok := <-lineCh:
			if !ok {
				// End of stream
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("stream scan error: %w", err)
				}
				return nil
			}
		}

		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse SSE line
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for [DONE] marker
		if data == "[DONE]" {
			// Stream completed successfully
			return nil
		}

		// Parse JSON chunk
		var chunk client.ChatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Log but continue - some providers send non-JSON comments
			continue
		}

		// Process chunk and emit events
		events := p.processChunk(&chunk, toolCallAccumulator)
		for _, event := range events {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case eventCh <- event:
			}
		}
	}
}

// processChunk converts a ChatCompletionChunk into StreamEvents.
func (p *streamParser) processChunk(chunk *client.ChatCompletionChunk, accumulator *toolCallAccumulator) []client.StreamEvent {
	var events []client.StreamEvent

	// Process each choice
	for _, choice := range chunk.Choices {
		// Handle role (first chunk)
		if choice.Delta.Role != "" {
			events = append(events, client.StreamEvent{
				Type: client.EventTypeCreated,
				Data: map[string]interface{}{
					"id":    chunk.ID,
					"model": chunk.Model,
					"role":  choice.Delta.Role,
				},
			})
		}

		// Handle content delta
		if choice.Delta.Content != "" {
			events = append(events, client.StreamEvent{
				Type: client.EventTypeOutputTextDelta,
				Data: choice.Delta.Content,
			})
		}

		// Handle reasoning delta (if present)
		if choice.Delta.Reasoning != nil {
			if p.config.EnableRawAgentReasoning {
				events = append(events, client.StreamEvent{
					Type: client.EventTypeReasoningContentDelta,
					Data: choice.Delta.Reasoning,
				})
			}
		}

		// Handle tool calls
		if len(choice.Delta.ToolCalls) > 0 {
			for _, toolCallDelta := range choice.Delta.ToolCalls {
				accumulator.add(toolCallDelta)
			}
		}

		// Handle finish reason
		if choice.FinishReason != "" {
			// Emit any accumulated tool calls
			toolCalls := accumulator.getCompleted()
			if len(toolCalls) > 0 {
				events = append(events, client.StreamEvent{
					Type: client.EventTypeOutputItemDone,
					Data: map[string]interface{}{
						"tool_calls": toolCalls,
					},
				})
			}

			// Emit completion event with usage
			completedEvent := &client.CompletedEvent{
				ResponseID: chunk.ID,
			}
			if chunk.Usage != nil {
				completedEvent.TokenUsage = chunk.Usage
			}

			events = append(events, client.StreamEvent{
				Type: client.EventTypeCompleted,
				Data: completedEvent,
			})
		}
	}

	return events
}

// toolCallAccumulator accumulates streaming tool call fragments.
type toolCallAccumulator struct {
	calls map[int]*client.ToolCall
}

// newToolCallAccumulator creates a new tool call accumulator.
func newToolCallAccumulator() *toolCallAccumulator {
	return &toolCallAccumulator{
		calls: make(map[int]*client.ToolCall),
	}
}

// add processes a tool call delta and accumulates it.
func (a *toolCallAccumulator) add(delta client.ToolCallDelta) {
	// Get or create tool call
	call, exists := a.calls[delta.Index]
	if !exists {
		call = &client.ToolCall{
			ID:   delta.ID,
			Type: delta.Type,
		}
		a.calls[delta.Index] = call
	}

	// Update ID and type if present (first chunk)
	if delta.ID != "" {
		call.ID = delta.ID
	}
	if delta.Type != "" {
		call.Type = delta.Type
	}

	// Accumulate function call data
	if delta.Function != nil {
		if call.Function == nil {
			call.Function = &client.FunctionCall{}
		}

		if delta.Function.Name != "" {
			call.Function.Name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			call.Function.Arguments += delta.Function.Arguments
		}
	}
}

// getCompleted returns all completed tool calls.
func (a *toolCallAccumulator) getCompleted() []client.ToolCall {
	var calls []client.ToolCall

	// Return calls in index order
	maxIndex := -1
	for idx := range a.calls {
		if idx > maxIndex {
			maxIndex = idx
		}
	}

	for i := 0; i <= maxIndex; i++ {
		if call, exists := a.calls[i]; exists {
			calls = append(calls, *call)
		}
	}

	return calls
}

// reset clears the accumulator.
// nolint:unused // Reserved for multi-turn tool call handling
func (a *toolCallAccumulator) reset() {
	a.calls = make(map[int]*client.ToolCall)
}
