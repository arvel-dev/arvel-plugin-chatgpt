// SPDX-License-Identifier: Apache-2.0

// Package skillset owns SDK-materialized workspace skill copies.
package skillset

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/safefile"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/protocol/skills"
)

// ErrBusy means another live assignment owns this workspace's shared catalog.
var ErrBusy = errors.New("workspace skill assignment is already in use")

// ErrSource reports invalid, changed or unavailable assigned skills safely.
var ErrSource = errors.New("assigned workspace skills unavailable")

const projectDir = ".agents/skills"
const marker = ".arvel-materialized"

var skillName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type lease struct {
	hash    [32]byte
	refs    int
	catalog []protocol.SkillRef
	cleanup func()
	coord   *coordination
}

// Manager serializes materialization and retains each live catalog until last release.
type Manager struct {
	gate      chan struct{}
	leases    map[string]*lease
	closed    bool
	excluded  []string
	stageRoot string
}

// New creates an owner whose bounded source snapshots stay in the plugin data directory.
// The data directory and credential homes are never accepted as skill sources.
func New(dataDir string) *Manager {
	excluded := []string{dataDir}
	if home, err := os.UserHomeDir(); err == nil {
		excluded = append(excluded, filepath.Join(home, ".codex"))
	}
	if home := os.Getenv("CODEX_HOME"); filepath.IsAbs(home) {
		excluded = append(excluded, home)
	}
	m := &Manager{gate: make(chan struct{}, 1), leases: map[string]*lease{}, excluded: excluded, stageRoot: dataDir}
	m.gate <- struct{}{}
	return m
}

// Acquire shares identical ordered assignments, including an empty assignment.
// Native activation is separate; this helper does not hide external CLI skills.
func (m *Manager) Acquire(ctx context.Context, cwd string, refs []protocol.SkillRef) ([]protocol.SkillRef, func(), error) {
	noop := func() {}
	select {
	case <-ctx.Done():
		return nil, noop, ctx.Err()
	case <-m.gate:
	}
	defer func() { m.gate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return nil, noop, err
	}
	if m.closed {
		return nil, noop, ErrSource
	}
	if !filepath.IsAbs(cwd) || !filepath.IsAbs(m.stageRoot) || len(refs) > 64 {
		return nil, noop, ErrSource
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, noop, ErrSource
	}
	key := root
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	refs = slices.Clone(refs)
	if len(refs) == 0 {
		refs = []protocol.SkillRef{}
	}
	for i := range refs {
		if !skillName.MatchString(refs[i].Name) || strings.Contains(refs[i].Name, "..") || !refs[i].Invocation.Valid() || !filepath.IsAbs(refs[i].Path) {
			return nil, noop, ErrSource
		}
		source, err := filepath.EvalSymlinks(refs[i].Path)
		if err != nil {
			return nil, noop, ErrSource
		}
		for _, excluded := range m.excluded {
			blocked, err := filepath.EvalSymlinks(excluded)
			if err != nil {
				blocked = filepath.Clean(excluded)
			}
			if inside(blocked, source) || inside(source, blocked) {
				return nil, noop, ErrSource
			}
		}
		refs[i].Path = source
	}
	hash, err := fingerprint(ctx, refs)
	if err != nil {
		return nil, noop, err
	}
	if existing := m.leases[key]; existing != nil {
		if existing.hash != hash {
			return nil, noop, ErrBusy
		}
		existing.refs++
		return slices.Clone(existing.catalog), m.release(key, existing), nil
	}
	coord, err := workspaceLock(root)
	if err != nil {
		return nil, noop, err
	}
	keepLock := false
	defer func() {
		if !keepLock {
			coord.close()
		}
	}()
	if err := coord.recover(); err != nil {
		return nil, noop, err
	}
	catalog, err := destinations(root, refs)
	if err != nil {
		return nil, noop, err
	}
	stagedRefs := slices.Clone(refs)
	if len(refs) > 0 {
		stage, err := os.MkdirTemp(m.stageRoot, "arvel-skill-source-")
		if err != nil {
			return nil, noop, ErrSource
		}
		defer os.RemoveAll(stage)
		copied, err := fingerprint(ctx, refs, stage)
		if err != nil {
			return nil, noop, err
		}
		if copied != hash {
			return nil, noop, ErrSource
		}
		for i := range stagedRefs {
			stagedRefs[i].Path = filepath.Join(stage, strconv.Itoa(i))
		}
	}
	if err := coord.prepare(catalog); err != nil {
		return nil, noop, err
	}
	cleanup, err := skills.Materialize(root, []string{filepath.FromSlash(projectDir)}, stagedRefs)
	if err != nil {
		_ = coord.recover()
		return nil, noop, ErrSource
	}
	rollback := true
	defer func() {
		if rollback {
			cleanup()
			_ = coord.recover()
		}
	}()
	after, err := fingerprint(ctx, refs)
	if err != nil {
		return nil, noop, err
	}
	if after != hash {
		return nil, noop, ErrSource
	}
	for _, ref := range catalog {
		if err := nativePolicy(ref); err != nil {
			return nil, noop, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, noop, err
	}
	entry := &lease{hash: hash, refs: 1, catalog: catalog, coord: coord, cleanup: func() { cleanup(); _ = coord.recover(); coord.close() }}
	m.leases[key] = entry
	rollback = false
	keepLock = true
	return slices.Clone(catalog), m.release(key, entry), nil
}

func (m *Manager) release(key string, owned *lease) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-m.gate
			defer func() { m.gate <- struct{}{} }()
			if m.leases[key] != owned {
				return
			}
			owned.refs--
			if owned.refs == 0 {
				delete(m.leases, key)
				owned.cleanup()
			}
		})
	}
}

