// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/arvel-dev/arvel-sdk/logx"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/redact"
)

func TestDiagnostics_SplitSecretsAndOversizedLines(t *testing.T) {
	var out bytes.Buffer
	rd := redact.New()
	rd.Add("opaque-secret-canary")
	s := stderrLines{log: logx.NewWith("test", logx.Options{Writer: &out, Level: slog.LevelDebug, Redactor: rd})}
	_, _ = s.Write([]byte("opaque-secret-"))
	_, _ = s.Write([]byte("canary\n"))
	_, _ = s.Write([]byte(strings.Repeat("x", stderrLimit) + "opaque-secret-canary\n"))
	s.flush()
	if strings.Contains(out.String(), "opaque-secret") || strings.Contains(out.String(), strings.Repeat("x", 100)) {
		t.Fatal("sensitive/oversized stderr persisted")
	}
	if !strings.Contains(out.String(), "omitted") {
		t.Fatal("missing bounded-line diagnostic")
	}
}

func TestPeer_RPCErrorOmitsSecretData(t *testing.T) {
	const secret = "opaque-login-secret-canary"
	p, scan, server, ctx := peerHarness(t, PeerOptions{})
	done := make(chan error, 1)
	go func() {
		done <- p.Call(ctx, "account/login/start", map[string]any{"type": "apiKey", "apiKey": secret}, nil)
	}()
	if !scan.Scan() {
		t.Fatal("missing request")
	}
	var m struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(scan.Bytes(), &m)
	_ = server.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": "rejected " + secret, "data": map[string]any{"secret": secret}}})
	err := <-done
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("RPC error exposed credentials")
	}
	var rpcError *protocol.RPCError
	if !errors.As(err, &rpcError) || rpcError.Code != -32000 || rpcError.Data != nil {
		t.Fatal("RPC code/data safety contract lost")
	}
}

func TestPeer_RegistersReturnedDeviceChallenge(t *testing.T) {
	const code = "opaque-device-code-canary"
	p, scan, server, ctx := peerHarness(t, PeerOptions{})
	done := make(chan error, 1)
	go func() {
		var result map[string]any
		done <- p.Call(ctx, "account/login/start", map[string]any{"type": "chatgptDeviceCode"}, &result)
	}()
	if !scan.Scan() {
		t.Fatal("missing login request")
	}
	var m struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(scan.Bytes(), &m)
	_ = server.Encode(map[string]any{"id": m.ID, "result": map[string]any{"type": "chatgptDeviceCode", "userCode": code}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- p.Call(ctx, "account/read", nil, nil) }()
	if !scan.Scan() {
		t.Fatal("missing account request")
	}
	_ = json.Unmarshal(scan.Bytes(), &m)
	_ = server.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32000, "message": "challenge " + code}})
	err := <-done
	if err == nil || strings.Contains(err.Error(), code) {
		t.Fatal("returned challenge leaked through later error")
	}
}
