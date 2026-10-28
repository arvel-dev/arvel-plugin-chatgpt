// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/models"
	"github.com/arvel-dev/arvel-sdk/logx"
	"github.com/arvel-dev/arvel-sdk/procx"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/redact"
)

// Client is the owned G1 process interface, also used by deterministic fakevendor tests.
type Client interface {
	Call(context.Context, string, any, any) error
	Close(context.Context) error
	Done() <-chan struct{}
	Err() error
	WaitResponses(context.Context, string, string) error
}

type dependencies struct {
	start         func(context.Context, appserver.Options) (Client, error)
	find          func(config) (string, error)
	version       func(context.Context, string) (string, error)
	poll, timeout time.Duration
	now           func() time.Time
	hooks         Hooks
}

// Manager serializes account operations and owns the process and refresh worker.
type Manager struct {
	cfg                           config
	deps                          dependencies
	log                           *slog.Logger
	redactor                      *redact.Redactor
	changed                       func(protocol.Status)
	ctx                           context.Context
	cancel                        context.CancelFunc
	done                          chan struct{}
	gate                          chan struct{}
	wake                          chan struct{}
	revision                      atomic.Uint64
	eventsMu                      sync.Mutex
	completed                     map[string]bool
	client                        Client
	retiring                      Client
	generation, stoppedGeneration uint64
	binary                        *protocol.BinaryInfo
	keyApplied                    bool
	borrowed                      bool
	loginFailed                   bool
	pending                       string
	identity                      string
	last                          protocol.Status
	haveLast                      bool
	catalog                       models.Cache
	quota                         quotaState
}

// New validates local configuration without starting a vendor or contacting the network.
func New(meta protocol.InitMeta, changed func(protocol.Status), logger *slog.Logger) (*Manager, error) {
	return newManager(meta, changed, logger, dependencies{})
}

func newManager(meta protocol.InitMeta, changed func(protocol.Status), logger *slog.Logger, d dependencies) (*Manager, error) {
	cfg, err := parseConfig(meta)
	if err != nil {
		return nil, protocol.NewError(protocol.ErrProviderError, err.Error(), nil)
	}
	if d.start == nil {
		d.start = func(ctx context.Context, o appserver.Options) (Client, error) { return appserver.Start(ctx, o) }
	}
	if d.find == nil {
		d.find = findBinary
	}
	if d.version == nil {
		d.version = version
	}
	if d.poll <= 0 {
		d.poll = 30 * time.Second
	}
	if d.timeout <= 0 {
		d.timeout = 15 * time.Second
	}
	if d.now == nil {
		d.now = time.Now
	}
	if logger == nil {
		logger = logx.New("chatgpt-connection")
	}
	rd := redact.New()
	rd.Add(cfg.key)
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{cfg: cfg, deps: d, log: logger, redactor: rd, changed: changed, ctx: ctx, cancel: cancel, done: make(chan struct{}), gate: make(chan struct{}, 1), wake: make(chan struct{}, 1), completed: map[string]bool{}}
	m.gate <- struct{}{}
	go m.run()
	return m, nil
}

func (m *Manager) operation(ctx context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, m.deps.timeout)
	stop := context.AfterFunc(m.ctx, cancel)
	release := func() { stop(); cancel() }
	select {
	case <-ctx.Done():
		release()
		return nil, nil, ctx.Err()
	case <-m.gate:
	}
	if err := m.ctx.Err(); err != nil {
		m.gate <- struct{}{}
		release()
		return nil, nil, err
	}
	return ctx, func() { m.gate <- struct{}{}; release() }, nil
}

// Status lazily starts the vendor and reads the authoritative account.
func (m *Manager) Status(ctx context.Context) (protocol.Status, error) {
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return protocol.Status{}, err
	}
	defer release()
	m.log.Debug("connection status requested")
	st := m.status(ctx, true)
	m.remember(st, false)
	if err := ctx.Err(); err != nil {
		return protocol.Status{}, err
	}
	return st, nil
}

