// SPDX-License-Identifier: Apache-2.0
package connection

import (
	"context"
	"net/url"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/protocol"
)

// Action runs only public login flows and explicitly scoped private logout.
func (m *Manager) Action(ctx context.Context, id string) (protocol.ActionResult, error) {
	ctx, release, err := m.operation(ctx)
	if err != nil {
		return protocol.ActionResult{}, err
	}
	defer release()
	if id == "install_help" {
		return protocol.ActionResult{Kind: protocol.ActionKindURL, URL: installURL}, nil
	}
	if m.cfg.kind != "chatgpt" || (id != "login" && id != "logout_arvel_login") || (id == "logout_arvel_login" && !m.cfg.separate) {
		return protocol.ActionResult{}, unavailable(errAction.Error())
	}
	m.log.Debug("connection action requested", "action", id)
	st := m.status(ctx, true)
	if m.client == nil || st.State == protocol.StateError {
		return protocol.ActionResult{}, unavailable("Codex is unavailable")
	}
	if err := m.cancelLogin(ctx); err != nil {
		return protocol.ActionResult{}, err
	}
	m.loginFailed = false
	if id == "logout_arvel_login" {
		if err := m.client.Call(ctx, "account/logout", nil, &struct{}{}); err != nil {
			return protocol.ActionResult{}, unavailable("Cannot sign out of private Codex login")
		}
		m.revision.Add(1)
		m.remember(m.status(ctx, false), true)
		return protocol.ActionResult{Kind: protocol.ActionKindDone, Message: "Signed out of Arvel login"}, nil
	}
	kind := "chatgpt"
	if m.cfg.remote {
		kind = "chatgptDeviceCode"
	}
	var login schema.LoginResponse
	if err := m.client.Call(ctx, "account/login/start", schema.LoginParams{Type: kind}, &login); err != nil {
		// The vendor may have started an attempt before the caller stopped waiting.
		m.closeClient()
		return protocol.ActionResult{}, unavailable("Cannot start Codex sign-in")
	}
	m.pending = login.LoginID
	result := protocol.ActionResult{Kind: protocol.ActionKindURL, URL: login.AuthURL, Explain: "Finish ChatGPT sign-in in your browser", AwaitCompletion: true}
	if m.cfg.remote {
		result.URL = login.VerificationURL
		result.Explain = "Enter this code to finish ChatGPT sign-in: " + login.UserCode
	}
	if login.Type != kind || login.LoginID == "" || !validURL(result.URL) || (m.cfg.remote && login.UserCode == "") {
		if m.pending == "" {
			m.closeClient()
		} else {
			_ = m.cancelLogin(ctx)
		}
		return protocol.ActionResult{}, unavailable("Codex returned an incomplete sign-in challenge")
	}
	m.redactor.Add(login.AuthURL)
	m.redactor.Add(login.VerificationURL)
	m.redactor.Add(login.UserCode)
	m.revision.Add(1)
	m.remember(m.loginStatus(st), true)
	m.signal()
	return result, nil
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

func (m *Manager) cancelLogin(ctx context.Context) error {
	if m.pending == "" {
		return nil
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := m.client.Call(ctx, "account/login/cancel", schema.CancelLoginParams{LoginID: m.pending}, &result); err != nil || (result.Status != "canceled" && result.Status != "notFound") {
		m.closeClient()
		return unavailable("Cannot cancel previous Codex sign-in")
	}
	m.pending = ""
	return nil
}

func (m *Manager) closeClient() {
	if m.client != nil {
		_ = m.client.Close(context.Background())
	}
	m.reset()
}
