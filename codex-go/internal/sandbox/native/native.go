// Package native provides a native (non-isolated) sandbox implementation.
//
// The native sandbox executes commands directly on the host system without
// any isolation or security restrictions. It's the fastest option but provides
// no protection against malicious or buggy commands.
package native

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/internal/sandbox"
)

// NativeSandbox implements the Sandbox interface using os/exec directly.
// It provides no isolation - commands run with full system access.
type NativeSandbox struct{}

// New creates a new native sandbox instance.
func New() *NativeSandbox {
	return &NativeSandbox{}
}

// Type returns the identifier for this sandbox type.
func (n *NativeSandbox) Type() string {
	return "native"
}

// IsAvailable checks if the native sandbox is available.
// It's always available since it uses standard os/exec.
func (n *NativeSandbox) IsAvailable() bool {
	return true
}

// Cleanup performs any necessary cleanup after command execution.
// Native sandbox has no resources to clean up, so this is a no-op.
func (n *NativeSandbox) Cleanup(ctx context.Context) error {
	return nil
}

// Execute runs a command directly using os/exec.
// It handles stdin/stdout/stderr, working directory, environment variables, and timeouts.
func (n *NativeSandbox) Execute(ctx context.Context, cmd *sandbox.Command) (*sandbox.Result, error) {
	startTime := time.Now()

	// Apply timeout if specified
	if cmd.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cmd.Timeout)
		defer cancel()
	}

	// Create the command with context for cancellation support
	execCmd := exec.CommandContext(ctx, cmd.Program, cmd.Args...)

	// Set working directory if specified
	if cmd.WorkingDirectory != "" {
		execCmd.Dir = cmd.WorkingDirectory
	}

	// Set environment variables if specified
	if len(cmd.Environment) > 0 {
		// Start with the parent process environment
		execCmd.Env = execCmd.Environ()

		// Add custom environment variables
		for key, value := range cmd.Environment {
			execCmd.Env = append(execCmd.Env, key+"="+value)
		}
	}

	// Setup stdin
	if cmd.Stdin != "" {
		execCmd.Stdin = strings.NewReader(cmd.Stdin)
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	// Execute the command
	err := execCmd.Run()

	executionTime := time.Since(startTime)

	// Build result
	result := &sandbox.Result{
		Stdout:        stdout.String(),
		Stderr:        stderr.String(),
		ExitCode:      0,
		ExecutionTime: executionTime,
		Error:         nil,
	}

	// Handle execution error
	if err != nil {
		// Check for context-related errors (timeout, cancellation) first
		if ctx.Err() != nil {
			result.ExitCode = -1
			result.Error = ctx.Err()
			return result, ctx.Err()
		}

		if exitErr, ok := err.(*exec.ExitError); ok {
			// Command ran but returned non-zero exit code
			result.ExitCode = exitErr.ExitCode()
			result.Error = nil // Non-zero exit is not an error in our model
			return result, nil
		}

		// Command failed to start or other system error
		result.ExitCode = -1
		result.Error = err
		return result, err
	}

	return result, nil
}
