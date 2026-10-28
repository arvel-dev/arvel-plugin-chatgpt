// SPDX-License-Identifier: Apache-2.0

// Package mapper translates recorded parent-thread events into ACP updates.
package mapper

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"unicode/utf8"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	acp "github.com/coder/acp-go-sdk"
)

const maxItems = 4096
const maxDiff = 8 << 20

// Mapper owns one turn. Callers serialize Push and Finish.
type Mapper struct {
	pending  *acp.SessionUpdate
	diff     string
	tools    map[string]uint8
	streamed map[string]bool
}

// New initializes isolated, bounded per-turn mapping state.
func New() *Mapper { return &Mapper{tools: map[string]uint8{}, streamed: map[string]bool{}} }

// Push maps one observed event and retains one trailing update for final metadata.
func (m *Mapper) Push(method string, raw json.RawMessage) ([]acp.SessionUpdate, error) {
	var updates []acp.SessionUpdate
	switch method {
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta":
		var v schema.DeltaEvent
		if json.Unmarshal(raw, &v) != nil || v.ItemID == "" {
			return nil, errors.New("invalid text event")
		}
		if len(m.streamed) >= maxItems && !m.streamed[v.ItemID] {
			return nil, errors.New("turn item limit exceeded")
		}
		if method == "item/agentMessage/delta" {
			m.streamed[v.ItemID] = true
			updates = append(updates, acp.UpdateAgentMessageText(v.Delta))
		} else {
			updates = append(updates, acp.UpdateAgentThoughtText(v.Delta))
		}
	case "turn/diff/updated":
		var v schema.DiffEvent
		if json.Unmarshal(raw, &v) != nil || len(v.Diff) > maxDiff {
			return nil, errors.New("invalid or excessive turn diff")
		}
		m.diff = v.Diff
	case "turn/plan/updated":
		var v schema.PlanEvent
		if json.Unmarshal(raw, &v) != nil || len(v.Plan) > maxItems {
			return nil, errors.New("invalid plan event")
		}
		entries := make([]acp.PlanEntry, 0, len(v.Plan))
		for _, step := range v.Plan {
			status := acp.PlanEntryStatus(step.Status)
			switch status {
			case acp.PlanEntryStatusPending, acp.PlanEntryStatusInProgress, acp.PlanEntryStatusCompleted:
			default:
				return nil, errors.New("invalid plan step status")
			}
			entries = append(entries, acp.PlanEntry{Content: step.Step, Priority: acp.PlanEntryPriorityMedium, Status: status})
		}
		updates = append(updates, acp.UpdatePlan(entries...))
	case "item/started", "item/completed":
		var event schema.ItemEvent
		if json.Unmarshal(raw, &event) != nil || event.Item.ID == "" {
			return nil, errors.New("invalid item event")
		}
		it := event.Item
		completed := method == "item/completed"
		if it.Type == "agentMessage" {
			if completed && !m.streamed[it.ID] && it.Text != "" {
				if len(m.streamed) >= maxItems {
					return nil, errors.New("turn item limit exceeded")
				}
				m.streamed[it.ID] = true
				updates = append(updates, acp.UpdateAgentMessageText(it.Text))
			}
			break
		}
		kind, title, content, locations, known, err := tool(it)
		if err != nil {
			return nil, err
		}
		if !known {
			break
		}
		state := m.tools[it.ID]
		if state == 2 {
			break
		}
		if state == 0 {
			if len(m.tools) >= maxItems {
				return nil, errors.New("turn item limit exceeded")
			}
			m.tools[it.ID] = 1
			updates = append(updates, acp.StartToolCall(acp.ToolCallId(it.ID), title, acp.WithStartKind(kind), acp.WithStartStatus(acp.ToolCallStatusInProgress), acp.WithStartContent(content), acp.WithStartLocations(locations)))
		}
		if completed {
			m.tools[it.ID] = 2
			status := acp.ToolCallStatusCompleted
			if it.Status == "failed" || it.Status == "declined" || (it.ExitCode != nil && *it.ExitCode != 0) {
				status = acp.ToolCallStatusFailed
			}
			if it.Output != nil {
				content = append(content, acp.ToolContent(acp.TextBlock(limitBytes(*it.Output, 8192))))
			}
			opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status), acp.WithUpdateContent(content), acp.WithUpdateLocations(locations)}
			if it.ExitCode != nil {
				opts = append(opts, acp.WithUpdateRawOutput(map[string]any{"exitCode": *it.ExitCode}))
			}
			updates = append(updates, acp.UpdateToolCall(acp.ToolCallId(it.ID), opts...))
		}
	}
	return m.hold(updates), nil
}

