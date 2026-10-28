// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/sanitize"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var headerKey = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func mcpName(name string) string {
	digest := sha256.Sum256([]byte(name))
	var slug strings.Builder
	for _, r := range name {
		if slug.Len() >= 24 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			slug.WriteRune(r)
		} else {
			slug.WriteByte('_')
		}
	}
	return "arvel_" + slug.String() + "_" + hex.EncodeToString(digest[:6])
}

func mcpConfig(params plugin.SessionParams) (map[string]any, map[string]string, *sanitize.Cleaner, error) {
	bad := setupError("Invalid or unsupported assigned MCP servers")
	encoded, err := json.Marshal(params.MCPServers)
	if err != nil || len(encoded) > 1<<20 || len(params.MCPServers) > 64 {
		return nil, nil, nil, bad
	}
	config := map[string]any{}
	names := map[string]string{}
	seen := map[string]bool{}
	var secrets []string
	for _, server := range params.MCPServers {
		variants := 0
		if server.Stdio != nil {
			variants++
		}
		if server.Http != nil {
			variants++
		}
		if server.Sse != nil {
			variants++
		}
		if server.Acp != nil {
			variants++
		}
		if variants != 1 {
			return nil, nil, nil, bad
		}
		var name string
		entry := map[string]any{}
		switch {
		case server.Stdio != nil:
			v := server.Stdio
			name = v.Name
			if v.Command == "" || strings.ContainsRune(v.Command, 0) || len(v.Args) > 256 || len(v.Env) > 128 {
				return nil, nil, nil, bad
			}
			entry["command"], entry["args"] = v.Command, slices.Clone(v.Args)
			env := map[string]string{}
			keys := map[string]bool{}
			for _, kv := range v.Env {
				key := strings.ToUpper(kv.Name)
				if !envKey.MatchString(kv.Name) || keys[key] || strings.ContainsRune(kv.Value, 0) {
					return nil, nil, nil, bad
				}
				keys[key] = true
				env[kv.Name] = kv.Value
				secrets = append(secrets, kv.Value)
			}
			for _, arg := range v.Args {
				if strings.ContainsRune(arg, 0) {
					return nil, nil, nil, bad
				}
				secrets = append(secrets, arg)
			}
			entry["env"] = env
		case server.Http != nil:
			v := server.Http
			name = v.Name
			u, err := url.Parse(v.Url)
			if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || len(v.Headers) > 128 {
				return nil, nil, nil, bad
			}
			headers := map[string]string{}
			keys := map[string]bool{}
			for _, header := range v.Headers {
				key := strings.ToLower(header.Name)
				if !headerKey.MatchString(header.Name) || strings.ContainsAny(header.Value, "\x00\r\n") || keys[key] {
					return nil, nil, nil, bad
				}
				keys[key] = true
				headers[header.Name] = header.Value
				secrets = append(secrets, header.Value)
				if strings.HasPrefix(strings.ToLower(header.Value), "bearer ") {
					secrets = append(secrets, strings.TrimSpace(header.Value[7:]))
				}
			}
			for _, values := range u.Query() {
				secrets = append(secrets, values...)
			}
			entry["url"], entry["http_headers"] = v.Url, headers
		default:
			return nil, nil, nil, bad
		}
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") || seen[name] {
			return nil, nil, nil, bad
		}
		seen[name] = true
		native := mcpName(name)
		if _, collision := names[native]; collision {
			return nil, nil, nil, bad
		}
		names[native] = name
		if err := mcpRules(entry, name, params.Meta); err != nil {
			return nil, nil, nil, bad
		}
		config[native] = entry
	}
	return config, names, secretCleaner(secrets), nil
}

