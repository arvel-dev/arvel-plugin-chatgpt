// SPDX-License-Identifier: Apache-2.0
package spike

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// preparePrivateIsolation owns a fresh home: authentication/configuration is
// never imported from the person's normal Codex installation.
func preparePrivateIsolation(home, cwd, binary string) error {
	if err := prepareIsolation(home, cwd, binary); err != nil {
		return err
	}
	notify, _ := json.Marshal([]string{binary, "isolation-marker", filepath.Join(cwd, "isolation-notify.sentinel")})
	content := fmt.Sprintf("notify = %s\n[projects.%s]\ntrust_level = \"trusted\"\n", notify, tomlString(cwd))
	return os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0600)
}

// privateIsolation uses the real CLI and a fixed loopback Responses fixture.
// The backend is synthetic: no account, real OpenAI API or spend is involved.
func (p *peer) privateIsolation(ctx context.Context, cwd string) error {
	requirements, err := p.request(ctx, "configRequirements/read", nil)
	if err != nil {
		return err
	}
	var managed struct {
		Requirements json.RawMessage `json:"requirements"`
	}
	if json.Unmarshal(requirements, &managed) != nil || string(managed.Requirements) != "null" {
		return errors.New("private isolation requires an unmanaged fixture environment")
	}
	raw, err := p.request(ctx, "config/read", map[string]any{"includeLayers": false})
	if err != nil {
		return err
	}
	var cfg struct {
		Config struct {
			MCP map[string]json.RawMessage `json:"mcp_servers"`
		} `json:"config"`
	}
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Config.MCP) != 0 {
		return errors.New("private isolation requires an owned home without inherited MCP")
	}
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		// Inspect only tool types in bounded synthetic input; prompts and auth
		// headers never enter logs or captures.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "fixture request too large", http.StatusRequestEntityTooLarge)
			return
		}
		var request struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		for _, tool := range request.Tools {
			if strings.HasPrefix(tool.Type, "web_search") {
				http.Error(w, "web search escaped the disabled baseline", http.StatusBadRequest)
				return
			}
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "isolation-response"}},
			map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "isolation-message", "content": []any{map[string]any{"type": "output_text", "text": "ISOLATION_PONG"}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "isolation-response", "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
		} {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
	}))
	defer backend.Close()
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]any{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false}, "notify": []any{},
		"sandbox_workspace_write": map[string]any{"network_access": false},
		"web_search":              "disabled",
		"model_providers":         map[string]any{"arvel_fixture": map[string]any{"name": "Arvel isolation fixture", "base_url": backend.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	// Add an unassigned definition after preflight. It must remain excluded by
	// project trust, not merely disappear from an earlier enumeration snapshot.
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	args, _ := json.Marshal([]string{"isolation-marker", filepath.Join(cwd, "isolation-late.sentinel")})
	f, err := os.OpenFile(filepath.Join(cwd, ".codex", "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
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
	raw, err = p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Thread.ID == "" {
		return errors.New("private isolation thread missing")
	}
	if err := p.isolationTurn(ctx, started.Thread.ID); err != nil {
		return err
	}
	if _, err = p.request(ctx, "thread/unsubscribe", map[string]any{"threadId": started.Thread.ID}); err != nil {
		return err
	}
	if _, err = p.request(ctx, "thread/resume", map[string]any{"threadId": started.Thread.ID, "cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "config": config}); err != nil {
		return err
	}
	if err := p.isolationTurn(ctx, started.Thread.ID); err != nil {
		return err
	}
	if calls.Load() != 2 {
		return errors.New("private isolation did not complete both synthetic backend turns")
	}
	for _, name := range []string{"isolation-global.sentinel", "isolation-project.sentinel", "isolation-late.sentinel", "isolation-notify.sentinel"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
			return errIsolationInherited
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (p *peer) isolationTurn(ctx context.Context, threadID string) error {
	raw, err := p.request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": "Reply ISOLATION_PONG without tools."}}})
	if err != nil {
		return err
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Turn.ID == "" {
		return errors.New("private isolation turn missing")
	}
	for {
		for _, event := range p.notifications {
			if targetTurnCompleted(event, threadID, result.Turn.ID) {
				p.notifications = nil
				return verifyTurn(event.Params, "text", p.cwd)
			}
		}
		p.notifications = nil
		event, err := p.receive(ctx)
		if err != nil {
			return err
		}
		if targetTurnCompleted(event, threadID, result.Turn.ID) {
			return verifyTurn(event.Params, "text", p.cwd)
		}
	}
}
