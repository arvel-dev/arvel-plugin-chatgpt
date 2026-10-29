// SPDX-License-Identifier: Apache-2.0
package schemadrift

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
)

// Finding is a stable structural incompatibility requiring human review.
type Finding struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}
type comparison struct {
	ctx      context.Context
	a, b     *bundle
	method   string
	seen     map[string]bool
	findings []Finding
	visits   int
}

// Compare checks all inventoried method payloads/responses using only local refs.
// Invalid input returns an error; structural removals/changes return findings.
func Compare(ctx context.Context, baselineDir, candidateDir, inventoryPath string) ([]Finding, error) {
	surface, err := inventory(inventoryPath)
	if err != nil {
		return nil, err
	}
	c := &comparison{ctx: ctx, a: &bundle{root: baselineDir, docs: map[string]any{}}, b: &bundle{root: candidateDir, docs: map[string]any{}}}
	for _, s := range surface {
		c.method = s.Envelope + ":" + s.Method
		c.seen = map[string]bool{}
		a, ok, err := c.a.method(ctx, s)
		if err != nil || !ok {
			return nil, errors.New("baseline method missing or invalid")
		}
		b, ok, err := c.b.method(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("candidate envelope: %w", err)
		}
		if !ok {
			c.add("$", "method removed")
			continue
		}
		if err = c.walk(a, b, "$"); err != nil {
			return nil, fmt.Errorf("%s: %w", c.method, err)
		}
		if s.Response != "" {
			a, err = c.a.load(s.Response)
			if err != nil {
				return nil, errors.New("baseline response missing or invalid")
			}
			b, err = c.b.load(s.Response)
			if os.IsNotExist(err) {
				c.add("response", "response schema removed")
				continue
			}
			if err != nil {
				return nil, err
			}
			if err = c.walk(a, b, "response"); err != nil {
				return nil, fmt.Errorf("%s response: %w", c.method, err)
			}
		}
	}
	sort.Slice(c.findings, func(i, j int) bool {
		a, b := c.findings[i], c.findings[j]
		return a.Method+"\x00"+a.Path+"\x00"+a.Reason < b.Method+"\x00"+b.Path+"\x00"+b.Reason
	})
	return c.findings, nil
}
func (c *comparison) add(path, reason string) {
	c.findings = append(c.findings, Finding{c.method, path, reason})
}
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
func equal(a, b any) bool { return reflect.DeepEqual(a, b) }

