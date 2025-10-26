package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/evmts/codex/codex-go/internal/conversation/manager"
	"github.com/evmts/codex/codex-go/internal/protocol"
)

// Model represents the TUI application state
type Model struct {
	// Core state
	viewMode        ViewMode
	conversationMgr manager.ConversationManager
	keys            KeyMap

	// Session management
	sessions       []string
	selectedIdx    int
	currentSession *manager.Session

	// Conversation state
	messages      []Message
	streamingText string
	inputText     textinput.Model

	// Tool approval state
	pendingTool      *PendingToolApproval
	toolApprovalChan chan bool

	// Status
	model       string
	totalTokens int
	err         error

	// UI state
	width  int
	height int
	ready  bool
}

// PendingToolApproval represents a tool waiting for approval
type PendingToolApproval struct {
	ToolName   string
	Parameters map[string]interface{}
	RiskLevel  string
}

// NewModel creates a new TUI model
func NewModel(mgr manager.ConversationManager) Model {
	ti := textinput.New()
	ti.Placeholder = "Type your message..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 80

	return Model{
		viewMode:         ViewModeSessionList,
		conversationMgr:  mgr,
		keys:             DefaultKeyMap(),
		sessions:         []string{},
		selectedIdx:      0,
		messages:         []Message{},
		inputText:        ti,
		model:            "claude-3-5-sonnet-20241022",
		totalTokens:      0,
		width:            80,
		height:           24,
		toolApprovalChan: make(chan bool, 1),
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.viewMode == ViewModeSessionList || m.pendingTool == nil {
				return m, tea.Quit
			}

		case "n":
			if m.viewMode == ViewModeSessionList {
				return m, m.createNewSession()
			}

		case "enter":
			if m.viewMode == ViewModeSessionList {
				// Select session
				if len(m.sessions) > 0 && m.selectedIdx < len(m.sessions) {
					m.currentSession = m.getSession(m.sessions[m.selectedIdx])
					m.viewMode = ViewModeConversation
					m.loadMessages()
				}
			} else if m.viewMode == ViewModeConversation && m.pendingTool == nil {
				// Submit message
				if m.inputText.Value() != "" {
					return m, m.submitMessage()
				}
			}

		case "a":
			if m.pendingTool != nil {
				m.approveTool()
				m.pendingTool = nil
				m.viewMode = ViewModeConversation
			}

		case "d":
			if m.pendingTool != nil {
				m.denyTool()
				m.pendingTool = nil
				m.viewMode = ViewModeConversation
			}

		case "up", "k":
			if m.viewMode == ViewModeSessionList && m.selectedIdx > 0 {
				m.selectedIdx--
			}

		case "down", "j":
			if m.viewMode == ViewModeSessionList && m.selectedIdx < len(m.sessions)-1 {
				m.selectedIdx++
			}
		}

	case streamingMsg:
		m.streamingText += msg.text
		return m, waitForStreaming(msg.done)

	case streamingDoneMsg:
		m.messages = append(m.messages, Message{
			Role:    "assistant",
			Content: m.streamingText,
		})
		m.streamingText = ""
		return m, nil

	case toolApprovalMsg:
		m.pendingTool = &PendingToolApproval{
			ToolName:   msg.toolName,
			Parameters: msg.params,
			RiskLevel:  msg.riskLevel,
		}
		m.viewMode = ViewModeToolApproval
		return m, nil

	case errorMsg:
		m.err = msg.err
		return m, nil
	}

	// Update text input if in conversation mode
	if m.viewMode == ViewModeConversation && m.pendingTool == nil {
		m.inputText, cmd = m.inputText.Update(msg)
		return m, cmd
	}

	return m, nil
}

