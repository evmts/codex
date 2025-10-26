// Package git provides git repository management tools for Codex Go.
//
// The git package implements the following tools:
//   - git_status: Show the working tree status
//   - git_diff: Show changes between commits, commit and working tree, etc
//   - git_log: Show commit logs
//   - git_commit: Record changes to the repository (requires approval)
//
// All git operations are executed using the system git binary with proper
// sandboxing when available. The tools parse git output into structured
// formats suitable for AI consumption.
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// gitExecutor provides common functionality for executing git commands.
type gitExecutor struct{}

// newGitExecutor creates a new git command executor.
func newGitExecutor() *gitExecutor {
	return &gitExecutor{}
}

// executeGit runs a git command and returns stdout, stderr, and error.
// The command is executed in the given working directory.
func (e *gitExecutor) executeGit(ctx context.Context, workingDir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workingDir

	// Create buffers for output
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	// Execute command
	execErr := cmd.Run()

	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	return stdout, stderr, execErr
}

// executeGitWithTimeout runs a git command with a timeout.
func (e *gitExecutor) executeGitWithTimeout(ctx context.Context, workingDir string, timeout time.Duration, args ...string) (stdout, stderr string, err error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	return e.executeGit(ctx, workingDir, args...)
}

// isGitRepo checks if the given directory is inside a git repository.
func (e *gitExecutor) isGitRepo(ctx context.Context, workingDir string) bool {
	_, _, err := e.executeGit(ctx, workingDir, "rev-parse", "--git-dir")
	return err == nil
}

// gitError wraps git command errors with contextual information.
type gitError struct {
	command string
	stdout  string
	stderr  string
	err     error
}

// Error returns the formatted error message.
func (e *gitError) Error() string {
	if e.stderr != "" {
		return fmt.Sprintf("git %s failed: %s", e.command, strings.TrimSpace(e.stderr))
	}
	if e.err != nil {
		return fmt.Sprintf("git %s failed: %v", e.command, e.err)
	}
	return fmt.Sprintf("git %s failed", e.command)
}

// Unwrap returns the underlying error.
func (e *gitError) Unwrap() error {
	return e.err
}

// newGitError creates a new git error.
func newGitError(command string, stdout, stderr string, err error) error {
	return &gitError{
		command: command,
		stdout:  stdout,
		stderr:  stderr,
		err:     err,
	}
}

// formatGitOutput formats git output for display.
// Removes empty lines at the start and end, and ensures consistent line endings.
func formatGitOutput(output string) string {
	lines := strings.Split(output, "\n")

	// Trim leading empty lines
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	// Trim trailing empty lines
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	if start >= end {
		return ""
	}

	return strings.Join(lines[start:end], "\n")
}

// parseFileStatus parses a git status porcelain line into XY status codes.
// Returns the XY code, the file path, and whether it's a rename (old -> new).
func parseFileStatus(line string) (xy, path, oldPath string) {
	if len(line) < 4 {
		return "", line, ""
	}

	xy = line[0:2]
	path = strings.TrimSpace(line[3:])

	// Handle renames (R  old -> new)
	if strings.Contains(path, " -> ") {
		parts := strings.SplitN(path, " -> ", 2)
		if len(parts) == 2 {
			oldPath = parts[0]
			path = parts[1]
		}
	}

	return xy, path, oldPath
}

// statusCodeDescription returns a human-readable description of git status codes.
func statusCodeDescription(xy string) string {
	if len(xy) != 2 {
		return "unknown"
	}

	x := xy[0]
	y := xy[1]

	var parts []string

	// Staged changes (index)
	switch x {
	case 'M':
		parts = append(parts, "modified")
	case 'A':
		parts = append(parts, "added")
	case 'D':
		parts = append(parts, "deleted")
	case 'R':
		parts = append(parts, "renamed")
	case 'C':
		parts = append(parts, "copied")
	}

	// Working tree changes
	switch y {
	case 'M':
		if len(parts) > 0 {
			parts = append(parts, "modified in working tree")
		} else {
			parts = append(parts, "modified")
		}
	case 'D':
		if len(parts) > 0 {
			parts = append(parts, "deleted in working tree")
		} else {
			parts = append(parts, "deleted")
		}
	case '?':
		parts = append(parts, "untracked")
	}

	if len(parts) == 0 {
		return "unchanged"
	}

	return strings.Join(parts, ", ")
}
