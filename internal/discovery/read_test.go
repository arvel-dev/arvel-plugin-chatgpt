// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/protocol"
)

func fixtureRoots(t *testing.T, config string) Roots {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return Roots{Home: home, CodexHome: filepath.Join(home, ".codex")}
}

func TestRead_ReplaysExistingDiscoveryGoldenThroughSDKProjection(t *testing.T) {
	fixture := filepath.Join("..", "..", "testdata", "fixtures", "discovery")
	config, err := os.ReadFile(filepath.Join(fixture, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	roots := fixtureRoots(t, string(config))
	dir := filepath.Join(roots.Home, ".agents", "skills", "fixture")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	skill, err := os.ReadFile(filepath.Join(fixture, "skill", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), skill, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: protocol.AllDiscoverKinds()})
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.McpServers) != 2 || len(got.Skills) != 1 || got.Skills[0].Name != "arvel-probe" || got.Skills[0].Path != realDir {
		t.Fatalf("catalog: %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(fixture, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Servers []struct {
			Name, Command, URL string
			Args               []string
			Env                map[string]protocol.SecretField
			BearerToken        protocol.SecretField
		}
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	for _, want := range golden.Servers {
		var found *protocol.DiscoveredMcp
		for i := range got.McpServers {
			if got.McpServers[i].Name == want.Name {
				found = &got.McpServers[i]
			}
		}
		if found == nil {
			t.Fatal("missing golden server", want.Name)
		}
		if want.Command != "" && !reflect.DeepEqual(found.Command, append([]string{want.Command}, want.Args...)) {
			t.Fatal("command arguments changed")
		}
		if !reflect.DeepEqual(found.Env, want.Env) && len(found.Env)+len(want.Env) > 0 {
			t.Fatal("env projection differs")
		}
		if want.URL != "" && (found.URL != want.URL || found.Headers["Authorization"] != want.BearerToken) {
			t.Fatal("HTTP bearer projection differs")
		}
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "synthetic-not-a-real-key") || strings.Contains(string(encoded), "Reply SKILL_PONG") {
		t.Fatal("secret or skill body survived projection")
	}
	after, err := os.ReadFile(filepath.Join(roots.CodexHome, "config.toml"))
	if err != nil || string(after) != string(config) {
		t.Fatal("discovery changed source")
	}
}

func TestRead_ProjectsKindsMissingSourcesAndCancellation(t *testing.T) {
	roots := fixtureRoots(t, "")
	project := t.TempDir()
	dir := filepath.Join(project, ".agents", "skills", "owned")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: project-fixture\n---\nPrivate body.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".agents", "skills", ".DS_Store"), []byte("ignored"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		p     protocol.DiscoverParams
		count int
		want  error
	}{
		{"empty", protocol.DiscoverParams{}, 0, nil},
		{"mcp-only", protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}, Projects: []string{project}}, 0, nil},
		{"skills", protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"skills"}, Projects: []string{project, project}}, 1, nil},
		{"unknown", protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"unknown"}}, 0, ErrParams},
		{"relative", protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"skills"}, Projects: []string{"relative"}}, 0, ErrParams},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Read(context.Background(), roots, tc.p)
			if !errors.Is(err, tc.want) || len(got.Skills) != tc.count {
				t.Fatalf("got %+v %v", got, err)
			}
			if err == nil && (got.Skills == nil || got.McpServers == nil) {
				t.Fatal("null arrays")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Read(ctx, roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"skills"}}); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
}

func TestRead_NeverEchoesParserDiagnosticsOrEmbeddedCredentials(t *testing.T) {
	const secret = "tiny-secret-canary"
	for _, config := range []string{
		"[mcp_servers.broken]\ncommand = \"" + secret,
		"[mcp_servers.safe]\ncommand=\"server\"\nargs=[\"--token\",\"" + secret + "\"]",
		"[mcp_servers.safe]\nurl=\"https://user:" + secret + "@example.invalid/mcp\"",
		"[mcp_servers.safe]\nurl=\"https://example.invalid/mcp?token=" + secret + "\"",
		"[mcp_servers.safe]\ncommand=\"server\"\nargs=[\"" + secret + "\"]\n[mcp_servers.safe.env]\nPASSWORD=\"" + secret + "\"",
	} {
		roots := fixtureRoots(t, config)
		got, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}})
		data, _ := json.Marshal(got)
		if strings.Contains(string(data), secret) || (err != nil && strings.Contains(err.Error(), secret)) {
			t.Fatal("secret exposed")
		}
		if len(got.McpServers) != 0 {
			t.Fatal("unsafe definition retained")
		}
	}
}

func TestRead_SymlinksNeverOpenCredentialSources(t *testing.T) {
	roots := fixtureRoots(t, "")
	credential := filepath.Join(roots.CodexHome, "auth.json")
	if err := os.WriteFile(credential, []byte("credential-canary"), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(roots.CodexHome, "config.toml")
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(credential, config); err != nil {
		t.Skip("symlink creation requires platform privilege")
	}
	if _, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}}); !errors.Is(err, ErrSource) {
		t.Fatal("followed credential symlink", err)
	}
	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, ".agents"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(roots.CodexHome, filepath.Join(project, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"skills"}, Projects: []string{project}}); !errors.Is(err, ErrSource) {
		t.Fatal("followed redirected skill root", err)
	}
}

func TestRead_EnforcesFileAndRequestBounds(t *testing.T) {
	roots := fixtureRoots(t, strings.Repeat("x", maxFile+1))
	if _, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}}); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized file accepted", err)
	}
	if _, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"skills"}, Projects: make([]string, 65)}); !errors.Is(err, ErrParams) {
		t.Fatal("excess projects accepted", err)
	}
}

func TestRead_ProjectsQuotedTOMLAndHeaderReferences(t *testing.T) {
	config := `[mcp_servers."quoted.server"]
command = "safe-launcher"
args = ["--stdio"]
env_vars = [{name="REMOTE_TOKEN",source="local"}]
[mcp_servers."quoted.server".env]
PASSWORD = """multi
line-secret-canary"""
[mcp_servers.http]
url="https://example.invalid/mcp"
[mcp_servers.http.http_headers]
X-Password="header-secret-canary"
[mcp_servers.http.env_http_headers]
Authorization="MCP_TOKEN"
`
	roots := fixtureRoots(t, config)
	got, err := Read(context.Background(), roots, protocol.DiscoverParams{Kinds: []protocol.DiscoverKind{"mcp"}})
	if err != nil || len(got.McpServers) != 2 {
		t.Fatalf("projection: %+v %v", got, err)
	}
	if !got.McpServers[0].Headers["X-Password"].Secret || got.McpServers[0].Headers["Authorization"].EnvRef != "MCP_TOKEN" || got.McpServers[1].Env["REMOTE_TOKEN"].EnvRef != "REMOTE_TOKEN" {
		t.Fatal("header/env references changed")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "secret-canary") {
		t.Fatal("literal value escaped")
	}
}
