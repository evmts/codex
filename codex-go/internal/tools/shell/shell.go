// Package shell provides the shell command execution tool runtime.
//
// The shell tool enables execution of shell commands with support for:
//   - Command approval workflows
//   - Sandbox isolation (bubblewrap/docker)
//   - Output streaming
//   - Escalated permissions for trusted operations
//   - Automatic retry without sandbox on permission denial
package shell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/internal/tools/runtime"
)

// shellArgs represents the parsed arguments for the shell tool.
type shellArgs struct {
	// Command is the shell command to execute
	Command string `json:"command"`

	// WorkingDirectory overrides the default working directory (optional)
	WorkingDirectory string `json:"working_directory,omitempty"`

	// Timeout is the maximum execution time in milliseconds (optional)
	Timeout int `json:"timeout,omitempty"`

	// WithEscalatedPermissions requests execution without sandbox (optional)
	WithEscalatedPermissions bool `json:"with_escalated_permissions,omitempty"`

	// Justification explains why escalated permissions are needed (optional)
	Justification string `json:"justification,omitempty"`

	// Environment contains additional environment variables (optional)
	Environment map[string]string `json:"environment,omitempty"`
}

// ShellTool implements the ToolRuntime interface for shell command execution.
type ShellTool struct {
	executor *CommandExecutor
}

// NewShellTool creates a new shell tool instance.
func NewShellTool() *ShellTool {
	return &ShellTool{
		executor: NewCommandExecutor(),
	}
}

// Name returns the unique identifier for this tool.
func (s *ShellTool) Name() string {
	return "shell"
}

