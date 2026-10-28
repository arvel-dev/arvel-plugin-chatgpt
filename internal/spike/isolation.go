// SPDX-License-Identifier: Apache-2.0
package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arvel-dev/arvel-sdk/redact"
)

var errIsolationInherited = errors.New("inherited executable configuration ran")

var errIsolationRace = errors.New("preflight race reproduced: newly inherited MCP executed after config/read despite per-name disable overrides")

// prepareIsolation writes only synthetic configuration owned by this probe.
func prepareIsolation(home, cwd, binary string) error {
	marker := filepath.Join(cwd, "isolation-global.sentinel")
	args, _ := json.Marshal([]string{"isolation-marker", marker})
	notify, _ := json.Marshal([]string{binary, "isolation-marker", filepath.Join(cwd, "isolation-notify.sentinel")})
	global := fmt.Sprintf("notify = %s\n[projects.%s]\ntrust_level = \"trusted\"\n[mcp_servers.global_probe]\ncommand = %s\nargs = %s\n[plugins._default]\nenabled = false\n", notify, tomlString(cwd), tomlString(binary), args)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(global), 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".codex"), 0700); err != nil {
		return err
	}
	args, _ = json.Marshal([]string{"isolation-marker", filepath.Join(cwd, "isolation-project.sentinel")})
	project := fmt.Sprintf("[mcp_servers.project_probe]\ncommand = %s\nargs = %s\n", tomlString(binary), args)
	return os.WriteFile(filepath.Join(cwd, ".codex", "config.toml"), []byte(project), 0600)
}

func tomlString(s string) string { b, _ := json.Marshal(s); return string(b) }

// IsolationMarker is the development-only child used to detect MCP startup.
// The recorder creates its output inside the disposable scratch directory.
func IsolationMarker(path string) error {
	if !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Base(path), "isolation-") || filepath.Ext(path) != ".sentinel" {
		return errors.New("invalid isolation marker path")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString("STARTED\n")
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (p *peer) isolation(ctx context.Context, cwd, scenario string) error {
	race := scenario != "config-isolation"
	forceUntrusted := strings.Contains(scenario, "untrusted-")
	raw, err := p.request(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false})
	if err != nil {
		return err
	}
	var response struct {
		Config struct {
			MCP     map[string]json.RawMessage `json:"mcp_servers"`
			Plugins map[string]json.RawMessage `json:"plugins"`
		} `json:"config"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return errors.New("invalid configuration response")
	}
	if _, ok := response.Config.MCP["global_probe"]; !ok {
		return errors.New("global MCP not visible to preflight")
	}
	if _, ok := response.Config.MCP["project_probe"]; !ok {
		return errors.New("project MCP not visible to preflight")
	}
	if forceUntrusted {
		// User-only config/read avoids creating transport-less disabled entries
		// for project definitions that the trust override will exclude.
		raw, err = p.request(ctx, "config/read", map[string]any{"includeLayers": false})
		if err != nil {
			return err
		}
		response.Config.MCP = nil
		response.Config.Plugins = nil
		if json.Unmarshal(raw, &response) != nil {
			return errors.New("invalid user-only configuration response")
		}
		if _, ok := response.Config.MCP["global_probe"]; !ok {
			return errors.New("global MCP not visible to user-only preflight")
		}
	}
	disabled := map[string]any{}
	for name := range response.Config.MCP {
		disabled[name] = map[string]any{"enabled": false}
	}
	plugins := map[string]any{"_default": map[string]any{"enabled": false}}
	for name := range response.Config.Plugins {
		if name != "_default" {
			plugins[name] = map[string]any{"enabled": false}
		}
	}
	config := map[string]any{"mcp_servers": disabled, "plugins": plugins, "features": map[string]any{"hooks": false, "plugins": false}, "notify": []any{}, "sandbox_workspace_write": map[string]any{"network_access": false}}
	if forceUntrusted {
		config["projects"] = map[string]any{cwd: map[string]any{"trust_level": "untrusted"}}
	}
	switch scenario {
	case "config-isolation-empty":
		config["mcp_servers"] = map[string]any{}
	case "config-isolation-null":
		config["mcp_servers"] = nil
	case "config-isolation-array":
		config["mcp_servers"] = []any{}
	}
	if race {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		args, _ := json.Marshal([]string{"isolation-marker", filepath.Join(cwd, "isolation-late.sentinel")})
		configPath := filepath.Join(cwd, ".codex", "config.toml")
		if scenario == "config-isolation-untrusted-user" {
			configPath = filepath.Join(cwd, "codex-home", "config.toml")
		}
		f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := fmt.Fprintf(f, "\n[mcp_servers.late_probe]\ncommand = %s\nargs = %s\n", tomlString(binary), args)
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	raw, err = p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Thread.ID == "" {
		return errors.New("missing isolation thread ID")
	}
	// Read back through the same stable API. This is evidence gathering, not an
	// assumption that config/read reflects a thread's in-memory configuration.
	if _, err = p.request(ctx, "config/read", map[string]any{"cwd": cwd, "includeLayers": false}); err != nil {
		return err
	}
	for _, name := range []string{"isolation-global.sentinel", "isolation-project.sentinel", "isolation-notify.sentinel"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return errIsolationInherited
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if race {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			if _, err := os.Stat(filepath.Join(cwd, "isolation-late.sentinel")); err == nil {
				return errIsolationRace
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				if forceUntrusted && scenario == "config-isolation-untrusted-project" {
					return errors.New("project-only untrusted candidate: no late marker observed during bounded startup; user config, hooks and persisted resume remain uncertified")
				}
				return errors.New("preflight race marker was not observed; suppression is not certified by this negative result")
			case <-ticker.C:
			}
		}
	}
	// No authenticated turn is sent. Startup alone cannot certify hook completion
	// or persisted resume; retain an explicitly incomplete observation.
	return errors.New("startup-only isolation evidence: hooks, plugin sources, completed turn and persisted resume remain unverified")
}

// Config origins can contain paths in dynamic map keys, not only values.
// This applies solely to owned isolation captures; normal protocol keys remain
// untouched, and JSON numbers retain their exact representation.
func redactIsolationKeys(raw json.RawMessage, rd *redact.Redactor) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("isolation capture contains invalid JSON")
	}
	collision := false
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, child := range x {
				cleanKey := rd.String(k)
				if _, exists := out[cleanKey]; exists {
					collision = true
					return nil
				}
				out[cleanKey] = walk(child)
			}
			return out
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
		}
		return v
	}
	clean := walk(value)
	if clean == nil || collision {
		return nil, errors.New("isolation capture key redaction collision")
	}
	return json.Marshal(clean)
}
