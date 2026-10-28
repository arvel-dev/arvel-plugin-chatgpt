// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func TestWindowsReadiness_AuthPrecedenceAndRefresh(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			f := &fakeClient{}
			m := testManager(t, "chatgpt", true, f, dependencies{goos: goos}, nil)
			statusState(t, m, protocol.StateNeedsLogin)
			f.mu.Lock()
			for _, method := range f.calls {
				if method == "windowsSandbox/readiness" {
					t.Error("readiness before authentication")
				}
			}
			f.account = &schema.Account{Type: "chatgpt", PlanType: "plus"}
			f.mu.Unlock()
			for _, state := range []string{"ready", "notConfigured", "updateRequired", "unexpected", "ready"} {
				f.mu.Lock()
				f.readiness = state
				f.mu.Unlock()
				want := protocol.StateReady
				if goos == "windows" && state != "ready" {
					want = protocol.StateDegraded
				}
				status := statusState(t, m, want)
				if status.Account == nil {
					t.Fatal("account details lost")
				}
			}
			f.mu.Lock()
			f.readinessError = true
			f.mu.Unlock()
			want := protocol.StateReady
			if goos == "windows" {
				want = protocol.StateDegraded
			}
			status := statusState(t, m, want)
			if strings.Contains(status.Message, "private-readiness-error") {
				t.Fatal("vendor error exposed")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, method := range f.calls {
				if method == "windowsSandbox/setup/start" {
					t.Error("automatic setup attempted")
				}
				if goos != "windows" && method == "windowsSandbox/readiness" {
					t.Error("non-Windows readiness probe")
				}
			}
		})
	}
}
