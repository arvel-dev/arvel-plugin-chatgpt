// SPDX-License-Identifier: Apache-2.0
package usage

import (
	"encoding/json"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Tokens returns the last turn's valid counters; missing data is not zero usage.
func Tokens(raw json.RawMessage) *protocol.TurnUsage {
	var u schema.ThreadTokenUsage
	if json.Unmarshal(raw, &u) != nil {
		return nil
	}
	var b struct {
		Input     *int64 `json:"inputTokens"`
		Output    *int64 `json:"outputTokens"`
		Cached    *int64 `json:"cachedInputTokens"`
		Reasoning *int64 `json:"reasoningOutputTokens"`
		Total     *int64 `json:"totalTokens"`
	}
	if json.Unmarshal(u.Last, &b) != nil || b.Input == nil || b.Output == nil || b.Cached == nil || b.Reasoning == nil || b.Total == nil {
		return nil
	}
	if *b.Input < 0 || *b.Output < 0 || *b.Cached < 0 || *b.Reasoning < 0 || *b.Total < 0 || *b.Cached > *b.Input || *b.Reasoning > *b.Output {
		return nil
	}
	// Subtraction avoids overflow from adding hostile int64 counters.
	if *b.Input > *b.Total || *b.Output != *b.Total-*b.Input {
		return nil
	}
	return &protocol.TurnUsage{InputTokens: *b.Input, OutputTokens: *b.Output, CacheReadTokens: *b.Cached, CostSource: protocol.UsageSourceUnknown}
}
