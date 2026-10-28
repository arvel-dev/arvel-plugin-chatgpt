// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/mapper"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func configureSubagents(cfg *threadConfig, catalog protocol.ModelsResult, parentModel string) error {
	options := cfg.meta.Subagents
	if options == nil || !options.Enabled {
		return nil
	}
	limit := options.MaxParallel
	if limit == 0 {
		limit = 1
	}
	if limit < 1 || limit > 16 {
		return setupError("Subagent concurrency must be between 1 and 16")
	}
	model := options.Model
	if model == "" {
		model = parentModel
	}
	if _, _, err := selection(catalog, model, options.Effort); err != nil {
		return err
	}
	features := cfg.start.Config["features"].(map[string]any)
	features["multi_agent"] = false
	features["multi_agent_v2"] = map[string]any{"enabled": true, "max_concurrent_threads_per_session": limit + 1, "wait_agent_enabled": true, "expose_spawn_agent_model_overrides": options.Model == "" && options.Effort == ""}
	agents := map[string]any{"enabled": true}
	if options.Model != "" {
		agents["default_subagent_model"] = options.Model
	}
	if options.Effort != "" && options.Effort != "auto" {
		agents["default_subagent_reasoning_effort"] = string(options.Effort)
	}
	// Native role files can replace developer instructions. Description-only
	// declarations preserve the effective Arvel parent instructions instead.
	for _, role := range []string{"default", "explorer", "worker"} {
		agents[role] = map[string]string{"description": "Delegate within the parent task instructions and permissions."}
	}
	cfg.start.Config["agents"] = agents
	cfg.children = limit
	return nil
}

type childSession struct {
	id, creator, parentTool, label, lastTurn string
	depth                                    int
	sub                                      *Subscription
}

type childOutput struct {
	base plugin.Output
	id   string
	meta protocol.SubagentMeta
}

func (o *childOutput) toolID(id acp.ToolCallId) acp.ToolCallId {
	return acp.ToolCallId("child:" + o.id + ":" + string(id))
}
func (o *childOutput) Update(update acp.SessionUpdate, _ *protocol.UpdateMeta) error {
	if update.ToolCall != nil {
		copy := *update.ToolCall
		copy.ToolCallId = o.toolID(copy.ToolCallId)
		update.ToolCall = &copy
	}
	if update.ToolCallUpdate != nil {
		copy := *update.ToolCallUpdate
		copy.ToolCallId = o.toolID(copy.ToolCallId)
		update.ToolCallUpdate = &copy
	}
	meta := o.meta
	return o.base.Update(update, &protocol.UpdateMeta{Subagent: &meta})
}
func (o *childOutput) RequestPermission(ctx context.Context, request plugin.PermissionRequest) (plugin.PermissionDecision, error) {
	request.ToolCall.ToolCallId = o.toolID(request.ToolCall.ToolCallId)
	return o.base.RequestPermission(ctx, request)
}

type childRun struct {
	session                       *childSession
	out                           *childOutput
	mapping                       *mapper.Mapper
	turn                          string
	active, groupOpen, groupKnown bool
	startup                       map[string]bool
}

type childTree struct {
	owner             *Session
	runtime           connection.Runtime
	sub               *Subscription
	root              string
	cfg               threadConfig
	output            plugin.Output
	rootMapping       *mapper.Mapper
	permissionContext context.Context
	states            map[string]*childRun
	lastOwner         string
}

func newChildTree(s *Session, runtime connection.Runtime, sub *Subscription, root string, cfg threadConfig, out plugin.Output, mapping *mapper.Mapper, permissionContext context.Context) *childTree {
	tree := &childTree{owner: s, runtime: runtime, sub: sub, root: root, cfg: cfg, output: out, rootMapping: mapping, permissionContext: permissionContext, states: map[string]*childRun{}, lastOwner: root}
	s.mu.Lock()
	known := make([]*childSession, 0, len(s.children))
	for _, child := range s.children {
		known = append(known, child)
	}
	s.mu.Unlock()
	for _, child := range known {
		tree.add(child, true)
	}
	return tree
}

func (t *childTree) add(child *childSession, known bool) *childRun {
	state := &childRun{session: child, out: &childOutput{base: t.output, id: child.id, meta: protocol.SubagentMeta{ID: child.id, ParentToolCallID: child.parentTool, Label: child.label}}, groupKnown: known, startup: map[string]bool{}}
	state.mapping = mapper.New()
	state.mapping.ConfigureMCP(t.cfg.mcpNames, t.cfg.clean)
	t.states[child.id] = state
	return state
}

