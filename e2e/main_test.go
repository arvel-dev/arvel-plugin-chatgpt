//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"os"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/spike"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "mcp-fixture" {
		if err := spike.ServeMCP(context.Background(), os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
