// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/usage"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func tokenEvent(c *sessionClient, thread, turn string, input int) {
	c.event("thread/tokenUsage/updated", map[string]any{"threadId": thread, "turnId": turn, "tokenUsage": map[string]any{"last": map[string]int{"inputTokens": input, "outputTokens": 5, "cachedInputTokens": 10, "reasoningOutputTokens": 2, "totalTokens": input + 5}, "total": map[string]int{"inputTokens": 999}}})
}
func TestTurn_UsageQuotaAndSameThreadRecovery(t *testing.T) {
	s, b := sessionFixture(t)
	thread := s.ID()
	now := time.Now()
	s.runtime.Quota = func() usage.QuotaContext {
		return usage.QuotaContext{ObservedAt: now, Usage: protocol.Usage{Source: protocol.UsageSourceExact, Windows: []protocol.UsageWindow{{ID: "primary", UsedPercent: 100, ResetsAt: now.Add(time.Hour)}}}}
	}
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		params := p.(schema.TurnStartParams)
		tokenEvent(b.client, params.ThreadID, "limited", 20)
		tokenEvent(b.client, params.ThreadID, "limited", 25)
		// Invalid optional counters must preserve the last valid measurement.
		tokenEvent(b.client, params.ThreadID, "limited", -1)
		b.client.event("error", map[string]any{"threadId": thread, "turnId": "limited", "willRetry": true, "error": map[string]any{"codexErrorInfo": "rateLimitExceeded", "message": "private-canary"}})
		b.client.event("turn/completed", map[string]any{"threadId": thread, "turn": map[string]any{"id": "limited", "status": "failed", "error": map[string]any{"codexErrorInfo": "usageLimitExceeded", "message": "private-canary"}}})
		tokenEvent(b.client, thread, "limited", 900) // May precede the start reply, never replaces terminal usage.
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "limited"}}, r)
	}
	result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("quota")}, &testOutput{})
	if err != nil || result.Stop == nil || result.Stop.Code != protocol.ErrRateLimited || result.Stop.WindowID != "primary" || result.Usage == nil || result.Usage.InputTokens != 25 {
		t.Fatalf("%+v %v", result, err)
	}
	if s.broken || b.aborts != 0 || s.ID() != thread {
		t.Fatal("quota retired resumable thread")
	}
	b.client.hook = nil
	result, err = s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("continue")}, &testOutput{})
	if err != nil || result.Stop != nil || s.ID() != thread || result.Usage != nil {
		t.Fatalf("recovery %+v %v", result, err)
	}
}
func TestTurn_AdvisoriesAreNotTerminalAndTokenOwnership(t *testing.T) {
	s, b := sessionFixture(t)
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		thread := p.(schema.TurnStartParams).ThreadID
		tokenEvent(b.client, "unknown-child", "turn", 999)
		tokenEvent(b.client, thread, "turn", 20)
		for _, retry := range []bool{true, false, true} {
			b.client.event("error", map[string]any{"threadId": thread, "turnId": "turn", "willRetry": retry, "error": map[string]string{"codexErrorInfo": "usageLimitExceeded", "message": "no terminal proof"}})
		}
		b.client.lifecycle("turn/completed", thread, "turn", "completed")
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "turn"}}, r)
	}
	result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("retry")}, &testOutput{})
	if err != nil || result.Stop != nil || result.Usage == nil || result.Usage.InputTokens != 20 {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestTurn_UsageAttachedToInterruptedAndFailure(t *testing.T) {
	for _, status := range []string{"interrupted", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, b := sessionFixture(t)
			b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
				if method != "turn/start" {
					return false, nil
				}
				thread := p.(schema.TurnStartParams).ThreadID
				tokenEvent(b.client, thread, "turn", 20)
				b.client.lifecycle("turn/completed", thread, "turn", status)
				return true, encodeResult(map[string]any{"turn": map[string]string{"id": "turn"}}, r)
			}
			result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("prompt")}, &testOutput{})
			if err != nil || result.Usage == nil {
				t.Fatal(result, err)
			}
			if status == "failed" && (result.Stop == nil || result.Stop.Code != protocol.ErrProviderError) {
				t.Fatal(result)
			}
		})
	}
}
func TestTurn_StartErrorIsSafeTypedRPC(t *testing.T) {
	s, b := sessionFixture(t)
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		return true, &protocol.RPCError{Code: 429, Message: "sk-private-canary", Data: json.RawMessage(`{"codexErrorInfo":"usageLimitExceeded"}`)}
	}
	_, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("prompt")}, &testOutput{})
	rpc, ok := err.(*protocol.RPCError)
	if !ok {
		t.Fatal(err)
	}
	d, ok := rpc.Arvel()
	if !ok || d.Code != protocol.ErrProviderError || d.Validate() != nil {
		t.Fatal(err)
	}
}

func TestTurn_ConcurrentSessionsKeepTokenCountersSeparate(t *testing.T) {
	first, b := sessionFixture(t)
	second, err := New(testContext(t), b, b.router, first.params, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	b.client.hook = func(ctx context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		thread := p.(schema.TurnStartParams).ThreadID
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return true, ctx.Err()
		}
		input := 20
		if thread == second.ID() {
			input = 40
		}
		tokenEvent(b.client, thread, "turn-"+thread, input)
		b.client.lifecycle("turn/completed", thread, "turn-"+thread, "completed")
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "turn-" + thread}}, r)
	}
	type outcome struct {
		tokens int64
		err    error
	}
	done := make(chan outcome, 2)
	for _, s := range []*Session{first, second} {
		go func() {
			r, e := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("concurrent")}, &testOutput{})
			var count int64
			if r.Usage != nil {
				count = r.Usage.InputTokens
			}
			done <- outcome{count, e}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("turns did not run concurrently")
		}
	}
	close(release)
	seen := map[int64]bool{}
	for i := 0; i < 2; i++ {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		seen[r.tokens] = true
	}
	if !seen[20] || !seen[40] {
		t.Fatal("sibling counters mixed", seen)
	}
}
