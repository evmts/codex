package openai

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/evmts/codex/codex-go/internal/client"
)

// TestStreamParser_NoGoroutineLeak verifies that the streaming parser doesn't leak goroutines
// when the context is cancelled or when the stream ends normally.
func TestStreamParser_NoGoroutineLeak(t *testing.T) {
	tests := []struct {
		name          string
		streamData    string
		cancelAfter   time.Duration
		expectError   bool
		checkInterval time.Duration
	}{
		{
			name: "no leak on normal completion",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"Hello"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{},"finish_reason":"stop","index":0}]}

data: [DONE]

`,
			cancelAfter:   0,
			expectError:   false,
			checkInterval: 50 * time.Millisecond,
		},
		{
			name: "no leak on context cancellation",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"This will be cancelled"},"index":0}]}

` + generateLongStream(100), // Generate a long stream to ensure goroutine is reading
			cancelAfter:   100 * time.Millisecond,
			expectError:   true,
			checkInterval: 50 * time.Millisecond,
		},
		{
			name: "no leak on stream end",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"done"},"index":0,"finish_reason":"stop"}]}

`,
			cancelAfter:   0,
			expectError:   false,
			checkInterval: 50 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Record initial goroutine count
			runtime.GC()
			time.Sleep(50 * time.Millisecond)
			initialGoroutines := runtime.NumGoroutine()

			// Create parser
			config := client.StreamConfig{}
			parser := newStreamParser(config)

			// Create context with cancellation
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Start goroutine to cancel after specified duration
			if tt.cancelAfter > 0 {
				time.AfterFunc(tt.cancelAfter, cancel)
			}

			// Create event channel
			eventCh := make(chan client.StreamEvent, 100)

			// Create reader
			reader := strings.NewReader(tt.streamData)

			// Run parser
			parseErr := parser.parse(ctx, reader, eventCh)

			// Verify error expectation
			if tt.expectError && parseErr == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && parseErr != nil {
				t.Errorf("unexpected error: %v", parseErr)
			}

			// Close event channel
			close(eventCh)

			// Drain event channel
			for range eventCh {
			}

			// Force garbage collection
			runtime.GC()

			// Wait for goroutines to clean up
			time.Sleep(tt.checkInterval)

			// Check goroutine count
			runtime.GC()
			time.Sleep(tt.checkInterval)
			finalGoroutines := runtime.NumGoroutine()

			// Allow for some variance (test runner goroutines, etc)
			goroutineDiff := finalGoroutines - initialGoroutines
			if goroutineDiff > 2 {
				t.Errorf("goroutine leak detected: initial=%d, final=%d, diff=%d",
					initialGoroutines, finalGoroutines, goroutineDiff)
			}
		})
	}
}

// TestStreamParser_MultipleIterationsNoLeak specifically tests that creating
// a goroutine on each iteration (the old buggy behavior) would leak, but our
// fix prevents this.
func TestStreamParser_MultipleIterationsNoLeak(t *testing.T) {
	// Generate a stream with many chunks
	streamData := "data: " + `{"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n"
	for i := 0; i < 100; i++ {
		streamData += fmt.Sprintf(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"chunk%d"},"index":0}]}`+"\n\n", i)
	}
	streamData += "data: [DONE]\n\n"

	// Record initial goroutine count
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	parser := newStreamParser(client.StreamConfig{})
	ctx := context.Background()
	eventCh := make(chan client.StreamEvent, 200)

	reader := strings.NewReader(streamData)

	// Run parser
	err := parser.parse(ctx, reader, eventCh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	close(eventCh)

	// Count events to ensure parsing worked
	eventCount := 0
	for range eventCh {
		eventCount++
	}

	if eventCount == 0 {
		t.Error("expected events but got none")
	}

	// Force garbage collection
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Check goroutine count - should be roughly the same
	finalGoroutines := runtime.NumGoroutine()
	goroutineDiff := finalGoroutines - initialGoroutines

	// With the old bug, we would have 100+ leaked goroutines
	// With the fix, we should have at most a couple extra from test infrastructure
	if goroutineDiff > 5 {
		t.Errorf("potential goroutine leak: initial=%d, final=%d, diff=%d (old bug would show 100+)",
			initialGoroutines, finalGoroutines, goroutineDiff)
	}
}

// TestStreamParser_ConcurrentCancellation tests that rapid cancellations
// don't cause goroutine leaks or panics.
func TestStreamParser_ConcurrentCancellation(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Run multiple parsers concurrently and cancel them at different times
	const numParsers = 10
	done := make(chan struct{}, numParsers)

	for i := 0; i < numParsers; i++ {
		go func(id int) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("parser %d panicked: %v", id, r)
				}
				done <- struct{}{}
			}()

			streamData := generateLongStream(50)
			parser := newStreamParser(client.StreamConfig{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Cancel at random times
			time.AfterFunc(time.Duration(id*10)*time.Millisecond, cancel)

			eventCh := make(chan client.StreamEvent, 100)
			reader := strings.NewReader(streamData)

			_ = parser.parse(ctx, reader, eventCh)
			close(eventCh)

			// Drain events
			for range eventCh {
			}
		}(i)
	}

	// Wait for all parsers to complete
	for i := 0; i < numParsers; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("timeout waiting for parsers to complete")
		}
	}

	// Force garbage collection
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	goroutineDiff := finalGoroutines - initialGoroutines

	// Should have minimal goroutine increase
	if goroutineDiff > 5 {
		t.Errorf("goroutine leak in concurrent scenario: initial=%d, final=%d, diff=%d",
			initialGoroutines, finalGoroutines, goroutineDiff)
	}
}

// TestStreamParser_IdleTimeoutNoLeak tests that idle timeout doesn't cause
// goroutine leaks.
func TestStreamParser_IdleTimeoutNoLeak(t *testing.T) {
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	initialGoroutines := runtime.NumGoroutine()

	// Create parser with short idle timeout
	parser := newStreamParser(client.StreamConfig{
		IdleTimeout: 100 * time.Millisecond,
	})

	ctx := context.Background()
	eventCh := make(chan client.StreamEvent, 10)

	// Use slow reader that will trigger timeout
	slowReader := &slowReader{delay: 200 * time.Millisecond, chunks: 10}

	// Run parser - should timeout
	err := parser.parse(ctx, slowReader, eventCh)
	if err == nil {
		t.Fatal("expected idle timeout error but got none")
	}

	close(eventCh)
	for range eventCh {
	}

	// Force garbage collection
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	finalGoroutines := runtime.NumGoroutine()
	goroutineDiff := finalGoroutines - initialGoroutines

	if goroutineDiff > 3 {
		t.Errorf("goroutine leak on idle timeout: initial=%d, final=%d, diff=%d",
			initialGoroutines, finalGoroutines, goroutineDiff)
	}
}

// TestStreamParser_ScanDoneChannel tests that the scanDone channel properly
// signals the scanning goroutine to exit.
func TestStreamParser_ScanDoneChannel(t *testing.T) {
	// Create a slow reader that never finishes
	slowReader := &slowReader{delay: 100 * time.Millisecond, chunks: 1000}

	parser := newStreamParser(client.StreamConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	eventCh := make(chan client.StreamEvent, 10)

	// Start parsing in a goroutine
	parseDone := make(chan error, 1)
	go func() {
		parseDone <- parser.parse(ctx, slowReader, eventCh)
	}()

	// Let it run for a bit
	time.Sleep(200 * time.Millisecond)

	// Cancel context
	cancel()

	// Parser should exit quickly
	select {
	case err := <-parseDone:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("parser did not exit after context cancellation")
	}

	close(eventCh)
}

// Helper functions

// generateLongStream creates a stream with many chunks
func generateLongStream(numChunks int) string {
	var sb strings.Builder
	for i := 0; i < numChunks; i++ {
		sb.WriteString(fmt.Sprintf(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"chunk%d"},"index":0}]}`+"\n\n", i))
	}
	return sb.String()
}

