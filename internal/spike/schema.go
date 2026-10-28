// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const bundleURL = "https://schema.arvel.invalid/bundle/"

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, errors.New("schema resource is absent from the local bundle")
}

// Bundle is a local-only schema compiler and exact method-to-payload index.
type Bundle struct {
	compiler *jsonschema.Compiler
	files    map[string]string
	methods  map[string]methodSchema
}

type methodSchema struct {
	Envelope string `json:"envelope"`
	Pointer  string `json:"pointer"`
	Params   string `json:"params,omitempty"`
	Response string `json:"response,omitempty"`
}

// LoadBundle compiles the recorded schema without external resource access.
func LoadBundle(ctx context.Context, dir string) (*Bundle, error) {
	b := &Bundle{compiler: jsonschema.NewCompiler(), files: map[string]string{}, methods: map[string]methodSchema{}}
	b.compiler.UseLoader(offlineLoader{})
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" || entry.Name() == "surface.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return errors.New("invalid JSON in schema bundle")
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if err := b.compiler.AddResource(bundleURL+rel, doc); err != nil {
			return errors.New("register schema resource")
		}
		if prior, exists := b.files[entry.Name()]; exists {
			return fmt.Errorf("ambiguous schema name: %s and %s", prior, rel)
		}
		b.files[entry.Name()] = rel
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load bundle: %w", err)
	}
	for _, rel := range b.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := b.compiler.Compile(bundleURL + rel); err != nil {
			return nil, fmt.Errorf("compile schema %s", rel)
		}
	}
	for _, name := range []string{"ClientRequest.json", "ClientNotification.json", "ServerRequest.json", "ServerNotification.json"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var doc struct {
			OneOf []struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"oneOf"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, err
		}
		for i, branch := range doc.OneOf {
			var method struct {
				Enum []string `json:"enum"`
			}
			_ = json.Unmarshal(branch.Properties["method"], &method)
			ref := parameterRef(branch.Properties["params"])
			if len(method.Enum) != 1 {
				return nil, errors.New("method schema has no unique method")
			}
			key := name + ":" + method.Enum[0]
			m := methodSchema{Envelope: name, Pointer: fmt.Sprintf("#/oneOf/%d", i)}
			if ref != "" {
				typeName := strings.TrimPrefix(ref, "#/definitions/")
				m.Params = b.files[typeName+".json"]
				if strings.HasSuffix(typeName, "Params") {
					m.Response = b.files[strings.TrimSuffix(typeName, "Params")+"Response.json"]
				}
			}
			if name == "ClientRequest.json" && method.Enum[0] == "account/logout" {
				m.Response = b.files["LogoutAccountResponse.json"]
			}
			if name == "ClientRequest.json" && method.Enum[0] == "configRequirements/read" {
				m.Response = b.files["ConfigRequirementsReadResponse.json"]
			}
			if name == "ClientRequest.json" && method.Enum[0] == "windowsSandbox/readiness" {
				m.Response = b.files["WindowsSandboxReadinessResponse.json"]
			}
			b.methods[key] = m
		}
	}
	return b, nil
}

func parameterRef(raw json.RawMessage) string {
	var schema struct {
		Ref   string            `json:"$ref"`
		AnyOf []json.RawMessage `json:"anyOf"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return ""
	}
	if schema.Ref != "" {
		return schema.Ref
	}
	for _, branch := range schema.AnyOf {
		if ref := parameterRef(branch); ref != "" {
			return ref
		}
	}
	return ""
}

func (b *Bundle) validate(name string, value any) error {
	if name == "" {
		return errors.New("no recorded schema mapping")
	}
	s, err := b.compiler.Compile(bundleURL + name)
	if err != nil {
		return errors.New("compile message schema")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode message for validation")
	}
	normalized, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return errors.New("decode message for validation")
	}
	if err := s.Validate(normalized); err != nil {
		return fmt.Errorf("message does not match %s", name)
	}
	return nil
}

// Surface writes an inventory of required SPEC methods and exact schema pointers.
func (b *Bundle) Surface(path string) error {
	required := map[string][]string{
		"ClientRequest.json":      {"initialize", "account/read", "account/login/start", "account/logout", "model/list", "account/rateLimits/read", "thread/start", "thread/resume", "turn/start", "turn/interrupt"},
		"ClientNotification.json": {"initialized"},
		"ServerRequest.json":      {"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval"},
		"ServerNotification.json": {"account/login/completed", "account/updated", "account/rateLimits/updated", "thread/started", "turn/started", "turn/completed", "turn/plan/updated", "turn/diff/updated", "item/started", "item/completed", "item/agentMessage/delta", "thread/tokenUsage/updated"},
	}
	out := map[string]methodSchema{}
	for envelope, methods := range required {
		for _, method := range methods {
			key := envelope + ":" + method
			m, ok := b.methods[key]
			if !ok {
				return fmt.Errorf("missing required method %s", method)
			}
			out[key] = m
		}
	}
	fields := map[string][]string{
		"InitializeParams.json":                      {"clientInfo"},
		"ThreadStartParams.json":                     {"cwd", "model", "approvalPolicy", "sandbox", "config", "developerInstructions", "ephemeral"},
		"TurnStartParams.json":                       {"threadId", "input", "model", "effort"},
		"CommandExecutionRequestApprovalParams.json": {"command", "itemId", "threadId", "turnId"},
		"PermissionsRequestApprovalResponse.json":    {"permissions", "scope"},
	}
	for file, names := range fields {
		rel, ok := b.files[file]
		if !ok {
			return fmt.Errorf("missing required schema %s", file)
		}
		for _, name := range names {
			pointer := "#/properties/" + name
			if _, err := b.compiler.Compile(bundleURL + rel + pointer); err != nil {
				return fmt.Errorf("missing schema field %s%s", rel, pointer)
			}
			out["field:"+rel+pointer] = methodSchema{Envelope: rel, Pointer: pointer}
		}
	}
	return writeJSON(path, out)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
