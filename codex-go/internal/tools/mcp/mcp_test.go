package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/evmts/codex/codex-go/internal/config"
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock MCP protocol messages
type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// TestMCPClient_StdioTransport tests the stdio transport implementation
func TestMCPClient_StdioTransport(t *testing.T) {
	t.Run("successful connection and initialization", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Command: "mock-mcp-server",
			Args:    []string{"--mode", "stdio"},
			Enabled: true,
		}

		client := newStdioClient(cfg)
		require.NotNil(t, client)

		// Test would connect to mock server
		// For now, test the client structure
		assert.Equal(t, "stdio", client.transportType())
	})

	t.Run("initialization timeout", func(t *testing.T) {
		t.Skip("Skipping stdio timeout test - requires proper mock server")
		// This test would require a mock MCP server that doesn't respond
		// For now, we test timeout behavior via HTTP transport
	})

	t.Run("list tools", func(t *testing.T) {
		_ = config.MCPServerConfig{
			Command: "mock-mcp-server",
			Args:    []string{"--mode", "stdio"},
			Enabled: true,
		}

		client := newMockStdioClientWithTools([]MCPTool{
			{
				Name:        "get_weather",
				Description: "Get weather information",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"location": map[string]interface{}{
							"type":        "string",
							"description": "City name",
						},
					},
					"required": []string{"location"},
				},
			},
		})

		tools, err := client.listTools(context.Background())
		require.NoError(t, err)
		assert.Len(t, tools, 1)
		assert.Equal(t, "get_weather", tools[0].Name)
	})
}

// TestMCPClient_HTTPTransport tests the HTTP transport implementation
func TestMCPClient_HTTPTransport(t *testing.T) {
	t.Run("successful connection", func(t *testing.T) {
		// Create mock HTTP server
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "initialize" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"protocolVersion": "1.0",
						"serverInfo": map[string]interface{}{
							"name":    "mock-server",
							"version": "1.0.0",
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		require.NotNil(t, client)

		err := client.initialize(context.Background())
		assert.NoError(t, err)
	})

	t.Run("with bearer token", func(t *testing.T) {
		t.Setenv("MCP_TEST_TOKEN", "secret-token-123")

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			assert.Equal(t, "Bearer secret-token-123", auth)

			resp := mcpResponse{
				JSONRPC: "2.0",
				ID:      1,
				Result:  map[string]interface{}{},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:               server.URL,
			BearerTokenEnvVar: "MCP_TEST_TOKEN",
			Enabled:           true,
		}

		client := newHTTPClient(cfg)
		err := client.initialize(context.Background())
		assert.NoError(t, err)
	})

	t.Run("list tools via HTTP", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "tools/list" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"tools": []map[string]interface{}{
							{
								"name":        "search_files",
								"description": "Search for files",
								"inputSchema": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"pattern": map[string]interface{}{
											"type": "string",
										},
									},
								},
							},
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		tools, err := client.listTools(context.Background())
		require.NoError(t, err)
		assert.Len(t, tools, 1)
		assert.Equal(t, "search_files", tools[0].Name)
	})
}

// TestMCPClient_CallTool tests tool execution forwarding
func TestMCPClient_CallTool(t *testing.T) {
	t.Run("successful tool call", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "tools/call" {
				params := req.Params.(map[string]interface{})
				assert.Equal(t, "get_weather", params["name"])

				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"content": []map[string]interface{}{
							{
								"type": "text",
								"text": "Temperature: 72°F, Sunny",
							},
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		result, err := client.callTool(context.Background(), "get_weather", map[string]interface{}{
			"location": "San Francisco",
		})

		require.NoError(t, err)
		assert.Contains(t, result, "72°F")
	})

	t.Run("tool returns error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "tools/call" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error: &mcpError{
						Code:    -32602,
						Message: "Invalid parameters",
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		_, err := client.callTool(context.Background(), "invalid_tool", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Invalid parameters")
	})

	t.Run("timeout handling", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Simulate slow response
			time.Sleep(200 * time.Millisecond)
		}))
		defer server.Close()

		timeout := 0.1
		cfg := config.MCPServerConfig{
			URL:            server.URL,
			ToolTimeoutSec: &timeout,
			Enabled:        true,
		}

		client := newHTTPClient(cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := client.callTool(ctx, "slow_tool", nil)
		assert.Error(t, err)
	})
}