// View renders the current view
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var content string

	switch m.viewMode {
	case ViewModeSessionList:
		content = RenderSessionList(m.sessions, m.selectedIdx)

	case ViewModeConversation:
		content = RenderConversation(
			m.getCurrentSessionID(),
			m.messages,
			m.streamingText,
			m.inputText.Value(),
		)

	case ViewModeToolApproval:
		conversationView := RenderConversation(
			m.getCurrentSessionID(),
			m.messages,
			m.streamingText,
			m.inputText.Value(),
		)
		toolPanel := RenderToolApproval(
			m.pendingTool.ToolName,
			m.pendingTool.Parameters,
			m.pendingTool.RiskLevel,
		)
		content = conversationView + "\n" + toolPanel
	}

	// Add error if present
	if m.err != nil {
		content += "\n\n" + RenderError(m.err)
	}

	// Add status bar
	var modeStr string
	switch m.viewMode {
	case ViewModeSessionList:
		modeStr = "session-list"
	case ViewModeConversation:
		modeStr = "conversation"
	case ViewModeToolApproval:
		modeStr = "tool-approval"
	default:
		modeStr = "unknown"
	}
	statusBar := RenderStatusBar(m.model, m.totalTokens, modeStr, m.width)

	// Add help
	help := RenderHelp()

	return content + "\n" + statusBar + "\n" + help
}

// Helper methods

func (m *Model) createNewSession() tea.Cmd {
	return func() tea.Msg {
		// Generate a new session ID
		sessionID := fmt.Sprintf("session-%d", len(m.sessions)+1)

		// Create session
		ctx := context.Background()
		_, err := m.conversationMgr.CreateSession(ctx, manager.SessionConfig{
			ID:     sessionID,
			Client: nil, // Uses manager's default client
			TurnContext: &manager.TurnContext{
				Cwd:            ".",
				ApprovalPolicy: "auto",
				SandboxPolicy:  protocol.SandboxPolicy{Mode: "native"},
				Model:          m.model,
			},
		})

		if err != nil {
			return errorMsg{err: err}
		}

		m.sessions = append(m.sessions, sessionID)
		m.selectedIdx = len(m.sessions) - 1
		return nil
	}
}

func (m *Model) getSession(sessionID string) *manager.Session {
	session, err := m.conversationMgr.GetSession(sessionID)
	if err != nil {
		m.err = err
		return nil
	}
	return session
}

func (m *Model) loadMessages() {
	// In a real implementation, load message history
	// For now, start with empty messages
	m.messages = []Message{}
}

func (m *Model) getCurrentSessionID() string {
	if len(m.sessions) > 0 && m.selectedIdx < len(m.sessions) {
		return m.sessions[m.selectedIdx]
	}
	return "no session"
}

func (m *Model) submitMessage() tea.Cmd {
	userInput := m.inputText.Value()
	m.messages = append(m.messages, Message{
		Role:    "user",
		Content: userInput,
	})
	m.inputText.SetValue("")
	m.streamingText = ""

	return func() tea.Msg {
		// Submit to conversation manager
		ctx := context.Background()
		textPtr := &userInput
		op := &protocol.OpUserInput{
			Items: []protocol.UserInput{
				{
					Type: "text",
					Text: textPtr,
				},
			},
		}

		err := m.conversationMgr.SubmitOp(ctx, m.getCurrentSessionID(), op)
		if err != nil {
			return errorMsg{err: err}
		}

		// Simulate streaming (in real impl, listen to events)
		return simulateStreaming("This is a simulated response.")
	}
}

func (m *Model) approveTool() {
	select {
	case m.toolApprovalChan <- true:
	default:
	}
}

func (m *Model) denyTool() {
	select {
	case m.toolApprovalChan <- false:
	default:
	}
}

// Message types for tea.Msg

type streamingMsg struct {
	text string
	done chan bool
}

type streamingDoneMsg struct{}

type toolApprovalMsg struct {
	toolName  string
	params    map[string]interface{}
	riskLevel string
}

type errorMsg struct {
	err error
}

// Commands

func waitForStreaming(done chan bool) tea.Cmd {
	return func() tea.Msg {
		<-done
		return streamingDoneMsg{}
	}
}

func simulateStreaming(text string) tea.Cmd {
	return func() tea.Msg {
		done := make(chan bool)

		// Split text into chunks for streaming effect
		chunks := strings.Split(text, " ")

		go func() {
			for range chunks {
				// In real impl, this would come from the streaming API
			}
			close(done)
		}()

		return streamingMsg{
			text: text,
			done: done,
		}
	}
}

// Run starts the TUI application
func Run(mgr manager.ConversationManager) error {
	p := tea.NewProgram(NewModel(mgr), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
