// SPDX-License-Identifier: Apache-2.0

package spike_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBase = "https://schema.arvel.invalid/codex/0.164.0/"

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("schema reference was not present in the recorded bundle")
}

func recordedSchemas(t *testing.T) (*jsonschema.Compiler, []string) {
	t.Helper()
	root := filepath.Join("..", "..", "testdata", "schema", "0.164.0")
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" || entry.Name() == "surface.json" || entry.Name() == "provenance.json" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if err := c.AddResource(schemaBase+name, doc); err != nil {
			return err
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, names
}

func TestRecordedSchemas_CompileWithoutExternalResources(t *testing.T) {
	c, names := recordedSchemas(t)
	if len(names) != 320 {
		t.Fatalf("recorded %d schemas, want the complete 320-file bundle", len(names))
	}
	for _, name := range names {
		if _, err := c.Compile(schemaBase + name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestApprovalSchemas_RequireMethodSpecificPayloads(t *testing.T) {
	// Synthetic contract examples, never presented as real vendor transcripts.
	c, _ := recordedSchemas(t)
	for _, tc := range []struct {
		name, schema, payload string
		valid                 bool
	}{
		{"command decline", "CommandExecutionRequestApprovalResponse.json", `{"decision":"decline"}`, true},
		{"file decline", "FileChangeRequestApprovalResponse.json", `{"decision":"decline"}`, true},
		{"permissions cannot use generic decline", "PermissionsRequestApprovalResponse.json", `{"decision":"decline"}`, false},
		{"permissions empty grant", "PermissionsRequestApprovalResponse.json", `{"permissions":{},"scope":"turn"}`, true},
		{"permissions session grant", "PermissionsRequestApprovalResponse.json", `{"permissions":{"network":{"enabled":true}},"scope":"session"}`, true},
		{"command is a string", "CommandExecutionRequestApprovalParams.json", `{"itemId":"item","threadId":"thread","turnId":"turn","startedAtMs":0,"command":"go test ./..."}`, true},
		{"command is not argv", "CommandExecutionRequestApprovalParams.json", `{"itemId":"item","threadId":"thread","turnId":"turn","startedAtMs":0,"command":["go","test","./..."]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := c.Compile(schemaBase + tc.schema)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.payload))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(doc); (err == nil) != tc.valid {
				t.Fatalf("valid=%t, want %t: %v", err == nil, tc.valid, err)
			}
		})
	}
}
