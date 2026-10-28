// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

type enteredWriter struct {
	io.Writer
	entered chan struct{}
	once    sync.Once
}

func (w *enteredWriter) Write(b []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	return w.Writer.Write(b)
}

func TestReplyBarrier_RequiresActualPipeWrite(t *testing.T) {
	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	writer := &enteredWriter{Writer: outputW, entered: make(chan struct{})}
	returned := make(chan struct{})
	p := NewPeer(inputR, writer, PeerOptions{Requests: map[string]Handler{"item/fileChange/requestApproval": func(context.Context, json.RawMessage) (any, error) {
		close(returned)
		return map[string]any{"decision": "decline"}, nil
	}}})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() { defer close(served); _ = p.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = inputW.Close()
		_ = outputR.Close()
		_ = outputW.Close()
		_ = inputR.Close()
		select {
		case <-served:
		case <-time.After(2 * time.Second):
			t.Error("peer did not stop")
		}
	})
	_, err := io.WriteString(inputW, `{"id":"approval","method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"turn","itemId":"item","startedAtMs":0}}`+"\n")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("handler not invoked")
	}
	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("response writer not invoked")
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	err = p.WaitResponses(short, "thread", "turn")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handler return incorrectly counted as completed write: %v", err)
	}
	if !bufio.NewScanner(outputR).Scan() {
		t.Fatal("missing response")
	}
	full, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err := p.WaitResponses(full, "thread", "turn"); err != nil {
		t.Fatal(err)
	}
}

type snapshotContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *snapshotContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestReplyBarrier_DoesNotFollowReusedWireID(t *testing.T) {
	a := &pendingResponse{thread: "thread", turn: "turn", done: make(chan struct{})}
	b := &pendingResponse{thread: "thread", turn: "turn", done: make(chan struct{})}
	state := &dialect{active: map[string]*pendingResponse{"n:1": a, "n:2": b}}
	parent, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := &snapshotContext{Context: parent, entered: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- state.wait(ctx, "thread", "turn") }()
	<-ctx.entered
	var output bytes.Buffer
	writer := liteWriter{output: &output, state: state}
	if _, err := writer.Write([]byte(`{"id":1,"result":{}}`)); err != nil {
		t.Fatal(err)
	}
	r := &liteReader{scanner: jsonl.NewScanner(strings.NewReader(`{"id":1,"method":"item/fileChange/requestApproval","params":{"threadId":"thread","turnId":"turn"}}`+"\n"), 0), state: state}
	if _, err := r.Read(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(`{"id":2,"result":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	reused := state.active["n:1"]
	state.mu.Unlock()
	if reused == nil || reused == a {
		t.Fatal("missing distinct reused request instance")
	}
	select {
	case <-reused.done:
		t.Fatal("new instance completed before its response")
	default:
	}
	if _, err := writer.Write([]byte(`{"id":1,"result":{}}`)); err != nil {
		t.Fatal(err)
	}
	if err := state.wait(parent, "thread", "turn"); err != nil {
		t.Fatal(err)
	}
}

func TestReplyBarrier_FailedWriteIsNotSuccess(t *testing.T) {
	state := &dialect{active: map[string]*pendingResponse{"n:1": {thread: "thread", turn: "turn", done: make(chan struct{})}}}
	writer := liteWriter{output: shortWriter{}, state: state}
	if _, err := writer.Write([]byte(`{"id":1,"result":{}}`)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if err := state.wait(context.Background(), "thread", "turn"); !errors.Is(err, ErrExited) {
		t.Fatalf("write failure certified as success: %v", err)
	}
}

func TestDialect_BoundsOutstandingServerRequests(t *testing.T) {
	var input strings.Builder
	for i := 0; i < 129; i++ {
		fmt.Fprintf(&input, "{\"id\":%d,\"method\":\"item/fileChange/requestApproval\",\"params\":{\"threadId\":\"t\"}}\n", i)
	}
	state := &dialect{active: make(map[string]*pendingResponse)}
	r := &liteReader{scanner: jsonl.NewScanner(strings.NewReader(input.String()), 0), state: state}
	if _, err := io.Copy(io.Discard, r); err == nil {
		t.Fatal("unbounded pending server requests accepted")
	}
}

func TestDialect_BoundsNotificationsBeforeSDKDispatch(t *testing.T) {
	const method = "item/agentMessage/delta"
	input := strings.Repeat(`{"method":"`+method+`","params":{"threadId":"thread","turnId":"turn","itemId":"item","delta":"text"}}`+"\n", 257)
	p := NewPeer(strings.NewReader(""), io.Discard, PeerOptions{Notifications: map[string]Notification{method: func(context.Context, json.RawMessage) {}}})
	r := &liteReader{scanner: jsonl.NewScanner(strings.NewReader(input), 0), state: p.state}
	if _, err := io.Copy(io.Discard, r); err == nil {
		t.Fatal("SDK notification backlog was unbounded")
	}
}
