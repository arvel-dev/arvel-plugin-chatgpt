// SPDX-License-Identifier: Apache-2.0

package spike

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
)

// inlineImage measures stable data-URL input using an owned harmless image and
// a loopback backend. No account/model/API call or host sandbox is certified.
func (p *peer) inlineImage(ctx context.Context, cwd string) error {
	if _, err := disposableSkillHome(); err != nil {
		return err
	}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return err
	}
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())
	var observed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			http.Error(w, "oversized fixture request", http.StatusRequestEntityTooLarge)
			return
		}
		var request struct {
			Input []struct {
				Content []struct {
					Type string `json:"type"`
					URL  string `json:"image_url"`
				} `json:"content"`
			} `json:"input"`
		}
		if json.Unmarshal(body, &request) != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		for _, item := range request.Input {
			for _, content := range item.Content {
				if content.Type == "input_image" && content.URL == imageURL {
					observed.Store(true)
				}
			}
		}
		text := "IMAGE_INPUT_ABSENT"
		if observed.Load() {
			text = "IMAGE_INPUT_OBSERVED"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "image-fixture"}},
			map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "image-message", "content": []any{map[string]any{"type": "output_text", "text": text}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "image-fixture", "usage": map[string]any{"input_tokens": 0, "input_tokens_details": nil, "output_tokens": 0, "output_tokens_details": nil, "total_tokens": 0}}},
		} {
			data, _ := json.Marshal(event)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	config := map[string]any{
		"projects": map[string]any{cwd: map[string]any{"trust_level": "untrusted"}},
		"features": map[string]any{"hooks": false, "plugins": false, "multi_agent": false, "multi_agent_v2": false},
		"agents":   map[string]any{"enabled": false}, "notify": []any{}, "web_search": "disabled",
		"sandbox_workspace_write": map[string]any{"network_access": false},
		"model_providers":         map[string]any{"arvel_fixture": map[string]any{"name": "Arvel inline image fixture", "base_url": server.URL, "wire_api": "responses", "requires_openai_auth": false, "request_max_retries": 0, "stream_max_retries": 0}},
	}
	raw, err := p.request(ctx, "thread/start", map[string]any{"cwd": cwd, "model": "gpt-5.6-luna", "modelProvider": "arvel_fixture", "approvalPolicy": "on-request", "sandbox": "read-only", "ephemeral": false, "config": config})
	if err != nil {
		return err
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Thread.ID == "" {
		return errors.New("image fixture thread missing")
	}
	if err := p.fixtureInputTurn(ctx, result.Thread.ID, []any{map[string]any{"type": "text", "text": "Reply without tools."}, map[string]any{"type": "image", "url": imageURL}}); err != nil {
		return err
	}
	if !observed.Load() {
		return errors.New("inline image did not reach synthetic backend")
	}
	return nil
}
