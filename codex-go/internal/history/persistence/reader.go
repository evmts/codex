package persistence

import (
	"bufio"
	"bytes"
	"fmt"
	"io"

	"github.com/evmts/codex/codex-go/internal/protocol"
	"github.com/spf13/afero"
)

// HistoryReader provides reading from a history file in JSONL format.
// It supports both line-by-line reading and batch reading.
type HistoryReader struct {
	fs       afero.Fs
	path     string
	file     afero.File
	scanner  *bufio.Scanner
	position int64
	closed   bool
}

// NewHistoryReader creates a new HistoryReader for the given path.
func NewHistoryReader(fs afero.Fs, path string) (*HistoryReader, error) {
	file, err := fs.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}

	return &HistoryReader{
		fs:       fs,
		path:     path,
		file:     file,
		scanner:  bufio.NewScanner(file),
		position: 0,
		closed:   false,
	}, nil
}

// ReadNext reads the next Submission or Event from the file.
// Returns (submission, nil, nil) for submissions, (nil, event, nil) for events,
// or (nil, nil, error) on failure or EOF.
func (r *HistoryReader) ReadNext() (*protocol.Submission, *protocol.Event, error) {
	if r.closed {
		return nil, nil, fmt.Errorf("reader is closed")
	}

	for r.scanner.Scan() {
		line := r.scanner.Bytes()
		r.position += int64(len(line)) + 1 // +1 for newline

		// Skip empty lines
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		// Parse the line
		sub, evt, err := UnmarshalHistoryLine(line)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse line at position %d: %w", r.position, err)
		}

		return sub, evt, nil
	}

	if err := r.scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("scanner error: %w", err)
	}

	return nil, nil, io.EOF
}

// ReadAll reads all Submissions and Events from the file.
// Returns separate slices for submissions and events.
func (r *HistoryReader) ReadAll() ([]*protocol.Submission, []*protocol.Event, error) {
	var submissions []*protocol.Submission
	var events []*protocol.Event

	for {
		sub, evt, err := r.ReadNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}

		if sub != nil {
			submissions = append(submissions, sub)
		}
		if evt != nil {
			events = append(events, evt)
		}
	}

	return submissions, events, nil
}

// Position returns the current byte position in the file.
func (r *HistoryReader) Position() int64 {
	return r.position
}

// Close closes the underlying file.
func (r *HistoryReader) Close() error {
	if r.closed {
		return nil
	}

	r.closed = true

	if err := r.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}

	return nil
}

// Path returns the file path of this reader.
func (r *HistoryReader) Path() string {
	return r.path
}
