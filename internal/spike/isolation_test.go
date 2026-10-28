// SPDX-License-Identifier: Apache-2.0
package spike

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/procx"
)

func TestIsolationProbe_RefusesSharedHomeBeforeDiscovery(t *testing.T) {
	t.Setenv("ARVEL_E2E", "1")
	for _, scenario := range Scenarios() {
		if !strings.HasPrefix(scenario, "config-isolation") {
			continue
		}
		out := filepath.Join(t.TempDir(), "uncreated")
		err := Run(context.Background(), RunOptions{Mode: "record", Scenario: scenario, Home: "shared", Codex: "nonexistent", Out: out})
		if err == nil || !strings.Contains(err.Error(), "private home") {
			t.Fatalf("unsafe probe accepted: %v", err)
		}
		if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created output for rejected probe")
		}
	}
}

func TestIsolationMarker_OnlyCreatesOwnedSentinel(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"isolation-relative.sentinel", filepath.Join(dir, "wrong.txt")} {
		if IsolationMarker(path) == nil {
			t.Fatal("accepted invalid marker")
		}
	}
	path := filepath.Join(dir, "isolation-owned.sentinel")
	if err := IsolationMarker(path); err != nil {
		t.Fatal(err)
	}
	if IsolationMarker(path) == nil {
		t.Fatal("overwrote existing sentinel")
	}
}

func TestIsolationProbe_NeverCertifiesStartupOnlyOrRacingConfiguration(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b := testBundle(t)
	for _, tc := range []struct {
		mode, scenario string
		race           bool
	}{
		{"isolation-startup", "config-isolation", false},
		{"isolation-race", "config-isolation-race", true},
		{"isolation-untrusted-user", "config-isolation-untrusted-user", true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cwd := t.TempDir()
			home := filepath.Join(cwd, "codex-home")
			if err := os.Mkdir(home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := prepareIsolation(home, cwd, binary); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var entries []Entry
			err := record(ctx, procx.Spec{Path: binary, Args: []string{"-test.run=^TestRecorderHelper$"}, Dir: cwd, Env: procx.Env().Set("SPIKE_TEST_MODE", tc.mode).Build()}, b, tc.scenario, &entries)
			if err == nil {
				t.Fatal("incomplete/unsafe evidence reported success")
			}
			if tc.race && !errors.Is(err, errIsolationRace) {
				t.Fatalf("missing explicit race finding: %v", err)
			}
			if !tc.race && !strings.Contains(err.Error(), "startup-only") {
				t.Fatalf("wrong incomplete outcome: %v", err)
			}
			for _, entry := range entries {
				if strings.Contains(string(entry.Message), cwd) || strings.Contains(string(entry.Message), binary) {
					t.Fatal("owned scratch/binary path persisted without scrubbing")
				}
			}
		})
	}
}
