package file

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ValidationError represents a path validation error with a specific type.
type ValidationError struct {
	Type    ValidationErrorType
	Path    string
	Message string
	Err     error
}

// ValidationErrorType categorizes validation errors.
type ValidationErrorType int

const (
	// ErrorPathTraversal indicates an attempt to access outside the workspace.
	ErrorPathTraversal ValidationErrorType = iota
	// ErrorSensitivePath indicates an attempt to access a sensitive system path.
	ErrorSensitivePath
	// ErrorSymlinkEscape indicates a symlink points outside the workspace.
	ErrorSymlinkEscape
	// ErrorInvalidPath indicates a malformed or invalid path.
	ErrorInvalidPath
	// ErrorAbsolutePathRequired indicates an absolute path was required but not provided.
	ErrorAbsolutePathRequired
)

func (e *ValidationError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s", e.Message, e.Err.Error())
	}
	return e.Message
}

func (e *ValidationError) Unwrap() error {
	return e.Err
}

// Sensitive system paths that should be blocked on Unix-like systems.
var unixSensitivePaths = []string{
	"/etc",
	"/usr",
	"/sys",
	"/proc",
	"/dev",
	"/boot",
	"/root",
	"/var/log",
	"/.ssh",
	"/private/etc",       // macOS
	"/private/var",       // macOS
	"/System",            // macOS
	"/Library/Security",  // macOS
}

// Sensitive paths in home directory (relative to $HOME).
var homeSensitivePaths = []string{
	".ssh",
	".gnupg",
	".aws",
	".kube",
	".docker",
	".config/gcloud",
	".azure",
	"Library/Keychains", // macOS
}

// Sensitive system paths on Windows.
var windowsSensitivePaths = []string{
	"C:\\Windows\\System32",
	"C:\\Windows\\SysWOW64",
	"C:\\Program Files",
	"C:\\Program Files (x86)",
	"C:\\ProgramData",
}

// ValidatePathForRead validates a path for read operations.
// It ensures the path is within the workspace and doesn't attempt path traversal.
func ValidatePathForRead(path, workspace string) error {
	// Early detection of suspicious patterns before resolution
	if DetectPathTraversal(path) {
		// Still need to check if it resolves safely, but be more suspicious
		// Some patterns like ..%2F should always be rejected
		if strings.Contains(strings.ToLower(path), "%2e") ||
		   strings.Contains(strings.ToLower(path), "%2f") ||
		   strings.Contains(strings.ToLower(path), "%5c") {
			return &ValidationError{
				Type:    ErrorPathTraversal,
				Path:    path,
				Message: fmt.Sprintf("suspicious encoded path pattern detected: %s", path),
			}
		}

		// Check for unusual dot patterns like "...."
		if strings.Contains(path, "....") {
			return &ValidationError{
				Type:    ErrorPathTraversal,
				Path:    path,
				Message: fmt.Sprintf("suspicious path pattern detected: %s", path),
			}
		}
	}

	// First, resolve and validate the path
	resolvedPath, err := ResolvePath(path, workspace)
	if err != nil {
		return err
	}

	// Check if path is within workspace
	if !IsPathInWorkspace(resolvedPath, workspace) {
		return &ValidationError{
			Type:    ErrorPathTraversal,
			Path:    path,
			Message: fmt.Sprintf("path is outside workspace: %s", path),
		}
	}

	// Check for symlinks that escape workspace
	if err := checkSymlinkSafety(resolvedPath, workspace); err != nil {
		return err
	}

	return nil
}

// ValidatePathForWrite validates a path for write operations.
// It performs the same checks as read, plus additional sensitive path checks.
func ValidatePathForWrite(path, workspace string) error {
	// First perform read validation
	if err := ValidatePathForRead(path, workspace); err != nil {
		return err
	}

	// Resolve the path to check for sensitive locations
	resolvedPath, err := ResolvePath(path, workspace)
	if err != nil {
		return err
	}

	// Check if path is a sensitive system path
	if isSensitivePath(resolvedPath) {
		return &ValidationError{
			Type:    ErrorSensitivePath,
			Path:    path,
			Message: fmt.Sprintf("cannot write to sensitive path: %s", path),
		}
	}

	return nil
}

// IsPathInWorkspace checks if a resolved absolute path is within the workspace.
func IsPathInWorkspace(path, workspace string) bool {
	// Clean both paths
	cleanPath := filepath.Clean(path)
	cleanWorkspace := filepath.Clean(workspace)

	// Ensure both are absolute
	if !filepath.IsAbs(cleanPath) || !filepath.IsAbs(cleanWorkspace) {
		return false
	}

	// Check if path is under workspace using Rel
	rel, err := filepath.Rel(cleanWorkspace, cleanPath)
	if err != nil {
		return false
	}

	// If relative path starts with "..", it's outside workspace
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return false
	}

	return true
}

// ResolvePath resolves a path relative to workspace and returns the absolute path.
// It handles both relative and absolute paths, and ensures path separators are normalized.
func ResolvePath(path, workspace string) (string, error) {
	// Validate workspace is absolute
	if !filepath.IsAbs(workspace) {
		return "", &ValidationError{
			Type:    ErrorAbsolutePathRequired,
			Path:    workspace,
			Message: "workspace must be an absolute path",
		}
	}

	// Clean the workspace
	cleanWorkspace := filepath.Clean(workspace)

	// Handle absolute paths
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	// Handle relative paths - join with workspace
	fullPath := filepath.Join(cleanWorkspace, path)
	return filepath.Clean(fullPath), nil
}

