// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

type sessionClient struct {
	mu      sync.Mutex
	opts    appserver.PeerOptions
	done    chan struct{}
	once    sync.Once
	calls   []string
	starts  int
	turns   int
	hook    func(context.Context, string, any, any) (bool, error)
	barrier func(context.Context, string, string) error
}

func encodeResult(value, result any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, result)
}
func (c *sessionClient) Call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	c.calls = append(c.calls, method)
	hook := c.hook
	c.mu.Unlock()
	if hook != nil {
		if handled, err := hook(ctx, method, params, result); handled {
			return err
		}
	}
	switch method {
	case "configRequirements/read":
		return encodeResult(map[string]any{"requirements": nil}, result)
	case "config/read":
		return encodeResult(map[string]any{"config": map[string]any{}, "origins": map[string]any{}}, result)
	case "thread/start":
		c.mu.Lock()
		c.starts++
		id := fmt.Sprintf("thread-%d", c.starts)
		c.mu.Unlock()
		c.event("thread/started", map[string]any{"thread": map[string]string{"id": id}})
		return encodeResult(map[string]any{"thread": map[string]string{"id": id}, "model": "model"}, result)
	case "thread/resume":
		id := params.(schema.ThreadResumeParams).ThreadID
		return encodeResult(map[string]any{"thread": map[string]string{"id": id}, "model": "model"}, result)
	case "turn/start":
		p := params.(schema.TurnStartParams)
		c.mu.Lock()
		c.turns++
		id := fmt.Sprintf("turn-%d", c.turns)
		c.mu.Unlock()
		c.lifecycle("turn/started", p.ThreadID, id, "inProgress")
		c.event("item/agentMessage/delta", map[string]string{"threadId": p.ThreadID, "turnId": id, "itemId": "message", "delta": "Hello"})
		c.lifecycle("turn/completed", p.ThreadID, id, "completed")
		return encodeResult(map[string]any{"turn": map[string]string{"id": id, "status": "inProgress"}}, result)
	case "thread/unsubscribe":
		return nil
	case "turn/interrupt":
		p := params.(schema.TurnInterruptParams)
		c.lifecycle("turn/completed", p.ThreadID, p.TurnID, "interrupted")
		return nil
	default:
		return errors.New("unexpected vendor method")
	}
}
func (c *sessionClient) event(method string, value any) {
	raw, _ := json.Marshal(value)
	c.opts.Notifications[method](context.Background(), raw)
}
func (c *sessionClient) lifecycle(method, thread, turn, status string) {
	c.event(method, map[string]any{"threadId": thread, "turn": map[string]string{"id": turn, "status": status}})
}
func (c *sessionClient) Close(context.Context) error { c.once.Do(func() { close(c.done) }); return nil }
func (c *sessionClient) Done() <-chan struct{}       { return c.done }
func (c *sessionClient) Err() error                  { return errors.New("synthetic exit") }
func (c *sessionClient) WaitResponses(ctx context.Context, thread, turn string) error {
	if c.barrier != nil {
		return c.barrier(ctx, thread, turn)
	}
	return ctx.Err()
}

type sessionBackend struct {
	mu         sync.Mutex
	router     *Router
	client     *sessionClient
	home       string
	generation uint64
	aborts     int
}

func (b *sessionBackend) Acquire(context.Context) (connection.Runtime, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return connection.Runtime{Client: b.client, Generation: b.generation, Home: b.home}, nil
}
func (b *sessionBackend) Models(context.Context, bool) (protocol.ModelsResult, error) {
	return protocol.ModelsResult{Default: "model", Models: []protocol.Model{{ID: "model", Label: "Model", Inputs: []string{"text", "image"}, Efforts: []protocol.Effort{"low", "high"}}, {ID: "other", Label: "Other", Inputs: []string{"text"}, Efforts: []protocol.Effort{"low"}}}}, nil
}
func (b *sessionBackend) Abort(ctx context.Context, id uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id != b.generation {
		return nil
	}
	b.aborts++
	b.router.Stopped(id)
	return b.client.Close(ctx)
}
func (b *sessionBackend) replace() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.router.Stopped(b.generation)
	_ = b.client.Close(context.Background())
	b.generation++
	b.client = &sessionClient{done: make(chan struct{}), opts: b.router.ForGeneration(b.generation)}
}

type testOutput struct {
	mu          sync.Mutex
	updates     []acp.SessionUpdate
	permission  func(context.Context, plugin.PermissionRequest) (plugin.PermissionDecision, error)
	updateError bool
}

func (o *testOutput) Update(update acp.SessionUpdate, _ *protocol.UpdateMeta) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.updateError {
		return errors.New("host unavailable")
	}
	o.updates = append(o.updates, update)
	return nil
}
func (o *testOutput) RequestPermission(ctx context.Context, request plugin.PermissionRequest) (plugin.PermissionDecision, error) {
	if o.permission != nil {
		return o.permission(ctx, request)
	}
	return plugin.PermissionDecision{OptionID: "reject_once"}, nil
}

