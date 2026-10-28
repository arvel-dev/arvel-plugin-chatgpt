// SPDX-License-Identifier: Apache-2.0

package safefile

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Open checks reparse points themselves and rejects them. Directory handles omit delete
// sharing, pinning each checked parent against replacement until the leaf opens.
func Open(base, name string) (*os.File, error) {
	if !localName(name) {
		return nil, ErrSource
	}
	var parents []windows.Handle
	defer func() {
		for _, h := range parents {
			_ = windows.CloseHandle(h)
		}
	}()
	parts := append([]string{""}, strings.Split(filepath.Clean(name), string(filepath.Separator))...)
	path := base
	for i, part := range parts {
		path = filepath.Join(path, part)
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return nil, ErrSource
		}
		h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return nil, err
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			_ = windows.CloseHandle(h)
			return nil, ErrSource
		}
		if i < len(parts)-1 {
			if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
				_ = windows.CloseHandle(h)
				return nil, ErrSource
			}
			parents = append(parents, h)
			continue
		}
		return os.NewFile(uintptr(h), path), nil
	}
	return nil, ErrSource
}