func (t *childTree) attach(state *childRun) error {
	return state.session.sub.ConfirmChild(func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		var identity schema.ApprovalIdentity
		if json.Unmarshal(raw, &identity) != nil || identity.ThreadID != state.session.id {
			return denial(method), nil
		}
		barrier, stop := context.WithTimeout(ctx, controlTimeout)
		defer stop()
		var ignored schema.ConfigReadResponse
		if t.runtime.Client.Call(barrier, "config/read", schema.ConfigReadParams{IncludeLayers: false}, &ignored) != nil {
			return denial(method), nil
		}
		bind := func(id string) bool {
			turn, active := state.session.sub.CurrentTurn()
			return active && id != "" && turn == id
		}
		return t.owner.approve(ctx, t.permissionContext, method, raw, state.out, t.runtime, state.session.sub, state.session.id, bind)
	})
}

func (t *childTree) begin() error {
	for _, state := range t.states {
		if err := t.attach(state); err != nil {
			return err
		}
	}
	return nil
}

func (t *childTree) write(updates []acp.SessionUpdate, out plugin.Output) error {
	for _, update := range updates {
		if err := out.Update(update, nil); err != nil {
			return setupError("Cannot deliver subagent output")
		}
	}
	return nil
}
func (t *childTree) ownerOutput(id string) (*mapper.Mapper, plugin.Output, error) {
	if id == t.root {
		return t.rootMapping, t.output, nil
	}
	if state := t.states[id]; state != nil {
		return state.mapping, state.out, nil
	}
	return nil, nil, ErrRouting
}
func (t *childTree) switchOwner(id string) error {
	if id == t.lastOwner {
		return nil
	}
	mapping, out, err := t.ownerOutput(t.lastOwner)
	if err != nil {
		return err
	}
	if err := t.write(mapping.Flush(), out); err != nil {
		return err
	}
	t.lastOwner = id
	return nil
}

func (t *childTree) group(state *childRun, start, failed bool) error {
	mapping, out, err := t.ownerOutput(state.session.creator)
	if err != nil {
		return err
	}
	if err := t.write(mapping.Flush(), out); err != nil {
		return err
	}
	id := acp.ToolCallId(state.session.parentTool)
	if state.session.creator != t.root {
		creator := t.states[state.session.creator]
		// parentTool is already the host-visible prefixed ID for child creators.
		id = acp.ToolCallId(strings.TrimPrefix(state.session.parentTool, "child:"+creator.session.id+":"))
	}
	var update acp.SessionUpdate
	if start {
		if state.groupOpen {
			return nil
		}
		if state.groupKnown {
			update = acp.UpdateToolCall(id, acp.WithUpdateStatus(acp.ToolCallStatusInProgress))
		} else {
			update = acp.StartToolCall(id, "Subagent "+state.session.label, acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusInProgress))
		}
		state.groupOpen = true
		state.groupKnown = true
	} else {
		if !state.groupOpen {
			return nil
		}
		status := acp.ToolCallStatusCompleted
		if failed {
			status = acp.ToolCallStatusFailed
		}
		update = acp.UpdateToolCall(id, acp.WithUpdateStatus(status))
		state.groupOpen = false
	}
	return out.Update(update, nil)
}

func (t *childTree) activity(ctx context.Context, event schema.ItemEvent, method string) error {
	item := event.Item
	if item.AgentThreadID == "" || len(item.AgentThreadID) > 256 || item.ID == "" || len(item.ID) > 256 {
		return ErrRouting
	}
	state := t.states[item.AgentThreadID]
	if state == nil {
		if item.Kind != "started" || method != "item/started" {
			return ErrRouting
		}
		creatorDepth := 0
		if event.ThreadID != t.root {
			creator := t.states[event.ThreadID]
			if creator == nil {
				return ErrRouting
			}
			creatorDepth = creator.session.depth
		}
		sub, err := t.sub.ClaimChild(item.AgentThreadID)
		if err != nil {
			return err
		}
		var response schema.ThreadReadResponse
		readCtx, stop := context.WithTimeout(ctx, controlTimeout)
		err = t.runtime.Client.Call(readCtx, "thread/read", schema.ThreadReadParams{ThreadID: item.AgentThreadID, IncludeTurns: false}, &response)
		stop()
		var source schema.SpawnSource
		if err != nil || response.Thread.ID != item.AgentThreadID || !sameChildDirectory(response.Thread.CWD, t.cfg.start.CWD) || json.Unmarshal(response.Thread.Source, &source) != nil || source.SubAgent.Spawn.Parent != event.ThreadID || source.SubAgent.Spawn.Depth != creatorDepth+1 || creatorDepth >= 64 {
			return setupError("Cannot verify subagent ownership")
		}
		label := t.cfg.clean.String(item.AgentPath)
		runes := []rune(label)
		if len(runes) > 200 {
			label = string(runes[:200])
		}
		parentTool := item.ID
		if event.ThreadID != t.root {
			parentTool = string(t.states[event.ThreadID].out.toolID(acp.ToolCallId(item.ID)))
		}
		child := &childSession{id: item.AgentThreadID, creator: event.ThreadID, parentTool: parentTool, label: label, depth: creatorDepth + 1, sub: sub}
		t.owner.mu.Lock()
		t.owner.children[child.id] = child
		t.owner.mu.Unlock()
		state = t.add(child, false)
		if err := t.attach(state); err != nil {
			return err
		}
	}
	if state.session.creator != event.ThreadID {
		return ErrRouting
	}
	if item.Kind == "started" && method == "item/started" {
		return t.group(state, true, false)
	}
	return nil
}

