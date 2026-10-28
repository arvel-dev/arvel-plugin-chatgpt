// SPDX-License-Identifier: Apache-2.0
package schema

import "encoding/json"

// RateLimitWindow distinguishes missing measurements from a measured zero.
type RateLimitWindow struct {
	UsedPercent        *int64 `json:"usedPercent"`
	ResetsAt           *int64 `json:"resetsAt"`
	WindowDurationMins *int64 `json:"windowDurationMins"`
}

// RateLimitSnapshot is the stable historical single-bucket quota view.
type RateLimitSnapshot struct {
	Primary              *RateLimitWindow `json:"primary"`
	Secondary            *RateLimitWindow `json:"secondary"`
	RateLimitReachedType *string          `json:"rateLimitReachedType"`
}

// RateLimitsUpdated is a sparse notification; callers refetch authoritative state.
type RateLimitsUpdated struct {
	RateLimits RateLimitSnapshot `json:"rateLimits"`
}

// ThreadTokenUsage separates the latest turn from cumulative thread totals.
type ThreadTokenUsage struct {
	Last               json.RawMessage `json:"last"`
	Total              json.RawMessage `json:"total"`
	ModelContextWindow *int64          `json:"modelContextWindow"`
}

// TurnError retains the discriminant without trusting external text as a code.
type TurnError struct {
	Message        string          `json:"message"`
	CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
}

// ErrorNotification is advisory; only turn/completed ends a turn.
type ErrorNotification struct {
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	Error     TurnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
}