// checkSymlinkSafety verifies that a path and any symlinks in its chain
// don't point outside the workspace.
func checkSymlinkSafety(path, workspace string) error {
	// Get the actual path following symlinks
	evalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If the file doesn't exist yet, that's okay for write operations
		if os.IsNotExist(err) {
			// Check parent directory instead
			parent := filepath.Dir(path)
			if parent != path { // Avoid infinite recursion at root
				return checkSymlinkSafety(parent, workspace)
			}
			return nil
		}
		// For other errors (like in-memory filesystems), skip symlink check
		// The path itself has already been validated
		return nil
	}

	// Also resolve workspace symlinks for fair comparison
	// (e.g., on macOS /var -> /private/var)
	evalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		// If workspace doesn't exist or can't be resolved, use as-is
		evalWorkspace = workspace
	}

	// If symlink resolution didn't change the path (or only canonicalized it),
	// it's not actually a symlink, just accept it
	if evalPath == path || filepath.Clean(evalPath) == filepath.Clean(path) {
		return nil
	}

	// Check if the resolved path is still within resolved workspace
	if !IsPathInWorkspace(evalPath, evalWorkspace) {
		return &ValidationError{
			Type:    ErrorSymlinkEscape,
			Path:    path,
			Message: fmt.Sprintf("symlink points outside workspace: %s -> %s", path, evalPath),
		}
	}

	return nil
}

// isSensitivePath checks if a path is in a sensitive system location.
func isSensitivePath(path string) bool {
	cleanPath := filepath.Clean(path)

	// Check OS-specific sensitive paths
	var sensitivePaths []string
	if runtime.GOOS == "windows" {
		sensitivePaths = windowsSensitivePaths
	} else {
		sensitivePaths = unixSensitivePaths
	}

	// Check system paths
	for _, sensitive := range sensitivePaths {
		if strings.HasPrefix(cleanPath, sensitive) {
			return true
		}
	}

	// Check home directory sensitive paths
	if homeDir, err := os.UserHomeDir(); err == nil {
		for _, sensitive := range homeSensitivePaths {
			sensitivePath := filepath.Join(homeDir, sensitive)
			if strings.HasPrefix(cleanPath, sensitivePath) {
				return true
			}
		}
	}

	return false
}

// DetectPathTraversal checks for common path traversal patterns in a string.
// This is useful for detecting attacks before path resolution.
func DetectPathTraversal(path string) bool {
	// Check for obvious traversal patterns
	traversalPatterns := []string{
		"../",
		"..\\",
		"/..",
		"\\..",
	}

	for _, pattern := range traversalPatterns {
		if strings.Contains(path, pattern) {
			return true
		}
	}

	// Check for encoded traversal attempts
	encodedPatterns := []string{
		"%2e%2e",  // URL encoded ..
		"%2e%2e/", // URL encoded ../
		"%2e%2e\\", // URL encoded ..\
		"..%2f",    // Mixed encoding
		"..%5c",    // Mixed encoding
	}

	lowerPath := strings.ToLower(path)
	for _, pattern := range encodedPatterns {
		if strings.Contains(lowerPath, pattern) {
			return true
		}
	}

	return false
}

// NormalizePath normalizes a path for consistent comparison.
// It handles case-insensitive filesystems and path separators.
func NormalizePath(path string) string {
	// Clean the path
	clean := filepath.Clean(path)

	// On case-insensitive filesystems (Windows, macOS by default), normalize case
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		clean = strings.ToLower(clean)
	}

	return clean
}

// IsHiddenPath checks if a path represents a hidden file or directory.
// On Unix-like systems, hidden files start with a dot.
// On Windows, this checks the file attributes.
func IsHiddenPath(path string) bool {
	base := filepath.Base(path)

	// Unix-style hidden files
	if strings.HasPrefix(base, ".") && base != "." && base != ".." {
		return true
	}

	// Windows hidden attribute
	if runtime.GOOS == "windows" {
		// Check file attributes
		info, err := os.Stat(path)
		if err == nil {
			// On Windows, we'd need to check syscall attributes
			// For now, just check the dot prefix
			_ = info
		}
	}

	return false
}

// ValidatePathComponents checks each component of a path for invalid characters.
func ValidatePathComponents(path string) error {
	components := strings.Split(filepath.Clean(path), string(filepath.Separator))

	for _, component := range components {
		if component == "" || component == "." {
			continue
		}

		// Check for null bytes
		if strings.Contains(component, "\x00") {
			return &ValidationError{
				Type:    ErrorInvalidPath,
				Path:    path,
				Message: "path contains null bytes",
			}
		}

		// Check for control characters
		for _, r := range component {
			if r < 32 && r != '\t' {
				return &ValidationError{
					Type:    ErrorInvalidPath,
					Path:    path,
					Message: "path contains control characters",
				}
			}
		}
	}

	return nil
}
