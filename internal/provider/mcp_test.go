// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func TestProvider_SDKMCPApprovalAndSecrets(t *testing.T) {
	for _, answer := range []string{"allow_once", "reject_once"} {
		t.Run(answer, func(t *testing.T) {
			var asks atomic.Int32
			h := newSDKHost(t, func(_ context.Context, raw json.RawMessage) (any, error) {
				asks.Add(1)
				var req acp.RequestPermissionRequest
				if json.Unmarshal(raw, &req) != nil || len(req.Options) != 2 || strings.Contains(string(raw), "owned-secret") {
					t.Error("unsafe permission request")
				}
				return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(answer))}, nil
			})
			if !h.mcpHTTP {
				t.Fatal("HTTP MCP capability missing")
			}
			var created acp.NewSessionResponse
			server := acp.McpServer{Http: &acp.McpServerHttpInline{Type: "http", Name: "project-tools", Url: "http://127.0.0.1:1/mcp", Headers: []acp.HttpHeader{{Name: "Authorization", Value: "Bearer owned-secret"}}}}
			err := h.peer.Call(h.ctx, protocol.MethodSessionNew, map[string]any{"cwd": h.cwd, "mcpServers": []acp.McpServer{server}}, &created)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := h.prompt(string(created.SessionId), "mcp-ask"); err != nil {
					t.Fatal(err)
				}
			}
			if asks.Load() != 2 {
				t.Fatal("MCP approval was cached or skipped")
			}
			var feed strings.Builder
			for len(h.updates) > 0 {
				feed.Write(<-h.updates)
			}
			if strings.Contains(feed.String(), "owned-secret") || strings.Contains(feed.String(), "owned-") || !strings.Contains(feed.String(), "project-tools") {
				t.Fatal("unsafe or missing MCP output")
			}
			want := `"status":"completed"`
			if answer == "reject_once" {
				want = `"status":"failed"`
			}
			if !strings.Contains(feed.String(), want) {
				t.Fatalf("MCP outcome missing: %s", feed.String())
			}
		})
	}
}

func TestProvider_SDKMCPStartupFailureOncePerSession(t *testing.T) {
	h := newSDKHost(t, nil)
	var created acp.NewSessionResponse
	server := acp.McpServer{Stdio: &acp.McpServerStdio{Name: "broken-tools", Command: "missing-fixture", Args: []string{}, Env: []acp.EnvVariable{}}}
	if err := h.peer.Call(h.ctx, protocol.MethodSessionNew, map[string]any{"cwd": h.cwd, "mcpServers": []acp.McpServer{server}}, &created); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := h.prompt(string(created.SessionId), "plain"); err != nil {
			t.Fatal(err)
		}
	}
	var feed strings.Builder
	for len(h.updates) > 0 {
		feed.Write(<-h.updates)
	}
	if strings.Count(feed.String(), "MCP server could not start") != 1 || strings.Contains(feed.String(), "owned-secret") || strings.Contains(feed.String(), "unattributed secret") {
		t.Fatalf("%s", feed.String())
	}
}

func TestProvider_SDKMCPCancellationLeavesSiblingResponsive(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := newSDKHost(t, func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	})
	defer close(release)
	var created acp.NewSessionResponse
	server := acp.McpServer{Stdio: &acp.McpServerStdio{Name: "tools", Command: "owned-tool", Args: []string{}, Env: []acp.EnvVariable{}}}
	if err := h.peer.Call(h.ctx, protocol.MethodSessionNew, map[string]any{"cwd": h.cwd, "mcpServers": []acp.McpServer{server}}, &created); err != nil {
		t.Fatal(err)
	}
	sibling := h.newSession(t, protocol.SessionMeta{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := h.prompt(string(created.SessionId), "mcp-ask")
		if err != nil || result.StopReason != acp.StopReasonCancelled {
			t.Errorf("cancel: %+v %v", result, err)
		}
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("MCP permission not delivered")
	}
	if result, err := h.prompt(sibling, "plain"); err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("sibling blocked: %+v %v", result, err)
	}
	if err := h.peer.Notify(protocol.MethodSessionCancel, map[string]any{"sessionId": created.SessionId}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-h.ctx.Done():
		t.Fatal("MCP turn did not cancel")
	}
	if result, err := h.prompt(sibling, "plain"); err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("sibling damaged: %+v %v", result, err)
	}
}

func TestProvider_SDKConnectionSecretSplitAcrossDeltas(t *testing.T) {
	h := newSDKHost(t, nil)
	id := h.newSession(t, protocol.SessionMeta{})
	if _, err := h.prompt(id, "key-output"); err != nil {
		t.Fatal(err)
	}
	var feed strings.Builder
	for len(h.updates) > 0 {
		feed.Write(<-h.updates)
	}
	if strings.Contains(feed.String(), "sk-integration-") || strings.Contains(feed.String(), "canary") {
		t.Fatal("split connection secret reached the host")
	}
}
