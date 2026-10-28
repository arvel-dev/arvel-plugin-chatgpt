// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"context"
	"encoding/json"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func TestProvider_SDKChildAttributionAndIdleReload(t *testing.T) {
	clock := &wireClock{now: time.Now()}
	h := newSDKHost(t, nil, plugin.WithClock(clock), plugin.WithIdleTimeout(time.Second))
	if !h.children {
		t.Fatal("subagent capability absent from SDK wire")
	}
	id := h.newSession(t, protocol.SessionMeta{Subagents: &protocol.Subagents{Enabled: true, MaxParallel: 2, Model: "gpt-6.1-sol", Effort: "low"}})
	for iteration := range 2 {
		if iteration == 1 {
			clock.advance(2 * time.Second)
		}
		if _, err := h.prompt(id, "child-agent"); err != nil {
			t.Fatal(err)
		}
	}
	var feed strings.Builder
	for len(h.updates) > 0 {
		feed.Write(<-h.updates)
	}
	if strings.Count(feed.String(), "early child output") != 2 || !strings.Contains(feed.String(), `"subagent":`) || !strings.Contains(feed.String(), `"parentToolCallId":"spawn-`) {
		t.Fatalf("missing child attribution: %s", feed.String())
	}
}

func TestProvider_SDKChildPermissionCancellationKeepsSiblingResponsive(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := newSDKHost(t, func(ctx context.Context, raw json.RawMessage) (any, error) {
		var request acp.RequestPermissionRequest
		if json.Unmarshal(raw, &request) != nil || !strings.HasPrefix(string(request.ToolCall.ToolCallId), "child:") {
			t.Error("child permission not attributed")
		}
		close(entered)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	})
	defer close(release)
	id := h.newSession(t, protocol.SessionMeta{Subagents: &protocol.Subagents{Enabled: true}})
	sibling := h.newSession(t, protocol.SessionMeta{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := h.prompt(id, "child-agent approve wait")
		if err != nil || result.StopReason != acp.StopReasonCancelled {
			t.Errorf("%+v %v", result, err)
		}
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("child permission not delivered")
	}
	if _, err := h.prompt(sibling, "plain"); err != nil {
		t.Fatal(err)
	}
	if err := h.peer.Notify(protocol.MethodSessionCancel, map[string]any{"sessionId": id}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-h.ctx.Done():
		t.Fatal("child cancellation did not finish")
	}
}
