package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/evmts/codex/codex-go/internal/tools/runtime"
	"github.com/spf13/afero"
)

// ReadTool implements the read_file tool runtime.
type ReadTool struct {
	fs afero.Fs
}

// NewReadTool creates a new ReadTool with the given filesystem.
func NewReadTool(fs afero.Fs) *ReadTool {
	return &ReadTool{fs: fs}
}

// readArgs represents the arguments for read_file tool.
type readArgs struct {
	Path      string `json:"path"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

// Name returns the tool name.
func (t *ReadTool) Name() string {
	return "read_file"
}

// Execute reads a file and returns its contents.
func (t *ReadTool) Execute(ctx context.Context, req *runtime.ToolRequest, execCtx *runtime.ExecutionContext) (*runtime.ToolResponse, error) {
	startTime := time.Now()

	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return nil, runtime.NewToolErrorWithCause(runtime.ErrorExecution, "context cancelled", err)
	}

	// Parse arguments
	var args readArgs
	if err := json.Unmarshal([]byte(req.Arguments), &args); err != nil {
		return nil, runtime.NewToolErrorWithCause(runtime.ErrorInvalidArguments, "invalid arguments", err)
	}

	if args.Path == "" {
		return nil, runtime.NewToolError(runtime.ErrorInvalidArguments, "path is required")
	}

	// Validate path
	fullPath, err := validatePath(req.WorkingDirectory, args.Path)
	if err != nil {
		return nil, runtime.NewToolErrorWithCause(runtime.ErrorInvalidArguments, err.Error(), err)
	}

	// Check if file exists
	exists, err := afero.Exists(t.fs, fullPath)
	if err != nil {
		success := false
		return &runtime.ToolResponse{
			Content:       fmt.Sprintf("Error checking file: %v", err),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	if !exists {
		success := false
		return &runtime.ToolResponse{
			Content:       fmt.Sprintf("File not found: %s", args.Path),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	// Get file info
	info, err := t.fs.Stat(fullPath)
	if err != nil {
		success := false
		return &runtime.ToolResponse{
			Content:       fmt.Sprintf("Error reading file info: %v", err),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	if info.IsDir() {
		success := false
		return &runtime.ToolResponse{
			Content:       fmt.Sprintf("Path is a directory: %s", args.Path),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	// Read file
	data, err := afero.ReadFile(t.fs, fullPath)
	if err != nil {
		success := false
		return &runtime.ToolResponse{
			Content:       fmt.Sprintf("Error reading file: %v", err),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	// Check if binary
	if isBinaryFile(data) {
		success := false
		return &runtime.ToolResponse{
			Content: fmt.Sprintf("File appears to be binary (%s, %s). Binary files cannot be read as text.",
				args.Path, formatFileSize(info.Size())),
			Success:       &success,
			ExecutionTime: time.Since(startTime),
		}, nil
	}

	// Process line range if specified
	content := string(data)
	if args.StartLine != nil || args.EndLine != nil {
		lines := strings.Split(content, "\n")
		start := 0
		end := len(lines)

		if args.StartLine != nil {
			start = *args.StartLine - 1 // Convert to 0-indexed
			if start < 0 {
				start = 0
			}
		}

		if args.EndLine != nil {
			end = *args.EndLine
			if end > len(lines) {
				end = len(lines)
			}
		}

		if start > end {
			start = end
		}

		if start < len(lines) && end <= len(lines) {
			selectedLines := lines[start:end]
			content = strings.Join(selectedLines, "\n")
			if end < len(lines) || strings.HasSuffix(string(data), "\n") {
				content += "\n"
			}
		} else {
			content = ""
		}
	}

	success := true
	response := &runtime.ToolResponse{
		Content:       fmt.Sprintf("File: %s\n\n%s", args.Path, content),
		Success:       &success,
		ExecutionTime: time.Since(startTime),
	}

	// Stream output if writer is available
	if execCtx.OutputWriter != nil {
		// Best effort streaming write
		_, _ = io.WriteString(execCtx.OutputWriter, response.Content) // nolint:errcheck
		response.StreamedOutput = true
	}

	return response, nil
}

// ApprovalKey generates a unique key for approval caching.
func (t *ReadTool) ApprovalKey(req *runtime.ToolRequest) string {
	return fmt.Sprintf("%s:%s:%s", t.Name(), req.WorkingDirectory, req.Arguments)
}

// NeedsInitialApproval returns false for read operations (they're safe).
func (t *ReadTool) NeedsInitialApproval(req *runtime.ToolRequest, approvalPolicy runtime.ApprovalPolicy, sandboxPolicy runtime.SandboxPolicy) bool {
	return false // Read operations don't need approval
}

// NeedsRetryApproval returns false (no retry needed for reads).
func (t *ReadTool) NeedsRetryApproval(approvalPolicy runtime.ApprovalPolicy) bool {
	return false
}

// SandboxPreference returns Auto (can run with or without sandbox).
func (t *ReadTool) SandboxPreference() runtime.SandboxPreference {
	return runtime.SandboxAuto
}

// EscalateOnFailure returns false (reads don't need escalation).
func (t *ReadTool) EscalateOnFailure() bool {
	return false
}

// WantsEscalatedFirstAttempt returns false.
func (t *ReadTool) WantsEscalatedFirstAttempt(req *runtime.ToolRequest) bool {
	return false
}

// SupportsParallel returns true (reads can be parallelized).
func (t *ReadTool) SupportsParallel() bool {
	return true
}

// SandboxRetryData returns nil (reads don't retry).
func (t *ReadTool) SandboxRetryData(req *runtime.ToolRequest) *runtime.SandboxRetryData {
	return nil
}
