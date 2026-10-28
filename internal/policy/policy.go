// SPDX-License-Identifier: Apache-2.0

// Package policy translates resolved host permissions without I/O or side effects.
package policy

import (
	"errors"
	"regexp"
	"strings"

	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Policy is an immutable compiled snapshot of host permissions.
type Policy struct {
	read, edit, network, command, mcp protocol.PermissionLevel
	allow, deny                       []*regexp.Regexp
	tools                             map[string]protocol.PermissionLevel
}

// Request lists all capabilities a vendor approval may grant.
type Request struct {
	Categories []protocol.PermissionCategory
	Command    *string
	Tool       string
}

// Compile validates policy and applies read-only normalization without role-name logic.
func Compile(meta protocol.SessionMeta) (*Policy, error) {
	if err := meta.Validate(); err != nil {
		return nil, errors.New("invalid session policy")
	}
	meta = meta.Normalize()
	p := &Policy{read: protocol.PermissionLevelAsk, edit: protocol.PermissionLevelAsk, network: protocol.PermissionLevelAsk, command: protocol.PermissionLevelAsk, mcp: protocol.PermissionLevelAsk, tools: map[string]protocol.PermissionLevel{}}
	if in := meta.Permissions; in != nil {
		p.read = level(in.Read)
		p.edit = level(in.Edit)
		p.network = level(in.Network)
		if in.Commands != nil {
			p.command = level(in.Commands.Default)
			var err error
			p.allow, err = patterns(in.Commands.Allow)
			if err != nil {
				return nil, err
			}
			p.deny, err = patterns(in.Commands.Deny)
			if err != nil {
				return nil, err
			}
		}
		if in.MCP != nil {
			p.mcp = level(in.MCP.Default)
			for k, v := range in.MCP.Tools {
				p.tools[k] = level(v)
			}
		}
	}
	return p, nil
}

func level(l protocol.PermissionLevel) protocol.PermissionLevel {
	if l == "" {
		return protocol.PermissionLevelAsk
	}
	return l
}
func patterns(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		if pattern == "" || len(pattern) > 8192 || strings.ContainsAny(pattern, "?[\x00\r\n") {
			return nil, errors.New("command patterns support literals and asterisk only")
		}
		pieces := strings.Split(pattern, "*")
		for i := range pieces {
			pieces[i] = regexp.QuoteMeta(pieces[i])
		}
		expression, err := regexp.Compile("(?s)^" + strings.Join(pieces, ".*") + "$")
		if err != nil {
			return nil, errors.New("invalid command pattern")
		}
		result = append(result, expression)
	}
	return result, nil
}

// Sandbox returns the vendor-native baseline; no host mode can disable it.
func (p *Policy) Sandbox() string {
	if p.edit == protocol.PermissionLevelAllow {
		return "workspace-write"
	}
	return "read-only"
}

// NetworkAllowed permits the native workspace network setting only on explicit allow.
func (p *Policy) NetworkAllowed() bool { return p.network == protocol.PermissionLevelAllow }

// ReadDenied identifies the documented native-read limitation for a safe warning.
func (p *Policy) ReadDenied() bool { return p.read == protocol.PermissionLevelDeny }

// Evaluate combines requested capabilities: deny dominates ask, which dominates allow.
func (p *Policy) Evaluate(r Request) protocol.PermissionLevel {
	decision := protocol.PermissionLevelAllow
	if len(r.Categories) == 0 {
		return protocol.PermissionLevelAsk
	}
	for _, category := range r.Categories {
		current := protocol.PermissionLevelAsk
		switch category {
		case protocol.PermissionCategoryRead:
			current = p.read
		case protocol.PermissionCategoryEdit:
			current = p.edit
		case protocol.PermissionCategoryNetwork:
			current = p.network
		case protocol.PermissionCategoryMCP:
			current = p.mcp
			if l, ok := p.tools[r.Tool]; ok {
				current = l
			}
		case protocol.PermissionCategoryCommand:
			current = p.commandDecision(r.Command)
		}
		if current == protocol.PermissionLevelDeny {
			return current
		}
		if current != protocol.PermissionLevelAllow {
			decision = protocol.PermissionLevelAsk
		}
	}
	return decision
}

func (p *Policy) commandDecision(command *string) protocol.PermissionLevel {
	if command == nil || strings.TrimSpace(*command) == "" {
		if p.command == protocol.PermissionLevelDeny {
			return p.command
		}
		return protocol.PermissionLevelAsk
	}
	for _, pattern := range p.deny {
		if pattern.MatchString(*command) {
			return protocol.PermissionLevelDeny
		}
	}
	for _, pattern := range p.allow {
		if pattern.MatchString(*command) {
			return protocol.PermissionLevelAllow
		}
	}
	return p.command
}
