// SPDX-License-Identifier: Apache-2.0
package schema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	s "github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
)

func TestMCPContracts_ReemitRecordedApprovalAndStartup(t *testing.T) {
	approvals, failures := 0, 0
	for _, scenario := range []string{"mcp-native-stdio-linux", "mcp-native-startup-failure-linux"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", scenario, "transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(raw, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var e struct {
				Message struct {
					Method string
					Params json.RawMessage
				}
			}
			if json.Unmarshal(line, &e) != nil {
				t.Fatal("invalid fixture")
			}
			switch e.Message.Method {
			case "mcpServer/elicitation/request":
				var v s.MCPApproval
				if err := json.Unmarshal(e.Message.Params, &v); err != nil {
					t.Fatal(err)
				}
				validate(t, "McpServerElicitationRequestParams.json", v)
				approvals++
			case "mcpServer/startupStatus/updated":
				var v s.MCPStartup
				if err := json.Unmarshal(e.Message.Params, &v); err != nil {
					t.Fatal(err)
				}
				validate(t, "McpServerStatusUpdatedNotification.json", v)
				if v.Status == "failed" {
					failures++
				}
			}
		}
	}
	if approvals == 0 || failures == 0 {
		t.Fatal("recording coverage missing")
	}
}

func TestWindowsReadiness_ReemitsRealLinuxResponse(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "windows-readiness-linux", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var id string
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var e struct {
			Message struct {
				ID     json.RawMessage
				Method string
				Result json.RawMessage
			}
		}
		if json.Unmarshal(line, &e) != nil {
			t.Fatal("invalid fixture")
		}
		if e.Message.Method == "windowsSandbox/readiness" {
			id = string(e.Message.ID)
			continue
		}
		if id != "" && string(e.Message.ID) == id && len(e.Message.Result) > 0 {
			var response s.WindowsReadiness
			if err := json.Unmarshal(e.Message.Result, &response); err != nil {
				t.Fatal(err)
			}
			validate(t, "WindowsSandboxReadinessResponse.json", response)
			found = true
		}
	}
	if !found {
		t.Fatal("readiness response missing")
	}
}

func TestHostTools_DecodeRecordedCatalogAndChildSource(t *testing.T) {
	for _, tc := range []struct{ fixture, method, response string }{
		{"skills-managed-linux", "skills/list", "SkillsListResponse.json"},
		{"subagent-owned-role-linux", "thread/read", "ThreadReadResponse.json"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", tc.fixture, "transcript.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]bool{}
			found := false
			for _, line := range bytes.Split(raw, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var entry struct {
					Message struct {
						ID     json.RawMessage
						Method string
						Result json.RawMessage
					}
				}
				if json.Unmarshal(line, &entry) != nil {
					t.Fatal("invalid fixture")
				}
				m := entry.Message
				if m.Method == tc.method {
					ids[string(m.ID)] = true
					continue
				}
				if !ids[string(m.ID)] || len(m.Result) == 0 {
					continue
				}
				var full any
				if json.Unmarshal(m.Result, &full) != nil {
					t.Fatal("invalid response")
				}
				validate(t, tc.response, full)
				if tc.method == "skills/list" {
					var v s.SkillsListResponse
					if json.Unmarshal(m.Result, &v) != nil || len(v.Data) == 0 {
						t.Fatal("catalog response lost")
					}
				} else {
					var v s.ThreadReadResponse
					var source s.SpawnSource
					if json.Unmarshal(m.Result, &v) != nil || v.Thread.ID == "" || v.Thread.CWD == "" || json.Unmarshal(v.Thread.Source, &source) != nil || source.SubAgent.Spawn.Parent == "" || source.SubAgent.Spawn.Depth != 1 {
						t.Fatal("child identity lost")
					}
				}
				found = true
			}
			if !found {
				t.Fatal("response missing")
			}
		})
	}
}
