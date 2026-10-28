// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

type fakeClient struct {
	mu           sync.Mutex
	account      *schema.Account
	calls        []string
	opts         appserver.Options
	done         chan struct{}
	once         sync.Once
	login        int
	keyError     bool
	readBlock    <-chan struct{}
	loginBlock   <-chan struct{}
	early        bool
	invalid      bool
	pages        map[string]schema.ModelListResponse
	modelError   bool
	modelParams  []schema.ModelListParams
	onModel      func()
	quota        any
	quotaError   bool
	accountError bool
	onQuota      func()
}

func (f *fakeClient) Call(ctx context.Context, method string, params, result any) error {
	f.mu.Lock()
	f.calls = append(f.calls, method)
	block := f.readBlock
	if method == "account/login/start" {
		block = f.loginBlock
	} else if method != "account/read" {
		block = nil
	}
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var response any
	switch method {
	case "account/read":
		if f.accountError {
			return errors.New("account unavailable")
		}
		response = schema.AccountReadResponse{Account: f.account, RequiresOpenAIAuth: true}
	case "account/rateLimits/read":
		if f.onQuota != nil {
			f.onQuota()
		}
		if f.quotaError {
			return errors.New("synthetic quota error")
		}
		response = map[string]any{"rateLimits": f.quota}
	case "account/logout":
		f.account = nil
		response = struct{}{}
	case "account/login/start":
		p := params.(schema.LoginParams)
		if f.keyError {
			return errors.New("sk-vendor-error-canary")
		}
		if p.Type == "apiKey" {
			f.account = &schema.Account{Type: "apiKey"}
			response = schema.LoginResponse{Type: "apiKey"}
			break
		}
		f.login++
		id := "login-a"
		if f.login > 1 {
			id = "login-b"
		}
		if f.early {
			f.account = &schema.Account{Type: "chatgpt", PlanType: "plus"}
			raw, _ := json.Marshal(schema.LoginCompleted{LoginID: &id, Success: true})
			f.opts.Peer.Notifications["account/login/completed"](ctx, raw)
		}
		r := schema.LoginResponse{Type: p.Type, LoginID: id, AuthURL: "https://auth.openai.com/sensitive-challenge", VerificationURL: "https://auth.openai.com/device", UserCode: "private-device-code"}
		if f.invalid {
			r.AuthURL = "file:///unsafe"
			r.VerificationURL = "file:///unsafe"
		}
		response = r
	case "account/login/cancel":
		response = map[string]any{"status": "canceled"}
	case "model/list":
		p := params.(schema.ModelListParams)
		f.modelParams = append(f.modelParams, p)
		if f.modelError {
			return errors.New("catalog failed")
		}
		if f.onModel != nil {
			f.onModel()
		}
		if f.pages != nil {
			cursor := ""
			if p.Cursor != nil {
				cursor = *p.Cursor
			}
			response = f.pages[cursor]
			break
		}
		response = schema.ModelListResponse{Data: []schema.Model{{Model: "model-one", DisplayName: "One", IsDefault: true}}}
	default:
		return errors.New("unexpected fake method")
	}
	raw, _ := json.Marshal(response)
	return json.Unmarshal(raw, result)
}
func (f *fakeClient) Close(context.Context) error { f.once.Do(func() { close(f.done) }); return nil }
func (f *fakeClient) Done() <-chan struct{}       { return f.done }
func (f *fakeClient) Err() error                  { return appserver.ErrExited }
func (f *fakeClient) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}
func (f *fakeClient) update(a *schema.Account) {
	f.mu.Lock()
	f.account = a
	h := f.opts.Peer.Notifications["account/updated"]
	f.mu.Unlock()
	h(context.Background(), json.RawMessage(`{"authMode":"chatgpt"}`))
}
func testManager(t *testing.T, kind string, separate bool, f *fakeClient, d dependencies, changed func(protocol.Status)) *Manager {
	t.Helper()
	if f.done == nil {
		f.done = make(chan struct{})
	}
	if d.start == nil {
		d.start = func(_ context.Context, o appserver.Options) (Client, error) {
			f.mu.Lock()
			f.opts = o
			f.mu.Unlock()
			return f, nil
		}
	}
	if d.find == nil {
		d.find = func(config) (string, error) { return "/fake/codex", nil }
	}
	if d.version == nil {
		d.version = func(context.Context, string) (string, error) { return "0.164.0", nil }
	}
	if d.poll == 0 {
		d.poll = time.Hour
	}
	meta := protocol.InitMeta{DataDir: t.TempDir(), Connection: protocol.ConnectionInfo{Kind: kind, Config: map[string]any{"separateLogin": separate}, Secrets: map[string]string{"apiKey": "sk-test-secret"}}}
	m, err := newManager(meta, changed, slog.New(slog.NewTextHandler(io.Discard, nil)), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := m.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return m
}
func statusState(t *testing.T, m *Manager, want protocol.StatusState) protocol.Status {
	t.Helper()
	s, err := m.Status(context.Background())
	if err != nil || s.State != want {
		t.Fatalf("status %+v %v, want %s", s, err, want)
	}
	return s
}
func TestManager_StatusMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		account    *schema.Account
		reject     bool
		want       protocol.StatusState
	}{
		{"signed out", "chatgpt", nil, false, protocol.StateNeedsLogin},
		{"wrong kind", "chatgpt", &schema.Account{Type: "apiKey"}, false, protocol.StateNeedsLogin},
		{"chatgpt", "chatgpt", &schema.Account{Type: "chatgpt", PlanType: "plus"}, false, protocol.StateReady},
		{"api", "api_key", nil, false, protocol.StateReady},
		{"stale private account", "api_key", &schema.Account{Type: "chatgpt"}, false, protocol.StateReady},
		{"rejected key", "api_key", nil, true, protocol.StateNeedsLogin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClient{account: tc.account, keyError: tc.reject}
			m := testManager(t, tc.kind, false, f, dependencies{}, nil)
			s := statusState(t, m, tc.want)
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			if tc.name == "stale private account" && f.count("account/logout") != 1 {
				t.Fatal("stale auth not replaced")
			}
		})
	}
}
func TestManager_InitializeIsLazyAndStartupIsSerialized(t *testing.T) {
	var starts atomic.Int32
	f := &fakeClient{done: make(chan struct{})}
	m := testManager(t, "chatgpt", false, f, dependencies{start: func(_ context.Context, o appserver.Options) (Client, error) {
		starts.Add(1)
		f.mu.Lock()
		f.opts = o
		f.mu.Unlock()
		return f, nil
	}}, nil)
	if starts.Load() != 0 {
		t.Fatal("eager startup")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { _, _ = m.Status(context.Background()) })
	}
	wg.Wait()
	if starts.Load() != 1 {
		t.Fatalf("starts %d", starts.Load())
	}
}
func TestManager_DiscoveryFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    dependencies
		want protocol.StatusState
	}{
		{"missing", dependencies{find: func(config) (string, error) { return "", errors.New("missing") }}, protocol.StateNotInstalled},
		{"outdated", dependencies{version: func(context.Context, string) (string, error) { return "0.121.0", nil }}, protocol.StateOutdated},
		{"version error", dependencies{version: func(context.Context, string) (string, error) { return "", errors.New("bad") }}, protocol.StateError},
		{"start error", dependencies{start: func(context.Context, appserver.Options) (Client, error) { return nil, errors.New("bad") }}, protocol.StateError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t, "chatgpt", false, &fakeClient{}, tc.d, nil)
			statusState(t, m, tc.want)
		})
	}
}
func TestManager_RequestCancellationPreservesProcessAndShutdownCancelsWait(t *testing.T) {
	f := &fakeClient{}
	m := testManager(t, "chatgpt", false, f, dependencies{}, nil)
	statusState(t, m, protocol.StateNeedsLogin)
	block := make(chan struct{})
	f.mu.Lock()
	f.readBlock = block
	f.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Status(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel %v", err)
	}
	select {
	case <-f.done:
		t.Fatal("request cancellation closed process")
	default:
	}
	done := make(chan struct{})
	go func() { _, _ = m.Status(context.Background()); close(done) }()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := m.Close(shutdown); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("operation not released")
	}
	if _, err := m.Status(context.Background()); err == nil {
		t.Fatal("used closed manager")
	}
}

