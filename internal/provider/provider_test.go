// SPDX-License-Identifier: Apache-2.0

package provider_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/testkit/mockhost"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/provider"
)

func TestProvider_ReportsBootstrapWithoutVendorAccess(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := mockhost.InProcess(t, provider.New())
	const canary = "sk-bootstrap-canary-never-echo"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := h.InitializeCtx(ctx, protocol.InitMeta{PluginAPI: protocol.PluginAPI,
		DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: "api_key", Secrets: map[string]string{"apiKey": canary}}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := h.Status()
	if err != nil || status.State != protocol.StateError || status.Message == "" {
		t.Fatalf("status: %+v, %v", status, err)
	}
	usage, err := h.Usage()
	if err != nil || usage.Source != protocol.UsageSourceUnknown || len(usage.Windows) != 0 || usage.Spend != nil {
		t.Fatalf("usage: %+v, %v", usage, err)
	}
	for name, call := range map[string]func() error{
		"models":  func() error { _, err := h.Models(false); return err },
		"action":  func() error { _, err := h.Action("login"); return err },
		"session": func() error { _, err := h.NewSession(protocol.SessionMeta{}); return err },
	} {
		t.Run(name, func(t *testing.T) {
			var rpc *protocol.RPCError
			if err := call(); !errors.As(err, &rpc) || rpc.Code != protocol.CodeServerError {
				t.Fatalf("expected typed provider error: %v", err)
			}
		})
	}
	var rpc *protocol.RPCError
	if err := h.Call(ctx, "_arvel/unknown", nil, nil); !errors.As(err, &rpc) || rpc.Code != protocol.CodeMethodNotFound {
		t.Fatalf("unknown method: %v", err)
	}
	h.Shutdown()
	if strings.Contains(h.Stdout()+h.Stderr(), canary) {
		t.Fatal("credential leaked")
	}
}

func TestProvider_DoesNotAdvertiseUnimplementedFeatures(t *testing.T) {
	p := provider.New()
	f, err := p.Initialize(context.Background(), protocol.InitMeta{})
	if err != nil || f.LoadSession || f.Subagents || f.PromptImage || f.Skills != nil || f.MCPHTTP || f.MCPSSE {
		t.Fatalf("features: %+v, %v", f, err)
	}
	if s, err := p.LoadSession(context.Background(), plugin.LoadParams{}); s != nil || err == nil {
		t.Fatal("bootstrap resumed a session")
	}
}
