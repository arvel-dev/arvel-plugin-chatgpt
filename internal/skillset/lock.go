// SPDX-License-Identifier: Apache-2.0
package skillset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/safefile"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// workspaceLock is held for the entire lease, across this provider's processes.
// The coordination inode persists so concurrent openers always lock the same file.
func workspaceLock(cwd string) (*coordination, error) {
	parent := cwd
	var directory string
	for {
		path := filepath.Join(parent, ".git")
		info, err := os.Lstat(path)
		if err == nil {
			if info.IsDir() {
				directory = path
				break
			}
			if !info.Mode().IsRegular() {
				return nil, ErrSource
			}
			file, err := safefile.Open(parent, ".git")
			if err != nil {
				return nil, ErrSource
			}
			raw, err := io.ReadAll(io.LimitReader(file, 4097))
			_ = file.Close()
			if err != nil || len(raw) > 4096 {
				return nil, ErrSource
			}
			value, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
			if !ok || value == "" || strings.ContainsAny(value, "\x00\r\n") {
				return nil, ErrSource
			}
			directory = value
			if !filepath.IsAbs(directory) {
				directory = filepath.Join(parent, directory)
			}
			directory, err = filepath.EvalSymlinks(directory)
			if err != nil {
				return nil, ErrSource
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, ErrSource
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	if directory == "" {
		directory = filepath.Join(cwd, ".agents")
		err := os.Mkdir(directory, 0700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrSource
		}
		file, err := safefile.Open(cwd, ".agents")
		if err != nil {
			return nil, ErrSource
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil || !info.IsDir() {
			return nil, ErrSource
		}
	}
	identity := cwd
	if runtime.GOOS == "windows" {
		identity = strings.ToLower(identity)
	}
	hash := sha256.Sum256([]byte(identity))
	path := filepath.Join(directory, "arvel-chatgpt-skills-"+hex.EncodeToString(hash[:12])+".lock")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, ErrSource
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, ErrSource
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) || current.Mode()&os.ModeSymlink != 0 {
		_ = file.Close()
		return nil, ErrSource
	}
	if err := lockHandle(file); err != nil {
		_ = file.Close()
		return nil, ErrBusy
	}
	return &coordination{file: file, cwd: cwd}, nil
}

type coordination struct {
	file *os.File
	cwd  string
}
type ownershipJournal struct {
	Names []string `json:"names"`
}

func (c *coordination) close() { unlockHandle(c.file); _ = c.file.Close() }
func (c *coordination) clear() error {
	if err := c.file.Truncate(0); err != nil {
		return ErrSource
	}
	_, err := c.file.Seek(0, io.SeekStart)
	if err != nil {
		return ErrSource
	}
	if c.file.Sync() != nil {
		return ErrSource
	}
	return nil
}

func (c *coordination) recover() error {
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return ErrSource
	}
	raw, err := io.ReadAll(io.LimitReader(c.file, 16385))
	if err != nil || len(raw) > 16384 {
		return ErrBusy
	}
	if len(raw) == 0 {
		return nil
	}
	var journal ownershipJournal
	if json.Unmarshal(raw, &journal) != nil || len(journal.Names) > 64 {
		return ErrBusy
	}

	for _, name := range journal.Names {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00\r\n") {
			return ErrBusy
		}
		path := filepath.Join(c.cwd, filepath.FromSlash(projectDir), name)
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			// A dead provider does not prove that an orphaned CLI task has stopped.
			// Preserve copies until their original cleanup or an explicit host recovery.
			return ErrBusy
		}
	}

	return c.clear()
}

func (c *coordination) write(journal ownershipJournal) error {
	if err := c.clear(); err != nil {
		return err
	}
	if json.NewEncoder(c.file).Encode(journal) != nil || c.file.Sync() != nil {
		return ErrSource
	}
	return nil
}

func (c *coordination) prepare(catalog []protocol.SkillRef) error {
	journal := ownershipJournal{Names: []string{}}
	for _, ref := range catalog {
		journal.Names = append(journal.Names, filepath.Base(ref.Path))
	}
	return c.write(journal)
}
