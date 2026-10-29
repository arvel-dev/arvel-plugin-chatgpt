// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/sanitize"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/skillset"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

const controlTimeout = 15 * time.Second
const cancelTimeout = 10 * time.Second

// Backend is the connection-owned process and account boundary.
type Backend interface {
	Acquire(context.Context) (connection.Runtime, error)
	Models(context.Context, bool) (protocol.ModelsResult, error)
	Abort(context.Context, uint64) error
}

// Snapshot retains only recovery identity, next-turn options and local budget
// accounting. It contains no credentials, prompt history or vendor config.
type Snapshot struct {
	VendorID string
	LastTurn string
	Model    string
	Effort   protocol.Effort
	Turns    int
	Empty    bool
}

type activeTurn struct {
	done              chan struct{}
	wake              chan struct{}
	permissionContext context.Context
	cancelPermissions context.CancelFunc
	cancelled         bool
	deadline          time.Time
}

// Session implements one stable ACP identity backed by a vendor thread.
type Session struct {
	mu                 sync.Mutex
	id                 string
	backend            Backend
	router             *Router
	log                *slog.Logger
	params             plugin.SessionParams
	config             threadConfig
	runtime            connection.Runtime
	sub                *Subscription
	catalog            protocol.ModelsResult
	snapshot           Snapshot
	completedTurns     map[string]struct{}
	active             *activeTurn
	closed             bool
	detached           bool
	broken             bool
	mcpApprovalSeq     uint64
	mcpStartupFailures map[string]bool
	skillManager       *skillset.Manager
	skillCatalog       []protocol.SkillRef
	skillRelease       func()
	children           map[string]*childSession
	// Tests inject a shorter escalation interval without sleeping ten seconds.
	cancelAfter time.Duration
	now         func() time.Time
}

var _ plugin.Session = (*Session)(nil)
var _ plugin.ConfigOptioner = (*Session)(nil)

// New creates a thread or honors the host's explicit ResumeFrom identity.
func New(ctx context.Context, backend Backend, router *Router, params plugin.SessionParams, logger *slog.Logger, managers ...*skillset.Manager) (*Session, error) {
	thread := ""
	if params.Meta.ResumeFrom != nil {
		thread = *params.Meta.ResumeFrom
		if thread == "" {
			return nil, setupError("Resume thread identity is empty")
		}
	}
	return open(ctx, backend, router, params, thread, nil, logger, managers)
}

// Load preserves the requested ACP identity. A snapshot restores in-process
// next-turn selections and budget accounting after the SDK's idle close.
func Load(ctx context.Context, backend Backend, router *Router, params plugin.LoadParams, saved *Snapshot, logger *slog.Logger, managers ...*skillset.Manager) (*Session, error) {
	if params.SessionID == "" {
		return nil, setupError("Session identity is empty")
	}
	return open(ctx, backend, router, params.SessionParams, params.SessionID, saved, logger, managers)
}

func open(ctx context.Context, backend Backend, router *Router, params plugin.SessionParams, id string, saved *Snapshot, logger *slog.Logger, managers []*skillset.Manager) (*Session, error) {
	// Own the host's nested slices/maps for the lifetime of this session.
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, setupError("Invalid session parameters")
	}
	if err := json.Unmarshal(encoded, &params); err != nil {
		return nil, setupError("Invalid session parameters")
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Session{backend: backend, router: router, log: logger, params: params, id: id, cancelAfter: cancelTimeout, now: time.Now}
	if len(managers) > 0 {
		s.skillManager = managers[0]
	}
	if len(params.Meta.Skills) > 0 && s.skillManager == nil {
		return nil, setupError("Workspace skill ownership is unavailable")
	}
	if saved != nil {
		s.snapshot = *saved
	}
	if err := s.connect(ctx, id); err != nil {
		if s.skillRelease != nil {
			s.skillRelease()
		}
		return nil, err
	}
	if s.id == "" {
		s.id = s.snapshot.VendorID
	}
	if s.config.policy.ReadDenied() {
		s.log.Warn("Codex native sandbox cannot enforce read-deny inside its readable scope")
	}
	return s, nil
}

