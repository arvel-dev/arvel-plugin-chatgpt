// SPDX-License-Identifier: Apache-2.0

// Package appserver owns the stable Codex stdio transport, not account or ACP policy.
package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/jsonl"
	"github.com/arvel-dev/arvel-sdk/jsonrpc"
	"github.com/arvel-dev/arvel-sdk/protocol"
	"github.com/arvel-dev/arvel-sdk/redact"
)

// ErrMethod indicates a method outside the verified stable subset.
var ErrMethod = errors.New("unverified app-server method or parameters")

// Handler answers a vendor request; it must honor cancellation and never wait for shutdown.
type Handler func(context.Context, json.RawMessage) (any, error)

// Notification handles ordered events; it must honor cancellation.
type Notification func(context.Context, json.RawMessage)

// PeerOptions installs handlers before the first vendor message is read.
type PeerOptions struct {
	Requests      map[string]Handler
	Notifications map[string]Notification
	Redactor      *redact.Redactor
	Logger        *slog.Logger
}

// Peer wraps SDK correlation and ordering with the observed Codex envelope dialect.
type Peer struct {
	rpc      *jsonrpc.Peer
	state    *dialect
	redactor *redact.Redactor
	log      *slog.Logger
}

var clientMethods = map[string]bool{"windowsSandbox/readiness": true, "skills/list": true, "thread/read": true, "initialize": true, "account/read": true, "account/login/start": true, "account/login/cancel": true, "account/logout": true, "account/rateLimits/read": true, "model/list": true, "config/read": true, "configRequirements/read": true, "thread/start": true, "thread/resume": true, "thread/unsubscribe": true, "turn/start": true, "turn/interrupt": true}
var serverMethods = []string{"item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval", "mcpServer/elicitation/request"}
var notificationMethods = []string{"mcpServer/startupStatus/updated", "account/login/completed", "account/updated", "account/rateLimits/updated", "thread/started", "thread/status/changed", "turn/started", "turn/completed", "turn/plan/updated", "turn/diff/updated", "item/started", "item/completed", "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded", "item/reasoning/textDelta", "thread/tokenUsage/updated"}

// NewPeer does not start goroutines; the owner must close input to unblock Serve.
func NewPeer(input io.Reader, output io.Writer, o PeerOptions) *Peer {
	rd := o.Redactor
	if rd == nil {
		rd = redact.New()
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	state := &dialect{active: map[string]*pendingResponse{}, handledNotifications: map[string]bool{}}
	rpc := jsonrpc.NewPeer(&liteReader{scanner: jsonl.NewScanner(input, 0), state: state}, &liteWriter{output: output, state: state}, jsonrpc.WithMaxLine(jsonl.DefaultMaxLine+32), jsonrpc.WithDrainTimeout(time.Second), jsonrpc.WithNotificationKey(func(_ string, raw json.RawMessage) string {
		var v struct {
			ThreadID string `json:"threadId"`
		}
		_ = json.Unmarshal(raw, &v)
		return v.ThreadID
	}))
	p := &Peer{rpc: rpc, state: state, redactor: rd, log: log}
	for _, method := range serverMethods {
		handler := o.Requests[method]
		rpc.Handle(method, func(ctx context.Context, raw json.RawMessage) (any, error) {
			if handler == nil {
				switch method {
				case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
					return schema.DecisionResponse{Decision: "decline"}, nil
				case "item/permissions/requestApproval":
					return schema.PermissionsResponse{Permissions: map[string]any{}, Scope: "turn"}, nil
				default:
					return schema.ElicitationResponse{Action: "decline"}, nil
				}
			}
			result, err := handler(ctx, raw)
			encoded, encodeErr := json.Marshal(result)
			if err != nil || encodeErr != nil || forbidden(encoded) {
				return nil, &protocol.RPCError{Code: protocol.CodeInternalError, Message: "app-server request handler failed"}
			}
			return result, nil
		})
	}
	for _, method := range notificationMethods {
		if h := o.Notifications[method]; h != nil {
			state.handledNotifications[method] = true
			rpc.HandleNotification(method, func(ctx context.Context, raw json.RawMessage) {
				defer state.finishNotification(method, raw)
				h(ctx, raw)
			})
		}
	}
	return p
}

// Serve owns SDK dispatch until EOF or cancellation. It never logs raw SDK errors.
func (p *Peer) Serve(ctx context.Context) error {
	defer p.state.fail(ErrExited)
	return p.rpc.Serve(ctx)
}

// WaitResponses confirms actual writes for a fixed snapshot of pending replies.
func (p *Peer) WaitResponses(ctx context.Context, threadID, turnID string) error {
	return p.state.wait(ctx, threadID, turnID)
}

// Call cancels only this wait; late responses are dropped by the SDK.
func (p *Peer) Call(ctx context.Context, method string, params, result any) error {
	if !clientMethods[method] {
		return ErrMethod
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return ErrMethod
	}
	if forbidden(raw) {
		return ErrMethod
	}
	if method == "account/login/start" {
		var v schema.LoginParams
		if json.Unmarshal(raw, &v) != nil || (v.Type != "apiKey" && v.Type != "chatgpt" && v.Type != "chatgptDeviceCode") {
			return ErrMethod
		}
		p.redactor.Add(v.APIKey)
	}
	p.log.Debug("app-server call", "method", method)
	err = p.rpc.Call(ctx, method, params, result)
	if err == nil && method == "account/login/start" {
		encoded, _ := json.Marshal(result)
		var response schema.LoginResponse
		if json.Unmarshal(encoded, &response) == nil {
			p.redactor.Add(response.UserCode)
			p.redactor.Add(response.AuthURL)
		}
	}
	var rpcErr *protocol.RPCError
	if errors.As(err, &rpcErr) {
		return &protocol.RPCError{Code: rpcErr.Code, Message: p.redactor.String(rpcErr.Message)}
	}
	return err
}

// Notify emits only the observed initialized notification.
func (p *Peer) Notify(method string, params any) error {
	if method != "initialized" {
		return ErrMethod
	}
	return p.rpc.Notify(method, params)
}
func forbidden(raw []byte) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			for k, a := range x {
				if k == "experimentalApi" || k == "baseInstructions" || k == "chatgptAuthTokens" || k == "request_permissions_tool" || k == "acceptWithExecpolicyAmendment" || k == "applyNetworkPolicyAmendment" {
					return true
				}
				if ((k == "sandbox" || k == "sandbox_mode") && a == "danger-full-access") || ((k == "approvalPolicy" || k == "approval_policy") && a == "never") || (k == "type" && (a == "chatgptAuthTokens" || a == "dangerFullAccess" || a == "externalSandbox")) || (k == "decision" && (a == "acceptWithExecpolicyAmendment" || a == "applyNetworkPolicyAmendment")) {
					return true
				}
				if visit(a) {
					return true
				}
			}
		case []any:
			for _, a := range x {
				if visit(a) {
					return true
				}
			}
		}
		return false
	}
	return visit(v)
}
