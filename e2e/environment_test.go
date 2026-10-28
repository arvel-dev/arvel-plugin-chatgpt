//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import "testing"

func TestEnvironment(t *testing.T) {
	for _, scenario := range []string{"env-stripped", "env-openai", "env-codex", "private-api-key"} {
		t.Run(scenario, func(t *testing.T) { live(t, scenario, "private") })
	}
}
