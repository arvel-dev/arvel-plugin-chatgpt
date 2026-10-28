// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/models"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Models returns a complete, validated catalog scoped to this connection's account.
func (m *Manager) Models(ctx context.Context, refresh bool) (protocol.ModelsResult, error) {
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return protocol.ModelsResult{}, err
	}
	defer release()
	if st := m.status(ctx, true); st.State != protocol.StateReady {
		return protocol.ModelsResult{}, unavailable("Connection is not ready for model discovery")
	}
	revision := m.revision.Load()
	if cached, ok := m.catalog.Get(m.deps.now(), revision, refresh); ok {
		m.log.Debug("model catalog cache hit")
		return cached, nil
	}
	hidden := true
	limit := uint32(100)
	params := schema.ModelListParams{IncludeHidden: &hidden, Limit: &limit}
	var catalog []schema.Model
	seen := map[string]bool{}
	for range 100 {
		var page schema.ModelListResponse
		if err := m.client.Call(ctx, "model/list", params, &page); err != nil {
			return protocol.ModelsResult{}, unavailable("Cannot read Codex models")
		}
		if len(catalog)+len(page.Data) > 10000 {
			return protocol.ModelsResult{}, unavailable("Codex model catalog is too large")
		}
		catalog = append(catalog, page.Data...)
		if page.NextCursor == nil {
			break
		}
		if *page.NextCursor == "" || seen[*page.NextCursor] {
			return protocol.ModelsResult{}, unavailable("Codex returned a repeated model cursor")
		}
		seen[*page.NextCursor] = true
		params.Cursor = page.NextCursor
		if len(seen) >= 100 {
			return protocol.ModelsResult{}, unavailable("Codex model pagination exceeded bound")
		}
	}
	billing := protocol.Billing("subscription")
	if m.cfg.kind == "api_key" {
		billing = protocol.Billing("api")
	}
	result, err := models.Map(catalog, billing)
	if err != nil {
		return protocol.ModelsResult{}, unavailable("Codex returned an invalid model catalog")
	}
	for i := range result.Models {
		v := &result.Models[i]
		v.Label = m.redactor.String(v.Label)
		v.Description = m.redactor.String(v.Description)
	}
	if m.revision.Load() != revision {
		return protocol.ModelsResult{}, unavailable("Codex account changed during model discovery; refresh models")
	}
	m.catalog.Put(m.deps.now(), revision, result)
	m.log.Debug("model catalog refreshed", "models", len(result.Models))
	return result, nil
}
