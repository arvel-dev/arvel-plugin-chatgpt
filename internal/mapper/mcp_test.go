// SPDX-License-Identifier: Apache-2.0
package mapper

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/sanitize"
	acp "github.com/coder/acp-go-sdk"
)

func TestMCP_ReplayRealResultsAndDecline(t *testing.T) {
	for _, scenario := range []string{"mcp-native-stdio-linux", "mcp-native-http-linux", "mcp-native-decline-linux"} {
		t.Run(scenario, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", scenario, "transcript.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			m := New()
			m.ConfigureMCP(map[string]string{"arvel_probe": "project-tools"}, nil)
			var updates []acp.SessionUpdate
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var e struct {
					Message struct {
						Method string
						Params json.RawMessage
					}
				}
				if err := json.Unmarshal(line, &e); err != nil {
					t.Fatal(err)
				}
				out, err := m.Push(e.Message.Method, e.Message.Params)
				if err != nil {
					t.Fatal(err)
				}
				updates = append(updates, out...)
			}
			updates = append(updates, m.Finish()...)
			encoded, _ := json.Marshal(updates)
			want := "MCP_PONG"
			if strings.Contains(scenario, "decline") {
				want = `"status":"failed"`
			}
			if !strings.Contains(string(encoded), want) || !strings.Contains(string(encoded), "project-tools") {
				t.Fatalf("result missing: %s", encoded)
			}
		})
	}
}

func TestMCP_StreamingSecretsAndBoundedResult(t *testing.T) {
	m := New()
	m.ConfigureMCP(map[string]string{"native": "tools"}, sanitize.New([]string{"short-secret"}))
	var all []acp.SessionUpdate
	push := func(method string, params any) {
		t.Helper()
		raw, _ := json.Marshal(params)
		out, err := m.Push(method, raw)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, out...)
	}
	for _, text := range []string{"before short-", "secret after"} {
		push("item/agentMessage/delta", map[string]string{"threadId": "t", "turnId": "u", "itemId": "msg", "delta": text})
	}
	params := map[string]any{"threadId": "t", "turnId": "u", "item": map[string]any{"id": "tool", "type": "mcpToolCall", "server": "native", "tool": "echo", "status": "completed", "result": map[string]any{"content": []any{map[string]string{"type": "text", "text": strings.Repeat("x", 9000) + "short-secret"}}}}}
	push("item/completed", params)
	push("item/completed", params)
	all = append(all, m.Finish()...)
	raw, _ := json.Marshal(all)
	if strings.Contains(string(raw), "short-secret") || strings.Contains(string(raw), "short-") || len(raw) > 11000 {
		t.Fatal("unsafe or unbounded output")
	}
	if strings.Count(string(raw), `"sessionUpdate":"tool_call_update"`) != 1 {
		t.Fatal("duplicate completion")
	}
}

func TestMCP_StartupFailureIsAssignedAndDeduplicated(t *testing.T) {
	m := New()
	m.ConfigureMCP(map[string]string{"native": "tools"}, nil)
	var all []acp.SessionUpdate
	for _, name := range []string{"other", "native", "native"} {
		raw, _ := json.Marshal(map[string]string{"threadId": "t", "name": name, "status": "failed", "error": "do-not-echo"})
		out, err := m.Push("mcpServer/startupStatus/updated", raw)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, out...)
	}
	all = append(all, m.Finish()...)
	raw, _ := json.Marshal(all)
	if len(all) != 1 || strings.Contains(string(raw), "do-not-echo") || !strings.Contains(string(raw), `"status":"failed"`) {
		t.Fatalf("%s", raw)
	}
}
