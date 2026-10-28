// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/policy"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func TestApprovals_CommandAndFileDecisionMatrix(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval"} {
		for _, tc := range []struct {
			name                 string
			level                protocol.PermissionLevel
			answer               string
			cancelled, hostError bool
			want                 string
			asks                 int
		}{
			{"once", "ask", "allow_once", false, false, "accept", 1},
			{"session", "ask", "allow_always", false, false, "acceptForSession", 1},
			{"reject", "ask", "reject_once", false, false, "decline", 1},
			{"unknown-answer", "ask", "unknown", false, false, "decline", 1},
			{"deny", "deny", "allow_once", false, false, "decline", 0},
			{"allow", "allow", "", false, false, "accept", 0},
			{"cancel", "ask", "", true, false, "cancel", 1},
			{"host-failure", "ask", "", false, true, "decline", 1},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				s, b := sessionFixture(t)
				p, err := policy.Compile(protocol.SessionMeta{Permissions: &protocol.Permissions{Edit: tc.level, Network: tc.level, Commands: &protocol.CommandPolicy{Default: tc.level}}})
				if err != nil {
					t.Fatal(err)
				}
				s.config.policy = p
				b.client.event("item/started", map[string]any{"threadId": s.ID(), "turnId": "turn", "item": map[string]any{"id": "item", "type": "fileChange", "changes": []any{map[string]any{"path": filepath.Join(s.config.start.CWD, "new-file"), "kind": map[string]string{"type": "add"}, "diff": "content"}}}})
				asks := 0
				out := &testOutput{permission: func(_ context.Context, request plugin.PermissionRequest) (plugin.PermissionDecision, error) {
					asks++
					if len(request.Options) != 3 || request.ToolCall.ToolCallId != "item" {
						t.Error("invalid host contract")
					}
					if method == "item/commandExecution/requestApproval" && request.Meta.Reason == "" {
						t.Error("elevation capabilities hidden")
					}
					if tc.hostError {
						return plugin.PermissionDecision{}, errors.New("host failure")
					}
					return plugin.PermissionDecision{OptionID: tc.answer, Cancelled: tc.cancelled}, nil
				}}
				raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "itemId": "item", "command": "tool --flag", "availableDecisions": []string{"acceptWithExecpolicyAmendment"}, "additionalPermissions": map[string]any{"network": true}})
				result, err := s.approve(testContext(t), context.Background(), method, raw, out, s.runtime, s.sub, s.ID(), func(id string) bool { return id == "turn" })
				encoded, _ := json.Marshal(result)
				if err != nil || string(encoded) != `{"decision":"`+tc.want+`"}` || asks != tc.asks {
					t.Fatalf("decision %s err %v asks %d", encoded, err, asks)
				}
			})
		}
	}
}

func TestApprovals_CancelWaitingHostAndNoLocalCache(t *testing.T) {
	s, _ := sessionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	finished := make(chan struct{})
	out := &testOutput{permission: func(ctx context.Context, _ plugin.PermissionRequest) (plugin.PermissionDecision, error) {
		close(entered)
		<-ctx.Done()
		return plugin.PermissionDecision{Cancelled: true}, nil
	}}
	raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "itemId": "item", "command": "tool"})
	go func() {
		defer close(finished)
		result, err := s.approve(testContext(t), ctx, "item/commandExecution/requestApproval", raw, out, s.runtime, s.sub, s.ID(), func(string) bool { return true })
		encoded, _ := json.Marshal(result)
		if err != nil || string(encoded) != `{"decision":"cancel"}` {
			t.Errorf("cancel: %s %v", encoded, err)
		}
	}()
	t.Cleanup(func() { cancel(); <-finished })
	<-entered
	cancel()
	<-finished
	asks := 0
	out.permission = func(context.Context, plugin.PermissionRequest) (plugin.PermissionDecision, error) {
		asks++
		return plugin.PermissionDecision{OptionID: "allow_always"}, nil
	}
	for range 2 {
		_, err := s.approve(testContext(t), context.Background(), "item/commandExecution/requestApproval", raw, out, s.runtime, s.sub, s.ID(), func(string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
	}
	if asks != 2 {
		t.Fatal("item ID incorrectly deduplicated permission requests")
	}
}

func TestApprovals_DeclinesUnknownFileScopeAndDeniedEscalation(t *testing.T) {
	s, b := sessionFixture(t)
	asks := 0
	out := &testOutput{permission: func(context.Context, plugin.PermissionRequest) (plugin.PermissionDecision, error) {
		asks++
		return plugin.PermissionDecision{OptionID: "allow_once"}, nil
	}}
	for _, root := range []string{"", filepath.Dir(s.config.start.CWD)} {
		raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "itemId": "unknown", "grantRoot": root})
		got, _ := s.approve(testContext(t), context.Background(), "item/fileChange/requestApproval", raw, out, s.runtime, s.sub, s.ID(), func(string) bool { return true })
		encoded, _ := json.Marshal(got)
		if string(encoded) != `{"decision":"decline"}` {
			t.Fatal(string(encoded))
		}
	}
	// A command allow-list cannot authorize an unsandboxed retry when edits or network are denied.
	s.config.policy, _ = policy.Compile(protocol.SessionMeta{Permissions: &protocol.Permissions{Edit: "deny", Network: "allow", Commands: &protocol.CommandPolicy{Default: "allow"}}})
	raw, _ := json.Marshal(map[string]any{"threadId": s.ID(), "turnId": "turn", "itemId": "command", "command": "allowed"})
	got, _ := s.approve(testContext(t), context.Background(), "item/commandExecution/requestApproval", raw, out, s.runtime, s.sub, s.ID(), func(string) bool { return true })
	encoded, _ := json.Marshal(got)
	if string(encoded) != `{"decision":"decline"}` || asks != 0 {
		t.Fatal("denied capabilities escalated")
	}
	_ = b
}

func TestApprovals_FilePathsRejectSymlinkAndPrivateEscapes(t *testing.T) {
	cwd, home := configDirs(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	home, _ = filepath.EvalSymlinks(home)
	if _, err := safeEditPath(cwd, "nested/new-file", home); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, filepath.Dir(home), "../outside"} {
		if _, err := safeEditPath(cwd, path, home); err == nil {
			t.Fatalf("escape accepted: %s", path)
		}
	}
	if err := os.Symlink(home, filepath.Join(cwd, "link")); err == nil {
		if _, err := safeEditPath(cwd, filepath.Join("link", "new-file"), home); err == nil {
			t.Fatal("symlink escape accepted")
		}
	}
}

func TestRouter_UnsupportedProducersNeverAskOrGrant(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	sub, _ := r.Register(1, "thread")
	defer sub.Close()
	var mu sync.Mutex
	asks := 0
	end, _ := sub.BeginTurn(func(context.Context, string, json.RawMessage) (any, error) {
		mu.Lock()
		asks++
		mu.Unlock()
		return map[string]string{"decision": "accept"}, nil
	})
	defer end()
	for _, method := range []string{"item/permissions/requestApproval"} {
		got, err := opts.Requests[method](testContext(t), json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item","permissions":{"network":{"enabled":true}},"serverName":"Arvel","message":"Allow tool"}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(got)
		want := `{"permissions":{},"scope":"turn"}`
		if string(raw) != want {
			t.Fatal(string(raw))
		}
	}
	if asks != 0 {
		t.Fatal("unverified producer activated")
	}
}
