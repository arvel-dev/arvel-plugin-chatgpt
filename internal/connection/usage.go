// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/usage"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// quotaState is guarded independently from the operation gate. Vendor callbacks
// and borrowed runtimes never acquire that gate while a turn is waiting.
type quotaState struct {
	mu                          sync.Mutex
	generation, epoch, revision uint64
	ready, dirty                bool
	active                      int
	next                        time.Time
	current                     usage.QuotaContext
	published                   protocol.Usage
	havePublished               bool
}

func copyUsage(u protocol.Usage) protocol.Usage {
	u.Windows = append([]protocol.UsageWindow(nil), u.Windows...)
	if u.Spend != nil {
		v := *u.Spend
		u.Spend = &v
	}
	return u
}

// resetQuota is called under the operation gate on principal/process changes.
func (m *Manager) resetQuota() {
	q := &m.quota
	q.mu.Lock()
	q.epoch++
	q.generation = m.generation
	q.ready = false
	q.dirty = false
	q.active = 0
	q.next = time.Time{}
	q.current = usage.QuotaContext{Usage: usage.Unknown(m.cfg.kind)}
	q.mu.Unlock()
	m.signal()
}

func (m *Manager) quotaEvent(generation uint64) {
	q := &m.quota
	q.mu.Lock()
	if q.generation == generation {
		q.revision++
		q.dirty = true
	}
	q.mu.Unlock()
	m.signal()
}

func (m *Manager) quotaReady(ready bool) {
	q := &m.quota
	q.mu.Lock()
	q.ready = ready
	if !ready {
		q.current = usage.QuotaContext{Usage: usage.Unknown(m.cfg.kind)}
	}
	q.mu.Unlock()
}

// refreshQuota is invoked only under the account operation gate, never by a
// vendor callback. Pushes during the RPC retain a newer dirty revision.
func (m *Manager) refreshQuota(ctx context.Context) {
	q := &m.quota
	now := m.deps.now()
	q.mu.Lock()
	if !q.ready || m.cfg.kind != "chatgpt" || m.client == nil || (!q.dirty && !q.next.IsZero() && (q.active == 0 || now.Before(q.next))) {
		q.mu.Unlock()
		return
	}
	generation, epoch, rev := q.generation, q.epoch, q.revision
	q.mu.Unlock()
	var response schema.RateLimitsResponse
	err := m.client.Call(ctx, "account/rateLimits/read", struct{}{}, &response)
	var snapshot schema.RateLimitSnapshot
	if err == nil {
		err = json.Unmarshal(response.RateLimits, &snapshot)
	}
	q.mu.Lock()
	if q.generation != generation || q.epoch != epoch {
		q.mu.Unlock()
		return
	}
	q.next = now.Add(usage.Freshness)
	q.dirty = q.revision != rev
	q.current = usage.QuotaContext{Usage: usage.Unknown(m.cfg.kind)}
	if err == nil && !q.dirty {
		q.current = usage.QuotaContext{Usage: usage.Snapshot(snapshot, m.cfg.kind, now), ObservedAt: now}
	}
	dirty := q.dirty
	q.mu.Unlock()
	if err != nil {
		m.log.Debug("quota snapshot unavailable")
	}
	if dirty {
		m.signal()
	}
}

// publishUsage runs only on the existing owned worker, outside the operation
// gate and quota lock, so host callbacks can call back into the manager.
func (m *Manager) publishUsage() {
	if m.deps.hooks.UsageUpdated == nil {
		return
	}
	q := &m.quota
	q.mu.Lock()
	u := copyUsage(q.current.Usage)
	if u.Source == "" {
		u = usage.Unknown(m.cfg.kind)
	}
	changed := !q.havePublished || !reflect.DeepEqual(u, q.published)
	if changed {
		q.published = copyUsage(u)
		q.havePublished = true
	}
	q.mu.Unlock()
	if changed {
		m.deps.hooks.UsageUpdated(u)
	}
}

// Usage reads current account-scoped quotas, starting the connection lazily.
func (m *Manager) Usage(ctx context.Context) (protocol.Usage, error) {
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return protocol.Usage{}, err
	}
	defer release()
	st := m.status(ctx, true)
	m.remember(st, false)
	m.quota.mu.Lock()
	u := copyUsage(m.quota.current.Usage)
	m.quota.mu.Unlock()
	if u.Source == "" {
		u = usage.Unknown(m.cfg.kind)
	}
	m.signal()
	if err := ctx.Err(); err != nil {
		return protocol.Usage{}, err
	}
	return u, nil
}

func (m *Manager) quotaRuntime() (func() usage.QuotaContext, func() func()) {
	q := &m.quota
	q.mu.Lock()
	generation, epoch := q.generation, q.epoch
	q.mu.Unlock()
	current := func() usage.QuotaContext {
		q.mu.Lock()
		defer q.mu.Unlock()
		if !q.ready || q.generation != generation || q.epoch != epoch || q.dirty {
			return usage.QuotaContext{Usage: usage.Unknown(m.cfg.kind)}
		}
		v := q.current
		v.Usage = copyUsage(v.Usage)
		return v
	}
	active := func() func() {
		q.mu.Lock()
		// A temporary account read failure revokes measurements, not the
		// running process. Keep its turns counted until the principal retires.
		valid := q.generation == generation && q.epoch == epoch
		if valid {
			q.active++
		}
		q.mu.Unlock()
		if valid {
			m.signal()
		}
		var once sync.Once
		return func() {
			once.Do(func() {
				q.mu.Lock()
				if valid && q.generation == generation && q.epoch == epoch {
					q.active--
				}
				q.mu.Unlock()
			})
		}
	}
	return current, active
}

func (m *Manager) signalUsage() {
	q := &m.quota
	q.mu.Lock()
	pending := m.deps.hooks.UsageUpdated != nil && (!q.havePublished || !reflect.DeepEqual(q.current.Usage, q.published))
	q.mu.Unlock()
	if pending {
		m.signal()
	}
}

// Account notifications invalidate observations immediately, before the owned
// worker validates the new authoritative principal under its operation gate.
func (m *Manager) invalidateQuotaObservation() {
	q := &m.quota
	q.mu.Lock()
	q.revision++
	q.dirty = true
	q.current = usage.QuotaContext{Usage: usage.Unknown(m.cfg.kind)}
	q.mu.Unlock()
}
