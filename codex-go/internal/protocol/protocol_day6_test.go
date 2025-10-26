package protocol

import (
    "encoding/json"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestEventToolCallApprovalNeeded_MarshalUnmarshal(t *testing.T) {
    evt := &Event{ID: "sub-1", Msg: &EventToolCallApprovalNeeded{
        CallID:          "call-1",
        ToolName:        "shell",
        Command:         []string{"sh", "-c", "echo"},
        WorkingDirectory: ".",
        Justification:   "test",
        IsRetry:         true,
        RetryReason:     "perm",
        RiskLevel:       "high",
        RiskReasons:     []string{"writes"},
        RiskMitigation:  "sandbox",
    }}

    data, err := json.Marshal(evt)
    require.NoError(t, err)

    var round Event
    require.NoError(t, json.Unmarshal(data, &round))
    got, ok := round.Msg.(*EventToolCallApprovalNeeded)
    require.True(t, ok)
    assert.Equal(t, "call-1", got.CallID)
    assert.Equal(t, "shell", got.ToolName)
    assert.Equal(t, ".", got.WorkingDirectory)
    assert.Equal(t, "high", got.RiskLevel)
    assert.True(t, got.IsRetry)
}

func TestSandboxPolicy_MarshalJSON(t *testing.T) {
    // Workspace-write should include all fields
    s := SandboxPolicy{Mode: "workspace-write", WritableRoots: []string{"/tmp"}, NetworkAccess: true, ExcludeTmpdirEnvVar: true, ExcludeSlashTmp: true}
    b, err := json.Marshal(s)
    require.NoError(t, err)
    var m map[string]interface{}
    require.NoError(t, json.Unmarshal(b, &m))
    assert.Equal(t, "workspace-write", m["mode"])
    assert.Contains(t, m, "writable_roots")
    assert.Contains(t, m, "network_access")

    // Other modes keep minimum required fields
    s = SandboxPolicy{Mode: "native"}
    b, err = json.Marshal(s)
    require.NoError(t, err)
    require.NoError(t, json.Unmarshal(b, &m))
    assert.Equal(t, "native", m["mode"])
}

