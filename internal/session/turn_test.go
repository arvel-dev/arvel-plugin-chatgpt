// SPDX-License-Identifier: Apache-2.0
package session

import (
	"context"
	"errors"
	"testing"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/plugin"
	"github.com/arvel-dev/arvel-sdk/protocol"
	acp "github.com/coder/acp-go-sdk"
)

func TestTurn_TerminalAndDeliveryFailures(t *testing.T) {
	for _, tc := range []struct {
		name, status, responseID        string
		outputFailure, crash, wantError bool
		want                            acp.StopReason
	}{
		{"completed", "completed", "turn", false, false, false, acp.StopReasonEndTurn},
		{"interrupted", "interrupted", "turn", false, false, false, acp.StopReasonCancelled},
		{"failed", "failed", "turn", false, false, true, ""},
		{"mismatch", "completed", "different", false, false, true, ""},
		{"host-disconnect", "completed", "turn", true, false, true, ""},
		{"missing-terminal", "", "turn", false, true, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, b := sessionFixture(t)
			b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
				if method != "turn/start" {
					return false, nil
				}
				params := p.(schema.TurnStartParams)
				b.client.lifecycle("turn/started", params.ThreadID, "turn", "inProgress")
				b.client.event("item/agentMessage/delta", map[string]string{"threadId": params.ThreadID, "turnId": "turn", "itemId": "item", "delta": "message"})
				if tc.status != "" {
					b.client.lifecycle("turn/completed", params.ThreadID, "turn", tc.status)
				}
				if tc.crash {
					_ = b.client.Close(context.Background())
				}
				return true, encodeResult(map[string]any{"turn": map[string]string{"id": tc.responseID}}, r)
			}
			result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("prompt")}, &testOutput{updateError: tc.outputFailure})
			if (err != nil) != tc.wantError || (!tc.wantError && result.StopReason != tc.want) {
				t.Fatalf("result %+v err %v", result, err)
			}
		})
	}
}

func TestTurn_BusyAndOptionsApplyOnlyToNextTurn(t *testing.T) {
	s, b := sessionFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	b.client.hook = func(ctx context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		params := p.(schema.TurnStartParams)
		if *params.Model != "model" || params.Effort != nil {
			t.Error("running snapshot changed")
		}
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return true, ctx.Err()
		}
		b.client.lifecycle("turn/completed", params.ThreadID, "turn", "completed")
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "turn"}}, r)
	}
	go func() {
		_, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("first")}, &testOutput{})
		finished <- err
	}()
	<-entered
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("concurrent")}, &testOutput{}); err == nil {
		t.Fatal("concurrent direct prompt accepted")
	}
	if _, err := s.SetOption(testContext(t), "model", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetOption(testContext(t), "effort", "low"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		params := p.(schema.TurnStartParams)
		if *params.Model != "other" || params.Effort == nil || *params.Effort != "low" {
			t.Error("next-turn options lost")
		}
		b.client.lifecycle("turn/completed", params.ThreadID, "next", "completed")
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "next"}}, r)
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("next")}, &testOutput{}); err != nil {
		t.Fatal(err)
	}
}

func TestTurn_AttemptedHistoryNeverSilentlyResetsAfterCrash(t *testing.T) {
	s, b := sessionFixture(t)
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("persist")}, &testOutput{}); err != nil {
		t.Fatal(err)
	}
	b.replace()
	b.client.hook = func(_ context.Context, method string, p, r any) (bool, error) {
		if method == "thread/resume" {
			id := p.(schema.ThreadResumeParams).ThreadID
			return true, &protocol.RPCError{Code: protocol.CodeInvalidRequest, Message: "no rollout found for thread id " + id}
		}
		return false, nil
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("recover")}, &testOutput{}); err == nil {
		t.Fatal("persisted history reset")
	}
	if b.client.starts != 0 {
		t.Fatal("replacement conversation created")
	}
}

func TestSession_DuplicateResumeCannotMutateLiveThread(t *testing.T) {
	s, b := sessionFixture(t)
	resume := s.ID()
	params := s.params
	params.Meta.ResumeFrom = &resume
	if _, err := New(testContext(t), b, b.router, params, nil); err == nil {
		t.Fatal("duplicate thread attached")
	}
	for _, method := range b.client.calls {
		if method == "thread/resume" {
			t.Fatal("duplicate resume mutated a live thread")
		}
	}
	if _, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("still usable")}, &testOutput{}); err != nil {
		t.Fatal(err)
	}
}

func TestTurn_CancelBeforeStartReplyStillProcessesApproval(t *testing.T) {
	s, b := sessionFixture(t)
	entered := make(chan struct{})
	hostEntered := make(chan struct{})
	finished := make(chan error, 1)
	b.client.hook = func(ctx context.Context, method string, p, r any) (bool, error) {
		if method != "turn/start" {
			return false, nil
		}
		params := p.(schema.TurnStartParams)
		close(entered)
		raw := []byte(`{"threadId":"` + params.ThreadID + `","turnId":"early","itemId":"command","command":"tool"}`)
		_, err := b.client.opts.Requests["item/commandExecution/requestApproval"](ctx, raw)
		if err != nil {
			return true, err
		}
		return true, encodeResult(map[string]any{"turn": map[string]string{"id": "early"}}, r)
	}
	out := &testOutput{permission: func(ctx context.Context, _ plugin.PermissionRequest) (plugin.PermissionDecision, error) {
		close(hostEntered)
		<-ctx.Done()
		return plugin.PermissionDecision{Cancelled: true}, nil
	}}
	go func() {
		result, err := s.Prompt(testContext(t), []acp.ContentBlock{acp.TextBlock("wait")}, out)
		if err == nil && result.StopReason != acp.StopReasonCancelled {
			err = errors.New("not cancelled")
		}
		finished <- err
	}()
	<-entered
	<-hostEntered
	s.Cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
