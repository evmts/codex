package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/evmts/codex/codex-go/internal/client"
	"github.com/evmts/codex/codex-go/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewClient tests the creation of a new OpenAI client.
func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  client.ClientConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: client.ClientConfig{
				BaseURL:        "https://api.openai.com/v1",
				APIKey:         "test-key",
				Model:          "gpt-4",
				RequestTimeout: 30 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "missing base URL",
			config: client.ClientConfig{
				APIKey:         "test-key",
				Model:          "gpt-4",
				RequestTimeout: 30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "missing API key",
			config: client.ClientConfig{
				BaseURL:        "https://api.openai.com/v1",
				Model:          "gpt-4",
				RequestTimeout: 30 * time.Second,
			},
			wantErr: true,
		},
		{
			name: "missing model",
			config: client.ClientConfig{
				BaseURL:        "https://api.openai.com/v1",
				APIKey:         "test-key",
				RequestTimeout: 30 * time.Second,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, c)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, c)
			}
		})
	}
}

// TestComplete tests non-streaming completions.
func TestComplete(t *testing.T) {
	tests := []struct {
		name           string
		request        *client.ChatCompletionRequest
		mockResponse   *client.ChatCompletionResponse
		mockStatusCode int
		wantErr        bool
		errType        interface{}
	}{
		{
			name: "successful completion",
			request: &client.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []client.Message{
					client.NewUserMessage("Hello"),
				},
			},
			mockResponse: &client.ChatCompletionResponse{
				ID:      "chatcmpl-123",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.Choice{
					{
						Index: 0,
						Message: client.Message{
							Role:    "assistant",
							Content: "Hi there!",
						},
						FinishReason: "stop",
					},
				},
				Usage: &client.TokenUsage{
					InputTokens:  10,
					OutputTokens: 5,
					TotalTokens:  15,
				},
			},
			mockStatusCode: http.StatusOK,
			wantErr:        false,
		},
		{
			name: "with tool calls",
			request: &client.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []client.Message{
					client.NewUserMessage("What's the weather?"),
				},
				Tools: []client.Tool{
					client.NewFunctionTool("get_weather", "Get weather", json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`)),
				},
			},
			mockResponse: &client.ChatCompletionResponse{
				ID:      "chatcmpl-124",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.Choice{
					{
						Index: 0,
						Message: client.Message{
							Role:    "assistant",
							Content: "",
							ToolCalls: []client.ToolCall{
								{
									ID:   "call_123",
									Type: "function",
									Function: &client.FunctionCall{
										Name:      "get_weather",
										Arguments: `{"location":"San Francisco"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			},
			mockStatusCode: http.StatusOK,
			wantErr:        false,
		},
		{
			name: "rate limited - 429",
			request: &client.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []client.Message{
					client.NewUserMessage("Hello"),
				},
			},
			mockStatusCode: http.StatusTooManyRequests,
			wantErr:        true,
			errType:        &client.UsageLimitError{}, // 429 returns UsageLimitError
		},
		{
			name: "server error - 500",
			request: &client.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []client.Message{
					client.NewUserMessage("Hello"),
				},
			},
			mockStatusCode: http.StatusInternalServerError,
			wantErr:        true,
			errType:        &client.UnexpectedStatusError{},
		},
		{
			name: "context window exceeded",
			request: &client.ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []client.Message{
					client.NewUserMessage(strings.Repeat("x", 100000)),
				},
			},
			mockStatusCode: http.StatusBadRequest,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mockServer *test.HTTPMockServer

			if tt.mockResponse != nil {
				mockServer = test.NewJSONMockServer(t, tt.mockStatusCode, tt.mockResponse)
			} else {
				mockServer = test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tt.mockStatusCode)
					if tt.mockStatusCode == http.StatusBadRequest {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error": map[string]interface{}{
								"message": "context_length_exceeded",
								"type":    "invalid_request_error",
							},
						})
					} else if tt.mockStatusCode == http.StatusTooManyRequests {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error": map[string]interface{}{
								"message": "Rate limit exceeded",
								"type":    "rate_limit_error",
							},
						})
					} else if tt.mockStatusCode >= 500 {
						json.NewEncoder(w).Encode(map[string]interface{}{
							"error": map[string]interface{}{
								"message": "Internal server error",
								"type":    "server_error",
							},
						})
					}
				})
			}

			cfg := client.ClientConfig{
				BaseURL:        mockServer.URL,
				APIKey:         "test-key",
				Model:          "gpt-4",
				RequestTimeout: 5 * time.Second,
				RetryConfig: client.RetryConfig{
					MaxRetries:           0, // Disable retries for simpler tests
					RetryableStatusCodes: []int{429, 500, 502, 503, 504},
				},
			}

			c, err := NewClient(cfg)
			require.NoError(t, err)

			ctx := test.ContextWithTimeout(t, 5*time.Second)
			resp, err := c.Complete(ctx, tt.request)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.IsType(t, tt.errType, err)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.Equal(t, tt.mockResponse.ID, resp.ID)
				assert.Equal(t, tt.mockResponse.Model, resp.Model)
				assert.Len(t, resp.Choices, len(tt.mockResponse.Choices))
			}
		})
	}
}

