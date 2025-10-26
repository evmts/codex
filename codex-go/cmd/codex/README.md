# Codex Go TUI

The Codex Go terminal user interface (TUI) provides an interactive chat experience for AI-assisted coding sessions.

## Features

- **Session Management**: Create and switch between multiple conversation sessions
- **Interactive Conversations**: Type messages and receive streaming responses  
- **Tool Approval**: Review and approve tool executions with detailed parameter inspection
- **Status Bar**: Real-time display of model, token usage, and current mode
- **Keyboard Shortcuts**: Efficient navigation without a mouse

## Installation

```bash
go build -o codex ./cmd/codex
./codex
```

## Keyboard Shortcuts

### Navigation
- `↑`/`k`: Move up in lists
- `↓`/`j`: Move down in lists
- `Enter`: Select item / Submit message

### Actions
- `n`: Create new session
- `a`: Approve tool execution
- `d`: Deny tool execution
- `q` / `Ctrl+C`: Quit application

## Views

### Session List View

The initial view showing all available sessions. Press `n` to create a new session or select an existing one with `Enter`.

### Conversation View

The main chat interface where you interact with the AI assistant. Type your message and press `Enter` to submit.

### Tool Approval View

When the AI requests to execute a tool, an approval panel appears showing:
- Tool name
- Parameters
- Risk level

Press `a` to approve or `d` to deny the tool execution.

## Architecture

The TUI is built using [Bubble Tea](https://github.com/charmbracelet/bubbletea), a Go framework for building terminal applications using The Elm Architecture.

### Components

- **app.go**: Main Bubble Tea model and update logic
- **views.go**: Rendering functions for different UI views
- **keys.go**: Keyboard binding definitions

### Integration

The TUI integrates with internal packages:
- `internal/conversation/manager`: Session and conversation management
- `internal/protocol`: Protocol types for operations and events
- `internal/client`: AI model client interface

## Development

### Testing

The TUI is designed with testable components. Core logic is separated from UI rendering to enable unit testing.

### Future Enhancements

- History persistence and session restoration
- Syntax highlighting for code blocks
- File tree viewer for workspace navigation
- Search and filter capabilities
- Configuration customization (themes, keybindings)
