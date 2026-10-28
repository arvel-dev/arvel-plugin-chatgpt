// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func readyFake() *fakeClient {
	return &fakeClient{done: make(chan struct{}), account: &schema.Account{Type: "chatgpt", PlanType: "plus"}}
}

func TestRuntime_PrivateLogoutRetiresBorrowedSessions(t *testing.T) {
	f := readyFake()
	stopped := make(chan uint64, 1)
	m := testManager(t, "chatgpt", true, f, dependencies{hooks: Hooks{Stopped: func(g uint64) { stopped <- g }}}, nil)
	runtime, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "logout_arvel_login"); err != nil {
		t.Fatal(err)
	}
	select {
	case generation := <-stopped:
		if generation != runtime.Generation {
			t.Fatal("wrong generation retired")
		}
	default:
		t.Fatal("logout left borrowed session runtime authorized")
	}
	select {
	case <-runtime.Client.Done():
	default:
		t.Fatal("logged-out runtime kept running")
	}
}

func TestRuntime_AccountPrincipalChangeRetiresBorrowedGeneration(t *testing.T) {
	f := readyFake()
	stopped := make(chan uint64, 2)
	m := testManager(t, "chatgpt", true, f, dependencies{hooks: Hooks{Stopped: func(g uint64) { stopped <- g }}, start: func(context.Context, appserver.Options) (Client, error) {
		next := readyFake()
		email := "next@example.invalid"
		next.account.Email = &email
		return next, nil
	}}, nil)
	first, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current := first.Client.(*fakeClient)
	current.mu.Lock()
	email := "changed@example.invalid"
	current.account.Email = &email
	current.mu.Unlock()
	if _, err := m.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case generation := <-stopped:
		if generation != first.Generation {
			t.Fatal("wrong generation")
		}
	default:
		t.Fatal("account change retained native approval context")
	}
}

func TestRuntime_FailedReplacementLoginPreservesBorrowedPrincipal(t *testing.T) {
	f := readyFake()
	m := testManager(t, "chatgpt", true, f, dependencies{}, nil)
	first, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "login"); err != nil {
		t.Fatal(err)
	}
	login := "login-a"
	raw, _ := json.Marshal(schema.LoginCompleted{LoginID: &login, Success: false})
	f.opts.Peer.Notifications["account/login/completed"](context.Background(), raw)
	_ = m.refresh()
	st, err := m.Status(context.Background())
	if err != nil || st.State != protocol.StateReady || st.Account == nil {
		t.Fatalf("existing account lost: %+v %v", st, err)
	}
	second, err := m.Acquire(context.Background())
	if err != nil || second.Generation != first.Generation {
		t.Fatalf("unchanged principal was retired: %v", err)
	}
}

type blockingRuntimeClient struct {
	*fakeClient
	entered, release chan struct{}
}

func (c *blockingRuntimeClient) Call(ctx context.Context, method string, p, r any) error {
	if method == "turn/start" {
		close(c.entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.release:
			return nil
		}
	}
	return c.fakeClient.Call(ctx, method, p, r)
}

func TestRuntime_BorrowsOneProcessWithoutHoldingAccountGate(t *testing.T) {
	f := readyFake()
	client := &blockingRuntimeClient{fakeClient: f, entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	m := testManager(t, "chatgpt", true, f, dependencies{start: func(_ context.Context, o appserver.Options) (Client, error) { f.opts = o; return client, nil }}, nil)
	t.Cleanup(func() { once.Do(func() { close(client.release) }) })
	a, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Acquire(context.Background())
	if err != nil || a.Client != b.Client || a.Generation != b.Generation {
		t.Fatal("did not share runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Client.Call(ctx, "turn/start", nil, nil) }()
	<-client.entered
	st, err := m.Status(ctx)
	if err != nil || st.State != protocol.StateReady {
		t.Fatal("borrowed turn blocked account status")
	}
	once.Do(func() { close(client.release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRuntime_GenerationAbortCannotKillReplacement(t *testing.T) {
	var mu sync.Mutex
	var started, stopped []uint64
	f := readyFake()
	d := dependencies{start: func(_ context.Context, o appserver.Options) (Client, error) {
		next := readyFake()
		next.opts = o
		return next, nil
	}, hooks: Hooks{ForGeneration: func(g uint64) appserver.PeerOptions {
		mu.Lock()
		started = append(started, g)
		mu.Unlock()
		return appserver.PeerOptions{}
	}, Stopped: func(g uint64) { mu.Lock(); stopped = append(stopped, g); mu.Unlock() }}}
	m := testManager(t, "chatgpt", true, f, d, nil)
	a, err := m.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Abort(context.Background(), a.Generation); err != nil {
		t.Fatal(err)
	}
	b, err := m.Acquire(context.Background())
	if err != nil || b.Generation <= a.Generation {
		t.Fatalf("restart failed: %+v %v", b, err)
	}
	if err := m.Abort(context.Background(), a.Generation); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Client.Done():
		t.Fatal("stale abort killed replacement")
	default:
	}
	mu.Lock()
	defer mu.Unlock()
	if len(started) != 2 || len(stopped) != 1 || stopped[0] != a.Generation {
		t.Fatalf("wrong generation callbacks: %v %v", started, stopped)
	}
}

func TestRuntime_LegacySharedHomeNeverStartsAgentProcess(t *testing.T) {
	f := readyFake()
	m := testManager(t, "chatgpt", false, f, dependencies{}, nil)
	if _, err := m.Acquire(context.Background()); err == nil {
		t.Fatal("shared login acquired session runtime")
	}
	if f.count("account/read") != 0 {
		t.Fatal("session gate started shared account process")
	}
}
