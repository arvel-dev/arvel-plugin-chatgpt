// SPDX-License-Identifier: Apache-2.0
package session

// The grant encoder in this file is deliberately test-only. The pinned vendor
// producer is UnderDevelopment; no production branch can invoke these grants.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/policy"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type requestedProfile struct {
	Network *struct {
		Enabled *bool `json:"enabled"`
	} `json:"network,omitempty"`
	FileSystem *struct {
		Read    []string `json:"read,omitempty"`
		Write   []string `json:"write,omitempty"`
		Entries []struct {
			Access string `json:"access"`
			Path   struct {
				Type    string          `json:"type"`
				Path    string          `json:"path,omitempty"`
				Pattern string          `json:"pattern,omitempty"`
				Value   json.RawMessage `json:"value,omitempty"`
			} `json:"path"`
		} `json:"entries,omitempty"`
		GlobScanMaxDepth *uint `json:"globScanMaxDepth,omitempty"`
	} `json:"fileSystem,omitempty"`
}

func syntheticProfileGrant(raw []byte, p *policy.Policy, cwd, home, answer string) (schema.PermissionsResponse, bool) {
	denied := schema.PermissionsResponse{Permissions: map[string]any{}, Scope: "turn"}
	var request requestedProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil {
		return denied, false
	}
	var categories []protocol.PermissionCategory
	if request.Network != nil && request.Network.Enabled != nil && *request.Network.Enabled {
		categories = append(categories, protocol.PermissionCategoryNetwork)
	}
	check := func(path string, category protocol.PermissionCategory) bool {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "*?[") {
			return false
		}
		resolved, err := safeEditPath(cwd, path, home)
		if err != nil || resolved == cwd {
			return false
		}
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			return false
		}
		categories = append(categories, category)
		return true
	}
	if fs := request.FileSystem; fs != nil {
		if fs.GlobScanMaxDepth != nil {
			return denied, false
		}
		for _, path := range fs.Read {
			if !check(path, protocol.PermissionCategoryRead) {
				return denied, false
			}
		}
		for _, path := range fs.Write {
			if !check(path, protocol.PermissionCategoryEdit) {
				return denied, false
			}
		}
		for _, entry := range fs.Entries {
			category := protocol.PermissionCategoryRead
			if entry.Access == "write" {
				category = protocol.PermissionCategoryEdit
			} else if entry.Access != "read" {
				return denied, false
			}
			if entry.Path.Type != "path" || entry.Path.Pattern != "" || len(entry.Path.Value) != 0 || !check(entry.Path.Path, category) {
				return denied, false
			}
		}
	}
	decision := p.Evaluate(policy.Request{Categories: categories})
	if decision == protocol.PermissionLevelDeny {
		return denied, false
	}
	asks := decision != protocol.PermissionLevelAllow
	if asks && answer != "allow_once" && answer != "allow_always" {
		return denied, true
	}
	var original map[string]any
	if json.Unmarshal(raw, &original) != nil {
		return denied, asks
	}
	scope := "turn"
	if asks && answer == "allow_always" {
		scope = "session"
	}
	return schema.PermissionsResponse{Permissions: original, Scope: scope}, asks
}

func TestPermissions_SyntheticScopedEncoderNeverExpandsRequest(t *testing.T) {
	cwd, home := configDirs(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	file := filepath.Join(cwd, "file")
	profile, _ := json.Marshal(map[string]any{"network": map[string]bool{"enabled": true}, "fileSystem": map[string]any{"read": []string{file}, "entries": []any{map[string]any{"access": "write", "path": map[string]string{"type": "path", "path": file}}}}})
	compiler := jsonschema.NewCompiler()
	contract, err := compiler.Compile(filepath.Join("..", "..", "testdata", "schema", "0.164.0", "PermissionsRequestApprovalResponse.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, answer, edit, scope string
		granted, asked            bool
	}{
		{"once", "allow_once", "ask", "turn", true, true},
		{"session", "allow_always", "ask", "session", true, true},
		{"decline", "reject_once", "ask", "turn", false, true},
		{"cancel", "", "ask", "turn", false, true},
		{"deny", "allow_once", "deny", "turn", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := policy.Compile(protocol.SessionMeta{Permissions: &protocol.Permissions{Edit: protocol.PermissionLevel(tc.edit)}})
			response, asked := syntheticProfileGrant(profile, p, cwd, home, tc.answer)
			encoded, _ := json.Marshal(response)
			var value any
			_ = json.Unmarshal(encoded, &value)
			if err := contract.Validate(value); err != nil {
				t.Fatal(err)
			}
			if asked != tc.asked || response.Scope != tc.scope || (len(response.Permissions) > 0) != tc.granted {
				t.Fatalf("%s asked=%v", encoded, asked)
			}
			if tc.granted {
				original, _ := json.Marshal(response.Permissions)
				var a, b any
				_ = json.Unmarshal(profile, &a)
				_ = json.Unmarshal(original, &b)
				want, _ := json.Marshal(a)
				got, _ := json.Marshal(b)
				if string(want) != string(got) {
					t.Fatal("grant scope expanded")
				}
			}
		})
	}
}

func TestPermissions_SyntheticEncoderRejectsBroadOrUnknownPaths(t *testing.T) {
	cwd, home := configDirs(t)
	cwd, _ = filepath.EvalSymlinks(cwd)
	p, _ := policy.Compile(protocol.SessionMeta{})
	for _, profile := range []any{
		map[string]any{"fileSystem": map[string]any{"write": []string{cwd}}},
		map[string]any{"fileSystem": map[string]any{"write": []string{filepath.Join(cwd, "*")}}},
		map[string]any{"fileSystem": map[string]any{"write": []string{home}}},
		map[string]any{"fileSystem": map[string]any{"entries": []any{map[string]any{"access": "write", "path": map[string]string{"type": "special", "value": "root"}}}}},
		map[string]any{"unknown": true},
	} {
		raw, _ := json.Marshal(profile)
		response, asked := syntheticProfileGrant(raw, p, cwd, home, "allow_always")
		if len(response.Permissions) != 0 || asked {
			t.Fatal("unsafe profile considered grantable")
		}
	}
}
