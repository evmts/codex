package runtime

import (
	"sync"
	"time"
)

// ToolCapabilities describes the operational characteristics of a tool.
// These capabilities inform the orchestrator how to schedule and execute the tool.
type ToolCapabilities struct {
	// SupportsParallel indicates if this tool can safely run concurrently
	// with other tool invocations. Read-only tools (grep_files, read_file, list_dir)
	// typically support parallelism, while stateful tools may not.
	SupportsParallel bool

	// RequiresApproval indicates if this tool always requires approval
	// regardless of policy settings. Used for high-risk operations.
	RequiresApproval bool

	// SupportsSandbox indicates if this tool can operate under sandbox restrictions.
	// Some tools (like viewing images) may not support sandboxing.
	SupportsSandbox bool

	// SupportsStreaming indicates if this tool can stream output incrementally.
	// Shell commands typically support streaming; file reads do not.
	SupportsStreaming bool

	// SupportsRetry indicates if this tool supports retry after sandbox denial.
	// Command-based tools support this; others may not.
	SupportsRetry bool
}

// OutputDelta represents an incremental update to tool output during streaming execution.
// Tools that support streaming can emit OutputDeltas as they produce results.
type OutputDelta struct {
	// CallID identifies which tool call this delta belongs to.
	CallID string

	// Type indicates what kind of output this is (stdout, stderr, status).
	Type OutputDeltaType

	// Content is the incremental output text.
	Content string

	// Timestamp records when this delta was produced.
	Timestamp time.Time
}

// OutputDeltaType categorizes different kinds of streaming output.
type OutputDeltaType int

const (
	// DeltaStdout represents standard output content.
	DeltaStdout OutputDeltaType = iota

	// DeltaStderr represents standard error content.
	DeltaStderr

	// DeltaStatus represents status messages (e.g., "connecting...", "reading file...").
	DeltaStatus

	// DeltaComplete indicates the tool execution completed.
	DeltaComplete
)

// ApprovalRequest encapsulates all information needed to request user approval.
// The orchestrator constructs this and passes it to the approval handler.
type ApprovalRequest struct {
	// CallID uniquely identifies this tool call.
	CallID string

	// ToolName is the name of the tool requesting approval.
	ToolName string

	// Command is the command to be executed (for shell/exec tools).
	// Empty for non-command tools.
	Command []string

	// WorkingDirectory is where the command will execute.
	WorkingDirectory string

	// Justification is an optional explanation of why this operation is needed.
	// May come from the tool arguments or generated during retry.
	Justification string

	// IsRetry indicates this is a retry after sandbox denial.
	IsRetry bool

	// RetryReason explains why the retry is needed (e.g., "Permission denied").
	RetryReason string

	// Risk contains the risk assessment for this operation.
	// Only populated for sandbox retry approvals.
	Risk *RiskAssessment
}

// RiskAssessment represents the security risk evaluation for a command.
// This helps users make informed approval decisions.
type RiskAssessment struct {
	// Level indicates the overall risk level.
	Level RiskLevel

	// Reasons lists specific concerns (e.g., "writes to system directories",
	// "network access detected", "destructive operation").
	Reasons []string

	// Mitigation suggests how sandbox restrictions would protect against risks.
	Mitigation string
}

// RiskLevel categorizes the severity of security risks.
type RiskLevel int

const (
	// RiskLow indicates minimal risk (e.g., read-only operations).
	RiskLow RiskLevel = iota

	// RiskMedium indicates moderate risk (e.g., writes within workspace).
	RiskMedium

	// RiskHigh indicates significant risk (e.g., system modifications, network access).
	RiskHigh

	// RiskCritical indicates severe risk (e.g., destructive operations, privilege escalation).
	RiskCritical
)

