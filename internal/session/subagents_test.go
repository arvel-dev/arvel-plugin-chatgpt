// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

type childRecording struct {
	testOutput
	metas []*protocol.UpdateMeta
}

func (o *childRecording) Update(u acp.SessionUpdate, meta *protocol.UpdateMeta) error {
	o.metas = append(o.metas, meta)
	return o.testOutput.Update(u, meta)
}

func childFixture(t *testing.T) (*Session, *sessionBackend) {
	t.Helper()
	cwd, home := configDirs(t)
	router := NewRouter()
	backend := &sessionBackend{router: router, home: home, generation: 1}
	backend.client = &sessionClient{done: make(chan struct{}), opts: router.ForGeneration(1)}
	s, err := New(testContext(t), backend, router, plugin.SessionParams{CWD: cwd, Meta: protocol.SessionMeta{Subagents: &protocol.Subagents{Enabled: true, MaxParallel: 2}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(testContext(t)) })
	return s, backend
}

func TestSubagents_EarlyChildOutputKeepsIdentityAndParentAccounting(t *testing.T) {
	s, b := childFixture(t)
	c := b.client
	c.hook = func(ctx context.Context, method string, params, result any) (bool, error) {
		switch method {
		case "thread/read":
			id := params.(schema.ThreadReadParams).ThreadID
			return true, encodeResult(map[string]any{"thread": map[string]any{"id": id, "cwd": s.config.start.CWD, "source": map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": s.snapshot.VendorID, "depth": 1}}}}}, result)
		case "turn/start":
			p := params.(schema.TurnStartParams)
			c.lifecycle("turn/started", p.ThreadID, "root-turn", "inProgress")
			for i := 1; i <= 2; i++ {
				child := fmt.Sprintf("child-%d", i)
				c.lifecycle("turn/started", child, "child-turn", "inProgress")
				c.event("item/agentMessage/delta", map[string]string{"threadId": child, "turnId": "child-turn", "itemId": "same-message", "delta": child})
				c.event("item/started", map[string]any{"threadId": p.ThreadID, "turnId": "root-turn", "item": map[string]string{"id": "spawn-" + child, "type": "subAgentActivity", "agentThreadId": child, "agentPath": child, "kind": "started"}})
				c.event("item/started", map[string]any{"threadId": child, "turnId": "child-turn", "item": map[string]string{"id": "same-tool", "type": "commandExecution", "command": "echo owned", "cwd": s.config.start.CWD, "status": "inProgress"}})
				c.event("thread/tokenUsage/updated", map[string]any{"threadId": child, "turnId": "child-turn", "tokenUsage": map[string]any{"last": map[string]int{"inputTokens": 999, "totalTokens": 999}}})
				c.lifecycle("turn/completed", child, "child-turn", "completed")
			}
			c.lifecycle("turn/completed", p.ThreadID, "root-turn", "completed")
			return true, encodeResult(map[string]any{"turn": map[string]string{"id": "root-turn", "status": "inProgress"}}, result)
		}
		return false, nil
	}
	out := &childRecording{}
	result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("delegate")}, out)
	if err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Usage != nil || s.Snapshot().LastTurn != "root-turn" || s.Snapshot().Turns != 1 {
		t.Fatal("child overwrote parent accounting")
	}
	messages, tools := map[string]bool{}, map[string]bool{}
	for i, u := range out.updates {
		if u.AgentMessageChunk != nil {
			if out.metas[i] == nil || out.metas[i].Subagent == nil {
				t.Fatal("child output has no attribution")
			}
			messages[out.metas[i].Subagent.ID] = true
		}
		if u.ToolCall != nil && strings.Contains(string(u.ToolCall.ToolCallId), "same-tool") {
			tools[string(u.ToolCall.ToolCallId)] = true
		}
	}
	if len(messages) != 2 || !tools["child:child-1:same-tool"] || !tools["child:child-2:same-tool"] {
		t.Fatalf("messages %v tools %v", messages, tools)
	}
}

func TestSubagents_ParentCompletionInterruptsActiveChild(t *testing.T) {
	s, b := childFixture(t)
	c := b.client
	interrupted := false
	c.hook = func(ctx context.Context, method string, params, result any) (bool, error) {
		switch method {
		case "thread/read":
			return true, json.Unmarshal([]byte(fmt.Sprintf(`{"thread":{"id":"child","cwd":%q,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":%q,"depth":1}}}}}`, s.config.start.CWD, s.snapshot.VendorID)), result)
		case "turn/start":
			p := params.(schema.TurnStartParams)
			c.lifecycle("turn/started", p.ThreadID, "rt", "inProgress")
			c.event("item/started", map[string]any{"threadId": p.ThreadID, "turnId": "rt", "item": map[string]string{"id": "spawn", "type": "subAgentActivity", "agentThreadId": "child", "kind": "started"}})
			c.lifecycle("turn/started", "child", "ct", "inProgress")
			c.lifecycle("turn/completed", p.ThreadID, "rt", "completed")
			return true, encodeResult(map[string]any{"turn": map[string]string{"id": "rt"}}, result)
		case "turn/interrupt":
			p := params.(schema.TurnInterruptParams)
			if p.ThreadID != "child" || p.TurnID != "ct" {
				t.Error("wrong child interrupted")
			}
			interrupted = true
		}
		return false, nil
	}
	_, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("delegate")}, &childRecording{})
	if err != nil || !interrupted {
		t.Fatalf("interrupted %v: %v", interrupted, err)
	}
}

