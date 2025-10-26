package manager

import (
    "context"
    "fmt"
    "sync"
    "time"

    "github.com/evmts/codex/codex-go/internal/client"
    "github.com/evmts/codex/codex-go/internal/history/persistence"
    "github.com/evmts/codex/codex-go/internal/protocol"
    "github.com/evmts/codex/codex-go/internal/tools/orchestrator"
)

// Session represents a conversation session with an AI agent.
// It manages the lifecycle of a single conversation including:
// - State transitions
// - Turn processing
// - Event handling
// - History tracking
// - Approval workflow coordination
type Session struct {
    // Immutable fields (set at creation)
    id        string
    client    client.Client
    createdAt time.Time
    orch      *orchestrator.Orchestrator
    provider  string // API provider name

    // Mutable fields (protected by mutex)
    mu                   sync.RWMutex
    stateMachine         *StateMachine
    currentTurnID        string
    pendingApproval      *PendingApproval
    approvalHandler      *SessionApprovalHandler
    eventHandlers        []EventHandler
    turnContext          *TurnContext
    tokenUsage           *protocol.TokenUsage
    lastAgentMessage     string
    reconstructedHistory []client.Message // Messages from history reconstruction for resume
    historyLogID         uint64           // ID for history log
    historyEntryCount    int              // Number of entries in history

    // History persistence
    history          *persistence.HistoryPersistence
    historyEnabled   bool

    // Cancellation
    ctx    context.Context
    cancel context.CancelFunc
}

// PendingApproval represents an approval request waiting for user response.
type PendingApproval struct {
	SubmissionID string
	Type         ApprovalType
	Timestamp    time.Time
}

// ApprovalType identifies the type of approval needed.
type ApprovalType string

const (
	ApprovalTypeExec  ApprovalType = "exec"
	ApprovalTypePatch ApprovalType = "patch"
)

// TurnContext contains persistent context for turns in a session.
type TurnContext struct {
	Cwd              string
	ApprovalPolicy   string
	ApprovalTimeout  time.Duration // Timeout for approval requests (default 5 minutes)
	SandboxPolicy    protocol.SandboxPolicy
	Model            string
	Effort           *string
	Summary          string
	MaxTurns         int           // Maximum multi-turn iterations (default 10, prevents infinite loops)
}

// EventHandler is called when events are emitted during turn processing.
type EventHandler func(ctx context.Context, event *protocol.Event) error

// SessionConfig contains configuration for creating a session.
type SessionConfig struct {
    ID            string
    Client        client.Client
    TurnContext   *TurnContext
    EventHandlers []EventHandler
    Orchestrator  *orchestrator.Orchestrator
    History       *persistence.HistoryPersistence
    Provider      string // API provider name (e.g., "anthropic", "openai")
}

// NewSession creates a new conversation session.
func NewSession(cfg SessionConfig) (*Session, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("client is required")
	}
	if cfg.TurnContext == nil {
		return nil, fmt.Errorf("turn context is required")
	}

	ctx, cancel := context.WithCancel(context.Background())

    s := &Session{
        id:            cfg.ID,
        client:        cfg.Client,
        provider:      cfg.Provider,
        createdAt:     time.Now(),
        stateMachine:  NewStateMachine(),
        eventHandlers: cfg.EventHandlers,
        turnContext:   cfg.TurnContext,
        orch:          cfg.Orchestrator,
        tokenUsage: &protocol.TokenUsage{
            InputTokens:  0,
            OutputTokens: 0,
            TotalTokens:  0,
        },
        ctx:    ctx,
        cancel: cancel,
    }

    if cfg.History != nil {
        s.history = cfg.History
        s.historyEnabled = true
    }

    return s, nil
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.id
}

// CreatedAt returns when the session was created.
func (s *Session) CreatedAt() time.Time {
	return s.createdAt
}

// State returns the current session state.
func (s *Session) State() SessionState {
	return s.stateMachine.GetState()
}

// IsClosed returns whether the session is closed.
func (s *Session) IsClosed() bool {
	return s.stateMachine.IsTerminal()
}

// Close closes the session and cancels any ongoing operations.
func (s *Session) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()

	if s.stateMachine.IsTerminal() {
		return fmt.Errorf("session already closed")
	}

    if err := s.stateMachine.Transition(StateClosed); err != nil {
        return fmt.Errorf("failed to close session: %w", err)
    }

    s.cancel()

    if s.historyEnabled && s.history != nil {
        _ = s.history.Flush()
        _ = s.history.Close()
    }
    return nil
}

// CanAcceptTurn returns whether the session can accept a new turn.
func (s *Session) CanAcceptTurn() bool {
	return s.stateMachine.CanAcceptTurn()
}

