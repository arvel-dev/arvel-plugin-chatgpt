//go:build e2e

// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/spike"
)

func live(t *testing.T, scenario, home string) {
	t.Helper()
	if os.Getenv("ARVEL_E2E") != "1" {
		t.Fatal("live probes require ARVEL_E2E=1 as well as -tags e2e")
	}
	schema, err := filepath.Abs(filepath.Join("..", "testdata", "schema", "0.164.0"))
	if err != nil {
		t.Fatal(err)
	}
	timeout := 45 * time.Second
	if scenario == "subagent-complete" {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+15*time.Second)
	defer cancel()
	if err := spike.Run(ctx, spike.RunOptions{Mode: "record", Scenario: scenario, Home: home, Schema: schema, Out: filepath.Join(t.TempDir(), "recording"), Timeout: timeout}); err != nil {
		t.Fatal(err)
	}
}

func TestProtocol_Handshake(t *testing.T) { live(t, "handshake", "private") }
func TestProtocol_Text(t *testing.T)      { live(t, "text", "shared") }
func TestProtocol_Interrupt(t *testing.T) { live(t, "interrupt", "shared") }