func mcpRules(entry map[string]any, name string, meta protocol.SessionMeta) error {
	base := protocol.PermissionLevelAsk
	var overrides map[string]protocol.PermissionLevel
	if meta.Permissions != nil && meta.Permissions.MCP != nil {
		base = meta.Permissions.MCP.Default
		if base == "" {
			base = protocol.PermissionLevelAsk
		}
		overrides = meta.Permissions.MCP.Tools
	}
	if name == "arvel" {
		entry["default_tools_approval_mode"] = "approve"
		return nil
	}
	entry["default_tools_approval_mode"] = "prompt"
	if base == protocol.PermissionLevelAllow {
		entry["default_tools_approval_mode"] = "approve"
	}
	allowed, denied := []string{}, []string{}
	tools := map[string]any{}
	for key, level := range overrides {
		tool, match := strings.CutPrefix(key, name+"/")
		if !match {
			continue
		}
		if tool == "" || len(tool) > 256 || strings.ContainsAny(tool, "\x00\r\n") {
			return ErrRouting
		}
		if level == protocol.PermissionLevelDeny {
			denied = append(denied, tool)
			continue
		}
		allowed = append(allowed, tool)
		mode := "prompt"
		if level == protocol.PermissionLevelAllow {
			mode = "approve"
		}
		tools[tool] = map[string]any{"approval_mode": mode}
	}
	slices.Sort(allowed)
	slices.Sort(denied)
	if base == protocol.PermissionLevelDeny {
		entry["enabled_tools"] = allowed
	}
	if len(denied) > 0 {
		entry["disabled_tools"] = denied
	}
	if len(tools) > 0 {
		entry["tools"] = tools
	}
	return nil
}

func secretCleaner(values []string) *sanitize.Cleaner {
	var forms []string
	for _, value := range values {
		if value != "" {
			forms = append(forms, value, url.QueryEscape(value), url.PathEscape(value))
		}
	}
	return sanitize.New(forms)
}

func (s *Session) approveMCP(ctx, permissionContext context.Context, raw json.RawMessage, out plugin.Output, thread string, bind func(string) bool) (any, error) {
	var req schema.MCPApproval
	if json.Unmarshal(raw, &req) != nil || req.ThreadID != thread || req.TurnID == nil || req.Mode != "form" || req.Meta.Kind != "mcp_tool_call" || req.Schema.Type != "object" || req.Schema.Properties == nil || len(req.Schema.Properties) != 0 || len(req.Schema.Required) != 0 {
		return schema.ElicitationResponse{Action: "decline"}, nil
	}
	s.mu.Lock()
	name, assigned := s.config.mcpNames[req.Server]
	clean := s.config.clean
	s.mcpApprovalSeq++
	id := fmt.Sprintf("mcp-approval-%d", s.mcpApprovalSeq)
	s.mu.Unlock()
	if !assigned || !bind(*req.TurnID) {
		return schema.ElicitationResponse{Action: "decline"}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(permissionContext, cancel)
	defer func() { stop(); cancel() }()
	if ctx.Err() != nil {
		return schema.ElicitationResponse{Action: "cancel"}, nil
	}
	kind, title := acp.ToolKindOther, clean.String("MCP tool ("+name+")")
	// Forced native prompt mode deliberately offers no persistent/session grant.
	// The per-tool native config already enforces allow/deny before this request.
	answer, err := out.RequestPermission(ctx, plugin.PermissionRequest{
		ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(id), Kind: &kind, Title: &title},
		Options:  []acp.PermissionOption{{OptionId: "allow_once", Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce}, {OptionId: "reject_once", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce}},
		Meta:     protocol.PermissionMeta{Category: protocol.PermissionCategoryMCP, Reason: clean.String(req.Message)},
	})
	if ctx.Err() != nil || answer.Cancelled {
		return schema.ElicitationResponse{Action: "cancel"}, nil
	}
	if err == nil && answer.OptionID == "allow_once" {
		return schema.ElicitationResponse{Action: "accept", Content: map[string]any{}}, nil
	}
	return schema.ElicitationResponse{Action: "decline"}, nil
}
