// SPDX-License-Identifier: Apache-2.0

// Package session owns thread routing and the provider's agent lifecycle.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
)

const (
	maxThreads = 128
	maxEvents  = 256
	maxBytes   = 8 << 20
	maxItems   = 4096
)

// ErrRouting means a generation can no longer deliver a complete event stream.
var ErrRouting = errors.New("app-server session routing unavailable")

// Event is an owned, ordered vendor notification.
type Event struct {
	Method string
	Params json.RawMessage
}

type route struct {
	events          []Event
	wake            chan struct{}
	done            chan struct{}
	err             error
	handler         ApprovalHandler
	approvalContext context.Context
	cancel          context.CancelFunc
	items           map[string][]string
	itemBytes       int
}

type generation struct {
	id      uint64
	setups  int
	bytes   int
	stopped bool
	routes  map[string]*route
	staged  map[string][]Event
}

// ApprovalHandler bridges a routed request without holding routing locks.
type ApprovalHandler func(context.Context, string, json.RawMessage) (any, error)

// Router has no background goroutines. Its owner consumes NextAbort and closes
// the matching process outside notification callbacks and connection locks.
type Router struct {
	mu        sync.Mutex
	current   *generation
	aborts    map[uint64]struct{}
	abortWake chan struct{}
}

// NewRouter creates bounded generation-local event routing.
func NewRouter() *Router {
	return &Router{aborts: make(map[uint64]struct{}), abortWake: make(chan struct{}, 1)}
}

var routedNotifications = []string{
	"thread/started", "thread/status/changed", "turn/started", "turn/completed",
	"thread/tokenUsage/updated", "error",
	"turn/plan/updated", "turn/diff/updated", "item/started", "item/completed",
	"item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded",
}

// ForGeneration installs handlers before process IO starts. A newer generation
// retires all previous subscriptions; stale callbacks cannot revive them.
func (r *Router) ForGeneration(id uint64) appserver.PeerOptions {
	r.mu.Lock()
	if r.current == nil || id > r.current.id {
		if r.current != nil {
			r.stopLocked(r.current, false)
		}
		clear(r.aborts)
		r.current = &generation{id: id, routes: make(map[string]*route), staged: make(map[string][]Event)}
	}
	r.mu.Unlock()
	opts := appserver.PeerOptions{Notifications: make(map[string]appserver.Notification), Requests: make(map[string]appserver.Handler)}
	for _, method := range routedNotifications {
		opts.Notifications[method] = func(_ context.Context, raw json.RawMessage) { r.enqueue(id, method, raw) }
	}
	for _, method := range []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "mcpServer/elicitation/request"} {
		opts.Requests[method] = func(ctx context.Context, raw json.RawMessage) (any, error) { return r.request(ctx, id, method, raw) }
	}
	return opts
}

// Prepare opens a bounded staging window before a thread/start or resume call.
// The returned release function is idempotent and must always be called.
func (r *Router) Prepare(id uint64) (func(), error) {
	r.mu.Lock()
	g := r.current
	if g == nil || g.id != id || g.stopped || g.setups >= maxThreads {
		r.mu.Unlock()
		return nil, ErrRouting
	}
	g.setups++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			g.setups--
			if g.setups == 0 {
				for _, events := range g.staged {
					for _, event := range events {
						g.bytes -= len(event.Params)
					}
				}
				clear(g.staged)
			}
		})
	}, nil
}

// Subscription receives one thread's ordered stream in one generation.
type Subscription struct {
	router     *Router
	generation *generation
	thread     string
	route      *route
}

// Register atomically adopts notifications that arrived before the thread reply.
func (r *Router) Register(id uint64, thread string) (*Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.current
	if g == nil || g.id != id || g.stopped || thread == "" || len(g.routes) >= maxThreads || g.routes[thread] != nil {
		return nil, ErrRouting
	}
	q := &route{events: g.staged[thread], wake: make(chan struct{}, 1), done: make(chan struct{}), items: make(map[string][]string)}
	delete(g.staged, thread)
	g.routes[thread] = q
	for _, event := range q.events {
		if !cacheItem(q, event) {
			r.stopLocked(g, true)
			return nil, ErrRouting
		}
	}
	return &Subscription{router: r, generation: g, thread: thread, route: q}, nil
}

// Next returns queued events until the subscription fails or is closed.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		s.router.mu.Lock()
		q := s.route
		if q.err != nil {
			err := q.err
			s.router.mu.Unlock()
			return Event{}, err
		}
		if len(q.events) > 0 {
			e := q.events[0]
			q.events[0] = Event{}
			q.events = q.events[1:]
			s.generation.bytes -= len(e.Params)
			s.router.mu.Unlock()
			return e, nil
		}
		s.router.mu.Unlock()
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-q.done:
		case <-q.wake:
		}
	}
}

