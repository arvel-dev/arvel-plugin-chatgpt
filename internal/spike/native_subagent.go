// SPDX-License-Identifier: Apache-2.0
package spike

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
)

func (p *peer) nativeSubagent(ctx context.Context, cwd, scenario string) error {
	if _, err := disposableSkillHome(); err != nil {
		return err
	}
	var parentCalls, childCalls atomic.Int32
	var childModel, childInstructions atomic.Bool
	releaseChild := make(chan struct{})
	var release sync.Once
	const instructions = "ARVEL_PARENT_INSTRUCTIONS. Reply to the user in Russian."
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "bounded fixture request required", http.StatusRequestEntityTooLarge)
			return
		}
		child := strings.Contains(string(raw), `\"agent_name\":\"/root/`)
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &request)
		var item any
		n := int32(0)
		if child {
			if scenario == "subagent-owned-capacity" {
				select {
				case <-releaseChild:
				case <-ctx.Done():
					return
				case <-r.Context().Done():
					return
				}
			}
			n = childCalls.Add(1)
			childModel.Store(request.Model == "gpt-5.6-luna")
			childInstructions.Store(strings.Contains(string(raw), instructions))
			item = map[string]any{"type": "message", "role": "assistant", "id": "owned-child-message", "content": []any{map[string]string{"type": "output_text", "text": "OWNED_CHILD_DONE"}}}
		} else {
			n = parentCalls.Add(1)
			switch n {
			case 1:
				spawn := map[string]string{"task_name": "worker", "message": "ARVEL_CHILD_TASK: return OWNED_CHILD_DONE.", "fork_turns": "none"}
				if scenario == "subagent-owned-role" {
					spawn["agent_type"] = "explorer"
				}
				if scenario == "subagent-owned-override" {
					spawn["model"] = "gpt-6.1-sol"
					spawn["reasoning_effort"] = "high"
				}
				args, _ := json.Marshal(spawn)
				item = map[string]any{"type": "function_call", "id": "owned-spawn", "call_id": "owned-spawn", "namespace": "collaboration", "name": "spawn_agent", "arguments": string(args)}
			case 2:
				if scenario == "subagent-owned-capacity" {
					args, _ := json.Marshal(map[string]string{"task_name": "worker_two", "message": "ARVEL_CHILD_TASK: return OWNED_CHILD_DONE.", "fork_turns": "none"})
					item = map[string]any{"type": "function_call", "id": "owned-spawn-two", "call_id": "owned-spawn-two", "namespace": "collaboration", "name": "spawn_agent", "arguments": string(args)}
				} else {
					item = map[string]any{"type": "function_call", "id": "owned-wait", "call_id": "owned-wait", "namespace": "collaboration", "name": "wait_agent", "arguments": "{}"}
				}
			case 3:
				if scenario == "subagent-owned-capacity" {
					release.Do(func() { close(releaseChild) })
					item = map[string]any{"type": "function_call", "id": "owned-wait", "call_id": "owned-wait", "namespace": "collaboration", "name": "wait_agent", "arguments": "{}"}
				} else {
					item = map[string]any{"type": "message", "role": "assistant", "id": "owned-parent-message", "content": []any{map[string]string{"type": "output_text", "text": "OWNED_PARENT_DONE"}}}
				}
			default:
				item = map[string]any{"type": "message", "role": "assistant", "id": "owned-parent-message", "content": []any{map[string]string{"type": "output_text", "text": "OWNED_PARENT_DONE"}}}
			}
		}
		if n > 6 {
			http.Error(w, "fixture request limit", http.StatusTooManyRequests)
			return
		}
		id := fmt.Sprintf("owned-%t-%d", child, n)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]string{"id": id}},
			map[string]any{"type": "response.output_item.done", "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
		} {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
	}))
	defer func() { release.Do(func() { close(releaseChild) }); backend.Close() }()
	children := map[string]bool{}
	p.observe = func(message wireMessage) {
		if message.Method != "item/started" && message.Method != "item/completed" {
			return
		}
		var event struct {
			Item struct {
				Type  string `json:"type"`
				Child string `json:"agentThreadId"`
			} `json:"item"`
		}
		if json.Unmarshal(message.Params, &event) == nil && event.Item.Type == "subAgentActivity" && event.Item.Child != "" {
			children[event.Item.Child] = true
		}
	}
	defer func() { p.observe = nil }()
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]string{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": map[string]any{"enabled": true, "max_concurrent_threads_per_session": 2, "wait_agent_enabled": true, "expose_spawn_agent_model_overrides": false}},
		"agents":   map[string]any{"enabled": true, "default_subagent_model": "gpt-5.6-luna", "default_subagent_reasoning_effort": "low"},
		"notify":   []any{}, "web_search": "disabled", "sandbox_workspace_write": map[string]any{"network_access": false}, "mcp_servers": map[string]any{},
		"model_providers": map[string]any{"arvel_fixture": map[string]any{"name": "Owned subagent fixture", "base_url": backend.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	if scenario == "subagent-owned-role" {
		agents := config["agents"].(map[string]any)
		for _, role := range []string{"default", "explorer", "worker"} {
			agents[role] = map[string]string{"description": "Delegate a task while preserving the parent instructions and permissions."}
		}
	}
	raw, err := p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "developerInstructions": instructions, "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Thread.ID == "" {
		return errors.New("owned parent identity absent")
	}
	if err := p.fixtureInputTurn(ctx, response.Thread.ID, []any{map[string]string{"type": "text", "text": "ARVEL_ROOT_TASK: spawn the owned worker, wait, return OWNED_PARENT_DONE."}}); err != nil {
		return err
	}
	if len(children) != 1 || childCalls.Load() != 1 || parentCalls.Load() < 3 || !childModel.Load() || !childInstructions.Load() {
		return errors.New("owned child configuration/lifecycle observation incomplete")
	}
	for child := range children {
		raw, err := p.request(ctx, "thread/read", map[string]any{"threadId": child, "includeTurns": false})
		if err != nil {
			return err
		}
		var result struct {
			Thread struct {
				ID     string `json:"id"`
				Source struct {
					SubAgent struct {
						Spawn struct {
							Parent string `json:"parent_thread_id"`
							Depth  int    `json:"depth"`
						} `json:"thread_spawn"`
					} `json:"subAgent"`
				} `json:"source"`
			} `json:"thread"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Thread.ID != child || result.Thread.Source.SubAgent.Spawn.Parent != response.Thread.ID || result.Thread.Source.SubAgent.Spawn.Depth != 1 {
			return errors.New("owned child source attribution absent")
		}
	}
	return nil
}
