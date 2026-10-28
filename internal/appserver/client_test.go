// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/jsonl"
	"github.com/arvel-dev/arvel-sdk/procx"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("APP_SERVER_FAKE"); mode != "" {
		fakeVendor(mode)
		return
	}
	os.Exit(m.Run())
}
func fakeVendor(mode string) {
	if len(os.Args) != 2 || os.Args[1] != "app-server" {
		os.Exit(91)
	}
	scan := jsonl.NewScanner(os.Stdin, 0)
	out := json.NewEncoder(os.Stdout)
	initialized := false
	var delayed json.RawMessage
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scan.Bytes(), &m) != nil {
			os.Exit(92)
		}
		switch m.Method {
		case "initialize":
			if mode == "invalid-init" {
				_ = out.Encode(map[string]any{"id": m.ID, "result": map[string]any{}})
				continue
			}
			if mode == "eof" {
				return
			}
			if mode == "malformed" {
				_, _ = io.WriteString(os.Stdout, "not json\n")
				continue
			}
			if mode == "stall-init" {
				continue
			}
			_ = out.Encode(map[string]any{"id": m.ID, "result": map[string]any{"userAgent": "fakevendor", "codexHome": "/scratch", "platformFamily": "test", "platformOs": "test"}})
		case "initialized":
			initialized = true
		case "account/read":
			if mode == "environment" {
				_ = out.Encode(map[string]any{"id": m.ID, "result": map[string]any{"openai": os.Getenv("OPENAI_API_KEY") != "", "codex": os.Getenv("CODEX_API_KEY") != "", "ssh": os.Getenv("SSH_AUTH_SOCK") != "", "github": os.Getenv("GH_TOKEN") != "", "rust": os.Getenv("RUST_LOG"), "home": os.Getenv("CODEX_HOME") != ""}})
				continue
			}
			if !initialized {
				os.Exit(93)
			}
			if mode == "crash" {
				os.Exit(7)
			}
			if mode == "delayed" {
				_ = out.Encode(map[string]any{"method": "account/updated", "params": map[string]any{}})
				delayed = m.ID
				continue
			}
			_ = out.Encode(map[string]any{"id": m.ID, "result": map[string]any{"account": nil, "requiresOpenaiAuth": true}})
		case "model/list":
			if delayed != nil {
				_ = out.Encode(map[string]any{"id": delayed, "result": map[string]any{"late": true}})
				delayed = nil
			}
			_ = out.Encode(map[string]any{"id": m.ID, "result": map[string]any{"data": []any{}, "nextCursor": nil}})
		}
	}
}
func fakeOptions(t *testing.T, mode string) Options {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Options{Path: binary, Dir: t.TempDir(), Env: procx.Env().Set("APP_SERVER_FAKE", mode).Build(), LogWriter: io.Discard}
}
func TestClient_HandshakeCallsAndIdempotentClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Start(ctx, fakeOptions(t, "normal"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Requires bool `json:"requiresOpenaiAuth"`
	}
	if err := c.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Requires {
		t.Fatal("account response missing")
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("cleanup not complete")
	}
}
func TestClient_CallCancellationDoesNotKillProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Start(ctx, fakeOptions(t, "delayed"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	callCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if err := c.Call(callCtx, "account/read", nil, nil); err == nil {
		t.Fatal("stalled call succeeded")
	}
	var result struct {
		Data []any `json:"data"`
	}
	if err := c.Call(ctx, "model/list", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
		t.Fatal("single call killed process")
	default:
	}
}
func TestClient_StartupAndCrashCleanup(t *testing.T) {
	for _, mode := range []string{"eof", "malformed", "invalid-init", "stall-init", "crash"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			o := fakeOptions(t, mode)
			o.InitializeTimeout = 500 * time.Millisecond
			c, err := Start(ctx, o)
			if mode != "crash" {
				if err == nil {
					c.Close(ctx)
					t.Fatal("broken initialization succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Call(ctx, "account/read", nil, nil); err == nil {
				t.Fatal("crashed process returned success")
			}
			select {
			case <-c.Done():
			case <-ctx.Done():
				t.Fatal("process not cleaned")
			}
		})
	}
}

func TestClient_EnvironmentUsesSDKIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o := fakeOptions(t, "environment")
	o.Env = append(o.Env, "OPENAI_API_KEY=synthetic-canary", "CODEX_API_KEY=synthetic-canary", "SSH_AUTH_SOCK=synthetic-canary", "GH_TOKEN=synthetic-canary", "CODEX_HOME="+t.TempDir(), "RUST_LOG=trace")
	c, err := Start(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var result struct {
		OpenAI, Codex, SSH, GitHub bool
		Rust                       string
		Home                       bool
	}
	if err := c.Call(ctx, "account/read", nil, &result); err != nil {
		t.Fatal(err)
	}
	if result.OpenAI || result.Codex || result.SSH || result.GitHub || result.Rust != "warn" || !result.Home {
		t.Fatal("vendor environment isolation failed")
	}
}

func TestClient_ParentCancellationFailsPendingCallsAndCleansUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	o := fakeOptions(t, "delayed")
	o.Peer.Notifications = map[string]Notification{"account/updated": func(context.Context, json.RawMessage) { close(entered) }}
	c, err := Start(ctx, o)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	pending := make(chan error, 1)
	go func() { pending <- c.Call(context.Background(), "account/read", nil, nil) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("fake vendor never received pending call")
	}
	cancel()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("parent cancellation left process running")
	}
	select {
	case err := <-pending:
		if err == nil {
			t.Fatal("pending call reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("pending call leaked")
	}
}