func (t *childTree) push(ctx context.Context, event Event) (bool, error) {
	var identity struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if json.Unmarshal(event.Params, &identity) != nil {
		return false, ErrRouting
	}
	if event.Method == "thread/started" || event.Method == "thread/status/changed" {
		return identity.ThreadID != t.root, nil
	}
	if identity.ThreadID == t.root || t.states[identity.ThreadID] != nil {
		if err := t.switchOwner(identity.ThreadID); err != nil {
			return true, err
		}
	}
	if state := t.states[identity.ThreadID]; state != nil && identity.TurnID != "" && identity.TurnID == state.session.lastTurn {
		return true, nil
	}
	if event.Method == "item/started" || event.Method == "item/completed" {
		var item schema.ItemEvent
		if json.Unmarshal(event.Params, &item) != nil {
			return true, ErrRouting
		}
		if item.Item.Type == "subAgentActivity" {
			return true, t.activity(ctx, item, event.Method)
		}
	}
	if identity.ThreadID == t.root {
		return false, nil
	}
	state := t.states[identity.ThreadID]
	if state == nil {
		return true, ErrRouting
	}
	if event.Method == "thread/tokenUsage/updated" || event.Method == "error" {
		return true, nil
	}
	if event.Method == "mcpServer/startupStatus/updated" {
		var status schema.MCPStartup
		if json.Unmarshal(event.Params, &status) != nil {
			return true, ErrRouting
		}
		if status.Status != "failed" && status.Status != "cancelled" {
			return true, nil
		}
		if state.startup[status.Name] {
			return true, nil
		}
		state.startup[status.Name] = true
		updates, err := state.mapping.Push(event.Method, event.Params)
		if err != nil {
			return true, err
		}
		return true, t.write(updates, state.out)
	}
	if event.Method == "turn/started" || event.Method == "turn/completed" {
		var lifecycle schema.TurnEvent
		if json.Unmarshal(event.Params, &lifecycle) != nil {
			return true, ErrRouting
		}
		identity.TurnID = lifecycle.Turn.ID
	}
	if identity.TurnID == "" || len(identity.TurnID) > 256 {
		return true, ErrRouting
	}
	if identity.TurnID == state.session.lastTurn {
		return true, nil
	}
	if state.turn != identity.TurnID {
		if state.active {
			return true, ErrRouting
		}
		if err := t.write(state.mapping.Finish(), state.out); err != nil {
			return true, err
		}
		state.mapping = mapper.New()
		state.mapping.ConfigureMCP(t.cfg.mcpNames, t.cfg.clean)
		state.turn = identity.TurnID
		state.active = true
		if err := t.group(state, true, false); err != nil {
			return true, err
		}
	}
	if event.Method == "turn/completed" {
		var lifecycle schema.TurnEvent
		_ = json.Unmarshal(event.Params, &lifecycle)
		if err := t.write(state.mapping.Finish(), state.out); err != nil {
			return true, err
		}
		state.active = false
		state.session.lastTurn = state.turn
		return true, t.group(state, false, lifecycle.Turn.Status != "completed")
	}
	if event.Method == "turn/started" {
		return true, nil
	}
	updates, err := state.mapping.Push(event.Method, event.Params)
	if err != nil {
		return true, err
	}
	return true, t.write(updates, state.out)
}

func (t *childTree) active() bool {
	for _, state := range t.states {
		if state.active || state.groupOpen {
			return true
		}
	}
	return false
}
func (t *childTree) targets() []*childSession {
	result := make([]*childSession, 0, len(t.states))
	for _, state := range t.states {
		result = append(result, state.session)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].id < result[j].id })
	return result
}

func interruptChildren(ctx context.Context, runtime connection.Runtime, targets []*childSession) error {
	for _, child := range targets {
		turn, active := child.sub.CurrentTurn()
		if turn != "" {
			if err := runtime.Client.WaitResponses(ctx, child.id, turn); err != nil {
				return err
			}
		}
		if active {
			if err := runtime.Client.Call(ctx, "turn/interrupt", schema.TurnInterruptParams{ThreadID: child.id, TurnID: turn}, &struct{}{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *childTree) finish() error {
	for _, child := range t.targets() {
		state := t.states[child.id]
		if err := t.write(state.mapping.Finish(), state.out); err != nil {
			return err
		}
	}
	return nil
}

func sameChildDirectory(observed, expected string) bool {
	observed, expected = filepath.Clean(observed), filepath.Clean(expected)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(observed, expected)
	}
	return observed == expected
}
