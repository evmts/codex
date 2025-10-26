package manager

import (
    "context"
    "testing"
    "time"

    "github.com/evmts/codex/codex-go/internal/client/mocks"
    "github.com/evmts/codex/codex-go/internal/protocol"
    "github.com/evmts/codex/codex-go/internal/tools/runtime"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "go.uber.org/mock/gomock"
)

func TestSessionApprovalHandler_AutoPolicySkipsApproval(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockClient := mocks.NewMockClient(ctrl)

    // Create session in processing state via SubmitTurn
    sess := createTestSession(t, mockClient)
    op := &protocol.OpUserTurn{
        Items:          []protocol.UserInput{{Type: "text", Text: strPtr("hello")}},
        Cwd:            ".",
        ApprovalPolicy: "auto",
        SandboxPolicy:  protocol.SandboxPolicy{Mode: "native"},
        Model:          "gpt-4",
    }
    submissionID, err := sess.SubmitTurn(context.Background(), op)
    require.NoError(t, err)

    // Collect events
    var gotApprovalEvent bool
    handler := func(ctx context.Context, e *protocol.Event) error {
        if _, ok := e.Msg.(*protocol.EventToolCallApprovalNeeded); ok {
            gotApprovalEvent = true
        }
        return nil
    }
    sess.eventHandlers = append(sess.eventHandlers, handler)

    // Create approval handler
    sah := NewSessionApprovalHandler(sess, submissionID)

    // Invoke HandleApproval with any request; expect immediate approval without events
    req := &runtime.ApprovalRequest{CallID: "call-1", ToolName: "shell", Command: []string{"echo", "hi"}}
    decision, err := sah.HandleApproval(context.Background(), req)
    require.NoError(t, err)
    assert.Equal(t, runtime.ApprovalApprovedForSession, decision)
    assert.False(t, gotApprovalEvent, "auto policy should not emit approval needed event")
    // State remains processing
    assert.Equal(t, StateProcessingTurn, sess.State())
}

func TestSessionApprovalHandler_ManualApprovalFlow(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockClient := mocks.NewMockClient(ctrl)

    sess := createTestSession(t, mockClient)
    op := &protocol.OpUserTurn{
        Items:          []protocol.UserInput{{Type: "text", Text: strPtr("hello")}},
        Cwd:            ".",
        ApprovalPolicy: "manual",
        SandboxPolicy:  protocol.SandboxPolicy{Mode: "workspace-write"},
        Model:          "gpt-4",
    }
    submissionID, err := sess.SubmitTurn(context.Background(), op)
    require.NoError(t, err)

    // Capture approval-needed event
    approvalCh := make(chan *protocol.EventToolCallApprovalNeeded, 1)
    handler := func(ctx context.Context, e *protocol.Event) error {
        if msg, ok := e.Msg.(*protocol.EventToolCallApprovalNeeded); ok {
            select { case approvalCh <- msg: default: }
        }
        return nil
    }
    sess.eventHandlers = append(sess.eventHandlers, handler)

    sah := NewSessionApprovalHandler(sess, submissionID)

    // Run HandleApproval in goroutine (it should block until decision)
    ctx := context.Background()
    req := &runtime.ApprovalRequest{CallID: "call-2", ToolName: "shell", Command: []string{"sh", "-c", "echo hi"}, WorkingDirectory: "."}
    done := make(chan struct{})
    var decision runtime.ApprovalDecision
    var herr error
    go func() {
        decision, herr = sah.HandleApproval(ctx, req)
        close(done)
    }()

    // Expect approval-needed event and state awaiting approval
    select {
    case <-approvalCh:
        assert.Equal(t, StateAwaitingApproval, sess.State())
    case <-time.After(time.Second):
        t.Fatal("timeout waiting for approval-needed event")
    }

    // Send decision
    require.NoError(t, sah.NotifyApprovalDecision("call-2", runtime.ApprovalApproved))

    <-done
    require.NoError(t, herr)
    assert.Equal(t, runtime.ApprovalApproved, decision)
}

func TestSessionApprovalHandler_ContextCancellation(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockClient := mocks.NewMockClient(ctrl)
    sess := createTestSession(t, mockClient)
    op := &protocol.OpUserTurn{
        Items:          []protocol.UserInput{{Type: "text", Text: strPtr("hi")}},
        Cwd:            ".",
        ApprovalPolicy: "manual",
        SandboxPolicy:  protocol.SandboxPolicy{Mode: "workspace-write"},
        Model:          "gpt-4",
    }
    submissionID, err := sess.SubmitTurn(context.Background(), op)
    require.NoError(t, err)

    sah := NewSessionApprovalHandler(sess, submissionID)
    ctx, cancel := context.WithCancel(context.Background())
    cancel()

    req := &runtime.ApprovalRequest{CallID: "call-3", ToolName: "shell"}
    decision, err := sah.HandleApproval(ctx, req)
    require.Error(t, err)
    assert.Equal(t, runtime.ApprovalDenied, decision)
}

func TestSessionApprovalHandler_ConcurrentRequestsError(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockClient := mocks.NewMockClient(ctrl)
    sess := createTestSession(t, mockClient)
    op := &protocol.OpUserTurn{
        Items:          []protocol.UserInput{{Type: "text", Text: strPtr("hi")}},
        Cwd:            ".",
        ApprovalPolicy: "manual",
        SandboxPolicy:  protocol.SandboxPolicy{Mode: "workspace-write"},
        Model:          "gpt-4",
    }
    submissionID, err := sess.SubmitTurn(context.Background(), op)
    require.NoError(t, err)

    sah := NewSessionApprovalHandler(sess, submissionID)

    // First request starts and blocks
    ctx1, cancel1 := context.WithCancel(context.Background())
    defer cancel1()
    req1 := &runtime.ApprovalRequest{CallID: "call-a", ToolName: "shell"}
    started := make(chan struct{})
    go func() {
        close(started)
        _, _ = sah.HandleApproval(ctx1, req1)
    }()
    <-started

    // Second concurrent request should error immediately
    req2 := &runtime.ApprovalRequest{CallID: "call-b", ToolName: "shell"}
    _, err = sah.HandleApproval(context.Background(), req2)
    require.Error(t, err)
    assert.Contains(t, err.Error(), "already pending")
}

