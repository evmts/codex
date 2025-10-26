package sandbox

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// SandboxManager orchestrates sandbox selection and application based on OS and policy.
// This mirrors the functionality of Rust's SandboxManager in codex-rs/core/src/sandboxing/mod.rs
type SandboxManager struct {
	// appliers contains registered sandbox implementations, ordered by preference
	appliers []SandboxApplier

	// logger is used for warning messages (e.g., when sandbox is unavailable)
	logger *log.Logger
}

// NewSandboxManager creates a new sandbox manager with OS-appropriate defaults.
func NewSandboxManager() *SandboxManager {
	sm := &SandboxManager{
		logger: log.New(os.Stderr, "[sandbox] ", log.LstdFlags),
	}

	// Register sandbox implementations in order of preference
	sm.registerDefaultAppliers()

	return sm
}

// registerDefaultAppliers registers the appropriate sandbox implementations for the current OS.
func (sm *SandboxManager) registerDefaultAppliers() {
	switch runtime.GOOS {
	case "darwin":
		// macOS: Use Seatbelt
		sm.appliers = []SandboxApplier{
			&seatbeltSandbox{},
		}

	case "linux":
		// Linux: Try Landlock first (kernel >= 5.13), fall back to Seccomp
		landlock := &landlockSandbox{}
		seccomp := &seccompSandbox{}

		if landlock.IsAvailable() {
			sm.appliers = []SandboxApplier{landlock, seccomp}
		} else {
			sm.appliers = []SandboxApplier{seccomp}
		}

	case "windows":
		// Windows: No sandbox implementation yet
		sm.logger.Println("WARNING: Sandbox not supported on Windows - commands run with full system access")
		sm.appliers = []SandboxApplier{}

	default:
		sm.logger.Printf("WARNING: Sandbox not supported on %s - commands run with full system access\n", runtime.GOOS)
		sm.appliers = []SandboxApplier{}
	}
}

// ApplyToCommand applies the appropriate sandbox to the given command based on the policy.
// Returns SandboxInfo describing what sandbox was applied (if any).
func (sm *SandboxManager) ApplyToCommand(cmd *exec.Cmd, policy *PolicyConfig, workspace string) (*SandboxInfo, error) {
	// If policy doesn't require sandboxing, skip it
	if !policy.ShouldSandbox() {
		return &SandboxInfo{
			Type:    SandboxTypeNone,
			Applied: false,
			Reason:  "policy allows full access",
		}, nil
	}

	// Try to apply the first available sandbox
	for _, applier := range sm.appliers {
		if !applier.IsAvailable() {
			continue
		}

		err := applier.Apply(cmd, policy, workspace)
		if err != nil {
			return nil, fmt.Errorf("failed to apply %s sandbox: %w", applier.Name(), err)
		}

		return &SandboxInfo{
			Type:    sandboxTypeFromName(applier.Name()),
			Applied: true,
			Reason:  fmt.Sprintf("using %s sandbox", applier.Name()),
		}, nil
	}

	// No sandbox available - log warning and proceed without sandboxing
	sm.logger.Printf("WARNING: No sandbox available for policy %s - command will run with full system access\n", policy.Policy)

	return &SandboxInfo{
		Type:    SandboxTypeNone,
		Applied: false,
		Reason:  "no sandbox available on this system",
	}, nil
}

// GetAvailableSandbox returns the name of the best available sandbox, or empty string if none.
func (sm *SandboxManager) GetAvailableSandbox() string {
	for _, applier := range sm.appliers {
		if applier.IsAvailable() {
			return applier.Name()
		}
	}
	return ""
}

// sandboxTypeFromName converts a sandbox name to SandboxType.
func sandboxTypeFromName(name string) SandboxType {
	switch name {
	case "seatbelt":
		return SandboxTypeSeatbelt
	case "landlock":
		return SandboxTypeLandlock
	case "seccomp":
		return SandboxTypeSeccomp
	default:
		return SandboxTypeNone
	}
}

// =============================================================================
// macOS Seatbelt Implementation
// =============================================================================

type seatbeltSandbox struct{}

func (s *seatbeltSandbox) Name() string {
	return "seatbelt"
}

func (s *seatbeltSandbox) IsAvailable() bool {
	return runtime.GOOS == "darwin"
}

