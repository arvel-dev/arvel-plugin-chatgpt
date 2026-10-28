// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/jsonl"
	"github.com/arvel-dev/arvel-sdk/procx"
)

func testBundle(t *testing.T) *Bundle {
	t.Helper()
	b, err := LoadBundle(context.Background(), filepath.Join("..", "..", "testdata", "schema", "0.164.0"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRecorderHelper(t *testing.T) {
	mode := os.Getenv("SPIKE_TEST_MODE")
	if mode == "" {
		return
	}
	if mode == "eof" {
		os.Exit(0)
	}
	if mode == "environment" {
		present := map[string]bool{}
		for _, name := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "GH_TOKEN", "SSH_AUTH_SOCK"} {
			present[name] = os.Getenv(name) != ""
		}
		_ = json.NewEncoder(os.Stdout).Encode(present)
		os.Exit(0)
	}
	if mode == "timeout" {
		for {
			time.Sleep(time.Second)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	scanner := jsonl.NewScanner(os.Stdin, 0)
	for scanner.Scan() {
		var m wireMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(3)
		}
		switch m.Method {
		case "initialize":
			if mode == "malformed" {
				_, _ = io.WriteString(os.Stdout, "not json\n")
				os.Exit(0)
			}
			if mode == "approval" || mode == "duplicate" {
				req := map[string]any{"id": "approval-id", "method": "item/permissions/requestApproval", "params": map[string]any{"cwd": "/scratch", "itemId": "item", "threadId": "thread", "turnId": "turn", "startedAtMs": 0, "permissions": map[string]any{}}}
				if encoder.Encode(req) != nil {
					os.Exit(4)
				}
				if !scanner.Scan() {
					os.Exit(5)
				}
				var answer struct {
					Result struct {
						Permissions map[string]any `json:"permissions"`
						Scope       string         `json:"scope"`
					} `json:"result"`
				}
				if json.Unmarshal(scanner.Bytes(), &answer) != nil || answer.Result.Permissions == nil || answer.Result.Scope != "turn" {
					os.Exit(6)
				}
				if mode == "duplicate" {
					_ = encoder.Encode(req)
					continue
				}
			}
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"codexHome": "/scratch", "platformFamily": "unix", "platformOs": "macos", "userAgent": "fixture-helper"}})
		case "initialized":
			if mode != "child-completes-first" && mode != "device-login" && !strings.HasPrefix(mode, "isolation-") {
				os.Exit(0)
			}
		case "account/login/start":
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"type": "chatgptDeviceCode", "loginId": "login-probe", "userCode": "DEVICE-CANARY", "verificationUrl": "https://auth.openai.com/codex/device"}})
			_ = encoder.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{"loginId": "login-probe", "success": true, "error": nil}})
		case "config/read":
			binary, err := os.Executable()
			if err != nil {
				os.Exit(9)
			}
			cwd, err := os.Getwd()
			if err != nil {
				os.Exit(9)
			}
			var params struct {
				CWD *string `json:"cwd"`
			}
			if json.Unmarshal(m.Params, &params) != nil {
				os.Exit(9)
			}
			servers := map[string]any{"global_probe": map[string]any{"command": binary}}
			if params.CWD != nil {
				servers["project_probe"] = map[string]any{}
			}
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"config": map[string]any{"mcp_servers": servers}, "origins": map[string]any{"projects." + cwd: map[string]any{}}}})
		case "account/read":
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"requiresOpenaiAuth": true, "account": map[string]any{"type": "chatgpt", "email": nil, "planType": "plus"}}})
		case "thread/start":
			if mode == "isolation-race" || mode == "isolation-untrusted-user" {
				var params struct {
					CWD    string `json:"cwd"`
					Config struct {
						MCP      map[string]any `json:"mcp_servers"`
						Projects map[string]struct {
							Trust string `json:"trust_level"`
						} `json:"projects"`
					} `json:"config"`
				}
				if json.Unmarshal(m.Params, &params) != nil {
					os.Exit(8)
				}
				if mode == "isolation-untrusted-user" {
					if params.Config.Projects[params.CWD].Trust != "untrusted" {
						os.Exit(10)
					}
					if _, exists := params.Config.MCP["project_probe"]; exists {
						os.Exit(11)
					}
					if _, exists := params.Config.MCP["global_probe"]; !exists {
						os.Exit(12)
					}
				}
				if IsolationMarker(filepath.Join(params.CWD, "isolation-late.sentinel")) != nil {
					os.Exit(8)
				}
			}
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"thread": map[string]any{"id": "parent"}}})
		case "turn/start":
			_ = encoder.Encode(map[string]any{"id": m.ID, "result": map[string]any{"turn": map[string]any{"id": "parent-turn"}}})
			for _, target := range []struct{ thread, turn, status string }{{"child", "child-turn", "completed"}, {"parent", "parent-turn", "failed"}} {
				_ = encoder.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": target.thread, "turn": map[string]any{"id": target.turn, "status": target.status}}})
			}
		}
	}
	os.Exit(0)
}

