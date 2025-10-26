package patch

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
)

// ApplyResult contains the results of applying patches.
type ApplyResult struct {
	FilesAffected []string
	Added         []string
	Updated       []string
	Deleted       []string
	Errors        []string
	Summary       string
	DryRun        bool
}

// BackupState stores the original state of files for rollback.
type BackupState struct {
	Path      string
	Content   []byte
	Existed   bool
	Operation string // "update", "add", "delete"
}

// applyPatches applies a set of patches atomically to the filesystem.
// If any patch fails, all changes are rolled back.
func applyPatches(fs afero.Fs, patches []FilePatch, root string, dryRun bool) (*ApplyResult, error) {
	return applyPatchesWithOptions(fs, patches, root, dryRun, false)
}

// applyPatchesWithOptions applies patches with additional options.
func applyPatchesWithOptions(fs afero.Fs, patches []FilePatch, root string, dryRun bool, allowOutsideRoot bool) (*ApplyResult, error) {
	result := &ApplyResult{
		FilesAffected: []string{},
		Added:         []string{},
		Updated:       []string{},
		Deleted:       []string{},
		Errors:        []string{},
		DryRun:        dryRun,
	}

	// Validate all patches first
	for i := range patches {
		if err := validatePatch(&patches[i]); err != nil {
			result.Errors = append(result.Errors, err.Error())
			return result, err
		}
	}

	// Validate paths and check for path traversal
	for _, patch := range patches {
		files := []string{}
		if patch.OriginalFile != "" {
			files = append(files, patch.OriginalFile)
		}
		if patch.NewFile != "" {
			files = append(files, patch.NewFile)
		}

		for _, file := range files {
			if err := validatePath(root, file, allowOutsideRoot); err != nil {
				result.Errors = append(result.Errors, err.Error())
				return result, err
			}
		}
	}

	// Store backups for rollback
	backups := []BackupState{}

	// Apply each patch
	for _, patch := range patches {
		backup, err := applyPatch(fs, &patch, root, dryRun, &backups)
		if err != nil {
			// Rollback all changes
			rollbackErr := rollbackChanges(fs, backups)
			if rollbackErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("rollback failed: %v", rollbackErr))
			}

			result.Errors = append(result.Errors, err.Error())
			result.Summary = fmt.Sprintf("Failed to apply patches: %v (rolled back)", err)
			return result, err
		}

		if backup != nil {
			backups = append(backups, *backup)
		}

		// Track results
		switch patch.Operation {
		case OperationAdd:
			result.Added = append(result.Added, patch.NewFile)
			result.FilesAffected = append(result.FilesAffected, patch.NewFile)
		case OperationDelete:
			result.Deleted = append(result.Deleted, patch.OriginalFile)
			result.FilesAffected = append(result.FilesAffected, patch.OriginalFile)
		case OperationUpdate:
			result.Updated = append(result.Updated, patch.NewFile)
			result.FilesAffected = append(result.FilesAffected, patch.NewFile)
		case OperationMove:
			result.Updated = append(result.Updated, patch.NewFile)
			result.FilesAffected = append(result.FilesAffected, patch.OriginalFile, patch.NewFile)
		}
	}

	// Generate summary
	result.Summary = generateSummary(result)

	return result, nil
}

// applyPatch applies a single patch to the filesystem.
func applyPatch(fs afero.Fs, patch *FilePatch, root string, dryRun bool, backups *[]BackupState) (*BackupState, error) {
	switch patch.Operation {
	case OperationAdd:
		return applyAddFile(fs, patch, root, dryRun, backups)
	case OperationDelete:
		return applyDeleteFile(fs, patch, root, dryRun, backups)
	case OperationUpdate:
		return applyUpdateFile(fs, patch, root, dryRun, backups)
	case OperationMove:
		return applyMoveFile(fs, patch, root, dryRun, backups)
	default:
		return nil, NewPatchError(ErrorParse, fmt.Sprintf("unknown operation: %v", patch.Operation))
	}
}

