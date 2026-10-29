// SPDX-License-Identifier: Apache-2.0

// Package fakecodex supplies an offline executable fixture, never a production backend.
package fakecodex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Serve runs a deterministic, offline Codex-shaped fixture. It never reads credentials.
func Serve(ctx context.Context, input io.ReadCloser, output io.Writer) error {
	stopped := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(stopped); _ = input.Close() })
	defer func() {
		if !stop() {
			<-stopped
		}
	}()
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("synthetic protocol assertion 91")
		}
	}
	account := ""
	loginCount := 0
	catalog := []byte(`{"data":[{"id":"synthetic","model":"gpt-6.1-sol","displayName":"Synthetic model","description":"Offline conformance fixture","isDefault":true,"hidden":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low","description":"Low"},{"reasoningEffort":"high","description":"High"}],"inputModalities":["text","image"]},{"id":"synthetic-other","model":"synthetic-other","displayName":"Other synthetic model","description":"Offline conformance fixture","isDefault":false,"hidden":false,"defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low","description":"Low"},{"reasoningEffort":"high","description":"High"}],"inputModalities":["text","image"]}],"nextCursor":null}`)
	overlap := map[string]string{}
	overlappingDone := false
	enc := json.NewEncoder(output)
	threadCount, turnCount := 0, 0
	quotaPercent := int64(38)
	threadCWD := map[string]string{}
	childParent := map[string]string{}
	threadMCP := map[string]map[string]any{}
	type pendingApproval struct {
		thread, turn string
		mcpServer    string
		answered     bool
	}
	pending := map[string]*pendingApproval{}
	event := func(method string, params any) { _ = enc.Encode(map[string]any{"method": method, "params": params}) }
	complete := func(thread, turn, status string) {
		event("turn/completed", map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": status}})
	}
	scan := bufio.NewScanner(input)
	scan.Buffer(make([]byte, 4096), 8<<20)
	reply := func(id json.RawMessage, v any) { _ = enc.Encode(map[string]any{"id": id, "result": v}) }
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result struct {
				Decision string         `json:"decision"`
				Action   string         `json:"action"`
				Content  map[string]any `json:"content"`
			} `json:"result"`
			Params struct {
				Type     string         `json:"type"`
				APIKey   string         `json:"apiKey"`
				ThreadID string         `json:"threadId"`
				TurnID   string         `json:"turnId"`
				CWD      string         `json:"cwd"`
				CWDs     []string       `json:"cwds"`
				Model    *string        `json:"model"`
				Effort   *string        `json:"effort"`
				Config   map[string]any `json:"config"`
				Input    []struct {
					Text string `json:"text"`
					Type string `json:"type"`
					URL  string `json:"url"`
					Name string `json:"name"`
					Path string `json:"path"`
				} `json:"input"`
			} `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &m) != nil {
			return fmt.Errorf("synthetic protocol assertion 93")
		}
		switch m.Method {
		case "":
			var id string
			if json.Unmarshal(m.ID, &id) != nil || pending[id] == nil {
				return fmt.Errorf("synthetic protocol assertion 97")
			}
			p := pending[id]
			if p.answered {
				return fmt.Errorf("synthetic protocol assertion 98")
			}
			p.answered = true
			if p.mcpServer != "" {
				if m.Result.Action == "cancel" {
					continue
				}
				item := map[string]any{"id": "mcp-" + p.turn, "type": "mcpToolCall", "server": p.mcpServer, "tool": "echo", "status": "failed", "result": nil}
				if m.Result.Action == "accept" {
					if m.Result.Content == nil || len(m.Result.Content) != 0 {
						return fmt.Errorf("synthetic protocol assertion 102")
					}
					item["status"] = "completed"
					item["result"] = map[string]any{"content": []any{map[string]string{"type": "text", "text": "MCP result owned-secret"}}}
				}
				event("item/completed", map[string]any{"threadId": p.thread, "turnId": p.turn, "item": item})
				for _, delta := range []string{"tool says owned-", "secret"} {
					event("item/agentMessage/delta", map[string]string{"threadId": p.thread, "turnId": p.turn, "itemId": "message", "delta": delta})
				}
				complete(p.thread, p.turn, "completed")
				continue
			}
			if m.Result.Decision == "cancel" {
				continue
			}
			event("item/agentMessage/delta", map[string]string{"threadId": p.thread, "turnId": p.turn, "itemId": "message", "delta": m.Result.Decision})
			complete(p.thread, p.turn, "completed")
		case "initialize":
			reply(m.ID, map[string]any{"userAgent": "synthetic Codex", "codexHome": "/synthetic", "platformFamily": "test", "platformOs": "test"})
		case "initialized":
		case "account/read":
			var a any
			if account == "chatgpt" {
				a = map[string]any{"type": "chatgpt", "email": "private@example.invalid", "planType": "plus"}
			} else if account == "apiKey" {
				a = map[string]any{"type": "apiKey"}
			}
			reply(m.ID, map[string]any{"account": a, "requiresOpenaiAuth": true})
		case "account/rateLimits/read":
			reply(m.ID, map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": quotaPercent, "resetsAt": time.Now().Add(time.Hour).Unix(), "windowDurationMins": 300}, "secondary": nil, "rateLimitReachedType": nil}})
		case "account/login/start":
			if m.Params.Type != "apiKey" && m.Params.Type != "chatgpt" && m.Params.Type != "chatgptDeviceCode" {
				return fmt.Errorf("unsupported synthetic login")
			}
			if m.Params.Type == "apiKey" {
				account = "apiKey"
				reply(m.ID, map[string]any{"type": "apiKey"})
				continue
			}
			loginCount++
			loginID := fmt.Sprintf("attempt-%d", loginCount)
			success := true
			if success {
				account = "chatgpt"
			}
			// A notification before the login response exercises the real transport race.
			_ = enc.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": loginID, "success": success, "error": nil}})
			if m.Params.Type == "chatgptDeviceCode" {
				reply(m.ID, map[string]any{"type": m.Params.Type, "loginId": loginID, "verificationUrl": "https://auth.openai.com/device", "userCode": "synthetic-device-code"})
			} else {
				reply(m.ID, map[string]any{"type": "chatgpt", "loginId": loginID, "authUrl": "https://auth.openai.com/synthetic-login"})
			}
		case "account/login/cancel":
			reply(m.ID, map[string]any{"status": "canceled"})
		case "account/logout":
			account = ""
			reply(m.ID, map[string]any{})
		case "model/list":
			reply(m.ID, json.RawMessage(catalog))
		case "configRequirements/read":
			reply(m.ID, map[string]any{"requirements": nil})
		case "windowsSandbox/readiness":
			reply(m.ID, map[string]string{"status": "ready"})
		case "skills/list":
			data := []any{}
			for _, cwd := range m.Params.CWDs {
				discovered := []any{}
				root := filepath.Join(cwd, ".agents", "skills")
				entries, err := os.ReadDir(root)
				if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("synthetic protocol assertion 105")
				}
				for _, entry := range entries {
					if !entry.IsDir() {
						continue
					}
					path := filepath.Join(root, entry.Name(), "SKILL.md")
					body, err := os.ReadFile(path)
					if err != nil {
						continue
					}
					if !strings.Contains(strings.ReplaceAll(string(body), "\r\n", "\n"), "name: "+entry.Name()+"\n") {
						return fmt.Errorf("synthetic protocol assertion 106")
					}
					discovered = append(discovered, map[string]any{"name": entry.Name(), "path": path, "enabled": true})
				}
				data = append(data, map[string]any{"cwd": cwd, "skills": discovered, "errors": []any{}})
			}
			reply(m.ID, map[string]any{"data": data})
		case "config/read":
			reply(m.ID, map[string]any{"config": map[string]any{}, "origins": map[string]any{}})
		case "thread/start", "thread/resume":
			features, ok := m.Params.Config["features"].(map[string]any)
			if os.Getenv("CODEX_HOME") == "" || m.Params.Config["notify"] == nil || !ok || features["plugins"] != false {
				return fmt.Errorf("synthetic protocol assertion 99")
			}
			id := m.Params.ThreadID
			if m.Method == "thread/start" {
				threadCount++
				id = fmt.Sprintf("thread-%d", threadCount)
				event("thread/started", map[string]any{"thread": map[string]string{"id": id}})
			}
			threadCWD[id] = m.Params.CWD
			if servers, ok := m.Params.Config["mcp_servers"].(map[string]any); ok {
				threadMCP[id] = servers
				for name, raw := range servers {
					entry, ok := raw.(map[string]any)
					if !ok {
						return fmt.Errorf("invalid synthetic MCP configuration")
					}
					if entry["default_tools_approval_mode"] == nil {
						return fmt.Errorf("synthetic protocol assertion 103")
					}
					if entry["command"] == "missing-fixture" {
						event("mcpServer/startupStatus/updated", map[string]any{"threadId": nil, "name": name, "status": "failed", "error": "unattributed secret"})
						for range 2 {
							event("mcpServer/startupStatus/updated", map[string]string{"threadId": id, "name": name, "status": "failed", "error": "owned-secret"})
						}
					}
				}
			}
			reply(m.ID, map[string]any{"thread": map[string]string{"id": id}, "model": "gpt-6.1-sol"})
		case "thread/read":
			id := m.Params.ThreadID
			reply(m.ID, map[string]any{"thread": map[string]any{"id": id, "cwd": threadCWD[id], "source": map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": childParent[id], "depth": 1}}}}})
		case "thread/unsubscribe":
			reply(m.ID, map[string]string{"status": "unsubscribed"})
		case "turn/start":
			turnCount++
			turn := fmt.Sprintf("turn-%d-%d", os.Getpid(), turnCount)
			thread := m.Params.ThreadID
			text := ""
			for _, input := range m.Params.Input {
				text += input.Text
			}
			if strings.HasPrefix(text, "conformance:") {
				event("turn/started", map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "inProgress"}})
				reply(m.ID, map[string]any{"turn": map[string]string{"id": turn, "status": "inProgress"}})
				message := func(value string) {
					event("item/agentMessage/delta", map[string]string{"threadId": thread, "turnId": turn, "itemId": "message", "delta": value})
				}
				switch text {
				case "conformance:hold":
					message(strings.Repeat("synthetic active turn ", 16))
					message(strings.Repeat("synthetic active turn ", 16))
				case "conformance:overlap":
					if overlappingDone {
						message("synthetic subsequent turn")
						complete(thread, turn, "completed")
						continue
					}
					if _, ok := overlap[thread]; ok {
						return fmt.Errorf("overlap needs distinct threads")
					}
					overlap[thread] = turn
					message("synthetic overlap waiting")
					if len(overlap) == 2 {
						for id, t := range overlap {
							event("item/agentMessage/delta", map[string]string{"threadId": id, "turnId": t, "itemId": "message", "delta": "synthetic overlap confirmed"})
							complete(id, t, "completed")
						}
						clear(overlap)
						overlappingDone = true
					}
				case "conformance:proxy":
					for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY", "SSH_AUTH_SOCK"} {
						event("item/completed", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "agentMessage", "id": "env-" + key, "text": key + "=" + os.Getenv(key) + "\n"}})
					}
					complete(thread, turn, "completed")
				default:
					return fmt.Errorf("unknown conformance prompt")
				}
				continue
			}
			if strings.Contains(text, "expect-image") {
				images := 0
				for _, input := range m.Params.Input {
					if input.Type == "image" && strings.HasPrefix(input.URL, "data:image/png;base64,") {
						images++
					}
				}
				if images != 1 {
					return fmt.Errorf("synthetic protocol assertion 101")
				}
			}
			if strings.Contains(text, "expect-skill") {
				found := 0
				for _, input := range m.Params.Input {
					if input.Type != "skill" {
						continue
					}
					body, err := os.ReadFile(input.Path)
					if err != nil || !strings.Contains(string(body), "OWNED_SKILL_BODY") || input.Name == "" {
						return fmt.Errorf("synthetic protocol assertion 107")
					}
					metadata, err := os.ReadFile(filepath.Join(filepath.Dir(input.Path), "agents", "openai.yaml"))
					if err != nil || !strings.Contains(string(metadata), "allow_implicit_invocation: false") {
						return fmt.Errorf("synthetic protocol assertion 108")
					}
					found++
				}
				if found != 1 {
					return fmt.Errorf("synthetic protocol assertion 109")
				}
			}
			if strings.Contains(text, "expect other") && (m.Params.Model == nil || *m.Params.Model != "synthetic-other") {
				return fmt.Errorf("synthetic model option not applied")
			}
			if strings.Contains(text, "expect high") && (m.Params.Effort == nil || *m.Params.Effort != "high") {
				return fmt.Errorf("synthetic protocol assertion 100")
			}
			if strings.Contains(text, "crash") {
				return fmt.Errorf("synthetic protocol assertion 85")
			}
			event("turn/started", map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": "inProgress"}})
			if strings.Contains(text, "key-output") {
				for _, delta := range []string{"sk-integration-", "canary"} {
					event("item/agentMessage/delta", map[string]string{"threadId": thread, "turnId": turn, "itemId": "m", "delta": delta})
				}
				complete(thread, turn, "completed")
				reply(m.ID, map[string]any{"turn": map[string]string{"id": turn}})
				continue
			}
			if strings.Contains(text, "child-agent") {
				child := "child-" + turn
				childParent[child] = thread
				threadCWD[child] = threadCWD[thread]
				event("turn/started", map[string]any{"threadId": child, "turn": map[string]string{"id": "ct-" + turn, "status": "inProgress"}})
				event("item/agentMessage/delta", map[string]string{"threadId": child, "turnId": "ct-" + turn, "itemId": "cm", "delta": "early child output"})
				event("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "subAgentActivity", "id": "spawn-" + turn, "agentThreadId": child, "agentPath": "owned-child", "kind": "started"}})
				if strings.Contains(text, "approve") {
					id := "approval-ct-" + turn
					pending[id] = &pendingApproval{thread: child, turn: "ct-" + turn}
					_ = enc.Encode(map[string]any{"id": id, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": child, "turnId": "ct-" + turn, "itemId": "child-tool", "command": "synthetic-child-tool", "cwd": threadCWD[thread]}})
				} else {
					complete(child, "ct-"+turn, "completed")
				}
				if !strings.Contains(text, "wait") {
					complete(thread, turn, "completed")
				}
				reply(m.ID, map[string]any{"turn": map[string]string{"id": turn, "status": "inProgress"}})
				continue
			}
			if strings.Contains(text, "mcp-ask") {
				servers := threadMCP[thread]
				if len(servers) != 1 {
					return fmt.Errorf("synthetic protocol assertion 104")
				}
				for name := range servers {
					event("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]string{"type": "mcpToolCall", "id": "mcp-" + turn, "server": name, "tool": "echo", "status": "inProgress"}})
					id := "approval-" + turn
					pending[id] = &pendingApproval{thread: thread, turn: turn, mcpServer: name}
					_ = enc.Encode(map[string]any{"id": id, "method": "mcpServer/elicitation/request", "params": map[string]any{"threadId": thread, "turnId": turn, "serverName": name, "mode": "form", "message": "Permit owned-secret?", "_meta": map[string]string{"codex_approval_kind": "mcp_tool_call"}, "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}}})
				}
			} else if strings.Contains(text, "usage") {
				event("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": turn, "tokenUsage": map[string]any{"last": map[string]int64{"inputTokens": 20, "outputTokens": 5, "cachedInputTokens": 10, "reasoningOutputTokens": 2, "totalTokens": 25}, "total": map[string]int64{"inputTokens": 200, "outputTokens": 50, "cachedInputTokens": 100, "reasoningOutputTokens": 20, "totalTokens": 250}}})
			}
			if strings.Contains(text, "quota") {
				quotaPercent = 100
				event("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 100}, "secondary": nil}})
				event("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "failed", "error": map[string]any{"message": "secret-vendor-canary", "codexErrorInfo": "usageLimitExceeded"}}})
			} else if strings.Contains(text, "transient") {
				event("error", map[string]any{"threadId": thread, "turnId": turn, "willRetry": true, "error": map[string]any{"message": "secret-vendor-canary", "codexErrorInfo": "rateLimitExceeded"}})
				complete(thread, turn, "completed")
			} else if strings.Contains(text, "model-error") {
				event("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "failed", "error": map[string]any{"message": `{"error":{"code":"model_not_found"}}`, "codexErrorInfo": "other"}}})
			} else if strings.Contains(text, "push-warning") {
				quotaPercent = 85
				event("account/rateLimits/updated", map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 85}}})
				complete(thread, turn, "completed")
			} else if strings.Contains(text, "approve") {
				method := "item/commandExecution/requestApproval"
				params := map[string]any{"threadId": thread, "turnId": turn, "itemId": "tool-" + turn, "command": "synthetic-tool", "cwd": threadCWD[thread]}
				if strings.Contains(text, "file") {
					method = "item/fileChange/requestApproval"
					event("item/started", map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"id": "tool-" + turn, "type": "fileChange", "status": "inProgress", "changes": []any{map[string]any{"path": filepath.Join(threadCWD[thread], "new-file"), "kind": map[string]string{"type": "add"}, "diff": "synthetic"}}}})
				}
				id := "approval-" + turn
				pending[id] = &pendingApproval{thread: thread, turn: turn}
				_ = enc.Encode(map[string]any{"id": id, "method": method, "params": params})
			} else if !strings.Contains(text, "wait") && !strings.Contains(text, "mcp-ask") {
				event("item/agentMessage/delta", map[string]string{"threadId": thread, "turnId": turn, "itemId": "message", "delta": "synthetic response"})
				event("turn/diff/updated", map[string]string{"threadId": thread, "turnId": turn, "diff": "synthetic diff"})
				complete(thread, turn, "completed")
			}
			reply(m.ID, map[string]any{"turn": map[string]string{"id": turn, "status": "inProgress"}})
		case "turn/interrupt":
			for _, p := range pending {
				if p.thread == m.Params.ThreadID && p.turn == m.Params.TurnID && !p.answered {
					return fmt.Errorf("synthetic protocol assertion 98")
				}
			}
			complete(m.Params.ThreadID, m.Params.TurnID, "interrupted")
			reply(m.ID, map[string]any{})
		default:
			return fmt.Errorf("synthetic protocol assertion 94")
		}
	}
	return scan.Err()
}