func (s *seatbeltSandbox) Apply(cmd *exec.Cmd, policy *PolicyConfig, workspace string) error {
	// On macOS, we would wrap the command with sandbox-exec
	// For now, this is a placeholder that sets environment variables
	// to indicate sandboxing is enabled.

	// The actual implementation would use sandbox-exec with a custom profile
	// similar to the Rust implementation in codex-rs/core/src/seatbelt.rs

	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	cmd.Env = append(cmd.Env, "CODEX_SANDBOX=seatbelt")

	if !policy.HasFullNetworkAccess() {
		cmd.Env = append(cmd.Env, "CODEX_SANDBOX_NETWORK_DISABLED=1")
	}

	// TODO: Implement full Seatbelt profile generation and sandbox-exec wrapping
	// This requires generating a sandbox profile similar to Rust implementation

	return nil
}

// =============================================================================
// Linux Landlock Implementation
// =============================================================================

type landlockSandbox struct{}

func (l *landlockSandbox) Name() string {
	return "landlock"
}

func (l *landlockSandbox) IsAvailable() bool {
	if runtime.GOOS != "linux" {
		return false
	}

	// Check if kernel version >= 5.13 (when Landlock was introduced)
	version, err := getLinuxKernelVersion()
	if err != nil {
		return false
	}

	// Landlock was introduced in kernel 5.13
	return version.Major > 5 || (version.Major == 5 && version.Minor >= 13)
}

func (l *landlockSandbox) Apply(cmd *exec.Cmd, policy *PolicyConfig, workspace string) error {
	// Landlock implementation would use the landlock syscalls
	// This is a placeholder that sets environment variables

	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	cmd.Env = append(cmd.Env, "CODEX_SANDBOX=landlock")

	if !policy.HasFullNetworkAccess() {
		cmd.Env = append(cmd.Env, "CODEX_SANDBOX_NETWORK_DISABLED=1")
	}

	// TODO: Implement full Landlock support using landlock syscalls
	// This requires using the landlock_create_ruleset, landlock_add_rule,
	// and landlock_restrict_self syscalls similar to Rust implementation

	return nil
}

// =============================================================================
// Linux Seccomp Implementation
// =============================================================================

type seccompSandbox struct{}

func (s *seccompSandbox) Name() string {
	return "seccomp"
}

func (s *seccompSandbox) IsAvailable() bool {
	return runtime.GOOS == "linux"
}

func (s *seccompSandbox) Apply(cmd *exec.Cmd, policy *PolicyConfig, workspace string) error {
	// Seccomp implementation would use seccomp-bpf filters
	// This is a placeholder that sets environment variables

	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	cmd.Env = append(cmd.Env, "CODEX_SANDBOX=seccomp")

	if !policy.HasFullNetworkAccess() {
		cmd.Env = append(cmd.Env, "CODEX_SANDBOX_NETWORK_DISABLED=1")
	}

	// TODO: Implement full Seccomp-BPF support
	// This requires setting up seccomp filters to restrict syscalls
	// similar to the Rust implementation in codex-rs/linux-sandbox

	return nil
}

// =============================================================================
// Linux Kernel Version Detection
// =============================================================================

type kernelVersion struct {
	Major int
	Minor int
	Patch int
}

func getLinuxKernelVersion() (*kernelVersion, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("not running on Linux")
	}

	// Read /proc/version or use uname
	cmd := exec.Command("uname", "-r")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get kernel version: %w", err)
	}

	return parseKernelVersion(strings.TrimSpace(string(output)))
}

func parseKernelVersion(version string) (*kernelVersion, error) {
	// Version format is typically: 5.13.0-generic or 5.13.0-1234-generic
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid kernel version format: %s", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid major version: %w", err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid minor version: %w", err)
	}

	patch := 0
	if len(parts) >= 3 {
		// Extract numeric part before any suffix (e.g., "0" from "0-generic")
		patchStr := strings.Split(parts[2], "-")[0]
		patch, _ = strconv.Atoi(patchStr)
	}

	return &kernelVersion{
		Major: major,
		Minor: minor,
		Patch: patch,
	}, nil
}

// SetNetworkDisabled is a helper to set the network disabled environment variable
// This can be checked by commands to voluntarily disable network access
const EnvNetworkDisabled = "CODEX_SANDBOX_NETWORK_DISABLED"
const EnvSandboxType = "CODEX_SANDBOX"