// applyAddFile creates a new file.
func applyAddFile(fs afero.Fs, patch *FilePatch, root string, dryRun bool, backups *[]BackupState) (*BackupState, error) {
	fullPath := filepath.Join(root, patch.NewFile)

	// Check if file already exists
	exists, err := afero.Exists(fs, fullPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to check if file exists", err)
	}

	backup := &BackupState{
		Path:      fullPath,
		Existed:   exists,
		Operation: "add",
	}

	if exists {
		// Backup existing content
		content, err := afero.ReadFile(fs, fullPath)
		if err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to read existing file", err)
		}
		backup.Content = content
	}

	// Generate new content from hunks
	newContent := generateContentFromHunks(patch.Hunks, true)

	if !dryRun {
		// Ensure directory exists
		dir := filepath.Dir(fullPath)
		if err := fs.MkdirAll(dir, 0755); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to create directory", err)
		}

		// Write file atomically
		if err := atomicWrite(fs, fullPath, []byte(newContent)); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to write file", err)
		}
	}

	return backup, nil
}

// applyDeleteFile removes a file.
func applyDeleteFile(fs afero.Fs, patch *FilePatch, root string, dryRun bool, backups *[]BackupState) (*BackupState, error) {
	fullPath := filepath.Join(root, patch.OriginalFile)

	// Check if file exists
	exists, err := afero.Exists(fs, fullPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to check if file exists", err)
	}

	if !exists {
		return nil, NewPatchErrorWithFile(ErrorFileNotFound, patch.OriginalFile, "file does not exist")
	}

	// Backup content for rollback
	content, err := afero.ReadFile(fs, fullPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to read file for backup", err)
	}

	backup := &BackupState{
		Path:      fullPath,
		Content:   content,
		Existed:   true,
		Operation: "delete",
	}

	if !dryRun {
		if err := fs.Remove(fullPath); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to delete file", err)
		}
	}

	return backup, nil
}

// applyUpdateFile modifies an existing file.
func applyUpdateFile(fs afero.Fs, patch *FilePatch, root string, dryRun bool, backups *[]BackupState) (*BackupState, error) {
	fullPath := filepath.Join(root, patch.OriginalFile)

	// Check if file exists
	exists, err := afero.Exists(fs, fullPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to check if file exists", err)
	}

	if !exists {
		return nil, NewPatchErrorWithFile(ErrorFileNotFound, patch.OriginalFile, "file does not exist")
	}

	// Read current content
	content, err := afero.ReadFile(fs, fullPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to read file", err)
	}

	backup := &BackupState{
		Path:      fullPath,
		Content:   content,
		Existed:   true,
		Operation: "update",
	}

	// Apply hunks to content
	newContent, err := applyHunks(string(content), patch.Hunks)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorConflict, patch.OriginalFile, "failed to apply hunks", err)
	}

	if !dryRun {
		// Write file atomically
		if err := atomicWrite(fs, fullPath, []byte(newContent)); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to write file", err)
		}
	}

	return backup, nil
}

// applyMoveFile renames/moves a file.
func applyMoveFile(fs afero.Fs, patch *FilePatch, root string, dryRun bool, backups *[]BackupState) (*BackupState, error) {
	oldPath := filepath.Join(root, patch.OriginalFile)
	newPath := filepath.Join(root, patch.NewFile)

	// Check if source file exists
	exists, err := afero.Exists(fs, oldPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to check if file exists", err)
	}

	if !exists {
		return nil, NewPatchErrorWithFile(ErrorFileNotFound, patch.OriginalFile, "source file does not exist")
	}

	// Read content
	content, err := afero.ReadFile(fs, oldPath)
	if err != nil {
		return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to read file", err)
	}

	// Apply any content changes from hunks
	newContent := string(content)
	if len(patch.Hunks) > 0 {
		newContent, err = applyHunks(string(content), patch.Hunks)
		if err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorConflict, patch.OriginalFile, "failed to apply hunks during move", err)
		}
	}

	backup := &BackupState{
		Path:      oldPath,
		Content:   content,
		Existed:   true,
		Operation: "move",
	}

	if !dryRun {
		// Ensure destination directory exists
		destDir := filepath.Dir(newPath)
		if err := fs.MkdirAll(destDir, 0755); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to create destination directory", err)
		}

		// Write to new location atomically
		if err := atomicWrite(fs, newPath, []byte(newContent)); err != nil {
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.NewFile, "failed to write file to new location", err)
		}

		// Remove old file
		if err := fs.Remove(oldPath); err != nil {
			// Try to rollback the new file
			_ = fs.Remove(newPath) // nolint:errcheck // Best effort cleanup
			return nil, NewPatchErrorWithFileAndCause(ErrorIO, patch.OriginalFile, "failed to remove old file", err)
		}
	}

	return backup, nil
}