// SubmitTurn submits a user turn for processing.
// Returns the submission ID for tracking.
func (s *Session) SubmitTurn(ctx context.Context, op *protocol.OpUserTurn) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.stateMachine.CanAcceptTurn() {
		return "", fmt.Errorf("session cannot accept turn in state %s", s.stateMachine.GetState())
	}

	if err := s.stateMachine.Transition(StateProcessingTurn); err != nil {
		return "", fmt.Errorf("failed to transition to processing state: %w", err)
	}

	submissionID := fmt.Sprintf("turn_%s_%d", s.id, time.Now().UnixNano())
	s.currentTurnID = submissionID

	// Update turn context, preserving MaxTurns and ApprovalTimeout if already set
	maxTurns := s.turnContext.MaxTurns
	approvalTimeout := s.turnContext.ApprovalTimeout

	s.turnContext = &TurnContext{
		Cwd:             op.Cwd,
		ApprovalPolicy:  op.ApprovalPolicy,
		ApprovalTimeout: approvalTimeout,
		SandboxPolicy:   op.SandboxPolicy,
		Model:           op.Model,
		Effort:          op.Effort,
		Summary:         op.Summary,
		MaxTurns:        maxTurns,
	}

	return submissionID, nil
}

// SubmitInterrupt submits an interrupt operation to abort the current turn.
func (s *Session) SubmitInterrupt(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentState := s.stateMachine.GetState()
	if currentState != StateProcessingTurn && currentState != StateAwaitingApproval {
		return fmt.Errorf("cannot interrupt from state %s", currentState)
	}

	if err := s.stateMachine.Transition(StateInterrupted); err != nil {
		return fmt.Errorf("failed to transition to interrupted state: %w", err)
	}

	return nil
}

// SubmitApproval submits an approval decision (exec or patch).
func (s *Session) SubmitApproval(ctx context.Context, submissionID string, decision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stateMachine.GetState() != StateAwaitingApproval {
		return fmt.Errorf("session not awaiting approval (state: %s)", s.stateMachine.GetState())
	}

	if s.pendingApproval == nil {
		return fmt.Errorf("no pending approval")
	}

	if s.pendingApproval.SubmissionID != submissionID {
		return fmt.Errorf("submission ID mismatch: expected %s, got %s",
			s.pendingApproval.SubmissionID, submissionID)
	}

	// Clear pending approval
	s.pendingApproval = nil

	// Transition based on decision
	if decision == "approve" || decision == "approved" {
		if err := s.stateMachine.Transition(StateProcessingTurn); err != nil {
			return fmt.Errorf("failed to transition after approval: %w", err)
		}
	} else {
		if err := s.stateMachine.Transition(StateInterrupted); err != nil {
			return fmt.Errorf("failed to transition after rejection: %w", err)
		}
	}

	return nil
}

// RequestApproval marks the session as awaiting approval.
func (s *Session) RequestApproval(submissionID string, approvalType ApprovalType) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stateMachine.GetState() != StateProcessingTurn {
		return fmt.Errorf("cannot request approval from state %s", s.stateMachine.GetState())
	}

	if err := s.stateMachine.Transition(StateAwaitingApproval); err != nil {
		return fmt.Errorf("failed to transition to awaiting approval: %w", err)
	}

	s.pendingApproval = &PendingApproval{
		SubmissionID: submissionID,
		Type:         approvalType,
		Timestamp:    time.Now(),
	}

	return nil
}

// CompleteTurn marks the current turn as completed.
func (s *Session) CompleteTurn() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentState := s.stateMachine.GetState()
	if currentState != StateProcessingTurn && currentState != StateAwaitingApproval {
		return fmt.Errorf("cannot complete turn from state %s", currentState)
	}

	if err := s.stateMachine.Transition(StateCompleted); err != nil {
		return fmt.Errorf("failed to transition to completed: %w", err)
	}

	s.currentTurnID = ""
	return nil
}

// FailTurn marks the current turn as failed with an error.
func (s *Session) FailTurn(errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.stateMachine.TransitionToError(errMsg); err != nil {
		return fmt.Errorf("failed to transition to error state: %w", err)
	}

	s.currentTurnID = ""
	return nil
}

// ResetToIdle resets the session to idle state after completion/error/interrupt.
func (s *Session) ResetToIdle() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	currentState := s.stateMachine.GetState()
	if currentState != StateCompleted && currentState != StateError && currentState != StateInterrupted {
		return fmt.Errorf("cannot reset to idle from state %s", currentState)
	}

	if err := s.stateMachine.Transition(StateIdle); err != nil {
		return fmt.Errorf("failed to transition to idle: %w", err)
	}

	return nil
}

// UpdateTurnContext updates the persistent turn context.
func (s *Session) UpdateTurnContext(override *protocol.OpOverrideTurnContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if override.Cwd != nil {
		s.turnContext.Cwd = *override.Cwd
	}
	if override.ApprovalPolicy != nil {
		s.turnContext.ApprovalPolicy = *override.ApprovalPolicy
	}
	if override.SandboxPolicy != nil {
		s.turnContext.SandboxPolicy = *override.SandboxPolicy
	}
	if override.Model != nil {
		s.turnContext.Model = *override.Model
	}
	if override.Effort != nil {
		s.turnContext.Effort = override.Effort
	}
	if override.Summary != nil {
		s.turnContext.Summary = *override.Summary
	}

	return nil
}

