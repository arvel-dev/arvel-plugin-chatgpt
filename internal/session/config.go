// SPDX-License-Identifier: Apache-2.0
package session

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/policy"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/sanitize"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

type caller interface {
	Call(context.Context, string, any, any) error
}

type threadConfig struct {
	start    schema.ThreadStartParams
	policy   *policy.Policy
	meta     protocol.SessionMeta
	mcpNames map[string]string
	clean    *sanitize.Cleaner
	children int
}

func setupError(message string) error {
	return protocol.NewError(protocol.ErrProviderError, message, map[string]any{"vendorCode": "session_error"})
}

func buildConfig(params plugin.SessionParams, home string) (threadConfig, error) {
	meta := params.Meta.Normalize()
	p, err := policy.Compile(meta)
	if err != nil {
		return threadConfig{}, setupError("Invalid session permissions or options")
	}
	mcp, names, clean, err := mcpConfig(params)
	if err != nil {
		return threadConfig{}, err
	}

	if meta.Budget != nil && (meta.Budget.MaxUSD != 0 || math.IsNaN(meta.Budget.MaxUSD) || math.IsInf(meta.Budget.MaxUSD, 0)) {
		return threadConfig{}, setupError("Cost budgets require measured usage and are not supported yet")
	}
	if !filepath.IsAbs(params.CWD) || !filepath.IsAbs(home) {
		return threadConfig{}, setupError("Session and provider data directories must be absolute paths")
	}
	cwd, err := filepath.EvalSymlinks(filepath.Clean(params.CWD))
	if err != nil {
		return threadConfig{}, setupError("Session working directory is unavailable")
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return threadConfig{}, setupError("Session working directory is unavailable")
	}
	privateHome, err := filepath.EvalSymlinks(filepath.Clean(home))
	if err != nil {
		return threadConfig{}, setupError("Private provider directory is unavailable")
	}
	if containsPath(cwd, privateHome) || containsPath(privateHome, cwd) {
		return threadConfig{}, setupError("Session directory must not overlap the private provider directory")
	}
	instructions := meta.Instructions
	if meta.Language != "" {
		if instructions != "" {
			instructions += "\n"
		}
		instructions += "Reply to the user in " + meta.Language + "."
	}
	openai := "openai"
	start := schema.ThreadStartParams{
		CWD: cwd, ModelProvider: &openai, ApprovalPolicy: "on-request", Sandbox: p.Sandbox(), Ephemeral: false,
		Config: map[string]any{
			"projects": map[string]any{cwd: map[string]any{"trust_level": "untrusted"}},
			"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": false}, "notify": []any{},
			"agents":                  map[string]any{"enabled": false},
			"sandbox_workspace_write": map[string]any{"network_access": p.NetworkAllowed()},
			"tools":                   map[string]any{"update_plan": map[string]any{"enabled": true}},
		},
	}
	start.Config["mcp_servers"] = mcp
	// Hosted web search does not cross the native command-approval boundary.
	// Without explicit network allow it must not be exposed as a model tool.
	if !p.NetworkAllowed() {
		start.Config["web_search"] = "disabled"
	}
	if meta.Model != "" {
		model := meta.Model
		start.Model = &model
	}
	if instructions != "" {
		start.DeveloperInstructions = &instructions
	}
	return threadConfig{start: start, policy: p, meta: meta, mcpNames: names, clean: clean}, nil
}

func containsPath(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (c threadConfig) resume(thread string) schema.ThreadResumeParams {
	s := c.start
	return schema.ThreadResumeParams{ThreadID: thread, CWD: s.CWD, Model: s.Model, ModelProvider: s.ModelProvider, ApprovalPolicy: s.ApprovalPolicy, Sandbox: s.Sandbox, DeveloperInstructions: s.DeveloperInstructions, Config: s.Config}
}

// preflight deliberately omits cwd: project configuration is atomically excluded
// by the untrusted project override, including definitions added after this read.
// Private-home ownership is required; this recipe is unsafe for a shared home.
func preflight(ctx context.Context, client caller, children ...bool) error {
	var requirements schema.ConfigRequirementsResponse
	if err := client.Call(ctx, "configRequirements/read", nil, &requirements); err != nil {
		return setupError("Cannot verify managed Codex requirements")
	}
	if !bytes.Equal(bytes.TrimSpace(requirements.Requirements), []byte("null")) {
		return setupError("Managed Codex requirements are not supported for agent sessions yet")
	}
	var cfg schema.ConfigReadResponse
	if err := client.Call(ctx, "config/read", schema.ConfigReadParams{IncludeLayers: false}, &cfg); err != nil {
		return setupError("Cannot verify inherited Codex configuration")
	}
	if cfg.Origins == nil {
		return setupError("Codex configuration origins are unavailable")
	}
	if len(cfg.Config.MCP) != 0 {
		return setupError("Inherited MCP servers are not supported in the private provider configuration")
	}
	if cfg.Config.ModelProvider != nil && *cfg.Config.ModelProvider != "openai" {
		return setupError("Custom model providers are not supported in agent sessions")
	}
	for key, origin := range cfg.Origins {
		switch origin.Name.Type {
		case "user", "packagedDefaults", "sessionFlags":
		default:
			return setupError("Managed or unknown Codex configuration layers are not supported for agent sessions yet")
		}
		if len(children) > 0 && children[0] && origin.Name.Type != "packagedDefaults" && ((strings.HasPrefix(key, "agents.") && strings.HasSuffix(key, ".config_file")) || key == "agents" || key == "features" || key == "features.multi_agent_v2" || key == "features.multi_agent_v2.subagent_developer_instructions") {
			return setupError("Inherited subagent instruction overrides are not supported")
		}
	}
	return nil
}