// applyHunks applies a series of hunks to file content.
func applyHunks(content string, hunks []Hunk) (string, error) {
	lines := strings.Split(content, "\n")
	// Remove trailing empty line if content ended with newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Apply hunks in order
	for _, hunk := range hunks {
		var err error
		lines, err = applyHunk(lines, &hunk)
		if err != nil {
			return "", err
		}
	}

	return strings.Join(lines, "\n") + "\n", nil
}

// applyHunk applies a single hunk to the lines.
func applyHunk(lines []string, hunk *Hunk) ([]string, error) {
	// Find where to apply the hunk (hunk line numbers are 1-based)
	startLine := hunk.OriginalStart - 1
	if startLine < 0 {
		startLine = 0
	}

	// Verify context lines match
	lineIndex := startLine
	hunkLineIndex := 0

	// Build the new lines
	result := make([]string, 0, len(lines))

	// Copy lines before the hunk
	result = append(result, lines[:startLine]...)

	// Process hunk lines
	for hunkLineIndex < len(hunk.Lines) {
		hunkLine := hunk.Lines[hunkLineIndex]

		switch hunkLine.Type {
		case LineContext:
			// Verify context matches
			if lineIndex >= len(lines) {
				return nil, NewPatchError(ErrorConflict,
					fmt.Sprintf("context line extends beyond file (line %d)", lineIndex+1))
			}
			if lines[lineIndex] != hunkLine.Content {
				return nil, NewPatchError(ErrorConflict,
					fmt.Sprintf("context mismatch at line %d: expected %q, got %q",
						lineIndex+1, hunkLine.Content, lines[lineIndex]))
			}
			result = append(result, hunkLine.Content)
			lineIndex++

		case LineRemove:
			// Verify the line matches what we expect to remove
			if lineIndex >= len(lines) {
				return nil, NewPatchError(ErrorConflict,
					fmt.Sprintf("remove line extends beyond file (line %d)", lineIndex+1))
			}
			if lines[lineIndex] != hunkLine.Content {
				return nil, NewPatchError(ErrorConflict,
					fmt.Sprintf("line to remove doesn't match at line %d: expected %q, got %q",
						lineIndex+1, hunkLine.Content, lines[lineIndex]))
			}
			// Don't add to result, just skip the line
			lineIndex++

		case LineAdd:
			// Add new line
			result = append(result, hunkLine.Content)
			// Don't increment lineIndex as we're not consuming an original line
		}

		hunkLineIndex++
	}

	// Copy remaining lines after the hunk
	result = append(result, lines[lineIndex:]...)

	return result, nil
}

// generateContentFromHunks generates file content from hunks (for new files).
func generateContentFromHunks(hunks []Hunk, onlyAdded bool) string {
	var lines []string

	for _, hunk := range hunks {
		for _, line := range hunk.Lines {
			if onlyAdded && line.Type == LineAdd {
				lines = append(lines, line.Content)
			} else if !onlyAdded && line.Type != LineRemove {
				lines = append(lines, line.Content)
			}
		}
	}

	return strings.Join(lines, "\n") + "\n"
}