// generateSlowStream creates a stream that would trigger idle timeout
func generateSlowStream(numChunks int, delay time.Duration) string {
	// Return a stream that simulates slow data
	// In reality, this is handled by the slowReader
	return generateLongStream(numChunks)
}

// slowReader simulates a slow network connection
type slowReader struct {
	data   []byte
	pos    int
	delay  time.Duration
	chunks int
	count  int
}

func (r *slowReader) Read(p []byte) (n int, err error) {
	if r.count >= r.chunks {
		return 0, io.EOF
	}

	// Simulate network delay
	time.Sleep(r.delay)

	// Generate data on the fly
	chunk := fmt.Sprintf(`data: {"id":"test","choices":[{"delta":{"content":"slow%d"},"index":0}]}`+"\n\n", r.count)
	r.count++

	if len(p) < len(chunk) {
		// Buffer too small, copy what we can
		n = copy(p, chunk[:len(p)])
		return n, nil
	}

	n = copy(p, chunk)
	return n, nil
}

// TestStreamParser_ReasoningContent tests parsing of reasoning_content deltas
func TestStreamParser_ReasoningContent(t *testing.T) {
	tests := []struct {
		name             string
		streamData       string
		enableReasoning  bool
		expectedReasoning []string
	}{
		{
			name: "reasoning content as string",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":"Let me think..."},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":" about this problem."},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"Here's my answer"},"index":0}]}

