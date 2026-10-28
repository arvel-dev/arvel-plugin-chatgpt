// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/connection"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/policy"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func (s *Session) approve(ctx, permissionContext context.Context, method string, raw json.RawMessage, out plugin.Output, runtime connection.Runtime, sub *Subscription, thread string, bind func(string) bool) (any, error) {
	var identity schema.ApprovalIdentity
	if json.Unmarshal(raw, &identity) != nil || identity.ThreadID != thread || identity.ItemID == "" || !bind(identity.TurnID) {
		return denial(method), nil
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(permissionContext, cancel)
	defer func() { stop(); cancel() }()
	if ctx.Err() != nil {
		return schema.DecisionResponse{Decision: "cancel"}, nil
	}
	s.mu.Lock()
	cfg := s.config
	s.mu.Unlock()
	request := plugin.PermissionRequest{ToolCall: acp.ToolCallUpdate{ToolCallId: acp.ToolCallId(identity.ItemID)}, Options: []acp.PermissionOption{
		{OptionId: "allow_once", Name: "Allow once", Kind: acp.PermissionOptionKindAllowOnce},
		{OptionId: "allow_always", Name: "Allow for this session", Kind: acp.PermissionOptionKindAllowAlways},
		{OptionId: "reject_once", Name: "Reject", Kind: acp.PermissionOptionKindRejectOnce},
	}}
	var decision protocol.PermissionLevel
	switch method {
	case "item/commandExecution/requestApproval":
		var command schema.CommandApproval
		if json.Unmarshal(raw, &command) != nil {
			return denial(method), nil
		}
		// A native sandbox failure can retry this exact command unsandboxed.
		// Command allow alone must never authorize denied edits or network.
		decision = cfg.policy.Evaluate(policy.Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryCommand, protocol.PermissionCategoryEdit, protocol.PermissionCategoryNetwork}, Command: command.Command})
		kind, title := acp.ToolKindExecute, "Command requiring sandbox escalation"
		if command.Command != nil {
			title = *command.Command
			request.Meta.Command = *command.Command
		}
		request.ToolCall.Kind, request.ToolCall.Title = &kind, &title
		request.Meta.Category = protocol.PermissionCategoryCommand
		request.Meta.Reason = "This command may run outside the native sandbox, including file writes and network access."
		if command.Network != nil && command.Network.Host != "" {
			request.Meta.Hosts = []string{command.Network.Host}
		}
	case "item/fileChange/requestApproval":
		var file schema.FileApproval
		if json.Unmarshal(raw, &file) != nil {
			return denial(method), nil
		}
		// SDK Call responses wait for all preceding notification callbacks. Its
		// independent request handler may otherwise outrun item/started caching.
		barrierContext, stop := context.WithTimeout(ctx, controlTimeout)
		var ignored schema.ConfigReadResponse
		err := runtime.Client.Call(barrierContext, "config/read", schema.ConfigReadParams{IncludeLayers: false}, &ignored)
		stop()
		if err != nil {
			if ctx.Err() != nil {
				return schema.DecisionResponse{Decision: "cancel"}, nil
			}
			return denial(method), nil
		}
		paths := sub.ItemPaths(identity.ItemID)
		if len(paths) == 0 {
			return denial(method), nil
		}
		for i, path := range paths {
			resolved, err := safeEditPath(cfg.start.CWD, path, runtime.Home)
			if err != nil {
				return denial(method), nil
			}
			paths[i] = resolved
		}
		if file.GrantRoot != nil {
			root, err := safeEditPath(cfg.start.CWD, *file.GrantRoot, runtime.Home)
			if err != nil {
				return denial(method), nil
			}
			for _, path := range paths {
				if !containsPath(root, path) {
					return denial(method), nil
				}
			}
		}
		decision = cfg.policy.Evaluate(policy.Request{Categories: []protocol.PermissionCategory{protocol.PermissionCategoryEdit}})
		kind, title := acp.ToolKindEdit, "Edit files"
		request.ToolCall.Kind, request.ToolCall.Title = &kind, &title
		for _, path := range paths {
			request.ToolCall.Locations = append(request.ToolCall.Locations, acp.ToolCallLocation{Path: path})
		}
		request.Meta.Category, request.Meta.Paths = protocol.PermissionCategoryEdit, paths
	default:
		return denial(method), nil
	}
	if decision == protocol.PermissionLevelDeny {
		return denial(method), nil
	}
	if ctx.Err() != nil {
		return schema.DecisionResponse{Decision: "cancel"}, nil
	}
	if decision == protocol.PermissionLevelAllow {
		return schema.DecisionResponse{Decision: "accept"}, nil
	}
	answer, err := out.RequestPermission(ctx, request)
	if ctx.Err() != nil || answer.Cancelled {
		return schema.DecisionResponse{Decision: "cancel"}, nil
	}
	if err != nil {
		return denial(method), nil
	}
	switch answer.OptionID {
	case "allow_once":
		return schema.DecisionResponse{Decision: "accept"}, nil
	case "allow_always":
		return schema.DecisionResponse{Decision: "acceptForSession"}, nil
	default:
		return denial(method), nil
	}
}

// safeEditPath resolves existing ancestors without reading file contents. New
// files are allowed, symlink escapes and any private-home overlap are rejected.
func safeEditPath(cwd, path, home string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", ErrRouting
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	ancestor := path
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			private, err := filepath.EvalSymlinks(home)
			if err != nil || !containsPath(cwd, resolved) || containsPath(private, resolved) || containsPath(resolved, private) {
				return "", ErrRouting
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) || filepath.Dir(ancestor) == ancestor {
			return "", ErrRouting
		}
		// A dangling symlink must not be treated as an ordinary missing path.
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			return "", ErrRouting
		}
		tail = append(tail, filepath.Base(ancestor))
		ancestor = filepath.Dir(ancestor)
	}
}
