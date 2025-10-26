package patch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PatchOperation indicates the type of file operation.
type PatchOperation int

const (
	// OperationAdd indicates a new file is being created.
	OperationAdd PatchOperation = iota

	// OperationDelete indicates a file is being removed.
	OperationDelete

	// OperationUpdate indicates a file is being modified in place.
	OperationUpdate

	// OperationMove indicates a file is being renamed/moved.
	OperationMove
)

// FilePatch represents changes to a single file.
type FilePatch struct {
	OriginalFile string
	NewFile      string
	Operation    PatchOperation
	Hunks        []Hunk
}

// Hunk represents a continuous block of changes within a file.
type Hunk struct {
	OriginalStart int
	OriginalLines int
	NewStart      int
	NewLines      int
	Lines         []Line
}

// Line represents a single line in a hunk.
type Line struct {
	Type    LineType
	Content string
}

// LineType indicates what kind of line this is.
type LineType int

const (
	// LineContext is an unchanged context line.
	LineContext LineType = iota

	// LineAdd is a line being added.
	LineAdd

	// LineRemove is a line being removed.
	LineRemove
)

var (
	// Regular expressions for parsing unified diff format
	fileHeaderRegex = regexp.MustCompile(`^--- (.+)$`)
	newFileRegex    = regexp.MustCompile(`^\+\+\+ (.+)$`)
	hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	binaryFileRegex = regexp.MustCompile(`^Binary files .+ differ$`)
)

// parseUnifiedDiff parses a unified diff format string into FilePatch structures.
func parseUnifiedDiff(diff string) ([]FilePatch, error) {
	if strings.TrimSpace(diff) == "" {
		return nil, NewPatchError(ErrorParse, "empty diff")
	}

	lines := strings.Split(diff, "\n")
	var patches []FilePatch
	var currentPatch *FilePatch
	var currentHunk *Hunk

	i := 0
	for i < len(lines) {
		line := lines[i]

		// Check for binary file indicator
		if binaryFileRegex.MatchString(line) {
			return nil, NewPatchError(ErrorParse, "binary files are not supported")
		}

		// Check for file header (---)
		if matches := fileHeaderRegex.FindStringSubmatch(line); matches != nil {
			// Save previous patch if exists
			if currentPatch != nil {
				if currentHunk != nil {
					currentPatch.Hunks = append(currentPatch.Hunks, *currentHunk)
					currentHunk = nil
				}
				patches = append(patches, *currentPatch)
			}

			// Start new patch
			currentPatch = &FilePatch{
				OriginalFile: cleanPath(matches[1]),
				Hunks:        []Hunk{},
			}

			// Next line should be +++ header
			i++
			if i >= len(lines) {
				return nil, NewPatchError(ErrorParse, "missing +++ header after --- header")
			}

			if matches := newFileRegex.FindStringSubmatch(lines[i]); matches != nil {
				currentPatch.NewFile = cleanPath(matches[1])
			} else {
				return nil, NewPatchError(ErrorParse, fmt.Sprintf("expected +++ header, got: %s", lines[i]))
			}

			// Determine operation type
			currentPatch.Operation = determineOperation(currentPatch.OriginalFile, currentPatch.NewFile)

			i++
			continue
		}

		// Check for hunk header (@@)
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			if currentPatch == nil {
				return nil, NewPatchError(ErrorParse, "hunk header without file header")
			}

			// Save previous hunk if exists
			if currentHunk != nil {
				currentPatch.Hunks = append(currentPatch.Hunks, *currentHunk)
			}

			// Parse hunk header
			originalStart, _ := strconv.Atoi(matches[1])
			originalLines := 1
			if matches[2] != "" {
				originalLines, _ = strconv.Atoi(matches[2])
			}

			newStart, _ := strconv.Atoi(matches[3])
			newLines := 1
			if matches[4] != "" {
				newLines, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &Hunk{
				OriginalStart: originalStart,
				OriginalLines: originalLines,
				NewStart:      newStart,
				NewLines:      newLines,
				Lines:         []Line{},
			}

			i++
			continue
		}

		// Parse hunk content lines
		if currentHunk != nil && len(line) > 0 {
			var lineType LineType
			content := line

			switch line[0] {
			case ' ':
				lineType = LineContext
				content = line[1:]
			case '+':
				lineType = LineAdd
				content = line[1:]
			case '-':
				lineType = LineRemove
				content = line[1:]
			case '\\':
				// Handle "\ No newline at end of file" marker
				i++
				continue
			default:
				// Empty line or end of hunk
				if strings.TrimSpace(line) == "" {
					i++
					continue
				}
			}

			currentHunk.Lines = append(currentHunk.Lines, Line{
				Type:    lineType,
				Content: content,
			})
		}

		i++
	}

	// Save last patch and hunk
	if currentPatch != nil {
		if currentHunk != nil {
			currentPatch.Hunks = append(currentPatch.Hunks, *currentHunk)
		}
		patches = append(patches, *currentPatch)
	}

	if len(patches) == 0 {
		return nil, NewPatchError(ErrorParse, "no valid patches found in diff")
	}

	// Validate that each patch has at least one hunk (unless it's a delete with no hunks)
	for i, patch := range patches {
		if len(patch.Hunks) == 0 && patch.Operation != OperationDelete {
			return nil, NewPatchError(ErrorParse, fmt.Sprintf("patch %d has no hunks", i))
		}
	}

	return patches, nil
}

