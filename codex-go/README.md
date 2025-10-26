# Codex Go

A complete Go rewrite of Codex TUI, implementing a feature-complete terminal UI using Bubble Tea while maintaining compatibility with the Codex protocol.

## Architecture

Built in layers from core to UI:

```
┌─────────────────────────────────────┐
│         TUI (Bubble Tea)            │  ← cmd/codex, internal/tui
├─────────────────────────────────────┤
│      SDK (Public Go API)            │  ← pkg/sdk
├─────────────────────────────────────┤
│    Tools & MCP Integration          │  ← internal/tools, internal/mcp
├─────────────────────────────────────┤
│  Conversation & History Manager     │  ← internal/conversation, internal/history
├─────────────────────────────────────┤
│      API Client & Streaming         │  ← internal/client, internal/tokencount
├─────────────────────────────────────┤
│   Protocol, Config, Errors          │  ← internal/protocol, internal/config
└─────────────────────────────────────┘
```

## Development

### Prerequisites

- Go 1.21+
- Make

### Setup

```bash
# Install development tools
make install-tools

# Download dependencies
make deps
```

### Testing

```bash
# Run all tests with coverage
make test

# Run unit tests only (fast)
make test-unit

# Update golden test files
make golden-update
```

### Building

```bash
# Build the TUI binary
make build

# Run the binary
./bin/codex
```

### Linting

```bash
make lint
make fmt
```

## Project Structure

- `cmd/codex/` - TUI binary entry point
- `internal/` - Internal packages (not for external use)
  - `protocol/` - Protocol types (Op, Event, Message)
  - `config/` - Configuration schema and loading
  - `errors/` - Error types with context
  - `client/` - API client with streaming
  - `tokencount/` - Token counting and usage tracking
  - `conversation/` - Session and turn state management
  - `history/` - Conversation persistence
  - `tools/` - Tool runtime system and implementations
  - `sandbox/` - Process isolation and sandboxing
  - `mcp/` - Model Context Protocol client
  - `tui/` - Bubble Tea UI components
- `pkg/sdk/` - Public SDK for embedding
- `test/` - Test utilities and fixtures
  - `testdata/` - Test data files
  - `golden/` - Golden file snapshots
  - `fixtures/` - Reusable test fixtures

## Development Approach

This project follows **Test-Driven Development (TDD)**:

1. Write tests first for each package
2. Implement functionality to pass tests
3. Refactor while keeping tests green
4. Use golden files for complex outputs (JSON, ANSI)
5. Mock external dependencies (HTTP, filesystem, exec)

## Feature Parity Checklist

- [ ] Streaming assistant messages and reasoning
- [ ] Exec command output with live deltas
- [ ] Patch diffs with approval workflow
- [ ] Command approval workflow
- [ ] Token usage tracking
- [ ] MCP tool calls and discovery
- [ ] Web search event rendering
- [ ] Plan updates (todo panel)
- [ ] Session resume and history
- [ ] Image attachment and viewing
- [ ] Error handling and display
- [ ] Status panels and indicators

## CI/CD

GitHub Actions workflow runs on every push:
- Lint with golangci-lint
- Test with race detector
- Build for multiple platforms
- Coverage reporting

## License

Same as parent Codex project (see root LICENSE file)
