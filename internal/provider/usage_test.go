// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func arvelResult(t *testing.T, response acp.PromptResponse) struct {
	Usage *protocol.TurnUsage  `json:"usage"`
	Stop  *protocol.StopDetail `json:"stop"`
} {
	t.Helper()
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-vendor-canary") {
		t.Fatal("vendor message leaked")
	}
	var body struct {
		Meta struct {
			Arvel struct {
				Usage *protocol.TurnUsage  `json:"usage"`
				Stop  *protocol.StopDetail `json:"stop"`
			} `json:"arvel"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body.Meta.Arvel
}
func TestProvider_SDKUsageTokensQuotaResumeAndTypedErrors(t *testing.T) {
	clock := &wireClock{now: time.Now()}
	h := newSDKHost(t, nil, plugin.WithClock(clock), plugin.WithIdleTimeout(time.Second))
	id := h.newSession(t, protocol.SessionMeta{})
	var u protocol.Usage
	if err := h.peer.Call(h.ctx, protocol.MethodUsage, nil, &u); err != nil || u.Source != protocol.UsageSourceExact || len(u.Windows) != 1 || u.Windows[0].UsedPercent != 38 {
		t.Fatal(u, err)
	}
	response, err := h.prompt(id, "usage quota")
	if err != nil {
		t.Fatal(err)
	}
	result := arvelResult(t, response)
	if result.Stop == nil || result.Stop.Code != protocol.ErrRateLimited || !result.Stop.Retryable || result.Stop.Validate() != nil {
		t.Fatal(result)
	}
	if result.Usage == nil || result.Usage.InputTokens != 20 || result.Usage.CacheReadTokens != 10 || result.Usage.CostSource != protocol.UsageSourceUnknown {
		t.Fatal(result)
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), `"costUsd"`) {
		t.Fatal("invented dollars")
	}
	// SDK idle unload/reload must resume the persisted thread after quota.
	clock.advance(2 * time.Second)
	var loaded acp.LoadSessionResponse
	if err := h.peer.Call(h.ctx, protocol.MethodSessionLoad, map[string]any{"sessionId": id, "cwd": h.cwd, "mcpServers": []any{}}, &loaded); err != nil {
		t.Fatal(err)
	}
	response, err = h.prompt(id, "usage continue")
	if err != nil {
		t.Fatal(err)
	}
	if result = arvelResult(t, response); result.Stop != nil || result.Usage == nil || result.Usage.InputTokens != 20 {
		t.Fatal(result)
	}
	if _, err = h.prompt(id, "transient"); err != nil {
		t.Fatal(err)
	}
	response, err = h.prompt(id, "model-error")
	if err != nil {
		t.Fatal(err)
	}
	result = arvelResult(t, response)
	if result.Stop == nil || result.Stop.Code != protocol.ErrModelUnavailable || result.Stop.Model == "" || result.Stop.Validate() != nil {
		t.Fatal(result)
	}
}
func TestProvider_SDKUsageNotificationThrottleLatestAndLogout(t *testing.T) {
	clock := &wireClock{now: time.Now()}
	h := newSDKHost(t, nil, plugin.WithClock(clock))
	id := h.newSession(t, protocol.SessionMeta{})
	var u protocol.Usage
	if err := h.peer.Call(h.ctx, protocol.MethodUsage, nil, &u); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.usages:
	case <-time.After(2 * time.Second):
		t.Fatal("no initial usage notification")
	}
	if _, err := h.prompt(id, "push-warning"); err != nil {
		t.Fatal(err)
	}
	// The synchronous request establishes the new snapshot before clock advance.
	if err := h.peer.Call(h.ctx, protocol.MethodUsage, nil, &u); err != nil || u.Status != protocol.UsageStatusWarning {
		t.Fatal(u, err)
	}
	// The worker's callback may arrive after the synchronous usage reply. Drive
	// the injected clock while observing wire messages, never sleep five seconds.
	await := func(test func(protocol.Usage) bool) {
		t.Helper()
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case u := <-h.usages:
				if err := u.Validate(); err != nil {
					t.Fatal(err)
				}
				if test(u) {
					return
				}
			case <-tick.C:
				clock.advance(5 * time.Second)
			case <-deadline.C:
				t.Fatal("missing trailing usage wire notification")
			}
		}
	}
	await(func(u protocol.Usage) bool {
		return u.Status == protocol.UsageStatusWarning && len(u.Windows) == 1 && u.Windows[0].UsedPercent == 85
	})
	var result protocol.ActionResult
	if err := h.peer.Call(h.ctx, protocol.MethodAction, map[string]any{"id": "logout_arvel_login"}, &result); err != nil {
		t.Fatal(err)
	}
	await(func(u protocol.Usage) bool { return u.Source == protocol.UsageSourceUnknown && len(u.Windows) == 0 })
}
