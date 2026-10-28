// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDiscovery_ManualPolicyAndSecretGolden(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "discovery")
	data, err := os.ReadFile(filepath.Join(root, "skill", "agents", "openai.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Policy struct {
			AllowImplicit *bool `yaml:"allow_implicit_invocation"`
		} `yaml:"policy"`
	}
	if err := yaml.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Policy.AllowImplicit == nil || *metadata.Policy.AllowImplicit {
		t.Fatal("native manual-only metadata was lost")
	}
	golden, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Servers []struct {
			Env map[string]struct {
				Secret bool   `json:"secret"`
				EnvRef string `json:"envRef"`
			} `json:"env"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(golden, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected.Servers) != 2 || !expected.Servers[0].Env["EXAMPLE_API_KEY"].Secret || expected.Servers[0].Env["EXAMPLE_TOKEN"].EnvRef != "EXAMPLE_TOKEN" {
		t.Fatal("secret/envRef golden contract changed")
	}
	if strings.Contains(string(golden), "synthetic-not-a-real-key") {
		t.Fatal("synthetic secret is present in discovery output")
	}
}
