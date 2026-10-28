// SPDX-License-Identifier: Apache-2.0
package schema_test

import (
	"bytes"
	"encoding/json"
	s "github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionContracts_ReplayObservedStablePayloads(t *testing.T) {
	counts := map[string]int{}
	for _, scenario := range []string{"text", "command-approval", "edit-accept", "reasoning", "plan-enabled", "config-isolation-private"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", scenario, "transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
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
			m := entry.Message
			var target any
			switch m.Method {
			case "thread/resume":
				target = &s.ThreadResumeParams{}
			case "config/read":
				target = &s.ConfigReadParams{}
			case "turn/completed", "turn/started":
				target = &s.TurnEvent{}
			case "item/started", "item/completed":
				target = &s.ItemEvent{}
			case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded":
				target = &s.DeltaEvent{}
			case "turn/diff/updated":
				target = &s.DiffEvent{}
			case "turn/plan/updated":
				target = &s.PlanEvent{}
			case "item/commandExecution/requestApproval":
				target = &s.CommandApproval{}
			case "item/fileChange/requestApproval":
				target = &s.FileApproval{}
			default:
				continue
			}
			if err := json.Unmarshal(m.Params, target); err != nil {
				t.Fatalf("%s: %v", m.Method, err)
			}
			switch v := target.(type) {
			case *s.CommandApproval:
				if v.Command == nil || *v.Command != "/bin/zsh -lc pwd" {
					t.Fatal("did not retain supplied command string")
				}
				// Experimental extras in this real fixture must not enter authorization.
				encoded, _ := json.Marshal(v)
				if bytes.Contains(encoded, []byte("availableDecisions")) || bytes.Contains(encoded, []byte("additionalPermissions")) {
					t.Fatal("experimental field retained")
				}
			case *s.ThreadResumeParams:
				validate(t, "ThreadResumeParams.json", v)
			case *s.ConfigReadParams:
				validate(t, "ConfigReadParams.json", v)
			case *s.TurnEvent:
				if v.ThreadID == "" || v.Turn.ID == "" {
					t.Fatal("missing turn identity")
				}
			case *s.ItemEvent:
				if v.ThreadID == "" || v.Item.ID == "" {
					t.Fatal("missing item identity")
				}
			}
			counts[m.Method]++
		}
	}
	for _, method := range []string{"thread/resume", "config/read", "turn/completed", "item/completed", "turn/diff/updated", "turn/plan/updated", "item/commandExecution/requestApproval", "item/fileChange/requestApproval"} {
		if counts[method] == 0 {
			t.Fatalf("no real payload tested for %s", method)
		}
	}
}
