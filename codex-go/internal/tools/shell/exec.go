package shell

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/internal/sandbox"
	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

// CommandSpec defines the specification for executing a command.
type CommandSpec struct {
	// Command is the full command array (program + args)
	Command []string

	// WorkingDirectory is where the command should execute
	WorkingDirectory string

	// Environment contains additional environment variables
	Environment map[string]string

	// CallID identifies this command execution
	CallID string

	// SandboxPolicy specifies the sandbox policy to apply (optional)
	SandboxPolicy *sandbox.PolicyConfig
}

// CommandExecutor handles the execution of shell commands.
type CommandExecutor struct {
	sandboxManager *sandbox.SandboxManager
}

// NewCommandExecutor creates a new command executor.
func NewCommandExecutor() *CommandExecutor {
	return &CommandExecutor{
		sandboxManager: sandbox.NewSandboxManager(),
	}
}

// Execute runs a command and returns the result.
func (e *CommandExecutor) Execute(ctx context.Context, spec *CommandSpec, execCtx *runtime.ExecutionContext) (*runtime.ToolResponse, error) {
	startTime := time.Now()

	// Validate command
	if len(spec.Command) == 0 {
		return nil, runtime.NewToolError(
			runtime.ErrorInvalidArguments,
			"command cannot be empty",
		)
	}

	// Create the command
	cmd := exec.CommandContext(ctx, spec.Command[0], spec.Command[1:]...)

	// Set working directory
	if spec.WorkingDirectory != "" {
		cmd.Dir = spec.WorkingDirectory
	}

	// Set environment variables with filtering to prevent credential leakage
	if len(spec.Environment) > 0 {
		// Create filter to remove sensitive environment variables
		filter := NewDefaultEnvFilter()

		// Start with filtered system environment
		cmd.Env = filter.Filter()

		// Add user-specified environment variables (also filtered for safety)
		filteredEnv := filter.FilterMap(spec.Environment)
		for k, v := range filteredEnv {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Create output capturer
	capturer := NewOutputCapturer(spec.CallID)

	// Set up output capture
	if execCtx.OutputWriter != nil {
		// Stream output in real-time
		cmd.Stdout = io.MultiWriter(capturer.stdout, execCtx.OutputWriter)
		cmd.Stderr = io.MultiWriter(capturer.stderr, execCtx.OutputWriter)
	} else {
		// Just capture output
		cmd.Stdout = capturer.stdout
		cmd.Stderr = capturer.stderr
	}

	// Apply sandbox policy if specified
	var sandboxInfo *sandbox.SandboxInfo
	if spec.SandboxPolicy != nil {
		workspace := spec.WorkingDirectory
		if workspace == "" {
			workspace = "."
		}

		info, err := e.sandboxManager.ApplyToCommand(cmd, spec.SandboxPolicy, workspace)
		if err != nil {
			return nil, runtime.NewToolErrorWithCause(
				runtime.ErrorExecution,
				fmt.Sprintf("failed to apply sandbox: %v", err),
				err,
			)
		}
		sandboxInfo = info
	}

	// Execute the command
	execErr := cmd.Run()

	// Calculate execution time
	executionTime := time.Since(startTime)

	// Get captured output
	stdout := capturer.Stdout()
	stderr := capturer.Stderr()

	// Determine exit code and success status
	exitCode := 0
	success := true

	if execErr != nil {
		success = false

		// Check if it was a timeout/cancellation
		if ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled {
			return nil, runtime.NewToolErrorWithCause(
				runtime.ErrorTimeout,
				"command execution timed out or was cancelled",
				ctx.Err(),
			)
		}

		// Extract exit code if available
		if exitError, ok := execErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			// Other execution errors (e.g., command not found)
			return nil, runtime.NewToolErrorWithCause(
				runtime.ErrorExecution,
				fmt.Sprintf("failed to execute command: %v", execErr),
				execErr,
			)
		}
	}

	// Aggregate output
	content := aggregateOutput(stdout, stderr)
	if content == "" && execErr != nil {
		content = execErr.Error()
	}

	// Build response
	resp := &runtime.ToolResponse{
		Content:        content,
		Success:        &success,
		ExitCode:       &exitCode,
		ExecutionTime:  executionTime,
		StreamedOutput: execCtx.OutputWriter != nil,
	}

	// Add sandbox metadata if sandboxing was applied
	if sandboxInfo != nil && sandboxInfo.Applied {
		if resp.Metadata == nil {
			resp.Metadata = make(map[string]interface{})
		}
		resp.Metadata["sandbox_type"] = sandboxInfo.Type.String()
		resp.Metadata["sandbox_applied"] = sandboxInfo.Applied
		resp.Metadata["sandbox_reason"] = sandboxInfo.Reason
	}

	return resp, nil
}

// ExecuteWithTimeout executes a command with a timeout.
func (e *CommandExecutor) ExecuteWithTimeout(ctx context.Context, spec *CommandSpec, execCtx *runtime.ExecutionContext, timeout time.Duration) (*runtime.ToolResponse, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	return e.Execute(ctx, spec, execCtx)
}

// IsCommandAvailable checks if a command is available in the system PATH.
func IsCommandAvailable(command string) bool {
	_, err := exec.LookPath(command)
	return err == nil
}

// SanitizeCommand performs basic sanitization on command strings.
// This is a simple implementation and should be enhanced for production use.
func SanitizeCommand(command string) string {
	// Remove null bytes
	command = strings.ReplaceAll(command, "\x00", "")
	return strings.TrimSpace(command)
}
