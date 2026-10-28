// SPDX-License-Identifier: Apache-2.0
package models

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func TestMap_RecordedCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "models", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var e struct {
			Message struct {
				Result json.RawMessage `json:"result"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &e) != nil || len(e.Message.Result) == 0 {
			continue
		}
		var page schema.ModelListResponse
		if json.Unmarshal(e.Message.Result, &page) != nil || len(page.Data) == 0 {
			continue
		}
		v, err := Map(page.Data, protocol.Billing("subscription"))
		if err != nil {
			t.Fatal(err)
		}
		if v.Default != "gpt-6.1-sol" {
			t.Fatalf("wrong executable default: %s", v.Default)
		}
		for _, m := range v.Models {
			for _, effort := range m.Efforts {
				if !effort.Valid() {
					t.Fatalf("bad effort %s", effort)
				}
			}
		}
		return
	}
	t.Fatal("no recorded model catalog")
}
func TestMap_HiddenDefaultAndEfforts(t *testing.T) {
	catalog := []schema.Model{
		{Model: "default", DisplayName: "Default", Hidden: true, IsDefault: true, DefaultReasoningEffort: "ultra", SupportedReasoningEfforts: []schema.ReasoningEffort{{ReasoningEffort: "none"}, {ReasoningEffort: "minimal"}, {ReasoningEffort: "max"}, {ReasoningEffort: "max"}, {ReasoningEffort: "ultra"}}},
		{Model: "hidden", DisplayName: "Hidden", Hidden: true},
		{Model: "visible", DisplayName: "Visible", InputModalities: []string{"text"}},
	}
	v, err := Map(catalog, protocol.Billing("api"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Models) != 2 || v.Default != "default" || len(v.Models[0].Efforts) != 1 || v.Models[0].Efforts[0] != protocol.EffortMax || v.Models[0].DefaultEffort != "" || len(v.Models[0].Inputs) != 2 {
		t.Fatalf("mapping %+v", v)
	}
	for _, bad := range [][]schema.Model{nil, {{Model: "x", DisplayName: "X"}}, {{Model: "x", DisplayName: "X", IsDefault: true}, {Model: "x", DisplayName: "X"}}, {{Model: "a", DisplayName: "A", IsDefault: true}, {Model: "b", DisplayName: "B", IsDefault: true}}} {
		if _, err := Map(bad, protocol.Billing("api")); err == nil {
			t.Fatal("accepted invalid catalog")
		}
	}
}
func TestCache_TTLRevisionRefreshAndDefensiveCopies(t *testing.T) {
	now := time.Now()
	var c Cache
	v := protocol.ModelsResult{Default: "x", Models: []protocol.Model{{ID: "x", Inputs: []string{"text"}, Efforts: []protocol.Effort{protocol.EffortLow}}}}
	c.Put(now, 1, v)
	v.Models[0].ID = "mutated"
	v.Models[0].Inputs[0] = "mutated"
	got, ok := c.Get(now.Add(5*time.Hour), 1, false)
	if !ok || got.Models[0].ID != "x" || got.Models[0].Inputs[0] != "text" {
		t.Fatal("cache alias")
	}
	got.Models[0].Efforts[0] = protocol.EffortMax
	got, _ = c.Get(now, 1, false)
	if got.Models[0].Efforts[0] != protocol.EffortLow {
		t.Fatal("return alias")
	}
	for _, tc := range []struct {
		now     time.Time
		rev     uint64
		refresh bool
	}{{now.Add(6 * time.Hour), 1, false}, {now, 2, false}, {now, 1, true}, {now.Add(-time.Second), 1, false}} {
		if _, ok := c.Get(tc.now, tc.rev, tc.refresh); ok {
			t.Fatal("stale cache hit")
		}
	}
}
