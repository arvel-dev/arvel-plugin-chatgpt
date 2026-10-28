//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import "testing"

func TestSkills(t *testing.T) {
	t.Run("private-discovery", func(t *testing.T) { live(t, "skills", "private") })
	t.Run("explicit-invocation", func(t *testing.T) { live(t, "skills-selected", "shared") })
}
