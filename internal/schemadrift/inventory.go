// SPDX-License-Identifier: Apache-2.0

// Package schemadrift conservatively compares reachable, local vendor schemas.
package schemadrift

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Surface names a semantic method and its separately generated response schema.
type Surface struct {
	Envelope string `json:"envelope"`
	Method   string `json:"method"`
	Response string `json:"response,omitempty"`
}
type node struct {
	file, pointer string
	value         any
}
type bundle struct {
	root string
	docs map[string]any
}

func inventory(path string) ([]Surface, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("read production inventory")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s []Surface
	if dec.Decode(&s) != nil || len(s) == 0 || len(s) > 256 {
		return nil, errors.New("invalid production inventory")
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return nil, errors.New("trailing inventory input")
	}
	seen := map[string]bool{}
	for _, v := range s {
		key := v.Envelope + ":" + v.Method
		if v.Method == "" || !localPath(v.Envelope) || (v.Response != "" && !localPath(v.Response)) || seen[key] {
			return nil, errors.New("invalid or duplicate inventory method")
		}
		seen[key] = true
	}
	return s, nil
}
func localPath(path string) bool {
	return path != "" && !strings.ContainsAny(path, "\\?#:") && !strings.HasPrefix(path, "/") && filepath.IsLocal(filepath.FromSlash(path))
}
func (b *bundle) load(file string) (node, error) {
	if !localPath(file) {
		return node{}, errors.New("nonlocal schema path")
	}
	if doc, ok := b.docs[file]; ok {
		return node{file: file, value: doc}, nil
	}
	path := filepath.Join(b.root, filepath.FromSlash(file))
	// Bundles may contain only ordinary files beneath their root, never redirected sources.
	walk := b.root
	for _, part := range strings.Split(file, "/") {
		walk = filepath.Join(walk, part)
		fi, err := os.Lstat(walk)
		if err != nil {
			return node{}, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return node{}, errors.New("symlinked schema source")
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return node{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil || len(raw) > 16<<20 {
		return node{}, errors.New("schema file exceeds limit")
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return node{}, errors.New("malformed schema JSON")
	}
	var tail any
	if dec.Decode(&tail) != io.EOF {
		return node{}, errors.New("trailing schema JSON")
	}
	b.docs[file] = doc
	return node{file: file, value: doc}, nil
}
func child(n node, key string, v any) node {
	return node{file: n.file, pointer: n.pointer + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(key), value: v}
}
func (b *bundle) resolve(n node) (node, error) {
	seen := map[string]bool{}
	for {
		m, ok := n.value.(map[string]any)
		if !ok {
			return n, nil
		}
		raw, has := m["$ref"]
		if !has {
			return n, nil
		}
		ref, ok := raw.(string)
		if !ok {
			return node{}, errors.New("invalid schema reference")
		}
		key := n.file + "#" + n.pointer
		if seen[key] {
			return node{}, errors.New("reference-only schema cycle")
		}
		seen[key] = true
		u, err := url.Parse(ref)
		if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" {
			return node{}, errors.New("external schema reference")
		}
		file := n.file
		if u.Path != "" {
			if strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\:?#") {
				return node{}, errors.New("nonlocal schema reference")
			}
			file = filepath.ToSlash(filepath.Join(filepath.Dir(n.file), filepath.FromSlash(u.Path)))
			if !localPath(file) {
				return node{}, errors.New("escaping schema reference")
			}
		}
		target, err := b.load(file)
		if err != nil {
			return node{}, fmt.Errorf("resolve local schema reference: %w", err)
		}
		if u.Fragment != "" {
			if !strings.HasPrefix(u.Fragment, "/") {
				return node{}, errors.New("unsupported schema anchor")
			}
			for _, part := range strings.Split(u.Fragment[1:], "/") {
				token := strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
				switch v := target.value.(type) {
				case map[string]any:
					next, ok := v[token]
					if !ok {
						return node{}, errors.New("missing schema pointer")
					}
					target = child(target, token, next)
				case []any:
					i, err := strconv.Atoi(token)
					if err != nil || i < 0 || i >= len(v) {
						return node{}, errors.New("invalid schema array pointer")
					}
					target = child(target, token, v[i])
				default:
					return node{}, errors.New("invalid schema pointer")
				}
			}
		}
		// Draft-07 ignores siblings of $ref; accept only annotations to avoid guessing a changed dialect.
		for k := range m {
			if k != "$ref" && k != "description" && k != "title" {
				return node{}, errors.New("unsupported reference sibling")
			}
		}
		n = target
	}
}
func (b *bundle) method(ctx context.Context, s Surface) (node, bool, error) {
	root, err := b.load(s.Envelope)
	if err != nil {
		return node{}, false, err
	}
	doc, ok := root.value.(map[string]any)
	if !ok {
		return node{}, false, errors.New("invalid envelope schema")
	}
	branches, ok := doc["oneOf"].([]any)
	if !ok {
		return node{}, false, errors.New("missing envelope alternatives")
	}
	found := false
	var result node
	for i, v := range branches {
		if err := ctx.Err(); err != nil {
			return node{}, false, err
		}
		n, err := b.resolve(child(child(root, "oneOf", branches), strconv.Itoa(i), v))
		if err != nil {
			return node{}, false, err
		}
		m, _ := n.value.(map[string]any)
		props, _ := m["properties"].(map[string]any)
		method, _ := props["method"].(map[string]any)
		values, _ := method["enum"].([]any)
		if len(values) == 1 && values[0] == s.Method {
			if found {
				return node{}, false, errors.New("duplicate semantic method")
			}
			result = n
			found = true
		}
	}
	return result, found, nil
}