func TestManager_LoginActionsAndPrivateLogout(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "browser", true: "device"}[remote], func(t *testing.T) {
			f := &fakeClient{}
			m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
			m.cfg.remote = remote
			result, err := m.Action(context.Background(), "login")
			if err != nil || result.Validate() != nil || !result.AwaitCompletion {
				t.Fatalf("login: %+v %v", result, err)
			}
			if remote && result.Explain != "Enter this code to finish ChatGPT sign-in: private-device-code" {
				t.Fatal("device code absent")
			}
			if _, err := m.Action(context.Background(), "login"); err != nil {
				t.Fatal(err)
			}
			if f.count("account/login/cancel") != 1 {
				t.Fatal("previous login not cancelled")
			}
			if _, err := m.Action(context.Background(), "logout_arvel_login"); err != nil {
				t.Fatal(err)
			}
			if f.count("account/logout") != 1 {
				t.Fatal("private logout missing")
			}
		})
	}
	for _, kind := range []string{"chatgpt", "api_key"} {
		f := &fakeClient{}
		m := testManager(t, kind, false, f, dependencies{}, nil)
		if _, err := m.Action(context.Background(), "logout_arvel_login"); err == nil {
			t.Fatal("unsafe logout accepted")
		}
		if _, err := m.Action(context.Background(), "unknown"); err == nil {
			t.Fatal("unknown action accepted")
		}
		if f.count("account/logout") != 0 {
			t.Fatal("unsafe vendor logout")
		}
	}
}
func TestManager_LoginFailureAndCancellation(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		f := &fakeClient{invalid: invalid, keyError: !invalid}
		m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
		if _, err := m.Action(context.Background(), "login"); err == nil {
			t.Fatal("invalid login succeeded")
		}
		if invalid && f.count("account/login/cancel") != 1 {
			t.Fatal("malformed challenge left pending")
		}
	}
	f := &fakeClient{loginBlock: make(chan struct{})}
	m := testManager(t, "chatgpt", false, f, dependencies{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Action(ctx, "login"); err == nil {
		t.Fatal("cancelled login succeeded")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("abandoned login process left alive")
	}
}
func TestManager_InstallHelpIsLocal(t *testing.T) {
	f := &fakeClient{}
	m := testManager(t, "chatgpt", false, f, dependencies{find: func(config) (string, error) { t.Error("help searched vendor"); return "", errors.New("unexpected") }}, nil)
	r, err := m.Action(context.Background(), "install_help")
	if err != nil || r.URL != installURL {
		t.Fatal("missing install URL")
	}
}

func awaitState(t *testing.T, changes <-chan protocol.Status, want protocol.StatusState) protocol.Status {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case st := <-changes:
			if st.State == want {
				return st
			}
		case <-timer.C:
			t.Fatalf("missing %s notification", want)
			return protocol.Status{}
		}
	}
}
func TestManager_EventsAndEarlyLoginCompletion(t *testing.T) {
	changes := make(chan protocol.Status, 16)
	f := &fakeClient{early: true}
	m := testManager(t, "chatgpt", true, f, dependencies{}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateNeedsLogin)
	if _, err := m.Action(context.Background(), "login"); err != nil {
		t.Fatal(err)
	}
	awaitState(t, changes, protocol.StateReady)
	_, release, err := m.operation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.pending != "" {
		t.Error("early completion not correlated")
	}
	release()
	f.update(nil)
	awaitState(t, changes, protocol.StateNeedsLogin)
	f.update(nil)
	// Synchronize worker by taking the gate after a completed authoritative read.
	statusState(t, m, protocol.StateNeedsLogin)
	select {
	case st := <-changes:
		t.Fatalf("duplicate status: %+v", st)
	default:
	}
}
func TestManager_StaleCompletionDoesNotClearNewLogin(t *testing.T) {
	f := &fakeClient{}
	m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
	for range 2 {
		if _, err := m.Action(context.Background(), "login"); err != nil {
			t.Fatal(err)
		}
	}
	id := "login-a"
	raw, _ := json.Marshal(schema.LoginCompleted{LoginID: &id})
	m.loginEvent(context.Background(), raw)
	// Drain under the operation gate to deterministically inspect the pending ID.
	_, release, err := m.operation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.drainEvents()
	if m.pending != "login-b" {
		t.Error("stale completion cleared new attempt")
	}
	release()
	m.loginEvent(context.Background(), json.RawMessage(`{"loginId":null,"success":true}`))
	_, release, err = m.operation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.drainEvents()
	if m.pending != "login-b" {
		t.Error("null completion cleared browser login")
	}
	release()
}
func TestManager_PollDetectsExternalLoginAndCrash(t *testing.T) {
	changes := make(chan protocol.Status, 16)
	f := &fakeClient{}
	m := testManager(t, "chatgpt", false, f, dependencies{poll: 10 * time.Millisecond}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateNeedsLogin)
	f.mu.Lock()
	f.account = &schema.Account{Type: "chatgpt"}
	f.mu.Unlock()
	awaitState(t, changes, protocol.StateReady)
	_ = f.Close(context.Background())
	awaitState(t, changes, protocol.StateError)
}
func TestManager_RefreshTimeoutReleasesGate(t *testing.T) {
	changes := make(chan protocol.Status, 16)
	f := &fakeClient{}
	m := testManager(t, "chatgpt", false, f, dependencies{timeout: 20 * time.Millisecond}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateNeedsLogin)
	f.mu.Lock()
	blocked := make(chan struct{})
	f.readBlock = blocked
	f.mu.Unlock()
	m.signal()
	awaitState(t, changes, protocol.StateError)
	f.mu.Lock()
	f.readBlock = nil
	close(blocked)
	f.mu.Unlock()
	statusState(t, m, protocol.StateNeedsLogin)
}

