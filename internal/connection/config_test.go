// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/protocol"
)

func TestConfig_IsolationMatrix(t *testing.T) {
	for _, kind := range []string{"chatgpt", "api_key"} {
		for _, separate := range []bool{false, true} {
			for _, remote := range []bool{false, true} {
				t.Run(kind+string(rune('0'+boolInt(separate)))+string(rune('0'+boolInt(remote))), func(t *testing.T) {
					meta := protocol.InitMeta{DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: kind, Config: map[string]any{"separateLogin": separate}}, Platform: protocol.PlatformInfo{Remote: remote}}
					c, err := parseConfig(meta)
					if kind == "api_key" && separate {
						if err == nil {
							t.Fatal("accepted separate API config")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if (c.home != "") != (kind == "api_key" || separate) || c.remote != remote {
						t.Fatal("wrong home or remote mode")
					}
					t.Setenv("OPENAI_API_KEY", "sk-env-canary")
					t.Setenv("CODEX_HOME", "/untrusted")
					t.Setenv("CODEX_ACCESS_TOKEN", "token")
					t.Setenv("GH_TOKEN", "secret")
					c.proxy = "http://127.0.0.1:1234"
					env := strings.Join(c.environment(), "\n")
					for _, bad := range []string{"sk-env-canary", "/untrusted", "CODEX_ACCESS_TOKEN=", "GH_TOKEN="} {
						if strings.Contains(env, bad) {
							t.Fatal("ambient auth leaked")
						}
					}
					// Windows environment names are case-insensitive; SDK Proxy keeps
					// one spelling when uppercase/lowercase twins address the same key.
					values := map[string]string{}
					for _, entry := range c.environment() {
						key, value, _ := strings.Cut(entry, "=")
						values[strings.ToUpper(key)] = value
					}
					for _, key := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY"} {
						if values[key] != c.proxy {
							t.Errorf("missing proxy: %s", key)
						}
					}
					if values["RUST_LOG"] != "warn" || values["NO_PROXY"] != "localhost,127.0.0.1" {
						t.Fatal("missing log control or proxy loopback exceptions")
					}
					if err := c.prepare(); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestConfig_InvalidAndDevOverride(t *testing.T) {
	for _, cfg := range []map[string]any{{"codexPath": 12}, {"codexPath": "relative"}, {"separateLogin": "true"}} {
		if _, err := parseConfig(protocol.InitMeta{DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: "chatgpt", Config: cfg}}); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
	meta := protocol.InitMeta{DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: "chatgpt"}}
	path := filepath.Join(t.TempDir(), "fake-codex")
	t.Setenv("ARVEL_CODEX_BIN", path)
	t.Setenv("ARVEL_DEV", "")
	c, err := parseConfig(meta)
	if err != nil || c.path != "" {
		t.Fatal("production honored override")
	}
	t.Setenv("ARVEL_DEV", "1")
	c, err = parseConfig(meta)
	if err != nil || c.path != path {
		t.Fatal("dev ignored override")
	}
	if err := os.WriteFile(path, []byte("synthetic"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if found, err := findBinary(c); err != nil || found != resolved {
		t.Fatalf("explicit path: %s %v", found, err)
	}
	c.path = filepath.Join(t.TempDir(), "missing")
	if _, err := findBinary(c); err == nil {
		t.Fatal("found nonexistent binary")
	}
}

func TestConfig_NewChatGPTConnectionDefaultsToPrivateHome(t *testing.T) {
	dir := t.TempDir()
	c, err := parseConfig(protocol.InitMeta{DataDir: dir, Connection: protocol.ConnectionInfo{Kind: "chatgpt"}})
	if err != nil || !c.separate || c.home != filepath.Join(dir, "codex-home") {
		t.Fatalf("new connection must use its own login: %+v %v", c, err)
	}
}

func TestAccountLabel_IsRecognizableWithoutFullIdentity(t *testing.T) {
	for _, tc := range []struct{ email, want string }{
		{"alice@example.invalid", "a***@example.invalid"},
		{"bob@example.invalid", "b***@example.invalid"},
		{"not-an-email", "ChatGPT account"},
		{"Alice <alice@example.invalid>", "ChatGPT account"},
		{"bad\n@example.invalid", "ChatGPT account"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			if got := maskedEmail(&tc.email); got != tc.want {
				t.Fatalf("label %q want %q", got, tc.want)
			}
		})
	}
}