func TestSubagents_RejectForeignSourceBeforeOutput(t *testing.T) {
	s, b := childFixture(t)
	c := b.client
	c.hook = func(ctx context.Context, method string, params, result any) (bool, error) {
		switch method {
		case "thread/read":
			return true, encodeResult(map[string]any{"thread": map[string]any{"id": "foreign", "cwd": s.config.start.CWD, "source": "cli"}}, result)
		case "turn/start":
			p := params.(schema.TurnStartParams)
			c.event("item/agentMessage/delta", map[string]string{"threadId": "foreign", "turnId": "ft", "itemId": "m", "delta": "PRIVATE_FOREIGN_OUTPUT"})
			c.event("item/started", map[string]any{"threadId": p.ThreadID, "turnId": "rt", "item": map[string]string{"id": "spawn", "type": "subAgentActivity", "agentThreadId": "foreign", "kind": "started"}})
			return true, encodeResult(map[string]any{"turn": map[string]string{"id": "rt"}}, result)
		}
		return false, nil
	}
	out := &childRecording{}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("delegate")}, out); err == nil {
		t.Fatal("foreign source accepted")
	}
	for _, u := range out.updates {
		if u.AgentMessageChunk != nil {
			t.Fatal("foreign output leaked")
		}
	}
}

func TestSubagents_ConfigurationPreservesHostControls(t *testing.T) {
	cwd, home := configDirs(t)
	catalog := protocol.ModelsResult{Default: "parent", Models: []protocol.Model{{ID: "parent", Inputs: []string{"text"}, Efforts: []protocol.Effort{"low"}}, {ID: "child", Inputs: []string{"text"}, Efforts: []protocol.Effort{"high"}}}}
	for _, limit := range []int{-1, 0, 1, 2, 16, 17} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			cfg, err := buildConfig(plugin.SessionParams{CWD: cwd, Meta: protocol.SessionMeta{Instructions: "role instructions", Task: &protocol.TaskMeta{Type: "task", ReadOnly: true}, Subagents: &protocol.Subagents{Enabled: true, Model: "child", Effort: "high", MaxParallel: limit}}}, home)
			if err != nil {
				if limit < 0 {
					return
				}
				t.Fatal(err)
			}
			err = configureSubagents(&cfg, catalog, "parent")
			if limit < 0 || limit > 16 {
				if err == nil {
					t.Fatal("invalid concurrency accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := limit
			if expected == 0 {
				expected = 1
			}
			features := cfg.start.Config["features"].(map[string]any)
			v2 := features["multi_agent_v2"].(map[string]any)
			agents := cfg.start.Config["agents"].(map[string]any)
			if cfg.children != expected || v2["max_concurrent_threads_per_session"] != expected+1 || v2["expose_spawn_agent_model_overrides"] != false || agents["default_subagent_model"] != "child" || agents["default_subagent_reasoning_effort"] != "high" {
				t.Fatal("incorrect native defaults or root-inclusive limit")
			}
			if cfg.start.Sandbox != "read-only" || cfg.start.ApprovalPolicy != "on-request" || *cfg.start.DeveloperInstructions != "role instructions" {
				t.Fatal("host controls changed")
			}
			for _, role := range []string{"default", "explorer", "worker"} {
				if agents[role].(map[string]string)["config_file"] != "" {
					t.Fatal("role can replace host instructions")
				}
			}
		})
	}
}

func TestSubagents_StaleParentActivityCannotReviveChild(t *testing.T) {
	s, b := childFixture(t)
	s.snapshot.LastTurn = "old-root-turn"
	c := b.client
	c.hook = func(ctx context.Context, method string, params, result any) (bool, error) {
		if method == "thread/read" {
			t.Error("stale child was queried")
			return true, fmt.Errorf("stale source")
		}
		if method != "turn/start" {
			return false, nil
		}
		p := params.(schema.TurnStartParams)
		c.event("item/started", map[string]any{"threadId": p.ThreadID, "turnId": "old-root-turn", "item": map[string]string{"type": "subAgentActivity", "id": "stale-spawn", "agentThreadId": "old-child", "kind": "started"}})
		c.lifecycle("turn/started", p.ThreadID, "new-root-turn", "inProgress")
		c.lifecycle("turn/completed", p.ThreadID, "new-root-turn", "completed")
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "new-root-turn"}}, result)
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("new task")}, &childRecording{}); err != nil {
		t.Fatal(err)
	}
}
