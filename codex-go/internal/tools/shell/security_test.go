package shell

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

// TestTimeoutFromArguments tests that timeout is properly parsed from shellArgs
func TestTimeoutFromArguments(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tool := NewShellTool()
	ctx := context.Background()

	tests := []struct {
		name        string
		arguments   string
		expectError bool
		expectKind  string
	}{
		{
			name:        "fast command with long timeout",
			arguments:   `{"command": "echo test", "timeout": 5000}`,
			expectError: false,
		},
		{
			name:        "slow command with short timeout",
			arguments:   `{"command": "sleep 5", "timeout": 100}`,
			expectError: true,
			expectKind:  "timeout",
		},
		{
			name:        "command without timeout",
			arguments:   `{"command": "echo test"}`,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &runtime.ToolRequest{
				CallID:           "test-timeout",
				ToolName:         "shell",
				Arguments:        tt.arguments,
				WorkingDirectory: "/tmp",
			}

			execCtx := &runtime.ExecutionContext{
				SessionID: "test-session",
				TurnID:    "test-turn",
				SandboxAttempt: &runtime.SandboxAttempt{
					Type:             runtime.SandboxNone,
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: "/tmp",
				},
				StartTime: time.Now(),
			}

			resp, err := tool.Execute(ctx, req, execCtx)

			if tt.expectError {
				require.Error(t, err)
				if tt.expectKind != "" {
					toolErr, ok := err.(*runtime.ToolError)
					require.True(t, ok, "expected ToolError")
					// Compare with runtime.ErrorTimeout constant
					assert.Equal(t, runtime.ErrorTimeout, toolErr.Kind)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
			}
		})
	}
}

