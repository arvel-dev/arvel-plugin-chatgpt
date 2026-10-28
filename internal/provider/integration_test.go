// SPDX-License-Identifier: Apache-2.0
package provider_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/provider"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/testkit/mockhost"
)

// TestMain doubles as a synthetic Codex executable. It never invokes a vendor/network.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, "codex-cli 0.164.0")
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "app-server" {
		serveFakeCodex()
		return
	}
	os.Exit(m.Run())
}
func serveFakeCodex() {
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
		if os.Getenv(key) != "" {
			os.Exit(91)
		}
	}
	// Do not inspect anything inside CODEX_HOME, including in this fake.
	initial, _ := os.ReadFile("initial-account")
	account := string(initial)
	_, failureMode := os.Stat("login-fails")
	loginCount := 0
	catalog, err := os.ReadFile("catalog.json")
	if err != nil {
		os.Exit(92)
	}
	var expected map[string]string
	raw, err := os.ReadFile("expected-env.json")
	if err != nil || json.Unmarshal(raw, &expected) != nil {
		os.Exit(95)
	}
	for k, v := range expected {
		if os.Getenv(k) != v {
			os.Exit(96)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	scan := bufio.NewScanner(os.Stdin)
	reply := func(id json.RawMessage, v any) { _ = enc.Encode(map[string]any{"id": id, "result": v}) }
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Type   string `json:"type"`
				APIKey string `json:"apiKey"`
			} `json:"params"`
		}
		if json.Unmarshal(scan.Bytes(), &m) != nil {
			os.Exit(93)
		}
		switch m.Method {
		case "initialize":
			reply(m.ID, map[string]any{"userAgent": "synthetic Codex", "codexHome": "/synthetic", "platformFamily": "test", "platformOs": "test"})
		case "initialized":
		case "account/read":
			var a any
			if account == "chatgpt" {
				a = map[string]any{"type": "chatgpt", "email": "private@example.invalid", "planType": "plus"}
			} else if account == "apiKey" {
				a = map[string]any{"type": "apiKey"}
			}
			reply(m.ID, map[string]any{"account": a, "requiresOpenaiAuth": true})
		case "account/login/start":
			if m.Params.Type == "apiKey" {
				account = "apiKey"
				reply(m.ID, map[string]any{"type": "apiKey"})
				continue
			}
			loginCount++
			loginID := fmt.Sprintf("attempt-%d", loginCount)
			success := failureMode != nil
			if success {
				account = "chatgpt"
			}
			// A notification before the login response exercises the real transport race.
			_ = enc.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": loginID, "success": success, "error": nil}})
			if m.Params.Type == "chatgptDeviceCode" {
				reply(m.ID, map[string]any{"type": m.Params.Type, "loginId": loginID, "verificationUrl": "https://auth.openai.com/device", "userCode": "synthetic-device-code"})
			} else {
				reply(m.ID, map[string]any{"type": "chatgpt", "loginId": loginID, "authUrl": "https://auth.openai.com/synthetic-login"})
			}
		case "account/login/cancel":
			reply(m.ID, map[string]any{"status": "canceled"})
		case "account/logout":
			account = ""
			reply(m.ID, map[string]any{})
		case "model/list":
			reply(m.ID, json.RawMessage(catalog))
		default:
			os.Exit(94)
		}
	}
}

