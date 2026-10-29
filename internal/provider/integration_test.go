// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/provider"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/testkit/mockhost"
	acp "github.com/coder/acp-go-sdk"
)

// TestMain doubles as a synthetic Codex executable. It never invokes a vendor/network.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, "codex-cli 0.164.0")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "app-server" {
		serveFakeCodex()
		return
	}
	os.Exit(m.Run())
}
func serveFakeCodex() {
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
		if os.Getenv(key) != "" {
			os.Exit(91)
		}
	}
	// Do not inspect anything inside CODEX_HOME, including in this fake.
	initial, _ := os.ReadFile("initial-account")
	account := string(initial)
	_, failureMode := os.Stat("login-fails")
	loginCount := 0
	catalog, err := os.ReadFile("catalog.json")
	if err != nil {
		os.Exit(92)
	}
	var expected map[string]string
	raw, err := os.ReadFile("expected-env.json")
	if err != nil || json.Unmarshal(raw, &expected) != nil {
		os.Exit(95)
	}
	for k, v := range expected {
		if os.Getenv(k) != v {
			os.Exit(96)
		}
	}
	enc := json.NewEncoder(os.Stdout)
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
	scan := bufio.NewScanner(os.Stdin)
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
			os.Exit(93)
		}
		switch m.Method {
		case "":
			var id string
			if json.Unmarshal(m.ID, &id) != nil || pending[id] == nil {
				os.Exit(97)
			}
			p := pending[id]
			if p.answered {
				os.Exit(98)
			}
			p.answered = true
			if p.mcpServer != "" {
				if m.Result.Action == "cancel" {
					continue
				}
				item := map[string]any{"id": "mcp-" + p.turn, "type": "mcpToolCall", "server": p.mcpServer, "tool": "echo", "status": "failed", "result": nil}
				if m.Result.Action == "accept" {
					if m.Result.Content == nil || len(m.Result.Content) != 0 {
						os.Exit(102)
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
			if m.Params.Type == "apiKey" {
				account = "apiKey"
				reply(m.ID, map[string]any{"type": "apiKey"})
				continue
			}
			loginCount++
			loginID := fmt.Sprintf("attempt-%d", loginCount)
			success := failureMode != nil
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
					os.Exit(105)
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
						os.Exit(106)
					}
					discovered = append(discovered, map[string]any{"name": entry.Name(), "path": path, "enabled": true})
				}
				data = append(data, map[string]any{"cwd": cwd, "skills": discovered, "errors": []any{}})
			}
			reply(m.ID, map[string]any{"data": data})
		case "config/read":
			reply(m.ID, map[string]any{"config": map[string]any{}, "origins": map[string]any{}})
		case "thread/start", "thread/resume":
			if os.Getenv("CODEX_HOME") == "" || m.Params.Config["notify"] == nil || m.Params.Config["features"].(map[string]any)["plugins"] != false {
				os.Exit(99)
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
					entry := raw.(map[string]any)
					if entry["default_tools_approval_mode"] == nil {
						os.Exit(103)
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
			if strings.Contains(text, "expect-image") {
				images := 0
				for _, input := range m.Params.Input {
					if input.Type == "image" && strings.HasPrefix(input.URL, "data:image/png;base64,") {
						images++
					}
				}
				if images != 1 {
					os.Exit(101)
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
						os.Exit(107)
					}
					metadata, err := os.ReadFile(filepath.Join(filepath.Dir(input.Path), "agents", "openai.yaml"))
					if err != nil || !strings.Contains(string(metadata), "allow_implicit_invocation: false") {
						os.Exit(108)
					}
					found++
				}
				if found != 1 {
					os.Exit(109)
				}
			}
			if strings.Contains(text, "expect high") && (m.Params.Effort == nil || *m.Params.Effort != "high") {
				os.Exit(100)
			}
			if strings.Contains(text, "crash") {
				os.Exit(85)
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
					os.Exit(104)
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
					os.Exit(98)
				}
			}
			complete(m.Params.ThreadID, m.Params.TurnID, "interrupted")
			reply(m.ID, map[string]any{})
		default:
			os.Exit(94)
		}
	}
}

