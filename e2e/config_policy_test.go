//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import "testing"

func TestConfigPolicy(t *testing.T) {
	for _, scenario := range []string{"command-approval", "edit-read-only", "edit-workspace", "edit-accept", "edit-session", "edit-cancel", "network-deny", "network-allow", "instructions", "mcp"} {
		t.Run(scenario, func(t *testing.T) { live(t, scenario, "shared") })
	}
}