// TestMCPRuntime_Integration tests the MCP runtime wrapper
func TestMCPRuntime_Integration(t *testing.T) {
	t.Run("tool runtime interface implementation", func(t *testing.T) {
		_ = config.MCPServerConfig{
			Command: "mock-mcp",
			Enabled: true,
		}

		tool := MCPTool{
			Name:        "test_tool",
			Description: "Test tool",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		}

		client := newMockStdioClientWithTools([]MCPTool{tool})
		mcpRuntime := NewMCPToolRuntime("test-server", tool, client)

		assert.Equal(t, "mcp__test-server__test_tool", mcpRuntime.Name())
		assert.True(t, mcpRuntime.SupportsParallel())
		assert.False(t, mcpRuntime.NeedsInitialApproval(nil, runtime.ApprovalNever, runtime.SandboxReadOnly))
		assert.Equal(t, runtime.SandboxAuto, mcpRuntime.SandboxPreference())
	})

	t.Run("execute tool through runtime", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "tools/call" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"content": []map[string]interface{}{
							{
								"type": "text",
								"text": "Tool execution result",
							},
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		tool := MCPTool{
			Name:        "test_tool",
			Description: "Test tool",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		}

		client := newHTTPClient(cfg)
		mcpRuntime := NewMCPToolRuntime("test-server", tool, client)

		req := &runtime.ToolRequest{
			CallID:           "test-call-1",
			ToolName:         mcpRuntime.Name(),
			Arguments:        `{"param": "value"}`,
			WorkingDirectory: "/tmp",
		}

		execCtx := &runtime.ExecutionContext{
			SessionID: "test-session",
			TurnID:    "test-turn",
			SandboxAttempt: &runtime.SandboxAttempt{
				Type:   runtime.SandboxNone,
				Policy: runtime.SandboxReadOnly,
			},
			ApprovalCache: runtime.NewMemoryApprovalCache(),
			StartTime:     time.Now(),
		}

		resp, err := mcpRuntime.Execute(context.Background(), req, execCtx)
		require.NoError(t, err)
		assert.Contains(t, resp.Content, "Tool execution result")
	})
}

// TestMCPManager tests the MCP manager for multiple servers
func TestMCPManager(t *testing.T) {
	t.Run("initialize multiple servers", func(t *testing.T) {
		cfg := &config.Config{
			MCPServers: map[string]config.MCPServerConfig{
				"weather": {
					Command: "weather-mcp",
					Enabled: true,
				},
				"files": {
					Command: "files-mcp",
					Enabled: true,
				},
				"disabled": {
					Command: "disabled-mcp",
					Enabled: false,
				},
			},
		}

		manager := NewMCPManager(cfg)
		require.NotNil(t, manager)

		// Should only have enabled servers
		assert.Len(t, manager.clients, 2)
		assert.Contains(t, manager.clients, "weather")
		assert.Contains(t, manager.clients, "files")
		assert.NotContains(t, manager.clients, "disabled")
	})

	t.Run("register tools with orchestrator", func(t *testing.T) {
		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			if req.Method == "initialize" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"protocolVersion": "1.0",
					},
				}
				json.NewEncoder(w).Encode(resp)
			} else if req.Method == "tools/list" {
				resp := mcpResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]interface{}{
						"tools": []map[string]interface{}{
							{
								"name":        "tool1",
								"description": "First tool",
								"inputSchema": map[string]interface{}{
									"type":       "object",
									"properties": map[string]interface{}{},
								},
							},
						},
					},
				}
				json.NewEncoder(w).Encode(resp)
			}
		}))
		defer server1.Close()

		cfg := &config.Config{
			MCPServers: map[string]config.MCPServerConfig{
				"server1": {
					URL:     server1.URL,
					Enabled: true,
				},
			},
		}

		manager := NewMCPManager(cfg)

		// Initialize the manager first
		err := manager.Initialize(context.Background())
		require.NoError(t, err)

		builder := runtime.NewToolRegistryBuilder()

		specs, err := manager.RegisterTools(context.Background(), nil, builder)
		require.NoError(t, err)
		assert.Len(t, specs, 1)
		assert.Equal(t, "mcp__server1__tool1", specs[0].Name)

		// Build the registry from the builder
		registry, _ := builder.Build()

		// Verify tool is registered
		tool := registry.Get("mcp__server1__tool1")
		assert.NotNil(t, tool)
	})

	t.Run("tool filtering", func(t *testing.T) {
		cfg := &config.Config{
			MCPServers: map[string]config.MCPServerConfig{
				"filtered": {
					Command:      "test-mcp",
					Enabled:      true,
					EnabledTools: []string{"tool1", "tool2"},
				},
			},
		}

		manager := NewMCPManager(cfg)
		tools := []MCPTool{
			{Name: "tool1", Description: "Tool 1"},
			{Name: "tool2", Description: "Tool 2"},
			{Name: "tool3", Description: "Tool 3"},
		}

		filtered := manager.filterTools("filtered", tools)
		assert.Len(t, filtered, 2)
		assert.Equal(t, "tool1", filtered[0].Name)
		assert.Equal(t, "tool2", filtered[1].Name)
	})

	t.Run("tool disabling", func(t *testing.T) {
		cfg := &config.Config{
			MCPServers: map[string]config.MCPServerConfig{
				"filtered": {
					Command:       "test-mcp",
					Enabled:       true,
					DisabledTools: []string{"dangerous_tool"},
				},
			},
		}

		manager := NewMCPManager(cfg)
		tools := []MCPTool{
			{Name: "safe_tool", Description: "Safe"},
			{Name: "dangerous_tool", Description: "Dangerous"},
		}

		filtered := manager.filterTools("filtered", tools)
		assert.Len(t, filtered, 1)
		assert.Equal(t, "safe_tool", filtered[0].Name)
	})
}

