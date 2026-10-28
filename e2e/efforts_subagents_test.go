//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import "testing"

func TestEffortsSubagents(t *testing.T) { live(t, "subagent-complete", "shared") }