func integrationMeta(t *testing.T, kind string, separate, remote bool) protocol.InitMeta {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "models", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var e struct {
			Message struct {
				Result json.RawMessage `json:"result"`
			} `json:"message"`
		}
		_ = json.Unmarshal(line, &e)
		if bytes.Contains(e.Message.Result, []byte(`"data"`)) {
			if err := os.WriteFile(filepath.Join(dir, "catalog.json"), e.Message.Result, 0600); err != nil {
				t.Fatal(err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing recorded catalog")
	}
	expected := map[string]string{"RUST_LOG": "warn", "CODEX_HOME": "", "HTTPS_PROXY": "http://127.0.0.1:4321", "HTTP_PROXY": "http://127.0.0.1:4321", "ALL_PROXY": "http://127.0.0.1:4321", "NO_PROXY": "localhost,127.0.0.1"}
	if kind == "api_key" || separate {
		expected["CODEX_HOME"] = filepath.Join(dir, "codex-home")
	}
	envJSON, _ := json.Marshal(expected)
	if err := os.WriteFile(filepath.Join(dir, "expected-env.json"), envJSON, 0600); err != nil {
		t.Fatal(err)
	}
	return protocol.InitMeta{Sandbox: &protocol.SandboxInfo{Mode: "os", Proxy: "http://127.0.0.1:4321"}, PluginAPI: protocol.PluginAPI, DataDir: dir, Platform: protocol.PlatformInfo{Remote: remote}, Connection: protocol.ConnectionInfo{Kind: kind, Config: map[string]any{"codexPath": binary, "separateLogin": separate}, Secrets: map[string]string{"apiKey": "sk-integration-canary"}}}
}

func TestProvider_FakeVendorThroughSDK(t *testing.T) {
	for _, kind := range []string{"chatgpt", "api_key"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "sk-ambient-canary")
			t.Setenv("CODEX_ACCESS_TOKEN", "ambient-token")
			t.Setenv("GH_TOKEN", "host-token")
			meta := integrationMeta(t, kind, false, false)
			if kind == "chatgpt" {
				if err := os.WriteFile(filepath.Join(meta.DataDir, "initial-account"), []byte("chatgpt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h := mockhost.InProcess(t, provider.New())
			if _, err := h.Initialize(meta); err != nil {
				t.Fatal(err)
			}
			st, err := h.Status()
			if err != nil || st.State != protocol.StateReady {
				t.Fatalf("status %+v %v", st, err)
			}
			models, err := h.Models(false)
			if err != nil || models.Default != "gpt-6.1-sol" {
				t.Fatalf("models %+v %v", models, err)
			}
			id, sessionErr := h.NewSession(protocol.SessionMeta{})
			if kind == "api_key" {
				if sessionErr != nil || id == "" {
					t.Fatalf("private API session: %v", sessionErr)
				}
				result, err := h.Prompt(id, []acp.ContentBlock{acp.TextBlock("hello")})
				if err != nil || result.StopReason != acp.StopReasonEndTurn {
					t.Fatalf("private API turn: %v", err)
				}
			} else {
				var rpc *protocol.RPCError
				if !errors.As(sessionErr, &rpc) || rpc.Code != protocol.CodeServerError {
					t.Fatalf("legacy session must be denied: %v", sessionErr)
				}
			}
			h.Shutdown()
			if err := h.CloseWithError(); err != nil {
				t.Fatalf("SDK teardown: %v", err)
			}
			for _, secret := range []string{"sk-integration-canary", "sk-ambient-canary", "ambient-token", "host-token", "private@example.invalid"} {
				if strings.Contains(h.Stdout()+h.Stderr(), secret) {
					t.Fatal("credential or identity leaked")
				}
			}
		})
	}
}

type recordingHost struct{ changes chan protocol.Status }

func (h recordingHost) StatusChanged(s protocol.Status) { h.changes <- s }
func (recordingHost) UsageUpdated(protocol.Usage)       {}
func (recordingHost) Logger() *slog.Logger              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestProvider_LoginPushesStatusThroughRealTransport(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			p := provider.New()
			h := recordingHost{make(chan protocol.Status, 16)}
			p.(plugin.HostAware).SetHost(h)
			meta := integrationMeta(t, "chatgpt", true, remote)
			// Cleanup is LIFO: stop the child before integrationMeta's TempDir
			// cleanup, because Windows cannot remove a running process's cwd.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := p.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			if _, err := p.Initialize(context.Background(), meta); err != nil {
				t.Fatal(err)
			}
			st, err := p.Status(context.Background())
			if err != nil || st.State != protocol.StateNeedsLogin {
				t.Fatalf("status %+v %v", st, err)
			}
			result, err := p.Action(context.Background(), "login")
			if err != nil || !result.AwaitCompletion {
				t.Fatalf("login %+v %v", result, err)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			for {
				select {
				case s := <-h.changes:
					if s.State == protocol.StateReady {
						return
					}
				case <-timer.C:
					t.Fatal("missing status_changed")
					return
				}
			}
		})
	}
}

