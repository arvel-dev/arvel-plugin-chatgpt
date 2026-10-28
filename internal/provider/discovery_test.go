// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/discovery"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/testkit/mockhost"
)

func TestDiscover_SDKWireSignedOutWithoutStartingCodexOrResolvingSecrets(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	const canary = "discovery-literal-password-canary"
	config := "[mcp_servers.fixture]\ncommand=\"fixture-server\"\nargs=[\"--stdio\"]\n[mcp_servers.fixture.env]\nPASSWORD=\"" + canary + "\"\n"
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	resolves := 0
	p := &provider{discoverRoots: func() (discovery.Roots, error) {
		resolves++
		return discovery.Roots{Home: home, CodexHome: filepath.Join(home, ".codex")}, nil
	}}
	h := mockhost.InProcess(t, p)
	if _, err := h.Initialize(protocol.InitMeta{PluginAPI: protocol.PluginAPI, DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: "chatgpt", Config: map[string]any{"codexPath": filepath.Join(t.TempDir(), "does-not-exist")}}}); err != nil {
		t.Fatal(err)
	}
	if resolves != 0 {
		t.Fatal("initialize scanned discovery roots")
	}
	var result protocol.DiscoverResult
	if err := h.Call(context.Background(), "_arvel/discover", protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}}, &result); err != nil {
		t.Fatal(err)
	}
	if resolves != 1 || len(result.McpServers) != 1 || !result.McpServers[0].Env["PASSWORD"].Secret || result.Skills == nil {
		t.Fatalf("projection: %+v", result)
	}
	if strings.Contains(h.Stdout()+h.Stderr(), canary) || !strings.Contains(h.Stdout(), `"discover"`) {
		t.Fatal("secret leaked or feature not announced")
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != config {
		t.Fatal("discovery changed configuration")
	}
	if _, err := os.Stat(filepath.Join(p.dataDir, "codex-home")); !os.IsNotExist(err) {
		t.Fatal("discovery started private account/process setup")
	}
	h.Shutdown()
	if _, err := p.Discover(context.Background(), protocol.DiscoverParams{}); err == nil {
		t.Fatal("discovery survived shutdown")
	}
}

func TestDiscover_RejectsPrivateStateThroughADataDirectoryAlias(t *testing.T) {
	home := t.TempDir()
	actual := filepath.Join(home, "private-data")
	codex := filepath.Join(actual, "codex-home")
	if err := os.MkdirAll(codex, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "config.toml"), []byte("[mcp_servers.private]\ncommand=\"private-server\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "data-alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Skip("symlink creation requires platform privilege")
	}
	p := &provider{discoverRoots: func() (discovery.Roots, error) { return discovery.Roots{Home: home, CodexHome: codex}, nil }}
	if _, err := p.Initialize(context.Background(), protocol.InitMeta{DataDir: alias, Connection: protocol.ConnectionInfo{Kind: "chatgpt"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if _, err := p.Discover(context.Background(), protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}}); err == nil {
		t.Fatal("private state became a user discovery source through a data-directory alias")
	}
}
