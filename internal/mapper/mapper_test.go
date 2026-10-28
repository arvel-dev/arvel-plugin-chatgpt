// SPDX-License-Identifier: Apache-2.0
package mapper

import (
	"bytes"
	"encoding/json"
	acp "github.com/coder/acp-go-sdk"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMapper_ReplaysRealParentFeedWithoutDuplicatedAgentText(t *testing.T) {
	for _, scenario := range []string{"text", "reasoning", "command", "edit-workspace", "mcp", "web", "plan-enabled"} {
		t.Run(scenario, func(t *testing.T) {
			m := New()
			var updates []acp.SessionUpdate
			raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", scenario, "transcript.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			hasDiff := false
			for _, line := range bytes.Split(raw, []byte{'\n'}) {
				if len(line) == 0 {
					continue
				}
				var entry struct {
					Message struct {
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					} `json:"message"`
				}
				if err := json.Unmarshal(line, &entry); err != nil {
					t.Fatal(err)
				}
				if entry.Message.Method == "turn/diff/updated" {
					hasDiff = true
				}
				out, err := m.Push(entry.Message.Method, entry.Message.Params)
				if err != nil {
					t.Fatal(err)
				}
				updates = append(updates, out...)
			}
			updates = append(updates, m.Finish()...)
			if len(updates) == 0 {
				t.Fatal("real feed produced no updates")
			}
			var whole strings.Builder
			for _, u := range updates {
				encoded, err := json.Marshal(u)
				if err != nil {
					t.Fatal(err)
				}
				whole.Write(encoded)
			}
			if hasDiff && !strings.Contains(whole.String(), `"arvel":{"diff":`) {
				t.Fatal("final nested diff missing")
			}
			if len(m.Finish()) != 0 {
				t.Fatal("duplicate final update")
			}
			if scenario == "command" && !strings.Contains(whole.String(), `"kind":"execute"`) {
				t.Fatal("missing execute tool")
			}
			if scenario == "edit-workspace" && !strings.Contains(whole.String(), `"type":"diff"`) {
				t.Fatal("missing real add diff")
			}
			if scenario == "plan-enabled" && !strings.Contains(whole.String(), `"sessionUpdate":"plan"`) {
				t.Fatal("missing plan")
			}
		})
	}
}

func TestMapper_TextCompletionDoesNotRepeatStreamedDelta(t *testing.T) {
	m := New()
	var all []acp.SessionUpdate
	for _, event := range []struct{ method, raw string }{
		{"item/agentMessage/delta", `{"threadId":"t","turnId":"turn","itemId":"i","delta":"hello"}`},
		{"item/completed", `{"threadId":"t","turnId":"turn","item":{"id":"i","type":"agentMessage","text":"hello"}}`},
		{"turn/diff/updated", `{"threadId":"t","turnId":"turn","diff":"patch"}`},
	} {
		out, err := m.Push(event.method, json.RawMessage(event.raw))
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, out...)
	}
	all = append(all, m.Finish()...)
	if len(all) != 1 || all[0].AgentMessageChunk == nil || all[0].AgentMessageChunk.Meta["arvel"].(map[string]any)["diff"] != "patch" {
		t.Fatal("duplicate stream or wrong diff level")
	}
}

func TestMapper_TruncationAndUnknownReasoning(t *testing.T) {
	s := strings.Repeat("ж", 9000)
	if got := limitBytes(s, 8192); len(got) > 8192 || !utf8.ValidString(got) {
		t.Fatal("invalid output truncation")
	}
	if got := limitRunes(s, 200); utf8.RuneCountInString(got) != 200 {
		t.Fatal("wrong title bound")
	}
	m := New()
	updates, err := m.Push("item/reasoning/textDelta", json.RawMessage(`{"delta":"private"}`))
	if err != nil || len(updates) != 0 || len(m.Finish()) != 0 {
		t.Fatal("unrecorded raw reasoning emitted")
	}
}