func tool(it schema.Item) (acp.ToolKind, string, []acp.ToolCallContent, []acp.ToolCallLocation, bool, error) {
	var kind acp.ToolKind
	var title string
	var content []acp.ToolCallContent
	var locations []acp.ToolCallLocation
	switch it.Type {
	case "commandExecution":
		kind = acp.ToolKindExecute
		title = it.Command
		if title == "" {
			title = "Command"
		}
	case "fileChange":
		kind = acp.ToolKindEdit
		title = "Edit files"
		if len(it.Changes) > maxItems {
			return kind, "", nil, nil, false, errors.New("too many file changes")
		}
		for _, change := range it.Changes {
			if change.Path == "" || len(change.Diff) > maxDiff {
				return kind, "", nil, nil, false, errors.New("invalid file change")
			}
			locations = append(locations, acp.ToolCallLocation{Path: change.Path})
			switch change.Kind.Type {
			case "add":
				content = append(content, acp.ToolDiffContent(change.Path, change.Diff))
			case "delete":
				content = append(content, acp.ToolDiffContent(change.Path, "", change.Diff))
			default:
				// Unified hunks are not whole-file old/new content. Keep them as text
				// instead of fabricating full file contents for ACP's Diff type.
				content = append(content, acp.ToolContent(acp.TextBlock(change.Path+"\n"+change.Diff)))
			}
		}
	case "mcpToolCall":
		kind = acp.ToolKindOther
		title = it.Tool + " (" + it.Server + ")"
	case "webSearch":
		kind = acp.ToolKindFetch
		title = "Search: " + it.Query
	default:
		return "", "", nil, nil, false, nil
	}
	return kind, limitRunes(title, 200), content, locations, true, nil
}

func (m *Mapper) hold(updates []acp.SessionUpdate) []acp.SessionUpdate {
	var result []acp.SessionUpdate
	for _, u := range updates {
		if m.pending != nil {
			result = append(result, *m.pending)
		}
		next := u
		m.pending = &next
	}
	return result
}

// Finish emits the final update exactly once, with nested ACP diff metadata.
func (m *Mapper) Finish() []acp.SessionUpdate {
	if m.pending == nil && m.diff != "" {
		u := acp.UpdateAgentMessageText("")
		m.pending = &u
	}
	if m.pending == nil {
		return nil
	}
	u := *m.pending
	m.pending = nil
	if m.diff != "" {
		var target *map[string]any
		switch {
		case u.AgentMessageChunk != nil:
			target = &u.AgentMessageChunk.Meta
		case u.AgentThoughtChunk != nil:
			target = &u.AgentThoughtChunk.Meta
		case u.ToolCall != nil:
			target = &u.ToolCall.Meta
		case u.ToolCallUpdate != nil:
			target = &u.ToolCallUpdate.Meta
		case u.Plan != nil:
			target = &u.Plan.Meta
		}
		if target != nil {
			meta := maps.Clone(*target)
			if meta == nil {
				meta = map[string]any{}
			}
			arvel, _ := meta["arvel"].(map[string]any)
			arvel = maps.Clone(arvel)
			if arvel == nil {
				arvel = map[string]any{}
			}
			arvel["diff"] = m.diff
			meta["arvel"] = arvel
			*target = meta
		}
		m.diff = ""
	}
	return []acp.SessionUpdate{u}
}

func limitRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
func limitBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const suffix = "\n[truncated]"
	end := n - len(suffix)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return fmt.Sprintf("%s%s", s[:end], suffix)
}
