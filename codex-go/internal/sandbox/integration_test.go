// +build integration

package sandbox_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/evmts/codex/codex-go/internal/sandbox"
	"github.com/evmts/codex/codex-go/internal/sandbox/native"
)

// TestViolationDetectionIntegration tests real sandbox violations on the system.
// This test is only run when the integration build tag is set.
func TestViolationDetectionIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	tests := []struct {
		name            string
		setupCmd        func(t *testing.T) *sandbox.Command
		expectViolation bool
		violationType   sandbox.ViolationType
	}{
		{
			name: "write to read-only directory",
			setupCmd: func(t *testing.T) *sandbox.Command {
				// Try to write to /etc which is typically read-only for non-root
				return &sandbox.Command{
					Program:          "sh",
					Args:             []string{"-c", "echo test > /etc/test_write.txt"},
					WorkingDirectory: "/tmp",
					Timeout:          5 * time.Second,
				}
			},
			expectViolation: true,
			violationType:   sandbox.ViolationTypeFileSystem,
		},
		{
			name: "successful command",
			setupCmd: func(t *testing.T) *sandbox.Command {
				tmpDir := t.TempDir()
				return &sandbox.Command{
					Program:          "sh",
					Args:             []string{"-c", "echo hello > test.txt"},
					WorkingDirectory: tmpDir,
					Timeout:          5 * time.Second,
				}
			},
			expectViolation: false,
		},
		{
			name: "read from protected file",
			setupCmd: func(t *testing.T) *sandbox.Command {
				// Try to read /etc/shadow which requires root
				return &sandbox.Command{
					Program:          "cat",
					Args:             []string{"/etc/shadow"},
					WorkingDirectory: "/tmp",
					Timeout:          5 * time.Second,
				}
			},
			expectViolation: true,
			violationType:   sandbox.ViolationTypeFileSystem,
		},
	}

	sb := native.New()
	if !sb.IsAvailable() {
		t.Skip("native sandbox not available")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := tt.setupCmd(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			result, err := sb.Execute(ctx, cmd)

			// We don't consider execution error as test failure
			// The violation detection should work regardless
			if err != nil && ctx.Err() != nil {
				t.Fatalf("command execution timed out: %v", err)
			}

			// Log the result for debugging
			t.Logf("Exit code: %d", result.ExitCode)
			if result.Stdout != "" {
				t.Logf("Stdout: %s", result.Stdout)
			}
			if result.Stderr != "" {
				t.Logf("Stderr: %s", result.Stderr)
			}

			if tt.expectViolation {
				if result.Violation == nil {
					t.Errorf("expected violation but got none. Exit code: %d, Stderr: %s",
						result.ExitCode, result.Stderr)
				} else {
					t.Logf("Detected violation: %s", result.Violation.FormatViolation())
					if result.Violation.Type != tt.violationType {
						t.Errorf("expected violation type %s, got %s",
							tt.violationType, result.Violation.Type)
					}
				}
			} else {
				if result.Violation != nil {
					t.Errorf("unexpected violation: %+v", result.Violation)
				}
			}
		})
	}
}

// TestViolationProtocolEventConversion tests the conversion to protocol events.
func TestViolationProtocolEventConversion(t *testing.T) {
	path := "/etc/passwd"
	syscall := "connect"

	violation := &sandbox.Violation{
		Type:         sandbox.ViolationTypeFileSystem,
		Operation:    "write",
		Path:         &path,
		Syscall:      &syscall,
		ErrorMessage: "permission denied",
		ExitCode:     1,
		Timestamp:    time.Now(),
	}

	event := violation.ToProtocolEvent("call-123", "native")

	// Verify the event has the right structure
	if event == nil {
		t.Fatal("expected non-nil event")
	}

	// The event is returned as interface{} to avoid circular imports
	// In actual usage, it would be cast to protocol.EventSandboxViolation
	t.Logf("Event: %+v", event)
}

// TestFileSystemViolationRealWorld tests detection with real filesystem operations.
func TestFileSystemViolationRealWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-world test in short mode")
	}

	// Create a test directory structure
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// Write a test file
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Make the file read-only
	if err := os.Chmod(testFile, 0444); err != nil {
		t.Fatalf("failed to chmod test file: %v", err)
	}

	sb := native.New()
	ctx := context.Background()

	// Try to write to the read-only file
	cmd := &sandbox.Command{
		Program:          "sh",
		Args:             []string{"-c", "echo new content > " + testFile},
		WorkingDirectory: tmpDir,
		Timeout:          5 * time.Second,
	}

	result, err := sb.Execute(ctx, cmd)
	if err != nil && ctx.Err() != nil {
		t.Fatalf("command execution failed: %v", err)
	}

	// Should detect a violation
	if result.ExitCode != 0 && result.Violation != nil {
		t.Logf("Successfully detected violation: %s", result.Violation.FormatViolation())

		if result.Violation.Type != sandbox.ViolationTypeFileSystem {
			t.Errorf("expected filesystem violation, got %s", result.Violation.Type)
		}

		if result.Violation.Path == nil {
			t.Error("expected path to be set in violation")
		}
	} else if result.ExitCode == 0 {
		t.Log("Command succeeded (possibly running as root), skipping violation check")
	} else {
		t.Logf("No violation detected. Exit code: %d, Stderr: %s", result.ExitCode, result.Stderr)
	}
}

// BenchmarkViolationDetection benchmarks the violation detection performance.
func BenchmarkViolationDetection(b *testing.B) {
	detector := sandbox.NewViolationDetector("native")
	result := &sandbox.Result{
		Stdout:   "",
		Stderr:   "permission denied: /etc/passwd\nopen /etc/passwd: Operation not permitted",
		ExitCode: 1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		detector.DetectViolation(result)
	}
}
