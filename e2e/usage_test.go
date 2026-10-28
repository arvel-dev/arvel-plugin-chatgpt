//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import "testing"

func TestUsage(t *testing.T) { live(t, "limits", "shared") }
