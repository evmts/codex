package native

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evmts/codex/codex-go/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeSandbox_Type verifies the sandbox type is correct
func TestNativeSandbox_Type(t *testing.T) {
	sb := New()
	assert.Equal(t, "native", sb.Type())
}

// TestNativeSandbox_IsAvailable verifies sandbox availability
func TestNativeSandbox_IsAvailable(t *testing.T) {
	sb := New()
	// Native sandbox is always available
	assert.True(t, sb.IsAvailable())
}

// TestNativeSandbox_Cleanup verifies cleanup is a no-op
func TestNativeSandbox_Cleanup(t *testing.T) {
	sb := New()
	ctx := context.Background()

	// Cleanup should be a no-op and return nil
	err := sb.Cleanup(ctx)
	assert.NoError(t, err)
}

// TestNativeSandbox_Execute_SimpleCommand tests executing a simple command
func TestNativeSandbox_Execute_SimpleCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "echo",
		Args:    []string{"hello", "world"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "hello world")
	assert.Nil(t, result.Error)
}

// TestNativeSandbox_Execute_WithWorkDir tests working directory handling
func TestNativeSandbox_Execute_WithWorkDir(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program:          "pwd",
		Args:             []string{},
		WorkingDirectory: "/tmp",
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "/tmp")
}

// TestNativeSandbox_Execute_WithEnv tests environment variable propagation
func TestNativeSandbox_Execute_WithEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "sh",
		Args:    []string{"-c", "echo $TEST_VAR"},
		Environment: map[string]string{
			"TEST_VAR": "test_value",
		},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "test_value")
}

// TestNativeSandbox_Execute_WithStdin tests stdin handling
func TestNativeSandbox_Execute_WithStdin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "cat",
		Args:    []string{},
		Stdin:   "input data\n",
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "input data\n", result.Stdout)
}

// TestNativeSandbox_Execute_WithStderr tests stderr handling
func TestNativeSandbox_Execute_WithStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "sh",
		Args:    []string{"-c", "echo stdout && echo stderr >&2"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "stdout")
	assert.Contains(t, result.Stderr, "stderr")
}

// TestNativeSandbox_Execute_NonZeroExit tests handling of non-zero exit codes
func TestNativeSandbox_Execute_NonZeroExit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "sh",
		Args:    []string{"-c", "exit 42"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err) // No error, just non-zero exit
	assert.Equal(t, 42, result.ExitCode)
	assert.Nil(t, result.Error)
}

// TestNativeSandbox_Execute_CommandNotFound tests command not found handling
func TestNativeSandbox_Execute_CommandNotFound(t *testing.T) {
	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "nonexistent_command_12345",
		Args:    []string{},
	}

	result, err := sb.Execute(ctx, cmd)
	require.Error(t, err)
	assert.NotEqual(t, 0, result.ExitCode)
	assert.NotNil(t, result.Error)
}

// TestNativeSandbox_Execute_Timeout tests command timeout
func TestNativeSandbox_Execute_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "sh",
		Args:    []string{"-c", "sleep 10"},
		Timeout: 100 * time.Millisecond,
	}

	result, err := sb.Execute(ctx, cmd)
	if err == nil {
		t.Logf("No error returned. Exit code: %d, Stdout: %q, Stderr: %q", result.ExitCode, result.Stdout, result.Stderr)
	}
	require.Error(t, err, "Expected timeout error")
	assert.Contains(t, err.Error(), "deadline exceeded")
	assert.NotEqual(t, 0, result.ExitCode)
}

// TestNativeSandbox_Execute_ContextCancellation tests context cancellation
func TestNativeSandbox_Execute_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	cmd := &sandbox.Command{
		Program: "sleep",
		Args:    []string{"10"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
	assert.NotEqual(t, 0, result.ExitCode)
}

// TestNativeSandbox_Execute_InvalidWorkDir tests invalid working directory
func TestNativeSandbox_Execute_InvalidWorkDir(t *testing.T) {
	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program:          "echo",
		Args:             []string{"test"},
		WorkingDirectory: "/nonexistent/directory/12345",
	}

	result, err := sb.Execute(ctx, cmd)
	require.Error(t, err)
	assert.NotEqual(t, 0, result.ExitCode)
}

// TestNativeSandbox_Execute_Concurrent tests concurrent execution
func TestNativeSandbox_Execute_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	// Run multiple commands concurrently
	numGoroutines := 5
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			cmd := &sandbox.Command{
				Program: "echo",
				Args:    []string{"concurrent", "test"},
			}

			result, err := sb.Execute(ctx, cmd)
			assert.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode)
			assert.Contains(t, result.Stdout, "concurrent")
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent execution")
		}
	}
}

// TestNativeSandbox_Execute_NetworkEnabled tests that network flag is ignored
func TestNativeSandbox_Execute_NetworkEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	tests := []struct {
		name           string
		networkEnabled bool
	}{
		{"network enabled", true},
		{"network disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &sandbox.Command{
				Program:        "echo",
				Args:           []string{"test"},
				NetworkEnabled: tt.networkEnabled,
			}

			result, err := sb.Execute(ctx, cmd)
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode)
			// Native sandbox ignores NetworkEnabled flag - command executes regardless
		})
	}
}

// TestNativeSandbox_Execute_ReadOnlyPaths tests that readonly paths are ignored
func TestNativeSandbox_Execute_ReadOnlyPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program:       "echo",
		Args:          []string{"test"},
		ReadOnlyPaths: []string{"/tmp"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	// Native sandbox ignores ReadOnlyPaths - full filesystem access
}

// TestNativeSandbox_Execute_MultipleInvocations tests multiple sequential invocations
func TestNativeSandbox_Execute_MultipleInvocations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	// Execute multiple commands sequentially
	for i := 0; i < 3; i++ {
		cmd := &sandbox.Command{
			Program: "echo",
			Args:    []string{"iteration", string(rune('0' + i))},
		}

		result, err := sb.Execute(ctx, cmd)
		require.NoError(t, err)
		assert.Equal(t, 0, result.ExitCode)
	}
}

// TestNativeSandbox_Execute_LongOutput tests handling of long output
func TestNativeSandbox_Execute_LongOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	// Generate 1000 lines of output
	cmd := &sandbox.Command{
		Program: "sh",
		Args:    []string{"-c", "for i in $(seq 1 1000); do echo line $i; done"},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify output contains lines
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	assert.Len(t, lines, 1000)
}

// TestNativeSandbox_Execute_MixedStdoutStderr tests mixed stdout/stderr
func TestNativeSandbox_Execute_MixedStdoutStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := New()
	ctx := context.Background()

	cmd := &sandbox.Command{
		Program: "sh",
		Args: []string{"-c", `
			echo "line1 stdout"
			echo "line1 stderr" >&2
			echo "line2 stdout"
			echo "line2 stderr" >&2
		`},
	}

	result, err := sb.Execute(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "line1 stdout")
	assert.Contains(t, result.Stdout, "line2 stdout")
	assert.Contains(t, result.Stderr, "line1 stderr")
	assert.Contains(t, result.Stderr, "line2 stderr")
}

// BenchmarkNativeSandbox_Execute benchmarks command execution
func BenchmarkNativeSandbox_Execute(b *testing.B) {
	sb := New()
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cmd := &sandbox.Command{
			Program: "echo",
			Args:    []string{"benchmark"},
		}

		_, err := sb.Execute(ctx, cmd)
		if err != nil {
			b.Fatal(err)
		}
	}
}
