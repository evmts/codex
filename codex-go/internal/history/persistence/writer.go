package persistence

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/spf13/afero"
)

// HistoryWriter provides append-only writing to a history file in JSONL format.
// It is safe for concurrent use and ensures durability through fsync.
type HistoryWriter struct {
	fs     afero.Fs
	path   string
	file   afero.File
	writer *bufio.Writer
	mu     sync.Mutex
	closed bool
}

// NewHistoryWriter creates a new HistoryWriter for the given path.
// It creates parent directories if they don't exist and opens the file
// in append mode, creating it if necessary.
func NewHistoryWriter(fs afero.Fs, path string) (*HistoryWriter, error) {
	// Create parent directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := fs.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Open file in append mode (create if doesn't exist)
	file, err := fs.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}

	return &HistoryWriter{
		fs:     fs,
		path:   path,
		file:   file,
		writer: bufio.NewWriter(file),
		closed: false,
	}, nil
}

// Append writes a Submission or Event to the history file.
// Each item is written as a single JSON line followed by a newline.
// This method is thread-safe.
func (w *HistoryWriter) Append(item interface{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("writer is closed")
	}

	// Marshal the item
	data, err := MarshalHistoryLine(item)
	if err != nil {
		return fmt.Errorf("failed to marshal item: %w", err)
	}

	// Write the JSON line
	if _, err := w.writer.Write(data); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	// Write newline
	if _, err := w.writer.Write([]byte("\n")); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// Flush to ensure data is written
	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	// Sync to disk for durability (if supported by the filesystem)
	// Note: afero's memory filesystem doesn't support Sync, so we ignore errors
	if syncer, ok := w.file.(interface{ Sync() error }); ok {
		_ = syncer.Sync()
	}

	return nil
}

// Flush flushes any buffered data to the underlying file.
// This method is thread-safe.
func (w *HistoryWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("writer is closed")
	}

	if err := w.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	// Sync to disk for durability
	if syncer, ok := w.file.(interface{ Sync() error }); ok {
		if err := syncer.Sync(); err != nil {
			return fmt.Errorf("failed to sync file: %w", err)
		}
	}

	return nil
}

// Close flushes any buffered data and closes the underlying file.
// This method is thread-safe and idempotent.
func (w *HistoryWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}

	w.closed = true

	// Flush buffer
	if err := w.writer.Flush(); err != nil {
		_ = w.file.Close() // Best effort cleanup
		return fmt.Errorf("failed to flush buffer: %w", err)
	}

	// Close file
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

// Path returns the file path of this writer.
func (w *HistoryWriter) Path() string {
	return w.path
}
