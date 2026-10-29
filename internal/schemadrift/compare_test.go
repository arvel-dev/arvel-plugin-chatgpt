// SPDX-License-Identifier: Apache-2.0
package schemadrift

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func write(t *testing.T, dir, name string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func schemaFixture(t *testing.T, mutate func(map[string]any)) (string, string, string) {
	t.Helper()
	a, b := t.TempDir(), t.TempDir()
	doc := map[string]any{"oneOf": []any{map[string]any{"type": "object", "properties": map[string]any{"method": map[string]any{"type": "string", "enum": []any{"test"}}, "params": map[string]any{"$ref": "#/definitions/Input"}}, "required": []any{"method", "params"}}}, "definitions": map[string]any{"Input": map[string]any{"type": "object", "properties": map[string]any{"optional": map[string]any{"type": "string"}, "next": map[string]any{"anyOf": []any{map[string]any{"$ref": "#/definitions/Input"}, map[string]any{"type": "null"}}}}, "required": []any{}}}}
	write(t, a, "ClientRequest.json", doc)
	mutate(doc)
	write(t, b, "ClientRequest.json", doc)
	inv := filepath.Join(t.TempDir(), "surface.json")
	raw, _ := json.Marshal([]Surface{{Envelope: "ClientRequest.json", Method: "test"}})
	if os.WriteFile(inv, raw, 0600) != nil {
		t.Fatal("write inventory")
	}
	return a, b, inv
}
func input(doc map[string]any) map[string]any {
	return doc["definitions"].(map[string]any)["Input"].(map[string]any)
}
func TestCompare_ReachableContract(t *testing.T) {
	cases := []struct {
		name           string
		mutate         func(map[string]any)
		drift, invalid bool
	}{
		{"identical recursive schema", func(map[string]any) {}, false, false},
		{"documentation", func(d map[string]any) { input(d)["description"] = "changed"; input(d)["title"] = "Changed" }, false, false},
		{"optional addition", func(d map[string]any) {
			input(d)["properties"].(map[string]any)["added"] = map[string]any{"type": "boolean"}
		}, false, false},
		{"removed optional", func(d map[string]any) { delete(input(d)["properties"].(map[string]any), "optional") }, true, false},
		{"renamed optional", func(d map[string]any) {
			p := input(d)["properties"].(map[string]any)
			p["renamed"] = p["optional"]
			delete(p, "optional")
		}, true, false},
		{"new required", func(d map[string]any) { input(d)["required"] = []any{"optional"} }, true, false},
		{"type changed", func(d map[string]any) {
			input(d)["properties"].(map[string]any)["optional"] = map[string]any{"type": "number"}
		}, true, false},
		{"method removed", func(d map[string]any) { d["oneOf"] = []any{} }, true, false},
		{"union reorder", func(d map[string]any) {
			p := input(d)["properties"].(map[string]any)["next"].(map[string]any)
			a := p["anyOf"].([]any)
			a[0], a[1] = a[1], a[0]
		}, false, false},
		{"union removal", func(d map[string]any) {
			p := input(d)["properties"].(map[string]any)["next"].(map[string]any)
			p["anyOf"] = p["anyOf"].([]any)[:1]
		}, true, false},
		{"external reference", func(d map[string]any) {
			input(d)["properties"].(map[string]any)["optional"] = map[string]any{"$ref": "https://example.invalid/schema"}
		}, false, true},
		{"missing reference", func(d map[string]any) {
			input(d)["properties"].(map[string]any)["optional"] = map[string]any{"$ref": "#/definitions/Missing"}
		}, false, true},
		{"ambiguous alternatives", func(d map[string]any) {
			p := input(d)["properties"].(map[string]any)["next"].(map[string]any)
			p["anyOf"] = append(p["anyOf"].([]any), map[string]any{"type": "null"})
		}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, inv := schemaFixture(t, tc.mutate)
			f, err := Compare(context.Background(), a, b, inv)
			if (err != nil) != tc.invalid {
				t.Fatalf("invalid=%v want %v: %v", err != nil, tc.invalid, err)
			}
			if err == nil && (len(f) > 0) != tc.drift {
				t.Fatalf("findings=%v", f)
			}
		})
	}
}

