// SPDX-License-Identifier: Apache-2.0
package schema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	s "github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func validate(t *testing.T, name string, value any) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "schema", "0.164.0")
	var path string
	if err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Name() == name {
			path = p
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("https://schema.invalid/contract", doc); err != nil {
		t.Fatal(err)
	}
	compiled, err := c.Compile("https://schema.invalid/contract")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
func TestStableTypes_MatchRecordedSchemas(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
	}{
		{"InitializeParams.json", s.InitializeParams{ClientInfo: s.ClientInfo{Name: "arvel", Title: "Arvel", Version: "0.1.0"}}},
		{"GetAccountParams.json", s.AccountReadParams{}},
		{"LoginAccountParams.json", s.LoginParams{Type: "chatgptDeviceCode"}},
		{"LoginAccountParams.json", s.LoginParams{Type: "apiKey", APIKey: "synthetic-not-a-real-key"}},
		{"ThreadStartParams.json", s.ThreadStartParams{CWD: "/scratch", ApprovalPolicy: "on-request", Sandbox: "read-only"}},
		{"ThreadResumeParams.json", s.ThreadResumeParams{ThreadID: "thread"}},
		{"TurnStartParams.json", s.TurnStartParams{ThreadID: "thread", Input: []s.UserInput{{Type: "text", Text: "PONG"}}}},
		{"TurnInterruptParams.json", s.TurnInterruptParams{ThreadID: "thread", TurnID: "turn"}},
		{"CommandExecutionRequestApprovalResponse.json", s.DecisionResponse{Decision: "decline"}},
		{"PermissionsRequestApprovalResponse.json", s.PermissionsResponse{Permissions: map[string]any{}, Scope: "turn"}},
	} {
		t.Run(tc.name, func(t *testing.T) { validate(t, tc.name, tc.v) })
	}
}
func TestStableTypes_ReemitRecordedRequests(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures")
	paths, err := filepath.Glob(filepath.Join(root, "*", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var e struct {
				Direction string `json:"direction"`
				Message   struct {
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				} `json:"message"`
			}
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatal(err)
			}
			if e.Direction != "client_to_server" {
				continue
			}
			var target any
			var name string
			switch e.Message.Method {
			case "initialize":
				target = &s.InitializeParams{}
				name = "InitializeParams.json"
			case "thread/start":
				target = &s.ThreadStartParams{}
				name = "ThreadStartParams.json"
			case "turn/start":
				target = &s.TurnStartParams{}
				name = "TurnStartParams.json"
			default:
				continue
			}
			if err := json.Unmarshal(e.Message.Params, target); err != nil {
				t.Fatal(err)
			}
			validate(t, name, target)
			count++
		}
	}
	if count < 48 {
		t.Fatalf("only %d recorded requests checked", count)
	}
}