// Execute runs the shell command with the given request under the specified execution context.
func (s *ShellTool) Execute(ctx context.Context, req *runtime.ToolRequest, execCtx *runtime.ExecutionContext) (*runtime.ToolResponse, error) {
	// Parse arguments
	args, err := s.parseArguments(req.Arguments)
	if err != nil {
		return nil, runtime.NewToolErrorWithCause(
			runtime.ErrorInvalidArguments,
			"failed to parse shell arguments",
			err,
		)
	}

	// Validate command
	if args.Command == "" {
		return nil, runtime.NewToolError(
			runtime.ErrorInvalidArguments,
			"command cannot be empty",
		)
	}

	// Build command array
	cmdArray := buildCommandArray(args.Command)

	// Determine working directory
	workingDir := req.WorkingDirectory
	if args.WorkingDirectory != "" {
		workingDir = args.WorkingDirectory
	}
	if workingDir == "" {
		workingDir = "."
	}

	// Merge environment variables
	env := make(map[string]string)
	for k, v := range req.Environment {
		env[k] = v
	}
	for k, v := range args.Environment {
		env[k] = v
	}

	// Create command spec
	spec := &CommandSpec{
		Command:          cmdArray,
		WorkingDirectory: workingDir,
		Environment:      env,
		CallID:           req.CallID,
	}

	// Execute the command
	result, err := s.executor.Execute(ctx, spec, execCtx)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ApprovalKey generates a unique key for caching approval decisions.
func (s *ShellTool) ApprovalKey(req *runtime.ToolRequest) string {
	args, err := s.parseArguments(req.Arguments)
	if err != nil {
		return ""
	}

	// Create a key from tool name, command, and working directory
	key := fmt.Sprintf("shell:%s:%s", args.Command, req.WorkingDirectory)

	// Hash for consistent length
	hash := sha256.Sum256([]byte(key))
	return "shell:" + hex.EncodeToString(hash[:8])
}

// NeedsInitialApproval determines if approval is required before the first execution attempt.
func (s *ShellTool) NeedsInitialApproval(req *runtime.ToolRequest, approvalPolicy runtime.ApprovalPolicy, sandboxPolicy runtime.SandboxPolicy) bool {
	// Never policy means no approval needed
	if approvalPolicy == runtime.ApprovalNever {
		return false
	}

	// Danger full access mode doesn't need approval for on-request policy
	if approvalPolicy == runtime.ApprovalOnRequest && sandboxPolicy == runtime.SandboxDangerFullAccess {
		return false
	}

	// Parse command to check if it's safe
	args, err := s.parseArguments(req.Arguments)
	if err != nil {
		return true // Invalid arguments should require approval
	}

	cmdArray := buildCommandArray(args.Command)

	// Known safe commands don't need approval with on-request policy
	if approvalPolicy == runtime.ApprovalOnRequest && runtime.IsKnownSafeCommand(cmdArray) {
		return false
	}

	// Unless-trusted policy requires approval for everything except known safe commands
	if approvalPolicy == runtime.ApprovalUnlessTrusted && !runtime.IsKnownSafeCommand(cmdArray) {
		return true
	}

	// On-request policy needs approval for non-safe commands
	if approvalPolicy == runtime.ApprovalOnRequest {
		return true
	}

	return false
}

// NeedsRetryApproval determines if approval is required before retrying without sandbox.
func (s *ShellTool) NeedsRetryApproval(approvalPolicy runtime.ApprovalPolicy) bool {
	// On-failure policy specifically requires approval for retry
	// Unless-trusted also requires approval for escalation
	return approvalPolicy == runtime.ApprovalOnFailure || approvalPolicy == runtime.ApprovalUnlessTrusted
}

// SandboxPreference indicates how this tool wants to interact with sandboxing.
func (s *ShellTool) SandboxPreference() runtime.SandboxPreference {
	return runtime.SandboxAuto
}

// EscalateOnFailure returns true if the tool should retry without sandbox
// when the initial sandboxed attempt fails with a permission denial.
func (s *ShellTool) EscalateOnFailure() bool {
	return true
}

// WantsEscalatedFirstAttempt returns true if the request explicitly asks
// for escalated permissions (bypass sandbox on first attempt).
func (s *ShellTool) WantsEscalatedFirstAttempt(req *runtime.ToolRequest) bool {
	args, err := s.parseArguments(req.Arguments)
	if err != nil {
		return false
	}
	return args.WithEscalatedPermissions
}

// SupportsParallel returns true if multiple invocations of this tool
// can safely execute concurrently.
func (s *ShellTool) SupportsParallel() bool {
	return true
}

// SandboxRetryData extracts command metadata needed for re-running without sandbox.
func (s *ShellTool) SandboxRetryData(req *runtime.ToolRequest) *runtime.SandboxRetryData {
	args, err := s.parseArguments(req.Arguments)
	if err != nil {
		return nil
	}

	cmdArray := buildCommandArray(args.Command)
	workingDir := req.WorkingDirectory
	if args.WorkingDirectory != "" {
		workingDir = args.WorkingDirectory
	}

	return &runtime.SandboxRetryData{
		Command:          cmdArray,
		WorkingDirectory: workingDir,
	}
}

// parseArguments parses the JSON arguments for the shell tool.
func (s *ShellTool) parseArguments(arguments string) (*shellArgs, error) {
	if arguments == "" {
		return nil, fmt.Errorf("arguments cannot be empty")
	}

	var args shellArgs
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return nil, fmt.Errorf("failed to parse JSON arguments: %w", err)
	}

	return &args, nil
}

// buildCommandArray builds a command array suitable for os/exec.
// Shell commands are executed via "sh -c" to support pipes, redirects, etc.
func buildCommandArray(command string) []string {
	// Always use sh -c for shell command execution
	// This allows pipes, redirects, and other shell features
	return []string{"sh", "-c", command}
}

// aggregateOutput combines stdout and stderr into a single output string.
func aggregateOutput(stdout, stderr string) string {
	var parts []string

	if stdout != "" {
		parts = append(parts, strings.TrimSpace(stdout))
	}

	if stderr != "" {
		parts = append(parts, strings.TrimSpace(stderr))
	}

	return strings.Join(parts, "\n")
}

// formatDuration formats a duration for display.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		seconds := d.Seconds()
		if seconds == float64(int(seconds)) {
			return fmt.Sprintf("%ds", int(seconds))
		}
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
