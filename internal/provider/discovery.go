// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/discovery"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

var _ plugin.Discoverer = (*provider)(nil)

func userDiscoveryRoots() (discovery.Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return discovery.Roots{}, discovery.ErrSource
	}
	codex := os.Getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}
	return discovery.Roots{Home: home, CodexHome: codex}, nil
}

// Discover implements only explicit, read-only import discovery. Its filesystem
// access is still constrained by the actual host sandbox; no manifest grants
// are widened to expose personal skill roots to the shared vendor process.
func (p *provider) Discover(ctx context.Context, params protocol.DiscoverParams) (protocol.DiscoverResult, error) {
	p.mu.Lock()
	if p.closed || p.manager == nil {
		p.mu.Unlock()
		return protocol.DiscoverResult{}, unavailable()
	}
	resolve, work, dataDir := p.discoverRoots, p.work, p.dataDir
	var logger *slog.Logger
	if p.host != nil {
		logger = p.host.Logger()
	}
	p.setups.Add(1)
	p.mu.Unlock()
	defer p.setups.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(work, cancel)
	defer func() { stop(); cancel() }()
	if logger != nil {
		logger.Debug("discovery started", "kinds", len(params.Kinds), "projects", len(params.Projects))
	}
	if len(params.Kinds) == 0 {
		return discovery.Read(ctx, discovery.Roots{}, params)
	}
	if resolve == nil {
		resolve = userDiscoveryRoots
	}
	roots, err := resolve()
	if err == nil {
		// A process environment override must not turn read-only user discovery
		// into a scan of this connection's private login state.
		privateRoot, sourceRoot := dataDir, roots.CodexHome
		if actual, e := filepath.EvalSymlinks(privateRoot); e == nil {
			privateRoot = actual
		}
		if actual, e := filepath.EvalSymlinks(sourceRoot); e == nil {
			sourceRoot = actual
		}
		rel, relErr := filepath.Rel(privateRoot, sourceRoot)
		if relErr == nil && (rel == "." || filepath.IsLocal(rel)) {
			err = discovery.ErrParams
		}
	}
	var result protocol.DiscoverResult
	if err == nil {
		result, err = discovery.Read(ctx, roots, params)
	}
	if err != nil {
		if logger != nil {
			logger.Debug("discovery stopped", "reason", "source_unavailable")
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return protocol.DiscoverResult{}, err
		}
		return protocol.DiscoverResult{}, protocol.NewError(protocol.ErrProviderError, "Discovery is unavailable for these sources or parameters", map[string]any{"vendorCode": "discovery_unavailable"})
	}
	if logger != nil {
		logger.Debug("discovery completed", "mcpServers", len(result.McpServers), "skills", len(result.Skills))
	}
	return result, nil
}
