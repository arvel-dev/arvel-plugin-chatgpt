// SPDX-License-Identifier: Apache-2.0

package schema

import "encoding/json"

// ThreadReadParams requests child source metadata without replaying history.
type ThreadReadParams struct {
	ThreadID     string `json:"threadId"`
	IncludeTurns bool   `json:"includeTurns"`
}

// ThreadReadResponse retains only source identity and the effective directory.
type ThreadReadResponse struct {
	Thread struct {
		ID     string          `json:"id"`
		CWD    string          `json:"cwd"`
		Source json.RawMessage `json:"source"`
	} `json:"thread"`
}

// SpawnSource is the stable subAgent/thread_spawn source variant.
type SpawnSource struct {
	SubAgent struct {
		Spawn struct {
			Parent string `json:"parent_thread_id"`
			Depth  int    `json:"depth"`
		} `json:"thread_spawn"`
	} `json:"subAgent"`
}

// WindowsReadiness is the stable parameterless readiness response.
type WindowsReadiness struct {
	Status string `json:"status"`
}

// SkillsListParams requests a fresh metadata catalog for explicit workspaces.
type SkillsListParams struct {
	CWDs        []string `json:"cwds"`
	ForceReload bool     `json:"forceReload"`
}

// SkillsListResponse discards external skill descriptions and parser messages.
type SkillsListResponse struct {
	Data []struct {
		CWD    string `json:"cwd"`
		Skills []struct {
			Name    string `json:"name"`
			Path    string `json:"path"`
			Enabled bool   `json:"enabled"`
		} `json:"skills"`
	} `json:"data"`
}

// MCPApproval is the stable native form request; it contains no tool/item ID.
type MCPApproval struct {
	ThreadID string  `json:"threadId"`
	TurnID   *string `json:"turnId"`
	Server   string  `json:"serverName"`
	Mode     string  `json:"mode"`
	Message  string  `json:"message"`
	Meta     struct {
		Kind string `json:"codex_approval_kind"`
	} `json:"_meta"`
	Schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"requestedSchema"`
}

// MCPStartup reports the observed thread-scoped startup lifecycle.
type MCPStartup struct {
	ThreadID *string `json:"threadId"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
}

// MCPResult retains only stable text output; no arbitrary structured data is echoed.
type MCPResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}
