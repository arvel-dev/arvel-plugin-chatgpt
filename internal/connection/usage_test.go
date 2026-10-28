// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/usage"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func quotaFixture(percent int) any {
	return map[string]any{"primary": map[string]any{"usedPercent": percent, "resetsAt": time.Now().Add(time.Hour).Unix(), "windowDurationMins": 10080}}
}
func waitQuota(t *testing.T, m *Manager, test func(protocol.Usage) bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		m.quota.mu.Lock()
		u := copyUsage(m.quota.current.Usage)
		m.quota.mu.Unlock()
		if test(u) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("quota worker did not converge")
		case <-tick.C:
		}
	}
}
func TestQuota_StartupActiveDeadlinePushAndScope(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().Unix())
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, quota: quotaFixture(38)}
	m := testManager(t, "chatgpt", true, f, dependencies{now: func() time.Time { return time.Unix(clock.Load(), 0) }}, nil)
	u, err := m.Usage(context.Background())
	if err != nil || len(u.Windows) != 1 || u.Windows[0].Label != "Week" {
		t.Fatal(u, err)
	}
	initial := f.count("account/rateLimits/read")
	r, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u.Windows[0].UsedPercent = 99
	if r.Quota().Usage.Windows[0].UsedPercent != 38 {
		t.Fatal("snapshot aliases mutable slice")
	}
	clock.Add(int64(usage.Freshness/time.Second) + 1)
	m.refresh()
	if f.count("account/rateLimits/read") != initial {
		t.Fatal("idle poll read quota")
	}
	release := r.ActiveTurn()
	defer release()
	waitQuota(t, m, func(u protocol.Usage) bool { return u.UpdatedAt.Unix() == clock.Load() })
	if f.count("account/rateLimits/read") != initial+1 {
		t.Fatal("activation did not refresh once")
	}
	release()
	release()
	f.mu.Lock()
	f.quota = quotaFixture(85)
	cb := f.opts.Peer.Notifications["account/rateLimits/updated"]
	f.mu.Unlock()
	cb(context.Background(), json.RawMessage(`{"rateLimits":{"primary":null}}`))
	waitQuota(t, m, func(u protocol.Usage) bool { return u.Status == protocol.UsageStatusWarning })
	// A principal change revokes every borrowed quota accessor immediately after
	// the authoritative account transition is processed.
	f.update(nil)
	statusState(t, m, protocol.StateNeedsLogin)
	if r.Quota().Usage.Source != protocol.UsageSourceUnknown {
		t.Fatal("old principal quota retained")
	}
	cb(context.Background(), json.RawMessage(`{}`))
	if r.Quota().Usage.Source != protocol.UsageSourceUnknown {
		t.Fatal("stale generation revived quota")
	}
}
func TestQuota_PushDuringReadRetainsRevisionAndHostCallbackReentry(t *testing.T) {
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, quota: quotaFixture(80)}
	var once sync.Once
	published := make(chan protocol.Usage, 8)
	var m *Manager
	ready := make(chan struct{})
	hooks := Hooks{UsageUpdated: func(u protocol.Usage) {
		<-ready
		// The worker must release the operation gate before host callbacks.
		if _, err := m.Usage(context.Background()); err != nil {
			t.Error(err)
		}
		select {
		case published <- u:
		default:
		}
	}}
	m = testManager(t, "chatgpt", true, f, dependencies{hooks: hooks}, nil)
	close(ready)
	f.mu.Lock()
	f.onQuota = func() {
		once.Do(func() {
			f.opts.Peer.Notifications["account/rateLimits/updated"](context.Background(), json.RawMessage(`{"rateLimits":{}}`))
		})
	}
	f.mu.Unlock()
	if _, err := m.Usage(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitQuota(t, m, func(u protocol.Usage) bool { return u.Status == protocol.UsageStatusWarning })
	if f.count("account/rateLimits/read") < 2 {
		t.Fatal("push during read was lost")
	}
	select {
	case u := <-published:
		if err := u.Validate(); err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback gate deadlock")
	}
}
func TestQuota_APIKeyNoLimitRPCAndFailureBackoff(t *testing.T) {
	f := &fakeClient{}
	m := testManager(t, "api_key", false, f, dependencies{}, nil)
	u, err := m.Usage(context.Background())
	if err != nil || u.Source != protocol.UsageSourceUnknown || u.Spend == nil || len(u.Windows) != 0 {
		t.Fatal(u, err)
	}
	if f.count("account/rateLimits/read") != 0 {
		t.Fatal("api key read plan quotas")
	}
	failing := &fakeClient{account: &schema.Account{Type: "chatgpt"}, quotaError: true}
	m2 := testManager(t, "chatgpt", true, failing, dependencies{}, nil)
	if u, err := m2.Usage(context.Background()); err != nil || u.Source != protocol.UsageSourceUnknown {
		t.Fatal(u, err)
	}
	before := failing.count("account/rateLimits/read")
	for i := 0; i < 3; i++ {
		m2.refresh()
	}
	if failing.count("account/rateLimits/read") != before {
		t.Fatal("failed quota read spins without backoff")
	}
}

func TestQuota_AccountReadFailureRevokesObservedWindows(t *testing.T) {
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, quota: quotaFixture(85)}
	m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
	r, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Quota().Usage.Windows) != 1 {
		t.Fatal("setup missing quota")
	}
	f.mu.Lock()
	f.accountError = true
	f.mu.Unlock()
	u, err := m.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Source != protocol.UsageSourceUnknown || len(u.Windows) != 0 || len(r.Quota().Usage.Windows) != 0 {
		t.Fatal("unverified account retained measured quota", u)
	}
	f.mu.Lock()
	f.accountError = false
	f.mu.Unlock()
	restored, err := m.Usage(context.Background())
	if err != nil || len(restored.Windows) != 1 {
		t.Fatal("account recovery did not refetch quota", restored, err)
	}
}

func TestQuota_ActiveTurnRemainsTrackedWhileAccountTemporarilyUnavailable(t *testing.T) {
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, quota: quotaFixture(38)}
	m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
	runtime, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.accountError = true
	f.mu.Unlock()
	statusState(t, m, protocol.StateError)
	release := runtime.ActiveTurn()
	defer release()
	m.quota.mu.Lock()
	active := m.quota.active
	m.quota.mu.Unlock()
	if active != 1 {
		t.Fatal("live turn was not tracked during temporary account failure")
	}
	f.mu.Lock()
	f.accountError = false
	f.mu.Unlock()
	statusState(t, m, protocol.StateReady)
	m.quota.mu.Lock()
	active = m.quota.active
	m.quota.mu.Unlock()
	if active != 1 {
		t.Fatal("same-principal recovery lost live turn tracking")
	}
	release()
	release()
	m.quota.mu.Lock()
	active = m.quota.active
	m.quota.mu.Unlock()
	if active != 0 {
		t.Fatal("turn release is not idempotent")
	}
}
