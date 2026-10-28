// SPDX-License-Identifier: Apache-2.0

package appserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sync"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

var errDialect = errors.New("app-server sent an invalid wire envelope")
var errDuplicateRequest = errors.New("app-server reused an active request ID")

// dialect adapts only envelopes; RawMessage preserves numbers without float64.
type dialect struct {
	mu     sync.Mutex
	active map[string]bool
}
type liteReader struct {
	scanner *jsonl.Scanner
	pending []byte
	state   *dialect
}

func (r *liteReader) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		if err := r.scanner.Err(); err != nil {
			return 0, err
		}
		raw := bytes.TrimSpace(r.scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var envelope map[string]json.RawMessage
		if raw[0] != '{' || json.Unmarshal(raw, &envelope) != nil || envelope == nil {
			return 0, errDialect
		}
		if version, ok := envelope["jsonrpc"]; ok && string(version) != `"2.0"` {
			return 0, errDialect
		}
		if id, ok := envelope["id"]; ok && len(envelope["method"]) > 0 {
			key, err := wireID(id)
			if err != nil {
				return 0, err
			}
			r.state.mu.Lock()
			duplicate := r.state.active[key]
			r.state.active[key] = true
			r.state.mu.Unlock()
			if duplicate {
				return 0, errDuplicateRequest
			}
		}
		envelope["jsonrpc"] = json.RawMessage(`"2.0"`)
		data, err := encodeEnvelope(envelope)
		if err != nil {
			return 0, errDialect
		}
		r.pending = append(data, '\n')
	}
	n := copy(dst, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type liteWriter struct {
	output io.Writer
	state  *dialect
}

func (w *liteWriter) Write(line []byte) (int, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(line, &envelope) != nil || envelope == nil {
		return 0, errDialect
	}
	delete(envelope, "jsonrpc")
	data, err := encodeEnvelope(envelope)
	if err != nil {
		return 0, errDialect
	}
	if len(data) > jsonl.DefaultMaxLine {
		return 0, jsonl.ErrLineTooLong
	}
	data = append(data, '\n')
	n, err := w.output.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return 0, err
	}
	if id, ok := envelope["id"]; ok && len(envelope["method"]) == 0 {
		w.state.mu.Lock()
		key, _ := wireID(id)
		delete(w.state.active, key)
		w.state.mu.Unlock()
	}
	return len(line), nil
}

func encodeEnvelope(envelope map[string]json.RawMessage) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(envelope); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

func wireID(raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return "s:" + value, nil
	}
	if n, ok := new(big.Int).SetString(string(raw), 10); ok {
		return "n:" + n.String(), nil
	}
	return "", errDialect
}
