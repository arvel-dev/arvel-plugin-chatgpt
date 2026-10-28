// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
)

func (p *peer) nativeMCP(ctx context.Context, cwd, scenario string) error {
	if _, err := disposableSkillHome(); err != nil {
		return err
	}
	marker := filepath.Join(cwd, "isolation-mcp.sentinel")
	var httpCalls, modelCalls atomic.Int32
	wanted := int32(1)
	if scenario == "mcp-native-repeated" {
		wanted = 2
	}
	if scenario == "mcp-native-session" {
		wanted = 2
		p.mcpSession = true
	}
	p.allowFixtureMCP = scenario != "mcp-native-decline" && scenario != "mcp-native-deny" && scenario != "mcp-native-empty"
	httpMCP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			http.Error(w, "fixture request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var out bytes.Buffer
		if err := ServeMCP(ctx, bytes.NewReader(body), &out, func() error { httpCalls.Add(1); return nil }); err != nil {
			http.Error(w, "fixture request failed", http.StatusBadRequest)
			return
		}
		if out.Len() == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out.Bytes())
	}))
	defer httpMCP.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "fixture request too large", http.StatusRequestEntityTooLarge)
			return
		}
		n := modelCalls.Add(1)
		var item any = map[string]any{"type": "message", "role": "assistant", "id": fmt.Sprintf("mcp-message-%d", n), "content": []any{map[string]any{"type": "output_text", "text": "MCP_POLICY_DONE"}}}
		if n <= wanted {
			call := map[string]any{"type": "function_call", "id": fmt.Sprintf("mcp-function-%d", n), "call_id": fmt.Sprintf("mcp-call-%d", n), "namespace": "mcp__arvel_probe", "name": "probe_echo", "arguments": "{}"}
			var request struct {
				Tools []struct {
					Type, Name string
					Tools      []struct{ Name string }
				}
			}
			if json.Unmarshal(body, &request) == nil {
				for _, tool := range request.Tools {
					if tool.Name == "mcp__arvel_probe" {
						call["namespace"] = tool.Name
						call["name"] = "probe_echo"
					}
				}
			}
			item = call
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("mcp-response-%d", n)}},
			map[string]any{"type": "response.output_item.done", "item": item},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("mcp-response-%d", n), "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
		} {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
	}))
	defer backend.Close()
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	server := map[string]any{"command": binary, "args": []string{"mcp-fixture", marker}, "default_tools_approval_mode": "prompt", "startup_timeout_sec": 5}
	if scenario == "mcp-native-startup-failure" {
		server["command"] = filepath.Join(cwd, "missing-owned-mcp-fixture")
	}
	if scenario == "mcp-native-http" {
		server = map[string]any{"url": httpMCP.URL, "http_headers": map[string]string{"X-Fixture": "owned"}, "default_tools_approval_mode": "prompt", "startup_timeout_sec": 5}
	}
	if scenario == "mcp-native-allow" {
		server["default_tools_approval_mode"] = "approve"
	}
	if scenario == "mcp-native-override" {
		server["tools"] = map[string]any{"probe_echo": map[string]any{"approval_mode": "approve"}}
		server["enabled_tools"] = []string{"probe_echo"}
	}
	if scenario == "mcp-native-empty" {
		server["enabled_tools"] = []string{}
	}
	if scenario == "mcp-native-deny" {
		server["disabled_tools"] = []string{"probe_echo"}
	}
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]any{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": false},
		"agents":   map[string]any{"enabled": false}, "notify": []any{}, "web_search": "disabled",
		"sandbox_workspace_write": map[string]any{"network_access": false},
		"mcp_servers":             map[string]any{"arvel_probe": server},
		"model_providers":         map[string]any{"arvel_fixture": map[string]any{"name": "Arvel MCP policy fixture", "base_url": backend.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	raw, err := p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Thread.ID == "" {
		return errors.New("MCP fixture thread missing")
	}
	if err := p.fixtureInputTurn(ctx, result.Thread.ID, []any{map[string]any{"type": "text", "text": "Use the assigned harmless MCP fixture tool only."}}); err != nil {
		return err
	}
	_, markerErr := os.Lstat(marker)
	if scenario == "mcp-native-decline" || scenario == "mcp-native-deny" || scenario == "mcp-native-empty" || scenario == "mcp-native-startup-failure" {
		if !errors.Is(markerErr, os.ErrNotExist) || httpCalls.Load() != 0 {
			return errors.New("refused MCP fixture tool executed")
		}
		wantApprovals := 1
		if scenario == "mcp-native-deny" || scenario == "mcp-native-empty" || scenario == "mcp-native-startup-failure" {
			wantApprovals = 0
		}
		if p.mcpApprovals != wantApprovals {
			return errors.New("refused MCP fixture approval count mismatch")
		}
	} else {
		if scenario == "mcp-native-http" {
			if httpCalls.Load() != wanted {
				return errors.New("HTTP MCP fixture tool call count mismatch")
			}
		} else if markerErr != nil {
			return errors.New("stdio MCP fixture tool did not execute")
		}
		wantApprovals := 1
		if scenario == "mcp-native-repeated" {
			wantApprovals = 2
		}
		if scenario == "mcp-native-allow" || scenario == "mcp-native-override" {
			wantApprovals = 0
		}
		if p.mcpApprovals != wantApprovals {
			return errors.New("native MCP approval/session scope was not observed")
		}
	}
	if scenario == "mcp-native-startup-failure" {
		observed := false
		for _, entry := range *p.entries {
			var notification struct {
				Method string                                  `json:"method"`
				Params struct{ ThreadID, Name, Status string } `json:"params"`
			}
			if json.Unmarshal(entry.Message, &notification) == nil && notification.Method == "mcpServer/startupStatus/updated" && notification.Params.ThreadID != "" && notification.Params.Name == "arvel_probe" && notification.Params.Status == "failed" {
				observed = true
			}
		}
		if !observed {
			return errors.New("native MCP startup failure was not observed")
		}
	}
	if modelCalls.Load() != wanted+1 {
		return errors.New("MCP fixture turn did not complete all tool rounds")
	}
	return nil
}
