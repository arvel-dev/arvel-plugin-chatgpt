// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// windowsStatus runs under the existing operation gate and retains auth precedence.
// The public readiness RPC may refresh an already registered Core installation;
// this provider never invokes sandbox setup/start or changes native policy.
func (m *Manager) windowsStatus(ctx context.Context, status protocol.Status) protocol.Status {
	if m.deps.goos != "windows" || status.State != protocol.StateReady {
		return status
	}
	var readiness schema.WindowsReadiness
	err := m.client.Call(ctx, "windowsSandbox/readiness", nil, &readiness)
	if err == nil && readiness.Status == "ready" {
		return status
	}
	status.State = protocol.StateDegraded
	switch {
	case err != nil:
		status.Message = "Cannot verify Codex Windows sandbox readiness"
	case readiness.Status == "notConfigured":
		status.Message = "Open Codex on Windows to complete sandbox setup"
	case readiness.Status == "updateRequired":
		status.Message = "Open Codex on Windows to update its sandbox setup"
	default:
		status.Message = "Codex returned unknown Windows sandbox readiness"
	}
	m.log.Debug("Windows sandbox readiness unavailable")
	return status
}