func sessionFixture(t *testing.T) (*Session, *sessionBackend) {
	t.Helper()
	cwd, home := configDirs(t)
	router := NewRouter()
	backend := &sessionBackend{router: router, home: home, generation: 1}
	backend.client = &sessionClient{done: make(chan struct{}), opts: router.ForGeneration(1)}
	s, err := New(testContext(t), backend, router, plugin.SessionParams{CWD: cwd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(testContext(t)) })
	return s, backend
}

func TestSession_EarlyStreamFinishesBeforeStartReply(t *testing.T) {
	s, _ := sessionFixture(t)
	out := &testOutput{}
	result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("hello"), acp.ResourceLinkBlock("reference", "https://example.invalid/resource")}, out)
	if err != nil || result.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("prompt: %+v %v", result, err)
	}
	if len(out.updates) != 1 || out.updates[0].AgentMessageChunk == nil || out.updates[0].AgentMessageChunk.Content.Text.Text != "Hello" {
		t.Fatalf("updates: %+v", out.updates)
	}
	if s.Snapshot().Turns != 1 || s.Snapshot().Empty {
		t.Fatal("turn accounting lost")
	}
}

func TestSession_UnsupportedPromptNeverReachesVendor(t *testing.T) {
	s, b := sessionFixture(t)
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.ImageBlock("eA==", "image/png")}, &testOutput{}); err == nil {
		t.Fatal("unsupported media accepted")
	}
	if b.client.turns != 0 {
		t.Fatal("unsupported prompt reached vendor")
	}
}

func TestSession_LoadPreservesIdentityAndNeverResetsUnknownHistory(t *testing.T) {
	s, b := sessionFixture(t)
	id := s.ID()
	saved := s.Snapshot()
	if err := s.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	b.replace()
	b.client.hook = func(_ context.Context, method string, _ any, _ any) (bool, error) {
		if method == "thread/resume" {
			return true, &protocol.RPCError{Code: protocol.CodeInvalidRequest, Message: "no rollout found for thread id " + saved.VendorID}
		}
		return false, nil
	}
	loaded, err := Load(testContext(t), b, b.router, plugin.LoadParams{SessionID: id, SessionParams: s.params}, &saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close(testContext(t))
	if loaded.ID() != id || b.client.starts != 1 {
		t.Fatal("known empty session not replaced safely")
	}
	if err := loaded.Close(testContext(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(testContext(t), b, b.router, plugin.LoadParams{SessionID: "external", SessionParams: s.params}, nil, nil); err == nil {
		t.Fatal("unknown history was reset")
	}
	if b.client.starts != 1 {
		t.Fatal("created replacement for unknown thread")
	}
}

func TestSession_ResumeFromAndExplicitModel(t *testing.T) {
	s, b := sessionFixture(t)
	resume := "existing"
	params := s.params
	params.Meta.ResumeFrom = &resume
	params.Meta.Model = "other"
	resumed, err := New(testContext(t), b, b.router, params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close(testContext(t))
	if resumed.ID() != resume || resumed.Snapshot().Model != "other" {
		t.Fatal("resume/model override lost")
	}
}

func TestSession_CancelWaitsForResponseWriteBeforeInterrupt(t *testing.T) {
	s, b := sessionFixture(t)
	started := make(chan struct{})
	barrierEntered := make(chan struct{})
	written := make(chan struct{})
	interrupt := make(chan struct{})
	b.client.barrier = func(ctx context.Context, thread, turn string) error {
		close(barrierEntered)
		select {
		case <-written:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.client.hook = func(ctx context.Context, method string, p, r any) (bool, error) {
		switch method {
		case "turn/start":
			params := p.(schema.TurnStartParams)
			b.client.lifecycle("turn/started", params.ThreadID, "turn", "inProgress")
			close(started)
			return true, encodeResult(map[string]any{"turn": map[string]string{"id": "turn"}}, r)
		case "turn/interrupt":
			select {
			case <-written:
			default:
				t.Error("interrupt preceded reply write")
			}
			close(interrupt)
		}
		return false, nil
	}
	finished := make(chan error, 1)
	go func() {
		result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("wait")}, &testOutput{})
		if err == nil && result.StopReason != acp.StopReasonCancelled {
			err = errors.New("not cancelled")
		}
		finished <- err
	}()
	t.Cleanup(func() {
		s.Cancel()
		select {
		case <-written:
		default:
			close(written)
		}
		_ = s.Close(testContext(t))
	})
	<-started
	s.Cancel()
	s.Cancel()
	<-barrierEntered
	select {
	case <-interrupt:
		t.Fatal("interrupt bypassed write barrier")
	default:
	}
	close(written)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestSession_CancelEscalatesAndSiblingFailsAsCrash(t *testing.T) {
	s, b := sessionFixture(t)
	s.cancelAfter = 20 * time.Millisecond
	sibling, err := New(testContext(t), b, b.router, s.params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sibling.Close(testContext(t))
	started := make(chan struct{}, 2)
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		switch method {
		case "turn/start":
			params := p.(schema.TurnStartParams)
			id := "turn-" + params.ThreadID
			b.client.lifecycle("turn/started", params.ThreadID, id, "inProgress")
			started <- struct{}{}
			return true, encodeResult(map[string]any{"turn": map[string]string{"id": id}}, r)
		case "turn/interrupt":
			return true, nil
		}
		return false, nil
	}
	results := make(chan error, 2)
	go func() {
		result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("wait")}, &testOutput{})
		if err == nil && result.StopReason != acp.StopReasonCancelled {
			err = errors.New("cancelled turn not marked cancelled")
		}
		results <- err
	}()
	go func() {
		_, err := sibling.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("wait")}, &testOutput{})
		if err == nil {
			err = errors.New("sibling incorrectly completed")
		}
		results <- err
	}()
	<-started
	<-started
	s.Cancel()
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("cancel/crash distinction: %v / %v", first, second)
	}
	if b.aborts != 1 {
		t.Fatalf("aborts: %d", b.aborts)
	}
	b.replace()
	if _, err := sibling.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("recover")}, &testOutput{}); err != nil {
		t.Fatal(err)
	}
}