// TestStream tests streaming completions.
func TestStream(t *testing.T) {
	t.Run("successful stream", func(t *testing.T) {
		mockServer := test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			// Verify streaming is requested
			var req client.ChatCompletionRequest
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &req)
			assert.True(t, req.Stream)

			// Send SSE stream
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			flusher := w.(http.Flusher)

			// Send initial chunk
			w.Write([]byte("data: " + mustMarshal(t, client.ChatCompletionChunk{
				ID:      "chatcmpl-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.ChunkChoice{
					{
						Index: 0,
						Delta: client.MessageDelta{
							Role:    "assistant",
							Content: "",
						},
					},
				},
			}) + "\n\n"))
			flusher.Flush()

			// Send content chunks
			for _, text := range []string{"Hello", " ", "world", "!"} {
				w.Write([]byte("data: " + mustMarshal(t, client.ChatCompletionChunk{
					ID:      "chatcmpl-123",
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "gpt-4",
					Choices: []client.ChunkChoice{
						{
							Index: 0,
							Delta: client.MessageDelta{
								Content: text,
							},
						},
					},
				}) + "\n\n"))
				flusher.Flush()
				time.Sleep(10 * time.Millisecond)
			}

			// Send final chunk
			w.Write([]byte("data: " + mustMarshal(t, client.ChatCompletionChunk{
				ID:      "chatcmpl-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.ChunkChoice{
					{
						Index:        0,
						Delta:        client.MessageDelta{},
						FinishReason: "stop",
					},
				},
				Usage: &client.TokenUsage{
					InputTokens:  10,
					OutputTokens: 5,
					TotalTokens:  15,
				},
			}) + "\n\n"))
			flusher.Flush()

			// Send done signal
			w.Write([]byte("data: [DONE]\n\n"))
			flusher.Flush()
		})

		cfg := client.ClientConfig{
			BaseURL:        mockServer.URL,
			APIKey:         "test-key",
			Model:          "gpt-4",
			RequestTimeout: 5 * time.Second,
		}

		c, err := NewClient(cfg)
		require.NoError(t, err)

		ctx := test.ContextWithTimeout(t, 5*time.Second)
		req := &client.ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []client.Message{
				client.NewUserMessage("Hello"),
			},
		}

		eventCh, err := c.Stream(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, eventCh)

		var events []client.StreamEvent
		for event := range eventCh {
			events = append(events, event)
			if event.Error != nil {
				t.Fatalf("unexpected error in stream: %v", event.Error)
			}
		}

		assert.NotEmpty(t, events)
		// Should have text deltas and completion event
		hasTextDelta := false
		hasCompleted := false
		for _, e := range events {
			if e.Type == client.EventTypeOutputTextDelta {
				hasTextDelta = true
			}
			if e.Type == client.EventTypeCompleted {
				hasCompleted = true
			}
		}
		assert.True(t, hasTextDelta, "should have text delta events")
		assert.True(t, hasCompleted, "should have completion event")
	})

	t.Run("stream with context cancellation", func(t *testing.T) {
		mockServer := test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			// Send one chunk then keep connection open
			w.Write([]byte("data: " + mustMarshal(t, client.ChatCompletionChunk{
				ID:      "chatcmpl-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.ChunkChoice{
					{
						Index: 0,
						Delta: client.MessageDelta{
							Role:    "assistant",
							Content: "Hello",
						},
					},
				},
			}) + "\n\n"))
			w.(http.Flusher).Flush()

			// Block to simulate long stream
			time.Sleep(5 * time.Second)
		})

		cfg := client.ClientConfig{
			BaseURL:        mockServer.URL,
			APIKey:         "test-key",
			Model:          "gpt-4",
			RequestTimeout: 30 * time.Second,
		}

		c, err := NewClient(cfg)
		require.NoError(t, err)

		ctx, cancel := test.ContextWithCancel(t)
		req := &client.ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []client.Message{
				client.NewUserMessage("Hello"),
			},
		}

		eventCh, err := c.Stream(ctx, req)
		require.NoError(t, err)

		// Wait for first event
		event := <-eventCh
		assert.NoError(t, event.Error)

		// Cancel context
		cancel()

		// Stream should close
		remainingEvents := 0
		for range eventCh {
			remainingEvents++
		}
		// Channel should close quickly after cancellation
		assert.LessOrEqual(t, remainingEvents, 2)
	})

	t.Run("stream with idle timeout", func(t *testing.T) {
		mockServer := test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			// Send one chunk then go silent
			w.Write([]byte("data: " + mustMarshal(t, client.ChatCompletionChunk{
				ID:      "chatcmpl-123",
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   "gpt-4",
				Choices: []client.ChunkChoice{
					{
						Index: 0,
						Delta: client.MessageDelta{
							Role:    "assistant",
							Content: "Hello",
						},
					},
				},
			}) + "\n\n"))
			w.(http.Flusher).Flush()

			// Simulate timeout
			time.Sleep(2 * time.Second)
		})

		cfg := client.ClientConfig{
			BaseURL:        mockServer.URL,
			APIKey:         "test-key",
			Model:          "gpt-4",
			RequestTimeout: 30 * time.Second,
			StreamConfig: client.StreamConfig{
				IdleTimeout: 500 * time.Millisecond,
				BufferSize:  16,
			},
		}

		c, err := NewClient(cfg)
		require.NoError(t, err)

		ctx := test.ContextWithTimeout(t, 5*time.Second)
		req := &client.ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []client.Message{
				client.NewUserMessage("Hello"),
			},
		}

		eventCh, err := c.Stream(ctx, req)
		require.NoError(t, err)

		// Should get first event, then timeout error
		gotTimeout := false
		for event := range eventCh {
			if event.Error != nil {
				assert.IsType(t, &client.IdleTimeoutError{}, event.Error)
				gotTimeout = true
			}
		}
		assert.True(t, gotTimeout, "should have received idle timeout error")
	})
}

