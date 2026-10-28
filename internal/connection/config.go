// SPDX-License-Identifier: Apache-2.0

// Package connection owns one Codex account and its lazy app-server process.
package connection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/arvel-dev/arvel-sdk/procx"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const minimumVersion = "0.164.0"
const installURL = "https://developers.openai.com/codex/cli"

type config struct {
	kind, path, dataDir, home, key, proxy string
	separate, remote                      bool
}

func parseConfig(meta protocol.InitMeta) (config, error) {
	c := config{kind: meta.Connection.Kind, dataDir: meta.DataDir, remote: meta.Platform.Remote, key: meta.Connection.Secrets["apiKey"]}
	// New Arvel connections authenticate independently; explicit false is a
	// legacy G2 catalog/status mode and cannot acquire an agent session.
	c.separate = c.kind == "chatgpt"
	if c.kind != "chatgpt" && c.kind != "api_key" {
		return c, errors.New("unsupported connection kind")
	}
	if !filepath.IsAbs(c.dataDir) {
		return c, errors.New("plugin data directory must be absolute")
	}
	if v, ok := meta.Connection.Config["codexPath"]; ok {
		var valid bool
		c.path, valid = v.(string)
		if !valid {
			return c, errors.New("codexPath must be a string")
		}
	}
	if v, ok := meta.Connection.Config["separateLogin"]; ok {
		var valid bool
		c.separate, valid = v.(bool)
		if !valid || (c.kind != "chatgpt" && c.separate) {
			return c, errors.New("invalid separateLogin configuration")
		}
	}
	if os.Getenv("ARVEL_DEV") == "1" && os.Getenv("ARVEL_CODEX_BIN") != "" {
		c.path = os.Getenv("ARVEL_CODEX_BIN")
	}
	if c.path != "" && !filepath.IsAbs(c.path) {
		return c, errors.New("codexPath must be absolute")
	}
	if c.kind == "api_key" || c.separate {
		c.home = filepath.Join(c.dataDir, "codex-home")
	}
	if meta.Sandbox != nil {
		c.proxy = meta.Sandbox.Proxy
	}
	return c, nil
}

func (c config) environment() []string {
	b := procx.Env().Strip("OPENAI_", "CODEX_", "RUST_LOG").Set("RUST_LOG", "warn").Proxy(c.proxy)
	if c.home != "" {
		b.Set("CODEX_HOME", c.home)
	}
	return b.Build()
}

func (c config) prepare() error {
	if err := os.MkdirAll(c.dataDir, 0700); err != nil {
		return errors.New("create plugin data directory")
	}
	if c.home != "" {
		// Refuse redirected private state; never follow a pre-existing home symlink.
		if fi, err := os.Lstat(c.home); err == nil {
			if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
				return errors.New("private Codex home must be a directory")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("inspect private Codex home directory")
		}
		if err := os.MkdirAll(c.home, 0700); err != nil {
			return errors.New("create private Codex home")
		}
		if err := os.Chmod(c.home, 0700); err != nil {
			return errors.New("restrict private Codex home")
		}
	}
	return nil
}

func findBinary(c config) (string, error) {
	if c.path == "" {
		return procx.FindBinary("codex", "~/.npm-global/bin", "/opt/homebrew/bin", `%APPDATA%\npm`, "~/.local/bin")
	}
	path, err := filepath.EvalSymlinks(c.path)
	if err != nil {
		return "", errors.New("configured Codex executable not found")
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || (runtime.GOOS != "windows" && fi.Mode()&0111 == 0) {
		return "", errors.New("configured Codex path is not executable")
	}
	return path, nil
}

func version(ctx context.Context, path string) (string, error) {
	v, _, err := procx.Version(ctx, path)
	return v, err
}
