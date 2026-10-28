// SPDX-License-Identifier: Apache-2.0

package discovery

import (
	"context"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/redact"
)

type server struct {
	Command      string
	Args         []string
	URL          string
	Env          map[string]string
	EnvVars      []any             `toml:"env_vars"`
	Headers      map[string]string `toml:"http_headers"`
	EnvHeaders   map[string]string `toml:"env_http_headers"`
	Bearer       string            `toml:"bearer_token"`
	BearerEnv    string            `toml:"bearer_token_env_var"`
	HeaderHelper string            `toml:"http_headers_helper"`
	Environment  string            `toml:"environment_id"`
	Enabled      *bool
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func readMCP(ctx context.Context, roots Roots, b *budget) ([]protocol.DiscoveredMcp, error) {
	result := []protocol.DiscoveredMcp{}
	rel, _ := filepath.Rel(roots.Home, roots.CodexHome)
	data, err := readFile(ctx, roots.Home, filepath.Join(rel, "config.toml"), b)
	if err != nil || len(data) == 0 {
		return result, err
	}
	var config struct {
		Servers map[string]server `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, ErrSource // Parser errors can quote secret-bearing source lines.
	}
	b.entries += len(config.Servers)
	if b.entries > maxEntries {
		return nil, ErrLimit
	}
	var secrets []string
	var names []string
	for name, s := range config.Servers {
		names = append(names, name)
		for _, fields := range []map[string]string{s.Env, s.Headers} {
			for _, value := range fields {
				if value != "" {
					secrets = append(secrets, value)
				}
			}
		}
		if s.Bearer != "" {
			secrets = append(secrets, s.Bearer)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, ok := projectServer(name, config.Servers[name], secrets)
		if ok {
			m.Source = "~/" + filepath.ToSlash(filepath.Join(rel, "config.toml"))
			result = append(result, m)
		}
	}
	return result, nil
}

func public(s string, secrets []string) bool {
	if len(s) > 8192 || strings.ContainsAny(s, "\x00\r\n") || redact.New().String(s) != s {
		return false
	}
	for _, secret := range secrets {
		// Unlike a log redactor, projection must also reject short literal secrets.
		if strings.Contains(s, secret) || strings.Contains(s, url.QueryEscape(secret)) || strings.Contains(s, url.PathEscape(secret)) {
			return false
		}
	}
	return true
}

func projectServer(name string, s server, secrets []string) (protocol.DiscoveredMcp, bool) {
	m := protocol.DiscoveredMcp{Name: name, Scope: protocol.DiscoverScopeUser, Source: "~/.codex/config.toml"}
	if name == "" || !public(name, secrets) || s.HeaderHelper != "" || (s.Environment != "" && s.Environment != "local") || (s.Enabled != nil && !*s.Enabled) {
		return m, false
	}
	if (s.Command == "") == (s.URL == "") {
		return m, false
	}
	if s.Command != "" {
		if s.Bearer != "" || s.BearerEnv != "" || len(s.Headers) != 0 || len(s.EnvHeaders) != 0 {
			return m, false
		}
		m.Transport = protocol.McpTransportStdio
		m.Command = append([]string{s.Command}, s.Args...)
		if len(m.Command) > 256 {
			return m, false
		}
		for _, arg := range m.Command {
			if !public(arg, secrets) || sensitiveArg(arg) {
				return m, false
			}
		}
	} else {
		if len(s.Env) != 0 || len(s.EnvVars) != 0 || len(s.Args) != 0 {
			return m, false
		}
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !public(s.URL, secrets) {
			return m, false
		}
		m.Transport, m.URL = protocol.McpTransportHTTP, s.URL
	}
	m.Env = map[string]protocol.SecretField{}
	m.Headers = map[string]protocol.SecretField{}
	for k := range s.Env {
		if !envName.MatchString(k) || !public(k, secrets) {
			return m, false
		}
		m.Env[k] = protocol.SecretField{Secret: true}
	}
	for _, raw := range s.EnvVars {
		var key string
		switch v := raw.(type) {
		case string:
			key = v
		case map[string]any:
			key, _ = v["name"].(string)
			source, _ := v["source"].(string)
			if source != "" && source != "local" {
				return m, false
			}
		default:
			return m, false
		}
		if !envName.MatchString(key) || !public(key, secrets) {
			return m, false
		}
		if _, literal := m.Env[key]; !literal {
			m.Env[key] = protocol.SecretField{EnvRef: key}
		}
	}
	for k := range s.Headers {
		if !safeHeader(k) || !public(k, secrets) {
			return m, false
		}
		m.Headers[k] = protocol.SecretField{Secret: true}
	}
	for k, ref := range s.EnvHeaders {
		if !safeHeader(k) || !envName.MatchString(ref) || !public(k, secrets) || !public(ref, secrets) {
			return m, false
		}
		if _, literal := m.Headers[k]; !literal {
			m.Headers[k] = protocol.SecretField{EnvRef: ref}
		}
	}
	if s.Bearer != "" {
		m.Headers["Authorization"] = protocol.SecretField{Secret: true}
	}
	if s.BearerEnv != "" {
		if !envName.MatchString(s.BearerEnv) || !public(s.BearerEnv, secrets) {
			return m, false
		}
		if s.Bearer == "" {
			m.Headers["Authorization"] = protocol.SecretField{EnvRef: s.BearerEnv}
		}
	}
	return m, true
}

func sensitiveArg(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return strings.Contains(arg, "://")
	}
	key, _, _ := strings.Cut(strings.ToLower(arg), "=")
	for _, term := range []string{"token", "password", "secret", "api-key", "apikey", "authorization", "credential"} {
		if strings.Contains(key, term) {
			return true
		}
	}
	return false
}

func safeHeader(k string) bool {
	return k != "" && len(k) <= 256 && !strings.ContainsAny(k, "\x00\r\n: \t")
}