// TestGetModelContextWindow tests context window retrieval.
func TestGetModelContextWindow(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		expectedWindow int64
	}{
		{
			name:           "gpt-4",
			model:          "gpt-4",
			expectedWindow: 8192,
		},
		{
			name:           "gpt-4-turbo",
			model:          "gpt-4-turbo",
			expectedWindow: 128000,
		},
		{
			name:           "gpt-3.5-turbo",
			model:          "gpt-3.5-turbo",
			expectedWindow: 16385,
		},
		{
			name:           "unknown model",
			model:          "unknown-model",
			expectedWindow: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := client.ClientConfig{
				BaseURL:        "https://api.openai.com/v1",
				APIKey:         "test-key",
				Model:          tt.model,
				RequestTimeout: 30 * time.Second,
			}

			c, err := NewClient(cfg)
			require.NoError(t, err)

			window := c.GetModelContextWindow()
			assert.Equal(t, tt.expectedWindow, window)
		})
	}
}

// TestGetAutoCompactTokenLimit tests auto-compact token limit retrieval.
func TestGetAutoCompactTokenLimit(t *testing.T) {
	cfg := client.ClientConfig{
		BaseURL:        "https://api.openai.com/v1",
		APIKey:         "test-key",
		Model:          "gpt-4",
		RequestTimeout: 30 * time.Second,
	}

	c, err := NewClient(cfg)
	require.NoError(t, err)

	limit := c.GetAutoCompactTokenLimit()
	// For gpt-4, should be a reasonable default (e.g., 80% of context window)
	assert.Greater(t, limit, int64(0))
	assert.Less(t, limit, c.GetModelContextWindow())
}

