// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/manifest"
)

func TestManifest_DeclaresIsolatedConnectionsAndSixAssets(t *testing.T) {
	root := filepath.Join("..", "..")
	m, err := manifest.LoadFile(filepath.Join(root, "arvel.json"))
	if err != nil {
		t.Fatal(err)
	}
	if problems := m.ValidateWith(manifest.Options{Official: true}); len(problems) != 0 {
		t.Fatal(problems)
	}
	if m.ID != "chatgpt" || m.Type != manifest.TypeProvider || m.License != "Apache-2.0" {
		t.Fatalf("identity: %+v", m)
	}
	if len(m.Connections) != 2 || m.Connections[0].Kind != "chatgpt" || m.Connections[1].Kind != "api_key" {
		t.Fatal("connection kinds do not match specification")
	}
	if len(m.Connections[0].Secrets) != 0 || len(m.Connections[1].Secrets) != 1 || m.Connections[1].Secrets[0].Pattern != "^sk-" || !m.Connections[1].Secrets[0].Required {
		t.Fatal("API key secret declaration")
	}
	if m.Sandbox == nil || len(m.Sandbox.VendorPaths.Read) != 1 || m.Sandbox.VendorPaths.Read[0] != "~/.codex" || len(m.Sandbox.VendorPaths.Write) != 0 {
		t.Fatal("vendor paths must rely on the existing plugin data-directory grant")
	}
	for _, platform := range manifest.Platforms() {
		goos, arch, _ := strings.Cut(platform, "-")
		entry, ok := m.EntryFor(goos, arch)
		ext, suffix := ".tar.gz", ""
		if goos == "windows" {
			ext, suffix = ".zip", ".exe"
		}
		if !ok || entry.Asset != "arvel-plugin-chatgpt_"+goos+"_"+arch+ext || entry.Bin != "arvel-plugin-chatgpt"+suffix {
			t.Fatalf("entry %s: %+v", platform, entry)
		}
	}
	release, err := os.ReadFile(filepath.Join(root, ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"project_name: arvel-plugin-chatgpt", "main: ./cmd/arvel-plugin-chatgpt", "binary: arvel-plugin-chatgpt", "draft: true", "CGO_ENABLED=0"} {
		if !strings.Contains(string(release), expected) {
			t.Fatalf("release configuration missing %q", expected)
		}
	}
}
