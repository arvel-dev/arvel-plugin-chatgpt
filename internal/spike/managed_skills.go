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

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/skillset"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func (p *peer) managedSkills(ctx context.Context, root string) error {
	if _, err := disposableSkillHome(); err != nil {
		return err
	}
	cwd, source, private := filepath.Join(root, "workspace"), filepath.Join(root, "source"), filepath.Join(root, "provider")
	for _, path := range []string{cwd, source, private} {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: original-name\ndescription: Use for all task summaries.\n---\nMANAGED_SKILL_OWNED_BODY\n"), 0600); err != nil {
		return err
	}
	collision := filepath.Join(cwd, ".agents", "skills", "host-name")
	if err := os.MkdirAll(collision, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(collision, "SKILL.md"), []byte("---\nname: host-name\ndescription: Unmanaged harmless collision.\n---\nUNMANAGED_BODY\n"), 0600); err != nil {
		return err
	}
	manager := skillset.New(private)
	defer manager.Close()
	refs := []protocol.SkillRef{{Name: "host-name", Path: source, Invocation: protocol.SkillInvocationManual}}
	catalog, firstRelease, err := manager.Acquire(ctx, cwd, refs)
	if err != nil {
		return err
	}
	_, secondRelease, err := manager.Acquire(ctx, cwd, refs)
	if err != nil {
		return err
	}
	defer secondRelease()
	firstRelease()
	if len(catalog) != 1 || catalog[0].Name != "arvel-host-name" {
		return errors.New("owned alias missing")
	}
	var phase atomic.Int32
	var implicitClean, explicitLoaded atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "fixture request bound", http.StatusRequestEntityTooLarge)
			return
		}
		loaded := strings.Contains(string(body), "MANAGED_SKILL_OWNED_BODY")
		marker := "MANAGED_IMPLICIT_CLEAN"
		if phase.Load() == 1 {
			implicitClean.Store(!loaded)
		} else {
			explicitLoaded.Store(loaded)
			marker = "MANAGED_EXPLICIT_LOADED"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]string{"id": "managed-response"}},
			map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "managed-message", "content": []any{map[string]string{"type": "output_text", "text": marker}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "managed-response", "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
		} {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
	}))
	defer backend.Close()
	raw, err := p.request(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true})
	if err != nil {
		return err
	}
	var listed struct {
		Data []struct {
			Skills []struct {
				Name, Path string
				Enabled    bool
			}
		}
	}
	if json.Unmarshal(raw, &listed) != nil {
		return errors.New("managed catalog invalid")
	}
	found := false
	for _, entry := range listed.Data {
		for _, skill := range entry.Skills {
			if skill.Name == catalog[0].Name && skill.Path == filepath.Join(catalog[0].Path, "SKILL.md") && skill.Enabled {
				found = true
			}
		}
	}
	if !found {
		return errors.New("managed alias not discovered by native CLI")
	}
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]string{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": false}, "agents": map[string]any{"enabled": false},
		"notify": []any{}, "web_search": "disabled", "mcp_servers": map[string]any{}, "sandbox_workspace_write": map[string]any{"network_access": false},
		"model_providers": map[string]any{"arvel_fixture": map[string]any{"name": "Owned managed skill fixture", "base_url": backend.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	raw, err = p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var response struct{ Thread struct{ ID string } }
	if json.Unmarshal(raw, &response) != nil || response.Thread.ID == "" {
		return errors.New("managed thread identity missing")
	}
	phase.Store(1)
	if err := p.fixtureInputTurn(ctx, response.Thread.ID, []any{map[string]string{"type": "text", "text": "Return a short summary without tools."}}); err != nil {
		return err
	}
	phase.Store(2)
	if err := p.fixtureInputTurn(ctx, response.Thread.ID, []any{map[string]string{"type": "text", "text": "Use the explicitly selected managed skill."}, map[string]string{"type": "skill", "name": catalog[0].Name, "path": filepath.Join(catalog[0].Path, "SKILL.md")}}); err != nil {
		return err
	}
	if !implicitClean.Load() || !explicitLoaded.Load() {
		return errors.New("managed manual invocation behavior not observed")
	}
	if _, err := p.request(ctx, "thread/unsubscribe", map[string]string{"threadId": response.Thread.ID}); err != nil {
		return err
	}
	secondRelease()
	if _, err := os.Stat(catalog[0].Path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("managed last release retained copy")
	}
	for _, path := range []string{filepath.Join(source, "SKILL.md"), filepath.Join(collision, "SKILL.md")} {
		if _, err := os.Stat(path); err != nil {
			return errors.New("managed cleanup altered unowned source")
		}
	}
	return nil
}
