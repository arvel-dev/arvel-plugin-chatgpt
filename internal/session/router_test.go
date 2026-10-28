// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func emit(opts appserver.PeerOptions, thread, text string) {
	raw, _ := json.Marshal(map[string]string{"threadId": thread, "turnId": "turn", "itemId": "item", "delta": text})
	opts.Notifications["item/agentMessage/delta"](context.Background(), raw)
}

func TestRouter_StagesEarlyEventsAndSeparatesThreads(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	release, err := r.Prepare(1)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	opts.Notifications["thread/started"](context.Background(), json.RawMessage(`{"thread":{"id":"a"}}`))
	emit(opts, "b", "B")
	emit(opts, "a", "A")
	a, err := r.Register(1, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := r.Register(1, "b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	release()
	e, err := a.Next(testContext(t))
	if err != nil || e.Method != "thread/started" {
		t.Fatalf("first event: %s %v", e.Method, err)
	}
	for _, tc := range []struct {
		sub  *Subscription
		want string
	}{{a, "A"}, {b, "B"}} {
		e, err := tc.sub.Next(testContext(t))
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Delta string `json:"delta"`
		}
		_ = json.Unmarshal(e.Params, &v)
		if v.Delta != tc.want {
			t.Fatalf("cross-thread event: %q", v.Delta)
		}
	}
	emit(opts, "unknown", "ignored")
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.current.staged) != 0 || r.current.bytes != 0 {
		t.Fatal("retained unregistered events")
	}
}

func TestRouter_ApprovalWaitDoesNotBlockOtherThread(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	a, _ := r.Register(1, "a")
	defer a.Close()
	b, _ := r.Register(1, "b")
	defer b.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	end, err := a.BeginTurn(func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		close(entered)
		select {
		case <-release:
			return map[string]string{"decision": "accept"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = opts.Requests["item/commandExecution/requestApproval"](testContext(t), json.RawMessage(`{"threadId":"a","turnId":"turn","itemId":"item"}`))
	}()
	t.Cleanup(func() { close(release); wg.Wait() })
	select {
	case <-entered:
	case <-testContext(t).Done():
		t.Fatal("approval never entered")
	}
	emit(opts, "b", "streaming")
	if _, err := b.Next(testContext(t)); err != nil {
		t.Fatal(err)
	}
	end() // Cancels the outstanding handler and does not wait while holding a lock.
}

func TestRouter_DeniesUnknownStaleAndUnsupportedApprovals(t *testing.T) {
	r := NewRouter()
	old := r.ForGeneration(1)
	a, _ := r.Register(1, "a")
	end, _ := a.BeginTurn(func(context.Context, string, json.RawMessage) (any, error) {
		t.Error("unsafe dispatch")
		return nil, nil
	})
	defer end()
	r.ForGeneration(2)
	for _, tc := range []struct{ method, thread, want string }{
		{"item/commandExecution/requestApproval", "a", `{"decision":"decline"}`},
		{"item/fileChange/requestApproval", "unknown", `{"decision":"decline"}`},
		{"item/permissions/requestApproval", "a", `{"permissions":{},"scope":"turn"}`},
		{"mcpServer/elicitation/request", "a", `{"action":"decline","content":null}`},
	} {
		got, err := old.Requests[tc.method](testContext(t), json.RawMessage(`{"threadId":"`+tc.thread+`"}`))
		encoded, _ := json.Marshal(got)
		if err != nil || string(encoded) != tc.want {
			t.Errorf("%s: %s, %v", tc.method, encoded, err)
		}
	}
	emit(old, "a", "stale")
	if _, err := a.Next(testContext(t)); !errors.Is(err, ErrRouting) {
		t.Fatalf("stale stream: %v", err)
	}
	a.Close()
}

func TestRouter_OverflowFailsEntireGeneration(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "registered", true: "early"}[staged], func(t *testing.T) {
			r := NewRouter()
			opts := r.ForGeneration(1)
			var sub *Subscription
			if staged {
				release, err := r.Prepare(1)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			} else {
				sub, _ = r.Register(1, "a")
				defer sub.Close()
			}
			for i := 0; i <= maxEvents; i++ {
				emit(opts, "a", "x")
			}
			if id, err := r.NextAbort(testContext(t)); err != nil || id != 1 {
				t.Fatalf("abort: %d %v", id, err)
			}
			if sub != nil {
				if _, err := sub.Next(testContext(t)); !errors.Is(err, ErrRouting) {
					t.Fatal(err)
				}
			}
			if _, err := r.Register(1, "new"); !errors.Is(err, ErrRouting) {
				t.Fatal("overflow generation remained usable")
			}
		})
	}
}

func TestRouter_StoppedCancelsApprovalAndRetiresMetadata(t *testing.T) {
	r := NewRouter()
	opts := r.ForGeneration(1)
	sub, _ := r.Register(1, "a")
	entered := make(chan struct{})
	finished := make(chan struct{})
	end, _ := sub.BeginTurn(func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	defer end()
	opts.Notifications["item/started"](context.Background(), json.RawMessage(`{"threadId":"a","turnId":"turn","item":{"id":"item","type":"fileChange","changes":[{"path":"file","kind":{"type":"add"},"diff":"body"}]}}`))
	paths := sub.ItemPaths("item")
	if len(paths) != 1 || paths[0] != "file" {
		t.Fatal(paths)
	}
	paths[0] = "modified"
	if sub.ItemPaths("item")[0] != "file" {
		t.Fatal("metadata not copied")
	}
	go func() {
		defer close(finished)
		got, err := opts.Requests["item/fileChange/requestApproval"](testContext(t), json.RawMessage(`{"threadId":"a","turnId":"turn","itemId":"item"}`))
		raw, _ := json.Marshal(got)
		if err != nil || string(raw) != `{"decision":"cancel"}` {
			t.Errorf("stop response: %s %v", raw, err)
		}
	}()
	t.Cleanup(func() { r.Stopped(1); <-finished })
	select {
	case <-entered:
	case <-testContext(t).Done():
		t.Fatal("handler did not start")
	}
	r.Stopped(1)
	r.Stopped(1)
	select {
	case <-finished:
	case <-testContext(t).Done():
		t.Fatal("handler did not stop")
	}
	if len(sub.ItemPaths("item")) != 0 {
		t.Fatal("retained stopped metadata")
	}
}

func TestRouter_MetadataBudgetIncludesKeysAndPathSlots(t *testing.T) {
	for _, paths := range []bool{false, true} {
		t.Run(map[bool]string{false: "keys", true: "path-slots"}[paths], func(t *testing.T) {
			q := &route{items: make(map[string][]string)}
			for i := 0; i < maxItems; i++ {
				id := strings.Repeat("x", 240) + string(rune(i+1))
				changes := []any{}
				if paths {
					for range 1024 {
						changes = append(changes, map[string]any{"path": "x", "kind": map[string]string{"type": "add"}, "diff": ""})
					}
				}
				raw, _ := json.Marshal(map[string]any{"item": map[string]any{"id": id, "type": "fileChange", "changes": changes}})
				if !cacheItem(q, Event{Method: "item/started", Params: raw}) {
					return
				}
			}
			t.Fatal("retained metadata bypassed its memory budget")
		})
	}
}