func (m *Manager) status(ctx context.Context, allowStart bool) protocol.Status {
	if m.cfg.kind == "api_key" && m.cfg.key == "" {
		return m.state(protocol.StateNeedsLogin, "Add an OpenAI API key")
	}
	if m.client != nil {
		select {
		case <-m.client.Done():
			m.reset()
		default:
		}
	}
	if m.client == nil {
		if !allowStart {
			return m.state(protocol.StateError, "Codex app-server stopped")
		}
		if st, ok := m.start(ctx); !ok {
			return st
		}
	}
	var response schema.AccountReadResponse
	accountRevision := m.revision.Load()
	if err := m.client.Call(ctx, "account/read", schema.AccountReadParams{}, &response); err != nil {
		m.invalidateQuotaObservation()
		m.quotaReady(false)
		m.signalUsage()
		m.log.Debug("[FIX:quota-account] account unavailable; quota observation revoked")
		return m.state(protocol.StateError, "Cannot read Codex account")
	}
	if m.cfg.kind == "api_key" && !m.keyApplied {
		// Always replace persisted private auth with this connection's supplied key.
		if response.Account != nil {
			if err := m.client.Call(ctx, "account/logout", nil, &struct{}{}); err != nil {
				return m.state(protocol.StateNeedsLogin, "Cannot replace private Codex login")
			}
		}
		var login schema.LoginResponse
		if err := m.client.Call(ctx, "account/login/start", schema.LoginParams{Type: "apiKey", APIKey: m.cfg.key}, &login); err != nil || login.Type != "apiKey" {
			return m.state(protocol.StateNeedsLogin, "Key rejected by Codex")
		}
		m.keyApplied = true
		accountRevision = m.revision.Load()
		if err := m.client.Call(ctx, "account/read", schema.AccountReadParams{}, &response); err != nil {
			m.invalidateQuotaObservation()
			m.quotaReady(false)
			m.signalUsage()
			m.log.Debug("[FIX:quota-account] account unavailable; quota observation revoked")
			return m.state(protocol.StateError, "Cannot read Codex account")
		}
	}
	raw, _ := json.Marshal(response.Account)
	if m.borrowed && !samePrincipal(m.identity, response.Account) {
		// Native session approvals belong to this account context. Recreate
		// the owned process before another account can use borrowed threads.
		m.closeClient()
		return m.status(ctx, true)
	}
	if m.identity != string(raw) {
		if !samePrincipal(m.identity, response.Account) {
			m.resetQuota()
		} else {
			m.invalidateQuotaObservation()
		}
		m.identity = string(raw)
		m.revision.Add(1)
		accountRevision++
	}
	want := "chatgpt"
	if m.cfg.kind == "api_key" {
		want = "apiKey"
	}
	if response.Account == nil || response.Account.Type != want {
		m.quotaReady(false)
		return m.loginStatus(m.state(protocol.StateNeedsLogin, "Sign in to this connection"))
	}
	st := m.state(protocol.StateReady, "Signed in with ChatGPT")
	if want == "apiKey" {
		st.Message = "API key configured in Codex"
	} else {
		st.Account = &protocol.AccountInfo{Plan: response.Account.PlanType, Label: maskedEmail(response.Account.Email)}
	}
	if m.revision.Load() != accountRevision {
		m.invalidateQuotaObservation()
		m.quotaReady(false)
		m.signal()
	} else {
		m.quotaReady(true)
		m.refreshQuota(ctx)
	}
	m.signalUsage()
	return m.loginStatus(st)
}

