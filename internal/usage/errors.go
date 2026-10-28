// SPDX-License-Identifier: Apache-2.0
package usage

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Classify uses structured discriminants only and emits static safe messages.
func Classify(e schema.TurnError, model string, current QuotaContext, now time.Time) protocol.StopDetail {
	d := protocol.StopDetail{Code: protocol.ErrProviderError, VendorCode: "vendor_error", Message: "Codex could not complete the request"}
	var code string
	_ = json.Unmarshal(e.CodexErrorInfo, &code)
	switch code {
	case "usageLimitExceeded":
		d.Code = protocol.ErrRateLimited
		d.Message = "Codex quota exhausted"
		d.Retryable = true
		if !current.ObservedAt.IsZero() && !current.ObservedAt.After(now) && now.Sub(current.ObservedAt) <= Freshness && current.Usage.Source == protocol.UsageSourceExact && current.Usage.Spend == nil {
			var matched *protocol.UsageWindow
			count := 0
			for i := range current.Usage.Windows {
				w := &current.Usage.Windows[i]
				if w.UsedPercent == 100 {
					count++
					matched = w
				}
			}
			if count == 1 && matched.ID != "" && matched.ResetsAt.After(now) && matched.ResetsAt.Year() >= 0 && matched.ResetsAt.Year() <= 9999 {
				d.WindowID = matched.ID
				d.ResetsAt = matched.ResetsAt
			}
		}
	case "contextWindowExceeded":
		d.Code = protocol.ErrContextOverflow
		d.Message = "Codex context window exceeded"
	case "unauthorized":
		d.Code = protocol.ErrAuthRequired
		d.Message = "Sign in to this connection again"
	case "serverOverloaded", "rateLimitExceeded", "flexUnavailable":
		d.Code = protocol.ErrOverloaded
		d.Message = "Codex is temporarily unavailable"
		d.Retryable = true
	case "other":
		if model != "" && len(e.Message) <= 64<<10 {
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(e.Message), &envelope) == nil && envelope.Error.Code == "model_not_found" {
				d.Code = protocol.ErrModelUnavailable
				d.Model = model
				d.Message = "Model is unavailable for this account"
			}
		}
	}
	if d.Code != protocol.ErrProviderError {
		d.VendorCode = ""
	}
	return d
}

// RPCError classifies a sanitized start failure without inspecting opaque Data.
func RPCError(err error, model string, current QuotaContext, now time.Time) error {
	var rpc *protocol.RPCError
	if !errors.As(err, &rpc) {
		return protocol.NewError(protocol.ErrProviderError, "Codex turn could not be started", map[string]any{"vendorCode": "turn_start_failed"})
	}
	// A recorded structured message can identify an unavailable model. Numeric
	// JSON-RPC codes and arbitrary text cannot establish quota or authentication.
	d := Classify(schema.TurnError{Message: rpc.Message, CodexErrorInfo: json.RawMessage(`"other"`)}, model, current, now)
	fields := map[string]any{"vendorCode": d.VendorCode}
	if d.Model != "" {
		fields["model"] = d.Model
	}
	return protocol.NewError(d.Code, d.Message, fields)
}