// connect runs only during setup or under Prompt's exclusive active-turn claim.
func (s *Session) connect(ctx context.Context, resume string) error {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	catalog, err := s.backend.Models(ctx, false)
	if err != nil {
		return err
	}
	runtime, err := s.backend.Acquire(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	params := s.params
	saved := s.snapshot
	s.mu.Unlock()
	if saved.Model != "" {
		params.Meta.Model = saved.Model
		params.Meta.Effort = saved.Effort
	}
	cfg, err := buildConfig(params, runtime.Home)
	if err != nil {
		return err
	}
	cfg.clean = sanitize.Combine(cfg.clean, runtime.Clean)
	model, effort, err := selection(catalog, cfg.meta.Model, cfg.meta.Effort)
	if err != nil {
		return err
	}
	cfg.start.Model = &model
	if err := configureSubagents(&cfg, catalog, model); err != nil {
		return err
	}
	if err = preflight(ctx, runtime.Client, cfg.children > 0); err != nil {
		return err
	}
	if err := s.prepareSkills(ctx, cfg.start.CWD); err != nil {
		return err
	}
	if s.skillManager != nil {
		if err := s.verifySkills(ctx, runtime.Client, cfg.start.CWD); err != nil {
			return err
		}
	}
	release, err := s.router.Prepare(runtime.Generation)
	if err != nil {
		return setupError("Codex process changed during session setup")
	}
	defer release()
	var response schema.ThreadResponse
	thread := resume
	if saved.VendorID != "" {
		thread = saved.VendorID
	}
	created := thread == ""
	var sub *Subscription
	attached := false
	defer func() {
		if sub != nil && !attached {
			sub.Close()
		}
	}()
	if !created {
		// Reserve before resume: that RPC can mutate an already loaded thread.
		// A duplicate local session must fail before touching another session.
		sub, err = s.router.Register(runtime.Generation, thread)
		if err != nil {
			return setupError("Codex session is already attached or unavailable")
		}
	}
	if created {
		err = runtime.Client.Call(ctx, "thread/start", cfg.start, &response)
	} else {
		err = runtime.Client.Call(ctx, "thread/resume", cfg.resume(thread), &response)
	}
	if err != nil && !created && saved.Empty && noRollout(err, thread) {
		// Only a locally created thread with no attempted turn may be replaced.
		sub.Close()
		sub = nil
		err = runtime.Client.Call(ctx, "thread/start", cfg.start, &response)
		created = true
	}
	if err != nil {
		return setupError("Cannot create or resume the Codex session")
	}
	if response.Thread.ID == "" || len(response.Thread.ID) > 256 || (!created && response.Thread.ID != thread) {
		return setupError("Codex returned an invalid session identity")
	}
	if sub == nil {
		sub, err = s.router.Register(runtime.Generation, response.Thread.ID)
		if err != nil {
			return setupError("Cannot register the Codex session stream")
		}
	}
	if cfg.children > 0 {
		if err := sub.EnableChildren(cfg.children); err != nil {
			return setupError("Cannot prepare subagent routing")
		}
	}
	completed := map[string]struct{}{}
	if len(response.Thread.Turns) > maxItems {
		return setupError("Codex resumed history exceeds the session limit")
	}
	for _, turn := range response.Thread.Turns {
		if turn.ID == "" || len(turn.ID) > 256 {
			return setupError("Codex returned an invalid historical turn identity")
		}
		switch turn.Status {
		case "completed", "interrupted", "failed":
			completed[turn.ID] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completedTurns = completed
	s.log.Debug("[FIX:resume-turns] restored terminal turn identities", "turns", len(completed))
	if s.sub != nil {
		s.sub.Close()
	}
	if s.runtime.Generation != runtime.Generation || s.children == nil {
		s.children = map[string]*childSession{}
	}
	s.runtime, s.sub, s.config, s.catalog = runtime, sub, cfg, catalog
	s.mcpStartupFailures = map[string]bool{}
	attached = true
	s.snapshot.VendorID = response.Thread.ID
	if s.snapshot.Model == saved.Model && s.snapshot.Effort == saved.Effort {
		s.snapshot.Model, s.snapshot.Effort = model, effort
	}
	if created {
		s.snapshot.Empty = true
	}
	s.broken = false
	s.log.Debug("session runtime attached", "generation", runtime.Generation)
	return nil
}

func noRollout(err error, thread string) bool {
	var rpc *protocol.RPCError
	return errors.As(err, &rpc) && rpc.Code == protocol.CodeInvalidRequest && rpc.Message == "no rollout found for thread id "+thread
}

// ID is immutable, including when a known empty vendor thread is replaced.
func (s *Session) ID() string { return s.id }

// Snapshot returns a defensive recovery descriptor without conversation content.
func (s *Session) Snapshot() Snapshot { s.mu.Lock(); defer s.mu.Unlock(); return s.snapshot }

// Cancel immediately cancels permission waits and signals the owned turn loop.
func (s *Session) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.cancelled {
		return
	}
	s.active.cancelled = true
	s.active.deadline = time.Now().Add(s.cancelAfter)
	s.active.cancelPermissions()
	select {
	case s.active.wake <- struct{}{}:
	default:
	}
}

// Close is idempotent and waits for this session's workers, never another thread.
func (s *Session) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	active := s.active
	s.mu.Unlock()
	s.Cancel()
	if active != nil {
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	leaseRelease := s.skillRelease
	s.skillRelease = nil
	if s.sub != nil {
		s.sub.Close()
		s.sub = nil
	}
	if leaseRelease != nil {
		defer leaseRelease()
	}
	if s.detached {
		s.mu.Unlock()
		return nil
	}
	s.detached = true
	runtime, thread := s.runtime, s.snapshot.VendorID
	children := make([]string, 0, len(s.children))
	for id := range s.children {
		children = append(children, id)
	}
	s.children = map[string]*childSession{}
	s.mu.Unlock()
	select {
	case <-runtime.Client.Done():
		return nil
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	for _, id := range children {
		if err := runtime.Client.Call(ctx, "thread/unsubscribe", schema.ThreadUnsubscribeParams{ThreadID: id}, &struct{}{}); err != nil {
			return setupError("Cannot unload a Codex subagent")
		}
	}
	if err := runtime.Client.Call(ctx, "thread/unsubscribe", schema.ThreadUnsubscribeParams{ThreadID: thread}, &struct{}{}); err != nil {
		return setupError("Cannot unload the Codex session")
	}
	return nil
}