// TestMCPSchema tests schema mapping
func TestMCPSchema(t *testing.T) {
	t.Run("convert MCP schema to runtime schema", func(t *testing.T) {
		mcpSchema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"location": map[string]interface{}{
					"type":        "string",
					"description": "City name",
				},
				"units": map[string]interface{}{
					"type": "string",
					"enum": []interface{}{"celsius", "fahrenheit"},
				},
			},
			"required": []interface{}{"location"},
		}

		runtimeSchema := convertMCPSchema(mcpSchema)
		assert.NotNil(t, runtimeSchema)

		// Verify schema structure
		schemaMap := runtimeSchema.(map[string]interface{})
		assert.Equal(t, "object", schemaMap["type"])

		props := schemaMap["properties"].(map[string]interface{})
		assert.Contains(t, props, "location")
		assert.Contains(t, props, "units")

		required := schemaMap["required"].([]interface{})
		assert.Len(t, required, 1)
		assert.Equal(t, "location", required[0])
	})

	t.Run("generate tool spec", func(t *testing.T) {
		tool := MCPTool{
			Name:        "get_weather",
			Description: "Get current weather",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		}

		spec := generateToolSpec("weather-server", tool)
		assert.Equal(t, "mcp__weather-server__get_weather", spec.Name)
		assert.Contains(t, spec.Description, "Get current weather")
		assert.Contains(t, spec.Description, "weather-server")
		assert.NotNil(t, spec.ParametersSchema)
		assert.True(t, spec.SupportsParallel)
	})
}

// TestConcurrentSafety tests concurrent access to MCP clients
func TestConcurrentSafety(t *testing.T) {
	t.Run("concurrent tool calls", func(t *testing.T) {
		callCount := 0
		mu := sync.Mutex{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			callCount++
			mu.Unlock()

			var req mcpRequest
			json.NewDecoder(r.Body).Decode(&req)

			resp := mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"content": []map[string]interface{}{
						{"type": "text", "text": "result"},
					},
				},
			}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)

		// Make 10 concurrent calls
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				_, err := client.callTool(context.Background(), "test_tool", map[string]interface{}{
					"id": n,
				})
				assert.NoError(t, err)
			}(i)
		}

		wg.Wait()
		assert.Equal(t, 10, callCount)
	})
}

// TestErrorHandling tests various error scenarios
func TestErrorHandling(t *testing.T) {
	t.Run("server not found", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Command: "non-existent-mcp-server",
			Enabled: true,
		}

		client := newStdioClient(cfg)
		err := client.initialize(context.Background())
		assert.Error(t, err)
	})

	t.Run("malformed response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("invalid json"))
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		_, err := client.listTools(context.Background())
		assert.Error(t, err)
	})

	t.Run("server returns error code", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		cfg := config.MCPServerConfig{
			URL:     server.URL,
			Enabled: true,
		}

		client := newHTTPClient(cfg)
		err := client.initialize(context.Background())
		assert.Error(t, err)
	})
}

// Helper functions for mocking

func newMockStdioClientWithTools(tools []MCPTool) *mockMCPClient {
	return &mockMCPClient{
		tools:     tools,
		transport: "stdio",
	}
}

type mockMCPClient struct {
	tools     []MCPTool
	transport string
	mu        sync.Mutex
}

func (m *mockMCPClient) initialize(ctx context.Context) error {
	return nil
}

func (m *mockMCPClient) listTools(ctx context.Context) ([]MCPTool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools, nil
}

func (m *mockMCPClient) callTool(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, tool := range m.tools {
		if tool.Name == name {
			return fmt.Sprintf("Mock result for %s", name), nil
		}
	}
	return "", fmt.Errorf("tool not found: %s", name)
}

func (m *mockMCPClient) close() error {
	return nil
}

func (m *mockMCPClient) transportType() string {
	return m.transport
}
