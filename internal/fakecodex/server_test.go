// SPDX-License-Identifier: Apache-2.0
package fakecodex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

type client struct {
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
}

func fixture(t *testing.T) *client {
	t.Helper()
	for _, k := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
		t.Setenv(k, "")
	}
	in, w := io.Pipe()
	r, out := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	c := &client{in: w, out: bufio.NewScanner(r), done: make(chan error, 1)}
	stop := context.AfterFunc(ctx, func() { _ = r.Close() })
	go func() { err := Serve(ctx, in, out); _ = out.Close(); c.done <- err }()
	t.Cleanup(func() {
		_ = w.Close()
		_ = r.Close()
		cancel()
		stop()
		select {
		case <-c.done:
		case <-time.After(time.Second):
			t.Error("fixture did not stop")
		}
	})
	return c
}
func (c *client) send(t *testing.T, id int, method string, params any) {
	t.Helper()
	if err := json.NewEncoder(c.in).Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
}
func (c *client) next(t *testing.T) map[string]any {
	t.Helper()
	if !c.out.Scan() {
		t.Fatal("fixture ended before response")
	}
	var m map[string]any
	if json.Unmarshal(c.out.Bytes(), &m) != nil {
		t.Fatal("invalid fixture framing")
	}
	return m
}
func (c *client) reply(t *testing.T, id int) map[string]any {
	t.Helper()
	for {
		m := c.next(t)
		if m["id"] == float64(id) {
			return m["result"].(map[string]any)
		}
	}
}

func TestServe_PrivateLoginAndUnknownMethod(t *testing.T) {
	c := fixture(t)
	c.send(t, 1, "account/read", nil)
	if c.reply(t, 1)["account"] != nil {
		t.Fatal("initial account must be absent")
	}
	c.send(t, 2, "account/login/start", map[string]string{"type": "apiKey", "apiKey": "sk-synthetic"})
	c.reply(t, 2)
	c.send(t, 3, "account/read", nil)
	if c.reply(t, 3)["account"].(map[string]any)["type"] != "apiKey" {
		t.Fatal("synthetic login missing")
	}
	c.send(t, 4, "unknown", nil)
	if c.out.Scan() {
		t.Fatal("unknown method silently accepted")
	}
	select {
	case err := <-c.done:
		if err == nil {
			t.Fatal("unknown method must fail")
		}
		c.done <- err
	case <-time.After(time.Second):
		t.Fatal("unknown method did not stop")
	}
}

func TestServe_HeldTurnStreamsAndInterrupts(t *testing.T) {
	c := fixture(t)
	c.send(t, 1, "turn/start", map[string]any{"threadId": "thread", "input": []any{map[string]string{"type": "text", "text": "conformance:hold"}}})
	reply := c.reply(t, 1)
	turn := reply["turn"].(map[string]any)["id"]
	if c.next(t)["method"] != "item/agentMessage/delta" {
		t.Fatal("held turn did not stream")
	}
	c.next(t)
	c.send(t, 2, "turn/interrupt", map[string]any{"threadId": "thread", "turnId": turn})
	m := c.next(t)
	if m["method"] != "turn/completed" || m["params"].(map[string]any)["turn"].(map[string]any)["status"] != "interrupted" {
		t.Fatal("missing interrupt completion")
	}
	c.reply(t, 2)
}

func TestServe_ConcurrencyBarrierAndProxy(t *testing.T) {
	c := fixture(t)
	for i, thread := range []string{"a", "b"} {
		c.send(t, i+1, "turn/start", map[string]any{"threadId": thread, "input": []any{map[string]string{"type": "text", "text": "conformance:overlap"}}})
		c.reply(t, i+1)
		if c.next(t)["method"] != "item/agentMessage/delta" {
			t.Fatal("missing barrier activity")
		}
	}
	completions := map[string]bool{}
	for range 4 {
		m := c.next(t)
		if m["method"] == "turn/completed" {
			completions[m["params"].(map[string]any)["threadId"].(string)] = true
		}
	}
	if !completions["a"] || !completions["b"] {
		t.Fatal("barrier lost a thread")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9876")
	c.send(t, 3, "turn/start", map[string]any{"threadId": "a", "input": []any{map[string]string{"type": "text", "text": "conformance:proxy"}}})
	c.reply(t, 3)
	m := c.next(t)
	if m["params"].(map[string]any)["item"].(map[string]any)["text"] != "HTTPS_PROXY=http://127.0.0.1:9876\n" {
		t.Fatal("proxy must come from child environment")
	}
	// Drain remaining proxy events so the single fixture writer can finish.
	for range 5 {
		c.next(t)
	}
}

func TestServe_MalformedMCPConfigFailsWithoutPanic(t *testing.T) {
	c := fixture(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	c.send(t, 1, "thread/start", map[string]any{"cwd": t.TempDir(), "config": map[string]any{"notify": []any{}, "features": map[string]any{"plugins": false}, "mcp_servers": map[string]any{"fixture": 3}}})
	// thread/started may precede config validation; consume until the explicit failure.
	for c.out.Scan() {
	}
	select {
	case err := <-c.done:
		if err == nil {
			t.Fatal("malformed MCP config accepted")
		}
		c.done <- err
	case <-time.After(time.Second):
		t.Fatal("malformed config did not stop")
	}
}
