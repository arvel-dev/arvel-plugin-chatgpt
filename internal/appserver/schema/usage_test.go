// SPDX-License-Identifier: Apache-2.0
package schema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	s "github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
)

func TestUsageContracts_RealRecordingsAndMinimumSchema(t *testing.T) {
	counts := map[string]int{}
	for _, scenario := range []string{"limits", "text", "failed"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", scenario, "transcript.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(raw, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var e struct {
				Message struct {
					Method string
					Params json.RawMessage
					Result json.RawMessage
				}
			}
			if err := json.Unmarshal(line, &e); err != nil {
				t.Fatal(err)
			}
			m := e.Message
			switch m.Method {
			case "thread/tokenUsage/updated":
				var v s.TokenUsageNotification
				if err := json.Unmarshal(m.Params, &v); err != nil {
					t.Fatal(err)
				}
				var u s.ThreadTokenUsage
				if json.Unmarshal(v.TokenUsage, &u) != nil || len(u.Last) == 0 {
					t.Fatal("missing turn counters")
				}
				validate(t, "ThreadTokenUsageUpdatedNotification.json", json.RawMessage(m.Params))
				counts[m.Method]++
			case "account/rateLimits/updated":
				var v s.RateLimitsUpdated
				if json.Unmarshal(m.Params, &v) != nil || v.RateLimits.Primary == nil || v.RateLimits.Primary.UsedPercent == nil {
					t.Fatal("missing quota")
				}
				validate(t, "AccountRateLimitsUpdatedNotification.json", json.RawMessage(m.Params))
				counts[m.Method]++
			case "error":
				var v s.ErrorNotification
				if json.Unmarshal(m.Params, &v) != nil || v.Error.Message == "" {
					t.Fatal("missing error")
				}
				validate(t, "ErrorNotification.json", json.RawMessage(m.Params))
				counts[m.Method]++
			}
			if scenario == "limits" && len(m.Result) > 0 && bytes.Contains(m.Result, []byte(`"rateLimits"`)) {
				var response s.RateLimitsResponse
				if json.Unmarshal(m.Result, &response) != nil {
					t.Fatal("bad response")
				}
				var snapshot s.RateLimitSnapshot
				if json.Unmarshal(response.RateLimits, &snapshot) != nil || snapshot.Primary == nil {
					t.Fatal("bad snapshot")
				}
				validate(t, "GetAccountRateLimitsResponse.json", json.RawMessage(m.Result))
				counts["read"]++
			}
		}
	}
	for _, key := range []string{"read", "thread/tokenUsage/updated", "account/rateLimits/updated", "error"} {
		if counts[key] == 0 {
			t.Fatalf("no real evidence for %s", key)
		}
	}
}