func (m *Manager) start(ctx context.Context) (protocol.Status, bool) {
	if m.retiring != nil {
		select {
		case <-m.retiring.Done():
			m.retiring = nil
		case <-ctx.Done():
			return m.state(protocol.StateError, "Previous Codex process is stopping"), false
		}
	}
	path, err := m.deps.find(m.cfg)
	if err != nil {
		return m.state(protocol.StateNotInstalled, "Codex executable not found"), false
	}
	m.binary = &protocol.BinaryInfo{Name: "codex", Path: path}
	v, err := m.deps.version(ctx, path)
	if err != nil {
		return m.state(protocol.StateError, "Cannot determine Codex version"), false
	}
	m.binary.Version = v
	if !procx.AtLeast(v, minimumVersion) {
		return m.state(protocol.StateOutdated, "Codex "+minimumVersion+" or newer is required"), false
	}
	if err := m.cfg.prepare(); err != nil {
		return m.state(protocol.StateError, "Cannot prepare Codex data directory"), false
	}
	// Process lifetime belongs to Manager, never to an individual status/action call.
	// Cancelling a start wait must nevertheless stop its incomplete handshake.
	startCtx, startCancel := context.WithCancel(m.ctx)
	stop := context.AfterFunc(ctx, startCancel)
	m.generation++
	m.resetQuota()
	c, err := m.deps.start(startCtx, appserver.Options{Path: path, Dir: m.cfg.dataDir, Env: m.cfg.environment(), Secrets: []string{m.cfg.key}, InitializeTimeout: m.deps.timeout, Peer: m.peerOptions()})
	if !stop() || ctx.Err() != nil {
		startCancel()
		if c != nil {
			_ = c.Close(context.Background())
		}
		m.stopGeneration()
		return m.state(protocol.StateError, "Codex startup cancelled"), false
	}
	if err != nil {
		startCancel()
		m.stopGeneration()
		return m.state(protocol.StateError, "Cannot start Codex app-server"), false
	}
	m.client = c
	m.signal()
	m.log.Debug("connection app-server initialized")
	return protocol.Status{}, true
}

func (m *Manager) state(state protocol.StatusState, message string) protocol.Status {
	st := protocol.Status{State: state, Message: message, Binary: m.binary}
	if state == protocol.StateNotInstalled || state == protocol.StateOutdated {
		st.Actions = []protocol.Action{{ID: "install_help", Label: "Install Codex", Kind: protocol.ActionKindURL}}
	} else if m.cfg.kind == "chatgpt" {
		st.Actions = []protocol.Action{{ID: "login", Label: "Sign in with ChatGPT", Kind: protocol.ActionKindURL}}
		if m.cfg.separate {
			st.Actions = append(st.Actions, protocol.Action{ID: "logout_arvel_login", Label: "Sign out of Arvel login", Kind: protocol.ActionKindDone})
		}
	}
	return st
}

func maskedEmail(email *string) string {
	if email == nil || len(*email) > 254 || strings.ContainsAny(*email, "\r\n\x00\"<>") {
		return "ChatGPT account"
	}
	parsed, err := mail.ParseAddress(*email)
	if err != nil || parsed.Address != *email {
		return "ChatGPT account"
	}
	local, domain, ok := strings.Cut(*email, "@")
	if !ok || local == "" || domain == "" {
		return "ChatGPT account"
	}
	first, _ := utf8.DecodeRuneInString(local)
	return string(first) + "***@" + domain
}

func samePrincipal(previous string, current *schema.Account) bool {
	var old *schema.Account
	if json.Unmarshal([]byte(previous), &old) != nil || old == nil || current == nil {
		return false
	}
	if old.Type != current.Type || (old.Email == nil) != (current.Email == nil) {
		return false
	}
	return old.Email == nil || *old.Email == *current.Email
}

func (m *Manager) remember(st protocol.Status, notify bool) {
	changed := !m.haveLast || !reflect.DeepEqual(st, m.last)
	m.last = st
	m.haveLast = true
	if changed {
		m.log.Info("connection state changed", "state", st.State)
	}
	if notify && changed && m.changed != nil {
		m.changed(st)
	}
}

func (m *Manager) reset() {
	m.resetQuota()
	m.stopGeneration()
	m.client = nil
	m.borrowed = false
	m.pending = ""
	m.loginFailed = false
	m.keyApplied = false
	m.identity = ""
	m.revision.Add(1)
}

// Close cancels operations and waits for the refresh worker and process cleanup.
func (m *Manager) Close(ctx context.Context) error {
	m.cancel()
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func unavailable(message string) error {
	return protocol.NewError(protocol.ErrProviderError, message, map[string]any{"vendorCode": "connection_error"})
}

var errAction = errors.New("action is unavailable for this connection")
