// SPDX-License-Identifier: Apache-2.0
package schema

import "encoding/json"

// ConfigReadParams selects effective user-only or cwd-aware configuration.
type ConfigReadParams struct {
	CWD           *string `json:"cwd,omitempty"`
	IncludeLayers bool    `json:"includeLayers"`
}

// ConfigReadResponse retains only configuration needed by isolation preflight.
// Credential-bearing unknown configuration fields are deliberately discarded.
type ConfigReadResponse struct {
	Config struct {
		MCP           map[string]json.RawMessage `json:"mcp_servers"`
		ModelProvider *string                    `json:"model_provider"`
	} `json:"config"`
	Origins map[string]struct {
		Name struct {
			Type string `json:"type"`
		} `json:"name"`
	} `json:"origins"`
}

// ConfigRequirementsResponse detects managed policy without interpreting or
// exposing its contents. G3 accepts only an explicit null requirements value.
type ConfigRequirementsResponse struct {
	Requirements json.RawMessage `json:"requirements"`
}

// ThreadUnsubscribeParams unloads only the specified in-memory thread.
type ThreadUnsubscribeParams struct {
	ThreadID string `json:"threadId"`
}

// ThreadResponse carries the persisted identity returned by start/resume.
type ThreadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
	Model string `json:"model"`
}

// Turn is the stable turn identity and terminal result subset.
type Turn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
}

// TurnResponse identifies the newly started turn.
type TurnResponse struct {
	Turn Turn `json:"turn"`
}

// TurnEvent attributes a lifecycle event to its thread.
type TurnEvent struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

// DeltaEvent attributes a recorded text/summary delta to its source item.
type DeltaEvent struct {
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	ItemID       string `json:"itemId"`
	Delta        string `json:"delta"`
	SummaryIndex int    `json:"summaryIndex,omitempty"`
}

// DiffEvent carries the latest unified turn diff.
type DiffEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Diff     string `json:"diff"`
}

// PlanEvent contains the observed plan step/status representation.
type PlanEvent struct {
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
	Plan     []PlanStep `json:"plan"`
}

// PlanStep is one vendor plan entry.
type PlanStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

// Item is the stable parent-feed item subset; unknown item kinds are ignored.
type Item struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Text          string          `json:"text,omitempty"`
	Command       string          `json:"command,omitempty"`
	Status        string          `json:"status,omitempty"`
	ExitCode      *int            `json:"exitCode,omitempty"`
	Output        *string         `json:"aggregatedOutput,omitempty"`
	Changes       []FileChange    `json:"changes,omitempty"`
	Server        string          `json:"server,omitempty"`
	Tool          string          `json:"tool,omitempty"`
	Query         string          `json:"query,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	AgentThreadID string          `json:"agentThreadId,omitempty"`
	AgentPath     string          `json:"agentPath,omitempty"`
	Kind          string          `json:"kind,omitempty"`
}

// FileChange contains the observed path/diff and change kind.
type FileChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
}

// ItemEvent attributes an item to an exact turn.
type ItemEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     Item   `json:"item"`
}

// ApprovalIdentity routes a vendor approval to one active turn/item.
type ApprovalIdentity struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
}

// CommandApproval excludes experimental availableDecisions/additionalPermissions.
type CommandApproval struct {
	ApprovalIdentity
	Command *string `json:"command"`
	CWD     *string `json:"cwd"`
	Reason  *string `json:"reason"`
	Network *struct {
		Host string `json:"host"`
	} `json:"networkApprovalContext"`
}

// FileApproval provides a possible grant root, not a list of changed paths.
type FileApproval struct {
	ApprovalIdentity
	GrantRoot *string `json:"grantRoot"`
	Reason    *string `json:"reason"`
}
