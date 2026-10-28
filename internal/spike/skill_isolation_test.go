// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillIsolation_RefusesOrdinaryHomeBeforeStartingCLI(t *testing.T) {
	t.Setenv("ARVEL_E2E", "1")
	t.Setenv("ARVEL_DISPOSABLE_SKILL_HOME", "")
	for _, home := range []string{"private", "shared"} {
		out := filepath.Join(t.TempDir(), "uncreated")
		err := Run(context.Background(), RunOptions{Mode: "record", Scenario: "skills-isolation", Home: home, Codex: "nonexistent", Out: out})
		if err == nil || (!strings.Contains(err.Error(), "disposable") && !strings.Contains(err.Error(), "private home")) {
			t.Fatalf("ordinary home reached binary discovery: %v", err)
		}
		if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected probe created output")
		}
	}
}

func TestOwnedScenarios_RefuseOrdinaryHomeBeforeBinaryOrOutput(t *testing.T) {
	t.Setenv("ARVEL_E2E", "1")
	t.Setenv("ARVEL_DISPOSABLE_SKILL_HOME", "")
	for _, scenario := range []string{"skills-managed", "image-inline", "mcp-native-stdio", "subagent-owned", "subagent-owned-override"} {
		for _, home := range []string{"private", "shared"} {
			out := filepath.Join(t.TempDir(), "uncreated")
			err := Run(t.Context(), RunOptions{Mode: "record", Scenario: scenario, Home: home, Codex: "nonexistent", Out: out})
			if err == nil || (!strings.Contains(err.Error(), "disposable") && !strings.Contains(err.Error(), "private home")) {
				t.Fatalf("%s/%s: %v", scenario, home, err)
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected probe created output")
			}
		}
	}
}

func TestSkillIsolation_NeverOverwritesAnExistingSkill(t *testing.T) {
	root := t.TempDir()
	path, err := writeIsolationSkill(root, "fixture", "original body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeIsolationSkill(root, "fixture", "replacement body"); err == nil {
		t.Fatal("overwrote existing skill")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "original body") || strings.Contains(string(data), "replacement body") {
		t.Fatal("existing skill was changed")
	}
}

func TestSkillIsolation_NegativeFixtureRetainsFourCompletedCounterexamples(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "fixtures", "skills-isolation-linux")
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Completed      bool     `json:"completed"`
		SecurityPassed bool     `json:"securityGatePassed"`
		BodyObserved   bool     `json:"unassignedSkillBodyObserved"`
		Observations   []string `json:"observations"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Completed || meta.SecurityPassed || !meta.BodyObserved || len(meta.Observations) != 4 {
		t.Fatal("counterexample was mislabeled as successful isolation or lost coverage")
	}
	data, err = os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	completed, selected, resumed := 0, false, false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var entry Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		var message wireMessage
		if err := json.Unmarshal(entry.Message, &message); err != nil {
			t.Fatal(err)
		}
		switch message.Method {
		case "turn/start":
			if strings.Contains(string(message.Params), "UNASSIGNED_BODY_SENTINEL") {
				t.Fatal("oracle body marker was supplied in the prompt instead of loaded from a skill")
			}
			selected = selected || strings.Contains(string(message.Params), `"type":"skill"`)
		case "thread/resume":
			resumed = true
		case "turn/completed":
			if err := verifyTurn(message.Params, "text", ""); err != nil {
				t.Fatal(err)
			}
			completed++
		}
	}
	if completed != 4 || !selected || !resumed || !strings.Contains(string(data), "UNASSIGNED_SKILL_BODY_OBSERVED") {
		t.Fatal("recording lost completed turns, structured selection, resume or the body observation")
	}
}
