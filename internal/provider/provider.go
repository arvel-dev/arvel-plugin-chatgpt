// SPDX-License-Identifier: Apache-2.0

// Package provider implements the SDK lifecycle for the ChatGPT provider.
package provider

import (
	"context"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const incomplete = "Provider bootstrap is incomplete"

type provider struct{}

var _ plugin.Provider = (*provider)(nil)

// New returns a provider without starting Codex or accessing the network.
func New() plugin.Provider { return &provider{} }

func (*provider) Initialize(context.Context, protocol.InitMeta) (plugin.Features, error) {
	return plugin.Features{}, nil
}

func (*provider) Status(context.Context) (protocol.Status, error) {
	return protocol.Status{State: protocol.StateError, Message: incomplete}, nil
}

func (*provider) Models(context.Context, bool) (protocol.ModelsResult, error) {
	return protocol.ModelsResult{}, unavailable()
}

func (*provider) Usage(context.Context) (protocol.Usage, error) {
	return protocol.Usage{Source: protocol.UsageSourceUnknown, Status: protocol.UsageStatusOK}, nil
}

func (*provider) Action(context.Context, string) (protocol.ActionResult, error) {
	return protocol.ActionResult{}, unavailable()
}

func (*provider) NewSession(context.Context, plugin.SessionParams) (plugin.Session, error) {
	return nil, unavailable()
}

func (*provider) LoadSession(context.Context, plugin.LoadParams) (plugin.Session, error) {
	return nil, unavailable()
}

func (*provider) Shutdown(context.Context) error { return nil }

func unavailable() error {
	return protocol.NewError(protocol.ErrProviderError, incomplete, nil)
}
