// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/usage"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Hooks installs semantic routes before a process starts. Callbacks only enqueue
// bounded work; they must not wait for host approvals or acquire Manager's gate.
type Hooks struct {
	ForGeneration func(uint64) appserver.PeerOptions
	Stopped       func(uint64)
	UsageUpdated  func(protocol.Usage)
}

// Runtime borrows one manager-owned process. A caller never closes Client itself.
type Runtime struct {
	Client     Client
	Generation uint64
	Home       string
	Kind       string
	Quota      func() usage.QuotaContext
	ActiveTurn func() func()
}

// NewWithHooks composes account handling with per-generation semantic routing.
func NewWithHooks(meta protocol.InitMeta, changed func(protocol.Status), logger *slog.Logger, hooks Hooks) (*Manager, error) {
	return newManager(meta, changed, logger, dependencies{hooks: hooks})
}

// Acquire requires an authenticated private home and releases the operation gate
// before returning; turn and permission waits must use their own contexts.
func (m *Manager) Acquire(ctx context.Context) (Runtime, error) {
	if m.cfg.home == "" {
		return Runtime{}, unavailable("Agent sessions require a separate Arvel sign-in; reconnect this provider to sign in")
	}
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return Runtime{}, err
	}
	defer release()
	st := m.status(ctx, true)
	m.remember(st, false)
	if st.State != protocol.StateReady {
		if st.State != protocol.StateNeedsLogin {
			return Runtime{}, unavailable(st.Message)
		}
		return Runtime{}, protocol.NewError(protocol.ErrAuthRequired, "Sign in to Arvel or configure an API key before starting a session", nil)
	}
	if err := ctx.Err(); err != nil {
		return Runtime{}, err
	}
	m.borrowed = true
	quota, active := m.quotaRuntime()
	return Runtime{Client: m.client, Generation: m.generation, Home: m.cfg.home, Kind: m.cfg.kind, Quota: quota, ActiveTurn: active}, nil
}

// Abort stops only the specified generation. Retiring process cleanup completes
// before another process may start, even if this caller stops waiting.
func (m *Manager) Abort(ctx context.Context, generation uint64) error {
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return err
	}
	if m.client == nil || m.generation != generation {
		release()
		return nil
	}
	client := m.client
	m.retiring = client
	m.reset()
	m.remember(m.state(protocol.StateError, "Codex app-server stopped"), true)
	release()
	return client.Close(ctx)
}

func (m *Manager) stopGeneration() {
	if m.generation == 0 || m.stoppedGeneration >= m.generation {
		return
	}
	m.stoppedGeneration = m.generation
	if m.deps.hooks.Stopped != nil {
		m.deps.hooks.Stopped(m.generation)
	}
}

func (m *Manager) peerOptions() appserver.PeerOptions {
	opts := appserver.PeerOptions{}
	if m.deps.hooks.ForGeneration != nil {
		opts = m.deps.hooks.ForGeneration(m.generation)
	}
	opts.Redactor = m.redactor
	opts.Requests = maps.Clone(opts.Requests)
	opts.Notifications = maps.Clone(opts.Notifications)
	if opts.Notifications == nil {
		opts.Notifications = map[string]appserver.Notification{}
	}
	generation := m.generation
	opts.Notifications["account/rateLimits/updated"] = func(_ context.Context, _ json.RawMessage) { m.quotaEvent(generation) }
	for method, account := range map[string]appserver.Notification{"account/updated": m.accountEvent, "account/login/completed": m.loginEvent} {
		semantic := opts.Notifications[method]
		opts.Notifications[method] = func(ctx context.Context, raw json.RawMessage) {
			account(ctx, raw)
			if semantic != nil {
				semantic(ctx, raw)
			}
		}
	}
	return opts
}
