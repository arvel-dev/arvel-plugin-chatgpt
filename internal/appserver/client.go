// SPDX-License-Identifier: Apache-2.0
package appserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/arvel-dev/arvel-plugin-chatgpt/internal/appserver/schema"
	"github.com/arvel-dev/arvel-sdk/logx"
	"github.com/arvel-dev/arvel-sdk/procx"
	"github.com/arvel-dev/arvel-sdk/redact"
)

// ErrExited reports a vendor process that closed its transport.
var ErrExited = errors.New("app-server process exited")

// Options separates transport startup from G2 connection/account selection.
type Options struct {
	Path, Dir         string
	Env               []string // Explicit SDK environment additions; ambient credential families are stripped.
	Info              schema.ClientInfo
	Peer              PeerOptions
	Secrets           []string
	LogWriter         io.Writer
	LogLevel          slog.Leveler
	InitializeTimeout time.Duration
}

// Client owns one process, its pipes and serving worker. It never restarts silently.
type Client struct {
	peer          *Peer
	proc          *procx.Proc
	input         *os.File
	output        *os.File
	cancel        context.CancelFunc
	processCancel context.CancelFunc
	done          chan struct{}
	mu            sync.Mutex
	err           error
	stopRead      func() bool
	stderr        *stderrLines
}

// Start performs initialize/initialized before returning. ctx owns process lifetime.
func Start(ctx context.Context, o Options) (*Client, error) {
	if o.Path == "" {
		return nil, errors.New("app-server requires an absolute executable")
	}
	if o.Info.Name == "" {
		o.Info = schema.ClientInfo{Name: "arvel", Title: "Arvel", Version: "0.1.0"}
	}
	if o.InitializeTimeout <= 0 {
		o.InitializeTimeout = 10 * time.Second
	}
	rd := o.Peer.Redactor
	if rd == nil {
		rd = redact.New()
	}
	for _, secret := range o.Secrets {
		rd.Add(secret)
	}
	log := logx.NewWith("chatgpt-appserver", logx.Options{Writer: o.LogWriter, Level: o.LogLevel, Redactor: rd})
	stderr := &stderrLines{log: log}
	inputR, inputW, err := os.Pipe()
	if err != nil {
		return nil, errors.New("create app-server input")
	}
	outputR, outputW, err := os.Pipe()
	if err != nil {
		_ = inputR.Close()
		_ = inputW.Close()
		return nil, errors.New("create app-server output")
	}
	builder := procx.Env()
	home := ""
	for _, entry := range o.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(key, "CODEX_HOME") {
			home = value
		}
		builder.Set(key, value)
	}
	builder.Strip("OPENAI_", "CODEX_", "RUST_LOG").Set("RUST_LOG", "warn")
	if home != "" {
		builder.Set("CODEX_HOME", home)
	}
	processCtx, processCancel := context.WithCancel(ctx)
	proc, err := procx.Start(processCtx, procx.Spec{Path: o.Path, Args: []string{"app-server"}, Dir: o.Dir, Env: builder.Build(), Stdin: inputR, Stdout: outputW, Stderr: stderr})
	_ = inputR.Close()
	_ = outputW.Close()
	if err != nil {
		processCancel()
		_ = inputW.Close()
		_ = outputR.Close()
		return nil, errors.New("start app-server process")
	}
	serveCtx, cancel := context.WithCancel(ctx)
	o.Peer.Redactor = rd
	o.Peer.Logger = log
	c := &Client{proc: proc, input: inputW, output: outputR, cancel: cancel, processCancel: processCancel, done: make(chan struct{}), stderr: stderr}
	c.peer = NewPeer(outputR, inputW, o.Peer)
	c.stopRead = context.AfterFunc(serveCtx, func() { _ = outputR.Close() })
	log.Debug("app-server process started")
	go func() {
		serveErr := c.peer.Serve(serveCtx)
		_ = inputW.Close()
		_ = outputR.Close()
		processCancel()
		proc.KillTree(100 * time.Millisecond)
		_, _ = proc.Wait()
		c.stopRead()
		cancel()
		stderr.flush()
		c.mu.Lock()
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			c.err = errors.New("app-server transport stopped")
		} else {
			c.err = ErrExited
		}
		c.mu.Unlock()
		log.Debug("app-server process stopped")
		close(c.done)
	}()
	initCtx, initCancel := context.WithTimeout(ctx, o.InitializeTimeout)
	defer initCancel()
	var initialized schema.InitializeResponse
	if err := c.peer.Call(initCtx, "initialize", schema.InitializeParams{ClientInfo: o.Info}, &initialized); err != nil {
		c.cancel()
		<-c.done
		return nil, errors.New("initialize app-server failed")
	}
	if initialized.UserAgent == "" || initialized.CodexHome == "" || initialized.PlatformFamily == "" || initialized.PlatformOS == "" {
		c.cancel()
		<-c.done
		return nil, errors.New("app-server initialization response is incomplete")
	}
	if err := c.peer.Notify("initialized", nil); err != nil {
		c.cancel()
		<-c.done
		return nil, errors.New("notify app-server initialized failed")
	}
	return c, nil
}

// Call preserves process ownership when a single request is cancelled.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	return c.peer.Call(ctx, method, params, result)
}

// Close is idempotent; cleanup continues even if the caller's wait is cancelled.
func (c *Client) Close(ctx context.Context) error {
	c.cancel()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done closes only after owned process/pipe cleanup and serving have finished.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is safe to call concurrently; it is meaningful after Done closes.
func (c *Client) Err() error { c.mu.Lock(); defer c.mu.Unlock(); return c.err }

// WaitResponses waits only for currently pending server replies for this turn.
func (c *Client) WaitResponses(ctx context.Context, threadID, turnID string) error {
	return c.peer.WaitResponses(ctx, threadID, turnID)
}