// cleanPath removes the a/ or b/ prefix and handles /dev/null.
func cleanPath(path string) string {
	path = strings.TrimSpace(path)

	if path == "/dev/null" {
		return ""
	}

	// Remove a/ or b/ prefix
	if strings.HasPrefix(path, "a/") {
		return path[2:]
	}
	if strings.HasPrefix(path, "b/") {
		return path[2:]
	}

	return path
}

// determineOperation determines what operation is being performed based on file paths.
func determineOperation(originalFile, newFile string) PatchOperation {
	if originalFile == "" && newFile != "" {
		return OperationAdd
	}
	if originalFile != "" && newFile == "" {
		return OperationDelete
	}
	if originalFile == newFile {
		return OperationUpdate
	}
	// Different paths = move/rename
	return OperationMove
}

// validatePatch performs validation on a parsed patch.
func validatePatch(patch *FilePatch) error {
	if patch.Operation == OperationAdd && patch.NewFile == "" {
		return NewPatchError(ErrorInvalidHunk, "add operation requires new file path")
	}
	if patch.Operation == OperationDelete && patch.OriginalFile == "" {
		return NewPatchError(ErrorInvalidHunk, "delete operation requires original file path")
	}
	if patch.Operation == OperationUpdate && patch.OriginalFile == "" {
		return NewPatchError(ErrorInvalidHunk, "update operation requires file path")
	}

	for _, hunk := range patch.Hunks {
		if err := validateHunk(&hunk); err != nil {
			return err
		}
	}

	return nil
}

// validateHunk validates a single hunk's line counts match actual lines.
func validateHunk(hunk *Hunk) error {
	addCount := 0
	removeCount := 0
	contextCount := 0

	for _, line := range hunk.Lines {
		switch line.Type {
		case LineAdd:
			addCount++
		case LineRemove:
			removeCount++
		case LineContext:
			contextCount++
		}
	}

	// The counts in the hunk header should match the actual line counts
	expectedOriginalLines := removeCount + contextCount
	expectedNewLines := addCount + contextCount

	if hunk.OriginalLines != expectedOriginalLines {
		return NewPatchError(ErrorInvalidHunk,
			fmt.Sprintf("original line count mismatch: header says %d, actual is %d",
				hunk.OriginalLines, expectedOriginalLines))
	}

	if hunk.NewLines != expectedNewLines {
		return NewPatchError(ErrorInvalidHunk,
			fmt.Sprintf("new line count mismatch: header says %d, actual is %d",
				hunk.NewLines, expectedNewLines))
	}

	return nil
}
