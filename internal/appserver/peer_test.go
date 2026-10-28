// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-sdk/jsonl"
)

func peerHarness(t *testing.T, o PeerOptions) (*Peer, *jsonl.Scanner, *json.Encoder, context.Context) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	p := NewPeer(inR, outW, o)
	done := make(chan struct{})
	go func() { _ = p.Serve(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		_ = inR.Close()
		_ = inW.Close()
		_ = outR.Close()
		_ = outW.Close()
		<-done
	})
	return p, jsonl.NewScanner(outR, 0), json.NewEncoder(inW), ctx
}
func TestPeer_AnswersEveryKnownAndUnknownRequestOnce(t *testing.T) {
	_, scan, server, _ := peerHarness(t, PeerOptions{})
	for i, method := range append(append([]string{}, serverMethods...), "unrecognized") {
		id := i + 100
		if err := server.Encode(map[string]any{"id": id, "method": method, "params": map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		if !scan.Scan() {
			t.Fatal("missing response")
		}
		var m struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if m.ID != id {
			t.Fatal("response correlation lost")
		}
		if method == "unrecognized" {
			if m.Error == nil || m.Error.Code != -32601 {
				t.Fatal("unknown request not rejected")
			}
		} else if m.Error != nil {
			t.Fatal("known safe-denial failed")
		}
		if method == "item/permissions/requestApproval" && m.Result["scope"] != "turn" {
			t.Fatal("incorrect permissions denial")
		}
	}
}
func TestPeer_ConcurrentCallsCorrelateOutOfOrder(t *testing.T) {
	p, scan, server, ctx := peerHarness(t, PeerOptions{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, method := range []string{"account/read", "model/list"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result struct {
				Method string `json:"method"`
			}
			err := p.Call(ctx, method, map[string]any{}, &result)
			if err == nil && result.Method != method {
				err = ErrMethod
			}
			errs <- err
		}()
	}
	requests := []struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}{}
	for i := 0; i < 2; i++ {
		if !scan.Scan() {
			t.Fatal("missing request")
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, m)
	}
	for i := 1; i >= 0; i-- {
		if err := server.Encode(map[string]any{"id": requests[i].ID, "result": map[string]any{"method": requests[i].Method}}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestPeer_NotificationCanCallPeer(t *testing.T) {
	var p *Peer
	called := make(chan error, 1)
	o := PeerOptions{Notifications: map[string]Notification{"account/updated": func(ctx context.Context, _ json.RawMessage) {
		called <- p.Call(ctx, "model/list", map[string]any{}, nil)
	}}}
	var scan *jsonl.Scanner
	var server *json.Encoder
	var ctx context.Context
	p, scan, server, ctx = peerHarness(t, o)
	result := make(chan error, 1)
	go func() { result <- p.Call(ctx, "account/read", nil, nil) }()
	if !scan.Scan() {
		t.Fatal("missing account call")
	}
	var original struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(scan.Bytes(), &original)
	_ = server.Encode(map[string]any{"method": "account/updated", "params": map[string]any{}})
	if !scan.Scan() {
		t.Fatal("notification callback deadlocked")
	}
	var nested struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(scan.Bytes(), &nested)
	_ = server.Encode(map[string]any{"id": nested.ID, "result": map[string]any{}})
	_ = server.Encode(map[string]any{"id": original.ID, "result": map[string]any{}})
	if err := <-called; err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
func TestPeer_RejectsUnverifiedAndUnsafeOutgoing(t *testing.T) {
	p, _, _, ctx := peerHarness(t, PeerOptions{})
	for _, tc := range []struct {
		method string
		params any
	}{{"unknown", nil}, {"initialize", map[string]any{"experimentalApi": true}}, {"account/login/start", map[string]any{"type": "chatgptAuthTokens"}}, {"thread/start", map[string]any{"sandbox": "danger-full-access"}}} {
		if err := p.Call(ctx, tc.method, tc.params, nil); err != ErrMethod {
			t.Fatal("unsafe call accepted")
		}
	}
}

func TestPeer_OrdersNotificationsByThreadBeforeCallReturns(t *testing.T) {
	var order []int
	var mu sync.Mutex
	o := PeerOptions{Notifications: map[string]Notification{"thread/status/changed": func(_ context.Context, raw json.RawMessage) {
		var v struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(raw, &v)
		if v.N == 1 {
			time.Sleep(10 * time.Millisecond)
		}
		mu.Lock()
		order = append(order, v.N)
		mu.Unlock()
	}}}
	p, scan, server, ctx := peerHarness(t, o)
	done := make(chan error, 1)
	go func() { done <- p.Call(ctx, "account/read", nil, nil) }()
	if !scan.Scan() {
		t.Fatal("missing request")
	}
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(scan.Bytes(), &request)
	for _, n := range []int{1, 2} {
		_ = server.Encode(map[string]any{"method": "thread/status/changed", "params": map[string]any{"threadId": "thread", "n": n}})
	}
	_ = server.Encode(map[string]any{"id": request.ID, "result": map[string]any{}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatal("notification order/barrier lost")
	}
}
func TestPeer_DeniesPendingApprovalDuringShutdown(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	p := NewPeer(inR, outW, PeerOptions{Requests: map[string]Handler{"item/commandExecution/requestApproval": func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		return map[string]any{"decision": "cancel"}, nil
	}}})
	done := make(chan struct{})
	go func() { _ = p.Serve(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		<-done
	})
	received := make(chan []byte, 1)
	go func() {
		scan := jsonl.NewScanner(outR, 0)
		if scan.Scan() {
			received <- append([]byte{}, scan.Bytes()...)
		} else {
			received <- nil
		}
	}()
	if err := json.NewEncoder(inW).Encode(map[string]any{"id": "pending", "method": "item/commandExecution/requestApproval", "params": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	inR.Close()
	select {
	case raw := <-received:
		var m struct {
			ID     string `json:"id"`
			Result struct {
				Decision string `json:"decision"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &m) != nil || m.ID != "pending" || m.Result.Decision != "cancel" {
			t.Fatal("approval was not answered during shutdown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown reply timed out")
	}
	<-done
}

func TestPeer_RejectsPersistentRuleHandlerReply(t *testing.T) {
	_, scan, server, _ := peerHarness(t, PeerOptions{Requests: map[string]Handler{"item/commandExecution/requestApproval": func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"decision": "acceptWithExecpolicyAmendment"}, nil
	}}})
	_ = server.Encode(map[string]any{"id": 1, "method": "item/commandExecution/requestApproval", "params": map[string]any{}})
	if !scan.Scan() {
		t.Fatal("no response")
	}
	var m struct {
		Error  json.RawMessage `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(scan.Bytes(), &m)
	if len(m.Error) == 0 || len(m.Result) > 0 {
		t.Fatal("persistent rule reply reached vendor")
	}
}