// GetTurnContext returns the current turn context (copy).
func (s *Session) GetTurnContext() TurnContext {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return *s.turnContext
}

// UpdateTokenUsage updates the token usage statistics.
func (s *Session) UpdateTokenUsage(usage *protocol.TokenUsage) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if usage != nil {
		s.tokenUsage = usage
	}
}

// GetTokenUsage returns the current token usage (copy).
func (s *Session) GetTokenUsage() protocol.TokenUsage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return *s.tokenUsage
}

// SetLastAgentMessage sets the last agent message.
func (s *Session) SetLastAgentMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastAgentMessage = msg
}

// GetLastAgentMessage returns the last agent message.
func (s *Session) GetLastAgentMessage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.lastAgentMessage
}

// GetPendingApproval returns the pending approval if any.
func (s *Session) GetPendingApproval() *PendingApproval {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.pendingApproval == nil {
		return nil
	}

	// Return a copy to prevent mutation
	return &PendingApproval{
		SubmissionID: s.pendingApproval.SubmissionID,
		Type:         s.pendingApproval.Type,
		Timestamp:    s.pendingApproval.Timestamp,
	}
}

// EmitEvent emits an event to all registered handlers.
func (s *Session) EmitEvent(ctx context.Context, event *protocol.Event) error {
    s.mu.RLock()
    handlers := make([]EventHandler, len(s.eventHandlers))
    copy(handlers, s.eventHandlers)
    s.mu.RUnlock()

    // Write to history if enabled (best-effort)
    if s.historyEnabled && s.history != nil {
        _ = s.history.RecordEvent(event)
    }

    for _, handler := range handlers {
        if err := handler(ctx, event); err != nil {
            return fmt.Errorf("event handler failed: %w", err)
        }
    }

    return nil
}

// Context returns the session context.
func (s *Session) Context() context.Context {
    return s.ctx
}

// Orchestrator returns the session's orchestrator (may be nil if not configured).
func (s *Session) Orchestrator() *orchestrator.Orchestrator {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.orch
}

// RecordSubmission writes a submission to history if enabled.
func (s *Session) RecordSubmission(sub *protocol.Submission) {
    if s.historyEnabled && s.history != nil && sub != nil {
        _ = s.history.RecordSubmission(sub)
    }
}

// SetApprovalHandler sets the approval handler for the current turn.
func (s *Session) SetApprovalHandler(handler *SessionApprovalHandler) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.approvalHandler = handler
}

// GetApprovalHandler returns the current approval handler, if any.
func (s *Session) GetApprovalHandler() *SessionApprovalHandler {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.approvalHandler
}

// ClearApprovalHandler clears the approval handler (called at turn completion).
func (s *Session) ClearApprovalHandler() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.approvalHandler = nil
}

// SetReconstructedHistory sets the conversation history from a reconstructed state.
// This is used when resuming from persisted history.
func (s *Session) SetReconstructedHistory(messages []client.Message) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.reconstructedHistory = messages
}

// GetReconstructedHistory returns the reconstructed conversation history.
func (s *Session) GetReconstructedHistory() []client.Message {
    s.mu.RLock()
    defer s.mu.RUnlock()
    // Return a copy to prevent external modification
    if s.reconstructedHistory == nil {
        return nil
    }
    result := make([]client.Message, len(s.reconstructedHistory))
    copy(result, s.reconstructedHistory)
    return result
}

// SetHistoryMetadata sets the history log ID and entry count.
func (s *Session) SetHistoryMetadata(logID uint64, entryCount int) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.historyLogID = logID
    s.historyEntryCount = entryCount
}

// GetHistoryMetadata returns the history log ID and entry count.
func (s *Session) GetHistoryMetadata() (uint64, int) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.historyLogID, s.historyEntryCount
}

// EmitSessionConfigured emits the EventSessionConfigured event.
// This should be called after session initialization or resume.
func (s *Session) EmitSessionConfigured(ctx context.Context) error {
    s.mu.RLock()
    turnCtx := *s.turnContext
    historyLogID := s.historyLogID
    historyEntryCount := s.historyEntryCount
    s.mu.RUnlock()

    // Build the event
    event := &protocol.Event{
        ID: fmt.Sprintf("session_config_%s", s.id),
        Msg: &protocol.EventSessionConfigured{
            SessionID:         s.id,
            Model:             turnCtx.Model,
            ReasoningEffort:   turnCtx.Effort,
            HistoryLogID:      historyLogID,
            HistoryEntryCount: historyEntryCount,
            InitialMessages:   nil, // TODO: populate if needed for resume
            RolloutPath:       "",  // Not implemented yet
        },
    }

    return s.EmitEvent(ctx, event)
}
