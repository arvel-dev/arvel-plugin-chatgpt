// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
)

var errUnassignedSkill = errors.New("unassigned skill bodies reached the synthetic model backend; skill isolation gate failed")

const disposableSkillMarker = "arvel-owned-disposable-skill-home\n"

// disposableSkillHome refuses ordinary developer homes before starting Codex.
// The marker is provisioned only in a fresh, disposable test container. This is
// a probe precondition, never a production HOME override or isolation recipe.
func disposableSkillHome() (string, error) {
	home, err := os.UserHomeDir()
	owned := os.Getenv("ARVEL_DISPOSABLE_SKILL_HOME")
	if err != nil || !filepath.IsAbs(owned) || filepath.Clean(home) != filepath.Clean(owned) {
		return "", errors.New("skill isolation requires an explicitly owned disposable user home")
	}
	info, err := os.Lstat(owned)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("disposable skill home must be a real directory")
	}
	markerPath := filepath.Join(owned, ".arvel-disposable-skill-home")
	info, err = os.Lstat(markerPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("disposable skill home ownership marker is missing or redirected")
	}
	marker, err := os.Open(markerPath)
	if err != nil {
		return "", errors.New("disposable skill home ownership marker is missing")
	}
	defer marker.Close()
	data, err := io.ReadAll(io.LimitReader(marker, 128))
	if err != nil || string(data) != disposableSkillMarker {
		return "", errors.New("disposable skill home ownership marker is invalid")
	}
	return owned, nil
}

func writeIsolationSkill(root, name, body string) (string, error) {
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", errors.New("skill probe refuses to overwrite an existing skill")
	}
	path := filepath.Join(dir, "SKILL.md")
	data := fmt.Sprintf("---\nname: %s\ndescription: Harmless explicitly requested isolation fixture.\n---\n%s\n", name, body)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		return "", err
	}
	return path, nil
}

type skillBodyBackend struct {
	expected atomic.Value
	observed atomic.Bool
}

func (b *skillBodyBackend) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/responses" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "fixture request too large", http.StatusRequestEntityTooLarge)
		return
	}
	expected, _ := b.expected.Load().(string)
	text := "SKILL_BODY_ABSENT"
	if expected != "" && bytes.Contains(body, []byte(expected)) {
		b.observed.Store(true)
		text = "UNASSIGNED_SKILL_BODY_OBSERVED"
	}
	// Only fixed observations enter RPC captures; request bodies and headers
	// remain in memory. This backend never executes a tool or contacts OpenAI.
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range []any{
		map[string]any{"type": "response.created", "response": map[string]any{"id": "skill-isolation-response"}},
		map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "skill-isolation-message", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
		map[string]any{"type": "response.completed", "response": map[string]any{"id": "skill-isolation-response", "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
	} {
		data, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return
		}
	}
}

func (p *peer) skillIsolation(ctx context.Context, scratch string) error {
	home, err := disposableSkillHome()
	if err != nil {
		return err
	}
	cwd := filepath.Join(scratch, "project")
	if err := os.Mkdir(cwd, 0700); err != nil {
		return err
	}
	// A repository marker establishes the owned ancestor skill search boundary.
	if err := os.Mkdir(filepath.Join(scratch, ".git"), 0700); err != nil {
		return err
	}
	roots := []string{filepath.Join(home, ".agents", "skills"), filepath.Join(scratch, ".agents", "skills"), filepath.Join(cwd, ".agents", "skills")}
	var disabled []any
	for i, root := range roots {
		path, err := writeIsolationSkill(root, fmt.Sprintf("arvel-known-%d", i), "KNOWN_SKILL_BODY")
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(path)) // exact owned fixture only
		disabled = append(disabled, map[string]any{"path": path, "enabled": false})
	}
	if _, err := p.request(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}); err != nil {
		return err
	}
	// These names did not exist in the preflight inventory. Exact path/name
	// disable rules cannot cover them. Each body marker is absent from prompts.
	var paths []string
	for i, root := range []string{roots[0], roots[2], roots[1], roots[0]} {
		path, err := writeIsolationSkill(root, fmt.Sprintf("arvel-late-%d", i), fmt.Sprintf("UNASSIGNED_BODY_SENTINEL_%d", i))
		if err != nil {
			return err
		}
		defer os.RemoveAll(filepath.Dir(path))
		paths = append(paths, path)
	}
	if _, err := p.request(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}); err != nil {
		return err
	}
	backend := &skillBodyBackend{}
	server := httptest.NewServer(http.HandlerFunc(backend.serve))
	defer server.Close()
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]any{"trust_level": "untrusted"}, scratch: map[string]any{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": false},
		"agents":   map[string]any{"enabled": false}, "notify": []any{}, "web_search": "disabled",
		"sandbox_workspace_write": map[string]any{"network_access": false},
		"skills":                  map[string]any{"include_instructions": false, "config": disabled},
		"model_providers":         map[string]any{"arvel_fixture": map[string]any{"name": "Arvel skill fixture", "base_url": server.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	start := func() (string, error) {
		raw, err := p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
		if err != nil {
			return "", err
		}
		var result struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Thread.ID == "" {
			return "", errors.New("skill isolation thread missing")
		}
		return result.Thread.ID, nil
	}
	root, err := start()
	if err != nil {
		return err
	}
	sibling, err := start()
	if err != nil {
		return err
	}
	for i, thread := range []string{root, sibling, root, root} {
		if i == 3 {
			if _, err := p.request(ctx, "thread/unsubscribe", map[string]any{"threadId": root}); err != nil {
				return err
			}
			if _, err := p.request(ctx, "thread/resume", map[string]any{"threadId": root, "cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "config": config}); err != nil {
				return err
			}
		}
		backend.expected.Store(fmt.Sprintf("UNASSIGNED_BODY_SENTINEL_%d", i))
		backend.observed.Store(false)
		input := []any{map[string]any{"type": "text", "text": fmt.Sprintf("Use $arvel-late-%d. Reply without tools.", i)}}
		if i == 1 {
			input = []any{map[string]any{"type": "text", "text": "Use the selected skill. Reply without tools."}, map[string]any{"type": "skill", "name": "arvel-late-1", "path": paths[i]}}
		}
		if err := p.fixtureInputTurn(ctx, thread, input); err != nil {
			return err
		}
		if !backend.observed.Load() {
			return errors.New("skill isolation counterexample incomplete; no isolation success is certified")
		}
	}
	return errUnassignedSkill
}

func (p *peer) fixtureInputTurn(ctx context.Context, thread string, input []any) error {
	raw, err := p.request(ctx, "turn/start", map[string]any{"threadId": thread, "input": input})
	if err != nil {
		return err
	}
	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Turn.ID == "" {
		return errors.New("skill isolation turn missing")
	}
	for {
		for _, event := range p.notifications {
			if targetTurnCompleted(event, thread, result.Turn.ID) {
				p.notifications = nil
				return verifyTurn(event.Params, "text", p.cwd)
			}
		}
		p.notifications = nil
		event, err := p.receive(ctx)
		if err != nil {
			return err
		}
		if targetTurnCompleted(event, thread, result.Turn.ID) {
			return verifyTurn(event.Params, "text", p.cwd)
		}
	}
}
