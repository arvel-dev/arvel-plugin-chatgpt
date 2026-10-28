// SPDX-License-Identifier: Apache-2.0

// Package mapper translates recorded parent-thread events into ACP updates.
package mapper

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/sanitize"
	acp "github.com/coder/acp-go-sdk"
)

const maxItems = 4096
const maxDiff = 8 << 20

// Mapper owns one turn. Callers serialize Push and Finish.
type Mapper struct {
	pending   *acp.SessionUpdate
	diff      string
	tools     map[string]uint8
	streamed  map[string]bool
	names     map[string]string
	clean     *sanitize.Cleaner
	text      map[string]string
	textOrder []string
	textBytes int
}

// New initializes isolated, bounded per-turn mapping state.
func New() *Mapper {
	return &Mapper{tools: map[string]uint8{}, streamed: map[string]bool{}, text: map[string]string{}}
}

// Flush preserves cross-thread order without finishing the turn's diff metadata.
func (m *Mapper) Flush() []acp.SessionUpdate {
	if m.pending == nil {
		return nil
	}
	update := *m.pending
	m.pending = nil
	return []acp.SessionUpdate{update}
}

// ConfigureMCP installs owned display names and a session-specific secret filter.
func (m *Mapper) ConfigureMCP(names map[string]string, clean *sanitize.Cleaner) {
	m.names, m.clean = maps.Clone(names), clean
}

func (m *Mapper) textDelta(key, delta string, final bool) (string, error) {
	old, exists := m.text[key]
	if !exists {
		if len(m.textOrder) >= 2*maxItems {
			return "", errors.New("turn text stream limit exceeded")
		}
		m.textOrder = append(m.textOrder, key)
	}
	joined := old + delta
	emit, tail := m.clean.Split(joined, final)
	m.textBytes += len(tail) - len(old)
	if m.textBytes > maxDiff {
		return "", errors.New("text redaction buffer limit exceeded")
	}
	if final {
		delete(m.text, key)
	} else {
		m.text[key] = tail
	}
	return emit, nil
}

// Push maps one observed event and retains one trailing update for final metadata.
func (m *Mapper) Push(method string, raw json.RawMessage) ([]acp.SessionUpdate, error) {
	var updates []acp.SessionUpdate
	switch method {
	case "mcpServer/startupStatus/updated":
		var v schema.MCPStartup
		if json.Unmarshal(raw, &v) != nil {
			return nil, errors.New("invalid MCP startup event")
		}
		name, assigned := m.names[v.Name]
		if !assigned || (v.Status != "failed" && v.Status != "cancelled") {
			break
		}
		id := acp.ToolCallId("mcp-startup:" + v.Name)
		if m.tools[string(id)] != 0 {
			break
		}
		if len(m.tools) >= maxItems {
			return nil, errors.New("turn item limit exceeded")
		}
		m.tools[string(id)] = 2
		updates = append(updates, acp.StartToolCall(id, limitRunes(m.clean.String("MCP startup ("+name+")"), 200), acp.WithStartKind(acp.ToolKindOther), acp.WithStartStatus(acp.ToolCallStatusFailed), acp.WithStartContent([]acp.ToolCallContent{acp.ToolContent(acp.TextBlock("MCP server could not start"))})))
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
			text, err := m.textDelta("message:"+v.ItemID, v.Delta, false)
			if err != nil {
				return nil, err
			}
			if text != "" {
				updates = append(updates, acp.UpdateAgentMessageText(text))
			}
		} else {
			text, err := m.textDelta("thought:"+v.ItemID, v.Delta, false)
			if err != nil {
				return nil, err
			}
			if text != "" {
				updates = append(updates, acp.UpdateAgentThoughtText(text))
			}
		}
	case "turn/diff/updated":
		var v schema.DiffEvent
		if json.Unmarshal(raw, &v) != nil || len(v.Diff) > maxDiff {
			return nil, errors.New("invalid or excessive turn diff")
		}
		m.diff = m.clean.String(v.Diff)
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
			entries = append(entries, acp.PlanEntry{Content: m.clean.String(step.Step), Priority: acp.PlanEntryPriorityMedium, Status: status})
		}
		updates = append(updates, acp.UpdatePlan(entries...))
	case "item/started", "item/completed":
		var event schema.ItemEvent
		if json.Unmarshal(raw, &event) != nil || event.Item.ID == "" {
			return nil, errors.New("invalid item event")
		}
		it := event.Item
		if method == "item/completed" {
			for _, prefix := range []string{"message:", "thought:"} {
				key := prefix + it.ID
				if _, ok := m.text[key]; ok {
					text, err := m.textDelta(key, "", true)
					if err != nil {
						return nil, err
					}
					if text != "" {
						if prefix == "message:" {
							updates = append(updates, acp.UpdateAgentMessageText(text))
						} else {
							updates = append(updates, acp.UpdateAgentThoughtText(text))
						}
					}
				}
			}
		}
		it.Text = m.clean.String(it.Text)
		it.Command = m.clean.String(it.Command)
		it.Query = m.clean.String(it.Query)
		if name, ok := m.names[it.Server]; ok {
			it.Server = name
		}
		it.Server = m.clean.String(it.Server)
		it.Tool = m.clean.String(it.Tool)
		for i := range it.Changes {
			it.Changes[i].Path = m.clean.String(it.Changes[i].Path)
			it.Changes[i].Diff = m.clean.String(it.Changes[i].Diff)
		}
		if it.Output != nil {
			text := m.clean.String(*it.Output)
			it.Output = &text
		}
		completed := method == "item/completed"
		if it.Type == "agentMessage" {
			if completed && !m.streamed[it.ID] && it.Text != "" {
				if len(m.streamed) >= maxItems {
					return nil, errors.New("turn item limit exceeded")
				}
				m.streamed[it.ID] = true
				updates = append(updates, acp.UpdateAgentMessageText(m.clean.String(it.Text)))
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
			if it.Type == "mcpToolCall" && len(it.Result) > 0 && string(it.Result) != "null" {
				var result schema.MCPResult
				if json.Unmarshal(it.Result, &result) != nil {
					return nil, errors.New("invalid MCP result")
				}
				var text strings.Builder
				for _, block := range result.Content {
					if block.Type == "text" {
						text.WriteString(block.Text)
						text.WriteByte('\n')
					}
				}
				if text.Len() > 0 {
					content = append(content, acp.ToolContent(acp.TextBlock(limitBytes(m.clean.String(text.String()), 8192))))
				}
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
	var flushed []acp.SessionUpdate
	for _, key := range m.textOrder {
		if tail, ok := m.text[key]; ok {
			delete(m.text, key)
			text := m.clean.String(tail)
			if text != "" {
				if strings.HasPrefix(key, "message:") {
					flushed = append(flushed, acp.UpdateAgentMessageText(text))
				} else {
					flushed = append(flushed, acp.UpdateAgentThoughtText(text))
				}
			}
		}
	}
	m.textOrder = nil
	m.textBytes = 0
	result := m.hold(flushed)
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
	return append(result, u)
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