// TestRateLimitParsing tests extraction of rate limit headers.
func TestRateLimitParsing(t *testing.T) {
	mockServer := test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ratelimit-limit-requests", "10000")
		w.Header().Set("x-ratelimit-remaining-requests", "9999")
		w.Header().Set("x-ratelimit-reset-requests", "60s")
		w.Header().Set("x-ratelimit-limit-tokens", "1000000")
		w.Header().Set("x-ratelimit-remaining-tokens", "900000")
		w.Header().Set("x-ratelimit-reset-tokens", "3600s")

		resp := &client.ChatCompletionResponse{
			ID:      "chatcmpl-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4",
			Choices: []client.Choice{
				{
					Index: 0,
					Message: client.Message{
						Role:    "assistant",
						Content: "Test",
					},
					FinishReason: "stop",
				},
			},
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	})

	cfg := client.ClientConfig{
		BaseURL:        mockServer.URL,
		APIKey:         "test-key",
		Model:          "gpt-4",
		RequestTimeout: 5 * time.Second,
	}

	c, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := test.ContextWithTimeout(t, 5*time.Second)
	req := &client.ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []client.Message{
			client.NewUserMessage("Test"),
		},
	}

	resp, err := c.Complete(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Rate limits should be tracked internally
	// This would require exposing rate limit state or adding a getter method
}

// Helper function to marshal JSON for tests
func mustMarshal(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

// mockHTTPClient is a simple HTTP client mock for testing
type mockHTTPClient struct {
	doFunc func(req *client.HTTPRequest) (*client.HTTPResponse, error)
}

func (m *mockHTTPClient) Do(req *client.HTTPRequest) (*client.HTTPResponse, error) {
	return m.doFunc(req)
}

// TestHTTPClientIntegration tests using a custom HTTP client
func TestHTTPClientIntegration(t *testing.T) {
	t.Run("custom HTTP client", func(t *testing.T) {
		mockResp := &client.ChatCompletionResponse{
			ID:      "chatcmpl-123",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "gpt-4",
			Choices: []client.Choice{
				{
					Index: 0,
					Message: client.Message{
						Role:    "assistant",
						Content: "Custom client response",
					},
					FinishReason: "stop",
				},
			},
		}

		mockClient := &mockHTTPClient{
			doFunc: func(req *client.HTTPRequest) (*client.HTTPResponse, error) {
				// Verify request
				assert.Equal(t, "POST", req.Method)
				assert.Contains(t, req.URL, "/chat/completions")
				assert.Equal(t, "Bearer test-key", req.Headers["Authorization"])

				// Return mock response
				body, _ := json.Marshal(mockResp)
				return &client.HTTPResponse{
					StatusCode: http.StatusOK,
					Headers:    make(map[string][]string),
					Body:       io.NopCloser(bytes.NewReader(body)),
				}, nil
			},
		}

		cfg := client.ClientConfig{
			BaseURL:        "https://api.openai.com/v1",
			APIKey:         "test-key",
			Model:          "gpt-4",
			HTTPClient:     mockClient,
			RequestTimeout: 5 * time.Second,
		}

		c, err := NewClient(cfg)
		require.NoError(t, err)

		ctx := test.ContextWithTimeout(t, 5*time.Second)
		req := &client.ChatCompletionRequest{
			Model: "gpt-4",
			Messages: []client.Message{
				client.NewUserMessage("Test"),
			},
		}

		resp, err := c.Complete(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, "Custom client response", resp.Choices[0].Message.Content)
	})
}

// TestRequestTimeout tests request timeout handling
func TestRequestTimeout(t *testing.T) {
	mockServer := test.NewHTTPMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Delay response to trigger timeout
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&client.ChatCompletionResponse{
			ID: "chatcmpl-123",
		})
	})

	cfg := client.ClientConfig{
		BaseURL:        mockServer.URL,
		APIKey:         "test-key",
		Model:          "gpt-4",
		RequestTimeout: 500 * time.Millisecond,
	}

	c, err := NewClient(cfg)
	require.NoError(t, err)

	ctx := test.ContextWithTimeout(t, 5*time.Second)
	req := &client.ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []client.Message{
			client.NewUserMessage("Test"),
		},
	}

	_, err = c.Complete(ctx, req)
	assert.Error(t, err)
	// Should be a timeout or context error
}
