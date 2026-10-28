// SPDX-License-Identifier: Apache-2.0

// Package provider implements the SDK lifecycle for the ChatGPT provider.
package provider

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/discovery"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/session"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/skillset"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const incomplete = "Provider is not initialized or is shutting down"

type provider struct {
	mu            sync.RWMutex
	host          plugin.Host
	manager       *connection.Manager
	closed        bool
	router        *session.Router
	sessions      map[string]*session.Session
	creating      int
	work          context.Context
	stop          context.CancelFunc
	workers       sync.WaitGroup
	setups        sync.WaitGroup
	discoverRoots func() (discovery.Roots, error)
	dataDir       string
	skills        *skillset.Manager
}

var _ plugin.Provider = (*provider)(nil)
var _ plugin.HostAware = (*provider)(nil)

// New returns a provider without starting Codex or accessing the network.
func New() plugin.Provider { return &provider{discoverRoots: userDiscoveryRoots} }

// SetHost receives the SDK notification/logging adapter before Initialize.
func (p *provider) SetHost(host plugin.Host) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.host = host
}

func (p *provider) Initialize(_ context.Context, meta protocol.InitMeta) (plugin.Features, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.manager != nil {
		return plugin.Features{}, unavailable()
	}
	var m *connection.Manager
	var err error
	router := session.NewRouter()
	hooks := connection.Hooks{ForGeneration: router.ForGeneration, Stopped: router.Stopped}
	if p.host != nil {
		hooks.UsageUpdated = p.host.UsageUpdated
		m, err = connection.NewWithHooks(meta, p.host.StatusChanged, p.host.Logger(), hooks)
	} else {
		m, err = connection.NewWithHooks(meta, nil, nil, hooks)
	}
	if err != nil {
		return plugin.Features{}, err
	}
	p.manager = m
	p.skills = skillset.New(meta.DataDir)
	p.dataDir = meta.DataDir
	p.router = router
	p.sessions = make(map[string]*session.Session)
	p.work, p.stop = context.WithCancel(context.Background())
	p.workers.Go(func() {
		for {
			generation, err := router.NextAbort(p.work)
			if err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(p.work, 15*time.Second)
			_ = m.Abort(ctx, generation)
			cancel()
		}
	})
	return plugin.Features{LoadSession: true, Subagents: true, PromptImage: true, MCPHTTP: true, MaxParallelSessions: 128, Skills: &protocol.SkillsInfo{ProjectDirs: []string{".agents/skills"}, Spec: "agentskills/1"}, InstructionFiles: []string{"AGENTS.md"}}, nil
}

func (p *provider) connection() (*connection.Manager, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.manager == nil || p.closed {
		return nil, unavailable()
	}
	return p.manager, nil
}

func (p *provider) Status(ctx context.Context) (protocol.Status, error) {
	m, err := p.connection()
	if err != nil {
		return protocol.Status{}, err
	}
	return m.Status(ctx)
}

func (p *provider) Models(ctx context.Context, refresh bool) (protocol.ModelsResult, error) {
	m, err := p.connection()
	if err != nil {
		return protocol.ModelsResult{}, err
	}
	return m.Models(ctx, refresh)
}

func (p *provider) Usage(ctx context.Context) (protocol.Usage, error) {
	m, err := p.connection()
	if err != nil {
		return protocol.Usage{}, err
	}
	return m.Usage(ctx)
}

func (p *provider) Action(ctx context.Context, id string) (protocol.ActionResult, error) {
	m, err := p.connection()
	if err != nil {
		return protocol.ActionResult{}, err
	}
	return m.Action(ctx, id)
}

func (p *provider) NewSession(ctx context.Context, params plugin.SessionParams) (plugin.Session, error) {
	return p.openSession(ctx, params, "")
}

func (p *provider) LoadSession(ctx context.Context, params plugin.LoadParams) (plugin.Session, error) {
	if params.SessionID == "" {
		return nil, protocol.NewError(protocol.ErrProviderError, "Session identity is empty", map[string]any{"vendorCode": "session_error"})
	}
	return p.openSession(ctx, params.SessionParams, params.SessionID)
}

func (p *provider) openSession(ctx context.Context, params plugin.SessionParams, id string) (plugin.Session, error) {
	p.mu.Lock()
	if p.closed || p.manager == nil {
		p.mu.Unlock()
		return nil, unavailable()
	}
	if p.sessions[id] == nil && len(p.sessions)+p.creating >= 128 {
		p.mu.Unlock()
		return nil, protocol.NewError(protocol.ErrProviderError, "Session recovery capacity reached; reconnect this provider to start more sessions", map[string]any{"vendorCode": "session_capacity"})
	}
	m, router, work, skills := p.manager, p.router, p.work, p.skills
	previous := p.sessions[id]
	var logger *slog.Logger
	if p.host != nil {
		logger = p.host.Logger()
	}
	p.creating++
	p.setups.Add(1)
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.creating--; p.mu.Unlock(); p.setups.Done() }()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(work, cancel)
	defer func() { stop(); cancel() }()
	var created *session.Session
	var err error
	if id == "" {
		created, err = session.New(ctx, m, router, params, logger, skills)
	} else {
		var saved *session.Snapshot
		if previous != nil {
			snapshot := previous.Snapshot()
			saved = &snapshot
		}
		created, err = session.Load(ctx, m, router, plugin.LoadParams{SessionID: id, SessionParams: params}, saved, logger, skills)
	}
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = created.Close(cleanup)
		return nil, unavailable()
	}
	p.sessions[created.ID()] = created
	p.mu.Unlock()
	return created, nil
}

func (p *provider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	m := p.manager
	stop := p.stop
	sessions := make([]*session.Session, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.mu.Unlock()
	if m == nil {
		return nil
	}
	if stop != nil {
		stop()
	}
	for _, s := range sessions {
		s.Cancel()
	}
	var errs []error
	for _, s := range sessions {
		if err := s.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	p.workers.Wait()
	if err := m.Close(ctx); err != nil {
		errs = append(errs, err)
		_ = m.Close(context.Background()) // Join the owner before removing live copies.
	}
	p.setups.Wait()
	if p.skills != nil {
		p.skills.Close()
	}
	return errors.Join(errs...)
}

func unavailable() error {
	return protocol.NewError(protocol.ErrProviderError, incomplete, map[string]any{"vendorCode": "provider_unavailable"})
}
