// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/safefile"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"gopkg.in/yaml.v3"
)

func readSkills(ctx context.Context, home string, projects []string, b *budget) ([]protocol.DiscoveredSkill, error) {
	result := []protocol.DiscoveredSkill{}
	type location struct {
		base  string
		scope protocol.DiscoverScope
	}
	locations := []location{{base: home, scope: protocol.DiscoverScopeUser}}
	seen := map[string]bool{}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		base, err := canonicalDir(project)
		if err != nil {
			return nil, err
		}
		if !seen[base] {
			locations = append(locations, location{base: base, scope: protocol.DiscoverScopeProject})
			seen[base] = true
		}
	}
	for _, root := range locations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir, err := safefile.Open(root.base, filepath.Join(".agents", "skills"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, ErrSource
		}
		info, err := dir.Stat()
		if err != nil || !info.IsDir() {
			_ = dir.Close()
			return nil, ErrSource
		}
		names, err := dir.Readdirnames(maxEntries + 1)
		_ = dir.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, ErrSource
		}
		b.entries += len(names)
		if b.entries > maxEntries {
			return nil, ErrLimit
		}
		slices.Sort(names)
		for _, folder := range names {
			if !localName(folder) {
				return nil, ErrSource
			}
			entry, err := safefile.Open(root.base, filepath.Join(".agents", "skills", folder))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, ErrSource
			}
			info, err := entry.Stat()
			_ = entry.Close()
			if err != nil {
				return nil, ErrSource
			}
			if !info.IsDir() {
				continue
			}
			rel := filepath.Join(".agents", "skills", folder, "SKILL.md")
			data, err := readFile(ctx, root.base, rel, b)
			if err != nil {
				return nil, err
			}
			if len(data) == 0 {
				continue
			}
			name, err := skillName(data)
			if err != nil {
				return nil, err
			}
			path := filepath.Join(root.base, filepath.Dir(rel))
			result = append(result, protocol.DiscoveredSkill{Name: name, Scope: root.scope, Source: path, Path: path})
		}
	}
	return result, nil
}

func skillName(data []byte) (string, error) {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return "", ErrSource
	}
	end := bytes.Index(data[4:], []byte("\n---\n"))
	if end < 0 {
		return "", ErrSource
	}
	var meta struct {
		Name string `yaml:"name"`
	}
	if yaml.Unmarshal(data[4:4+end], &meta) != nil || meta.Name == "" || len(meta.Name) > 256 || !public(meta.Name, nil) {
		return "", ErrSource
	}
	return meta.Name, nil
}
