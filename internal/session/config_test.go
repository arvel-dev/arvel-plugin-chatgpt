// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func configDirs(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	home := filepath.Join(root, "private")
	for _, p := range []string{cwd, home} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return cwd, home
}

func TestConfig_StartResumePreserveHostPolicy(t *testing.T) {
	cwd, home := configDirs(t)
	for _, readOnly := range []bool{false, true} {
		meta := protocol.SessionMeta{Instructions: "Inspect the project.", Language: "Russian", Task: &protocol.TaskMeta{Type: "task", ReadOnly: readOnly}, Permissions: &protocol.Permissions{Edit: protocol.PermissionLevelAllow, Network: protocol.PermissionLevelDeny}}
		cfg, err := buildConfig(plugin.SessionParams{CWD: cwd, Meta: meta}, home)
		if err != nil {
			t.Fatal(err)
		}
		want := "workspace-write"
		if readOnly {
			want = "read-only"
		}
		if cfg.start.Sandbox != want || cfg.start.ApprovalPolicy != "on-request" || cfg.start.Ephemeral || cfg.start.Model != nil {
			t.Fatal("incorrect native baseline")
		}
		if got := *cfg.start.DeveloperInstructions; got != "Inspect the project.\nReply to the user in Russian." {
			t.Fatal(got)
		}
		resume := cfg.resume("thread")
		if resume.ThreadID != "thread" || resume.Sandbox != cfg.start.Sandbox || !reflect.DeepEqual(resume.Config, cfg.start.Config) || resume.DeveloperInstructions != cfg.start.DeveloperInstructions {
			t.Fatal("resume lost host controls")
		}
		if cfg.start.Config["features"].(map[string]any)["plugins"] != false || cfg.start.Config["features"].(map[string]any)["hooks"] != false || len(cfg.start.Config["notify"].([]any)) != 0 {
			t.Fatal("missing execution suppression")
		}
		features := cfg.start.Config["features"].(map[string]any)
		if features["multi_agent"] != false || features["multi_agent_v2"] != false || cfg.start.Config["agents"].(map[string]any)["enabled"] != false {
			t.Fatal("unassigned native delegation enabled")
		}
		if cfg.start.Config["projects"].(map[string]any)[cfg.start.CWD].(map[string]any)["trust_level"] != "untrusted" {
			t.Fatal("project trust not forced")
		}
		if cfg.start.Config["web_search"] != "disabled" {
			t.Fatal("network-denied session retained server-side web search")
		}
	}
}

func TestConfig_RejectsUnsupportedInputsAndPrivateDirectoryOverlap(t *testing.T) {
	cwd, home := configDirs(t)
	for _, tc := range []struct {
		name   string
		params plugin.SessionParams
	}{
		{"cost", plugin.SessionParams{CWD: cwd, Meta: protocol.SessionMeta{Budget: &protocol.Budget{MaxUSD: 1}}}},
		{"relative", plugin.SessionParams{CWD: "relative"}},
		{"private", plugin.SessionParams{CWD: home}},
		{"ancestor", plugin.SessionParams{CWD: filepath.Dir(home)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildConfig(tc.params, home); err == nil {
				t.Fatal("unsupported setup accepted")
			}
		})
	}
	link := filepath.Join(cwd, "linked-private")
	if err := os.Symlink(home, link); err == nil {
		if _, err := buildConfig(plugin.SessionParams{CWD: link}, home); err == nil {
			t.Fatal("symlink overlap accepted")
		}
	}
}

type configCaller struct {
	requirements, config string
	fail                 string
	methods              []string
}

func TestSession_SetupErrorsSatisfySDKContract(t *testing.T) {
	var rpc *protocol.RPCError
	if !errors.As(setupError("Session is unavailable"), &rpc) {
		t.Fatal("untyped setup error")
	}
	data, ok := rpc.Arvel()
	if !ok || data.Validate() != nil {
		t.Fatalf("invalid SDK error: %+v", data)
	}
}

func (c *configCaller) Call(_ context.Context, method string, params, result any) error {
	c.methods = append(c.methods, method)
	if method == c.fail {
		return errors.New("private vendor error")
	}
	if method == "configRequirements/read" {
		return json.Unmarshal([]byte(c.requirements), result)
	}
	if method == "config/read" {
		if params.(schema.ConfigReadParams).CWD != nil {
			return errors.New("project preflight would race")
		}
		return json.Unmarshal([]byte(c.config), result)
	}
	return errors.New("unexpected method")
}

func TestConfig_PreflightFailsClosedBeforeThreadCreation(t *testing.T) {
	for _, tc := range []struct {
		name, requirements, config, fail string
		want                             bool
	}{
		{"owned", `{"requirements":null}`, `{"config":{},"origins":{}}`, "", true},
		{"managed", `{"requirements":{}}`, `{"config":{},"origins":{}}`, "", false},
		{"missing", `{}`, `{"config":{},"origins":{}}`, "", false},
		{"inherited", `{"requirements":null}`, `{"config":{"mcp_servers":{"server":{"command":"private"}}},"origins":{}}`, "", false},
		{"unknown-origin", `{"requirements":null}`, `{"config":{},"origins":{"field":{"name":{"type":"legacyManagedConfigTomlFromFile"}}}}`, "", false},
		{"no-origins", `{"requirements":null}`, `{"config":{}}`, "", false},
		{"custom-provider", `{"requirements":null}`, `{"config":{"model_provider":"custom"},"origins":{}}`, "", false},
		{"rpc-failure", `{"requirements":null}`, `{"config":{},"origins":{}}`, "config/read", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &configCaller{requirements: tc.requirements, config: tc.config, fail: tc.fail}
			err := preflight(testContext(t), c)
			if (err == nil) != tc.want {
				t.Fatalf("preflight: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private vendor error") {
				t.Fatal("vendor error leaked")
			}
			for _, method := range c.methods {
				if strings.HasPrefix(method, "thread/") {
					t.Fatal("thread created before verification")
				}
			}
		})
	}
}

func TestConfig_SubagentsRejectInheritedInstructionReplacements(t *testing.T) {
	for _, key := range []string{"agents.explorer.config_file", "agents.custom.config_file", "agents", "features.multi_agent_v2.subagent_developer_instructions", "features.multi_agent_v2", "features"} {
		t.Run(key, func(t *testing.T) {
			encoded, _ := json.Marshal(map[string]any{"config": map[string]any{}, "origins": map[string]any{key: map[string]any{"name": map[string]string{"type": "user"}}}})
			caller := &configCaller{requirements: `{"requirements":null}`, config: string(encoded)}
			if err := preflight(testContext(t), caller, true); err == nil {
				t.Fatal("possible inherited instruction replacement accepted")
			}
		})
	}
}
