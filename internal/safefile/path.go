// SPDX-License-Identifier: Apache-2.0

// Package safefile opens explicit source paths without following symlinks.
package safefile

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrSource reports an unsafe source identity without revealing path contents.
var ErrSource = errors.New("source path unavailable")

func localName(name string) bool {
	return name != "." && filepath.IsLocal(name) && !strings.ContainsAny(name, "\x00\r\n")
}
