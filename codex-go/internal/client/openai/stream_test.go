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