// BeginTurn installs approval handling before any outbound turn request. The
// handler verifies turn identity and runs without the router lock.
func (s *Subscription) BeginTurn(handler ApprovalHandler) (func(), error) {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	q := s.route
	if q.err != nil || q.handler != nil || handler == nil {
		return nil, ErrRouting
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.handler, q.approvalContext, q.cancel = handler, ctx, cancel
	clear(q.items)
	q.itemBytes = 0
	var once sync.Once
	return func() {
		once.Do(func() {
			s.router.mu.Lock()
			defer s.router.mu.Unlock()
			cancel()
			if q.approvalContext == ctx {
				q.handler = nil
				q.cancel = nil
				q.approvalContext = nil
				clear(q.items)
				q.itemBytes = 0
			}
		})
	}, nil
}

// ItemPaths returns a copy of observed file-change paths. Callers must first
// establish the notification dispatch barrier when serving an approval request.
func (s *Subscription) ItemPaths(item string) []string {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	return append([]string(nil), s.route.items[item]...)
}

// Close unregisters only this thread and cancels its active approval handlers.
func (s *Subscription) Close() {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	if s.route.err != nil {
		return
	}
	for _, e := range s.route.events {
		s.generation.bytes -= len(e.Params)
	}
	closeRoute(s.route)
	delete(s.generation.routes, s.thread)
}

// Stopped invalidates subscriptions once without requesting another process stop.
func (r *Router) Stopped(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil && r.current.id == id {
		r.stopLocked(r.current, false)
	}
}

// NextAbort waits for fail-closed overflow cleanup; notifications only signal it.
func (r *Router) NextAbort(ctx context.Context) (uint64, error) {
	for {
		r.mu.Lock()
		for id := range r.aborts {
			delete(r.aborts, id)
			r.mu.Unlock()
			return id, nil
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-r.abortWake:
		}
	}
}

func closeRoute(q *route) {
	if q.err != nil {
		return
	}
	q.err = ErrRouting
	q.events = nil
	clear(q.items)
	q.itemBytes = 0
	if q.cancel != nil {
		q.cancel()
	}
	q.handler = nil
	close(q.done)
}

func (r *Router) stopLocked(g *generation, abort bool) {
	if g.stopped {
		return
	}
	g.stopped = true
	for _, q := range g.routes {
		closeRoute(q)
	}
	clear(g.routes)
	clear(g.staged)
	g.bytes = 0
	if abort {
		r.aborts[g.id] = struct{}{}
		select {
		case r.abortWake <- struct{}{}:
		default:
		}
	}
}

func (r *Router) enqueue(id uint64, method string, raw json.RawMessage) {
	var identity struct {
		ThreadID string `json:"threadId"`
		Thread   struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &identity) != nil {
		r.fail(id)
		return
	}
	thread := identity.ThreadID
	if method == "thread/started" {
		thread = identity.Thread.ID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.current
	if g == nil || g.id != id || g.stopped {
		return
	}
	if thread == "" {
		r.stopLocked(g, true)
		return
	}
	q := g.routes[thread]
	if q == nil && g.setups == 0 {
		return
	}
	if g.bytes+len(raw) > maxBytes {
		r.stopLocked(g, true)
		return
	}
	event := Event{Method: method, Params: append(json.RawMessage(nil), raw...)}
	if q == nil {
		if len(g.staged[thread]) >= maxEvents || (g.staged[thread] == nil && len(g.staged) >= maxThreads) {
			r.stopLocked(g, true)
			return
		}
		g.staged[thread] = append(g.staged[thread], event)
	} else {
		if len(q.events) >= maxEvents || !cacheItem(q, event) {
			r.stopLocked(g, true)
			return
		}
		q.events = append(q.events, event)
		select {
		case q.wake <- struct{}{}:
		default:
		}
	}
	g.bytes += len(raw)
}

func cacheItem(q *route, event Event) bool {
	if event.Method != "item/started" && event.Method != "item/completed" {
		return true
	}
	var v schema.ItemEvent
	if json.Unmarshal(event.Params, &v) != nil {
		return false
	}
	if v.Item.Type != "fileChange" {
		return true
	}
	if v.Item.ID == "" || len(v.Item.ID) > 256 || len(v.Item.Changes) > maxItems {
		return false
	}
	if _, exists := q.items[v.Item.ID]; !exists && len(q.items) >= maxItems {
		return false
	}
	size := q.itemBytes
	if old, exists := q.items[v.Item.ID]; exists {
		size -= len(v.Item.ID) + 32
		for _, path := range old {
			size -= len(path) + 32
		}
	}
	size += len(v.Item.ID) + 32
	if size > maxBytes/maxThreads {
		return false
	}
	paths := make([]string, 0, len(v.Item.Changes))
	for _, change := range v.Item.Changes {
		if change.Path == "" {
			return false
		}
		size += len(change.Path) + 32
		if size > maxBytes/maxThreads {
			return false
		}
		paths = append(paths, change.Path)
	}
	q.itemBytes = size
	q.items[v.Item.ID] = paths
	return true
}

func (r *Router) fail(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.current != nil && r.current.id == id {
		r.stopLocked(r.current, true)
	}
}

func denial(method string) any {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		return schema.DecisionResponse{Decision: "decline"}
	case "item/permissions/requestApproval":
		return schema.PermissionsResponse{Permissions: map[string]any{}, Scope: "turn"}
	default:
		return schema.ElicitationResponse{Action: "decline"}
	}
}

func (r *Router) request(ctx context.Context, id uint64, method string, raw json.RawMessage) (any, error) {
	var identity schema.ApprovalIdentity
	if json.Unmarshal(raw, &identity) != nil || identity.ThreadID == "" {
		return denial(method), nil
	}
	// Permissions grants and MCP delivery are not enabled in G3.
	if method != "item/commandExecution/requestApproval" && method != "item/fileChange/requestApproval" {
		return denial(method), nil
	}
	r.mu.Lock()
	g := r.current
	if g == nil || g.id != id || g.stopped {
		r.mu.Unlock()
		return denial(method), nil
	}
	q := g.routes[identity.ThreadID]
	if q == nil || q.handler == nil {
		r.mu.Unlock()
		return denial(method), nil
	}
	handler, turnContext := q.handler, q.approvalContext
	r.mu.Unlock()
	callContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(turnContext, cancel)
	defer func() { stop(); cancel() }()
	result, err := handler(callContext, method, raw)
	if callContext.Err() != nil {
		return schema.DecisionResponse{Decision: "cancel"}, nil
	}
	if err != nil {
		return denial(method), nil
	}
	return result, nil
}