func TestCompare_RealBaselineAndCompleteProductionInventory(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "testdata", "schema", "0.164.0")
	inv := filepath.Join(dir, "production-surface.json")
	f, err := Compare(context.Background(), dir, dir, inv)
	if err != nil || len(f) != 0 {
		t.Fatalf("real self comparison: %v %v", f, err)
	}
	surface, err := inventory(inv)
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]bool{}
	for _, s := range surface {
		methods[s.Method] = true
	}
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, "internal", "appserver", "peer.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range g.Specs {
			v, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range v.Names {
				if name.Name != "clientMethods" && name.Name != "serverMethods" && name.Name != "notificationMethods" {
					continue
				}
				ast.Inspect(v.Values[0], func(n ast.Node) bool {
					if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						method, _ := strconv.Unquote(lit.Value)
						if !methods[method] {
							t.Errorf("unprotected production method %s", method)
						}
					}
					return true
				})
			}
		}
	}
	if !methods["error"] || !methods["initialized"] {
		t.Fatal("missing routed notification")
	}
}

func TestResolve_RenamedDefinitionAndEscapedPointer(t *testing.T) {
	a, b, inv := schemaFixture(t, func(d map[string]any) {
		defs := d["definitions"].(map[string]any)
		defs["Renamed"] = defs["Input"]
		delete(defs, "Input")
		props := d["oneOf"].([]any)[0].(map[string]any)["properties"].(map[string]any)
		props["params"].(map[string]any)["$ref"] = "#/definitions/Renamed"
		next := defs["Renamed"].(map[string]any)["properties"].(map[string]any)["next"].(map[string]any)["anyOf"].([]any)
		next[0].(map[string]any)["$ref"] = "#/definitions/Renamed"
	})
	f, err := Compare(context.Background(), a, b, inv)
	if err != nil || len(f) > 0 {
		t.Fatalf("renamed definition: %v %v", f, err)
	}
	dir := t.TempDir()
	write(t, dir, "a.json", map[string]any{"definitions": map[string]any{"a/b~c": map[string]any{"type": "string"}}})
	bundle := &bundle{root: dir, docs: map[string]any{}}
	n, err := bundle.resolve(node{file: "a.json", value: map[string]any{"$ref": "#/definitions/a~1b~0c"}})
	if err != nil || n.value.(map[string]any)["type"] != "string" {
		t.Fatal("escaped pointer", err)
	}
}

func TestCompare_EnumsInvalidJSONAndInventory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []any
		drift  bool
	}{{"enum addition", []any{"old", "new"}, false}, {"enum removal", []any{"new"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, inv := schemaFixture(t, func(map[string]any) {})
			for _, dir := range []string{a, b} {
				raw, err := os.ReadFile(filepath.Join(dir, "ClientRequest.json"))
				if err != nil {
					t.Fatal(err)
				}
				var d map[string]any
				if json.Unmarshal(raw, &d) != nil {
					t.Fatal("read schema")
				}
				values := []any{"old"}
				if dir == b {
					values = tc.values
				}
				input(d)["properties"].(map[string]any)["optional"] = map[string]any{"type": "string", "enum": values}
				write(t, dir, "ClientRequest.json", d)
			}
			f, err := Compare(context.Background(), a, b, inv)
			if err != nil || (len(f) > 0) != tc.drift {
				t.Fatalf("%v %v", f, err)
			}
		})
	}
	a, b, inv := schemaFixture(t, func(map[string]any) {})
	if os.WriteFile(filepath.Join(b, "ClientRequest.json"), []byte(`{"bad":`), 0600) != nil {
		t.Fatal("write malformed")
	}
	if _, err := Compare(context.Background(), a, b, inv); err == nil {
		t.Fatal("malformed candidate accepted")
	}
	write(t, filepath.Dir(inv), filepath.Base(inv), []Surface{{Envelope: "ClientRequest.json", Method: "test"}, {Envelope: "ClientRequest.json", Method: "test"}})
	if _, err := inventory(inv); err == nil {
		t.Fatal("duplicate inventory accepted")
	}
}

func TestCompare_OrderedTupleItemsCannotBeReordered(t *testing.T) {
	a, b, inv := schemaFixture(t, func(map[string]any) {})
	for _, dir := range []string{a, b} {
		raw, err := os.ReadFile(filepath.Join(dir, "ClientRequest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if json.Unmarshal(raw, &d) != nil {
			t.Fatal("read schema")
		}
		tuple := []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}
		if dir == b {
			tuple[0], tuple[1] = tuple[1], tuple[0]
		}
		input(d)["properties"].(map[string]any)["optional"] = map[string]any{"type": "array", "items": tuple}
		write(t, dir, "ClientRequest.json", d)
	}
	f, err := Compare(context.Background(), a, b, inv)
	if err != nil || len(f) == 0 {
		t.Fatalf("ordered tuple change must be detected: %v %v", f, err)
	}
}
