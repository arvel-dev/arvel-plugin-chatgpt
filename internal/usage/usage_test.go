// SPDX-License-Identifier: Apache-2.0
package usage

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

func ptr[T any](v T) *T { return &v }
func TestSnapshot_WindowsAndUnknownSpend(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	for _, percent := range []int64{-1, 0, 79, 80, 100, 101} {
		s := schema.RateLimitSnapshot{Primary: &schema.RateLimitWindow{UsedPercent: ptr(percent), ResetsAt: ptr(now.Add(time.Hour).Unix()), WindowDurationMins: ptr(int64(10080))}}
		u := Snapshot(s, "chatgpt", now)
		if err := u.Validate(); err != nil {
			t.Fatal(err)
		}
		valid := percent >= 0 && percent <= 100
		if (len(u.Windows) == 1) != valid {
			t.Fatalf("percent %d: %+v", percent, u)
		}
		if valid && (u.Windows[0].ID != "primary" || u.Windows[0].Label != "Week") {
			t.Fatal(u)
		}
		if valid && percent >= 80 && u.Status != protocol.UsageStatusWarning {
			t.Fatal(u)
		}
		s.RateLimitReachedType = ptr("rate_limit_reached")
		if Snapshot(s, "chatgpt", now).Status != protocol.UsageStatusExhausted {
			t.Fatal("missing explicit rejection")
		}
		api := Snapshot(s, "api_key", now)
		if len(api.Windows) != 0 || api.Spend == nil || api.Spend.Source != protocol.UsageSourceUnknown || api.Source != protocol.UsageSourceUnknown {
			t.Fatal(api)
		}
	}
	for _, reset := range []int64{0, -1, math.MaxInt64} {
		u := Snapshot(schema.RateLimitSnapshot{Primary: &schema.RateLimitWindow{UsedPercent: ptr(int64(0)), ResetsAt: ptr(reset)}}, "chatgpt", now)
		if !u.Windows[0].ResetsAt.IsZero() {
			t.Fatal("invalid reset retained")
		}
		if _, err := json.Marshal(u); err != nil {
			t.Fatal(err)
		}
	}
	if Unknown("chatgpt").Source != protocol.UsageSourceUnknown {
		t.Fatal("missing usage is exact")
	}
}
func TestClassify_ExactCodesAndSafeFallback(t *testing.T) {
	now := time.Now().UTC()
	q := QuotaContext{Usage: protocol.Usage{Source: protocol.UsageSourceExact, Windows: []protocol.UsageWindow{{ID: "primary", UsedPercent: 100, ResetsAt: now.Add(time.Hour)}}}, ObservedAt: now}
	for code, want := range map[string]protocol.ErrorCode{"usageLimitExceeded": protocol.ErrRateLimited, "contextWindowExceeded": protocol.ErrContextOverflow, "unauthorized": protocol.ErrAuthRequired, "rateLimitExceeded": protocol.ErrOverloaded, "flexUnavailable": protocol.ErrOverloaded, "serverOverloaded": protocol.ErrOverloaded, "sessionBudgetExceeded": protocol.ErrProviderError, "future": protocol.ErrProviderError} {
		raw, _ := json.Marshal(code)
		d := Classify(schema.TurnError{CodexErrorInfo: raw, Message: "secret-canary"}, "model", q, now)
		if d.Code != want {
			t.Fatal(code, d)
		}
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(d)
		if strings.Contains(string(data), "secret-canary") {
			t.Fatal("raw error leaked")
		}
		if want == protocol.ErrRateLimited && (d.WindowID != "primary" || !d.Retryable) {
			t.Fatal(d)
		}
	}
	for _, message := range []string{`{"error":{"code":"model_not_found"}}`, "text says model_not_found"} {
		d := Classify(schema.TurnError{CodexErrorInfo: json.RawMessage(`"other"`), Message: message}, "model", q, now)
		if (d.Code == protocol.ErrModelUnavailable) != (strings.HasPrefix(message, "{")) {
			t.Fatal(d)
		}
	}
	for _, raw := range []string{`null`, `{"httpConnectionFailed":{"httpStatusCode":429}}`, `"future"`} {
		if Classify(schema.TurnError{CodexErrorInfo: json.RawMessage(raw)}, "m", q, now).Code == protocol.ErrRateLimited {
			t.Fatal("false quota")
		}
	}
	for _, age := range []time.Duration{-time.Second, Freshness + time.Second} {
		stale := q
		stale.ObservedAt = now.Add(-age)
		d := Classify(schema.TurnError{CodexErrorInfo: json.RawMessage(`"usageLimitExceeded"`)}, "m", stale, now)
		if d.WindowID != "" || !d.ResetsAt.IsZero() {
			t.Fatal(d)
		}
	}
	q.Usage.Windows = append(q.Usage.Windows, q.Usage.Windows[0])
	if Classify(schema.TurnError{CodexErrorInfo: json.RawMessage(`"usageLimitExceeded"`)}, "m", q, now).WindowID != "" {
		t.Fatal("ambiguous quota hint")
	}
}
func TestTokens_LastNotCumulativeAndMalformed(t *testing.T) {
	raw := json.RawMessage(`{"last":{"inputTokens":20,"outputTokens":5,"cachedInputTokens":10,"reasoningOutputTokens":2,"totalTokens":25},"total":{"inputTokens":200}}`)
	u := Tokens(raw)
	if u == nil || u.InputTokens != 20 || u.OutputTokens != 5 || u.CacheReadTokens != 10 || u.CostUSD != 0 || u.CostSource != protocol.UsageSourceUnknown {
		t.Fatal(u)
	}
	for _, bad := range []string{`{}`, `{"last":null}`, strings.Replace(string(raw), `"inputTokens":20`, `"inputTokens":-1`, 1), strings.Replace(string(raw), `"totalTokens":25`, `"totalTokens":9223372036854775808`, 1)} {
		if Tokens(json.RawMessage(bad)) != nil {
			t.Fatal("malformed tokens accepted")
		}
	}
}
