// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) accountEvent(_ context.Context, _ json.RawMessage) {
	m.revision.Add(1)
	m.invalidateQuotaObservation()
	m.signal()
}

func (m *Manager) loginEvent(_ context.Context, raw json.RawMessage) {
	var event schema.LoginCompleted
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	if event.LoginID != nil && *event.LoginID != "" {
		m.eventsMu.Lock()
		if len(m.completed) < 128 {
			m.completed[*event.LoginID] = event.Success
		}
		m.eventsMu.Unlock()
	}
	m.revision.Add(1)
	m.invalidateQuotaObservation()
	m.signal()
}

func (m *Manager) drainEvents() {
	m.eventsMu.Lock()
	if success, exists := m.completed[m.pending]; m.pending != "" && exists {
		m.pending = ""
		m.loginFailed = !success
		m.log.Debug("[FIX:login-completion] matched sign-in completion", "success", success)
	}
	clear(m.completed)
	m.eventsMu.Unlock()
}

func (m *Manager) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.deps.poll)
	defer ticker.Stop()
	var death <-chan struct{}
	for {
		select {
		case <-m.ctx.Done():
			// Cancellation releases the active operation; no new operations can enter.
			<-m.gate
			if m.client != nil {
				_ = m.client.Close(context.Background())
				m.reset()
			}
			if m.retiring != nil {
				_ = m.retiring.Close(context.Background())
				m.retiring = nil
			}
			m.gate <- struct{}{}
			return
		case <-death:
			ctx, release, err := m.operation(m.ctx)
			if err == nil {
				_ = ctx
				if m.client != nil {
					select {
					case <-m.client.Done():
						m.reset()
						m.remember(m.state(protocol.StateError, "Codex app-server stopped"), true)
					default:
					}
				}
				release()
			}
			death = nil
		case <-m.wake:
			death = m.refresh()
			m.publishUsage()
		case <-ticker.C:
			death = m.refresh()
			m.publishUsage()
		}
	}
}

func (m *Manager) refresh() <-chan struct{} {
	ctx, release, err := m.operation(m.ctx)
	if err != nil {
		return nil
	}
	defer release()
	m.drainEvents()
	if m.client == nil {
		return nil
	}
	done := m.client.Done()
	select {
	case <-done:
		m.reset()
		m.remember(m.state(protocol.StateError, "Codex app-server stopped"), true)
		return nil
	default:
	}
	m.remember(m.status(ctx, false), true)
	return done
}

// loginStatus ensures completion and retry are visible through SDK status deduplication.
func (m *Manager) loginStatus(st protocol.Status) protocol.Status {
	if m.pending != "" {
		st.Message = "Complete ChatGPT sign-in"
	} else if m.loginFailed {
		st.Message = "ChatGPT sign-in failed or was cancelled; try again"
	}
	return st
}