func TestManager_ModelPaginationCacheAndInvalidation(t *testing.T) {
	next := "next"
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, pages: map[string]schema.ModelListResponse{
		"":     {Data: []schema.Model{{Model: "hidden-default", DisplayName: "Default", Hidden: true, IsDefault: true}}, NextCursor: &next},
		"next": {Data: []schema.Model{{Model: "visible", DisplayName: "Visible"}}},
	}}
	now := time.Now()
	m := testManager(t, "chatgpt", false, f, dependencies{now: func() time.Time { return now }}, nil)
	statusState(t, m, protocol.StateReady)
	// Worker never calls now; all model calls below are serialized.
	v, err := m.Models(context.Background(), false)
	if err != nil || v.Default != "hidden-default" || len(v.Models) != 2 {
		t.Fatalf("catalog %+v %v", v, err)
	}
	f.mu.Lock()
	for _, p := range f.modelParams {
		if p.IncludeHidden == nil || !*p.IncludeHidden {
			t.Error("hidden models not requested")
		}
	}
	f.mu.Unlock()
	count := f.count("model/list")
	if _, err := m.Models(context.Background(), false); err != nil || f.count("model/list") != count {
		t.Fatal("cache not used")
	}
	if _, err := m.Models(context.Background(), true); err != nil || f.count("model/list") != count+2 {
		t.Fatal("refresh ignored")
	}
	now = now.Add(6 * time.Hour)
	if _, err := m.Models(context.Background(), false); err != nil || f.count("model/list") != count+4 {
		t.Fatal("TTL ignored")
	}
	m.accountEvent(context.Background(), nil)
	if _, err := m.Models(context.Background(), false); err != nil || f.count("model/list") != count+6 {
		t.Fatal("event invalidation ignored")
	}
	f.mu.Lock()
	f.modelError = true
	f.mu.Unlock()
	if _, err := m.Models(context.Background(), true); err == nil {
		t.Fatal("failed refresh succeeded")
	}
	if _, err := m.Models(context.Background(), false); err != nil {
		t.Fatal("failed refresh destroyed complete cache")
	}
}
func TestManager_ModelCyclesAndAccountChangeDuringFetch(t *testing.T) {
	next := "repeat"
	f := &fakeClient{account: &schema.Account{Type: "chatgpt"}, pages: map[string]schema.ModelListResponse{"": {NextCursor: &next}, "repeat": {NextCursor: &next}}}
	m := testManager(t, "chatgpt", false, f, dependencies{}, nil)
	if _, err := m.Models(context.Background(), false); err == nil {
		t.Fatal("cursor cycle accepted")
	}
	f.mu.Lock()
	f.pages = nil
	f.onModel = func() { m.accountEvent(context.Background(), nil) }
	f.mu.Unlock()
	if _, err := m.Models(context.Background(), false); err == nil {
		t.Fatal("published catalog across account change")
	}
}

