// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"github.com/arvel-dev/arvel-sdk/protocol"
	"testing"
)

func TestBaseline_EditNetworkReadOnlyMatrix(t *testing.T) {
	for _, edit := range protocol.AllPermissionLevels() {
		for _, net := range protocol.AllPermissionLevels() {
			for _, readOnly := range []bool{false, true} {
				meta := protocol.SessionMeta{Permissions: &protocol.Permissions{Edit: edit, Network: net}, Task: &protocol.TaskMeta{Type: "feature", ReadOnly: readOnly}}
				p, err := Compile(meta)
				if err != nil {
					t.Fatal(err)
				}
				want := "read-only"
				if edit == protocol.PermissionLevelAllow && !readOnly {
					want = "workspace-write"
				}
				if p.Sandbox() != want || p.NetworkAllowed() != (net == protocol.PermissionLevelAllow) {
					t.Fatalf("wrong baseline for %s/%s/%v", edit, net, readOnly)
				}
				if meta.Permissions.Edit != edit {
					t.Fatal("mutated host policy")
				}
			}
		}
	}
}

func TestCommandMatching_ExactVendorStringAndDenyPrecedence(t *testing.T) {
	p, err := Compile(protocol.SessionMeta{Permissions: &protocol.Permissions{Commands: &protocol.CommandPolicy{Default: protocol.PermissionLevelAsk, Allow: []string{"go test *", "*safe*"}, Deny: []string{"*unsafe*"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command string
		want    protocol.PermissionLevel
	}{
		{"go test ./...", protocol.PermissionLevelAllow},
		{"/bin/zsh -lc 'go test ./...'", protocol.PermissionLevelAsk},
		{"safe but unsafe", protocol.PermissionLevelDeny},
		{"go test sub/path\nnext", protocol.PermissionLevelAllow},
		{"привет", protocol.PermissionLevelAsk},
	} {
		got := p.Evaluate(Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryCommand}, Command: &tc.command})
		if got != tc.want {
			t.Fatalf("%q: %s want %s", tc.command, got, tc.want)
		}
	}
	if got := p.Evaluate(Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryCommand}}); got != protocol.PermissionLevelAsk {
		t.Fatal("missing command auto-approved")
	}
}

func TestElevatedCommand_CombinesFileAndNetworkDenials(t *testing.T) {
	cmd := "go test ./..."
	for _, deny := range []protocol.PermissionCategory{protocol.PermissionCategoryEdit, protocol.PermissionCategoryNetwork} {
		perms := &protocol.Permissions{Edit: protocol.PermissionLevelAllow, Network: protocol.PermissionLevelAllow, Commands: &protocol.CommandPolicy{Default: protocol.PermissionLevelAllow}}
		if deny == protocol.PermissionCategoryEdit {
			perms.Edit = protocol.PermissionLevelDeny
		} else {
			perms.Network = protocol.PermissionLevelDeny
		}
		p, err := Compile(protocol.SessionMeta{Permissions: perms})
		if err != nil {
			t.Fatal(err)
		}
		if p.Evaluate(Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryCommand, protocol.PermissionCategoryEdit, protocol.PermissionCategoryNetwork}, Command: &cmd}) != protocol.PermissionLevelDeny {
			t.Fatal("command allow bypassed a capability denial")
		}
	}
}

func TestPolicy_FailsClosedForUnknownAndUnsupportedPatterns(t *testing.T) {
	for _, pattern := range []string{"", "go test ?", "[unsafe]"} {
		if _, err := Compile(protocol.SessionMeta{Permissions: &protocol.Permissions{Commands: &protocol.CommandPolicy{Deny: []string{pattern}}}}); err == nil {
			t.Fatal("silently ignored unsupported deny pattern")
		}
	}
	p, err := Compile(protocol.SessionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Evaluate(Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryOther}}) != protocol.PermissionLevelAsk {
		t.Fatal("unknown capability allowed")
	}
}