// Close releases only this manager's owned copies and rejects future acquisition.
func (m *Manager) Close() {
	<-m.gate
	defer func() { m.gate <- struct{}{} }()
	m.closed = true
	for key, entry := range m.leases {
		delete(m.leases, key)
		entry.cleanup()
	}
}

func destinations(cwd string, refs []protocol.SkillRef) ([]protocol.SkillRef, error) {
	if len(refs) == 0 {
		return []protocol.SkillRef{}, nil
	}
	target := filepath.Join(cwd, filepath.FromSlash(projectDir))
	for _, relative := range []string{".agents", filepath.FromSlash(projectDir)} {
		file, err := safefile.Open(cwd, relative)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, ErrSource
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || !info.IsDir() {
			return nil, ErrSource
		}
	}
	var result []protocol.SkillRef
	seen := map[string]bool{}
	claimed := map[string]bool{}
	for _, ref := range refs {
		if !skillName.MatchString(ref.Name) || strings.Contains(ref.Name, "..") || !ref.Invocation.Valid() || seen[strings.ToLower(ref.Name)] {
			return nil, ErrSource
		}
		seen[strings.ToLower(ref.Name)] = true
		chosen := ""
		for _, candidate := range []string{ref.Name, "arvel-" + ref.Name} {
			if claimed[strings.ToLower(candidate)] {
				return nil, ErrBusy
			}
			path := filepath.Join(target, candidate)
			if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
				chosen = candidate
				break
			} else if err != nil {
				return nil, ErrSource
			}

		}
		if chosen == "" {
			return nil, ErrBusy
		}
		claimed[strings.ToLower(chosen)] = true
		result = append(result, protocol.SkillRef{Name: chosen, Path: filepath.Join(target, chosen), Invocation: ref.Invocation})
	}
	return result, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func fingerprint(ctx context.Context, refs []protocol.SkillRef, snapshots ...string) ([32]byte, error) {
	var zero [32]byte
	hash := sha256.New()
	encoded, _ := json.Marshal(refs)
	_, _ = hash.Write(encoded)
	count, total := 0, int64(0)
	for i, ref := range refs {
		copyRoot := ""
		if len(snapshots) > 0 {
			copyRoot = filepath.Join(snapshots[0], strconv.Itoa(i))
			if err := os.Mkdir(copyRoot, 0700); err != nil {
				return zero, ErrSource
			}
		}
		if !filepath.IsAbs(ref.Path) {
			return zero, ErrSource
		}
		root, err := filepath.EvalSymlinks(ref.Path)
		if err != nil {
			return zero, ErrSource
		}
		// Check the required entry point before reading any other source content.
		entry, err := safefile.Open(root, "SKILL.md")
		if err != nil {
			return zero, ErrSource
		}
		info, err := entry.Stat()
		_ = entry.Close()
		if err != nil || !info.Mode().IsRegular() {
			return zero, ErrSource
		}
		found := false
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return ErrSource
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return ErrSource
			}
			if relative == "." {
				if !entry.IsDir() {
					return ErrSource
				}
				return nil
			}
			count++
			if count > 2000 {
				return ErrSource
			}
			if entry.IsDir() {
				if copyRoot != "" {
					if err := os.Mkdir(filepath.Join(copyRoot, relative), 0700); err != nil {
						return ErrSource
					}
				}
				_, _ = hash.Write([]byte("dir:" + relative + "\x00"))
				return nil
			}
			if !entry.Type().IsRegular() {
				return ErrSource
			}
			if relative == marker {
				return ErrSource
			}
			file, err := safefile.Open(root, relative)
			if err != nil {
				return ErrSource
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				return ErrSource
			}
			if info.Size() > 64<<20-total {
				return ErrSource
			}
			header, _ := json.Marshal([]any{relative, info.Mode().Perm() & 0111, info.Size()})
			_, _ = hash.Write(header)
			var writer io.Writer = hash
			var copied *os.File
			if copyRoot != "" {
				copied, err = os.OpenFile(filepath.Join(copyRoot, relative), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|info.Mode().Perm()&0111)
				if err != nil {
					return ErrSource
				}
				writer = io.MultiWriter(hash, copied)
			}
			n, err := io.Copy(writer, io.LimitReader(contextReader{ctx, file}, (64<<20)-total+1))
			if copied != nil {
				if closeErr := copied.Close(); closeErr != nil && err == nil {
					err = closeErr
				}
			}
			total += n
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrSource
			}
			if total > 64<<20 || n != info.Size() {
				return ErrSource
			}
			if relative == "SKILL.md" {
				found = true
			}
			return nil
		})
		if err != nil {
			return zero, err
		}
		if !found {
			return zero, ErrSource
		}
	}
	copy(zero[:], hash.Sum(nil))
	return zero, nil
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}
