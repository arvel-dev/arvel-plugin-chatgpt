// SPDX-License-Identifier: Apache-2.0

// Package provider implements the SDK lifecycle for the ChatGPT provider.
package provider

import (
	"context"
	"sync"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const incomplete = "Agent sessions are not implemented yet"

type provider struct {
	mu      sync.RWMutex
	host    plugin.Host
	manager *connection.Manager
	closed  bool
}

var _ plugin.Provider = (*provider)(nil)
var _ plugin.HostAware = (*provider)(nil)

// New returns a provider without starting Codex or accessing the network.
func New() plugin.Provider { return &provider{} }

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
	if p.host != nil {
		m, err = connection.New(meta, p.host.StatusChanged, p.host.Logger())
	} else {
		m, err = connection.New(meta, nil, nil)
	}
	if err != nil {
		return plugin.Features{}, err
	}
	p.manager = m
	return plugin.Features{}, nil
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

func (*provider) Usage(context.Context) (protocol.Usage, error) {
	return protocol.Usage{Source: protocol.UsageSourceUnknown, Status: protocol.UsageStatusOK}, nil
}

func (p *provider) Action(ctx context.Context, id string) (protocol.ActionResult, error) {
	m, err := p.connection()
	if err != nil {
		return protocol.ActionResult{}, err
	}
	return m.Action(ctx, id)
}

func (*provider) NewSession(context.Context, plugin.SessionParams) (plugin.Session, error) {
	return nil, unavailable()
}

func (*provider) LoadSession(context.Context, plugin.LoadParams) (plugin.Session, error) {
	return nil, unavailable()
}

func (p *provider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.closed = true
	m := p.manager
	p.mu.Unlock()
	if m == nil {
		return nil
	}
	return m.Close(ctx)
}

func unavailable() error {
	return protocol.NewError(protocol.ErrProviderError, incomplete, nil)
}