func TestSession_OptionsSurviveIdleReloadAndBudget(t *testing.T) {
	s, b := sessionFixture(t)
	s.params.Meta.Budget = &protocol.Budget{MaxTurns: 1}
	if _, err := s.SetOption(testContext(t), "effort", "high"); err != nil {
		t.Fatal(err)
	}
	options, err := s.SetOption(testContext(t), "model", "other")
	if err != nil {
		t.Fatal(err)
	}
	if s.Snapshot().Effort != "" {
		t.Fatal("unsupported effort retained")
	}
	options[0].Select.Name = "mutated"
	if s.ConfigOptions()[0].Select.Name == "mutated" {
		t.Fatal("shared option tree")
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("hello")}, &testOutput{}); err != nil {
		t.Fatal(err)
	}
	saved := s.Snapshot()
	_ = s.Close(testContext(t))
	loaded, err := Load(testContext(t), b, b.router, plugin.LoadParams{SessionID: s.ID(), SessionParams: s.params}, &saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close(testContext(t))
	if loaded.Snapshot().Model != "other" {
		t.Fatal("idle reload forgot model")
	}
	result, err := loaded.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("over budget")}, &testOutput{})
	if err != nil || result.Stop == nil || result.Stop.Code != protocol.ErrBudgetExceeded {
		t.Fatalf("budget: %+v %v", result, err)
	}
	if b.client.turns != 1 {
		t.Fatal("over-budget turn sent")
	}
}

func TestSession_LoadAfterRestartIgnoresHistoricalTurnEvents(t *testing.T) {
	for _, kind := range []string{"delta", "lifecycle", "usage"} {
		t.Run(kind, func(t *testing.T) {
			original, b := sessionFixture(t)
			id, params := original.ID(), original.params
			if err := original.Close(testContext(t)); err != nil {
				t.Fatal(err)
			}
			b.replace()
			replay := func(thread string) {
				switch kind {
				case "delta":
					b.client.event("item/agentMessage/delta", map[string]string{"threadId": thread, "turnId": "old-a", "itemId": "old-message", "delta": "history-leak"})
				case "lifecycle":
					b.client.lifecycle("turn/completed", thread, "old-b", "interrupted")
				case "usage":
					b.client.event("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": "old-a", "tokenUsage": map[string]any{}})
				}
			}
			b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
				switch method {
				case "thread/resume":
					thread := p.(schema.ThreadResumeParams).ThreadID
					replay(thread)
					return true, encodeResult(map[string]any{"thread": map[string]any{"id": thread, "turns": []map[string]string{{"id": "old-a", "status": "completed"}, {"id": "old-b", "status": "interrupted"}}}, "model": "model"}, r)
				case "turn/start":
					thread := p.(schema.TurnStartParams).ThreadID
					replay(thread)
					b.client.lifecycle("turn/started", thread, "fresh", "inProgress")
					b.client.event("item/agentMessage/delta", map[string]string{"threadId": thread, "turnId": "fresh", "itemId": "fresh-message", "delta": "Fresh response"})
					b.client.lifecycle("turn/completed", thread, "fresh", "completed")
					return true, encodeResult(map[string]any{"turn": map[string]string{"id": "fresh", "status": "inProgress"}}, r)
				}
				return false, nil
			}
			loaded, err := Load(testContext(t), b, b.router, plugin.LoadParams{SessionID: id, SessionParams: params}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer loaded.Close(testContext(t))
			out := &testOutput{}
			result, err := loaded.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("continue")}, out)
			if err != nil || result.StopReason != acp.StopReasonEndTurn {
				t.Fatalf("loaded prompt: %+v %v", result, err)
			}
			if len(out.updates) != 1 || out.updates[0].AgentMessageChunk == nil || out.updates[0].AgentMessageChunk.Content.Text.Text != "Fresh response" {
				t.Fatal("historical output reached current turn", out.updates)
			}
		})
	}
}