// ExecutionResult captures the complete result of a tool execution,
// including output, timing, and sandbox information.
type ExecutionResult struct {
	// Request is the original tool request.
	Request *ToolRequest

	// Response is the tool's response.
	Response *ToolResponse

	// Error is set if execution failed.
	Error *ToolError

	// SandboxUsed indicates whether sandbox was active during execution.
	SandboxUsed bool

	// RetryCount tracks how many retry attempts were made.
	RetryCount int

	// ApprovalRequired indicates whether user approval was requested.
	ApprovalRequired bool

	// StartTime records when execution began.
	StartTime time.Time

	// EndTime records when execution completed.
	EndTime time.Time
}

// MemoryApprovalCache is a simple in-memory implementation of ApprovalCache.
// It uses a sync.Map for thread-safe access across goroutines.
type MemoryApprovalCache struct {
	cache sync.Map
}

// NewMemoryApprovalCache creates a new in-memory approval cache.
func NewMemoryApprovalCache() *MemoryApprovalCache {
	return &MemoryApprovalCache{}
}

// Get retrieves a cached approval decision by key.
func (m *MemoryApprovalCache) Get(key string) *ApprovalDecision {
	if val, ok := m.cache.Load(key); ok {
		decision, ok := val.(ApprovalDecision)
		if !ok {
			// Invalid type stored in cache; return nil
			return nil
		}
		return &decision
	}
	return nil
}

// Put stores an approval decision for the given key.
func (m *MemoryApprovalCache) Put(key string, decision ApprovalDecision) {
	// Only cache session-level approvals
	if decision == ApprovalApprovedForSession {
		m.cache.Store(key, decision)
	}
}

// Clear removes all cached approvals (typically called when session ends).
func (m *MemoryApprovalCache) Clear() {
	m.cache = sync.Map{}
}

// CommandSpec defines the complete specification for executing a command.
// This is used by shell/exec tools to describe what they want to run.
type CommandSpec struct {
	// Program is the executable to run.
	Program string

	// Args are the command-line arguments (excluding the program name).
	Args []string

	// WorkingDirectory is where the command should execute.
	WorkingDirectory string

	// Environment contains additional environment variables.
	Environment map[string]string

	// Timeout specifies the maximum execution duration.
	Timeout time.Duration

	// WithEscalatedPermissions requests execution without sandbox.
	WithEscalatedPermissions bool

	// Justification explains why escalated permissions are needed.
	Justification string
}

// ToolSpec defines the metadata for a tool exposed to the AI model.
// This corresponds to the tool definitions sent in API requests.
type ToolSpec struct {
	// Name is the unique tool identifier.
	Name string

	// Description explains what the tool does (shown to the AI).
	Description string

	// ParametersSchema is the JSON schema for tool arguments.
	ParametersSchema interface{}

	// Strict indicates whether strict schema validation is required.
	Strict bool

	// SupportsParallel indicates if the tool supports concurrent execution.
	SupportsParallel bool
}

// ToolRegistryBuilder helps construct a ToolRegistry with both
// runtime implementations and their corresponding specs.
type ToolRegistryBuilder struct {
	runtimes map[string]ToolRuntime
	specs    []ToolSpec
}

// NewToolRegistryBuilder creates a new builder.
func NewToolRegistryBuilder() *ToolRegistryBuilder {
	return &ToolRegistryBuilder{
		runtimes: make(map[string]ToolRuntime),
		specs:    make([]ToolSpec, 0),
	}
}

// RegisterTool adds both a runtime implementation and its spec.
func (b *ToolRegistryBuilder) RegisterTool(runtime ToolRuntime, spec ToolSpec) {
	b.runtimes[runtime.Name()] = runtime
	b.specs = append(b.specs, spec)
}

// RegisterRuntime adds a runtime implementation without a spec.
// Used for tools that have multiple names/aliases pointing to one implementation.
func (b *ToolRegistryBuilder) RegisterRuntime(name string, runtime ToolRuntime) {
	b.runtimes[name] = runtime
}

// AddSpec adds a tool spec without registering a runtime.
// Used for special tools like WebSearch that are handled differently.
func (b *ToolRegistryBuilder) AddSpec(spec ToolSpec) {
	b.specs = append(b.specs, spec)
}