func TestProvider_SDKEmitsStatusChangedAfterLogin(t *testing.T) {
	meta := integrationMeta(t, "chatgpt", true, false)
	delete(meta.Connection.Config, "separateLogin") // New connections need their own login by default.
	h := mockhost.InProcess(t, provider.New())
	if _, err := h.Initialize(meta); err != nil {
		t.Fatal(err)
	}
	st, err := h.Status()
	if err != nil || st.State != protocol.StateNeedsLogin {
		t.Fatalf("status %+v %v", st, err)
	}
	if _, err := h.Action("login"); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			for _, line := range strings.Split(h.Stdout(), "\n") {
				var frame struct {
					Method string          `json:"method"`
					Params protocol.Status `json:"params"`
				}
				if json.Unmarshal([]byte(line), &frame) == nil && frame.Method == protocol.MethodStatusChanged && frame.Params.State == protocol.StateReady {
					if frame.Params.Validate() != nil {
						t.Fatal("invalid status_changed body")
					}
					if frame.Params.Account == nil || frame.Params.Account.Label != "p***@example.invalid" {
						t.Fatalf("missing masked account identity: %+v", frame.Params.Account)
					}
					return
				}
			}
		case <-deadline.C:
			t.Fatal("SDK did not emit status_changed")
			return
		}
	}
}

func TestProvider_SDKNotifiesEveryFailedLogin(t *testing.T) {
	meta := integrationMeta(t, "chatgpt", true, false)
	if err := os.WriteFile(filepath.Join(meta.DataDir, "login-fails"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	h := mockhost.InProcess(t, provider.New())
	if _, err := h.Initialize(meta); err != nil {
		t.Fatal(err)
	}
	if st, err := h.Status(); err != nil || st.State != protocol.StateNeedsLogin {
		t.Fatalf("status %+v %v", st, err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := h.Action("login"); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(2 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		completed := false
		for !completed {
			select {
			case <-tick.C:
				count := 0
				for _, line := range strings.Split(h.Stdout(), "\n") {
					var frame struct {
						Method string          `json:"method"`
						Params protocol.Status `json:"params"`
					}
					if json.Unmarshal([]byte(line), &frame) == nil && frame.Method == protocol.MethodStatusChanged && frame.Params.Message == "ChatGPT sign-in failed or was cancelled; try again" {
						count++
					}
				}
				completed = count >= attempt
			case <-timer.C:
				tick.Stop()
				t.Fatal("failed login completion suppressed by SDK debounce")
			}
		}
		timer.Stop()
		tick.Stop()
	}
}
