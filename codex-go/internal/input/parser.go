package input

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// FileReference represents a parsed file reference from @ syntax
type FileReference struct {
	// Original token including @
	Original string
	// Path is the resolved absolute path
	Path string
	// DisplayName is the user-friendly name shown in output
	DisplayName string
	// StartIndex is the position in the original text
	StartIndex int
	// EndIndex is the end position in the original text
	EndIndex int
}

// ParseResult contains the parsed input with file references extracted
type ParseResult struct {
	// ProcessedText is the input with @ references replaced by placeholders
	ProcessedText string
	// FileReferences are the extracted file references
	FileReferences []FileReference
}

// ParseFileReferences extracts @ file references from user input
// Examples:
//   - "Check @config.json" -> extracts config.json
//   - "Review @src/main.go" -> extracts src/main.go
//   - "Compare @"file with spaces.txt"" -> extracts file with spaces.txt
func ParseFileReferences(input string, workingDir string) (*ParseResult, error) {
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	var refs []FileReference
	var processedText strings.Builder

	i := 0
	for i < len(input) {
		// Look for @ symbol
		if input[i] == '@' {
			// Found potential file reference
			ref, endIdx, err := extractFileReference(input, i, workingDir)
			if err != nil {
				// Not a valid file reference, keep the @ symbol
				processedText.WriteByte(input[i])
				i++
				continue
			}

			refs = append(refs, ref)

			// Replace with placeholder that includes filename
			placeholder := fmt.Sprintf("[file: %s]", ref.DisplayName)
			processedText.WriteString(placeholder)

			i = endIdx
		} else {
			processedText.WriteByte(input[i])
			i++
		}
	}

	return &ParseResult{
		ProcessedText:  processedText.String(),
		FileReferences: refs,
	}, nil
}

// extractFileReference extracts a single file reference starting at position i (which points to @)
// Returns the FileReference, the end index (exclusive), and any error
func extractFileReference(input string, startIdx int, workingDir string) (FileReference, int, error) {
	if startIdx >= len(input) || input[startIdx] != '@' {
		return FileReference{}, 0, fmt.Errorf("not at @ symbol")
	}

	i := startIdx + 1 // Skip the @

	// Check if path is quoted
	var pathStr string
	var endIdx int

	if i < len(input) && input[i] == '"' {
		// Quoted path: read until closing quote
		i++ // Skip opening quote
		start := i
		for i < len(input) && input[i] != '"' {
			if input[i] == '\\' && i+1 < len(input) {
				// Handle escaped quotes
				i += 2
			} else {
				i++
			}
		}
		if i >= len(input) {
			return FileReference{}, 0, fmt.Errorf("unclosed quote in file reference")
		}
		pathStr = input[start:i]
		i++ // Skip closing quote
		endIdx = i
	} else {
		// Unquoted path: read until whitespace or end
		start := i
		for i < len(input) && !unicode.IsSpace(rune(input[i])) {
			i++
		}
		if i == start {
			return FileReference{}, 0, fmt.Errorf("empty file reference")
		}
		pathStr = input[start:i]
		endIdx = i
	}

	// Validate and resolve path
	absPath, displayName, err := resolvePath(pathStr, workingDir)
	if err != nil {
		return FileReference{}, 0, err
	}

	return FileReference{
		Original:    input[startIdx:endIdx],
		Path:        absPath,
		DisplayName: displayName,
		StartIndex:  startIdx,
		EndIndex:    endIdx,
	}, endIdx, nil
}

// resolvePath resolves a file path to absolute path and validates it
// Returns (absolutePath, displayName, error)
func resolvePath(path string, workingDir string) (string, string, error) {
	if path == "" {
		return "", "", fmt.Errorf("empty path")
	}

	// Security: prevent path traversal attacks
	cleaned := filepath.Clean(path)

	// Resolve to absolute path
	var absPath string
	if filepath.IsAbs(cleaned) {
		absPath = cleaned
	} else {
		absPath = filepath.Join(workingDir, cleaned)
	}

	// Ensure the resolved path is within working directory or explicitly allowed
	// This prevents ../../../etc/passwd style attacks
	if !filepath.IsAbs(path) {
		// For relative paths, ensure they don't escape working directory
		relPath, err := filepath.Rel(workingDir, absPath)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return "", "", fmt.Errorf("path traversal not allowed: %s", path)
		}
	}

	// Check if file exists and is readable
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("file not found: %s", path)
		}
		return "", "", fmt.Errorf("cannot access file: %w", err)
	}

	// Ensure it's a regular file, not a directory
	if info.IsDir() {
		return "", "", fmt.Errorf("path is a directory, not a file: %s", path)
	}

	// Use original path as display name if relative, otherwise use basename
	displayName := path
	if filepath.IsAbs(path) {
		displayName = filepath.Base(path)
	}

	return absPath, displayName, nil
}

// GetCurrentToken extracts the @ token at the cursor position
// This is used for autocomplete functionality
// Returns the token WITHOUT the leading @ symbol, and the start position
func GetCurrentToken(input string, cursorPos int) (token string, startPos int, hasAt bool) {
	if cursorPos < 0 || cursorPos > len(input) {
		return "", 0, false
	}

	// Adjust cursor position if at end or on whitespace
	searchStart := cursorPos
	if searchStart > 0 && (searchStart >= len(input) || unicode.IsSpace(rune(input[searchStart]))) {
		searchStart--
	}

	// Find potential @ symbols by scanning backwards
	// We'll check each @ to see if its token contains the cursor
	for i := searchStart; i >= 0; i-- {
		if input[i] == '@' {
			// Found an @, parse forward to see if cursor is within this token
			tokenEnd := i + 1
			if tokenEnd >= len(input) {
				// @ is at end of input
				if cursorPos >= i {
					return "", i, true
				}
				continue
			}

			// Check if this is a quoted path
			if input[tokenEnd] == '"' {
				tokenEnd++ // Skip opening quote
				quoteStart := tokenEnd
				for tokenEnd < len(input) && input[tokenEnd] != '"' {
					if input[tokenEnd] == '\\' && tokenEnd+1 < len(input) {
						tokenEnd += 2
					} else {
						tokenEnd++
					}
				}
				// Check if cursor is within this quoted token
				if cursorPos >= i && cursorPos <= tokenEnd {
					return input[quoteStart:tokenEnd], i, true
				}
			} else {
				// Unquoted path
				tokenStart := tokenEnd
				for tokenEnd < len(input) && !unicode.IsSpace(rune(input[tokenEnd])) {
					tokenEnd++
				}
				// Check if cursor is within this unquoted token
				if cursorPos >= i && cursorPos <= tokenEnd {
					return input[tokenStart:tokenEnd], i, true
				}
			}

			// If cursor is before this token, stop searching
			if cursorPos < i {
				return "", 0, false
			}
		}
	}

	return "", 0, false
}
