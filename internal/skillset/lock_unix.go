// SPDX-License-Identifier: Apache-2.0
//go:build darwin || linux

package skillset

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockHandle(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }
func unlockHandle(file *os.File)     { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
