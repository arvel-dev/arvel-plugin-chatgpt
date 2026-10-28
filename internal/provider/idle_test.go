// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

type wireClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*wireTimer
}
type wireTimer struct {
	clock   *wireClock
	at      time.Time
	fn      func()
	stopped bool
}

func (c *wireClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *wireClock) AfterFunc(delay time.Duration, fn func()) plugin.Stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &wireTimer{clock: c, at: c.now.Add(delay), fn: fn}
	c.timers = append(c.timers, timer)
	return timer
}
func (t *wireTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.stopped
	t.stopped = true
	return active
}
func (c *wireClock) advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	var callbacks []func()
	for _, timer := range c.timers {
		if !timer.stopped && !timer.at.After(c.now) {
			timer.stopped = true
			callbacks = append(callbacks, timer.fn)
		}
	}
	c.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}

func TestProvider_SDKIdleReloadPreservesChangedEffortAndBudget(t *testing.T) {
	clock := &wireClock{now: time.Now()}
	h := newSDKHost(t, nil, plugin.WithClock(clock), plugin.WithIdleTimeout(time.Second))
	id := h.newSession(t, protocol.SessionMeta{Effort: "low", Budget: &protocol.Budget{MaxTurns: 2}})
	if _, err := h.prompt(id, "plain"); err != nil {
		t.Fatal(err)
	}
	if err := h.peer.Call(h.ctx, protocol.MethodSessionSetConfigOption, map[string]string{"sessionId": id, "configId": "effort", "value": "high"}, &acp.SetSessionConfigOptionResponse{}); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Second)
	if response, err := h.prompt(id, "plain expect high"); err != nil || response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("idle reload: %+v %v", response, err)
	}
	response, err := h.prompt(id, "over budget")
	if err != nil || response.StopReason != acp.StopReasonMaxTurnRequests {
		t.Fatalf("idle budget: %+v %v", response, err)
	}
}
