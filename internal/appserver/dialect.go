// SPDX-License-Identifier: Apache-2.0

package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"sync"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

var errDialect = errors.New("app-server sent an invalid wire envelope")
var errDuplicateRequest = errors.New("app-server reused an active request ID")
var errRequestLimit = errors.New("app-server exceeded pending request capacity")

// dialect adapts only envelopes; RawMessage preserves numbers without float64.
type dialect struct {
	mu                                   sync.Mutex
	active                               map[string]*pendingResponse
	stopped                              bool
	activeBytes                          int
	handledNotifications                 map[string]bool
	notifications                        map[string][]notificationTicket
	notificationBytes, notificationCount int
}

type notificationTicket struct {
	method string
	bytes  int
}

func notificationKey(raw json.RawMessage) string {
	var identity struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(raw, &identity)
	return identity.ThreadID
}

func (d *dialect) reserveNotification(method string, params json.RawMessage, size int) error {
	if !d.handledNotifications[method] {
		return nil
	}
	key := notificationKey(params)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return ErrExited
	}
	if d.notificationCount >= 4096 || d.notificationBytes+size > 8<<20 || len(d.notifications[key]) >= 256 || (d.notifications[key] == nil && len(d.notifications) >= 129) {
		return errRequestLimit
	}
	if d.notifications == nil {
		d.notifications = make(map[string][]notificationTicket)
	}
	d.notifications[key] = append(d.notifications[key], notificationTicket{method: method, bytes: size})
	d.notificationBytes += size
	d.notificationCount++
	return nil
}

func (d *dialect) finishNotification(method string, params json.RawMessage) {
	key := notificationKey(params)
	d.mu.Lock()
	defer d.mu.Unlock()
	queue := d.notifications[key]
	if len(queue) == 0 || queue[0].method != method {
		return
	}
	d.notificationBytes -= queue[0].bytes
	d.notificationCount--
	queue[0] = notificationTicket{}
	if len(queue) == 1 {
		delete(d.notifications, key)
	} else {
		d.notifications[key] = queue[1:]
	}
}

type liteReader struct {
	scanner *jsonl.Scanner
	pending []byte
	state   *dialect
}

func (r *liteReader) Read(dst []byte) (n int, err error) {
	defer func() {
		if err != nil {
			r.state.fail(err)
		}
	}()
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
			duplicate := r.state.active[key] != nil
			limited := len(r.state.active) >= 128 || r.state.activeBytes+len(raw) > 8<<20
			if !duplicate && !limited {
				var source struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
				}
				_ = json.Unmarshal(envelope["params"], &source)
				r.state.active[key] = &pendingResponse{thread: source.ThreadID, turn: source.TurnID, done: make(chan struct{}), bytes: len(raw)}
				r.state.activeBytes += len(raw)
			}
			r.state.mu.Unlock()
			if duplicate {
				return 0, errDuplicateRequest
			}
			if limited {
				return 0, errRequestLimit
			}
		}
		if _, hasID := envelope["id"]; !hasID && len(envelope["method"]) > 0 {
			var method string
			if json.Unmarshal(envelope["method"], &method) != nil {
				return 0, errDialect
			}
			if err := r.state.reserveNotification(method, envelope["params"], len(raw)); err != nil {
				return 0, err
			}
		}
		envelope["jsonrpc"] = json.RawMessage(`"2.0"`)
		data, err := encodeEnvelope(envelope)
		if err != nil {
			return 0, errDialect
		}
		r.pending = append(data, '\n')
	}
	n = copy(dst, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

type liteWriter struct {
	output io.Writer
	state  *dialect
}

func (w *liteWriter) Write(line []byte) (result int, failure error) {
	defer func() {
		if failure != nil {
			w.state.fail(failure)
		}
	}()
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
		if pending := w.state.active[key]; pending != nil {
			delete(w.state.active, key)
			w.state.activeBytes -= pending.bytes
			close(pending.done)
		}
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

// Each object is a request instance: reuse of a wire ID cannot extend a snapshot.
type pendingResponse struct {
	thread, turn string
	done         chan struct{}
	bytes        int
}

func (d *dialect) fail(_ error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	d.stopped = true
	d.activeBytes = 0
	clear(d.notifications)
	d.notificationBytes = 0
	d.notificationCount = 0
	for key, pending := range d.active {
		delete(d.active, key)
		close(pending.done)
	}
}

func (d *dialect) wait(ctx context.Context, thread, turn string) error {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return ErrExited
	}
	var pending []*pendingResponse
	for _, p := range d.active {
		if p.thread == thread && (turn == "" || p.turn == "" || p.turn == turn) {
			pending = append(pending, p)
		}
	}
	d.mu.Unlock()
	for _, p := range pending {
		select {
		case <-p.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return ErrExited
	}
	return ctx.Err()
}
