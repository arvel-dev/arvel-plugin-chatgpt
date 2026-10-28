// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"slices"
)

func activeChildren(g *generation) bool {
	for _, q := range g.routes {
		if q.parent == "" && q.childLimit > 0 && q.handler != nil {
			return true
		}
	}
	return false
}

// CurrentTurn returns the active turn observed on this route.
func (s *Subscription) CurrentTurn() (string, bool) {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	return s.route.turn, s.route.err == nil && s.route.turnActive
}

func discardUnclaimed(g *generation) {
	if g.setups != 0 || activeChildren(g) {
		return
	}
	for _, events := range g.staged {
		for _, event := range events {
			g.bytes -= len(event.Params)
		}
	}
	clear(g.staged)
}

// EnableChildren permits bounded staging only during an active owning turn.
func (s *Subscription) EnableChildren(limit int) error {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	if s.route.err != nil || limit < 1 || limit > 16 || s.route.parent != "" {
		return ErrRouting
	}
	s.route.childLimit = limit
	return nil
}

// ClaimChild reserves an activity-attributed identity pending thread/read proof.
func (s *Subscription) ClaimChild(thread string) (*Subscription, error) {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	return s.claimChildLocked(thread)
}

func (s *Subscription) claimChildLocked(thread string) (*Subscription, error) {
	g := s.generation
	if g.stopped || s.route.err != nil || s.route.childLimit == 0 || thread == "" || len(thread) > 256 || thread == s.thread {
		return nil, ErrRouting
	}
	if existing := g.routes[thread]; existing != nil {
		if existing.parent != s.thread {
			s.router.stopLocked(g, true)
			return nil, ErrRouting
		}
		return &Subscription{router: s.router, generation: g, thread: thread, route: existing}, nil
	}
	if len(g.routes) >= maxThreads {
		s.router.stopLocked(g, true)
		return nil, ErrRouting
	}
	q := &route{events: g.staged[thread], wake: make(chan struct{}, 1), done: make(chan struct{}), items: make(map[string][]string), parent: s.thread, ready: make(chan struct{})}
	delete(g.staged, thread)
	for _, event := range q.events {
		if !cacheItem(q, event) {
			s.router.stopLocked(g, true)
			return nil, ErrRouting
		}
	}
	g.routes[thread] = q
	return &Subscription{router: s.router, generation: g, thread: thread, route: q}, nil
}

// ConfirmChild releases staged events and requests after source verification.
func (s *Subscription) ConfirmChild(handler ApprovalHandler) error {
	s.router.mu.Lock()
	defer s.router.mu.Unlock()
	g, q := s.generation, s.route
	parent := g.routes[q.parent]
	if g.stopped || q.err != nil || q.parent == "" || parent == nil || parent.handler == nil || handler == nil {
		return ErrRouting
	}
	if q.confirmed {
		if q.cancel != nil {
			q.cancel()
		}
		q.handler = handler
		q.approvalContext, q.cancel = context.WithCancel(parent.approvalContext)
		return nil
	}
	if len(parent.events)+len(q.events) > maxEvents {
		s.router.stopLocked(g, true)
		return ErrRouting
	}
	parent.events = append(parent.events, q.events...)
	slices.SortFunc(parent.events, func(a, b Event) int {
		if a.sequence < b.sequence {
			return -1
		}
		if a.sequence > b.sequence {
			return 1
		}
		return 0
	})
	q.events = nil
	q.handler = handler
	q.approvalContext, q.cancel = context.WithCancel(parent.approvalContext)
	q.confirmed = true
	close(q.ready)
	select {
	case parent.wake <- struct{}{}:
	default:
	}
	return nil
}
