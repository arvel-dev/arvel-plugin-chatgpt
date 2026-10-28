// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestChildren_StageUntilSourceConfirmationAndPreserveOrder(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	root, err := r.Register(1, "root")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.EnableChildren(1); err != nil {
		t.Fatal(err)
	}
	end, err := root.BeginTurn(func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("child request sent to root handler")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	emit := func(method string, v any) { raw, _ := json.Marshal(v); opts.Notifications[method](testContext(t), raw) }
	emit("item/agentMessage/delta", map[string]string{"threadId": "child", "turnId": "ct", "itemId": "cm", "delta": "early child"})
	emit("item/started", map[string]any{"threadId": "root", "turnId": "rt", "item": map[string]string{"type": "subAgentActivity", "id": "activity", "agentThreadId": "child", "kind": "started"}})
	emit("turn/completed", map[string]any{"threadId": "root", "turn": map[string]string{"id": "rt", "status": "completed"}})
	first, err := root.Next(testContext(t))
	if err != nil || first.Method != "item/started" {
		t.Fatal("pending child leaked before confirmation")
	}
	child, err := root.ClaimChild("child")
	if err != nil {
		t.Fatal(err)
	}
	replied := make(chan struct{})
	raw := json.RawMessage(`{"threadId":"child","turnId":"ct","itemId":"ci","command":"owned"}`)
	go func() {
		defer close(replied)
		got, err := opts.Requests["item/commandExecution/requestApproval"](testContext(t), raw)
		encoded, _ := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"decision":"accept"`) {
			t.Errorf("%s %v", encoded, err)
		}
	}()
	if err := child.ConfirmChild(func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]string{"decision": "accept"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	<-replied
	for _, want := range []string{"item/agentMessage/delta", "turn/completed"} {
		event, err := root.Next(testContext(t))
		if err != nil || event.Method != want {
			t.Fatalf("want %s got %s %v", want, event.Method, err)
		}
	}
	end()
	root.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.current.routes) != 0 || len(r.current.staged) != 0 || r.current.bytes != 0 {
		t.Fatal("child routing resources leaked")
	}
}

func TestChildren_CrossRootClaimRetiresGeneration(t *testing.T) {
	r := NewRouter()
	r.ForGeneration(1)
	first, _ := r.Register(1, "first")
	second, _ := r.Register(1, "second")
	_ = first.EnableChildren(1)
	_ = second.EnableChildren(1)
	if _, err := first.ClaimChild("child"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ClaimChild("child"); err == nil {
		t.Fatal("child moved to sibling")
	}
	if _, err := first.Next(testContext(t)); err == nil {
		t.Fatal("conflicting generation still usable")
	}
}

func TestChildren_UnknownRequestsDeclineAndParentCloseReleasesPending(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	root, _ := r.Register(1, "root")
	_ = root.EnableChildren(1)
	end, _ := root.BeginTurn(func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("unconfirmed child reached host")
		return nil, nil
	})
	_, err := root.ClaimChild("child")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		got, err := opts.Requests["mcpServer/elicitation/request"](testContext(t), json.RawMessage(`{"threadId":"child","turnId":"ct"}`))
		encoded, _ := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"action":"decline"`) {
			t.Errorf("%s %v", encoded, err)
		}
	}()
	end()
	root.Close()
	<-done
}