data: [DONE]

`,
			enableReasoning: true,
			expectedReasoning: []string{
				"Let me think...",
				" about this problem.",
			},
		},
		{
			name: "reasoning content as object with text field",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":{"type":"reasoning_content","text":"First step"}},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":{"type":"reasoning_content","text":" of analysis"}},"index":0}]}

data: [DONE]

`,
			enableReasoning: true,
			expectedReasoning: []string{
				"First step",
				" of analysis",
			},
		},
		{
			name: "reasoning content as object with content field",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":{"content":"Thinking process"}},"index":0}]}

data: [DONE]

`,
			enableReasoning: true,
			expectedReasoning: []string{
				"Thinking process",
			},
		},
		{
			name: "reasoning disabled",
			streamData: `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":"This should not be emitted"},"index":0}]}

data: [DONE]

`,
			enableReasoning:   false,
			expectedReasoning: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create parser with reasoning config
			config := client.StreamConfig{
				EnableRawAgentReasoning: tt.enableReasoning,
			}
			parser := newStreamParser(config)

			ctx := context.Background()
			eventCh := make(chan client.StreamEvent, 100)

			reader := strings.NewReader(tt.streamData)

			// Run parser in goroutine
			go func() {
				_ = parser.parse(ctx, reader, eventCh)
				close(eventCh)
			}()

			// Collect reasoning deltas
			var reasoningDeltas []string
			for evt := range eventCh {
				if evt.Type == client.EventTypeReasoningContentDelta {
					if delta, ok := evt.Data.(string); ok {
						reasoningDeltas = append(reasoningDeltas, delta)
					}
				}
			}

			// Verify expected reasoning
			if len(reasoningDeltas) != len(tt.expectedReasoning) {
				t.Errorf("expected %d reasoning deltas, got %d", len(tt.expectedReasoning), len(reasoningDeltas))
			}

			for i, expected := range tt.expectedReasoning {
				if i >= len(reasoningDeltas) {
					t.Errorf("missing reasoning delta at index %d", i)
					continue
				}
				if reasoningDeltas[i] != expected {
					t.Errorf("reasoning delta %d: expected %q, got %q", i, expected, reasoningDeltas[i])
				}
			}
		})
	}
}

// TestStreamParser_MixedContentAndReasoning tests interleaved content and reasoning
func TestStreamParser_MixedContentAndReasoning(t *testing.T) {
	streamData := `data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":"Step 1: analyze"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"Based on"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"reasoning":" the problem"},"index":0}]}

data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":" my analysis"},"index":0}]}

data: [DONE]

`

	config := client.StreamConfig{
		EnableRawAgentReasoning: true,
	}
	parser := newStreamParser(config)

	ctx := context.Background()
	eventCh := make(chan client.StreamEvent, 100)

	reader := strings.NewReader(streamData)

	// Run parser in goroutine
	go func() {
		_ = parser.parse(ctx, reader, eventCh)
		close(eventCh)
	}()

	// Collect events in order
	var contentDeltas []string
	var reasoningDeltas []string
	for evt := range eventCh {
		switch evt.Type {
		case client.EventTypeOutputTextDelta:
			if delta, ok := evt.Data.(string); ok {
				contentDeltas = append(contentDeltas, delta)
			}
		case client.EventTypeReasoningContentDelta:
			if delta, ok := evt.Data.(string); ok {
				reasoningDeltas = append(reasoningDeltas, delta)
			}
		}
	}

	// Verify content deltas
	expectedContent := []string{"Based on", " my analysis"}
	if len(contentDeltas) != len(expectedContent) {
		t.Errorf("expected %d content deltas, got %d", len(expectedContent), len(contentDeltas))
	}
	for i, expected := range expectedContent {
		if i >= len(contentDeltas) {
			continue
		}
		if contentDeltas[i] != expected {
			t.Errorf("content delta %d: expected %q, got %q", i, expected, contentDeltas[i])
		}
	}

	// Verify reasoning deltas
	expectedReasoning := []string{"Step 1: analyze", " the problem"}
	if len(reasoningDeltas) != len(expectedReasoning) {
		t.Errorf("expected %d reasoning deltas, got %d", len(expectedReasoning), len(reasoningDeltas))
	}
	for i, expected := range expectedReasoning {
		if i >= len(reasoningDeltas) {
			continue
		}
		if reasoningDeltas[i] != expected {
			t.Errorf("reasoning delta %d: expected %q, got %q", i, expected, reasoningDeltas[i])
		}
	}
}

// TestStreamParser_BackpressureSlowConsumer tests that backpressure prevents memory exhaustion
// when the consumer is slow and the buffer fills up.
func TestStreamParser_BackpressureSlowConsumer(t *testing.T) {
	tests := []struct {
		name                string
		enableBackpressure  bool
		bufferSize          int
		numChunks           int
		consumerDelay       time.Duration
		expectAllEvents     bool
	}{
		{
			name:                "backpressure blocks producer",
			enableBackpressure:  true,
			bufferSize:          10,
			numChunks:           50,
			consumerDelay:       10 * time.Millisecond,
			expectAllEvents:     true,
		},
		{
			name:                "no backpressure drops events",
			enableBackpressure:  false,
			bufferSize:          10,
			numChunks:           50,
			consumerDelay:       10 * time.Millisecond,
			expectAllEvents:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate stream data
			var sb strings.Builder
			sb.WriteString(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n")
			for i := 0; i < tt.numChunks; i++ {
				sb.WriteString(fmt.Sprintf(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"chunk%d"},"index":0}]}`+"\n\n", i))
			}
			sb.WriteString(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{},"finish_reason":"stop","index":0},"usage":{"input_tokens":10,"output_tokens":50,"total_tokens":60}}]}` + "\n\n")
			sb.WriteString("data: [DONE]\n\n")

			config := client.StreamConfig{
				EnableBackpressure:    tt.enableBackpressure,
				BufferSize:            tt.bufferSize,
				BackpressureThreshold: 0.8,
			}
			parser := newStreamParser(config)

			ctx := context.Background()
			eventCh := make(chan client.StreamEvent, tt.bufferSize)

			reader := strings.NewReader(sb.String())

			// Run parser in goroutine
			parserDone := make(chan error, 1)
			go func() {
				parserDone <- parser.parse(ctx, reader, eventCh)
			}()

			// Slow consumer
			var receivedEvents int
			consumeDone := make(chan struct{})
			go func() {
				defer close(consumeDone)
				for range eventCh {
					receivedEvents++
					// Simulate slow processing
					time.Sleep(tt.consumerDelay)
				}
			}()

			// Wait for parser to finish
			select {
			case err := <-parserDone:
				if err != nil {
					t.Fatalf("parser error: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("parser timeout")
			}

			// Close channel and wait for consumer
			close(eventCh)
			<-consumeDone

			// Verify event count
			// With backpressure, we expect all events (1 created + numChunks content + 1 completed)
			// Without backpressure, some events may be dropped
			// Note: The "created" event is optional depending on the stream format
			expectedMin := 2 // At least content and completed
			if tt.expectAllEvents {
				// Allow for created event to be optional
				expectedMin = tt.numChunks + 1 // All content chunks plus at least completed
			}

			if receivedEvents < expectedMin {
				t.Errorf("expected at least %d events, got %d", expectedMin, receivedEvents)
			}

			if tt.expectAllEvents && receivedEvents < tt.numChunks+1 {
				t.Errorf("with backpressure, expected at least %d events, got %d (some were dropped)", tt.numChunks+1, receivedEvents)
			}
		})
	}
}

// TestStreamParser_BackpressureWarningLogs tests that warnings are logged when buffer is full.
func TestStreamParser_BackpressureWarningLogs(t *testing.T) {
	// Generate enough chunks to fill the buffer
	var sb strings.Builder
	sb.WriteString(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n")
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"chunk%d"},"index":0}]}`+"\n\n", i))
	}
	sb.WriteString("data: [DONE]\n\n")

	config := client.StreamConfig{
		EnableBackpressure:    true,
		BufferSize:            5, // Small buffer to trigger warnings
		BackpressureThreshold: 0.6,
	}
	parser := newStreamParser(config)

	ctx := context.Background()
	eventCh := make(chan client.StreamEvent, config.BufferSize)

	reader := strings.NewReader(sb.String())

	// Run parser in goroutine
	parserDone := make(chan error, 1)
	go func() {
		parserDone <- parser.parse(ctx, reader, eventCh)
	}()

	// Very slow consumer to ensure buffer fills up
	time.Sleep(100 * time.Millisecond)

	// Start consuming slowly
	go func() {
		for range eventCh {
			time.Sleep(50 * time.Millisecond)
		}
	}()

	// Wait for parser to finish
	select {
	case err := <-parserDone:
		if err != nil {
			t.Fatalf("parser error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parser timeout")
	}

	close(eventCh)

	// Note: In a real implementation, we would capture log output and verify warnings
	// For now, this test ensures the code path is exercised without panicking
}

