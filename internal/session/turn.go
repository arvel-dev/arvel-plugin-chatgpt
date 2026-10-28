// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/mapper"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/usage"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func promptInput(blocks []acp.ContentBlock) ([]schema.UserInput, error) {
	input := make([]schema.UserInput, 0, len(blocks))
	imageBytes, images := 0, 0
	for _, block := range blocks {
		if block.Validate() != nil {
			return nil, setupError("Invalid prompt content")
		}
		switch {
		case block.Text != nil:
			input = append(input, schema.UserInput{Type: "text", Text: block.Text.Text})
		case block.ResourceLink != nil:
			input = append(input, schema.UserInput{Type: "text", Text: block.ResourceLink.Name + ": " + block.ResourceLink.Uri})
		case block.Image != nil:
			item, size, err := imageInput(*block.Image)
			if err != nil {
				return nil, err
			}
			imageBytes += size
			images++
			if imageBytes > maxPromptImageBytes || images > maxPromptImages {
				return nil, setupError("Prompt image size or count limit exceeded")
			}
			input = append(input, item)
		default:
			return nil, setupError("Audio and embedded resource prompts are not supported yet")
		}
	}
	if len(input) == 0 {
		return nil, setupError("Prompt content is empty")
	}
	return input, nil
}

// Prompt owns its transport/event workers and joins them on every return path.
func (s *Session) Prompt(ctx context.Context, blocks []acp.ContentBlock, out plugin.Output) (plugin.TurnResult, error) {
	input, err := promptInput(blocks)
	if err != nil {
		return plugin.TurnResult{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return plugin.TurnResult{}, setupError("Session is closed")
	}
	if s.active != nil {
		s.mu.Unlock()
		return plugin.TurnResult{}, protocol.NewError(protocol.ErrBusy, "Session already has a running turn", nil)
	}
	if budget := s.params.Meta.Budget; budget != nil && budget.MaxTurns > 0 && s.snapshot.Turns >= budget.MaxTurns {
		s.mu.Unlock()
		return plugin.TurnResult{StopReason: acp.StopReasonMaxTurnRequests, Stop: &protocol.StopDetail{Code: protocol.ErrBudgetExceeded, Limit: "turns"}}, nil
	}
	permissionContext, cancelPermissions := context.WithCancel(context.Background())
	active := &activeTurn{done: make(chan struct{}), wake: make(chan struct{}, 1), permissionContext: permissionContext, cancelPermissions: cancelPermissions}
	s.active = active
	broken := s.broken
	resume := s.snapshot.VendorID
	select {
	case <-s.runtime.Client.Done():
		broken = true
	default:
	}
	s.mu.Unlock()
	defer func() {
		cancelPermissions()
		s.mu.Lock()
		s.active = nil
		if s.closed && s.sub != nil {
			s.sub.Close()
			s.sub = nil
		}
		if s.closed && s.skillRelease != nil {
			release := s.skillRelease
			s.skillRelease = nil
			release()
		}
		close(active.done)
		s.mu.Unlock()
	}()
	if broken {
		if err := s.connect(ctx, resume); err != nil {
			return plugin.TurnResult{}, err
		}
	}
	return s.runTurn(ctx, input, out, active)
}

type startReply struct {
	response schema.TurnResponse
	err      error
}
type nextEvent struct {
	event Event
	err   error
}

func (s *Session) runTurn(ctx context.Context, input []schema.UserInput, out plugin.Output, active *activeTurn) (result plugin.TurnResult, resultErr error) {
	s.mu.Lock()
	runtime, sub := s.runtime, s.sub
	cfg := s.config
	input = selectedSkills(input, s.params.Meta.Skills, s.skillCatalog)
	thread, model, effort := s.snapshot.VendorID, s.snapshot.Model, s.snapshot.Effort
	previousTurn := s.snapshot.LastTurn
	imageCount := 0
	for _, item := range input {
		if item.Type == "image" {
			imageCount++
			if !imageModel(s.catalog, model) {
				s.mu.Unlock()
				return plugin.TurnResult{}, setupError("Selected model does not support image input")
			}
		}
	}
	s.snapshot.Empty = false // Once delivery is attempted, history loss is ambiguous.
	s.mu.Unlock()
	if imageCount > 0 {
		s.log.Debug("turn image input prepared", "images", imageCount)
	}
	if runtime.ActiveTurn != nil {
		release := runtime.ActiveTurn()
		defer release()
	}
	work, stop := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { stop(); workers.Wait() }()
	var turnMu sync.Mutex
	turnID := ""
	bind := func(id string) bool {
		turnMu.Lock()
		defer turnMu.Unlock()
		if id == "" || len(id) > 256 || id == previousTurn {
			return false
		}
		if turnID == "" {
			turnID = id
			select {
			case active.wake <- struct{}{}:
			default:
			}
		}
		return turnID == id
	}
	currentTurn := func() string { turnMu.Lock(); defer turnMu.Unlock(); return turnID }
	mapping := mapper.New()
	mapping.ConfigureMCP(cfg.mcpNames, cfg.clean)
	end, err := sub.BeginTurn(func(requestContext context.Context, method string, raw json.RawMessage) (any, error) {
		return s.approve(requestContext, active.permissionContext, method, raw, out, runtime, sub, thread, bind)
	}, active.permissionContext)
	if err != nil {
		return plugin.TurnResult{}, setupError("Session event stream is unavailable")
	}
	defer end()
	tree := newChildTree(s, runtime, sub, thread, cfg, out, mapping, active.permissionContext)
	if err := tree.begin(); err != nil {
		return plugin.TurnResult{}, setupError("Cannot attach subagent routing")
	}
	replies := make(chan startReply, 1)
	events := make(chan nextEvent, 1)
	eventAck := make(chan struct{})
	params := schema.TurnStartParams{ThreadID: thread, Input: input, Model: &model}
	if effort != "" && effort != "auto" {
		explicit := string(effort)
		params.Effort = &explicit
	}
	workers.Go(func() {
		var response schema.TurnResponse
		err := runtime.Client.Call(work, "turn/start", params, &response)
		replies <- startReply{response, err}
	})
	workers.Go(func() {
		for {
			event, err := sub.Next(work)
			select {
			case events <- nextEvent{event, err}:
			case <-work.Done():
				return
			}
			if err != nil {
				return
			}
			select {
			case <-eventAck:
			case <-work.Done():
				return
			}
		}
	})
	write := func(updates []acp.SessionUpdate) error {
		for _, update := range updates {
			if err := out.Update(update, nil); err != nil {
				return setupError("Cannot deliver the session update")
			}
		}
		return nil
	}
	var tokens *protocol.TurnUsage
	defer func() {
		if resultErr == nil {
			result.Usage = tokens
		}
	}()
	quota := func() usage.QuotaContext {
		if runtime.Quota != nil {
			return runtime.Quota()
		}
		return usage.QuotaContext{}
	}
	var terminal *schema.Turn
	started := false
	interruptStarted := false
	interruptDone := make(chan error, 1)
	var deadline <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	parentDone := ctx.Done()
	markBroken := func() { s.mu.Lock(); s.broken = true; s.mu.Unlock() }
	abort := func() {
		markBroken()
		cleanup, cancel := context.WithTimeout(context.Background(), controlTimeout)
		defer cancel()
		_ = s.backend.Abort(cleanup, runtime.Generation)
	}
	cancelled := func() bool { s.mu.Lock(); defer s.mu.Unlock(); return active.cancelled }
	needAck := false
	childrenDraining := false
	childrenDone := make(chan error, 1)
	for {
		if needAck {
			select {
			case eventAck <- struct{}{}:
			case <-work.Done():
			}
			needAck = false
		}
		if terminal != nil && started && tree.active() && !childrenDraining {
			childrenDraining = true
			active.cancelPermissions()
			if timer == nil {
				timer = time.NewTimer(controlTimeout)
				deadline = timer.C
			}
			targets := tree.targets()
			workers.Go(func() {
				cleanup, cancel := context.WithTimeout(work, controlTimeout)
				defer cancel()
				childrenDone <- interruptChildren(cleanup, runtime, targets)
			})
		}
		if terminal != nil && started && !tree.active() {
			active.cancelPermissions()
			if err := tree.finish(); err != nil {
				abort()
				return plugin.TurnResult{}, err
			}
			if err := write(mapping.Finish()); err != nil {
				markBroken()
				return plugin.TurnResult{}, err
			}
			s.mu.Lock()
			s.snapshot.Turns++
			s.snapshot.LastTurn = currentTurn()
			s.mu.Unlock()
			if ctx.Err() != nil {
				return plugin.TurnResult{}, ctx.Err()
			}
			if cancelled() {
				return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
			}
			switch terminal.Status {
			case "completed":
				return plugin.TurnResult{StopReason: acp.StopReasonEndTurn}, nil
			case "interrupted":
				return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
			default:
				var failure schema.TurnError
				if json.Unmarshal(terminal.Error, &failure) != nil {
					failure = schema.TurnError{}
				}
				detail := usage.Classify(failure, model, quota(), s.now())
				return plugin.TurnResult{StopReason: acp.StopReasonEndTurn, Stop: &detail}, nil
			}
		}
		if cancelled() {
			if timer == nil {
				s.mu.Lock()
				remaining := time.Until(active.deadline)
				s.mu.Unlock()
				timer = time.NewTimer(max(remaining, 0))
				deadline = timer.C
			}
			if !interruptStarted && currentTurn() != "" {
				interruptStarted = true
				s.mu.Lock()
				until := active.deadline
				s.mu.Unlock()
				id := currentTurn()
				targets := tree.targets()
				workers.Go(func() {
					interruptContext, cancel := context.WithDeadline(work, until)
					defer cancel()
					err := interruptChildren(interruptContext, runtime, targets)
					if err == nil {
						err = runtime.Client.WaitResponses(interruptContext, thread, id)
					}
					if err == nil {
						err = runtime.Client.Call(interruptContext, "turn/interrupt", schema.TurnInterruptParams{ThreadID: thread, TurnID: id}, &struct{}{})
					}
					interruptDone <- err
				})
			}
		}
		select {
		case reply := <-replies:
			if reply.err != nil {
				abort()
				if cancelled() {
					return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
				}
				return plugin.TurnResult{}, usage.RPCError(reply.err, model, quota(), s.now())
			}
			if !bind(reply.response.Turn.ID) {
				abort()
				return plugin.TurnResult{}, setupError("Codex returned inconsistent turn identities")
			}
			started = true
		case next := <-events:
			needAck = next.err == nil
			if next.err != nil {
				markBroken()
				if cancelled() {
					return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
				}
				return plugin.TurnResult{}, setupError("Codex session stream stopped")
			}
			var identity struct {
				ThreadID string `json:"threadId"`
				TurnID   string `json:"turnId"`
			}
			if json.Unmarshal(next.event.Params, &identity) != nil {
				abort()
				return plugin.TurnResult{}, setupError("Invalid Codex session event")
			}
			if identity.ThreadID == thread && previousTurn != "" && identity.TurnID == previousTurn {
				continue
			}
			if handled, err := tree.push(active.permissionContext, next.event); err != nil {
				abort()
				if cancelled() {
					return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
				}
				return plugin.TurnResult{}, setupError("Invalid subagent session output")
			} else if handled {
				continue
			}
			if next.event.Method == "thread/started" || next.event.Method == "thread/status/changed" {
				continue
			}
			if identity.ThreadID != thread {
				abort()
				return plugin.TurnResult{}, setupError("Codex session identity changed")
			}
			if next.event.Method == "mcpServer/startupStatus/updated" {
				var status schema.MCPStartup
				if json.Unmarshal(next.event.Params, &status) != nil {
					abort()
					return plugin.TurnResult{}, setupError("Invalid MCP startup event")
				}
				if _, assigned := cfg.mcpNames[status.Name]; !assigned || (status.Status != "failed" && status.Status != "cancelled") {
					continue
				}
				s.mu.Lock()
				seen := s.mcpStartupFailures[status.Name]
				s.mcpStartupFailures[status.Name] = true
				s.mu.Unlock()
				if seen {
					continue
				}
				s.log.Warn("assigned MCP server startup failed")
				updates, err := mapping.Push(next.event.Method, next.event.Params)
				if err != nil {
					abort()
					return plugin.TurnResult{}, setupError("Invalid MCP startup output")
				}
				if err = write(updates); err != nil {
					abort()
					return plugin.TurnResult{}, err
				}
				continue
			}
			if next.event.Method == "turn/started" || next.event.Method == "turn/completed" {
				var lifecycle schema.TurnEvent
				if json.Unmarshal(next.event.Params, &lifecycle) == nil && previousTurn != "" && lifecycle.Turn.ID == previousTurn {
					continue
				}
				if !bind(lifecycle.Turn.ID) {
					abort()
					return plugin.TurnResult{}, setupError("Codex returned inconsistent turn identities")
				}
				if next.event.Method == "turn/completed" {
					if terminal != nil {
						abort()
						return plugin.TurnResult{}, setupError("Codex returned duplicate turn completion")
					}
					terminal = &lifecycle.Turn
				}
				continue
			}
			if previousTurn != "" && identity.TurnID == previousTurn {
				continue
			}
			if !bind(identity.TurnID) {
				abort()
				return plugin.TurnResult{}, setupError("Codex returned inconsistent turn identities")
			}
			if next.event.Method == "thread/tokenUsage/updated" {
				var event schema.TokenUsageNotification
				if terminal == nil && json.Unmarshal(next.event.Params, &event) == nil {
					if observed := usage.Tokens(event.TokenUsage); observed != nil {
						tokens = observed
					}
				}
				continue
			}
			if next.event.Method == "error" {
				continue
			} // Retry advisories never complete a turn.
			updates, err := mapping.Push(next.event.Method, next.event.Params)
			if err != nil {
				abort()
				return plugin.TurnResult{}, setupError("Invalid Codex session output")
			}
			if err = write(updates); err != nil {
				abort()
				return plugin.TurnResult{}, err
			}
		case err := <-childrenDone:
			if err != nil && tree.active() {
				abort()
				return plugin.TurnResult{}, setupError("Cannot stop active subagents")
			}
		case err := <-interruptDone:
			if err != nil {
				abort()
				return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
			}
		case <-active.wake:
		case <-parentDone:
			parentDone = nil
			s.Cancel()
		case <-deadline:
			abort()
			if childrenDraining && !cancelled() {
				return plugin.TurnResult{}, setupError("Subagents did not stop before the session deadline")
			}
			if ctx.Err() != nil {
				return plugin.TurnResult{}, ctx.Err()
			}
			return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
		case <-runtime.Client.Done():
			markBroken()
			if cancelled() {
				return plugin.TurnResult{StopReason: acp.StopReasonCancelled}, nil
			}
			return plugin.TurnResult{}, setupError("Codex app-server stopped during the turn")
		}
	}
}
