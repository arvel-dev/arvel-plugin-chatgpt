// SPDX-License-Identifier: Apache-2.0

// Package schema defines the stable Codex app-server subset recorded in G0.
package schema

import "encoding/json"

// ClientInfo identifies the plugin during initialization.
type ClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// InitializeParams deliberately excludes experimental capabilities.
type InitializeParams struct {
	ClientInfo ClientInfo `json:"clientInfo"`
}

// InitializeResponse contains the observed server identity.
type InitializeResponse struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

// AccountReadParams never reads vendor credential files.
type AccountReadParams struct {
	RefreshToken bool `json:"refreshToken"`
}

// Account is the stable account identity; email may be absent or null.
type Account struct {
	Type     string  `json:"type"`
	Email    *string `json:"email"`
	PlanType string  `json:"planType,omitempty"`
}

// AccountReadResponse distinguishes an absent account from a signed-in account.
type AccountReadResponse struct {
	Account            *Account `json:"account"`
	RequiresOpenAIAuth bool     `json:"requiresOpenaiAuth"`
}

// LoginParams supports the three approved public login variants only.
type LoginParams struct {
	Type   string `json:"type"`
	APIKey string `json:"apiKey,omitempty"`
}

// LoginResponse is the public browser/device/API-key response union.
type LoginResponse struct {
	Type            string `json:"type"`
	LoginID         string `json:"loginId,omitempty"`
	AuthURL         string `json:"authUrl,omitempty"`
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
}

// CancelLoginParams identifies a private login attempt.
type CancelLoginParams struct {
	LoginID string `json:"loginId"`
}

// ModelListParams controls the stable catalog pagination.
type ModelListParams struct {
	Cursor        *string `json:"cursor,omitempty"`
	Limit         *uint32 `json:"limit,omitempty"`
	IncludeHidden *bool   `json:"includeHidden,omitempty"`
}

// ModelListResponse retains model payloads for the G2 mapper.
type ModelListResponse struct {
	Data       []json.RawMessage `json:"data"`
	NextCursor *string           `json:"nextCursor"`
}

// ThreadStartParams contains only source-verified stable overrides.
type ThreadStartParams struct {
	Model                 *string        `json:"model,omitempty"`
	CWD                   string         `json:"cwd"`
	ApprovalPolicy        string         `json:"approvalPolicy"`
	Sandbox               string         `json:"sandbox"`
	DeveloperInstructions *string        `json:"developerInstructions,omitempty"`
	Config                map[string]any `json:"config,omitempty"`
	Ephemeral             bool           `json:"ephemeral"`
}

// ThreadResumeParams selects an already persisted thread.
type ThreadResumeParams struct {
	ThreadID string `json:"threadId"`
}

// UserInput covers text and stable image/skill inputs without experimental auth.
type UserInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// TurnStartParams applies model and effort overrides to one vendor thread.
type TurnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
	Effort   *string     `json:"effort,omitempty"`
	Model    *string     `json:"model,omitempty"`
}

// TurnInterruptParams cancels the exact parent turn, never an arbitrary child.
type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// DecisionResponse is the command/file-specific approval response.
type DecisionResponse struct {
	Decision string `json:"decision"`
}

// PermissionsResponse denies by returning an empty profile with turn scope.
type PermissionsResponse struct {
	Permissions map[string]any `json:"permissions"`
	Scope       string         `json:"scope"`
}

// ElicitationResponse declines an unhandled MCP form without persisting policy.
type ElicitationResponse struct {
	Action  string `json:"action"`
	Content any    `json:"content"`
}

// TurnNotification identifies the source thread and opaque turn for G3 mapping.
type TurnNotification struct {
	ThreadID string          `json:"threadId"`
	Turn     json.RawMessage `json:"turn"`
}

// ItemNotification retains exact item variants for future schema-backed mapping.
type ItemNotification struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Item     json.RawMessage `json:"item"`
}

// RateLimitsResponse preserves exact window and discriminant shapes for G4.
type RateLimitsResponse struct {
	RateLimits          json.RawMessage            `json:"rateLimits"`
	RateLimitsByLimitID map[string]json.RawMessage `json:"rateLimitsByLimitId"`
}

// TokenUsageBreakdown contains counters only; no cost is inferred.
type TokenUsageBreakdown struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

// TokenUsageNotification attributes counters to the exact vendor turn.
type TokenUsageNotification struct {
	ThreadID   string          `json:"threadId"`
	TurnID     string          `json:"turnId"`
	TokenUsage json.RawMessage `json:"tokenUsage"`
}
