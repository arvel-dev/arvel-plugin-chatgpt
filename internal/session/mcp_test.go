// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func TestMCPConfig_AssignedServersAndNativePolicy(t *testing.T) {
	for _, level := range []protocol.PermissionLevel{"ask", "allow", "deny"} {
		t.Run(string(level), func(t *testing.T) {
			params := plugin.SessionParams{MCPServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "project tools", Command: "owned-tool", Args: []string{}, Env: []acp.EnvVariable{{Name: "TOKEN", Value: "abc123"}}}}}, Meta: protocol.SessionMeta{Permissions: &protocol.Permissions{MCP: &protocol.MCPPolicy{Default: level, Tools: map[string]protocol.PermissionLevel{"project tools/read": "allow", "project tools/write": "deny"}}}}}
			config, names, clean, err := mcpConfig(params)
			if err != nil {
				t.Fatal(err)
			}
			alias := mcpName("project tools")
			if len(config) != 1 || names[alias] != "project tools" {
				t.Fatal("assignment lost")
			}
			entry := config[alias].(map[string]any)
			want := "prompt"
			if level == "allow" {
				want = "approve"
			}
			if entry["default_tools_approval_mode"] != want {
				t.Fatal("wrong approval mode")
			}
			if level == "deny" {
				enabled := entry["enabled_tools"].([]string)
				if len(enabled) != 1 || enabled[0] != "read" {
					t.Fatal("denied tools exposed")
				}
			}
			if strings.Contains(clean.String("echo abc123"), "abc123") {
				t.Fatal("short secret exposed")
			}
		})
	}
}