// atomicWrite writes content to a file atomically using temp file + rename.
func atomicWrite(fs afero.Fs, path string, content []byte) error {
	// Write to temp file
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tempFile := filepath.Join(dir, "."+base+".tmp")

	if err := afero.WriteFile(fs, tempFile, content, 0644); err != nil {
		return err
	}

	// Rename temp file to target (atomic on Unix-like systems)
	if err := fs.Rename(tempFile, path); err != nil {
		// Clean up temp file on failure
		_ = fs.Remove(tempFile) // nolint:errcheck // Best effort cleanup
		return err
	}

	return nil
}

// rollbackChanges restores the original state of files.
func rollbackChanges(fs afero.Fs, backups []BackupState) error {
	for i := len(backups) - 1; i >= 0; i-- {
		backup := backups[i]

		switch backup.Operation {
		case "add":
			// Remove the added file, or restore original if it existed
			if backup.Existed {
				// Restore original content
				if err := afero.WriteFile(fs, backup.Path, backup.Content, 0644); err != nil {
					return fmt.Errorf("failed to restore file %s: %w", backup.Path, err)
				}
			} else {
				// Remove the file we added
				if err := fs.Remove(backup.Path); err != nil {
					return fmt.Errorf("failed to remove file %s: %w", backup.Path, err)
				}
			}

		case "delete":
			// Restore the deleted file
			if err := afero.WriteFile(fs, backup.Path, backup.Content, 0644); err != nil {
				return fmt.Errorf("failed to restore deleted file %s: %w", backup.Path, err)
			}

		case "update":
			// Restore original content
			if err := afero.WriteFile(fs, backup.Path, backup.Content, 0644); err != nil {
				return fmt.Errorf("failed to restore file %s: %w", backup.Path, err)
			}

		case "move":
			// Restore original file and remove new file
			// This is tricky as we don't have the new path in backup
			// For now, just restore the original file
			if err := afero.WriteFile(fs, backup.Path, backup.Content, 0644); err != nil {
				return fmt.Errorf("failed to restore moved file %s: %w", backup.Path, err)
			}
		}
	}

	return nil
}

// validatePath validates that a path is within the root and doesn't contain path traversal.
func validatePath(root, path string, allowOutsideRoot bool) error {
	if path == "" {
		return nil
	}

	// Reject absolute paths that aren't relative to root
	if filepath.IsAbs(path) && !allowOutsideRoot {
		return NewPatchErrorWithFile(ErrorPathTraversal, path, "absolute paths are not allowed")
	}

	// Resolve the path
	fullPath := filepath.Join(root, path)
	cleanPath := filepath.Clean(fullPath)

	// Check if path escapes root
	if !allowOutsideRoot {
		cleanRoot := filepath.Clean(root)
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil {
			return NewPatchErrorWithFile(ErrorPathTraversal, path, "invalid path")
		}

		// Check if relative path goes outside root
		if strings.HasPrefix(rel, "..") {
			return NewPatchErrorWithFile(ErrorPathTraversal, path, "path is outside root directory")
		}
	}

	return nil
}

// generateSummary creates a human-readable summary of the patch results.
func generateSummary(result *ApplyResult) string {
	var parts []string

	if result.DryRun {
		parts = append(parts, "[DRY RUN]")
	}

	if len(result.Added) > 0 {
		parts = append(parts, fmt.Sprintf("Added %d file(s): %s",
			len(result.Added), strings.Join(result.Added, ", ")))
	}

	if len(result.Updated) > 0 {
		parts = append(parts, fmt.Sprintf("Updated %d file(s): %s",
			len(result.Updated), strings.Join(result.Updated, ", ")))
	}

	if len(result.Deleted) > 0 {
		parts = append(parts, fmt.Sprintf("Deleted %d file(s): %s",
			len(result.Deleted), strings.Join(result.Deleted, ", ")))
	}

	if len(result.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("Errors: %s", strings.Join(result.Errors, "; ")))
	}

	if len(parts) == 0 {
		return "No changes applied"
	}

	return strings.Join(parts, ". ")
}
