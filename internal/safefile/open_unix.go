//go:build darwin || linux

// SPDX-License-Identifier: Apache-2.0

package safefile

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Open pins each directory and refuses every symlink before opening the next component.
// O_NONBLOCK also prevents a raced FIFO from blocking before the regular-file check.
func Open(base, name string) (*os.File, error) {
	if !localName(name) {
		return nil, ErrSource
	}
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, err := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), filepath.Join(base, name)), nil
}
