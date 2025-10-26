// Package persistence provides history persistence functionality for Codex sessions.
//
// This package handles reading and writing session history to disk in JSONL format,
// with support for rollouts (timestamped snapshots), atomic writes, and fsync for
// durability.
//
// Key features:
// - Append-only JSONL format (one Op/Event per line)
// - Atomic writes with fsync for durability
// - Rollout support (history.jsonl.1234567890 snapshots)
// - Session directory management (~/.codex/sessions/)
// - Resume from history (replay Ops/Events)
// - Concurrent read safety
// - Uses afero.Fs abstraction for testing
package persistence

import (
	"fmt"
	"path/filepath"

	"github.com/evmts/codex/codex-go/internal/protocol"
	"github.com/spf13/afero"
)

// HistoryPersistence manages persistence of session history to disk.
// It provides high-level operations for recording and loading history,
// managing rollouts, and accessing session metadata.
type HistoryPersistence struct {
	fs         afero.Fs
	sessionDir string
	sessionID  string
	writer     *HistoryWriter
}

// NewHistoryPersistence creates a new HistoryPersistence for the given session directory.
// The session directory should be the full path to the session (e.g., ~/.codex/sessions/session-id).
// It creates the directory if it doesn't exist and opens the history file for writing.
// Directories are created with SensitiveDirMode (0700) to protect sensitive session data.
func NewHistoryPersistence(fs afero.Fs, sessionDir string) (*HistoryPersistence, error) {
	// Create session directory if it doesn't exist
	// Use 0700 to ensure only the owner can access session data
	if err := fs.MkdirAll(sessionDir, SensitiveDirMode); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Extract session ID from path
	sessionID := filepath.Base(sessionDir)

	// Open history writer
	historyPath := filepath.Join(sessionDir, "history.jsonl")
	writer, err := NewHistoryWriter(fs, historyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create history writer: %w", err)
	}

	return &HistoryPersistence{
		fs:         fs,
		sessionDir: sessionDir,
		sessionID:  sessionID,
		writer:     writer,
	}, nil
}

// RecordSubmission appends a Submission to the history file.
func (hp *HistoryPersistence) RecordSubmission(submission *protocol.Submission) error {
	return hp.writer.Append(submission)
}

// RecordEvent appends an Event to the history file.
func (hp *HistoryPersistence) RecordEvent(event *protocol.Event) error {
	return hp.writer.Append(event)
}

// LoadHistory reads all Submissions and Events from the history file.
// Returns separate slices for submissions and events.
func (hp *HistoryPersistence) LoadHistory() ([]*protocol.Submission, []*protocol.Event, error) {
	reader, err := NewHistoryReader(hp.fs, hp.HistoryPath())
	if err != nil {
		// If file doesn't exist, return empty history
		if !fileExists(hp.fs, hp.HistoryPath()) {
			return []*protocol.Submission{}, []*protocol.Event{}, nil
		}
		return nil, nil, fmt.Errorf("failed to create history reader: %w", err)
	}
	defer reader.Close()

	return reader.ReadAll()
}

// CreateRollout creates a timestamped snapshot of the current history file.
// Returns the path to the created rollout file.
func (hp *HistoryPersistence) CreateRollout() (string, error) {
	// Flush any buffered data first
	if err := hp.writer.Flush(); err != nil {
		return "", fmt.Errorf("failed to flush before rollout: %w", err)
	}

	return CreateRollout(hp.fs, hp.HistoryPath())
}

// ListRollouts returns a list of all rollout files for this session,
// sorted by timestamp (oldest first).
func (hp *HistoryPersistence) ListRollouts() ([]string, error) {
	return ListRollouts(hp.fs, hp.HistoryPath())
}

// CleanupOldRollouts keeps only the most recent N rollouts and deletes the rest.
func (hp *HistoryPersistence) CleanupOldRollouts(keepCount int) error {
	return CleanupOldRollouts(hp.fs, hp.HistoryPath(), keepCount)
}

// Flush flushes any buffered data to disk.
func (hp *HistoryPersistence) Flush() error {
	return hp.writer.Flush()
}

// Close flushes any buffered data and closes the history file.
func (hp *HistoryPersistence) Close() error {
	return hp.writer.Close()
}

// SessionID returns the session ID.
func (hp *HistoryPersistence) SessionID() string {
	return hp.sessionID
}

// SessionDir returns the session directory path.
func (hp *HistoryPersistence) SessionDir() string {
	return hp.sessionDir
}

// HistoryPath returns the full path to the history file.
func (hp *HistoryPersistence) HistoryPath() string {
	return hp.writer.Path()
}

// GetSessionDir returns the session directory path for a given session ID.
func GetSessionDir(sessionsRoot, sessionID string) string {
	return filepath.Join(sessionsRoot, sessionID)
}

// GetSessionHistoryPath returns the full path to the history file for a given session ID.
func GetSessionHistoryPath(sessionsRoot, sessionID string) string {
	return filepath.Join(GetSessionDir(sessionsRoot, sessionID), "history.jsonl")
}

// fileExists checks if a file exists.
func fileExists(fs afero.Fs, path string) bool {
	exists, err := afero.Exists(fs, path)
	return err == nil && exists
}