// Only the JSON Schema type keyword is an unordered set here. Tuple items and
// array-valued const/default constraints retain their positional semantics.
func keywordEqual(key string, a, b any) bool {
	if key == "type" {
		if x, ok := a.([]any); ok {
			if y, ok := b.([]any); ok {
				return reflect.DeepEqual(set(x), set(y))
			}
		}
	}
	return equal(a, b)
}
func set(v []any) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		raw, _ := json.Marshal(x)
		out = append(out, string(raw))
	}
	slices.Sort(out)
	return out
}
func annotation(key string) bool {
	switch key {
	case "description", "title", "$schema", "$id", "definitions", "$defs", "examples", "$comment":
		return true
	}
	return false
}
func (c *comparison) walk(a, b node, path string) error {
	c.visits++
	if c.visits > 1000000 {
		return errors.New("schema comparison budget exceeded")
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	var err error
	a, err = c.a.resolve(a)
	if err != nil {
		return err
	}
	b, err = c.b.resolve(b)
	if err != nil {
		return err
	}
	pair := a.file + "#" + a.pointer + "|" + b.file + "#" + b.pointer
	if c.seen[pair] {
		return nil
	}
	c.seen[pair] = true
	x, xok := a.value.(map[string]any)
	y, yok := b.value.(map[string]any)
	if !xok || !yok {
		if !equal(a.value, b.value) {
			c.add(path, "schema value changed")
		}
		return nil
	}
	for _, k := range keys(x) {
		if annotation(k) {
			continue
		}
		av := x[k]
		bv, exists := y[k]
		switch k {
		case "properties":
			xp, ok := av.(map[string]any)
			if !ok {
				return errors.New("invalid schema properties")
			}
			yp, _ := bv.(map[string]any)
			for _, name := range keys(xp) {
				v, ok := yp[name]
				if !ok {
					c.add(path+"."+name, "property removed")
					continue
				}
				if err = c.walk(child(child(a, k, av), name, xp[name]), child(child(b, k, bv), name, v), path+"."+name); err != nil {
					return err
				}
			}
		case "required":
			xa, ok := av.([]any)
			if !ok {
				return errors.New("invalid required set")
			}
			ya, _ := bv.([]any)
			for _, v := range xa {
				if !slices.Contains(set(ya), stringJSON(v)) {
					c.add(path, "required guarantee removed")
				}
			}
			for _, v := range ya {
				if !slices.Contains(set(xa), stringJSON(v)) {
					c.add(path, "required field added")
				}
			}
		case "enum":
			xa, ok := av.([]any)
			if !ok {
				return errors.New("invalid enum")
			}
			ya, _ := bv.([]any)
			if !exists {
				c.add(path, "enum constraint changed")
				continue
			}
			for _, v := range set(xa) {
				if !slices.Contains(set(ya), v) {
					c.add(path, "enum alternative removed")
				}
			}
		case "oneOf", "anyOf", "allOf":
			xa, ok := av.([]any)
			if !ok {
				return errors.New("invalid schema union")
			}
			ya, ok := bv.([]any)
			if !ok {
				c.add(path, "union removed or changed")
				continue
			}
			if k == "allOf" && len(xa) != len(ya) {
				c.add(path, "intersection constraints changed")
			}
			if err = c.union(child(a, k, av), child(b, k, bv), xa, ya, path+"/"+k); err != nil {
				return err
			}
		case "items", "additionalProperties", "contains", "not", "if", "then", "else":
			if !exists {
				c.add(path+"/"+k, "schema constraint removed")
				continue
			}
			if err = c.walk(child(a, k, av), child(b, k, bv), path+"/"+k); err != nil {
				return err
			}
		default:
			if !exists || !keywordEqual(k, av, bv) {
				c.add(path+"/"+k, "schema constraint changed")
			}
		}
	}
	for _, k := range keys(y) {
		if _, ok := x[k]; ok || annotation(k) || k == "properties" {
			continue
		}
		if k == "required" {
			v, _ := y[k].([]any)
			if len(v) == 0 {
				continue
			}
		}
		c.add(path+"/"+k, "schema constraint added")
	}
	return nil
}
func stringJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
func signature(b *bundle, n node) (string, error) {
	n, err := b.resolve(n)
	if err != nil {
		return "", err
	}
	m, ok := n.value.(map[string]any)
	if !ok {
		return stringJSON(n.value), nil
	}
	p, _ := m["properties"].(map[string]any)
	for _, k := range []string{"type", "method", "kind", "status", "decision", "mode", "action"} {
		if v, ok := p[k]; ok {
			n, err := b.resolve(child(child(n, "properties", p), k, v))
			if err != nil {
				return "", err
			}
			v, _ := n.value.(map[string]any)
			if values, ok := v["enum"].([]any); ok && len(values) == 1 {
				return k + ":" + stringJSON(values[0]), nil
			}
			if v["const"] != nil {
				return k + ":" + stringJSON(v["const"]), nil
			}
		}
	}
	if len(p) > 0 {
		return "object", nil
	}
	if values, ok := m["enum"].([]any); ok {
		_ = values
		return "enum-type:" + stringJSON(m["type"]), nil
	}
	for _, kind := range []string{"oneOf", "anyOf", "allOf"} {
		if _, ok := m[kind]; ok {
			return "union:" + kind, nil
		}
	}
	if typ, ok := m["type"]; ok {
		return "type:" + stringJSON(typ), nil
	}
	// Unsupported shapes share a broad key; only a unique structural match is accepted.
	return "schema", nil
}
func (c *comparison) union(a, b node, x, y []any, path string) error {
	choices := map[string][]int{}
	for i, v := range y {
		key, err := signature(c.b, child(b, strconv.Itoa(i), v))
		if err != nil {
			return err
		}
		choices[key] = append(choices[key], i)
	}
	for i, v := range x {
		key, err := signature(c.a, child(a, strconv.Itoa(i), v))
		if err != nil {
			return err
		}
		indexes := choices[key]
		if len(indexes) == 0 {
			c.add(path, "union alternative removed or changed")
			continue
		}
		if len(indexes) > 1 {
			var matches []int
			for _, j := range indexes {
				probe := &comparison{ctx: c.ctx, a: c.a, b: c.b, method: c.method, seen: maps.Clone(c.seen), visits: c.visits}
				err := probe.walk(child(a, strconv.Itoa(i), v), child(b, strconv.Itoa(j), y[j]), path)
				c.visits = probe.visits
				if err != nil {
					return err
				}
				if len(probe.findings) == 0 {
					matches = append(matches, j)
				}
			}
			if len(matches) != 1 {
				c.add(path, "ambiguous or incompatible union alternatives")
				continue
			}
			indexes = matches
		}
		j := indexes[0]
		if err = c.walk(child(a, strconv.Itoa(i), v), child(b, strconv.Itoa(j), y[j]), path+"/"+key); err != nil {
			return err
		}
	}
	return nil
}