// Build constructs the final registry and returns it along with the spec list.
func (b *ToolRegistryBuilder) Build() (*ToolRegistry, []ToolSpec) {
	registry := &ToolRegistry{
		tools: b.runtimes,
	}
	return registry, b.specs
}

// StreamWriter provides a safe way to write incremental output during tool execution.
// It handles formatting and delivery of OutputDeltas to the client.
type StreamWriter struct {
	callID    string
	onDelta   func(delta OutputDelta)
	mu        sync.Mutex
	closed    bool
	lastWrite time.Time
}

// NewStreamWriter creates a StreamWriter for the given call ID.
// The onDelta callback is invoked for each output delta.
func NewStreamWriter(callID string, onDelta func(delta OutputDelta)) *StreamWriter {
	return &StreamWriter{
		callID:    callID,
		onDelta:   onDelta,
		lastWrite: time.Now(),
	}
}

// Write implements io.Writer for streaming stdout content.
func (s *StreamWriter) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, nil
	}

	if len(p) > 0 && s.onDelta != nil {
		delta := OutputDelta{
			CallID:    s.callID,
			Type:      DeltaStdout,
			Content:   string(p),
			Timestamp: time.Now(),
		}
		s.onDelta(delta)
		s.lastWrite = time.Now()
	}

	return len(p), nil
}

// WriteStatus emits a status message (not stdout/stderr content).
func (s *StreamWriter) WriteStatus(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.onDelta == nil {
		return
	}

	delta := OutputDelta{
		CallID:    s.callID,
		Type:      DeltaStatus,
		Content:   message,
		Timestamp: time.Now(),
	}
	s.onDelta(delta)
}

// WriteStderr emits stderr content (for tools that distinguish stdout/stderr).
func (s *StreamWriter) WriteStderr(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.onDelta == nil {
		return
	}

	delta := OutputDelta{
		CallID:    s.callID,
		Type:      DeltaStderr,
		Content:   content,
		Timestamp: time.Now(),
	}
	s.onDelta(delta)
}

// Complete signals that tool execution is complete.
func (s *StreamWriter) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.onDelta == nil {
		return
	}

	delta := OutputDelta{
		CallID:    s.callID,
		Type:      DeltaComplete,
		Timestamp: time.Now(),
	}
	s.onDelta(delta)
	s.closed = true
}

// Close is an alias for Complete (to satisfy io.Closer if needed).
func (s *StreamWriter) Close() error {
	s.Complete()
	return nil
}

// IsKnownSafeCommand determines if a command is considered safe to execute
// without approval or sandbox restrictions. This is a placeholder for
// command safety analysis logic.
func IsKnownSafeCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}

	// List of commands that are generally safe (read-only operations)
	safeCommands := map[string]bool{
		"ls":     true,
		"pwd":    true,
		"echo":   true,
		"cat":    true,
		"grep":   true,
		"find":   true,
		"which":  true,
		"type":   true,
		"head":   true,
		"tail":   true,
		"wc":     true,
		"date":   true,
		"whoami": true,
		"id":     true,
		"uname":  true,
		"file":   true,
		"stat":   true,
	}

	program := command[0]
	return safeCommands[program]
}

// IsDangerousCommand determines if a command is potentially dangerous
// and should always require approval. This is a placeholder for
// command risk analysis logic.
func IsDangerousCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}

	// List of commands that are potentially dangerous
	dangerousCommands := map[string]bool{
		"rm":        true,
		"rmdir":     true,
		"dd":        true,
		"mkfs":      true,
		"fdisk":     true,
		"parted":    true,
		"sudo":      true,
		"su":        true,
		"chmod":     true,
		"chown":     true,
		"kill":      true,
		"killall":   true,
		"reboot":    true,
		"shutdown":  true,
		"halt":      true,
		"init":      true,
		"systemctl": true,
	}

	program := command[0]
	return dangerousCommands[program]
}