func integrationMeta(t *testing.T, kind string, separate, remote bool) protocol.InitMeta {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "models", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var e struct {
			Message struct {
				Result json.RawMessage `json:"result"`
			} `json:"message"`
		}
		_ = json.Unmarshal(line, &e)
		if bytes.Contains(e.Message.Result, []byte(`"data"`)) {
			if err := os.WriteFile(filepath.Join(dir, "catalog.json"), e.Message.Result, 0600); err != nil {
				t.Fatal(err)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing recorded catalog")
	}
	expected := map[string]string{"RUST_LOG": "warn", "CODEX_HOME": "", "HTTPS_PROXY": "http://127.0.0.1:4321", "HTTP_PROXY": "http://127.0.0.1:4321", "ALL_PROXY": "http://127.0.0.1:4321", "NO_PROXY": "localhost,127.0.0.1"}
	if kind == "api_key" || separate {
		expected["CODEX_HOME"] = filepath.Join(dir, "codex-home")
	}
	envJSON, _ := json.Marshal(expected)
	if err := os.WriteFile(filepath.Join(dir, "expected-env.json"), envJSON, 0600); err != nil {
		t.Fatal(err)
	}
	return protocol.InitMeta{Sandbox: &protocol.SandboxInfo{Mode: "os", Proxy: "http://127.0.0.1:4321"}, PluginAPI: protocol.PluginAPI, DataDir: dir, Platform: protocol.PlatformInfo{Remote: remote}, Connection: protocol.ConnectionInfo{Kind: kind, Config: map[string]any{"codexPath": binary, "separateLogin": separate}, Secrets: map[string]string{"apiKey": "sk-integration-canary"}}}
}

func TestProvider_FakeVendorThroughSDK(t *testing.T) {
	for _, kind := range []string{"chatgpt", "api_key"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "sk-ambient-canary")
			t.Setenv("CODEX_ACCESS_TOKEN", "ambient-token")
			t.Setenv("GH_TOKEN", "host-token")
			meta := integrationMeta(t, kind, false, false)
			if kind == "chatgpt" {
				if err := os.WriteFile(filepath.Join(meta.DataDir, "initial-account"), []byte("chatgpt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h := mockhost.InProcess(t, provider.New())
			if _, err := h.Initialize(meta); err != nil {
				t.Fatal(err)
			}
			st, err := h.Status()
			if err != nil || st.State != protocol.StateReady {
				t.Fatalf("status %+v %v", st, err)
			}
			models, err := h.Models(false)
			if err != nil || models.Default != "gpt-6.1-sol" {
				t.Fatalf("models %+v %v", models, err)
			}
			if _, err := h.NewSession(protocol.SessionMeta{}); err == nil {
				t.Fatal("unimplemented sessions enabled")
			}
			h.Shutdown()
			for _, secret := range []string{"sk-integration-canary", "sk-ambient-canary", "ambient-token", "host-token", "private@example.invalid"} {
				if strings.Contains(h.Stdout()+h.Stderr(), secret) {
					t.Fatal("credential or identity leaked")
				}
			}
		})
	}
}

type recordingHost struct{ changes chan protocol.Status }

func (h recordingHost) StatusChanged(s protocol.Status) { h.changes <- s }
func (recordingHost) UsageUpdated(protocol.Usage)       {}
func (recordingHost) Logger() *slog.Logger              { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestProvider_LoginPushesStatusThroughRealTransport(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			p := provider.New()
			h := recordingHost{make(chan protocol.Status, 16)}
			p.(plugin.HostAware).SetHost(h)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := p.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			meta := integrationMeta(t, "chatgpt", true, remote)
			if _, err := p.Initialize(context.Background(), meta); err != nil {
				t.Fatal(err)
			}
			st, err := p.Status(context.Background())
			if err != nil || st.State != protocol.StateNeedsLogin {
				t.Fatalf("status %+v %v", st, err)
			}
			result, err := p.Action(context.Background(), "login")
			if err != nil || !result.AwaitCompletion {
				t.Fatalf("login %+v %v", result, err)
			}
			timer := time.NewTimer(2 * time.Second)
			defer timer.Stop()
			for {
				select {
				case s := <-h.changes:
					if s.State == protocol.StateReady {
						return
					}
				case <-timer.C:
					t.Fatal("missing status_changed")
					return
				}
			}
		})
	}
}

func TestProvider_SDKEmitsStatusChangedAfterLogin(t *testing.T) {
	meta := integrationMeta(t, "chatgpt", true, false)
	h := mockhost.InProcess(t, provider.New())
	if _, err := h.Initialize(meta); err != nil {
		t.Fatal(err)
	}
	st, err := h.Status()
	if err != nil || st.State != protocol.StateNeedsLogin {
		t.Fatalf("status %+v %v", st, err)
	}
	if _, err := h.Action("login"); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			for _, line := range strings.Split(h.Stdout(), "\n") {
				var frame struct {
					Method string          `json:"method"`
					Params protocol.Status `json:"params"`
				}
				if json.Unmarshal([]byte(line), &frame) == nil && frame.Method == protocol.MethodStatusChanged && frame.Params.State == protocol.StateReady {
					if frame.Params.Validate() != nil {
						t.Fatal("invalid status_changed body")
					}
					return
				}
			}
		case <-deadline.C:
			t.Fatal("SDK did not emit status_changed")
			return
		}
	}
}

func TestProvider_SDKNotifiesEveryFailedLogin(t *testing.T) {
	meta := integrationMeta(t, "chatgpt", true, false)
	if err := os.WriteFile(filepath.Join(meta.DataDir, "login-fails"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	h := mockhost.InProcess(t, provider.New())
	if _, err := h.Initialize(meta); err != nil {
		t.Fatal(err)
	}
	if st, err := h.Status(); err != nil || st.State != protocol.StateNeedsLogin {
		t.Fatalf("status %+v %v", st, err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := h.Action("login"); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(2 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		completed := false
		for !completed {
			select {
			case <-tick.C:
				count := 0
				for _, line := range strings.Split(h.Stdout(), "\n") {
					var frame struct {
						Method string          `json:"method"`
						Params protocol.Status `json:"params"`
					}
					if json.Unmarshal([]byte(line), &frame) == nil && frame.Method == protocol.MethodStatusChanged && frame.Params.Message == "ChatGPT sign-in failed or was cancelled; try again" {
						count++
					}
				}
				completed = count >= attempt
			case <-timer.C:
				tick.Stop()
				t.Fatal("failed login completion suppressed by SDK debounce")
			}
		}
		timer.Stop()
		tick.Stop()
	}
}