func TestRecorder_OwnsChildrenAndAnswersServerRequests(t *testing.T) {
	b := testBundle(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"handshake", "approval", "duplicate", "eof", "malformed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if mode == "timeout" {
				var quick context.CancelFunc
				ctx, quick = context.WithTimeout(ctx, 100*time.Millisecond)
				defer quick()
			}
			var entries []Entry
			err := record(ctx, procx.Spec{Path: binary, Args: []string{"-test.run=^TestRecorderHelper$"}, Dir: t.TempDir(), Env: procx.Env().Set("SPIKE_TEST_MODE", mode).Build()}, b, "handshake", &entries)
			if mode == "handshake" || mode == "approval" {
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "transcript.jsonl")
				if err := saveRecording(filepath.Dir(path)+"-recording", entries, map[string]any{"synthetic": true}); err != nil {
					t.Fatal(err)
				}
				if err := verifyTranscript(context.Background(), b, filepath.Join(filepath.Dir(path)+"-recording", "transcript.jsonl")); err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("failed probe reported success")
			}
		})
	}
}

func TestSanitize_RemovesSecretsAndPreservesCorrelation(t *testing.T) {
	for name, raw := range map[string]string{
		"nested":             `{"id":"request","params":{"apiKey":"canary","nested":{"accessToken":"canary","email":"person@company.example"}},"result":{"id":"request"}}`,
		"escaped":            `{"text":"person@company.example /Users/person/private/file","userCode":"canary","authUrl":"https://auth.example/?token=canary"}`,
		"arrays":             `{"env":[{"name":"PASSWORD","value":"sk-ant-secret-canary-12345678901234567890"}],"refresh_token":"canary"}`,
		"opaque credentials": `{"credentials":{"arbitrary":"canary"},"id_token":"canary","message":"failed https://example.test/auth?code=canary","id":"id_canary"}`,
	} {
		t.Run(name, func(t *testing.T) {
			clean, err := Sanitize(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"canary", "person@company.example", "/Users/person"} {
				if strings.Contains(string(clean), secret) {
					t.Fatalf("sensitive string survived: %s", secret)
				}
			}
			again, err := Sanitize(clean)
			if err != nil || string(again) != string(clean) {
				t.Fatal("sanitization must be idempotent")
			}
		})
	}
	if _, err := Sanitize(json.RawMessage(`{"token":`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestScanner_EnforcesProtocolBoundary(t *testing.T) {
	// Exercise the SDK framing primitive used by the recorder, including recovery.
	s := jsonl.NewScanner(strings.NewReader(strings.Repeat("x", jsonl.DefaultMaxLine)+"\n"+strings.Repeat("x", jsonl.DefaultMaxLine+1)+"\n{}\n"), 0)
	if !s.Scan() || s.Err() != nil || len(s.Bytes()) != jsonl.DefaultMaxLine {
		t.Fatal("64 MiB line was rejected")
	}
	if !s.Scan() || !errors.Is(s.Err(), jsonl.ErrLineTooLong) {
		t.Fatal("oversize line was accepted")
	}
	if !s.Scan() || s.Err() != nil || s.Text() != "{}" {
		t.Fatal("scanner did not recover")
	}
}

func TestRun_RejectsUnsafeOrUnavailableProbes(t *testing.T) {
	t.Setenv("ARVEL_E2E", "")
	if err := Run(context.Background(), RunOptions{Mode: "record"}); err == nil {
		t.Fatal("live probe was not gated")
	}
	t.Setenv("ARVEL_E2E", "1")
	for _, o := range []RunOptions{{Mode: "sandbox"}, {Mode: "record", Out: "ignored", Scenario: "login", Home: "shared"}, {Mode: "record", Out: "ignored", Scenario: "handshake", Home: "unknown"}, {Mode: "record", Out: "ignored", Scenario: "handshake", Codex: "relative"}} {
		if err := Run(context.Background(), o); err == nil {
			t.Fatal("invalid probe accepted")
		}
	}
}

func TestProbePath_RestrictsApprovalToTheScratchFile(t *testing.T) {
	cwd := t.TempDir()
	for _, tc := range []struct {
		path    string
		allowed bool
	}{
		{filepath.Join(cwd, "arvel_probe.txt"), true},
		{"arvel_probe.txt", true},
		{filepath.Join(cwd, "other.txt"), false},
		{filepath.Join(cwd, "..", "arvel_probe.txt"), false},
	} {
		if got := allowedProbeFile(cwd, tc.path); got != tc.allowed {
			t.Errorf("allowed=%t, want %t", got, tc.allowed)
		}
	}
	canonical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if !allowedProbeFile(cwd, filepath.Join(canonical, "arvel_probe.txt")) {
		t.Fatal("canonical scratch path was refused")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("do not change"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cwd, "arvel_probe.txt")
	if err := os.Symlink(outside, link); err == nil && allowedProbeFile(cwd, link) {
		t.Fatal("symlink to an outside file was approved")
	}
}

func TestRecorder_WaitsForParentCompletion(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var entries []Entry
	err = record(ctx, procx.Spec{Path: binary, Args: []string{"-test.run=^TestRecorderHelper$"}, Dir: t.TempDir(), Env: procx.Env().Set("SPIKE_TEST_MODE", "child-completes-first").Build()}, testBundle(t), "text", &entries)
	if err == nil {
		t.Fatal("child success incorrectly completed the failed parent probe")
	}
}

func TestRequiredObservation_RejectsEchoedPrompt(t *testing.T) {
	echo := Entry{Direction: "server_to_client", Message: json.RawMessage(`{"method":"item/completed","params":{"item":{"type":"userMessage","text":"IMAGE_READY"}}}`)}
	if err := requiredObservation([]Entry{echo}, "image"); err == nil {
		t.Fatal("user echo counted as a model response")
	}
	answer := Entry{Direction: "server_to_client", Message: json.RawMessage(`{"method":"item/completed","params":{"item":{"type":"agentMessage","text":"IMAGE_READY"}}}`)}
	if err := requiredObservation([]Entry{answer}, "image"); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLogin_DisplaysChallengeWithoutPersistingIt(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var entries []Entry
	displayed := 0
	err = recordWithDeviceCode(ctx, procx.Spec{Path: binary, Args: []string{"-test.run=^TestRecorderHelper$"}, Dir: t.TempDir(), Env: procx.Env().Set("SPIKE_TEST_MODE", "device-login").Build()}, testBundle(t), "device-login", &entries, func(url, code string) error {
		if url != "https://auth.openai.com/codex/device" || code != "DEVICE-CANARY" {
			t.Error("unexpected challenge")
		}
		displayed++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if displayed != 1 {
		t.Fatalf("challenge displayed %d times", displayed)
	}
	for _, entry := range entries {
		if strings.Contains(string(entry.Message), "DEVICE-CANARY") {
			t.Fatal("challenge survived capture sanitization")
		}
	}
}

func TestCapture_RedactsChallengeEchoedInLaterText(t *testing.T) {
	var entries []Entry
	p := &peer{entries: &entries}
	for _, raw := range []string{
		`{"id":1,"result":{"userCode":"opaque-device-canary"}}`,
		`{"method":"notice","params":{"message":"challenge opaque-device-canary was rejected"}}`,
	} {
		if err := p.capture("server_to_client", json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range entries {
		if strings.Contains(string(entry.Message), "opaque-device-canary") {
			t.Fatal("known device code was persisted in later text")
		}
	}
}

func TestRun_SandboxRequiresProductionHostBoundary(t *testing.T) {
	t.Setenv("ARVEL_E2E", "1")
	output := filepath.Join(t.TempDir(), "must-not-exist")
	// A nonexistent binary proves the boundary preflight precedes vendor discovery/start.
	err := Run(context.Background(), RunOptions{Mode: "sandbox", Codex: filepath.Join(t.TempDir(), "missing-codex"), Out: output})
	if err == nil || !strings.Contains(err.Error(), "production Arvel M5c launcher") {
		t.Fatalf("expected production host boundary guidance: %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unavailable sandbox probe created output: %v", err)
	}
}
