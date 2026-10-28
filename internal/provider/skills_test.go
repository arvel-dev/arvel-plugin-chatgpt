// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func TestProvider_SDKManagedSkillAliasIdleReloadAndCrash(t *testing.T) {
	source := t.TempDir()
	original := "---\nname: original-name\ndescription: Owned test source.\n---\nOWNED_SKILL_BODY\n"
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	clock := &wireClock{now: time.Now()}
	h := newSDKHost(t, nil, plugin.WithClock(clock), plugin.WithIdleTimeout(time.Second))
	if h.skills == nil || h.skills.Spec != "agentskills/1" || len(h.skills.ProjectDirs) != 1 || h.skills.ProjectDirs[0] != ".agents/skills" {
		t.Fatal("managed skills capability absent on SDK wire")
	}
	collision := filepath.Join(h.cwd, ".agents", "skills", "host-name")
	if err := os.MkdirAll(collision, 0700); err != nil {
		t.Fatal(err)
	}
	userSkill := "---\nname: host-name\ndescription: External test skill.\n---\nEXTERNAL_BODY\n"
	if err := os.WriteFile(filepath.Join(collision, "SKILL.md"), []byte(userSkill), 0600); err != nil {
		t.Fatal(err)
	}
	id := h.newSession(t, protocol.SessionMeta{Skills: []protocol.SkillRef{{Name: "host-name", Path: source, Invocation: protocol.SkillInvocationManual}}})
	for _, stage := range []string{"first", "idle", "crash"} {
		if stage == "idle" {
			clock.advance(2 * time.Second)
		}
		if stage == "crash" {
			if _, err := h.prompt(id, "crash"); err == nil {
				t.Fatal("synthetic crash succeeded")
			}
		}
		if _, err := h.prompt(id, "$host-name expect-skill"); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	current, _ := os.ReadFile(filepath.Join(source, "SKILL.md"))
	if string(current) != original {
		t.Fatal("external source changed")
	}
	current, _ = os.ReadFile(filepath.Join(collision, "SKILL.md"))
	if string(current) != userSkill {
		t.Fatal("external collision changed")
	}
}