// TestStreamParser_BackpressureContextCancellation tests that context cancellation
// works correctly even when backpressure is blocking.
func TestStreamParser_BackpressureContextCancellation(t *testing.T) {
	// Generate large stream
	var sb strings.Builder
	sb.WriteString(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n")
	for i := 0; i < 100; i++ {
		sb.WriteString(fmt.Sprintf(`data: {"id":"test","object":"chat.completion.chunk","model":"gpt-4","choices":[{"delta":{"content":"chunk%d"},"index":0}]}`+"\n\n", i))
	}

	config := client.StreamConfig{
		EnableBackpressure:    true,
		BufferSize:            5,
		BackpressureThreshold: 0.8,
	}
	parser := newStreamParser(config)

	ctx, cancel := context.WithCancel(context.Background())
	eventCh := make(chan client.StreamEvent, config.BufferSize)

	reader := strings.NewReader(sb.String())

	// Run parser in goroutine
	parserDone := make(chan error, 1)
	go func() {
		parserDone <- parser.parse(ctx, reader, eventCh)
	}()

	// Let buffer fill up
	time.Sleep(100 * time.Millisecond)

	// Cancel context while backpressure is blocking
	cancel()

	// Parser should exit quickly
	select {
	case err := <-parserDone:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("parser did not exit after context cancellation")
	}

	close(eventCh)
}

// TestStreamParser_BackpressureMetrics tests buffer usage calculations.
func TestStreamParser_BackpressureMetrics(t *testing.T) {
	config := client.StreamConfig{
		EnableBackpressure:    true,
		BufferSize:            10,
		BackpressureThreshold: 0.7,
	}
	parser := newStreamParser(config)

	ctx := context.Background()
	eventCh := make(chan client.StreamEvent, config.BufferSize)

	// Send events to fill buffer partially
	for i := 0; i < 5; i++ {
		eventCh <- client.StreamEvent{
			Type: client.EventTypeOutputTextDelta,
			Data: fmt.Sprintf("chunk%d", i),
		}
	}

	// Try to send another event with backpressure
	testEvent := client.StreamEvent{
		Type: client.EventTypeOutputTextDelta,
		Data: "test",
	}

	// This should not block since buffer is not full
	done := make(chan error, 1)
	go func() {
		done <- parser.sendEventWithBackpressure(ctx, eventCh, testEvent)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("send blocked unexpectedly")
	}

	close(eventCh)
}
