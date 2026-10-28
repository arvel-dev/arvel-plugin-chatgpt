// SPDX-License-Identifier: Apache-2.0

// Package discovery reads documented vendor catalogs only on an explicit host request.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/safefile"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const maxFile = 1 << 20
const maxEntries = 2000
const maxResult = 8 << 20

var (
	// ErrParams indicates an invalid or excessive discovery request.
	ErrParams = errors.New("invalid discovery kinds, roots or projects")
	// ErrSource deliberately excludes source text, paths and parser diagnostics.
	ErrSource = errors.New("discovery source is unavailable or malformed")
	// ErrLimit indicates that bounded discovery could not return a complete result.
	ErrLimit = errors.New("discovery source exceeds its size or entry limit")
)

// Roots identifies the real user home and the user's default Codex configuration
// directory, never the private login directory selected for agent sessions.
type Roots struct {
	Home, CodexHome string
}

type budget struct{ bytes, entries int }

// Read returns a bounded catalog without starting a process, resolving env
// secrets, materializing skills or changing files. Empty kinds requests nothing.
func Read(ctx context.Context, roots Roots, p protocol.DiscoverParams) (protocol.DiscoverResult, error) {
	result := protocol.DiscoverResult{McpServers: []protocol.DiscoveredMcp{}, Skills: []protocol.DiscoveredSkill{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(p.Kinds) > 2 || len(p.Projects) > 64 {
		return result, ErrParams
	}
	for _, kind := range p.Kinds {
		if !kind.Valid() {
			return result, ErrParams
		}
	}
	for _, project := range p.Projects {
		if !filepath.IsAbs(project) {
			return result, ErrParams
		}
	}
	if len(p.Kinds) == 0 {
		return result, nil
	}
	if !filepath.IsAbs(roots.Home) || !filepath.IsAbs(roots.CodexHome) {
		return result, ErrParams
	}
	codexRel, err := filepath.Rel(roots.Home, roots.CodexHome)
	if err != nil || !localName(codexRel) {
		return result, ErrParams
	}
	roots.Home, err = filepath.EvalSymlinks(roots.Home)
	if err != nil {
		return result, ErrSource
	}
	roots.CodexHome = filepath.Join(roots.Home, codexRel)
	b := &budget{}
	if slices.Contains(p.Kinds, protocol.DiscoverKindMCP) {
		result.McpServers, err = readMCP(ctx, roots, b)
		if err != nil {
			return result, err
		}
	}
	if slices.Contains(p.Kinds, protocol.DiscoverKindSkills) {
		result.Skills, err = readSkills(ctx, roots.Home, p.Projects, b)
		if err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > maxResult {
		return result, ErrLimit
	}
	return result, nil
}

func readFile(ctx context.Context, base, name string, b *budget) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := safefile.Open(base, name)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, ErrSource
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrSource
	}
	if info.Size() > maxFile {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, ErrSource
	}
	if len(data) > maxFile {
		return nil, ErrLimit
	}
	b.bytes += len(data)
	if b.bytes > maxResult {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func localName(name string) bool {
	return name != "." && filepath.IsLocal(name) && !strings.ContainsAny(name, "\x00\r\n")
}

func canonicalDir(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", ErrParams
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", ErrSource
	}
	return resolved, nil
}