func TestMCPConfig_RejectsAmbiguousOrUnsupportedTransports(t *testing.T) {
	for _, server := range []acp.McpServer{
		{}, {Sse: &acp.McpServerSseInline{}}, {Acp: &acp.McpServerAcpInline{}},
		{Stdio: &acp.McpServerStdio{Name: "s", Command: "tool"}, Http: &acp.McpServerHttpInline{Name: "s", Url: "http://localhost"}},
		{Http: &acp.McpServerHttpInline{Name: "s", Url: "https://user:secret@example.test"}},
		{Stdio: &acp.McpServerStdio{Name: "s", Command: "tool", Env: []acp.EnvVariable{{Name: "TOKEN", Value: "a"}, {Name: "token", Value: "b"}}}},
	} {
		if _, _, _, err := mcpConfig(plugin.SessionParams{MCPServers: []acp.McpServer{server}}); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
}

func TestMCPApproval_OnceOnlyAndAssignedThread(t *testing.T) {
	for _, answer := range []string{"allow_once", "reject_once", "allow_always"} {
		t.Run(answer, func(t *testing.T) {
			s, _ := sessionFixture(t)
			alias := mcpName("tools")
			s.config.mcpNames = map[string]string{alias: "tools"}
			s.config.clean = secretCleaner([]string{"xyz-secret"})
			asks := 0
			out := &testOutput{permission: func(_ context.Context, req plugin.PermissionRequest) (plugin.PermissionDecision, error) {
				asks++
				if len(req.Options) != 2 || req.Meta.Category != protocol.PermissionCategoryMCP || strings.Contains(req.Meta.Reason, "xyz-secret") {
					t.Error("unsafe host permission request")
				}
				return plugin.PermissionDecision{OptionID: answer}, nil
			}}
			raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "serverName": alias, "mode": "form", "message": "xyz-secret", "_meta": map[string]string{"codex_approval_kind": "mcp_tool_call"}, "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}})
			for range 2 {
				got, err := s.approve(testContext(t), context.Background(), "mcpServer/elicitation/request", raw, out, s.runtime, s.sub, s.ID(), func(id string) bool { return id == "turn" })
				encoded, _ := json.Marshal(got)
				want := "decline"
				if answer == "allow_once" {
					want = "accept"
				}
				if err != nil || !strings.Contains(string(encoded), `"action":"`+want+`"`) {
					t.Fatalf("%s %v", encoded, err)
				}
			}
			if asks != 2 {
				t.Fatal("unexpected persistent permission")
			}
			_, err := s.approve(testContext(t), context.Background(), "mcpServer/elicitation/request", raw, out, s.runtime, s.sub, "other-thread", func(string) bool { return true })
			if err != nil || asks != 2 {
				t.Fatal("cross-thread approval escaped")
			}
		})
	}
}

func TestMCPConfig_StartResumeAndSiblingAssignments(t *testing.T) {
	cwd, home := configDirs(t)
	var configs []threadConfig
	for _, command := range []string{"first", "second"} {
		p := plugin.SessionParams{CWD: cwd, MCPServers: []acp.McpServer{{Stdio: &acp.McpServerStdio{Name: "arvel", Command: command, Args: []string{}, Env: []acp.EnvVariable{}}}}, Meta: protocol.SessionMeta{Task: &protocol.TaskMeta{Type: "task", ReadOnly: true}, Permissions: &protocol.Permissions{MCP: &protocol.MCPPolicy{Default: "deny"}}}}
		cfg, err := buildConfig(p, home)
		if err != nil {
			t.Fatal(err)
		}
		entry := cfg.start.Config["mcp_servers"].(map[string]any)[mcpName("arvel")].(map[string]any)
		if entry["command"] != command || entry["default_tools_approval_mode"] != "approve" || cfg.start.Sandbox != "read-only" {
			t.Fatal("host assignment or sandbox lost")
		}
		start, _ := json.Marshal(cfg.start.Config)
		resume, _ := json.Marshal(cfg.resume("thread").Config)
		if string(start) != string(resume) {
			t.Fatal("resume policy drift")
		}
		configs = append(configs, cfg)
	}
	first := configs[0].start.Config["mcp_servers"].(map[string]any)[mcpName("arvel")].(map[string]any)
	if first["command"] != "first" {
		t.Fatal("sibling assignment overwritten")
	}
}

func TestMCPApproval_RejectsUnknownFormsBeforeBinding(t *testing.T) {
	for _, change := range []map[string]any{
		{"turnId": nil}, {"serverName": "unassigned"}, {"mode": "url"},
		{"_meta": map[string]string{"codex_approval_kind": "other"}},
		{"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]string{"type": "string"}}}},
	} {
		s, _ := sessionFixture(t)
		alias := mcpName("tools")
		s.config.mcpNames = map[string]string{alias: "tools"}
		req := map[string]any{"threadId": s.ID(), "turnId": "turn", "serverName": alias, "mode": "form", "_meta": map[string]string{"codex_approval_kind": "mcp_tool_call"}, "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}}
		for k, v := range change {
			req[k] = v
		}
		raw, _ := json.Marshal(req)
		out := &testOutput{permission: func(context.Context, plugin.PermissionRequest) (plugin.PermissionDecision, error) {
			t.Fatal("unknown form reached host")
			return plugin.PermissionDecision{}, nil
		}}
		got, err := s.approveMCP(testContext(t), context.Background(), raw, out, s.ID(), func(string) bool { t.Fatal("unknown form bound a turn"); return false })
		encoded, _ := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"action":"decline"`) {
			t.Fatalf("%s %v", encoded, err)
		}
	}
}

func TestMCPApproval_CancelWaitingHost(t *testing.T) {
	s, _ := sessionFixture(t)
	alias := mcpName("tools")
	s.config.mcpNames = map[string]string{alias: "tools"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan struct{})
	out := &testOutput{permission: func(ctx context.Context, _ plugin.PermissionRequest) (plugin.PermissionDecision, error) {
		close(entered)
		<-ctx.Done()
		return plugin.PermissionDecision{Cancelled: true}, nil
	}}
	raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "serverName": alias, "mode": "form", "_meta": map[string]string{"codex_approval_kind": "mcp_tool_call"}, "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}})
	go func() {
		defer close(done)
		got, err := s.approveMCP(testContext(t), ctx, raw, out, s.ID(), func(string) bool { return true })
		encoded, _ := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"action":"cancel"`) {
			t.Errorf("%s %v", encoded, err)
		}
	}()
	<-entered
	cancel()
	<-done
}