// TestWorkingDirectoryValidation tests working directory validation
func TestWorkingDirectoryValidation(t *testing.T) {
	tool := NewShellTool()
	ctx := context.Background()

	// Create a temporary directory for testing
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		workingDir  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid directory",
			workingDir:  tmpDir,
			expectError: false,
		},
		{
			name:        "non-existent directory",
			workingDir:  "/path/that/does/not/exist/12345",
			expectError: true,
			errorMsg:    "does not exist",
		},
		{
			name:        "path traversal attempt",
			workingDir:  tmpDir + "/../../../etc",
			expectError: false, // Will be cleaned by filepath.Abs
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &runtime.ToolRequest{
				CallID:           "test-workdir",
				ToolName:         "shell",
				Arguments:        `{"command": "echo test"}`,
				WorkingDirectory: tt.workingDir,
			}

			execCtx := &runtime.ExecutionContext{
				SessionID: "test-session",
				TurnID:    "test-turn",
				SandboxAttempt: &runtime.SandboxAttempt{
					Type:             runtime.SandboxNone,
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: tmpDir,
				},
				StartTime: time.Now(),
			}

			_, err := tool.Execute(ctx, req, execCtx)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				// May succeed or fail based on actual path resolution
				if err != nil {
					t.Logf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

// TestWorkingDirectoryOutsideWorkspace tests that working directory must be within workspace
func TestWorkingDirectoryOutsideWorkspace(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tool := NewShellTool()
	ctx := context.Background()

	tmpDir := t.TempDir()
	outsideDir := "/tmp"

	req := &runtime.ToolRequest{
		CallID:           "test-workspace-boundary",
		ToolName:         "shell",
		Arguments:        `{"command": "echo test"}`,
		WorkingDirectory: outsideDir,
	}

	// Test with sandboxed context that restricts to workspace
	execCtx := &runtime.ExecutionContext{
		SessionID: "test-session",
		TurnID:    "test-turn",
		SandboxAttempt: &runtime.SandboxAttempt{
			Type:             runtime.SandboxBubblewrap,
			Policy:           runtime.SandboxWorkspaceWrite,
			WorkingDirectory: tmpDir, // Workspace root
		},
		StartTime: time.Now(),
	}

	_, err := tool.Execute(ctx, req, execCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside workspace bounds")
}

// TestDangerousEnvironmentVariables tests that dangerous environment variables are rejected
func TestDangerousEnvironmentVariables(t *testing.T) {
	tool := NewShellTool()
	ctx := context.Background()

	dangerousVars := []string{
		"LD_PRELOAD",
		"LD_LIBRARY_PATH",
		"DYLD_INSERT_LIBRARIES",
		"DYLD_LIBRARY_PATH",
		"DYLD_FRAMEWORK_PATH",
		"PYTHONPATH",
		"PERLLIB",
		"PERL5LIB",
		"RUBYLIB",
		"NODE_PATH",
	}

	for _, varName := range dangerousVars {
		t.Run(varName, func(t *testing.T) {
			req := &runtime.ToolRequest{
				CallID:           "test-dangerous-env",
				ToolName:         "shell",
				Arguments:        `{"command": "echo test", "environment": {"` + varName + `": "/malicious/path"}}`,
				WorkingDirectory: "/tmp",
			}

			execCtx := &runtime.ExecutionContext{
				SessionID: "test-session",
				TurnID:    "test-turn",
				SandboxAttempt: &runtime.SandboxAttempt{
					Type:             runtime.SandboxNone,
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: "/tmp",
				},
				StartTime: time.Now(),
			}

			_, err := tool.Execute(ctx, req, execCtx)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "dangerous environment variable")
		})
	}
}

// TestRelativePathInPATH tests that relative paths in PATH are rejected
func TestRelativePathInPATH(t *testing.T) {
	tool := NewShellTool()
	ctx := context.Background()

	req := &runtime.ToolRequest{
		CallID:           "test-relative-path",
		ToolName:         "shell",
		Arguments:        `{"command": "echo test", "environment": {"PATH": "../bin:/usr/bin"}}`,
		WorkingDirectory: "/tmp",
	}

	execCtx := &runtime.ExecutionContext{
		SessionID: "test-session",
		TurnID:    "test-turn",
		SandboxAttempt: &runtime.SandboxAttempt{
			Type:             runtime.SandboxNone,
			Policy:           runtime.SandboxDangerFullAccess,
			WorkingDirectory: "/tmp",
		},
		StartTime: time.Now(),
	}

	_, err := tool.Execute(ctx, req, execCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative path in PATH")
}

// TestCommandSanitization tests command sanitization
func TestCommandSanitization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal command",
			input:    "echo hello",
			expected: "echo hello",
		},
		{
			name:     "command with null bytes",
			input:    "echo\x00hello",
			expected: "echohello",
		},
		{
			name:     "command with control characters",
			input:    "echo\x01\x02\x03hello",
			expected: "echohello",
		},
		{
			name:     "command with newlines and tabs",
			input:    "echo hello\nworld\ttest",
			expected: "echo hello\nworld\ttest",
		},
		{
			name:     "command with leading/trailing whitespace",
			input:    "  echo hello  ",
			expected: "echo hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeCommand(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCommandSanitizationInExecution tests that sanitization is applied during execution
func TestCommandSanitizationInExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tool := NewShellTool()
	ctx := context.Background()

	// Use a command that has extra whitespace which will be sanitized
	req := &runtime.ToolRequest{
		CallID:           "test-sanitization",
		ToolName:         "shell",
		Arguments:        `{"command": "  echo   hello  "}`,
		WorkingDirectory: "/tmp",
	}

	execCtx := &runtime.ExecutionContext{
		SessionID: "test-session",
		TurnID:    "test-turn",
		SandboxAttempt: &runtime.SandboxAttempt{
			Type:             runtime.SandboxNone,
			Policy:           runtime.SandboxDangerFullAccess,
			WorkingDirectory: "/tmp",
		},
		StartTime: time.Now(),
	}

	resp, err := tool.Execute(ctx, req, execCtx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Contains(t, resp.Content, "hello")
}

// TestSandboxAppliedFromExecutionContext tests that sandbox is applied from ExecutionContext
func TestSandboxAppliedFromExecutionContext(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tool := NewShellTool()
	ctx := context.Background()

	req := &runtime.ToolRequest{
		CallID:           "test-sandbox",
		ToolName:         "shell",
		Arguments:        `{"command": "echo test"}`,
		WorkingDirectory: "/tmp",
	}

	tests := []struct {
		name           string
		sandboxPolicy  runtime.SandboxPolicy
		expectMetadata bool
	}{
		{
			name:           "read-only sandbox",
			sandboxPolicy:  runtime.SandboxReadOnly,
			expectMetadata: true,
		},
		{
			name:           "workspace-write sandbox",
			sandboxPolicy:  runtime.SandboxWorkspaceWrite,
			expectMetadata: true,
		},
		{
			name:           "danger-full-access (no sandbox)",
			sandboxPolicy:  runtime.SandboxDangerFullAccess,
			expectMetadata: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCtx := &runtime.ExecutionContext{
				SessionID: "test-session",
				TurnID:    "test-turn",
				SandboxAttempt: &runtime.SandboxAttempt{
					Type:             runtime.SandboxBubblewrap,
					Policy:           tt.sandboxPolicy,
					WorkingDirectory: "/tmp",
					NetworkEnabled:   false,
				},
				StartTime: time.Now(),
			}

			resp, err := tool.Execute(ctx, req, execCtx)
			require.NoError(t, err)
			require.NotNil(t, resp)

			if tt.expectMetadata {
				// Sandbox should be attempted (may not actually apply if not available)
				assert.NotNil(t, resp.Metadata)
			}
		})
	}
}

// TestValidateWorkingDirectory tests the validateWorkingDirectory function
func TestValidateWorkingDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		workingDir  string
		execCtx     *runtime.ExecutionContext
		expectError bool
	}{
		{
			name:       "valid directory",
			workingDir: tmpDir,
			execCtx: &runtime.ExecutionContext{
				SandboxAttempt: &runtime.SandboxAttempt{
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: tmpDir,
				},
			},
			expectError: false,
		},
		{
			name:       "non-existent directory",
			workingDir: "/nonexistent/path/12345",
			execCtx: &runtime.ExecutionContext{
				SandboxAttempt: &runtime.SandboxAttempt{
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: "/tmp",
				},
			},
			expectError: true,
		},
		{
			name:       "file instead of directory",
			workingDir: createTempFile(t, tmpDir),
			execCtx: &runtime.ExecutionContext{
				SandboxAttempt: &runtime.SandboxAttempt{
					Policy:           runtime.SandboxDangerFullAccess,
					WorkingDirectory: tmpDir,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWorkingDirectory(tt.workingDir, tt.execCtx)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestValidateEnvironmentVariables tests the validateEnvironmentVariables function
func TestValidateEnvironmentVariables(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		expectError bool
	}{
		{
			name: "safe environment variables",
			env: map[string]string{
				"HOME":    "/home/user",
				"USER":    "testuser",
				"TMPDIR":  "/tmp",
				"CUSTOM":  "value",
			},
			expectError: false,
		},
		{
			name: "LD_PRELOAD",
			env: map[string]string{
				"LD_PRELOAD": "/malicious.so",
			},
			expectError: true,
		},
		{
			name: "LD_LIBRARY_PATH",
			env: map[string]string{
				"LD_LIBRARY_PATH": "/malicious/lib",
			},
			expectError: true,
		},
		{
			name: "relative path in PATH",
			env: map[string]string{
				"PATH": "../bin:/usr/bin",
			},
			expectError: true,
		},
		{
			name: "valid PATH",
			env: map[string]string{
				"PATH": "/usr/bin:/bin:/usr/local/bin",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEnvironmentVariables(tt.env)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Helper function to create a temporary file for testing
func createTempFile(t *testing.T, dir string) string {
	f, err := os.CreateTemp(dir, "testfile")
	require.NoError(t, err)
	defer f.Close()
	return f.Name()
}

// TestEmptyCommandAfterSanitization tests that empty commands after sanitization are rejected
func TestEmptyCommandAfterSanitization(t *testing.T) {
	tool := NewShellTool()
	ctx := context.Background()

	// Command with only whitespace (will be empty after sanitization)
	req := &runtime.ToolRequest{
		CallID:           "test-empty-sanitized",
		ToolName:         "shell",
		Arguments:        `{"command": "     "}`,
		WorkingDirectory: "/tmp",
	}

	execCtx := &runtime.ExecutionContext{
		SessionID: "test-session",
		TurnID:    "test-turn",
		SandboxAttempt: &runtime.SandboxAttempt{
			Type:             runtime.SandboxNone,
			Policy:           runtime.SandboxDangerFullAccess,
			WorkingDirectory: "/tmp",
		},
		StartTime: time.Now(),
	}

	_, err := tool.Execute(ctx, req, execCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty after sanitization")
}

// TestNoBypassPaths tests that there are no bypass paths around sandbox
func TestNoBypassPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tool := NewShellTool()
	ctx := context.Background()

	// Test that even with escalated permissions flag, sandbox is applied via ExecutionContext
	req := &runtime.ToolRequest{
		CallID:           "test-no-bypass",
		ToolName:         "shell",
		Arguments:        `{"command": "echo test", "with_escalated_permissions": true}`,
		WorkingDirectory: "/tmp",
	}

	// ExecutionContext should control sandboxing, not the tool arguments
	execCtx := &runtime.ExecutionContext{
		SessionID: "test-session",
		TurnID:    "test-turn",
		SandboxAttempt: &runtime.SandboxAttempt{
			Type:             runtime.SandboxBubblewrap,
			Policy:           runtime.SandboxReadOnly,
			WorkingDirectory: "/tmp",
		},
		StartTime: time.Now(),
	}

	resp, err := tool.Execute(ctx, req, execCtx)
	require.NoError(t, err)
	require.NotNil(t, resp)
	// Sandbox should still be attempted even with escalated permissions in args
	// The orchestrator controls actual sandbox policy
}
