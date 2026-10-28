// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"bytes"
	"log/slog"
	"sync"
)

const stderrLimit = 64 << 10

// stderrLines drops oversized lines entirely, avoiding partial secret disclosure.
type stderrLines struct {
	mu       sync.Mutex
	log      *slog.Logger
	buf      []byte
	dropping bool
}

func (s *stderrLines) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size := len(data)
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		chunk := data
		if index >= 0 {
			chunk = data[:index]
		}
		if !s.dropping {
			if len(s.buf)+len(chunk) > stderrLimit {
				s.buf = nil
				s.dropping = true
			} else {
				s.buf = append(s.buf, chunk...)
			}
		}
		if index < 0 {
			break
		}
		s.emit()
		data = data[index+1:]
	}
	return size, nil
}
func (s *stderrLines) emit() {
	if s.dropping {
		s.log.Debug("app-server stderr line omitted")
	} else if len(s.buf) > 0 {
		// MCP children may print session secrets unknown to the process logger.
		s.log.Debug("app-server stderr content omitted", "bytes", len(s.buf))
	}
	s.buf = nil
	s.dropping = false
}
func (s *stderrLines) flush() { s.mu.Lock(); defer s.mu.Unlock(); s.emit() }
