// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/provider"
	"github.com/arvel-dev/arvel-sdk/jsonrpc"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

type sdkHost struct {
	peer    *jsonrpc.Peer
	ctx     context.Context
	updates chan json.RawMessage
	usages  chan protocol.Usage
	cwd     string
}

func newSDKHost(t *testing.T, handler func(context.Context, json.RawMessage) (any, error), options ...plugin.Option) *sdkHost {
	t.Helper()
	meta := integrationMeta(t, "chatgpt", true, false)
	delete(meta.Connection.Config, "separateLogin")
	if err := os.WriteFile(filepath.Join(meta.DataDir, "initial-account"), []byte("chatgpt"), 0600); err != nil {
		t.Fatal(err)
	}
	pluginIn, hostOut := io.Pipe()
	hostIn, pluginOut := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	callContext, stopCalls := context.WithTimeout(ctx, 20*time.Second)
	h := &sdkHost{peer: jsonrpc.NewPeer(hostIn, hostOut), ctx: callContext, updates: make(chan json.RawMessage, 256), usages: make(chan protocol.Usage, 256), cwd: t.TempDir()}
	if handler == nil {
		handler = func(context.Context, json.RawMessage) (any, error) {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("allow_once")}, nil
		}
	}
	h.peer.Handle(protocol.MethodSessionRequestPermission, handler)
	h.peer.HandleNotification(protocol.MethodSessionUpdate, func(_ context.Context, raw json.RawMessage) {
		select {
		case h.updates <- append(json.RawMessage(nil), raw...):
		case <-ctx.Done():
		}
	})
	h.peer.HandleNotification(protocol.MethodUsageUpdated, func(_ context.Context, raw json.RawMessage) {
		var u protocol.Usage
		if json.Unmarshal(raw, &u) != nil {
			t.Error("invalid usage JSON")
			return
		}
		select {
		case h.usages <- u:
		case <-ctx.Done():
		}
	})
	runDone, peerDone := make(chan error, 1), make(chan struct{})
	options = append(options, plugin.WithStdio(pluginIn, pluginOut), plugin.WithStderr(io.Discard))
	go func() {
		err := plugin.Run(ctx, provider.New(), options...)
		_ = pluginOut.Close()
		_ = pluginIn.Close()
		runDone <- err
	}()
	go func() { defer close(peerDone); _ = h.peer.Serve(ctx) }()
	t.Cleanup(func() {
		_ = h.peer.Notify(protocol.MethodShutdown, nil)
		_ = hostOut.Close()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("SDK shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("SDK shutdown did not join workers")
			cancel()
			_ = pluginIn.Close()
			_ = pluginOut.Close()
			<-runDone
		}
		cancel()
		stopCalls()
		_ = hostIn.Close()
		<-peerDone
	})
	var initialized acp.InitializeResponse
	if err := h.peer.Call(h.ctx, protocol.MethodInitialize, map[string]any{"protocolVersion": 1, "_meta": protocol.WithMeta(nil, meta)}, &initialized); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *sdkHost) newSession(t *testing.T, meta protocol.SessionMeta) string {
	t.Helper()
	var response acp.NewSessionResponse
	if err := h.peer.Call(h.ctx, protocol.MethodSessionNew, map[string]any{"cwd": h.cwd, "mcpServers": []any{}, "_meta": protocol.WithMeta(nil, meta)}, &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionId == "" || len(response.ConfigOptions) != 2 {
		t.Fatal("missing session/config options")
	}
	return string(response.SessionId)
}
func (h *sdkHost) prompt(id, text string) (acp.PromptResponse, error) {
	var response acp.PromptResponse
	err := h.peer.Call(h.ctx, protocol.MethodSessionPrompt, map[string]any{"sessionId": id, "prompt": []acp.ContentBlock{acp.TextBlock(text)}}, &response)
	return response, err
}

func TestProvider_SDKStreamsFinalDiffAndApprovalMatrix(t *testing.T) {
	var mu sync.Mutex
	answer := "allow_once"
	seen := 0
	h := newSDKHost(t, func(_ context.Context, raw json.RawMessage) (any, error) {
		var request acp.RequestPermissionRequest
		if json.Unmarshal(raw, &request) != nil || len(request.Options) != 3 {
			t.Error("invalid host permission request")
		}
		mu.Lock()
		defer mu.Unlock()
		seen++
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(acp.PermissionOptionId(answer))}, nil
	})
	id := h.newSession(t, protocol.SessionMeta{})
	if response, err := h.prompt(id, "plain"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("plain: %+v %v", response, err)
	}
	select {
	case raw := <-h.updates:
		var update map[string]any
		_ = json.Unmarshal(raw, &update)
		nested := update["update"].(map[string]any)
		if nested["_meta"].(map[string]any)["arvel"].(map[string]any)["diff"] != "synthetic diff" {
			t.Fatal("final diff lost")
		}
	case <-h.ctx.Done():
		t.Fatal("stream missing")
	}
	for _, method := range []string{"command", "file"} {
		for _, choice := range []string{"allow_once", "allow_always", "reject_once"} {
			mu.Lock()
			answer = choice
			before := seen
			mu.Unlock()
			response, err := h.prompt(id, "approve "+method)
			if err != nil || response.StopReason != acp.StopReasonEndTurn {
				t.Fatalf("%s/%s: %+v %v", method, choice, response, err)
			}
			mu.Lock()
			after := seen
			mu.Unlock()
			if after != before+1 {
				t.Fatal("approval missing or duplicated")
			}
		}
	}
	denied := h.newSession(t, protocol.SessionMeta{Task: &protocol.TaskMeta{Type: "task", ReadOnly: true}, Permissions: &protocol.Permissions{Commands: &protocol.CommandPolicy{Default: "allow"}, Network: "allow"}})
	mu.Lock()
	before := seen
	mu.Unlock()
	if _, err := h.prompt(denied, "approve command"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != before {
		t.Fatal("read-only denial asked host")
	}
}

func TestProvider_SDKCancelApprovalKeepsSiblingStreaming(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	h := newSDKHost(t, func(ctx context.Context, _ json.RawMessage) (any, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected("allow_always")}, nil
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	a, b := h.newSession(t, protocol.SessionMeta{}), h.newSession(t, protocol.SessionMeta{})
	finished := make(chan error, 1)
	go func() {
		response, err := h.prompt(a, "approve command")
		if err == nil && response.StopReason != acp.StopReasonCancelled {
			t.Error("cancel result lost")
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("approval did not arrive")
	}
	if response, err := h.prompt(b, "plain"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("blocked sibling: %+v %v", response, err)
	}
	var status protocol.Status
	if err := h.peer.Call(h.ctx, protocol.MethodStatus, nil, &status); err != nil || status.State != protocol.StateReady {
		t.Fatalf("blocked status: %+v %v", status, err)
	}
	if err := h.peer.Notify(protocol.MethodSessionCancel, map[string]string{"sessionId": a}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-h.ctx.Done():
		t.Fatal("cancel did not finish")
	}
	close(release) // A late host grant must not authorize another turn.
	if _, err := h.prompt(a, "plain"); err != nil {
		t.Fatal(err)
	}
}

func TestProvider_SDKCrashResumesWithoutReplayingPrompt(t *testing.T) {
	h := newSDKHost(t, nil)
	id := h.newSession(t, protocol.SessionMeta{})
	if _, err := h.prompt(id, "plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.prompt(id, "crash"); err == nil {
		t.Fatal("process exit reported success")
	}
	if response, err := h.prompt(id, "plain after recovery"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("recovery: %+v %v", response, err)
	}
}