func TestManager_MissingKeyDoesNotStartVendor(t *testing.T) {
	f := &fakeClient{}
	m := testManager(t, "api_key", false, f, dependencies{find: func(config) (string, error) {
		t.Error("missing key started discovery")
		return "", errors.New("unexpected")
	}}, nil)
	m.cfg.key = ""
	statusState(t, m, protocol.StateNeedsLogin)
}
func TestManager_CrashRestartsOnlyOnExplicitUse(t *testing.T) {
	var starts atomic.Int32
	var clientsMu sync.Mutex
	var clients []*fakeClient
	changes := make(chan protocol.Status, 16)
	m := testManager(t, "chatgpt", false, &fakeClient{}, dependencies{start: func(_ context.Context, o appserver.Options) (Client, error) {
		f := &fakeClient{done: make(chan struct{}), opts: o}
		clientsMu.Lock()
		clients = append(clients, f)
		clientsMu.Unlock()
		starts.Add(1)
		return f, nil
	}}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateNeedsLogin)
	clientsMu.Lock()
	first := clients[0]
	clientsMu.Unlock()
	_ = first.Close(context.Background())
	awaitState(t, changes, protocol.StateError)
	if starts.Load() != 1 {
		t.Fatal("worker silently restarted vendor")
	}
	statusState(t, m, protocol.StateNeedsLogin)
	if starts.Load() != 2 {
		t.Fatal("explicit use did not restart")
	}
}
func TestManager_CancelledStartupReapsClient(t *testing.T) {
	f := &fakeClient{done: make(chan struct{})}
	m := testManager(t, "chatgpt", false, f, dependencies{start: func(ctx context.Context, _ appserver.Options) (Client, error) { <-ctx.Done(); return f, ctx.Err() }}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := m.Status(ctx); err == nil {
		t.Fatal("cancelled startup succeeded")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("incomplete startup was not reaped")
	}
}

func awaitMessage(t *testing.T, changes <-chan protocol.Status, want string) protocol.Status {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case st := <-changes:
			if st.Message == want {
				return st
			}
		case <-timer.C:
			t.Fatalf("missing login completion message %q", want)
			return protocol.Status{}
		}
	}
}
func TestManager_FailedLoginNotifiesAndRetryHasFreshStatus(t *testing.T) {
	changes := make(chan protocol.Status, 16)
	f := &fakeClient{}
	m := testManager(t, "chatgpt", true, f, dependencies{}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateNeedsLogin)
	if _, err := m.Action(context.Background(), "login"); err != nil {
		t.Fatal(err)
	}
	fail := func(id string) {
		raw, _ := json.Marshal(schema.LoginCompleted{LoginID: &id, Success: false})
		m.loginEvent(context.Background(), raw)
	}
	fail("login-a")
	st := awaitMessage(t, changes, "ChatGPT sign-in failed or was cancelled; try again")
	if st.State != protocol.StateNeedsLogin {
		t.Fatalf("failed login changed readiness: %+v", st)
	}
	if _, err := m.Action(context.Background(), "login"); err != nil {
		t.Fatal(err)
	}
	awaitMessage(t, changes, "Complete ChatGPT sign-in")
	fail("login-a")
	_, release, err := m.operation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.drainEvents()
	if m.pending != "login-b" {
		t.Error("old failure cleared retry")
	}
	release()
	fail("login-b")
	awaitMessage(t, changes, "ChatGPT sign-in failed or was cancelled; try again")
}

func TestManager_FailedReplacementLoginPreservesExistingAccount(t *testing.T) {
	changes := make(chan protocol.Status, 16)
	f := &fakeClient{account: &schema.Account{Type: "chatgpt", PlanType: "plus"}}
	m := testManager(t, "chatgpt", false, f, dependencies{}, func(st protocol.Status) { changes <- st })
	statusState(t, m, protocol.StateReady)
	if _, err := m.Action(context.Background(), "login"); err != nil {
		t.Fatal(err)
	}
	id := "login-a"
	raw, _ := json.Marshal(schema.LoginCompleted{LoginID: &id, Success: false})
	m.loginEvent(context.Background(), raw)
	st := awaitMessage(t, changes, "ChatGPT sign-in failed or was cancelled; try again")
	if st.State != protocol.StateReady || st.Account == nil || st.Account.Plan != "plus" {
		t.Fatalf("existing account lost: %+v", st)
	}
}

func (f *fakeClient) WaitResponses(ctx context.Context, _, _ string) error { return ctx.Err() }
